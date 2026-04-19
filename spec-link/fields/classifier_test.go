package fields_test

import (
	"testing"

	"antic-pt/spec-link/config"
	"antic-pt/spec-link/fields"
)

func makeClassifier() *fields.Classifier {
	endpoints := []config.EndpointConfig{
		{
			Path:             "/api/servers/:id",
			Volatility:       "low",
			MaxStalenessMs:   5000,
			ReplaceThreshold: 0.5,
			DefaultClass:     "SPECULATIVE",
			Fields: map[string]config.FieldConfig{
				"serverId":       {Class: "INVARIANT", Volatility: "invariant"},
				"region":         {Class: "INVARIANT", Volatility: "invariant"},
				"cpuUsage":       {Class: "SPECULATIVE", Volatility: "high"},
				"memoryUsage":    {Class: "SPECULATIVE", Volatility: "high"},
				"uptimeDays":     {Class: "SPECULATIVE", Volatility: "low"},
				"activeAlarms":   {Class: "DEFERRED"},
				"accountBalance": {Class: "DEFERRED"},
			},
		},
		{
			Path:         "/api/accounts/:id",
			Volatility:   "low",
			DefaultClass: "DEFERRED",
			Fields:       map[string]config.FieldConfig{},
		},
	}
	return fields.NewClassifier(endpoints)
}

// ── ClassOf ───────────────────────────────────────────────────────────────────

func TestClassOf_ExplicitInvariant(t *testing.T) {
	c := makeClassifier()
	if got := c.ClassOf("/api/servers/042", "serverId"); got != "INVARIANT" {
		t.Errorf("expected INVARIANT, got %s", got)
	}
}

func TestClassOf_ExplicitDeferred(t *testing.T) {
	c := makeClassifier()
	if got := c.ClassOf("/api/servers/042", "activeAlarms"); got != "DEFERRED" {
		t.Errorf("expected DEFERRED, got %s", got)
	}
}

func TestClassOf_ExplicitSpeculative(t *testing.T) {
	c := makeClassifier()
	if got := c.ClassOf("/api/servers/042", "cpuUsage"); got != "SPECULATIVE" {
		t.Errorf("expected SPECULATIVE, got %s", got)
	}
}

func TestClassOf_UnlistedFieldInheritsDefault(t *testing.T) {
	c := makeClassifier()
	// "uptime" is not listed → inherits DefaultClass: SPECULATIVE
	if got := c.ClassOf("/api/servers/042", "uptime"); got != "SPECULATIVE" {
		t.Errorf("expected SPECULATIVE, got %s", got)
	}
}

func TestClassOf_DeferredDefault(t *testing.T) {
	c := makeClassifier()
	// /api/accounts/:id has DefaultClass: DEFERRED, no explicit fields
	if got := c.ClassOf("/api/accounts/1", "balance"); got != "DEFERRED" {
		t.Errorf("expected DEFERRED for default-deferred endpoint, got %s", got)
	}
}

func TestClassOf_UnknownPath_FallsBackToSpeculative(t *testing.T) {
	c := makeClassifier()
	if got := c.ClassOf("/api/unknown/path", "anyField"); got != "SPECULATIVE" {
		t.Errorf("expected SPECULATIVE fallback for unknown path, got %s", got)
	}
}

// ── VolatilityOf ──────────────────────────────────────────────────────────────

func TestVolatilityOf_ExplicitHigh(t *testing.T) {
	c := makeClassifier()
	if got := c.VolatilityOf("/api/servers/042", "cpuUsage"); got != "high" {
		t.Errorf("expected high, got %s", got)
	}
}

func TestVolatilityOf_InheritsEndpointDefault(t *testing.T) {
	c := makeClassifier()
	// "uptime" not listed — inherits endpoint volatility "low"
	if got := c.VolatilityOf("/api/servers/042", "uptime"); got != "low" {
		t.Errorf("expected low, got %s", got)
	}
}

// ── ReplaceThreshold ──────────────────────────────────────────────────────────

func TestReplaceThreshold_Configured(t *testing.T) {
	c := makeClassifier()
	if got := c.ReplaceThreshold("/api/servers/042"); got != 0.5 {
		t.Errorf("expected 0.5, got %f", got)
	}
}

func TestReplaceThreshold_DefaultForUnknown(t *testing.T) {
	c := makeClassifier()
	if got := c.ReplaceThreshold("/unknown"); got != 0.5 {
		t.Errorf("expected default 0.5, got %f", got)
	}
}

// ── DeferredFields ────────────────────────────────────────────────────────────

func TestDeferredFields(t *testing.T) {
	c := makeClassifier()
	all := []string{"serverId", "cpuUsage", "activeAlarms", "accountBalance", "uptimeDays"}
	deferred := c.DeferredFields("/api/servers/042", all)
	if len(deferred) != 2 {
		t.Errorf("expected 2 deferred fields, got %d: %v", len(deferred), deferred)
	}
}

// ── InvariantFields ───────────────────────────────────────────────────────────

func TestInvariantFields(t *testing.T) {
	c := makeClassifier()
	all := []string{"serverId", "region", "cpuUsage", "activeAlarms"}
	invariant := c.InvariantFields("/api/servers/042", all)
	if len(invariant) != 2 {
		t.Errorf("expected 2 invariant fields, got %d: %v", len(invariant), invariant)
	}
}

// ── Path matching ─────────────────────────────────────────────────────────────

func TestPathMatching_WithQueryString(t *testing.T) {
	c := makeClassifier()
	// Query strings should be stripped before matching.
	if got := c.ClassOf("/api/servers/042?refresh=true", "serverId"); got != "INVARIANT" {
		t.Errorf("expected INVARIANT with query string, got %s", got)
	}
}

func TestPathMatching_MultiSegmentParam(t *testing.T) {
	c := makeClassifier()
	// :id should match any single segment, not slashes.
	if got := c.ClassOf("/api/servers/node-42b", "cpuUsage"); got != "SPECULATIVE" {
		t.Errorf("expected SPECULATIVE for alphanumeric param, got %s", got)
	}
}
