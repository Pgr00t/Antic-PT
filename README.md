# Antic-PT: Anticipation Protocol

**Respond Before You're Asked.**

Antic-PT is an open, transport-agnostic protocol layer that enables **synchronous speculative responses** for read-heavy HTTP services. It eliminates perceived network latency by forking a single incoming request into two concurrent execution tracks:

1. **Fast Track**: Serves a cached speculative response immediately (in milliseconds).
2. **Formal Track**: Validates against the authoritative data source in the background.

The client is informed of any discrepancy via a structured reconciliation signal (`CONFIRM`, `PATCH`, `REPLACE`, or `ABORT`).

Antic-PT is designed to be a **drop-in layer** in front of any existing REST API, requiring zero changes to the underlying application.

## Structure

* [`ANTIC-PT-SPEC.md`](./ANTIC-PT-SPEC.md) - The core protocol specification (Draft v0.1).
* [`spec-link/`](./spec-link) - The production-grade Go middleware proxy that implements the Antic-PT dual-track logic.
* [`demo/`](./demo) - A demonstration environment featuring an Express standard-REST API alongside the Antic-PT interface, complete with a beautifully visualized client dashboard.

## Running the Demo (Node.js Reference)

A fully visualized demonstration environment using Server-Sent Events (SSE) to showcase the dual-track system.

1. Navigate to the server demo directory:
   ```bash
   cd demo/server
   ```
2. Install dependencies:
   ```bash
   npm install
   ```
3. Start the server (serves the backend and front-end):
   ```bash
   npm start
   ```
4. Open your browser to `http://localhost:4000`.

## Running Spec-Link (Go Production Proxy)

The actual production-ready implementation of the Spec-Link proxy, written in Go for extreme concurrency and low memory usage.

1. Navigate to the `spec-link` directory:
   ```bash
   cd spec-link
   ```
2. Download dependencies:
   ```bash
   go mod tidy
   ```
3. Run the server (defaults to embedded demo mode):
   ```bash
   go run main.go
   ```
   *Note: In embedded mode, Spec-Link spins up its own mock upstream API and serves the demo client UI from the `demo/client` directory.*

## Architecture Summary

```
┌──────────────────────────────────────────────────────────────────────┐
│  Client (Resolver SDK)                                               │
│       │                                                              │
│  GET /spec/resource/123                                              │
│       ▼                                                              │
│  ┌─────────────┐                                                     │
│  │  Spec-Link  │ ──── FORK ─────────────────────────────┐            │
│  │  (Proxy)    │                                        │            │
│  └─────────────┘                                        │            │
│       │                                                 │            │
│  FAST TRACK (ms)                              FORMAL TRACK (ms–s)    │
│  Read State-Vault ──► Stream speculative       Query real DB         │
│                       response                               │       │
│                                                Compare versions      │
│  Client renders ◄──────────────────────────── Send signal:           │
│  UI instantly                                  CONFIRM / PATCH /     │
│                                                REPLACE / ABORT       │
└──────────────────────────────────────────────────────────────────────┘
```

## License
MIT
