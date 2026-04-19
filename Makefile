# Antic-PT: Anticipation Protocol — Makefile

.PHONY: help build test fmt lint run run-proxy run-api seed clean

help:
	@echo ""
	@echo "  Antic-PT v0.2.1 Development Commands"
	@echo ""
	@echo "  make run          Start everything for demo (API server + Go proxy, seeded vault)"
	@echo "  make build        Build the Spec-Link Go proxy binary"
	@echo "  make test         Run all Go unit tests"
	@echo "  make fmt          Format Go code"
	@echo "  make lint         Lint Go code"
	@echo "  make run-api      Start Node.js demo API server only (:4001)"
	@echo "  make run-proxy    Start Go Spec-Link proxy only (:4000)"
	@echo "  make seed         Warm vault manually after proxy is running"
	@echo "  make clean        Remove build artifacts"
	@echo ""

# ── Build ────────────────────────────────────────────────────────────────────

build:
	cd spec-link && go build -o ../bin/spec-link main.go

# ── Test ─────────────────────────────────────────────────────────────────────

test:
	cd spec-link && go test -v ./...

# ── Format / Lint ─────────────────────────────────────────────────────────────

fmt:
	cd spec-link && go fmt ./...

lint:
	cd spec-link && go vet ./...

# ── Run (full demo stack) ─────────────────────────────────────────────────────
#
# Starts:
#   1. Node.js demo API server on :4001  (authoritative upstream)
#   2. Go Spec-Link proxy on :4000       (dual-track + signal channel + passthrough)
#
# The proxy is started with -seed so it pre-warms the State Vault from :4001/seed/*
# after a 2-second delay (giving the Node server time to start).
#
# Open http://localhost:4000 in your browser after running this.

run:
	@echo ""
	@echo "  Starting Antic-PT v0.2.1 demo stack"
	@echo "  Demo →  http://localhost:4000"
	@echo "  API  →  http://localhost:4001/api/*"
	@echo "  Signals→ http://localhost:4000/antic/signals"
	@echo ""
	@pkill -f "node index.js" 2>/dev/null || true
	@pkill -f "spec-link" 2>/dev/null || true
	@sleep 1
	@make build
	@# Start Node API server in background
	@cd demo/server && node index.js &
	@sleep 2
	@echo "[makefile] Node API server started on :4001"
	@# Start Go proxy in foreground (Ctrl+C stops everything)
	@cd spec-link && ../bin/spec-link -config antic-pt.yaml -seed

# ── Individual server targets ─────────────────────────────────────────────────

run-api:
	cd demo/server && node index.js

run-proxy: build
	cd spec-link && ../bin/spec-link -config antic-pt.yaml

# ── Seed vault manually ───────────────────────────────────────────────────────
# Useful if you started proxy without -seed and want to prime the vault.

seed:
	@echo "Seeding vault via /seed/* endpoints..."
	@curl -s http://localhost:4001/seed/user/1      | curl -X POST http://localhost:4000/spec/user/1      -H 'Content-Type: application/json' -d @- > /dev/null 2>&1 || true
	@curl -s "http://localhost:4000/spec/user/1?_seed=1"      -H 'X-Antic-Client-Id: seed' > /dev/null || true
	@curl -s "http://localhost:4000/spec/dashboard/1?_seed=1" -H 'X-Antic-Client-Id: seed' > /dev/null || true
	@curl -s "http://localhost:4000/spec/feed/1?_seed=1"      -H 'X-Antic-Client-Id: seed' > /dev/null || true
	@echo "Seed requests sent."

# ── Clean ─────────────────────────────────────────────────────────────────────

clean:
	rm -rf bin/

# Aliases for backwards compatibility
r: run
demo-server: run-api
