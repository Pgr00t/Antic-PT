# Antic-PT Protocol Specification
## Draft v0.2.2 — Partial Payload Reconciliation + Provisional Write Commits

---

## Abstract

HTTP APIs are dishonest by default. Every response presents itself as current truth, even when it is cached, eventually consistent, stale, mid-replication, or partially invalid. Clients have no standardized way to know the difference, and no standardized way to be told when the truth changes.

Antic-PT is a protocol extension for HTTP APIs that allows servers to return **provisional responses** with machine-readable deterministic metadata and **standardized reconciliation signals**. It operates at the field level — not the response level — allowing individual JSON fields within a payload to carry different certainty classes, so that safe fields render instantly while unsafe fields wait for authoritative confirmation.

Antic-PT does not replace HTTP caching. It replaces the silence that follows it.

---

## Status of This Document

Draft v0.2.2. Not yet submitted for standardization. Reference implementation available as Spec-Link (Go proxy) and AnticipationResolver (JavaScript SDK). Section 13 (Provisional Write Commits) is fully specified; implementation targets v1.0.

---

## Table of Contents

1. Motivation
2. Terminology
3. Core Concepts
4. Field Classification Model
5. Protocol Headers
6. Reconciliation Signal Vocabulary
7. Signal Delivery Order
8. The Dual-Track Request Lifecycle
9. Client SDK Contract
10. Spec-Link Proxy Behavior
11. Failure Modes and Required Behavior
12. Scope and Non-Goals
13. Future Extension: Provisional Write Commits
14. Comparison with Existing Patterns
15. Appendix A: Header Reference
16. Appendix B: Proxy Configuration Reference

---

## 1. Motivation

### The Problem

Consider a standard API response for an operational dashboard:

```json
{
  "serverId": "srv-042",
  "region": "us-east-1",
  "cpuUsage": 0.73,
  "memoryUsage": 0.61,
  "activeAlarms": 2,
  "accountBalance": 1842.50,
  "uptimeDays": 312
}
```

Today, this payload has one of two states from the client's perspective: arrived or not arrived. If the server takes 300ms to respond, the client waits 300ms and renders nothing. If the server returns a cached response, the client renders it as truth with no indication it may be stale.

The actual certainty picture is far more nuanced:

| Field | True Certainty |
|---|---|
| `serverId` | Immutable. Cache indefinitely. |
| `region` | Immutable. Cache indefinitely. |
| `cpuUsage` | Changes every second. Stale after 2s. |
| `memoryUsage` | Changes every second. Stale after 2s. |
| `activeAlarms` | Critical. Must never speculate. |
| `accountBalance` | Financial. Must never speculate. |
| `uptimeDays` | Changes once per day. Speculate freely. |

No existing HTTP caching mechanism expresses this distinction at field granularity. Antic-PT does.

### What Antic-PT Provides

- A field classification model so servers can declare which fields are safe to speculate
- A deterministic metadata vocabulary so clients know exactly what they were served and why
- A standardized reconciliation signal set so servers can correct clients with surgical precision
- A transport-agnostic design that works over SSE, WebSocket, or polling equally
- Full compatibility with existing REST APIs — no schema changes required on the upstream service

---

## 2. Terminology

**Fast Track**: The path that returns a speculative response from the State Vault immediately, in milliseconds.

**Formal Track**: The path that queries the authoritative upstream data source concurrently with the Fast Track.

**State Vault**: The proxy-side cache that stores the most recently validated response for a given resource.

**Vault Snapshot**: An immutable copy of the State Vault entry captured at the moment the Fast Track response is served. Used as the diff baseline for the corresponding Formal Track. Distinct from the live vault entry, which may be updated by concurrent requests.

**Reconciliation Signal**: A structured message sent by the proxy to the client after the Formal Track completes, indicating the nature of any discrepancy.

**Speculative Response**: A response served from the State Vault before the Formal Track has completed.

**Provisional Response**: A server-side response to a write before the write has fully committed. (Defined for future use — see Section 13.)

**Reconcile ID**: A unique identifier linking a speculative Fast Track response to its subsequent reconciliation signal. Scoped to a single request. Never reused.

**Staleness**: The elapsed time in milliseconds between when the State Vault entry was last validated and when the Fast Track response was served.

**Volatility**: A developer-declared hint about how frequently a field or endpoint value is expected to change. Values: `high`, `low`, `invariant`. May be declared per-field or per-endpoint as a fallback default.

**REPLACE Threshold**: The fraction of SPECULATIVE fields that must differ between the Vault Snapshot and the Formal Track response before REPLACE is triggered instead of PATCH. Configurable per endpoint. Default: `0.5`.

---

## 3. Core Concepts

### 3.1 The Protocol Reports Facts, Not Opinions

Antic-PT does not emit a confidence score. Confidence is business logic — a 30-second-stale stock price and a 30-second-stale server uptime counter have entirely different implied reliability, and the proxy cannot know the difference.

Antic-PT exposes only deterministic facts the proxy genuinely knows:
- How old is this data? (`X-Antic-Staleness`)
- What did the developer declare about each field's volatility? (`X-Antic-Volatility`)
- What certainty class does each field carry? (`X-Antic-State`)

