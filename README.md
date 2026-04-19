# Antic-PT: Anticipation Protocol (v0.2.1)

**Respond Before You're Asked.**

Antic-PT is an open, transport-agnostic protocol layer that transforms traditional REST APIs into **Surgical Certainty Layers**. It eliminates perceived network latency by forking a single incoming request into two concurrent execution tracks:

1. **Fast Track**: Serves a cached speculative response immediately (sub-15ms).
2. **Formal Track**: Validates against the authoritative data source in the background.

Unlike traditional caching, Antic-PT provides **Field-Level Reconciliation**. It surgically corrects only the fields that have drifted and withholds deferred fields that require absolute certainty, ensuring the UI remains both fast and honest.

## Key Concepts (v0.2.1)

- **Surgical Reconciliation**: Uses JSON Patches (RFC 6902) to correct specific fields in a live UI without a full reload.
- **Certainty Classes**: Fields are classified as `SPECULATIVE` (render fast) or `DEFERRED` (withhold until verified).
- **Multiplexed Signals**: A dedicated channel (`/antic/signals`) handles background reconciliation via `CONFIRM`, `PATCH`, and `FILL`.

## Structure

* [`ANTIC-PT-SPEC.md`](./ANTIC-PT-SPEC.md) - The formal protocol specification (v0.2.1).
* [`spec-link/`](./spec-link) - High-performance Go middleware proxy implementing the dual-track logic.
* [`packages/resolver-js/`](./packages/resolver-js) - Standard JS SDK for stateful reconciliation.
* [`demo/`](./demo) - "Certainty Layer" demonstration environment with pulsing skeletons and visual patch highlights.

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

1. **Run the stack**:
   ```bash
   make run
   ```
2. **Open the browser**:
   Navigate to `http://localhost:4000`.
3. **Observe**:
   Select the "Dashboard" and click **⚡ Antic-PT**. Watch the Revenue field correction (PATCH) and User Count skeletons (FILL) work in tandem.

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  Client (Resolver SDK)                                       │
│       │                                                      │
│  GET /spec/dashboard/1                                       │
│       ▼                                                      │
│  ┌─────────────┐                                             │
│  │  Spec-Link  │ ─── FORK ───────────────────────┐           │
│  │  (Proxy v0.2)│                                 │           │
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
