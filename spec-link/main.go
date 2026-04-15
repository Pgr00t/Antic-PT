// Spec-Link — Antic-PT v1.0 Proxy (Go)
//
// Usage:
//   spec-link                     # uses ./antic-pt.yaml
//   spec-link --config /path/to/config.yaml
//
// Demo mode (default):
//   When upstream is "embedded", a built-in mock API is served on a secondary
//   port so you can see the full dual-track in action without any external
//   service. This is the default for development and demonstration.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"antic-pt/spec-link/config"
	"antic-pt/spec-link/proxy"
	"antic-pt/spec-link/vault"
)

func main() {
	cfgPath := flag.String("config", "antic-pt.yaml", "Path to antic-pt.yaml configuration file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("❌  Failed to load config %q: %v", *cfgPath, err)
	}

	// ── Vault ────────────────────────────────────────────────────────────
	var v *vault.MemoryVault
	switch cfg.Vault.Driver {
	case "memory", "":
		v = vault.NewMemory()
		seedDemoData(v)
	default:
		log.Fatalf("❌  Unsupported vault driver %q (Redis coming in v1.1)", cfg.Vault.Driver)
	}

	// ── Demo upstream ─────────────────────────────────────────────────────
	// When config upstream == "embedded", we spin up a mock API server on
	// port+1 so the full round-trip is visible in demos.
	if strings.ToLower(cfg.FormalTrack.Upstream) == "embedded" || cfg.FormalTrack.Upstream == "" {
		upstreamPort := cfg.Port + 1
		cfg.FormalTrack.Upstream = fmt.Sprintf("http://localhost:%d", upstreamPort)
		go startDemoUpstream(upstreamPort)
		time.Sleep(50 * time.Millisecond) // give upstream a moment to bind
	}

	// ── Spec-Link Proxy ───────────────────────────────────────────────────
	handler := proxy.NewHandler(cfg, v)

	mux := http.NewServeMux()

	// /spec/* → Spec-Link dual-track
	mux.HandleFunc(cfg.Prefix+"/", handler.HandleSpec)

	// Static client files (demo UI)
	clientDir := resolveClientDir(*cfgPath)
	if clientDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(clientDir)))
		log.Printf("📂  Serving demo UI from %s", clientDir)
	}

	printBanner(cfg, clientDir)

	addr := fmt.Sprintf(":%d", cfg.Port)
	if err := http.ListenAndServe(addr, corsMiddleware(mux)); err != nil {
		log.Fatalf("❌  Server error: %v", err)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Demo upstream server — simulates a real API with realistic latency
// ────────────────────────────────────────────────────────────────────────────

var demoUpstreamDB = map[string]map[string]interface{}{
	"user/1": {
		"id": 1, "name": "Alice Chen", "role": "Product Designer", "team": "Growth",
		"avatar": "AC", "projects": 12, "tasks_open": 4, "tasks_done": 91,
		"streak_days": 14, "last_active": "just now", "kpi_score": 95,
	},
	"feed/1": {
		"items": []interface{}{
			map[string]interface{}{"id": 0, "author": "Marcus Roy", "action": "opened issue #501", "time": "just now", "type": "code"},
			map[string]interface{}{"id": 1, "author": "Bob Kim", "action": "merged PR #443", "time": "3m ago", "type": "code"},
			map[string]interface{}{"id": 2, "author": "Sara Lee", "action": "commented on Design System", "time": "8m ago", "type": "design"},
			map[string]interface{}{"id": 3, "author": "Dev Ops", "action": "deployed v2.4.1 to staging", "time": "15m ago", "type": "deploy"},
		},
	},
	"dashboard/1": {
		"revenue": 128400, "revenue_delta": 12.4,
		"active_users": 4821, "users_delta": 8.1,
		"conversion": 3.72, "conv_delta": 0.43,
		"latency_p99": 187, "latency_delta": -34,
	},
}

func startDemoUpstream(port int) {
	mux := http.NewServeMux()

	// /api/{resource}/{id} — simulates a slow but real database query
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Simulate DB latency: 280–380ms (realistic P50 for a network + DB hop)
		delay := 280 + rand.Intn(100)
		time.Sleep(time.Duration(delay) * time.Millisecond)

		// Parse resource/id from path (strip leading /api/)
		trimmed := strings.TrimPrefix(r.URL.Path, "/api/")
		trimmed = strings.Trim(trimmed, "/")

		data, ok := demoUpstreamDB[trimmed]
		if !ok {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}

		// Attach timing metadata
		out := copyMap(data)
		out["_meta"] = map[string]interface{}{
			"latency_ms": delay,
			"source":     "database",
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(out)
	})

	addr := fmt.Sprintf(":%d", port)
	log.Printf("🗄   Demo upstream listening on %s (simulates ~300ms DB latency)", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Demo upstream error: %v", err)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Seed demo data into vault
// ────────────────────────────────────────────────────────────────────────────

func seedDemoData(v *vault.MemoryVault) {
	v.Seed("user", "1", map[string]interface{}{
		"id": 1, "name": "Alice Chen", "role": "Product Designer", "team": "Growth",
		"avatar": "AC", "projects": 12, "tasks_open": 4, "tasks_done": 89,
		"streak_days": 14, "last_active": "2 min ago", "kpi_score": 94,
	}, 47)

	v.Seed("feed", "1", map[string]interface{}{
		"items": []interface{}{
			map[string]interface{}{"id": 1, "author": "Bob Kim", "action": "merged PR #443", "time": "3m ago", "type": "code"},
			map[string]interface{}{"id": 2, "author": "Sara Lee", "action": "commented on Design System", "time": "8m ago", "type": "design"},
			map[string]interface{}{"id": 3, "author": "Dev Ops", "action": "deployed v2.4.1 to staging", "time": "15m ago", "type": "deploy"},
			map[string]interface{}{"id": 4, "author": "Alice Chen", "action": "created milestone Q2 Sprint", "time": "1h ago", "type": "plan"},
		},
	}, 112)

	v.Seed("dashboard", "1", map[string]interface{}{
		"revenue": 128400, "revenue_delta": 12.4,
		"active_users": 4821, "users_delta": 8.1,
		"conversion": 3.72, "conv_delta": 0.43,
		"latency_p99": 187, "latency_delta": -34,
	}, 88)
}

// ────────────────────────────────────────────────────────────────────────────
// CORS middleware
// ────────────────────────────────────────────────────────────────────────────

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Antic-Intent, X-Antic-Version")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func copyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// resolveClientDir tries to find the demo client/ directory relative to the
// config file's location so the server can serve the demo UI.
func resolveClientDir(cfgPath string) string {
	base := filepath.Dir(cfgPath)

	candidates := []string{
		filepath.Join(base, "demo", "client"),
		filepath.Join(base, "..", "demo", "client"),
		filepath.Join(base, "client"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}

func printBanner(cfg *config.SpecLinkConfig, clientDir string) {
	upstreamPort := cfg.Port + 1
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║       ANTIC-PT  SPEC-LINK  v1.0  (Go)               ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf( "║  Proxy:      http://localhost:%s               ║\n", padRight(strconv.Itoa(cfg.Port), 5))
	fmt.Printf( "║  Spec route: http://localhost:%s%s       ║\n", strconv.Itoa(cfg.Port), padRight(cfg.Prefix, 10))
	fmt.Printf( "║  Upstream:   %s  ║\n", padRight(cfg.FormalTrack.Upstream, 38))
	fmt.Printf( "║  Vault:      %s                                  ║\n", padRight(cfg.Vault.Driver, 7))
	if clientDir != "" {
		fmt.Printf("║  Demo UI:    http://localhost:%s               ║\n", padRight(strconv.Itoa(cfg.Port), 5))
	}
	fmt.Printf( "║  Demo API:   http://localhost:%s (embedded)     ║\n", padRight(strconv.Itoa(upstreamPort), 5))
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}