The client SDK computes rendering decisions from those facts. This keeps the protocol honest and the developer in control.

### 3.2 Field-Level Granularity

Antic-PT operates at the JSON field level, not the response level. A single API response can contain fields of different certainty classes simultaneously. The Fast Track serves what it safely can. The Formal Track delivers only what changed, as a minimal patch.

### 3.3 Reconciliation Is Always Explicit

When the Formal Track completes, the proxy always sends a reconciliation signal, even if nothing changed. Silence is not confirmation. A `CONFIRM` signal is required to close the uncertainty window.

### 3.4 Concurrent Requests Use Vault Snapshots

When multiple in-flight requests target the same resource concurrently, each Formal Track diffs against the Vault Snapshot taken at the moment its corresponding Fast Track was served — not the current live vault state at Formal Track completion time.

This means if Request A's Formal Track completes and updates the live vault before Request B's Formal Track completes, Request B still diffs against the vault as it was when B was served. Each request receives a correct per-request reconciliation regardless of interleaving.

---

## 4. Field Classification Model

Every field in a speculative response belongs to exactly one of four classes. Classification is declared in proxy configuration per field, with an endpoint-level default for unclassified fields.

### 4.1 SPECULATIVE

The field may be served from the State Vault immediately. If the Formal Track finds a different value, a `PATCH` signal is sent containing only this field's update.

**Appropriate for**: Metrics, counters, usage stats, any read-heavy value where a brief moment of staleness is tolerable.

**Example fields**: `cpuUsage`, `pageViews24h`, `queueDepth`, `latencyP99`

### 4.2 DEFERRED

The field must not be served from the State Vault. Its value is withheld from the Fast Track response (returned as `null` or omitted) and delivered only after the Formal Track completes via a `FILL` signal.

DEFERRED fields never pass through a speculative state. They transition directly from withheld to confirmed.

**Appropriate for**: Financial values, alarm counts, security-relevant state, any field where a wrong speculative render has meaningful consequences.

**Example fields**: `accountBalance`, `activeAlarms`, `permissionLevel`, `transactionStatus`

### 4.3 INVARIANT

The field never changes for this resource identifier. Cache it indefinitely. Never include it in any reconciliation signal.

If an INVARIANT field is found to differ between the Vault Snapshot and the Formal Track response, this is treated as an integrity violation and triggers ABORT with `reason: invariant_violation`. The proxy must not silently patch an INVARIANT field.

**Appropriate for**: Identifiers, region assignments, creation timestamps, schema constants.

**Example fields**: `serverId`, `region`, `createdAt`, `resourceType`

### 4.4 PROVISIONAL

Reserved for write-path use (see Section 13). On read paths, treat as DEFERRED.

---

### 4.5 Field Class Declaration

Field classes are declared in Spec-Link proxy configuration. Upstream services require zero changes.

**Spec-Link configuration example:**

```yaml
endpoints:
  - path: /api/servers/:id
    volatility: low              # endpoint-level fallback for unlisted fields
    max_staleness_ms: 5000
    replace_threshold: 0.5       # fraction of changed SPECULATIVE fields → REPLACE
    default_class: SPECULATIVE
    fields:
      serverId:
        class: INVARIANT
      region:
        class: INVARIANT
      cpuUsage:
        class: SPECULATIVE
        volatility: high         # per-field override
      memoryUsage:
        class: SPECULATIVE
        volatility: high
      uptimeDays:
        class: SPECULATIVE
        volatility: low
      activeAlarms:
        class: DEFERRED
      accountBalance:
        class: DEFERRED
```

Fields not listed inherit the endpoint-level `volatility` and default to class SPECULATIVE. Operators must explicitly classify fields with financial, security, or alarm semantics as DEFERRED rather than relying on defaults.

---

## 5. Protocol Headers

Antic-PT uses custom HTTP response headers to communicate the certainty state of a response.

### 5.1 Required Headers (on speculative responses)

#### `X-Antic-State`
The certainty state of this response.

| Value | Meaning |
|---|---|
| `speculative` | Served from State Vault. Formal Track in progress. |
| `confirmed` | Served directly from upstream. No reconciliation needed. |
| `deferred` | Some fields withheld. Formal Track required for complete data. |

```http
X-Antic-State: speculative
```

#### `X-Antic-Reconcile-Id`
A unique opaque identifier linking this response to its reconciliation signal. Generated per request. Never reused.

```http
X-Antic-Reconcile-Id: arc_9f3a82b1
```

#### `X-Antic-Staleness`
The age of the State Vault entry at the moment the Fast Track response was served, in milliseconds.

```http
X-Antic-Staleness: 340
```

### 5.2 Optional Headers

#### `X-Antic-Volatility`
Per-field volatility hints as a comma-separated key=value list. Fields not listed inherit the endpoint-level default. The SDK parses this into a per-field map for granular rendering decisions.

```http
X-Antic-Volatility: cpuUsage=high, memoryUsage=high, uptimeDays=low, region=invariant
```

This header is per-field, not a single endpoint-level summary. Expressing it as a field-keyed list is required for the SDK to make per-field rendering decisions. A single endpoint-level value would be insufficient when the endpoint contains fields of mixed volatility.

