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
	Port        int              `yaml:"port"`
	// Prefix is the URI prefix used to trigger Antic-PT logic (e.g., "/spec").
	Prefix      string           `yaml:"prefix"`
	// Vault contains configuration for the State-Vault backing store.
	Vault       VaultConfig      `yaml:"vault"`
	// Intent contains configuration for request classification.
	Intent      IntentConfig     `yaml:"intent"`
	// FormalTrack contains configuration for the authoritative upstream API.
	FormalTrack FormalTrackConfig `yaml:"formal_track"`
	// Reconcile contains configuration for track reconciliation strategies.
	Reconcile   ReconcileConfig  `yaml:"reconciliation"`
}

// VaultConfig defines the connection and behaviour settings for the State-Vault.
type VaultConfig struct {
	// Driver specifies the storage backend (e.g., "memory", "redis").
	Driver     string `yaml:"driver"`
	// URL is the connection string for the driver (if applicable).
	URL        string `yaml:"url"`
	// DefaultTTL is the default time-to-live for vault entries in milliseconds.
	DefaultTTL int    `yaml:"default_ttl_ms"`
}

// IntentConfig defines how incoming requests are classified for speculation.
type IntentConfig struct {
	// Mode specifies the default classification mode ("auto", "guided", "bypass").
	Mode                string  `yaml:"mode"`
	// AIConfidenceThresh is the minimum confidence score required for auto-speculation.
	AIConfidenceThresh  float64 `yaml:"ai_confidence_threshold"`
}

// FormalTrackConfig defines the behaviour of the authoritative execution track.
type FormalTrackConfig struct {
	// Upstream is the base URL of the authoritative API.
	Upstream  string `yaml:"upstream"`
	// TimeoutMS is the maximum time to wait for the upstream response.
	TimeoutMS int    `yaml:"timeout_ms"`
}

// ReconcileConfig defines the strategy for reconciling Fast and Formal tracks.
type ReconcileConfig struct {
	// Strategy specifies the reconciliation mode ("patch", "replace").
	Strategy string `yaml:"strategy"`
}

// FormalTrackTimeout returns the upstream timeout as a time.Duration.
func (c *SpecLinkConfig) FormalTrackTimeout() time.Duration {
	return time.Duration(c.FormalTrack.TimeoutMS) * time.Millisecond
}

// Load reads and parses a configuration file from the disk.
// It applies sensible defaults for missing fields.
func Load(path string) (*SpecLinkConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg SpecLinkConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Apply sensible defaults
	if cfg.Port == 0 {
		cfg.Port = 4000
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "/spec"
	}
	if cfg.Vault.Driver == "" {
		cfg.Vault.Driver = "memory"
	}
	if cfg.Vault.DefaultTTL == 0 {
		cfg.Vault.DefaultTTL = 30_000
	}
	if cfg.Intent.Mode == "" {
		cfg.Intent.Mode = "auto"
	}
	if cfg.Intent.AIConfidenceThresh == 0 {
		cfg.Intent.AIConfidenceThresh = 0.75
	}
	if cfg.FormalTrack.TimeoutMS == 0 {
		cfg.FormalTrack.TimeoutMS = 5000
	}
	if cfg.Reconcile.Strategy == "" {
		cfg.Reconcile.Strategy = "patch"
	}

	return &cfg, nil
}
