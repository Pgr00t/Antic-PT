// Package proxy/signals implements the multiplexed SSE signal channel for Antic-PT v0.2.
//
// All reconciliation signals for all in-flight requests share a single persistent SSE
// connection per client, identified by a client-generated UUID. Signals are matched to
// the originating request on the client side via X-Antic-Reconcile-Id.
//
// Endpoint: GET /antic/signals?client_id=<uuid>
package proxy

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// signalMsg is an SSE event buffered for delivery to a client.
type signalMsg struct {
	event     string
	id        string // Reconcile ID
	data      string // JSON-encoded payload
	expiresAt time.Time
}

// clientConn tracks one persistent SSE connection from a client.
type clientConn struct {
	mu     sync.Mutex
	ch     chan signalMsg
	maxWin time.Duration // maximum X-Antic-Max-Window across all active requests
	buffer []signalMsg   // signals buffered during disconnect
	active bool          // true when SSE connection is open
}

// SignalHub manages all client SSE connections and routes signals to the correct client.
// It is safe for concurrent use.
type SignalHub struct {
	mu      sync.RWMutex
	clients map[string]*clientConn // keyed by client_id
}

// NewSignalHub creates an empty hub ready to accept connections.
func NewSignalHub() *SignalHub {
	return &SignalHub{
		clients: make(map[string]*clientConn),
	}
}

// getOrCreate returns the clientConn for clientID, creating it if needed.
func (h *SignalHub) getOrCreate(clientID string) *clientConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.clients[clientID]; ok {
		return c
	}
	c := &clientConn{
		ch:     make(chan signalMsg, 64),
		buffer: nil,
		active: false,
	}
	h.clients[clientID] = c
	return c
}

// Publish sends a signal to the client identified by clientID.
// If the client has an active SSE connection, the signal is delivered immediately.
// If the connection is down, the signal is buffered until reconnect or expiry.
func (h *SignalHub) Publish(clientID string, ev sseEvent, maxWindow time.Duration) {
	data, err := marshalJSON(ev.Data)
	if err != nil {
		log.Printf("[signals] marshal error for reconcile %s: %v", ev.ID, err)
		return
	}

	msg := signalMsg{
		event:     ev.Event,
		id:        ev.ID,
		data:      data,
		expiresAt: time.Now().Add(maxWindow),
	}

	c := h.getOrCreate(clientID)
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.active {
		select {
		case c.ch <- msg:
		default:
			log.Printf("[signals] client %s channel full, dropping signal %s", clientID, ev.ID)
		}
	} else {
		// Buffer while disconnected.
		c.buffer = append(c.buffer, msg)
	}
}

// ServeSignals is the HTTP handler for GET /antic/signals?client_id=<uuid>.
// It establishes a persistent SSE connection and streams reconciliation signals.
func (h *SignalHub) ServeSignals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		http.Error(w, `{"error":"client_id query parameter is required"}`, http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// SSE response headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no")

	c := h.getOrCreate(clientID)

	// Mark connection active and replay any buffered signals.
	c.mu.Lock()
	c.active = true
	buffered := pruneExpired(c.buffer)
	c.buffer = nil
	c.mu.Unlock()

	for _, msg := range buffered {
		writeSSEMsg(w, msg)
	}
	flusher.Flush()

	// Send a connected heartbeat so the client knows the channel is live.
	fmt.Fprintf(w, ": connected client_id=%s\n\n", clientID)
	flusher.Flush()

	// Heartbeat ticker to keep the connection alive through proxies.
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			// Client disconnected.
			c.mu.Lock()
			c.active = false
			c.mu.Unlock()
			log.Printf("[signals] client %s disconnected", clientID)
			return

		case msg, ok := <-c.ch:
			if !ok {
				return
			}
			writeSSEMsg(w, msg)
			flusher.Flush()

		case <-ticker.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

// writeSSEMsg writes a single SSE message to the response writer.
func writeSSEMsg(w http.ResponseWriter, msg signalMsg) {
	if msg.id != "" {
		fmt.Fprintf(w, "id: %s\n", msg.id)
	}
	if msg.event != "" {
		fmt.Fprintf(w, "event: %s\n", msg.event)
	}
	fmt.Fprintf(w, "data: %s\n\n", msg.data)
}

// pruneExpired removes buffered signals whose max window has expired.
func pruneExpired(buf []signalMsg) []signalMsg {
	now := time.Now()
	result := buf[:0]
	for _, msg := range buf {
		if msg.expiresAt.After(now) {
			result = append(result, msg)
		}
	}
	return result
}
