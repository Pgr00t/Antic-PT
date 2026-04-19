# Contributing to Antic-PT

Thank you for your interest in contributing. Antic-PT is an open protocol and reference implementation — contributions to the spec, proxy, SDK, tooling, and ecosystem bindings are all welcome.

---

## Before You Open an Issue or PR

Please read these two documents first:

- **[RATIONALE.md](./RATIONALE.md)** — Why this protocol exists and what problems it is solving. Understanding this will help you contribute at the right level.
- **[ANTIC-PT-SPEC.md](./ANTIC-PT-SPEC.md)** — The formal protocol specification. Any change that affects protocol behavior must be grounded in the spec.

---

## What We Welcome

### Protocol Contributions
Changes to `ANTIC-PT-SPEC.md` are the highest-value contributions. If you've found a correctness gap, an underspecified failure mode, or a real-world deployment edge case the spec doesn't address:

1. Open an issue describing the gap and your proposed resolution before writing any code.
2. If consensus is reached, update the spec and any affected reference implementations in the same PR.
3. Include a test case or demo scenario that demonstrates the edge case.

### Proxy Contributions (`spec-link/`)
- Written in Go. Requires Go 1.21+.
- All protocol-relevant behavior must be tested: `go test ./...`
- Run `go fmt ./...` before committing. PRs with formatting drift will not be merged.
- Do not introduce new dependencies without discussion. The proxy is intentionally minimal.

### SDK Contributions (`packages/resolver-js/`)
- Written in TypeScript. Requires Node 20+ for the build toolchain (`tsup`, `vitest`).
- The SDK must remain zero-dependency at runtime. No `node_modules` in the browser bundle.
- Build: `npm run build` — produces ESM, CJS, and IIFE bundles.
- All exported API surface must be typed.

### Ecosystem Bindings (new)
SDK bindings for other languages and frameworks are welcome:
- **React hook** — `packages/resolver-js/src/react.ts` is the reference. Follow same event contract.
- **Vue / Svelte composables** — follow the same `on('speculative')` / `on('confirm')` pattern.
- **Python / Go / Rust client SDKs** — treat `ANTIC-PT-SPEC.md` §9 as the contract.
- **GraphQL / tRPC bindings** — community extensions; document as such.

### Demo Contributions (`demo/`)
- The demo server is Node.js (Express). Keep it simple and illustrative.
- The demo UI is vanilla HTML/CSS/JS — no framework.
- Demo changes should showcase protocol behavior, not UI polish.

---

## What We Do Not Accept

- Changes that re-introduce response-level speculation (the entire response is either speculated or not). Field-level classification is a core spec requirement.
- Changes that add a confidence score header or proxy-computed "AI" speculation gating. Per the spec and rationale: the proxy emits facts (`X-Antic-Staleness`, `X-Antic-Volatility`), the SDK computes rendering decisions.
- Write-side provisional commit work against `main`. Write-side work belongs on the `v1-provisional-writes` branch.
- New runtime dependencies in the JS SDK.
- Changes that break the `CONFIRM`, `PATCH`, `FILL`, `REPLACE`, `ABORT` signal ordering contract (see spec §7).

---

## Development Setup

### Proxy (Go)

```bash
cd spec-link
go build ./...          # build
go test ./...           # unit tests (no Redis required, except integration tests)
go fmt ./...            # format
```

### JS SDK

```bash
cd packages/resolver-js
npm install
npm run build           # produces dist/index.{js,mjs,global.js}
```

### Demo

```bash
# Terminal 1 — upstream demo server (Node)
cd demo/server && node index.js

# Terminal 2 — Spec-Link proxy
./bin/spec-link -config spec-link/antic-pt.yaml

# Browser
open demo/client/index.html
```

Or use the Makefile shortcut:

```bash
make run    # starts both demo server and proxy concurrently
```

---

## Commit Style

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat:  a new feature
fix:   a bug fix
docs:  documentation only
spec:  protocol spec change
test:  adding or updating tests
refactor: code changes that don't change behavior
chore: CI, build, tooling
```

Examples:
```
feat: add FILL signal emission after DEFERRED field delivery
fix: REPLACE threshold not applied when replace_threshold=0.0
spec: clarify INVARIANT violation vault update requirement (§11.3)
docs: add vault snapshot concurrency explanation to README
```

---

## Questions About the Protocol

Open a GitHub Discussion rather than an issue for questions about design rationale, deployment patterns, or ideas that are not yet proposals.

---

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](./LICENSE).