#### `X-Antic-Deferred-Fields`
Comma-separated list of field names withheld from this response, to be delivered via FILL signal.

```http
X-Antic-Deferred-Fields: activeAlarms, accountBalance
```

#### `X-Antic-Max-Window`
Maximum time in milliseconds the client should wait before treating the reconciliation signal as lost and falling back to a direct fetch. The proxy must emit a signal or ABORT before this window expires.

```http
X-Antic-Max-Window: 3000
```

---

## 6. Reconciliation Signal Vocabulary

Reconciliation signals are delivered over a secondary channel after the Formal Track completes. The default transport is Server-Sent Events (SSE). The signal schema is transport-agnostic.

### 6.1 Signal Envelope

Every signal shares a common envelope:

```json
{
  "signal": "PATCH",
  "id": "arc_9f3a82b1",
  "timestamp": 1718200032441
}
```

### 6.2 CONFIRM

The Formal Track validated the Fast Track response. No SPECULATIVE fields differ and no DEFERRED fields were present. CONFIRM is the terminal signal when no corrections are needed.

```json
{
  "signal": "CONFIRM",
  "id": "arc_9f3a82b1",
  "timestamp": 1718200032441
}
```

**Client required behavior**: Transition all SPECULATIVE fields to confirmed state. Remove any syncing visual indicators. CONFIRM is only emitted when there is no accompanying PATCH or FILL.

### 6.3 PATCH

One or more SPECULATIVE fields differ between the Vault Snapshot and the Formal Track response. The delta is a standard JSON Patch document per RFC 6902.

```json
{
  "signal": "PATCH",
  "id": "arc_9f3a82b1",
  "timestamp": 1718200032441,
  "ops": [
    { "op": "replace", "path": "/cpuUsage", "value": 0.81 },
    { "op": "replace", "path": "/memoryUsage", "value": 0.58 }
  ]
}
```

**Client required behavior**: Apply the RFC 6902 patch operations to the current rendered state. Only listed fields change. All unlisted SPECULATIVE fields are confirmed as correct. If DEFERRED fields were declared (`X-Antic-Deferred-Fields` non-empty), do not finalize confirmed state until FILL is also processed.

**Implementation note**: RFC 6902 is used verbatim. Implementations must not invent a custom diff format.

### 6.4 FILL

Delivers values for DEFERRED fields withheld from the Fast Track response.

```json
{
  "signal": "FILL",
  "id": "arc_9f3a82b1",
  "timestamp": 1718200032441,
  "fields": {
    "activeAlarms": 2,
    "accountBalance": 1842.50
  }
}
```

**Client required behavior**: Insert the provided field values into the rendered state. FILL fields transition from withheld to confirmed immediately — they never pass through a speculative state. FILL is always delivered after PATCH when both are needed (see Section 7).

### 6.5 REPLACE

The Formal Track response diverges beyond the configured `replace_threshold`, or a structural change is detected. The client replaces the full resource state.

```json
{
  "signal": "REPLACE",
  "id": "arc_9f3a82b1",
  "timestamp": 1718200032441,
  "data": { }
}
```

**REPLACE trigger conditions** (any one is sufficient):
- The fraction of SPECULATIVE fields with different values meets or exceeds `replace_threshold` (default `0.5`)
- Any field is added to the response that was not in the Vault Snapshot
- Any field is removed from the response that was in the Vault Snapshot
- Any field's value type changes between the Vault Snapshot and the Formal Track response

Structural changes always trigger REPLACE regardless of `replace_threshold`. REPLACE subsumes both PATCH and FILL — no separate FILL signal is sent when REPLACE is emitted.

**Client required behavior**: Replace the entire resource state with `data`. Treat as a full re-render.

### 6.6 ABORT

The Formal Track failed, timed out, or detected an integrity violation.

```json
{
  "signal": "ABORT",
  "id": "arc_9f3a82b1",
  "timestamp": 1718200032441,
  "reason": "upstream_timeout",
  "retryable": true
}
```

**Reason values**:

| Reason | Retryable | Description |
|---|---|---|
| `upstream_timeout` | true | Formal Track did not complete within `X-Antic-Max-Window` |
| `upstream_error` | true | Upstream returned a non-2xx response |
| `schema_mismatch` | false | Response structure incompatible with field configuration |
| `auth_revoked` | false | Upstream rejected authentication during Formal Track |
| `invariant_violation` | false | An INVARIANT field had a different value in Formal Track |
| `connection_lost` | true | SSE connection dropped; synthetic signal emitted by SDK |

**Client required behavior**: Mark the resource state as unconfirmed. If `retryable: true`, the SDK initiates a direct fetch after the `X-Antic-Max-Window` backoff. If `retryable: false`, display an error state and do not re-speculate on this resource until the State Vault entry is explicitly invalidated.

---

## 7. Signal Delivery Order

When a Formal Track results in both field corrections and deferred field values, the proxy emits signals as separate SSE events in a strictly defined order:

```
1. PATCH    (if any SPECULATIVE fields differ)
2. FILL     (if any DEFERRED fields are present)
```

