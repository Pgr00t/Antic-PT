// Package fields provides the field classification engine for Antic-PT v0.2.
//
// A Classifier matches an incoming request path against configured endpoint patterns
// and answers per-field questions: what certainty class does this field carry, what
// is its declared volatility, and what thresholds apply for REPLACE decisions.
//
// Field certainty classes (per spec Section 4):
//   - SPECULATIVE  – served from vault immediately; corrected via PATCH if wrong.
//   - DEFERRED     – withheld from Fast Track; delivered via FILL after Formal Track.
//   - INVARIANT    – cached indefinitely; violation triggers ABORT.
//   - PROVISIONAL  – reserved for write-path (v1.0); treated as DEFERRED on reads.
package fields

import (
	"regexp"
	"strings"

	"antic-pt/spec-link/config"
)

const (
	ClassSpeculative = "SPECULATIVE"
	ClassDeferred    = "DEFERRED"
	ClassInvariant   = "INVARIANT"
	ClassProvisional = "PROVISIONAL"

	VolatilityHigh      = "high"
	VolatilityLow       = "low"
	VolatilityInvariant = "invariant"
)

// compiledEndpoint pairs a route pattern regex with its endpoint configuration.
type compiledEndpoint struct {
	pattern *regexp.Regexp
	config  config.EndpointConfig
}

// Classifier resolves per-field certainty class and volatility for a given request path.
// It is compiled once at startup from the proxy configuration and is safe for concurrent use.
type Classifier struct {
	endpoints []compiledEndpoint
}

// NewClassifier compiles the provided endpoint configurations into path-matching patterns.
// Patterns use :param wildcards that match a single path segment (no slashes).
func NewClassifier(endpoints []config.EndpointConfig) *Classifier {
	compiled := make([]compiledEndpoint, 0, len(endpoints))
	for _, ep := range endpoints {
		pattern := pathToRegex(ep.Path)
		re, err := regexp.Compile("^" + pattern + "$")
		if err != nil {
			continue // skip malformed patterns
		}
		compiled = append(compiled, compiledEndpoint{pattern: re, config: ep})
	}
	return &Classifier{endpoints: compiled}
}

// pathToRegex converts a path pattern with :param wildcards to a regex string.
// Example: /api/servers/:id → /api/servers/[^/]+
func pathToRegex(path string) string {
	// Escape dots and replace :param with a non-slash segment matcher.
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if strings.HasPrefix(seg, ":") {
			segments[i] = "[^/]+"
		} else {
			segments[i] = regexp.QuoteMeta(seg)
		}
	}
	return strings.Join(segments, "/")
}

// matchEndpoint returns the first endpoint configuration whose pattern matches path, or nil.
func (c *Classifier) matchEndpoint(path string) *config.EndpointConfig {
	// Strip query string for matching.
	if idx := strings.Index(path, "?"); idx != -1 {
		path = path[:idx]
	}
	for i := range c.endpoints {
		if c.endpoints[i].pattern.MatchString(path) {
			cfg := c.endpoints[i].config
			return &cfg
		}
	}
	return nil
}

// ClassOf returns the certainty class for field in a response at path.
// Falls back to the endpoint's DefaultClass, then SPECULATIVE if no endpoint matches.
func (c *Classifier) ClassOf(path, field string) string {
	ep := c.matchEndpoint(path)
	if ep == nil {
		return ClassSpeculative
	}
	if fc, ok := ep.Fields[field]; ok && fc.Class != "" {
		return normaliseClass(fc.Class)
	}
	return normaliseClass(ep.DefaultClass)
}

// VolatilityOf returns the declared volatility hint for field at path.
// Falls back to the endpoint volatility, then "low".
func (c *Classifier) VolatilityOf(path, field string) string {
	ep := c.matchEndpoint(path)
	if ep == nil {
		return VolatilityLow
	}
	if fc, ok := ep.Fields[field]; ok && fc.Volatility != "" {
		return fc.Volatility
	}
	return ep.Volatility
}

// ReplaceThreshold returns the configured fraction of changed SPECULATIVE fields
// that triggers REPLACE instead of PATCH. Defaults to 0.5.
func (c *Classifier) ReplaceThreshold(path string) float64 {
	ep := c.matchEndpoint(path)
	if ep == nil {
		return 0.5
	}
	return ep.ReplaceThreshold
}

// MaxStalenessMs returns the maximum vault entry age (ms) before the Fast Track
// is skipped for this endpoint. Defaults to 5000ms.
func (c *Classifier) MaxStalenessMs(path string) int {
	ep := c.matchEndpoint(path)
	if ep == nil {
		return 5000
	}
	return ep.MaxStalenessMs
}

// VolatilityMap returns a map of field → volatility for all fields in the Formal Track
// response, used to build the X-Antic-Volatility header.
func (c *Classifier) VolatilityMap(path string, fields []string) map[string]string {
	result := make(map[string]string, len(fields))
	for _, f := range fields {
		result[f] = c.VolatilityOf(path, f)
	}
	return result
}

// DeferredFields returns the subset of the provided field names that are classified DEFERRED
// (or PROVISIONAL, which is treated as DEFERRED on read paths).
func (c *Classifier) DeferredFields(path string, fields []string) []string {
	var deferred []string
	for _, f := range fields {
		class := c.ClassOf(path, f)
		if class == ClassDeferred || class == ClassProvisional {
			deferred = append(deferred, f)
		}
	}
	return deferred
}

// InvariantFields returns the subset of the provided field names that are classified INVARIANT.
func (c *Classifier) InvariantFields(path string, fields []string) []string {
	var invariant []string
	for _, f := range fields {
		if c.ClassOf(path, f) == ClassInvariant {
			invariant = append(invariant, f)
		}
	}
	return invariant
}

// normaliseClass uppercases the class string and substitutes aliases.
func normaliseClass(s string) string {
	switch strings.ToUpper(s) {
	case ClassDeferred, ClassInvariant, ClassProvisional:
		return strings.ToUpper(s)
	default:
		return ClassSpeculative
	}
}
