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
* [`ADOPT.md`](./ADOPT.md) - The 60-Minute Integration Guide for adopting Edge Proxy.
* [`packages/edge-proxy/`](./packages/edge-proxy) - High-performance Node middleware proxy implementing the read/write track logic with Fast-JSON-Patch and Upstash Redis.
* [`packages/resolver/`](./packages/resolver) - Standard JS SDK for stateful reconciliation.
* [`packages/react-query/`](./packages/react-query) - React Query integration hook for Antic-PT.
* [`packages/swr/`](./packages/swr) - SWR integration hook for Antic-PT.
* [`demo/react-test/`](./demo/react-test) - React testing application demonstrating the Edge Proxy.

## Running the Demo

The quickest way to see the "Certainty Layer" in action:

**React Demo:**
1. Navigate to the demo directory: `cd demo/react-test`
2. Install dependencies: `npm install`
3. Start the dev server: `npm run dev`
4. Open the displayed localhost URL.
5. Observe instantaneous UI rendering while the Formal Track updates underlying price changes.

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  Client (Resolver SDK)                                       │
│       │                                                      │
│  GET /spec/dashboard/1                                       │
│       ▼                                                      │
│  ┌──────────────┐                                            │
│  │  Edge Proxy  │ ─── FORK ───────────────────────┐           │
│  │              │                                 │           │
│  └──────────────┘                                 │           │
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
