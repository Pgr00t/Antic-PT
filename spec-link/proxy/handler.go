// Package proxy contains the core Spec-Link dual-track logic.
//
// Industry-standard dual-track architecture:
//
//	HandleSpec forks one incoming GET request into two concurrent execution paths:
//	  • Fast Track  – Reads the State-Vault and emits a "speculative" SSE event.
//	  • Formal Track – Proxies to the upstream API and emits reconciliation signals.
package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"antic-pt/spec-link/config"
	"antic-pt/spec-link/intent"
	"antic-pt/spec-link/vault"
)

// sseEvent represents a single Server-Sent Event message sent to the client.
type sseEvent struct {
	// Event is the SSE event type (e.g., "speculative", "confirm", "patch").
	Event string
	// ID is the resource version associated with this event.
	ID string
	// Data is the JSON-serializable payload.
	Data any
	// Terminal signifies if this is the final event in the stream.
	Terminal bool
}

// Handler implements the Spec-Link HTTP proxy logic.
type Handler struct {
	cfg        *config.SpecLinkConfig
	vault      vault.Vault
	scorer     *intent.Scorer
	httpClient *http.Client
}

// NewHandler initializes a new Handler with the provided configuration, vault storage, and scorer.
func NewHandler(cfg *config.SpecLinkConfig, v vault.Vault, scorer *intent.Scorer) *Handler {
	return &Handler{
		cfg:    cfg,
		vault:  v,
		scorer: scorer,
		httpClient: &http.Client{
			Timeout: cfg.FormalTrackTimeout() + 2*time.Second,
		},
	}
}

// HandleSpec manages dual-track execution for GET requests
// and optimistic mutation handling for POST/PUT/PATCH/DELETE.
func (h *Handler) HandleSpec(w http.ResponseWriter, r *http.Request) {
	// Handle CORS preflight.
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Antic-Intent, X-Antic-Version")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Route mutating methods to the optimistic mutation handler.
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		if !h.cfg.Mutations.Enabled {
			http.Error(w, `{"error":"mutations are disabled","code":405}`, http.StatusMethodNotAllowed)
			return
		}
		h.HandleMutation(w, r)
		return
	case http.MethodGet:
		// Fall through to dual-track read logic below.
	default:
		http.Error(w, `{"error":"method not allowed","code":405}`, http.StatusMethodNotAllowed)
		return
	}

	// 1. Resolve resource and ID from the request path.
	resource, id, err := parsePath(r.URL.Path, h.cfg.Prefix)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	// 2. Determine the intent mode based on headers or configuration.
	intentMode := r.Header.Get("X-Antic-Intent")
	if intentMode == "" {
		intentMode = h.cfg.Intent.Mode
	}
	if intentMode == "bypass" {
		h.HandlePassthrough(w, r)
		return
	}

	// 3. Initialize SSE streaming.
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported by this server", http.StatusInternalServerError)
		return
	}

	sessionID := newSessionID()
	h.setSSEHeaders(w, sessionID)

	// 4. Set up the event channel for coordinated SSE writing.
	eventCh := make(chan sseEvent, 8)

	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.FormalTrackTimeout())
	defer cancel()

	clientVersion := parseClientVersion(r)

	// 5. Synchronously check the vault for an existing entry.
	entry := h.vault.Get(resource, id)

	// 6. Compute confidence score and decide whether to speculate.
	//    In "guided" mode the client trusts its own X-Antic-Intent header;
	//    in "auto" mode the scorer makes the call.
	confidenceScore := h.scorer.Score(resource, entry)
	shouldSpeculate := entry != nil && (intentMode == "guided" || confidenceScore >= h.cfg.Intent.AIConfidenceThresh)

	if !shouldSpeculate && entry != nil {
		log.Printf("[spec-link] score %.2f < threshold %.2f for %s/%s — skipping speculation",
			confidenceScore, h.cfg.Intent.AIConfidenceThresh, resource, id)
	}

	// 7. Launch concurrent execution tracks.
	go h.runFastTrack(ctx, resource, id, entry, clientVersion, sessionID, confidenceScore, shouldSpeculate, eventCh)
	go h.runFormalTrack(ctx, r, resource, id, entry, sessionID, eventCh)

	// 7. Stream events to the response until completion or context cancellation.
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-eventCh:
			if !ok {
				return
			}
			if err := writeSSEEvent(w, ev); err != nil {
				log.Printf("[spec-link] sse write error: %v", err)
				return
			}
			flusher.Flush()
			if ev.Terminal {
				return
			}
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Fast Track
// ────────────────────────────────────────────────────────────────────────────

