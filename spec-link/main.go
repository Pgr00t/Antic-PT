// Spec-Link is the reference Go proxy implementation for Antic-PT v0.2.
//
// It reads antic-pt.yaml from the working directory (or the path given by
// the -config flag), initialises the State Vault, field classifier, and
// signal hub, then listens for connections on the configured port.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"antic-pt/spec-link/config"
	"antic-pt/spec-link/proxy"
	"antic-pt/spec-link/vault"
)

func main() {
	configPath := flag.String("config", "antic-pt.yaml", "path to configuration file")
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

	// Initialise the multiplexed signal hub (GET /antic/signals).
	hub := proxy.NewSignalHub()

	// Initialise the proxy handler.
	handler := proxy.NewHandler(cfg, v, hub)

	mux := http.NewServeMux()

	// Signal channel endpoint — one persistent SSE connection per client.
	mux.HandleFunc("/antic/signals", hub.ServeSignals)

	// Spec-Link dual-track endpoint.
	mux.HandleFunc(cfg.Prefix+"/", handler.HandleSpec)

	// Transparent passthrough for all other routes.
	mux.HandleFunc("/", handler.HandlePassthrough)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf(`
╔═══════════════════════════════════════════════════════╗
║         SPEC-LINK v0.2.1 (Antic-PT)                  ║
╠═══════════════════════════════════════════════════════╣
║  Listening:      http://localhost%s                ║
║  Spec prefix:    %s/*                               ║
║  Signal channel: http://localhost%s/antic/signals  ║
║  Upstream:       %s              ║
╚═══════════════════════════════════════════════════════╝`,
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