PATCH is always emitted before FILL. If there are no SPECULATIVE field differences, PATCH is omitted and FILL is emitted directly. If there are no DEFERRED fields, FILL is omitted. When neither PATCH nor FILL is needed, CONFIRM is emitted instead.

REPLACE and ABORT are terminal signals. When either is emitted, no further signals are sent for that Reconcile ID.

### 7.1 SDK Buffering Requirement

The SDK handles out-of-order signal arrival defensively:

- If FILL is received before PATCH for the same Reconcile ID, buffer the FILL for a maximum of 50ms
- If PATCH arrives within the buffer window, process PATCH then FILL in order
- If the buffer window expires without PATCH, process FILL directly

Proxies must not rely on the SDK's buffer window to paper over delivery ordering failures. Correct proxies always emit in the specified order. The buffer is a defensive client measure for edge cases, not an acceptance of proxy non-compliance.

---

## 8. The Dual-Track Request Lifecycle

```
Client
  │
  │  GET /spec/resource/id
  ▼
Spec-Link Proxy
  │
  ├──── FAST TRACK ────────────────────────────────────────────┐
  │     Check State Vault for resource/id                      │
  │     If cold miss → skip to Formal Track only               │
  │     If stale (age > max_staleness_ms) → same as cold miss  │
  │     If fresh:                                              │
  │       Capture Vault Snapshot (immutable, per-request)      │
  │       Apply field classification:                          │
  │         INVARIANT  → include as-is                         │
  │         SPECULATIVE → include from snapshot                │
  │         DEFERRED   → omit (return null)                    │
  │       Attach X-Antic-* headers                             │
  │       Return HTTP 200 immediately ─────────────────────► Client
  │                                                           renders
  │                                                           instantly
  ├──── FORMAL TRACK ──────────────────────────────────────────┤
  │     Query upstream API / database                          │
  │     Check INVARIANT fields — violation → ABORT             │
  │     Diff against Vault Snapshot (RFC 6902)                 │
  │     Update live State Vault                                │
  │     Determine signal sequence:                             │
  │       No diff, no deferred    → CONFIRM                    │
  │       SPECULATIVE diff only   → PATCH                      │
  │       DEFERRED fields only    → FILL                       │
  │       Both                    → PATCH then FILL            │
  │       Threshold/structural    → REPLACE                    │
  │       Upstream failure        → ABORT                      │
  │     Emit signal(s) over /antic/signals SSE ─────────────► Client
  │                                                           reconciles
  ▼
 Done
```

### 8.1 Cold Vault / Stale Behavior

When no State Vault entry exists, or the entry exceeds `max_staleness_ms`, Spec-Link proxies directly to upstream, returns the response with `X-Antic-State: confirmed`, and populates the State Vault. No Reconcile ID is issued. No signal is sent.

### 8.2 Signal Channel

All reconciliation signals for all in-flight requests share a single persistent SSE connection per client, established at:

```
GET /antic/signals?client_id=<uuid>
```

The `client_id` is generated once by the SDK and stable for the lifetime of the session (persisted in `localStorage` when available). Signals are matched to requests client-side via `X-Antic-Reconcile-Id`.

---

## 9. Client SDK Contract

The reference SDK is a transport-agnostic vanilla JavaScript state machine. Framework integrations (React, Vue, Svelte) are thin wrappers around this core.

### 9.1 Core API

```javascript
const resolver = new AnticipationResolver('http://localhost:4000/spec/servers/042', {
  baseUrl: '',        // base URL prefix (optional, defaults to window.location.origin)
  maxWindow: 3000,
  onTimeout: 'refetch'   // 'refetch' | 'error'
});

resolver.on('speculative', (data, meta) => {
  // data: full response with DEFERRED fields as null
  // meta: { staleness, volatility (per-field map), reconciledId, deferredFields }
  renderUI(data);
  showSyncIndicator();
});

resolver.on('patch', (ops) => {
  // ops: RFC 6902 JSON Patch array
  applyPatch(currentData, ops);
  // do not hide sync indicator yet — FILL may follow
});

resolver.on('fill', (fields) => {
  mergeFields(currentData, fields);
  hideSyncIndicator();
});

resolver.on('confirm', () => {
  hideSyncIndicator();
});

resolver.on('replace', (data) => {
  replaceUI(data);
  hideSyncIndicator();
});

resolver.on('abort', (reason, retryable) => {
  showErrorState(reason);
  if (retryable) resolver.refetch();
});

resolver.on('speculationAbandoned', (meta) => {
  // meta: { reconciledId, reason, elapsed, resource }
  metrics.increment('antic.speculation_abandoned', { resource: meta.resource, reason: meta.reason });
});

resolver.fetch();
```

### 9.2 Per-Field Volatility Access

The SDK parses `X-Antic-Volatility` into a field-keyed map, enabling per-field rendering decisions:

```javascript
resolver.on('speculative', (data, meta) => {
  // meta.volatility: { cpuUsage: 'high', uptimeDays: 'low', region: 'invariant', ... }
  Object.entries(data).forEach(([field, value]) => {
    const vol = meta.volatility[field] ?? meta.endpointVolatility;
    if (vol === 'high') renderWithPulseIndicator(field, value);
    else renderNormal(field, value);
  });
});
```

### 9.3 Static Helper: applyPatch

