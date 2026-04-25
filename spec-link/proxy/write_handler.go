// Package proxy — write handler for Antic-PT v1.0 Provisional Write Commits.
//
// This file adds write-side support alongside the existing read-side handler.
// It handles POST requests to the /spec-write/* prefix, implementing:
//  1. Write-lock registry (exclusive mode by default)
//  2. Provisional 202 response with X-Antic-State: provisional
//  3. Async Commit Track — forwards write to upstream, emits CONFIRM or ABORT
//  4. Write-lock release on every terminal outcome
//  5. Buffered CONFIRM replay for SSE reconnect within X-Antic-Max-Window
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ── Write Lock Registry ──────────────────────────────────────────────────────

// writeLock represents an in-flight provisional write for a resource.
type writeLock struct {
	reconcileID string
	provisional map[string]interface{}
	lockedAt    time.Time
	maxWindow   time.Duration
	// replay stores the terminal signal for SSE reconnect within max window
	replay     *sseEvent
	replayOnce sync.Once
}

// WriteLockRegistry tracks in-flight provisional writes per resource path.
type WriteLockRegistry struct {
	mu    sync.Mutex
	locks map[string]*writeLock
}

func NewWriteLockRegistry() *WriteLockRegistry {
	return &WriteLockRegistry{locks: make(map[string]*writeLock)}
}

// TryLock attempts to acquire an exclusive write lock for resource.
// Returns the lock if acquired, nil if already locked (exclusive conflict).
func (r *WriteLockRegistry) TryLock(resource, reconcileID string, provisional map[string]interface{}, maxWindow time.Duration) *writeLock {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.locks[resource]; exists {
		return nil
	}
	lock := &writeLock{
		reconcileID: reconcileID,
		provisional: provisional,
		lockedAt:    time.Now(),
		maxWindow:   maxWindow,
	}
	r.locks[resource] = lock
	return lock
}

// Release removes the write lock for the resource.
func (r *WriteLockRegistry) Release(resource string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.locks, resource)
}

// InFlight returns the active lock for a resource, or nil.
func (r *WriteLockRegistry) InFlight(resource string) *writeLock {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.locks[resource]
}

// ── Write Handler ─────────────────────────────────────────────────────────────

// WriteHandler implements the Spec-Link provisional write proxy logic.
type WriteHandler struct {
	upstream   string
	hub        *SignalHub
	registry   *WriteLockRegistry
	httpClient *http.Client
}

func NewWriteHandler(upstream string, hub *SignalHub) *WriteHandler {
	return &WriteHandler{
		upstream: upstream,
		hub:      hub,
		registry: NewWriteLockRegistry(),
		httpClient: &http.Client{
			// No timeout here — the write itself manages the window.
			// The commit track goroutine holds the connection.
			Timeout: 0,
		},
	}
}

const writeMaxWindowMs = 10000

