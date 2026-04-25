// Spec-Link is the reference Go proxy implementation for Antic-PT v0.2.1.
//
// It reads antic-pt.yaml from the working directory (or the path given by
// the -config flag), initialises the State Vault, field classifier, and
// signal hub, then listens for connections on the configured port.
//
// In demo mode (-seed flag), it also pre-warms the vault from the
// upstream /seed/* endpoint so the first /spec/* request is always
// a cache hit demonstrating the speculative path.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"antic-pt/spec-link/config"
	"antic-pt/spec-link/proxy"
	"antic-pt/spec-link/vault"
)

func main() {
	configPath := flag.String("config", "antic-pt.yaml", "path to configuration file")
	seedVault := flag.Bool("seed", false, "pre-warm vault from upstream /seed/* on startup")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("[spec-link] failed to load config: %v", err)
	}

	// Initialise State Vault.
	var v vault.Vault
	switch cfg.Vault.Driver {
	case "redis":
		rv, err := vault.NewRedis(cfg.Vault.URL, cfg.Vault.DefaultTTL)
		if err != nil {
			log.Fatalf("[spec-link] redis vault init failed: %v", err)
		}
		v = rv
		log.Printf("[spec-link] vault: redis (%s)", cfg.Vault.URL)
	default:
		v = vault.NewMemory()
		log.Printf("[spec-link] vault: memory")
	}

	// Optional: pre-warm the vault from the upstream /seed/* endpoint.
	// This ensures the first /spec/* request is a cache hit.
	if *seedVault {
		seedResources := []struct{ resource, id string }{
			{"api/user", "1"},
			{"api/feed", "1"},
			{"api/dashboard", "1"},
		}
		client := &http.Client{Timeout: 5 * time.Second}
		for _, r := range seedResources {
			url := fmt.Sprintf("%s/seed/%s/%s", cfg.FormalTrack.Upstream, r.resource, r.id)
			resp, err := client.Get(url)
			if err != nil {
				log.Printf("[spec-link] seed: could not reach %s: %v", url, err)
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var data map[string]interface{}
			if err := json.Unmarshal(body, &data); err != nil {
				log.Printf("[spec-link] seed: failed to parse %s: %v", url, err)
				continue
			}
			v.Set(r.resource, r.id, data)
			log.Printf("[spec-link] seed: primed vault for %s/%s", r.resource, r.id)
		}
	}

	// Initialise the multiplexed signal hub (GET /antic/signals).
	hub := proxy.NewSignalHub()

	// Initialise the read-side proxy handler.
	handler := proxy.NewHandler(cfg, v, hub)

	// Initialise the write-side provisional commit handler.
	// Write upstream can be overridden; defaults to same upstream as read track.
	writeUpstream := cfg.FormalTrack.Upstream
	// For the Binance write demo the write upstream is the local exchange server.
	if wu := cfg.WriteTrack.Upstream; wu != "" {
		writeUpstream = wu
	}
	writeHandler := proxy.NewWriteHandler(writeUpstream, hub)

	mux := http.NewServeMux()

	// Signal channel endpoint — one persistent SSE connection per client.
	mux.HandleFunc("/antic/signals", hub.ServeSignals)

	// Health check endpoint (non-keep-alive).
	mux.HandleFunc("/antic/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"online"}`))
	})

	// Spec-Link write-side provisional commit endpoint.
	mux.HandleFunc("/spec-write/", writeHandler.HandleWrite)

	// Spec-Link dual-track read endpoint.
	mux.HandleFunc(cfg.Prefix+"/", handler.HandleSpec)

	// Transparent passthrough for all other routes (API, static files via upstream).
	mux.HandleFunc("/", handler.HandlePassthrough)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf(`
╔════════════════════════════════════════════════════════════╗
║           SPEC-LINK v0.2.1 (Antic-PT)                     ║
╠════════════════════════════════════════════════════════════╣
║  Proxy:          http://localhost%s                    ║
║  Spec prefix:    %s/*                                   ║
║  Signal channel: http://localhost%s/antic/signals      ║
║  Upstream:       %s                  ║
╚════════════════════════════════════════════════════════════╝`,
		addr, cfg.Prefix, addr, cfg.FormalTrack.Upstream)

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second, // longer for SSE signal connections
		IdleTimeout:  120 * time.Second,
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("[spec-link] server error: %v", err)
	}
}