func (h *Handler) runFastTrack(
	ctx context.Context,
	resource, id string,
	entry *vault.Entry,
	clientVersion int,
	sessionID string,
	confidenceScore float64,
	shouldSpeculate bool,
	out chan<- sseEvent,
) {
	start := time.Now()

	if entry == nil {
		// Cache miss — inform client; Formal Track will deliver the data.
		select {
		case out <- sseEvent{
			Event: "meta",
			ID:    "0",
			Data: map[string]any{
				"type":    "cache-miss",
				"message": "No vault entry found. Awaiting formal track.",
			},
		}:
		case <-ctx.Done():
		}
		return
	}

	if !shouldSpeculate {
		// Score below threshold — inform client we are deferring to formal track.
		select {
		case out <- sseEvent{
			Event: "meta",
			ID:    "0",
			Data: map[string]any{
				"type":             "low-confidence",
				"message":          "Confidence score below threshold. Awaiting formal track.",
				"confidence_score": confidenceScore,
			},
		}:
		case <-ctx.Done():
		}
		return
	}

	// Version ordering contract: refuse to speculate with stale data.
	if entry.Version < clientVersion {
		select {
		case out <- sseEvent{
			Event: "meta",
			ID:    "0",
			Data: map[string]any{
				"type":    "version-conflict",
				"message": "Client version is ahead of vault. Awaiting formal track.",
			},
		}:
		case <-ctx.Done():
		}
		return
	}

	latencyMs := time.Since(start).Milliseconds()

	// Build the speculative payload — embed _antic metadata envelope including the confidence score.
	payload := copyMap(entry.Data)
	payload["_antic"] = map[string]any{
		"version":               entry.Version,
		"source":                "vault",
		"age_ms":                entry.AgeMS(),
		"fast_track_latency_ms": latencyMs,
		"confidence_score":      confidenceScore,
		"session_id":            sessionID,
	}

	select {
	case out <- sseEvent{
		Event: "speculative",
		ID:    strconv.Itoa(entry.Version),
		Data:  payload,
	}:
	case <-ctx.Done():
	}
}