```javascript
const patched = AnticipationResolver.applyPatch(currentData, ops);
// RFC 6902 patch applied to a shallow copy. Original is not mutated.
```

### 9.4 React Hook (Reference Wrapper)

```javascript
const { data, deferredFields, meta, status, refetch } = useAnticipation('/spec/servers/042', {
  baseUrl: 'http://localhost:4000'
});
// status: 'idle' | 'speculative' | 'confirmed' | 'patching' | 'filling' | 'error'
// meta: { staleness, volatility (per-field map), reconciledId, deferredFields }
// deferredFields: array of field names not yet available
```

### 9.5 Speculation Abandoned Event

When `onTimeout: 'refetch'` is configured and the SDK falls back to a direct fetch because the Formal Track did not deliver a signal within `X-Antic-Max-Window`, the SDK must emit an observable `speculationAbandoned` event before initiating the fallback fetch.

Silent fallback to direct fetch without this event is not permitted.

### 9.6 SDK Must Not

- Apply speculative values to DEFERRED fields under any circumstance
- Emit a `confirm` event before the reconciliation signal is received
- Silently drop an ABORT signal
- Assume a lost SSE connection means CONFIRM — treat as ABORT with `reason: connection_lost`
- Apply a FILL before processing a pending PATCH for the same Reconcile ID, except after the 50ms buffer window expires

---

## 10. Spec-Link Proxy Behavior

### 10.1 State Vault

- Stores the most recent Formal Track response per resource path + query string
- Each entry includes: response body, field classification map, timestamp of last validation
- Eviction policy is operator-configurable (TTL, LRU, explicit invalidation)
- Supported backends: in-memory (default), Redis

**Eviction vs. staleness**: LRU eviction and `max_staleness_ms` are independent gates. An entry can be rejected by either. If LRU evicts before `max_staleness_ms` expires, the next request is a cold miss.

### 10.2 Vault Snapshot Capture

At the moment Spec-Link begins serving a Fast Track response, it captures an immutable Vault Snapshot keyed to the request's Reconcile ID. This snapshot is the sole diff baseline for that request's Formal Track. The live State Vault entry may be updated by concurrent requests before this Formal Track completes — the snapshot is unaffected.

### 10.3 REPLACE Threshold Evaluation

```
structural_change   = any field added, removed, or type-changed
speculative_total   = count of fields classified SPECULATIVE
changed_speculative = count of SPECULATIVE fields differing from snapshot
fraction_changed    = changed_speculative / speculative_total

if structural_change:
    emit REPLACE
elif fraction_changed >= replace_threshold:
    emit REPLACE
else:
    emit PATCH (changed fields) + FILL (deferred fields, if any)
```

`replace_threshold` default: `0.5`. Configurable per endpoint.

### 10.4 Signal Delivery Guarantee

Spec-Link emits exactly one terminal signal sequence per in-flight Fast Track response. A terminal sequence ends with CONFIRM, REPLACE, or ABORT. PATCH and FILL are non-terminal and must be followed by no further signals for that Reconcile ID.

### 10.5 SSE Signal Channel (`/antic/signals`)

```
GET /antic/signals?client_id=<uuid>
Accept: text/event-stream
```

- One persistent SSE connection per `client_id`
- All signals for all in-flight requests delivered over this connection
- Signals matched to requests by the client via `X-Antic-Reconcile-Id`
- On connection drop: proxy buffers pending signals for duration of maximum active `X-Antic-Max-Window`
- On reconnect: proxy replays buffered signals in emission order; expired signals are dropped
- 15-second keepalive heartbeat comments (`": heartbeat"`) to prevent proxy timeouts

### 10.6 Concurrent Request Handling

For concurrent requests targeting the same resource:
- Each request receives its own unique Reconcile ID
- Each request captures its own independent Vault Snapshot at Fast Track serve time
- Each Formal Track diffs against its own snapshot, not the current live vault
- Separate signals are emitted per Reconcile ID
- Signals must not be collapsed or deduplicated across Reconcile IDs

---

## 11. Failure Modes and Required Behavior

### 11.1 SSE Connection Drop Before Signal Arrives

**Proxy**: Buffer pending signals for the maximum active `X-Antic-Max-Window`. Replay on reconnect.
**SDK**: Attempt reconnect. If individual window expires, emit synthetic ABORT with `reason: connection_lost, retryable: true`. Client must refetch directly.

### 11.2 Formal Track Slower Than Max Window

**Proxy**: Emit ABORT with `reason: upstream_timeout, retryable: true` at window expiry. Never wait indefinitely. Never silently confirm.

### 11.3 INVARIANT Field Value Changes

**Proxy**: Emit ABORT with `reason: invariant_violation, retryable: false`. Log the field name and both values. Update State Vault with new authoritative response. Do not silently patch.
**SDK**: Display error state. Do not re-speculate. Invariant violation requires operator investigation.

### 11.4 Cold Vault / Stale Vault on Request

**Proxy**: Bypass Fast Track entirely. Proxy directly to upstream. Return `X-Antic-State: confirmed`. No Reconcile ID. No signal. Populate vault with result.

### 11.5 Signal Received for Unknown Reconcile ID

