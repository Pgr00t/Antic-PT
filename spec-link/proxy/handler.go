// Package proxy implements the Spec-Link dual-track HTTP proxy for Antic-PT v0.2.
//
// Each request arriving at the /spec prefix is handled by HandleSpec, which:
//  1. Checks the State Vault for a cached entry.
//  2. If cold: proxies directly to upstream, returns X-Antic-State: confirmed, no signal.
//  3. If warm: checks staleness against max_staleness_ms.
//     If stale: same as cold.
//     If fresh: serves speculative response immediately (Fast Track) with field
//     classification applied (DEFERRED fields omitted), attaches X-Antic-* headers,
//     then launches the Formal Track concurrently, which publishes the reconciliation
//     signal to the SignalHub.
package proxy

import (
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
	"antic-pt/spec-link/fields"
	"antic-pt/spec-link/vault"
)

const defaultMaxWindowMs = 3000

// Handler implements the Spec-Link HTTP proxy logic.
type Handler struct {
	cfg        *config.SpecLinkConfig
	vault      vault.Vault
	snapshots  *vault.SnapshotStore
	classifier *fields.Classifier
	hub        *SignalHub
	httpClient *http.Client
}

// NewHandler initialises a Handler with all dependencies.
func NewHandler(cfg *config.SpecLinkConfig, v vault.Vault, hub *SignalHub) *Handler {
	return &Handler{
		cfg:        cfg,
		vault:      v,
		snapshots:  &vault.SnapshotStore{},
		classifier: fields.NewClassifier(cfg.Endpoints),
		hub:        hub,
		httpClient: &http.Client{
			Timeout: cfg.FormalTrackTimeout() + 2*time.Second,
		},
	}
}

// ────────────────────────────────────────────────────────────────────────────
// HandleSpec — main entry point
// ────────────────────────────────────────────────────────────────────────────

// HandleSpec routes GET requests through the dual-track execution logic.
func (h *Handler) HandleSpec(w http.ResponseWriter, r *http.Request) {
	// CORS preflight.
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Antic-Client-Id")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed","code":405}`, http.StatusMethodNotAllowed)
		return
	}

	// 1. Resolve resource path (strip /spec prefix).
	resourcePath := strings.TrimPrefix(r.URL.Path, h.cfg.Prefix)
	if resourcePath == "" {
		resourcePath = "/"
	}

	resource, id, err := parsePath(resourcePath)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	// 2. Check the vault.
	entry := h.vault.Get(resource, id)

	// 3. Cold miss or stale → serve confirmed directly.
	maxStaleness := h.classifier.MaxStalenessMs(resourcePath)
	if entry == nil || entry.AgeMS() > int64(maxStaleness) {
		h.serveConfirmed(w, r, resourcePath)
		return
	}

	// 4. Hot: serve speculative response immediately.
	reconcileID := newReconcileID()
	snap := h.snapshots.Capture(reconcileID, entry)

	// Determine client ID for signal routing (sent by SDK as a query param or header).
	clientID := r.Header.Get("X-Antic-Client-Id")
	if clientID == "" {
		clientID = r.URL.Query().Get("client_id")
	}
	if clientID == "" {
		// No client ID → cannot route signals; serve confirmed instead.
		h.serveConfirmed(w, r, resourcePath)
		return
	}

	// Build response body, omitting DEFERRED fields.
	allFields := fieldKeys(entry.Data)
	deferredFields := h.classifier.DeferredFields(resourcePath, allFields)
	deferredSet := toSet(deferredFields)

	payload := make(map[string]interface{}, len(entry.Data))
	for k, v := range entry.Data {
		if deferredSet[k] {
			payload[k] = nil // DEFERRED: omitted (nil marker)
		} else {
			payload[k] = v
		}
	}

	// Build X-Antic-Volatility header value: "field=level, field2=level".
	volatilityMap := h.classifier.VolatilityMap(resourcePath, allFields)
	volatilityHeader := formatVolatilityHeader(volatilityMap)

	// Attach X-Antic-* headers.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers",
		"X-Antic-State, X-Antic-Reconcile-Id, X-Antic-Staleness, X-Antic-Volatility, X-Antic-Deferred-Fields, X-Antic-Max-Window")
	w.Header().Set("X-Antic-State", "speculative")
	w.Header().Set("X-Antic-Reconcile-Id", reconcileID)
	w.Header().Set("X-Antic-Staleness", strconv.FormatInt(entry.AgeMS(), 10))
	w.Header().Set("X-Antic-Max-Window", strconv.Itoa(defaultMaxWindowMs))
	if volatilityHeader != "" {
		w.Header().Set("X-Antic-Volatility", volatilityHeader)
	}
	if len(deferredFields) > 0 {
		w.Header().Set("X-Antic-Deferred-Fields", strings.Join(deferredFields, ", "))
	}

	// Write the speculative JSON body.
	body, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(body)

	// 5. Launch Formal Track concurrently — it will publish signals to the hub.
	ctx, cancel := context.WithTimeout(context.Background(), h.cfg.FormalTrackTimeout())
	go func() {
		defer cancel()
		defer h.snapshots.Release(reconcileID)
		h.runFormalTrack(ctx, r, resourcePath, resource, id, snap, clientID, reconcileID, deferredSet)
	}()
}

// ────────────────────────────────────────────────────────────────────────────
// Confirmed (cold / stale) path
// ────────────────────────────────────────────────────────────────────────────

// serveConfirmed proxies directly to upstream, returns X-Antic-State: confirmed,
// populates the vault, and sends no reconciliation signal.
func (h *Handler) serveConfirmed(w http.ResponseWriter, r *http.Request, resourcePath string) {
	upstreamURL := h.cfg.FormalTrack.Upstream + resourcePath
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.FormalTrackTimeout())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		http.Error(w, `{"error":"upstream request failed"}`, http.StatusBadGateway)
		return
	}
	for _, hdr := range []string{"Authorization", "Accept", "Accept-Language"} {
		if v := r.Header.Get(hdr); v != "" {
			req.Header.Set(hdr, v)
		}
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		http.Error(w, `{"error":"upstream unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		http.Error(w, `{"error":"upstream read error"}`, http.StatusBadGateway)
		return
	}

	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
		return
	}

	// Parse and store in vault.
	var freshData map[string]interface{}
	if err := json.Unmarshal(body, &freshData); err == nil {
		delete(freshData, "_meta")
		resource, id, _ := parsePath(resourcePath)
		h.vault.Set(resource, id, freshData)
	}

	// Return with confirmed header — no Reconcile-Id, no signal.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "X-Antic-State")
	w.Header().Set("X-Antic-State", "confirmed")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// ────────────────────────────────────────────────────────────────────────────
