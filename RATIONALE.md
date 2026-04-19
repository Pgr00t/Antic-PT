# Antic-PT: Rationale
## Why This Exists, What Was Wrong Before, and What the Pivot Fixed

---

## The Problem We Are Actually Solving

HTTP APIs lie by omission.

Not maliciously. Structurally. When a server returns a 200 OK, it presents that payload as current truth. There is nothing in the response to indicate whether that data came directly from a database queried milliseconds ago, or from a cache that was last validated three minutes ago, or from a replica that is mid-replication and disagrees with the primary by two fields.

The client has no way to know. So it treats everything as true. And the user waits — either for the real data to arrive, or unknowingly for stale data that already arrived and was presented as fresh.

This is not a performance problem. It is a semantic problem. **APIs do not have a vocabulary for uncertainty.** Antic-PT is an attempt to build that vocabulary.

---

## Why Existing Solutions Do Not Solve This

This question deserves a direct answer because it is the first thing any experienced engineer will ask.

### stale-while-revalidate (RFC 5861)

The `stale-while-revalidate` cache directive does something adjacent: serve stale content, revalidate in the background. It is well-understood, widely supported, and useful.

It does not solve our problem for three reasons:

First, it operates at the response level. The entire response is either fresh or stale. There is no mechanism to say "this field is safe to serve stale, this field must wait for validation, this field never changes." A response is an atomic unit to `stale-while-revalidate`.

Second, it has no reconciliation signal. When the revalidation completes in the background, the client is not told. The next request gets fresh data. The current request — and the UI already rendered from it — is never corrected. If you showed the user stale data and it was wrong, you do not find out until the next page load.

Third, it has no staleness metadata. The client cannot make rendering decisions based on how old the data is because it is not told how old the data is.

### React Query / SWR / TanStack Query

These libraries implement the stale-while-revalidate pattern in JavaScript with background refetching and cache invalidation. They are excellent libraries and extremely well adopted.

They do not solve our problem for two reasons:

First, they are client-side libraries. They work only in environments where JavaScript runs — browsers and React Native. A server-side rendered application, a mobile app built in Swift or Kotlin, a microservice calling another microservice, a CLI tool — none of these benefit from React Query. Our problem exists on every HTTP client, not just React applications.

Second, they still treat responses as atomic units. When a refetch completes and the data has changed, the entire resource state is replaced. There is no mechanism for a partial field-level update that leaves confirmed fields untouched and only corrects the fields that changed. This causes UI thrash — the entire component re-renders when only one number changed.

### Apollo Client Optimistic Responses

Apollo allows clients to predict what a server will return for a mutation and render that prediction immediately, rolling back if the server disagrees. This is genuinely useful and closer in spirit to what Antic-PT is doing.

It does not solve our problem for two reasons:

First, it requires GraphQL. The services that most need latency improvement — legacy REST APIs, high-traffic internal services, operational tooling — are not GraphQL services. Requiring a schema migration as a precondition for adopting a latency protocol is the wrong trade.

Second, the speculation is client-side. The client guesses what the server will return. The client is the least informed party — it does not know what other concurrent requests may have changed the data, what background jobs may be running, what the current state of dependent systems is. Moving speculation responsibility to the server, the party that actually knows the data, is architecturally more correct.

### HTTP 103 Early Hints

HTTP 103 allows servers to send preliminary headers before the final response. It is useful for preloading linked resources.

It is not designed for, and cannot express, the certainty semantics we need. It does not have a reconciliation model. It does not know about field-level classification. It is solving a different problem.

---

## What Was Wrong With v0.1

The original Antic-PT framing had four specific problems. Naming them honestly is part of the record.

### Problem 1: Universality Claim

v0.1 described itself as a "drop-in layer for any REST API." This is not true and it undermined the project's credibility with anyone who thought carefully about it.

The server side required zero changes. But the client side required a Resolver SDK that understood the signal vocabulary, managed reconciliation state, and handled CONFIRM, PATCH, REPLACE, and ABORT correctly. Existing clients — mobile apps, CLI tools, third-party integrations — received zero benefit and potentially broken behavior. "Drop-in" was a marketing claim, not a technical one.

### Problem 2: Response-Level Speculation

v0.1 speculated on entire responses. Either the whole cached response was served or it wasn't. When the Formal Track found a difference, the whole response was replaced.

This causes visible UI jitter. If a dashboard has twenty fields and one changed, the entire component re-renders. Worse, fields that should never be speculated — financial balances, alarm counts, security state — were speculated along with everything else. There was no mechanism to say "not this field."

The correct model is field-level. Each field carries an explicit certainty class. Speculation is surgical. Reconciliation delivers only what changed.

### Problem 3: The Confidence Score

v0.1 proposed an `X-Antic-Confidence` header with a floating-point score — for example, `0.82`. This was removed in v0.2 and should never return.