**SDK**: Discard the signal silently. Do not apply to unknown state. Do not emit an error.

### 11.6 FILL Received Before PATCH

**SDK**: Buffer FILL for a maximum of 50ms. If PATCH arrives within the window, process PATCH then FILL in order. If window expires, process FILL directly.

---

## 12. Scope and Non-Goals

### In Scope

- Read-heavy HTTP endpoints returning JSON
- Field-level partial speculation with explicit classification
- Standardized reconciliation signal delivery over SSE
- REST API compatibility with zero upstream changes
- Operational dashboards, monitoring UIs, analytics interfaces, admin panels

### Out of Scope in v0.2

- Write-side provisional commits (see Section 13)
- GraphQL or gRPC specific bindings (community extensions welcome)
- Real-time streaming data sources
- Binary response formats
- Authentication or authorization logic

### Non-Goals

- Replacing HTTP cache-control directives — Antic-PT complements them
- Providing a CDN or edge deployment solution — topology is operator choice
- Guaranteeing sub-10ms Fast Track — that is a deployment concern, not a protocol concern

---

## 13. Provisional Write Commits (v1.0 Specification)

This section defines the Antic-PT v1.0 write-side extension. It is not implemented in v0.2. The read-path signal vocabulary in Sections 6–8 is designed to be compatible with this extension without modification.

---

### 13.1 The Problem

Client-side optimism (the client guesses what the server will return on a write) is architecturally unsound: the client is the least informed party and has no standardized mechanism to be corrected. Blocking UX (the client waits for full server confirmation) is slow. Neither is acceptable.

The gap is not in the client's rendering strategy — it is in the protocol's write-side semantics. There is no standardized mechanism for a server to say "I am provisionally accepting this write; here is what you may render now; I will correct you if the final outcome differs."

---

### 13.2 The Provisional Commit Model

In Antic-PT v1.0, a server may respond to a write (POST, PUT, PATCH, DELETE) with a **provisional commit**:

```http
POST /api/orders HTTP/1.1
Content-Type: application/json
X-Antic-Client-Id: 7f3a82b1-9c4d-4f2e-b3a1-2d8e6f9b0c3a

HTTP/1.1 202 Accepted
X-Antic-State: provisional
X-Antic-Reconcile-Id: arc_c7f29a1b
X-Antic-Max-Window: 5000

{
  "orderId": "ord_9a3b2c1d",
  "symbol": "BTCUSDT",
  "side": "BUY",
  "quantity": "0.01",
  "price": "74800.00",
  "status": "PENDING"
}
```

The client renders the provisional response immediately. The server emits `CONFIRM` or `ABORT` when the transaction finalizes.

**`202 Accepted` is mandatory** for provisional responses. `200 OK` must not be used for provisional writes — it implies finality the server is not yet asserting.

PROVISIONAL fields (Section 4.4) are fields in the write response whose final values are not yet confirmed. The client renders them as speculative pending confirmation.

---

### 13.3 Write-Side Signal Vocabulary

The read-side signals (`PATCH`, `FILL`, `REPLACE`) are not emitted for provisional writes. Writes use `CONFIRM` and `ABORT` only.

#### 13.3.1 CONFIRM on a Write

`CONFIRM` closes the provisional window with a successful outcome. It carries an optional `data` field.

```json
{
  "signal": "CONFIRM",
  "id": "arc_c7f29a1b",
  "timestamp": 1718200032441,
  "data": {
    "orderId": "ord_9a3b2c1d",
    "symbol": "BTCUSDT",
    "side": "BUY",
    "quantity": "0.01",
    "price": "74801.23",
    "status": "FILLED",
    "filledAt": 1718200032100
  }
}
```

**If `data` is absent**: The provisional response is treated as exact. The client marks all PROVISIONAL fields as confirmed without modification.

**If `data` is present**: The client replaces the provisionally rendered state with `data` entirely, as if a read-side `REPLACE` signal had been received. This handles the case where the confirmed outcome differs from the provisional response — e.g., a limit order filled at a slightly different price due to fee adjustments or partial fills.

**Client required behavior**: Apply `data` if present; mark provisional state confirmed; remove all speculative UI indicators.

#### 13.3.2 ABORT on a Write

`ABORT` closes the provisional window with a rejected outcome. It carries `reason`, `retryable`, and an optional `state` field.

```json
{
  "signal": "ABORT",
  "id": "arc_c7f29a1b",
  "timestamp": 1718200032441,
  "reason": "insufficient_funds",
  "retryable": false,
  "state": {
    "accountBalance": "342.18",
    "availableBalance": "298.40"
  }
}
```

**`reason`**: A machine-readable rejection reason. Write-specific reason values are defined in Section 13.4.

**`retryable`**: Whether the same write may be retried. `false` for business-rule rejections (insufficient funds, invalid parameters). `true` for infrastructure failures (upstream timeout during commit).

**`state`**: Optional. The authoritative resource state the client should revert to. Provides exactly the fields the client needs to undo the provisional render — not necessarily the full resource. If `state` is absent, the client must treat the resource as unconfirmed and initiate a direct fetch to recover ground truth.

**Difference from read-side ABORT**: On reads, `state` is never present because the read never mutated anything. On writes, `state` is the server's correction of the client's provisional render — the "here is where you actually are" signal that makes silent client-side guessing unnecessary.