// Formal Track
// ────────────────────────────────────────────────────────────────────────────

func (h *Handler) runFormalTrack(
	ctx context.Context,
	r *http.Request,
	resourcePath, resource, id string,
	snap *vault.Snapshot,
	clientID, reconcileID string,
	deferredSet map[string]bool,
) {
	upstreamPath := resourcePath
	upstreamURL := h.cfg.FormalTrack.Upstream + upstreamPath
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		h.publishAbort(clientID, reconcileID, "upstream_error", true)
		return
	}
	for _, hdr := range []string{"Authorization", "Accept", "Accept-Language"} {
		if v := r.Header.Get(hdr); v != "" {
			req.Header.Set(hdr, v)
		}
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.publishAbort(clientID, reconcileID, "upstream_timeout", true)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		reason := fmt.Sprintf("upstream_%d", resp.StatusCode)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			h.publishAbort(clientID, reconcileID, "auth_revoked", false)
		} else {
			h.publishAbort(clientID, reconcileID, reason, true)
		}
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		h.publishAbort(clientID, reconcileID, "upstream_error", true)
		return
	}

	var freshData map[string]interface{}
	if err := json.Unmarshal(body, &freshData); err != nil {
		h.publishAbort(clientID, reconcileID, "schema_mismatch", false)
		return
	}
	delete(freshData, "_meta")

	// ── INVARIANT violation check ────────────────────────────────────────────
	invariantFields := h.classifier.InvariantFields(resourcePath, fieldKeys(snap.Data))
	for _, f := range invariantFields {
		snapVal := snap.Data[f]
		freshVal := freshData[f]
		if !jsonValuesEqual(snapVal, freshVal) {
			log.Printf("[spec-link] INVARIANT violation: field=%s snap=%v fresh=%v reconcile=%s",
				f, snapVal, freshVal, reconcileID)
			h.publishAbort(clientID, reconcileID, "invariant_violation", false)
			// Still update vault with authoritative data (per spec Section 11.3).
			h.vault.Set(resource, id, freshData)
			return
		}
	}

	// Update live vault.
	newEntry := h.vault.Set(resource, id, freshData)
	_ = newEntry

	// ── Reconciliation logic ─────────────────────────────────────────────────
	allFreshFields := fieldKeys(freshData)
	allSnapFields := fieldKeys(snap.Data)

	// Structural change detection.
	if structuralChange(allSnapFields, allFreshFields, snap.Data, freshData) {
		h.publishReplace(clientID, reconcileID, freshData)
		return
	}

	// Compute SPECULATIVE field diff.
	specPatches := []patchOp{}
	changedSpecCount := 0
	totalSpecCount := 0

	for _, f := range allSnapFields {
		class := h.classifier.ClassOf(resourcePath, f)
		if class != fields.ClassSpeculative {
			continue
		}
		totalSpecCount++
		snapVal := snap.Data[f]
		freshVal := freshData[f]
		if !jsonValuesEqual(snapVal, freshVal) {
			changedSpecCount++
			specPatches = append(specPatches, patchOp{
				Op:    "replace",
				Path:  "/" + f,
				Value: freshVal,
			})
		}
	}

	// REPLACE threshold check.
	threshold := h.classifier.ReplaceThreshold(resourcePath)
	if totalSpecCount > 0 && float64(changedSpecCount)/float64(totalSpecCount) >= threshold {
		h.publishReplace(clientID, reconcileID, freshData)
		return
	}

	// Emit PATCH (if any SPECULATIVE fields changed).
	if len(specPatches) > 0 {
		h.publishSignal(clientID, sseEvent{
			Event: "patch",
			ID:    reconcileID,
			Data: map[string]any{
				"signal":    "PATCH",
				"id":        reconcileID,
				"timestamp": time.Now().UnixMilli(),
				"ops":       specPatches,
			},
		})
	}

	// Emit FILL (if DEFERRED fields were present).
	deferredValues := map[string]interface{}{}
	for _, f := range allFreshFields {
		if deferredSet[f] {
			deferredValues[f] = freshData[f]
		}
	}
	if len(deferredValues) > 0 {
		h.publishSignal(clientID, sseEvent{
			Event: "fill",
			ID:    reconcileID,
			Data: map[string]any{
				"signal":    "FILL",
				"id":        reconcileID,
				"timestamp": time.Now().UnixMilli(),
				"fields":    deferredValues,
			},
		})
	}

	// Emit CONFIRM if nothing changed and no deferred fields.
	if len(specPatches) == 0 && len(deferredValues) == 0 {
		h.publishSignal(clientID, sseEvent{
			Event: "confirm",
			ID:    reconcileID,
			Data: map[string]any{
				"signal":    "CONFIRM",
				"id":        reconcileID,
				"timestamp": time.Now().UnixMilli(),
			},
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Signal publishing helpers
// ────────────────────────────────────────────────────────────────────────────

func (h *Handler) publishSignal(clientID string, ev sseEvent) {
	h.hub.Publish(clientID, ev, defaultMaxWindowMs*time.Millisecond)
}

func (h *Handler) publishAbort(clientID, reconcileID, reason string, retryable bool) {
	h.publishSignal(clientID, sseEvent{
		Event: "abort",
		ID:    reconcileID,
		Data: map[string]any{
			"signal":    "ABORT",
			"id":        reconcileID,
			"timestamp": time.Now().UnixMilli(),
			"reason":    reason,
			"retryable": retryable,
		},
		Terminal: true,
	})
}

func (h *Handler) publishReplace(clientID, reconcileID string, data map[string]interface{}) {
	h.publishSignal(clientID, sseEvent{
		Event: "replace",
		ID:    reconcileID,
		Data: map[string]any{
			"signal":    "REPLACE",
			"id":        reconcileID,
			"timestamp": time.Now().UnixMilli(),
			"data":      data,
		},
		Terminal: true,
	})
}

// ────────────────────────────────────────────────────────────────────────────
// HandlePassthrough — non-spec routes
// ────────────────────────────────────────────────────────────────────────────

// HandlePassthrough provides transparent reverse-proxy for routes outside the spec prefix.
func (h *Handler) HandlePassthrough(w http.ResponseWriter, r *http.Request) {
	upstreamURL := h.cfg.FormalTrack.Upstream + r.URL.RequestURI()
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.FormalTrackTimeout())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, r.Body)
	if err != nil {
		http.Error(w, `{"error":"proxy error"}`, http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()

	resp, err := h.httpClient.Do(req)
	if err != nil {
		http.Error(w, `{"error":"upstream unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// ────────────────────────────────────────────────────────────────────────────
// Utilities
// ────────────────────────────────────────────────────────────────────────────

// sseEvent is an internal event structure used when publishing to the hub.
type sseEvent struct {
	Event    string
	ID       string
	Data     any
	Terminal bool
}

type patchOp struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

func newReconcileID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "arc_" + hex.EncodeToString(b)
}

func parsePath(p string) (resource, id string, err error) {
	p = path.Clean("/" + strings.TrimPrefix(p, "/"))
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("path must be /<resource>/<id>")
	}
	// Resource is everything except the last part.
	// ID is the last part.
	id = parts[len(parts)-1]
	resource = strings.Join(parts[:len(parts)-1], "/")
	return resource, id, nil
}

func fieldKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func toSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func formatVolatilityHeader(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for f, v := range m {
		parts = append(parts, f+"="+v)
	}
	return strings.Join(parts, ", ")
}

// structuralChange returns true if any field was added, removed, or changed type.
func structuralChange(snapFields, freshFields []string, snapData, freshData map[string]interface{}) bool {
	snapSet := toSet(snapFields)
	freshSet := toSet(freshFields)
	for f := range freshSet {
		if !snapSet[f] {
			return true // field added
		}
	}
	for f := range snapSet {
		if !freshSet[f] {
			return true // field removed
		}
		// Type change check.
		if fmt.Sprintf("%T", snapData[f]) != fmt.Sprintf("%T", freshData[f]) {
			return true
		}
	}
	return false
}

// jsonValuesEqual compares two interface{} values for JSON equality.
func jsonValuesEqual(a, b interface{}) bool {
	if a == nil && b == nil {
		return true
	}
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// marshalJSON encodes v to a JSON string for SSE data field.
func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// copyMap performs a shallow copy of a map for use in mutations.
func copyMap(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func parseClientVersion(r *http.Request) int {
	v, _ := strconv.Atoi(r.Header.Get("X-Antic-Version"))
	return v
}
