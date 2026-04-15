// Package config handles parsing of the antic-pt.yaml configuration file.
package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// SpecLinkConfig is the root configuration for the Spec-Link proxy.
type SpecLinkConfig struct {
	Port        int              `yaml:"port"`
	Prefix      string           `yaml:"prefix"`
	Vault       VaultConfig      `yaml:"vault"`
	Intent      IntentConfig     `yaml:"intent"`
	FormalTrack FormalTrackConfig `yaml:"formal_track"`
	Reconcile   ReconcileConfig  `yaml:"reconciliation"`
}

// VaultConfig controls the State-Vault backing store.
type VaultConfig struct {
	Driver     string `yaml:"driver"`      // "memory" | "redis"
	URL        string `yaml:"url"`         // redis connection string
	DefaultTTL int    `yaml:"default_ttl_ms"`
}

// IntentConfig controls how requests are classified.
type IntentConfig struct {
	Mode                string  `yaml:"mode"`                    // "auto" | "guided" | "bypass"
	AIConfidenceThresh  float64 `yaml:"ai_confidence_threshold"` // 0.0–1.0
}

// FormalTrackConfig controls the upstream proxy behaviour.
type FormalTrackConfig struct {
	Upstream  string `yaml:"upstream"`   // e.g. "http://localhost:4002"
	TimeoutMS int    `yaml:"timeout_ms"`
}

// ReconcileConfig controls how the Fast Track and Formal Track are reconciled.
type ReconcileConfig struct {
	Strategy string `yaml:"strategy"` // "patch" | "replace"
}

// FormalTrackTimeout returns the formal track timeout as a time.Duration.
func (c *SpecLinkConfig) FormalTrackTimeout() time.Duration {
	return time.Duration(c.FormalTrack.TimeoutMS) * time.Millisecond
}

// Load reads and parses the config file at path, applying sensible defaults.
func Load(path string) (*SpecLinkConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg SpecLinkConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Apply defaults
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