// runFormalTrack handles the authoritative execution path by proxying to the upstream API.
func (h *Handler) runFormalTrack(
	ctx context.Context,
	r *http.Request,
	resource, id string,
	specEntry *vault.Entry,
	sessionID string,
	out chan<- sseEvent,
) {
	start := time.Now()

	// Build upstream URL: strip /spec prefix, keep rest of path + query.
	upstreamPath := strings.TrimPrefix(r.URL.Path, h.cfg.Prefix)
	upstreamURL := h.cfg.FormalTrack.Upstream + upstreamPath
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	// Forward the request to the real upstream with a fresh context.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		h.sendAbort(ctx, out, "request_build_error", 500)
		return
	}
	// Copy selected client headers through.
	for _, hdr := range []string{"Authorization", "Accept", "Accept-Language"} {
		if v := r.Header.Get(hdr); v != "" {
			req.Header.Set(hdr, v)
		}
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.sendAbort(ctx, out, "upstream_unreachable", 503)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MB limit
	if err != nil {
		h.sendAbort(ctx, out, "upstream_read_error", 502)
		return
	}

	if resp.StatusCode != http.StatusOK {
		h.sendAbort(ctx, out, fmt.Sprintf("upstream_%d", resp.StatusCode), resp.StatusCode)
		return
	}

	// Parse upstream JSON response.
	var freshData map[string]interface{}
	if err := json.Unmarshal(body, &freshData); err != nil {
		h.sendAbort(ctx, out, "upstream_parse_error", 502)
		return
	}

	// Strip any internal _meta field the upstream may have added.
	delete(freshData, "_meta")

	formalLatencyMs := time.Since(start).Milliseconds()

	// Update the vault with fresh data.
	newEntry := h.vault.Set(resource, id, freshData)

	// Reconciliation logic
	if specEntry == nil {
		// Cache miss — send REPLACE (client has no speculative data yet).
		payload := copyMap(freshData)
		payload["_antic"] = map[string]any{
			"version":                 newEntry.Version,
			"source":                  "live",
			"formal_track_latency_ms": formalLatencyMs,
			"session_id":              sessionID,
		}
		h.send(ctx, out, sseEvent{
			Event:    "replace",
			ID:       strconv.Itoa(newEntry.Version),
			Data:     payload,
			Terminal: true,
		})
		return
	}

	// Compare spec payload vs fresh payload.
	if mapsEqual(specEntry.Data, freshData) {
		// CONFIRM — speculative was 100% accurate. Improve scorer.
		h.scorer.RecordOutcome(resource, "confirm")
		h.send(ctx, out, sseEvent{
			Event: "confirm",
			ID:    strconv.Itoa(newEntry.Version),
			Data: map[string]any{
				"status":                  "ok",
				"version":                 newEntry.Version,
				"formal_track_latency_ms": formalLatencyMs,
				"total_latency_ms":        time.Since(start.Add(-time.Duration(formalLatencyMs) * time.Millisecond)).Milliseconds(),
				"session_id":              sessionID,
			},
			Terminal: true,
		})
		return
	}

	// Data changed — decide between PATCH and REPLACE based on diff size.
	patches := diffMaps(specEntry.Data, freshData)

	if h.cfg.Reconcile.Strategy == "patch" && len(patches) <= 10 {
		// PATCH — small delta. Record as a miss so scorer learns the resource is volatile.
		h.scorer.RecordOutcome(resource, "patch")
		h.send(ctx, out, sseEvent{
			Event: "patch",
			ID:    strconv.Itoa(newEntry.Version),
			Data: map[string]any{
				"ops":                     patches,
				"formal_track_latency_ms": formalLatencyMs,
				"session_id":              sessionID,
			},
			Terminal: true,
		})
	} else {
		// REPLACE — large diff. Record as a miss.
		h.scorer.RecordOutcome(resource, "replace")
		payload := copyMap(freshData)
		payload["_antic"] = map[string]any{
			"version":                 newEntry.Version,
			"source":                  "live",
			"formal_track_latency_ms": formalLatencyMs,
			"session_id":              sessionID,
		}
		h.send(ctx, out, sseEvent{
			Event:    "replace",
			ID:       strconv.Itoa(newEntry.Version),
			Data:     payload,
			Terminal: true,
		})
	}
}