**Client required behavior**: Revert provisional UI state. If `state` is present, apply it. If `state` is absent, mark resource as unconfirmed and initiate a direct fetch. Surface `reason` in the UI — do not silently revert. The user must be told what happened.

#### 13.3.3 Write-Side ABORT Reason Values

| Reason | Retryable | Description |
|---|---|---|
| `insufficient_funds` | false | Write rejected: sender lacks sufficient balance |
| `invalid_parameters` | false | Write rejected: request parameters failed server validation |
| `rate_limited` | true | Write rejected: client has exceeded request rate threshold |
| `conflict` | false | Write rejected: resource was concurrently modified |
| `upstream_timeout` | true | Commit did not finalize within `X-Antic-Max-Window` |
| `upstream_error` | true | Upstream returned a non-2xx response during commit |
| `write_conflict` | false | A concurrent provisional write is in-flight for this resource (see Section 13.5) |
| `connection_lost` | true | SSE connection dropped; synthetic signal emitted by SDK |

---

### 13.4 New Request Header: `X-Antic-Write-Mode`

Governs how the proxy handles concurrent provisional writes targeting the same resource.

```http
X-Antic-Write-Mode: exclusive
```

| Value | Behavior |
|---|---|
| `exclusive` | Default. Proxy rejects new write with `409 Conflict` and `X-Antic-State: write_rejected` if a provisional write is already in-flight for this resource. Client receives no `Reconcile-Id`; the rejection is synchronous. |
| `independent` | Proxy accepts the write and issues a new `Reconcile-Id`. No ordering guarantee between concurrent writes. Use only when writes are known to be non-overlapping (e.g., writes to separate sub-resources). |

`exclusive` is the required default. Proxies must not silently accept a second write while one is in-flight — the resulting state is undefined and the client cannot reason about it.

---

### 13.5 Concurrent Write Semantics

When two provisional writes target the same resource before either confirms:

**Under `exclusive` mode (default)**:
- The second write is rejected synchronously with `409 Conflict`
- Response headers include `X-Antic-State: write_rejected` and `X-Antic-Reconcile-Id` of the in-flight write
- The client surfaces the conflict to the user immediately
- No signal is emitted for the rejected write

**Under `independent` mode**:
- Both writes proceed with independent `Reconcile-Id` values
- Each resolves independently via its own `CONFIRM` or `ABORT`
- The proxy makes no ordering guarantees between them
- If Write A's committed state conflicts with Write B's provisional assumption, Write B's eventual `CONFIRM` or `ABORT` carries the authoritative resolution

Operators using `independent` mode accept full responsibility for write ordering correctness. The protocol expresses what happened; it does not prevent conflicts in independent mode.

---

### 13.6 Provisional Write Lifecycle

```
Client
  │
  │  POST /api/orders
  │  X-Antic-Client-Id: <uuid>
  │  X-Antic-Write-Mode: exclusive (default)
  ▼
Spec-Link Proxy
  │
  ├── Check in-flight write registry for this resource ──────────────────┐
  │   If in-flight write exists (exclusive mode):                        │
  │     Return 409 Conflict, X-Antic-State: write_rejected               │
  │     (No signal emitted — rejection is synchronous)                   │
  │                                                                      │
  │   If no conflict:                                                    │
  │     Register resource as write-locked                                │
  │     Forward write to upstream                                        │
  │     Await provisional acknowledgement                                │
  │                                                                      │
  ├── PROVISIONAL RESPONSE ─────────────────────────────────────────── Client
  │   202 Accepted                                                       renders
  │   X-Antic-State: provisional                                         provisional
  │   X-Antic-Reconcile-Id: arc_c7f29a1b                                 state
  │   X-Antic-Max-Window: 5000                                           immediately
  │   { "status": "PENDING", "orderId": "...", ... }
  │
  ├── COMMIT TRACK (async) ─────────────────────────────────────────────┤
  │   Await upstream commit confirmation                                  │
  │   On commit success:                                                  │
  │     Release write lock                                                │
  │     Emit CONFIRM (with data if outcome differs from provisional)      │
  │   On commit failure:                                                  │
  │     Release write lock                                                │
  │     Emit ABORT (with reason and state for revert)                    │
  │   On timeout (X-Antic-Max-Window exceeded):                           │
  │     Release write lock                                                │
  │     Emit ABORT (reason: upstream_timeout, retryable: true)           │
  │                                                                      │
  │   Emit signal over /antic/signals SSE ─────────────────────────── Client
  │                                                                      reconciles
  ▼
 Done
```

---

### 13.7 Why This Requires v1.0

Provisional writes require:

1. **Durable write-lock registration**: The proxy must track in-flight writes across requests. An in-memory store is insufficient for multi-instance deployments.
2. **Server-side provisional state persistence**: The provisional response body must be stored for potential revert if the SSE connection drops before ABORT is delivered.
3. **Maximum uncertainty window SLAs**: Unlike reads (where a stale speculative response is merely imprecise), a provisional write that never confirms leaves real-world state (an order, a payment, an account update) in an undefined condition. The proxy must guarantee signal delivery or explicitly emit ABORT within the window.
4. **Write-lock release guarantees**: Exclusive locks must be released on CONFIRM, ABORT, or timeout — never left dangling. Dangling locks require manual operator intervention.