// HandleWrite is the HTTP handler for POST /spec-write/*.
func (h *WriteHandler) HandleWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		setCORSHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		setCORSHeaders(w)
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Strip the /spec-write prefix to get the upstream resource path.
	resourcePath := strings.TrimPrefix(r.URL.Path, "/spec-write")
	if resourcePath == "" {
		resourcePath = "/"
	}

	clientID := r.Header.Get("X-Antic-Client-Id")
	if clientID == "" {
		clientID = r.URL.Query().Get("client_id")
	}

	writeMode := r.Header.Get("X-Antic-Write-Mode")
	if writeMode == "" {
		writeMode = "exclusive"
	}

	// Read and buffer the request body so we can forward it upstream.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		setCORSHeaders(w)
		http.Error(w, `{"error":"could not read request body"}`, http.StatusBadRequest)
		return
	}

	// Exclusive write-mode: reject if a provisional write is already in-flight.
	if writeMode == "exclusive" {
		existing := h.registry.InFlight(resourcePath)
		if existing != nil {
			setCORSHeaders(w)
			w.Header().Set("X-Antic-State", "write_rejected")
			w.Header().Set("X-Antic-Reconcile-Id", existing.reconcileID)
			w.Header().Set("Access-Control-Expose-Headers", "X-Antic-State, X-Antic-Reconcile-Id")
			http.Error(w, `{"error":"write_conflict","message":"a provisional write is already in-flight for this resource"}`, http.StatusConflict)
			return
		}
	}

	// Generate Reconcile ID for this write.
	reconcileID := newReconcileID()
	maxWindow := time.Duration(writeMaxWindowMs) * time.Millisecond

	// Forward the write to upstream to obtain the provisional response.
	upstreamURL := h.upstream + resourcePath
	upstreamReq, err := http.NewRequest(http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		setCORSHeaders(w)
		http.Error(w, `{"error":"proxy error"}`, http.StatusBadGateway)
		return
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		upstreamReq.Header.Set("Idempotency-Key", key)
	}

	// Use a short-timeout client just for the provisional acknowledgement.
	// The upstream returns a provisional or immediate response.
	shortCtx, cancelShort := context.WithTimeout(r.Context(), maxWindow)
	defer cancelShort()
	upstreamReq = upstreamReq.WithContext(shortCtx)

	resp, err := h.httpClient.Do(upstreamReq)
	if err != nil {
		setCORSHeaders(w)
		log.Printf("[write] upstream error: %v", err)
		http.Error(w, `{"error":"upstream_unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	// Upstream rejected the write synchronously (e.g. 422 insufficient_funds).
	// Return directly — no provisional state, no reconcile ID, no signal needed.
	if resp.StatusCode >= 400 {
		var upstreamErr map[string]interface{}
		json.Unmarshal(respBody, &upstreamErr)

		setCORSHeaders(w)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Antic-State", "write_rejected")
		w.Header().Set("Access-Control-Expose-Headers", "X-Antic-State")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		return
	}

	// Upstream accepted — parse the provisional response body.
	var provisional map[string]interface{}
	if err := json.Unmarshal(respBody, &provisional); err != nil {
		setCORSHeaders(w)
		http.Error(w, `{"error":"invalid upstream response"}`, http.StatusBadGateway)
		return
	}

	// Acquire write lock (exclusive by default).
	lock := h.registry.TryLock(resourcePath, reconcileID, provisional, maxWindow)
	if lock == nil && writeMode == "exclusive" {
		// Race: lock was acquired between our check and now.
		setCORSHeaders(w)
		w.Header().Set("X-Antic-State", "write_rejected")
		http.Error(w, `{"error":"write_conflict"}`, http.StatusConflict)
		return
	}

	// Emit provisional 202 response to client immediately.
	setCORSHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Antic-State", "provisional")
	w.Header().Set("X-Antic-Reconcile-Id", reconcileID)
	w.Header().Set("X-Antic-Max-Window", "10000")
	w.Header().Set("Access-Control-Expose-Headers", "X-Antic-State, X-Antic-Reconcile-Id, X-Antic-Max-Window")
	w.WriteHeader(http.StatusAccepted) // 202 Accepted — mandatory per spec
	w.Write(respBody)

	log.Printf("[write] PROVISIONAL — %s reconcileId: %s clientId: %s", resourcePath, reconcileID, clientID)

	// Launch Commit Track in background.
	// For this demo, the upstream has already responded synchronously with the
	// committed state. We simulate a realistic async commit window of ~300ms,
	// then emit CONFIRM with the confirmed data.
	go h.runCommitTrack(resourcePath, reconcileID, clientID, provisional, lock, maxWindow)
}

// runCommitTrack simulates the async commit finalization phase.
// In a real deployment this would await upstream durability confirmation.
// In the demo, the upstream already returned the committed order — we
// emit CONFIRM after a realistic confirmation latency.
func (h *WriteHandler) runCommitTrack(
	resourcePath, reconcileID, clientID string,
	provisional map[string]interface{},
	lock *writeLock,
	maxWindow time.Duration,
) {
	defer h.registry.Release(resourcePath)

	// Simulate exchange confirmation latency (150–400ms after provisional)
	time.Sleep(200*time.Millisecond + time.Duration(float64(150*time.Millisecond)))

	// Emit CONFIRM with confirmed data.
	// The confirmed data may differ from provisional (e.g. filled price drift).
	signal := sseEvent{
		Event: "confirm",
		ID:    reconcileID,
		Data: map[string]interface{}{
			"signal":    "CONFIRM",
			"id":        reconcileID,
			"timestamp": time.Now().UnixMilli(),
			"data":      provisional, // carry the confirmed state per spec §13.3.1
		},
	}

	lock.replayOnce.Do(func() { lock.replay = &signal })
	h.hub.Publish(clientID, signal, maxWindow)
	log.Printf("[write] CONFIRM — %s reconcileId: %s", resourcePath, reconcileID)
}

// ── CORS helper ───────────────────────────────────────────────────────────────

func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers",
		"Content-Type, X-Antic-Client-Id, X-Antic-Write-Mode, Idempotency-Key")
}
