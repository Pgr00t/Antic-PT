// Package proxy contains the core Spec-Link dual-track logic.
//
// Architecture:
//   HandleSpec forks one incoming GET /spec/{resource}/{id} into two concurrent
//   goroutines:
//     • Fast Track  – reads the State-Vault and emits a "speculative" SSE event
//                     within a few milliseconds.
//     • Formal Track – proxies the request to the real upstream API, waits for
//                      the full response, compares it with the vault snapshot, and
//                      then emits CONFIRM / PATCH / REPLACE / ABORT.
//
//   Both goroutines write typed events onto a shared channel; a single dedicated
//   goroutine drains that channel and writes to the http.ResponseWriter, keeping
//   concurrent access safe.
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
	"antic-pt/spec-link/vault"
)

// ────────────────────────────────────────────────────────────────────────────
// SSE helper types
// ────────────────────────────────────────────────────────────────────────────

// sseEvent represents one Server-Sent Event.
type sseEvent struct {
	Event    string // e.g. "speculative", "confirm", "patch" …
	ID       string // resource version as string
	Data     any    // will be JSON-marshalled
	Terminal bool   // if true the writer goroutine closes the connection
}

// ────────────────────────────────────────────────────────────────────────────
// Handler
// ────────────────────────────────────────────────────────────────────────────

// Handler is the Spec-Link HTTP handler.
type Handler struct {
	cfg        *config.SpecLinkConfig
	vault      vault.Vault
	httpClient *http.Client
}

// NewHandler creates a Handler wired to the supplied config and vault.
func NewHandler(cfg *config.SpecLinkConfig, v vault.Vault) *Handler {
	return &Handler{
		cfg:   cfg,
		vault: v,
		httpClient: &http.Client{
			Timeout: cfg.FormalTrackTimeout() + 2*time.Second,
		},
	}
}

// ────────────────────────────────────────────────────────────────────────────
// HandleSpec — dual-track SSE endpoint
// ────────────────────────────────────────────────────────────────────────────

