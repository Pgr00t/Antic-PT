# Antic-PT: Anticipation Protocol — Makefile

.PHONY: help build test fmt lint run run-proxy run-api seed clean demo stop

help:
	@echo ""
	@echo "  Antic-PT v0.2.2 Development Commands"
	@echo ""
	@echo "  make demo         Start the full production-demo (Exchange + Proxy + Dashboards)"
	@echo "  make run          Start the basic read-side demo stack (Node API + Proxy)"
	@echo "  make build        Build the Spec-Link Go proxy binary"
	@echo "  make test         Run all Go unit tests"
	@echo "  make fmt          Format Go code"
	@echo "  make lint         Lint Go code"
	@echo "  make stop         Stop all running demo processes"
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
	@echo "  Starting Antic-PT v0.2.2 basic read-side stack"
	@echo "  Demo →  http://localhost:4000"
	@echo ""
	@make stop
	@sleep 1
	@make build
	@cd demo/server && node index.js &
	@sleep 2
	@cd spec-link && ../bin/spec-link -config antic-pt.yaml -seed

# ── Full Production Demo ──────────────────────────────────────────────────────
#
# Starts:
#   1. Mock Exchange Server on :4005 (Write Downstream)
#   2. Spec-Link Proxy on :4002 (Live Binance Feed + Write Track)
#   3. Read Dashboard on :4000
#   4. Write Dashboard on :4006
#
demo:
	@echo ""
	@echo "  🚀 Starting Antic-PT v0.2.2 Full Demo Experience"
	@echo ""
	@echo "  Read Dashboard:  http://localhost:4000"
	@echo "  Write Dashboard: http://localhost:4006"
	@echo "  Proxy API:       http://localhost:4002/spec/*"
	@echo ""
	@make stop
	@sleep 1
	@make build
	@echo "[demo] Starting Mock Exchange (:4005)..."
	@node integrations/binance/write/server/exchange.js &
	@echo "[demo] Starting Spec-Link Proxy (:4002)..."
	@./bin/spec-link -config integrations/binance/antic-pt.yaml &
	@echo "[demo] Starting Dashboards..."
	@npx http-server integrations/binance/client -p 4000 -c-1 --cors --silent &
	@npx http-server integrations/binance/write/client -p 4006 -c-1 --cors --silent &
	@echo ""
	@echo "  Everything is up! Press Ctrl+C to stop (or run 'make stop')"
	@wait

stop:
	@echo "Stopping all Antic-PT processes..."
	@pkill -f "node index.js" || true
	@pkill -f "exchange.js" || true
	@pkill -f "spec-link" || true
	@pkill -f "http-server" || true
	@echo "Done."

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
