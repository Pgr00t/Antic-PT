# ANTIC-PT: Anticipation Protocol Specification

**Version:** 0.1 (Draft)  
**Status:** Experimental  
**Authors:** Antic-PT Open Source Project  
**License:** MIT  

---

## Abstract

Antic-PT (Anticipation Protocol) is an open, transport-agnostic protocol layer that enables **synchronous speculative responses** for read-heavy HTTP services. A single incoming request is forked into two concurrent execution tracks — a Fast Track that serves a cached speculative response immediately, and a Formal Track that validates against the authoritative data source. The client is informed of any discrepancy via a structured reconciliation signal.

Antic-PT is designed to be a **drop-in layer** in front of any existing REST API with zero changes to the underlying application.

---

## 1. Motivation

Standard REST API flow is sequential:

```
Client → Network → Service → Database → Response → Client renders UI
  0ms      50ms     10ms      150ms       50ms           260ms total
```

The user waits 200–500ms per interaction staring at a loading state.

Partial solutions exist (React Query's `staleWhileRevalidate`, Apollo optimistic UI), but they are:
- Implemented at the **application layer** — requiring custom code per endpoint
- **Framework-specific** — not portable across mobile, web, CLI
- Lacking a **standardized reconciliation contract** — conflict resolution is ad-hoc

Antic-PT solves this at the **protocol and infrastructure layer**, making perceived sub-10ms responses achievable on standard infrastructure without rewriting application logic.

---

## 2. Terminology

| Term | Definition |
|---|---|
| **Fast Track** | The speculative execution path that reads from the State-Vault and responds immediately |
| **Formal Track** | The authoritative execution path that queries the real data source |
| **State-Vault** | An in-memory store (e.g., Redis, DragonflyDB) that holds versioned resource snapshots |
| **Spec-Link** | The Antic-PT middleware proxy that intercepts requests and forks execution |
| **Resolver** | The client-side library that handles the dual-stream and reconciliation |
| **Intent Token** | A lightweight header that optionally guides speculative behavior |
| **Reconciliation Signal** | A structured message sent by the Formal Track to confirm or correct the speculative response |

---

## 3. Protocol Architecture

```
┌──────────────────────────────────────────────────────────────────────┐
│                          ANTIC-PT FLOW                               │
│                                                                      │
│  Client (Resolver SDK)                                               │
│       │                                                              │
│       │  GET /spec/resource/123                                      │
│       │  X-Antic-Intent: auto                                        │
│       ▼                                                              │
│  ┌─────────────┐                                                     │
│  │  Spec-Link  │ ──── FORK ─────────────────────────────┐           │
│  │  (Proxy)    │                                        │           │
│  └─────────────┘                                        │           │
│       │                                                 │           │
│  FAST TRACK (ms)                              FORMAL TRACK (ms–s)   │
│       │                                                 │           │
│  Read State-Vault ──► Stream speculative       Query real DB        │
│  response to client                                     │           │
│       │                                        Compare versions     │
│  Client renders UI ◄───────────────────────── Send signal:          │
│  immediately                                   CONFIRM / PATCH /    │
│                                                REPLACE / ABORT      │
└──────────────────────────────────────────────────────────────────────┘
```

---

## 4. Request Format

### 4.1 Route Prefix

Antic-PT requests are distinguished by a URI prefix:

```
Standard REST:    GET /api/users/123
Antic-PT:         GET /spec/users/123
```

The Spec-Link proxy strips the `/spec` prefix before forwarding to the Formal Track.

### 4.2 The X-Antic-Intent Header

The primary signal from client to Spec-Link. This is **not** a JWT. It is a simple, human-readable header.

```
X-Antic-Intent: <mode>[;<hints>]
```

#### Modes

| Mode | Description |
|---|---|
| `auto` | AI Intent Layer classifies the request and determines cache behavior |
| `guided` | Developer provides explicit hints to guide speculative behavior |
| `bypass` | Skip Fast Track entirely; behave as standard REST (useful for debugging) |

#### Guided Mode Hints (optional key-value pairs)

```
X-Antic-Intent: guided; resource=user; id=123; version=47; ttl=5000
```

| Hint | Type | Description |
|---|---|---|
| `resource` | string | Logical resource type (e.g., `user`, `feed`, `inventory`) |
| `id` | string | Resource identifier for State-Vault lookup |
| `version` | integer | Last known version from client (prevents phantom reads) |
| `ttl` | integer (ms) | Maximum acceptable staleness window |
| `confidence` | float 0–1 | Minimum AI confidence threshold before speculating |

#### Auto Mode (AI Intent Layer)

When `X-Antic-Intent: auto`, the embedded AI Intent Layer in Spec-Link:

1. Analyzes the request URL pattern, method, and headers
2. Classifies the request as `speculatable` or `non-speculatable`
3. Auto-generates a cache key from the request signature
4. Sets a default staleness window based on learned traffic patterns
5. Falls back to `bypass` mode if confidence is below threshold (default: 0.75)

---

## 5. Response Format

### 5.1 Transport

Antic-PT responses use **Server-Sent Events (SSE)** as the reference transport.

```
Content-Type: text/event-stream
Cache-Control: no-cache
X-Antic-Track: fast|formal
X-Antic-Version: <version-number>
```

### 5.2 Dual-Stream Event Types

All events follow the SSE wire format:

```
event: <event-type>
id: <resource-version>
data: <JSON payload>

```

#### Event Catalogue

| Event | Sent By | Description |
|---|---|---|
| `speculative` | Fast Track | Initial speculative response from cache |
| `confirm` | Formal Track | Formal data matches speculative — no UI change needed |
| `patch` | Formal Track | Formal data partially differs — send JSON patch |
| `replace` | Formal Track | Formal data fully differs — replace entire payload |
| `abort` | Formal Track | Request failed; speculative data should be reverted |
| `meta` | Either | Non-data signals (e.g., `cache-miss`, `version-conflict`) |

### 5.3 Example Stream

```
event: speculative
id: 47
data: {"id": 123, "name": "Alice", "balance": 540, "_antic": {"version": 47, "source": "vault", "age_ms": 320}}

event: confirm
id: 47
data: {"status": "ok", "version": 47}
```

```
event: speculative
id: 47
data: {"id": 123, "name": "Alice", "balance": 540, "_antic": {"version": 47, "source": "vault"}}

event: patch
id: 48
data: [{"op": "replace", "path": "/balance", "value": 560}, {"op": "replace", "path": "/_antic/version", "value": 48}]
```

### 5.4 The `_antic` Envelope

Every `speculative` response payload includes a reserved `_antic` metadata object stripped before delivery to the application layer:

```json
"_antic": {
  "version": 47,
  "source": "vault | live",
  "age_ms": 320,
  "confidence": 0.91,
  "cache_key": "user:123"
}
```

---

## 6. Reconciliation Signals

### 6.1 CONFIRM

No action required by the Resolver. The Fast Track was correct.

```
event: confirm
id: 48
data: {"status": "ok", "version": 48, "latency_ms": 187}
```

### 6.2 PATCH

A JSON Patch (RFC 6902) array describing the delta. The Resolver applies the patch to the rendered state.

```
event: patch
id: 48
data: [{"op": "replace", "path": "/name", "value": "Alicia"}]
```

### 6.3 REPLACE

The Resolver discards the speculative payload and renders the new data. Should be used sparingly — triggers a visible UI update.

```
event: replace
id: 48
data: {"id": 123, "name": "Alicia", "balance": 560, ...}
```

### 6.4 ABORT

The speculative payload was wrong AND could not be corrected. The Resolver must revert UI to its pre-request state.

```
event: abort
id: 0
data: {"reason": "db_error", "code": 503, "revert": true}
```

---

## 7. Versioning — Preventing Phantom Reads

To prevent old cache data from overwriting newer client state, Antic-PT uses **monotonic version integers**.

### Rule: Version Ordering Contract

```
A speculative response MAY be rendered ONLY IF:

  server_version >= client_known_version

If server_version < client_known_version:
  Fast Track MUST emit a `meta` event with type `version-conflict`
  and fall through to Formal Track response only.
```

The version is always an integer that increments on every write. It is stored alongside the resource in the State-Vault.

---

## 8. State-Vault Interface

The State-Vault is any store implementing the following interface:

```
GET  vault://<resource>/<id>          → { data, version, updated_at }
SET  vault://<resource>/<id>  <data>  → { version }
DEL  vault://<resource>/<id>          → ok
TTL  vault://<resource>/<id>  <ms>    → ok
```

Reference implementations:
- Redis (strings with JSON serialization)
- DragonflyDB (drop-in Redis replacement, 25x throughput)
- In-memory (for testing and single-instance deployments)

### Vault Write Strategy

The Formal Track writes to the Vault after every successful DB response:

```
DB Response received → Increment version → Write to Vault → Send reconciliation signal
```

This ensures the Vault is always at most **one Formal Track request behind** the DB.

---

## 9. Safety Contract

Antic-PT v0.1 enforces the following guarantees:

| Guarantee | Detail |
|---|---|
| **Read-only scope** | The Fast Track NEVER processes POST, PUT, PATCH, DELETE, or any HTTP method with mutation semantics |
| **No double-sends** | Each request session has a unique `X-Antic-Session-ID` to prevent duplicate Formal Track executions |
| **No silent failures** | If the Formal Track errors, an `abort` event is always sent — there is no "silent confirm" |
| **Version monotonicity** | A lower-versioned response NEVER overwrites a higher-versioned client state |
| **Passthrough on miss** | A State-Vault cache miss causes the Fast Track to skip the `speculative` event and wait for Formal Track response — behavior degrades gracefully to standard REST |

---

## 10. Spec-Link Configuration

The Spec-Link proxy is configured via a single YAML file:

```yaml
# antic-pt.yaml
spec_link:
  prefix: /spec              # Route prefix to intercept
  vault:
    driver: redis            # redis | dragonfly | memory
    url: redis://localhost:6379
    default_ttl_ms: 30000
  intent:
    mode: auto               # auto | guided | bypass
    ai_confidence_threshold: 0.75
  formal_track:
    upstream: http://localhost:3000   # Your existing API
    timeout_ms: 5000
  reconciliation:
    strategy: patch          # patch | replace (default patch strategy)
```

---

## 11. Resolver SDK Contract (Client)

The Resolver SDK must implement the following interface across all platforms:

```typescript
interface AnticResolver {
  // Initiate an Antic-PT request
  fetch(url: string, options?: AnticOptions): AnticRequest;
}

interface AnticOptions {
  intent?: 'auto' | 'guided';
  hints?: IntentHints;
  onSpeculative: (data: any, meta: AnticMeta) => void;  // Called ~5ms
  onConfirm: (version: number) => void;                 // Called ~200ms
  onPatch: (patch: JSONPatch[]) => void;                // Called ~200ms  
  onReplace: (data: any) => void;                       // Called ~200ms
  onAbort: (reason: string) => void;                    // Called on error
  conflictPolicy?: 'server-wins' | 'client-wins' | 'merge' | 'defer';
}
```

---

## 12. Conformance Levels

| Level | Requirements |
|---|---|
| **Antic-PT Basic** | Implements `speculative` + `confirm` + `replace` events; guided mode only |
| **Antic-PT Standard** | Adds `patch` events, `auto` mode, version ordering contract |
| **Antic-PT Full** | Adds AI Intent Layer, multi-client sync, observable metrics endpoint |

---

## 13. Non-Goals (v0.1)

- **Write operations** — speculative writes with rollback are planned for v2.0
- **HTTP/3 QUIC transport** — SSE is the reference transport; QUIC is planned for v3.0
- **Multi-region vault sync** — single-region only in v0.1
- **End-to-end encryption of stream** — use TLS at the transport layer

---

## 14. Reference Implementation

- **Spec-Link Proxy:** Go (v1.0) — lightweight binary, <10MB, zero dependencies
- **State-Vault:** Redis 7+ or DragonflyDB 1+
- **Resolver SDK:** JavaScript/TypeScript (v1.2), Swift (v2.0), Kotlin (v2.0)
- **Demo:** Node.js (v0.2) — for illustration only, not production

---

*Antic-PT is an open specification. Implementations, extensions, and feedback are welcome.*  
*"Respond before you're asked."*