// HandleMutation handles optimistic write operations (POST, PUT, PATCH, DELETE).
// It immediately streams an "optimistic" event, then forwards the mutation to
// the upstream and streams either "committed" or "reverted" based on the outcome.
func (h *Handler) HandleMutation(w http.ResponseWriter, r *http.Request) {
	resource, id, err := parsePath(r.URL.Path, h.cfg.Prefix)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	sessionID := newSessionID()
	h.setSSEHeaders(w, sessionID)

	// Read and limit the request body.
	body, err := io.ReadAll(io.LimitReader(r.Body, h.cfg.Mutations.BodyLimitBytes))
	if err != nil {
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}

	// Parse the client's intended new state (if body is JSON).
	var clientData map[string]interface{}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &clientData)
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.FormalTrackTimeout())
	defer cancel()

	// Build the optimistic preview: for PUT/PATCH, merge client intent over current vault state.
	// For DELETE, the optimistic event signals removal.
	optimisticData := h.buildOptimistic(r.Method, resource, id, clientData)

	// Emit "optimistic" immediately — client applies mutation locally.
	ev := sseEvent{
		Event: "optimistic",
		ID:    sessionID,
		Data: map[string]any{
			"method":     r.Method,
			"resource":   resource,
			"id":         id,
			"data":       optimisticData,
			"session_id": sessionID,
		},
	}
	if err := writeSSEEvent(w, ev); err != nil {
		return
	}
	flusher.Flush()

	// Forward mutation to upstream asynchronously and send committed/reverted.
	h.runMutationTrack(ctx, w, flusher, r, resource, id, body, clientData, sessionID)
}

// buildOptimistic constructs the predicted state after a mutation.
// For PUT: use client body as-is. For PATCH: merge over vault entry. For DELETE: nil.
func (h *Handler) buildOptimistic(method, resource, id string, clientData map[string]interface{}) map[string]interface{} {
	switch method {
	case http.MethodDelete:
		return nil
	case http.MethodPatch:
		// Merge client patch fields over the current vault entry.
		existing := h.vault.Get(resource, id)
		if existing != nil {
			merged := copyMap(existing.Data)
			for k, v := range clientData {
				merged[k] = v
			}
			return merged
		}
	}
	// PUT and POST: use client body directly.
	return clientData
}

// runMutationTrack forwards the mutation to upstream and emits committed or reverted.
func (h *Handler) runMutationTrack(
	ctx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	r *http.Request,
	resource, id string,
	body []byte,
	clientData map[string]interface{},
	sessionID string,
) {
	start := time.Now()

	upstreamPath := strings.TrimPrefix(r.URL.Path, h.cfg.Prefix)
	upstreamURL := h.cfg.FormalTrack.Upstream + upstreamPath
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		h.writeMutationAbort(w, flusher, sessionID, "request_build_error", 500)
		return
	}
	// Forward relevant headers.
	for _, hdr := range []string{"Content-Type", "Authorization", "Accept"} {
		if v := r.Header.Get(hdr); v != "" {
			req.Header.Set(hdr, v)
		}
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.writeMutationAbort(w, flusher, sessionID, "upstream_unreachable", 503)
		return
	}
	defer resp.Body.Close()

	latencyMs := time.Since(start).Milliseconds()

	// Non-2xx means the upstream rejected the mutation — client must revert.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		ev := sseEvent{
			Event: "reverted",
			ID:    sessionID,
			Data: map[string]any{
				"reason":     fmt.Sprintf("upstream_%d", resp.StatusCode),
				"code":       resp.StatusCode,
				"detail":     string(respBody),
				"session_id": sessionID,
			},
			Terminal: true,
		}
		writeSSEEvent(w, ev)
		flusher.Flush()
		return
	}

	// 2xx success — update vault and send committed with server's actual response.
	var serverData map[string]interface{}
	if r.Method != http.MethodDelete {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, h.cfg.Mutations.BodyLimitBytes))
		if len(respBody) > 0 {
			_ = json.Unmarshal(respBody, &serverData)
		}
		// Fall back to client data if server returned no body (e.g., 204).
		if serverData == nil {
			serverData = clientData
		}
		h.vault.Set(resource, id, serverData)
	} else {
		// DELETE: evict from vault.
		h.vault.Delete(resource, id)
	}

	// server-wins: always send actual server response so client has ground truth.
	ev := sseEvent{
		Event: "committed",
		ID:    sessionID,
		Data: map[string]any{
			"method":     r.Method,
			"resource":   resource,
			"id":         id,
			"data":       serverData, // nil for DELETE
			"latency_ms": latencyMs,
			"session_id": sessionID,
		},
		Terminal: true,
	}
	writeSSEEvent(w, ev)
	flusher.Flush()
}