The problem is that confidence is business logic, not protocol logic. Whether a 30-second-stale stock price should be treated as high or low confidence depends entirely on market volatility at that moment — information the proxy does not have. Whether a 30-second-stale server uptime counter should be treated as high confidence is a completely different question with a completely different answer. A protocol that asks operators to compute and expose a confidence score they cannot actually calculate will not be adopted. Developers will set it to a constant and it will become decorative metadata.

The fix is to expose only deterministic facts the proxy genuinely knows: how old is this data (`X-Antic-Staleness`) and what did the developer declare about its expected volatility (`X-Antic-Volatility`). The client SDK computes what to do from those facts. This keeps the protocol honest.

### Problem 4: The Framing as a Finished Protocol

v0.1 was presented as an industry protocol. It was a proof of concept for a reconciliation primitive. These are different things, and presenting the former as the latter opened the project to legitimate criticism that it was reimplementing patterns that already existed.

The correct framing is: v0.1 was step one of a protocol family whose destination — write-side provisional commits — is genuinely novel territory. Presented as step one, the same work is a credible foundation. Presented as a finished protocol, it is an incomplete reimplementation of `stale-while-revalidate`.

---

## What the Pivot Fixed

The pivot from v0.1 to v0.2 is not a rewrite. The proxy infrastructure, the dual-track architecture, and the signal vocabulary are all preserved. The pivot is in three things: scope, semantics, and honesty.

### Scope: From Universal to Precise

v0.2 does not try to serve every HTTP API. It targets read-heavy, stale-tolerant, high-frequency endpoints — specifically operational dashboards, monitoring UIs, analytics interfaces, and admin panels. These have exactly the right characteristics: high perceived latency sensitivity, tolerance for brief field-level inconsistency, low catastrophic risk if a speculative render is slightly wrong, and easy measurement of UX improvement.

Winning this use case deeply is worth more than claiming universal applicability. If the protocol proves itself on dashboards, the broader story follows from evidence rather than assertion.

### Semantics: From Response-Level to Field-Level

The introduction of the field classification model — SPECULATIVE, DEFERRED, INVARIANT, PROVISIONAL — is the most significant technical change in v0.2 and the most significant differentiator from every existing pattern.

This model acknowledges what practitioners already know: not all fields in a response are equal. Some can be shown immediately with low risk. Some must never be shown before confirmation. Some never change and can be cached indefinitely. The protocol now expresses this explicitly, and reconciliation delivers only what changed, expressed as a standard RFC 6902 JSON Patch.

### Honesty: Acknowledging the Write-Side Destination

The write-side provisional commit model — where a server issues a provisional 202 Accepted on a mutation and later emits CONFIRM or ABORT when the transaction finalizes — is the genuinely novel territory this protocol is heading toward. No existing HTTP standard formalizes this model. It would represent a meaningful contribution.

But it requires more than a proxy layer. It requires durable signal delivery, server-side provisional state persistence, and a trust foundation built by proving the read-side signal vocabulary in production. v0.2 is honest about this. The write-side spec is documented as a future extension, not pretended as a current capability.

---

## Why Now

A fair question: if this problem is real, why hasn't it been solved?

The infrastructure to support it has only recently matured. Holding open tens of thousands of persistent SSE connections — the mechanism Antic-PT uses for signal delivery — would have been impractical on standard server infrastructure five years ago. Edge computing has only recently become a place where stateful routing logic can run at sub-10ms latency. WebTransport, a lower-latency alternative to WebSockets, is newly available.

The underlying primitives are ready. The timing is not accidental.

---

## What This Is Not

Antic-PT is not trying to replace HTTP caching directives. It complements them. A response served under `Cache-Control: max-age=60` and a response served under Antic-PT field speculation are doing different things for different reasons.

Antic-PT is not a CDN solution. Topology — where Spec-Link is deployed, whether at the edge or co-located with the backend — is an operator choice. The protocol behavior is identical regardless of deployment location.

Antic-PT is not a React library. The AnticipationResolver SDK is a vanilla JavaScript state machine. React, Vue, and Svelte integrations are thin wrappers. The protocol works for any HTTP client that can read response headers and receive SSE events.

---

## The One Thing That Will Determine Whether This Matters

Everything in this document is argument. Argument has limited value.

What will determine whether Antic-PT matters is whether one engineer at a company with a real high-frequency read endpoint integrates Spec-Link, sees their dashboard go from 300ms perceived load to instant with reliable field-level reconciliation, and writes about it.

That is the only validation that counts. This rationale document, the spec, the demos, the SDK — all of it is preparation for that moment. The protocol lives or dies in production, not in documentation.

---

*Antic-PT Rationale — Accompanies Draft v0.2 of ANTIC-PT-SPEC.md*
