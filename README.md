# Antic-PT: Anticipation Protocol (v0.2.2)

**Respond Before You're Asked.**

Antic-PT is an open, transport-agnostic protocol layer that transforms traditional REST APIs into **Surgical Certainty Layers**. It eliminates perceived network latency by treating API responses as field-graphs with explicit certainty classes.

1. **Fast Track**: Serves a cached speculative response immediately (sub-15ms).
2. **Formal Track**: Validates against the authoritative data source in the background.

Unlike traditional caching, Antic-PT provides **Field-Level Reconciliation** for reads and **Provisional Commits** for writes. It surgically corrects only what has drifted, providing immediate UI feedback while maintaining authoritative server control over the final outcome.

## Key Concepts (v0.2.2)

- **Surgical Reconciliation**: Uses JSON Patches (RFC 6902) to correct specific fields in a live UI without a full reload.
- **Certainty Classes**: Fields are classified as `SPECULATIVE` (render fast) or `DEFERRED` (withhold until verified).
- **Provisional Write Commits**: Submit writes safely with sub-15ms UI feedback, then receive an authoritative `CONFIRM` (with drift correction) or `ABORT` (with revert state) signal.
- **Multiplexed Signals**: A dedicated SSE channel (`/antic/signals`) handles all background reconciliation.

## Structure

* [`ANTIC-PT-SPEC.md`](./ANTIC-PT-SPEC.md) - The formal protocol specification (v0.2.2).
* [`ADOPT.md`](./ADOPT.md) - The 60-Minute Integration Guide for adopting Spec-Link.
* [`spec-link/`](./spec-link) - High-performance Go middleware proxy implementing the read/write track logic.
* [`packages/resolver-js/`](./packages/resolver-js) - Standard JS SDK for stateful reconciliation.
* [`integrations/binance/`](./integrations/binance) - Real-world demonstrations against the live Binance API (Read & Write side).

## Development Commands

Use the root `Makefile` for a consolidated workflow:

```bash
make build         # Build the Spec-Link proxy
make run           # Start the full demo stack (Proxy :4000 + Upstream :4001)
make test          # Run all tests
make fmt           # Format the codebase
```

## Running the Demo

The quickest way to see the "Certainty Layer" in action:

**Read-Side (Real-time Ticker Reconciliation):**
1. Run `make demo`
2. Open `http://localhost:4000`
3. Observe instantaneous UI rendering while the Formal Track updates underlying price changes.

**Write-Side (Provisional Order Commit):**
1. Run `make demo`
2. Open `http://localhost:4006`
3. Place an order to see instantaneous `202 Provisional` UI updates, followed by authoritative `CONFIRM` or `ABORT` corrections.

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  Client (Resolver SDK)                                       │
│       │                                                      │
│  GET /spec/dashboard/1                                       │
│       ▼                                                      │
│  ┌─────────────┐                                             │
│  │  Spec-Link  │ ─── FORK ───────────────────────┐           │
│  │  (Proxy)    │                                 │           │
│  └─────────────┘                                 │           │
│       │                                          │           │
│  FAST TRACK (~10ms)                FORMAL TRACK (~350ms)     │
│  Serve Speculative ───────────────► Query Authoritative DB   │
│  Response                                        │           │
│                                           Compare Fields     │
│  UI Renders ◄──────────────────────────── Send Signal:       │
│  Instantly                                PATCH / FILL /     │
│                                           CONFIRM            │
└──────────────────────────────────────────────────────────────┘
```

## License
MIT
