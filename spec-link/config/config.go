// Package config handles parsing and validation of the antic-pt.yaml configuration file.
package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// SpecLinkConfig represents the root configuration structure for the Spec-Link proxy.
type SpecLinkConfig struct {
	// Port is the port number the proxy will listen on.
	Port int `yaml:"port"`
	// Prefix is the URI prefix used to trigger Antic-PT logic (e.g., "/spec").
	Prefix string `yaml:"prefix"`
	// Vault contains configuration for the State-Vault backing store.
	Vault VaultConfig `yaml:"vault"`
	// FormalTrack contains configuration for the authoritative upstream API.
	FormalTrack FormalTrackConfig `yaml:"formal_track"`
	// WriteTrack contains optional configuration for the write-side upstream.
	WriteTrack WriteTrackConfig `yaml:"write_track"`
	// Reconcile contains configuration for track reconciliation strategies.
	Reconcile ReconcileConfig `yaml:"reconciliation"`
	// Endpoints lists per-endpoint field classification and behaviour configuration.
	Endpoints []EndpointConfig `yaml:"endpoints"`
}

// VaultConfig defines the connection and behaviour settings for the State-Vault.
type VaultConfig struct {
	// Driver specifies the storage backend (e.g., "memory", "redis").
	Driver string `yaml:"driver"`
	// URL is the connection string for the driver (if applicable).
	URL string `yaml:"url"`
	// DefaultTTL is the default time-to-live for vault entries in milliseconds.
	DefaultTTL int `yaml:"default_ttl_ms"`
}

// FormalTrackConfig defines the behaviour of the authoritative execution track.
type FormalTrackConfig struct {
	// Upstream is the base URL of the authoritative API.
	Upstream string `yaml:"upstream"`
	// TimeoutMS is the maximum time to wait for the upstream response.
	TimeoutMS int `yaml:"timeout_ms"`
}

// WriteTrackConfig defines the write-side provisional commit upstream.
type WriteTrackConfig struct {
	// Upstream is the base URL of the write upstream (may differ from read upstream).
	Upstream string `yaml:"upstream"`
}

// ReconcileConfig defines the strategy for reconciling Fast and Formal tracks.
type ReconcileConfig struct {
	// Strategy specifies the reconciliation mode ("patch", "replace").
	Strategy string `yaml:"strategy"`
}

// EndpointConfig declares field-level classification and behaviour for a single API endpoint.
// Path supports :param wildcards (e.g. /api/servers/:id).
type EndpointConfig struct {
	// Path is the URL path pattern this configuration applies to.
	Path string `yaml:"path"`
	// Volatility is the endpoint-level default volatility hint for unlisted fields.
	// Values: "high", "low", "invariant". Default: "low".
	Volatility string `yaml:"volatility"`
	// MaxStalenessMs is the maximum vault entry age (ms) before the Fast Track is skipped.
	MaxStalenessMs int `yaml:"max_staleness_ms"`
	// ReplaceThreshold is the fraction of changed SPECULATIVE fields that triggers REPLACE.
	// Range: 0.0–1.0. Default: 0.5.
	ReplaceThreshold float64 `yaml:"replace_threshold"`
	// DefaultClass is the fallback certainty class for fields not explicitly listed.
	// Values: "SPECULATIVE", "DEFERRED". Default: "SPECULATIVE".
	DefaultClass string `yaml:"default_class"`
	// Fields maps field names to their explicit certainty class and volatility.
	Fields map[string]FieldConfig `yaml:"fields"`
}

// FieldConfig declares the certainty class and optional volatility hint for a single field.
type FieldConfig struct {
	// Class is the certainty class for this field.
	// Values: "SPECULATIVE", "DEFERRED", "INVARIANT", "PROVISIONAL".
	Class string `yaml:"class"`
	// Volatility overrides the endpoint-level volatility for this specific field.
	// Values: "high", "low", "invariant".
	Volatility string `yaml:"volatility"`
}

// FormalTrackTimeout returns the upstream timeout as a time.Duration.
func (c *SpecLinkConfig) FormalTrackTimeout() time.Duration {
	return time.Duration(c.FormalTrack.TimeoutMS) * time.Millisecond
}

// Load reads and parses a configuration file from the disk and applies sensible defaults.
func Load(path string) (*SpecLinkConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg SpecLinkConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Proxy defaults
	if cfg.Port == 0 {
		cfg.Port = 4000
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "/spec"
	}

	// Vault defaults
	if cfg.Vault.Driver == "" {
		cfg.Vault.Driver = "memory"
	}
	if cfg.Vault.DefaultTTL == 0 {
		cfg.Vault.DefaultTTL = 30_000
	}

	// Formal track defaults
	if cfg.FormalTrack.TimeoutMS == 0 {
		cfg.FormalTrack.TimeoutMS = 5000
	}

	// Reconciliation defaults
	if cfg.Reconcile.Strategy == "" {
		cfg.Reconcile.Strategy = "patch"
	}

	// Endpoint defaults
	for i := range cfg.Endpoints {
		ep := &cfg.Endpoints[i]
		if ep.Volatility == "" {
			ep.Volatility = "low"
		}
		if ep.MaxStalenessMs == 0 {
			ep.MaxStalenessMs = 5000
		}
		if ep.ReplaceThreshold == 0 {
			ep.ReplaceThreshold = 0.5
		}
		if ep.DefaultClass == "" {
			ep.DefaultClass = "SPECULATIVE"
		}
		// Inherit endpoint volatility to any field that did not declare its own.
		for name, fc := range ep.Fields {
			if fc.Volatility == "" {
				fc.Volatility = ep.Volatility
				ep.Fields[name] = fc
			}
		}
	}

	return &cfg, nil
}