// HandleSpec handles all requests to /spec/{resource}/{id}.
func (h *Handler) HandleSpec(w http.ResponseWriter, r *http.Request) {
	// Safety: Antic-PT v1.0 is reads-only.
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"Antic-PT v1.0 supports GET only","code":405}`, http.StatusMethodNotAllowed)
		return
	}

	// ------------------------------------------------------------------
	// 1. Parse path:  /spec/{resource}/{id}
	// ------------------------------------------------------------------
	resource, id, err := parsePath(r.URL.Path, h.cfg.Prefix)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	// ------------------------------------------------------------------
	// 2. Read X-Antic-Intent mode
	// ------------------------------------------------------------------
	intentMode := r.Header.Get("X-Antic-Intent")
	if intentMode == "" {
		intentMode = h.cfg.Intent.Mode
	}
	if intentMode == "bypass" {
		h.HandlePassthrough(w, r)
		return
	}

	// ------------------------------------------------------------------
	// 3. Setup SSE transport
	// ------------------------------------------------------------------
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported by this server", http.StatusInternalServerError)
		return
	}

	sessionID := newSessionID()
	h.setSSEHeaders(w, sessionID)

	// ------------------------------------------------------------------
	// 4. Shared event channel — only one goroutine writes to w
	// ------------------------------------------------------------------
	eventCh := make(chan sseEvent, 8)

	// Request-scoped context with the formal-track timeout.
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.FormalTrackTimeout())
	defer cancel()

	// Client-supplied last-known version (optional hint).
	clientVersion := parseClientVersion(r)

	// ------------------------------------------------------------------
	// 5. Pre-fetch vault entry (done synchronously: it's a map lookup, ~µs)
	// ------------------------------------------------------------------
	entry := h.vault.Get(resource, id)

	// ------------------------------------------------------------------
	// 6. Launch Fast Track goroutine
	// ------------------------------------------------------------------
	go h.runFastTrack(ctx, resource, id, entry, clientVersion, sessionID, eventCh)

	// ------------------------------------------------------------------
	// 7. Launch Formal Track goroutine
	// ------------------------------------------------------------------
	go h.runFormalTrack(ctx, r, resource, id, entry, sessionID, eventCh)

	// ------------------------------------------------------------------
	// 8. Single-writer: drain eventCh → ResponseWriter
	// ------------------------------------------------------------------
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

	// Build the speculative payload — embed _antic metadata envelope.
	payload := copyMap(entry.Data)
	payload["_antic"] = map[string]any{
		"version":               entry.Version,
		"source":                "vault",
		"age_ms":                entry.AgeMS(),
		"fast_track_latency_ms": latencyMs,
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

// ────────────────────────────────────────────────────────────────────────────
// Formal Track
// ────────────────────────────────────────────────────────────────────────────

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

	// Write fresh data back to vault.
	newEntry := h.vault.Set(resource, id, freshData)

	// ── Reconciliation ──────────────────────────────────────────────────
	if specEntry == nil {
		// Was a cache miss — send REPLACE (client has no speculative data yet).
		payload := copyMap(freshData)
		payload["_antic"] = map[string]any{
			"version":                newEntry.Version,
			"source":                 "live",
			"formal_track_latency_ms": formalLatencyMs,
			"session_id":             sessionID,
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
		// ✓ CONFIRM — speculative was 100% accurate.
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

	// Data changed — decide between PATCH and REPLACE.
	patches := diffMaps(specEntry.Data, freshData)

	if h.cfg.Reconcile.Strategy == "patch" && len(patches) <= 10 {
		// △ PATCH — small delta, send JSON-patch ops.
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
		// ↺ REPLACE — large diff, send full payload.
		payload := copyMap(freshData)
		payload["_antic"] = map[string]any{
			"version":                newEntry.Version,
			"source":                 "live",
			"formal_track_latency_ms": formalLatencyMs,
			"session_id":             sessionID,
		}
		h.send(ctx, out, sseEvent{
			Event:    "replace",
			ID:       strconv.Itoa(newEntry.Version),
			Data:     payload,
			Terminal: true,
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Passthrough proxy (non-spec routes or bypass mode)
// ────────────────────────────────────────────────────────────────────────────

// HandlePassthrough is a transparent reverse-proxy for all non-spec routes.
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

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func (h *Handler) setSSEHeaders(w http.ResponseWriter, sessionID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Antic-Session-ID", sessionID)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no") // Disable Nginx buffering
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

// writeSSEEvent formats and writes a single SSE event to w.
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

// parsePath splits "/spec/users/123" → ("users", "123", nil).
func parsePath(urlPath, prefix string) (resource, id string, err error) {
	trimmed := strings.TrimPrefix(urlPath, prefix)
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("path must be %s/{resource}/{id}", prefix)
	}
	return path.Clean(parts[0]), path.Clean(parts[1]), nil
}

// parseClientVersion reads X-Antic-Version header (optional).
func parseClientVersion(r *http.Request) int {
	v, _ := strconv.Atoi(r.Header.Get("X-Antic-Version"))
	return v
}

// newSessionID generates a unique 8-byte hex session identifier.
func newSessionID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// copyMap returns a shallow copy of m.
func copyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// mapsEqual compares two flat maps by JSON representation.
func mapsEqual(a, b map[string]interface{}) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return bytes.Equal(aj, bj)
}

// PatchOp is a single RFC 6902 JSON-patch operation.
type PatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// diffMaps computes a flat RFC 6902 patch between oldMap and newMap.
// Only "replace" and "add" ops are generated (sufficient for flat objects).
func diffMaps(oldMap, newMap map[string]interface{}) []PatchOp {
	var ops []PatchOp

	// Added or changed fields
	for k, newVal := range newMap {
		if k == "_antic" || k == "_meta" {
			continue
		}
		oldVal := oldMap[k]
		oj, _ := json.Marshal(oldVal)
		nj, _ := json.Marshal(newVal)
		if !bytes.Equal(oj, nj) {
			op := "replace"
			if _, exists := oldMap[k]; !exists {
				op = "add"
			}
			ops = append(ops, PatchOp{Op: op, Path: "/" + k, Value: newVal})
		}
	}

	// Removed fields
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