// writeMutationAbort emits a reverted event for infrastructure-level errors.
func (h *Handler) writeMutationAbort(w http.ResponseWriter, flusher http.Flusher, sessionID, reason string, code int) {
	ev := sseEvent{
		Event: "reverted",
		ID:    sessionID,
		Data: map[string]any{
			"reason":     reason,
			"code":       code,
			"session_id": sessionID,
		},
		Terminal: true,
	}
	writeSSEEvent(w, ev)
	flusher.Flush()
}

// HandlePassthrough provides a transparent reverse-proxy for non-spec routes.
func (h *Handler) HandlePassthrough(w http.ResponseWriter, r *http.Request) {
	upstreamURL := h.cfg.FormalTrack.Upstream + r.URL.RequestURI()

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, r.Body)
	if err != nil {
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}
	for k, vals := range r.Header {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (h *Handler) setSSEHeaders(w http.ResponseWriter, sessionID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Antic-Session-ID", sessionID)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no")
}

func (h *Handler) send(ctx context.Context, out chan<- sseEvent, ev sseEvent) {
	select {
	case out <- ev:
	case <-ctx.Done():
	}
}

func (h *Handler) sendAbort(ctx context.Context, out chan<- sseEvent, reason string, code int) {
	h.send(ctx, out, sseEvent{
		Event: "abort",
		ID:    "0",
		Data: map[string]any{
			"reason": reason,
			"code":   code,
			"revert": true,
		},
		Terminal: true,
	})
}

// writeSSEEvent serializes and writes a single SSE event to the client.
func writeSSEEvent(w io.Writer, ev sseEvent) error {
	data, err := json.Marshal(ev.Data)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "event: %s\n", ev.Event)
	fmt.Fprintf(&buf, "id: %s\n", ev.ID)
	fmt.Fprintf(&buf, "data: %s\n\n", data)
	_, err = w.Write(buf.Bytes())
	return err
}

// parsePath extracts the resource and ID from a URL path.
func parsePath(urlPath, prefix string) (resource, id string, err error) {
	trimmed := strings.TrimPrefix(urlPath, prefix)
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("path must be %s/{resource}/{id}", prefix)
	}
	return path.Clean(parts[0]), path.Clean(parts[1]), nil
}

// parseClientVersion extracts the X-Antic-Version header if present.
func parseClientVersion(r *http.Request) int {
	v, _ := strconv.Atoi(r.Header.Get("X-Antic-Version"))
	return v
}

// newSessionID generates a random 8-byte hex session identifier.
func newSessionID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func copyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func mapsEqual(a, b map[string]interface{}) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return bytes.Equal(aj, bj)
}

// PatchOp defines a single RFC 6902 JSON Patch operation.
type PatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// diffMaps calculates the difference between two maps as a list of PatchOps.
func diffMaps(oldMap, newMap map[string]interface{}) []PatchOp {
	var ops []PatchOp

	for k, newVal := range newMap {
		if k == "_antic" || k == "_meta" {
			continue
		}
		oldVal, exists := oldMap[k]
		if !exists {
			ops = append(ops, PatchOp{Op: "add", Path: "/" + k, Value: newVal})
			continue
		}

		oj, _ := json.Marshal(oldVal)
		nj, _ := json.Marshal(newVal)
		if !bytes.Equal(oj, nj) {
			ops = append(ops, PatchOp{Op: "replace", Path: "/" + k, Value: newVal})
		}
	}

	for k := range oldMap {
		if k == "_antic" || k == "_meta" {
			continue
		}
		if _, exists := newMap[k]; !exists {
			ops = append(ops, PatchOp{Op: "remove", Path: "/" + k})
		}
	}

	return ops
}