The read-side work in v0.2 builds the trust foundation in the signal vocabulary, delivery mechanism, and client SDK contract that provisional writes require before operators will trust the protocol with write semantics.

---

## 14. Comparison with Existing Patterns

| Capability | stale-while-revalidate | React Query | SWR | Antic-PT v0.2 |
|---|---|---|---|---|
| Field-level certainty classes | No | No | No | Yes |
| Per-field volatility metadata | No | No | No | Yes |
| DEFERRED fields (never speculate) | No | No | No | Yes |
| Deterministic staleness header | No | No | No | Yes |
| RFC 6902 patch delivery | No | No | No | Yes |
| Structured reconciliation signals | No | No | No | Yes |
| Works outside browser/React | Partial | No | No | Yes |
| REPLACE threshold configurable | N/A | No | No | Yes |
| Zero upstream API changes | Yes | Yes | Yes | Yes |
| Write-side provisional commits | No | Partial | No | Planned v1.0 |

**Key distinction**: stale-while-revalidate, React Query, and SWR treat a response as a single unit — either fresh or stale, replace all or replace nothing. Antic-PT treats a response as a field graph where each node carries an explicit certainty class. Reconciliation is surgical: only what changed is delivered, only fields safe to speculate are served early, and the client is always explicitly told what happened.

---

## Appendix A: Header Reference

| Header | Required | Values | Description |
|---|---|---|---|
| `X-Antic-State` | Yes (speculative) | `speculative`, `confirmed`, `provisional`, `write_rejected` | Certainty state of this response |
| `X-Antic-Reconcile-Id` | Yes (speculative/provisional) | Opaque string `arc_<hex>` | Links response to reconciliation signal |
| `X-Antic-Staleness` | Yes (speculative) | Integer (ms) | Age of Vault entry at serve time |
| `X-Antic-Volatility` | No | `field=level,...` | Per-field volatility hints, comma-separated key=value |
| `X-Antic-Deferred-Fields` | No | Comma-separated names | Fields withheld pending Formal Track |
| `X-Antic-Max-Window` | No | Integer (ms) | Maximum time client waits for signal |
| `X-Antic-Client-Id` | No (request header) | UUID | Client sends to proxy for signal channel routing |
| `X-Antic-Write-Mode` | No (request header) | `exclusive`, `independent` | Write concurrency mode. Default: `exclusive` |

---

## Appendix B: Proxy Configuration Reference

| Key | Scope | Default | Description |
|---|---|---|---|
| `port` | global | `4000` | Port Spec-Link listens on |
| `prefix` | global | `/spec` | URL prefix that triggers Antic-PT logic |
| `vault.driver` | global | `memory` | State Vault backend: `memory` or `redis` |
| `vault.default_ttl_ms` | global | `30000` | Default vault entry TTL in milliseconds |
| `formal_track.upstream` | global | required | Base URL of the authoritative upstream API |
| `formal_track.timeout_ms` | global | `5000` | Max time to wait for upstream response |
| `path` | endpoint | required | URL path pattern, supports `:param` wildcards |
| `volatility` | endpoint | `low` | Fallback volatility for fields without explicit declaration |
| `max_staleness_ms` | endpoint | `5000` | Maximum vault entry age before cold-miss fallback |
| `replace_threshold` | endpoint | `0.5` | Fraction of changed SPECULATIVE fields that triggers REPLACE |
| `default_class` | endpoint | `SPECULATIVE` | Default class for unlisted fields. Set to `DEFERRED` for high-sensitivity endpoints. |
| `fields.<n>.class` | field | inherits `default_class` | Field certainty class |
| `fields.<n>.volatility` | field | inherits endpoint | Per-field volatility override |

> **⚠ Default Classification Warning**: Fields not listed in configuration default to `SPECULATIVE`. This means any new field the upstream API adds will be served speculatively without operator action. For APIs that evolve over time — particularly those that may introduce financial, security, or alarm-relevant fields in future releases — this default is dangerous. Operators must treat API schema updates as a prompt to review field classification. A safe operational posture is to set `default_class: DEFERRED` for high-sensitivity endpoints.

---

## Signal Reference

| Signal | When Sent | Terminal | Client Action |
|---|---|---|---|
| `CONFIRM` | No diff (read) / Commit succeeded (write) | Yes | Read: mark confirmed. Write: apply `data` if present, else confirm provisional as-is |
| `PATCH` | SPECULATIVE fields differ (read only) | No | Apply RFC 6902 ops |
| `FILL` | DEFERRED fields now available (read only) | No | Merge fields into state |
| `REPLACE` | Structural or threshold diff (read only) | Yes | Replace full resource state |
| `ABORT` | Formal Track failed / write rejected | Yes | Read: discard speculative. Write: revert provisional; apply `state` if present, else refetch |

---

*Antic-PT Draft v0.2.2 — Section 13 Provisional Write Commits fully specified.*
*Reference implementation: Spec-Link (Go), AnticipationResolver (JavaScript)*
*License: MIT*
