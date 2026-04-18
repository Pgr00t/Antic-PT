// Package intent provides heuristic-based confidence scoring for Antic-PT speculation decisions.
//
// The scorer evaluates whether a cached vault entry is likely still accurate enough to serve
// as a speculative response. It combines three weighted signals:
//
//   - Cache age         (40%) — newer entries are more likely current
//   - Historical accuracy (40%) — tracks CONFIRM/PATCH/REPLACE outcomes per resource type
//   - Vault version velocity (20%) — rapidly-versioning resources are volatile
//
// A confidence score in [0.0, 1.0] is returned. Scores at or above the configured threshold
// trigger Fast Track speculation; scores below fall back to Formal Track only.
package intent

import (
	"sync"
	"time"

	"antic-pt/spec-link/vault"
)

// outcomeStats tracks reconciliation outcomes for a single resource type.
type outcomeStats struct {
	confirms int
	total    int
}

// Scorer computes a per-request speculation confidence score from live signals.
// It is safe for concurrent use across goroutines.
type Scorer struct {
	mu       sync.RWMutex
	accuracy map[string]*outcomeStats // keyed by resource type (e.g. "user", "feed")
}

// NewScorer initialises a new Scorer with empty accuracy history.
func NewScorer() *Scorer {
	return &Scorer{
		accuracy: make(map[string]*outcomeStats),
	}
}

// Score returns a confidence value in [0.0, 1.0] for speculating on the given vault entry.
// Higher values indicate greater confidence that the cached data is still accurate.
// A nil entry (cache miss) always returns 0.0 — there is nothing to speculate with.
func (s *Scorer) Score(resource string, entry *vault.Entry) float64 {
	if entry == nil {
		return 0.0
	}

	age := ageScore(entry.AgeMS())
	acc := s.accuracyScore(resource)
	vel := velocityScore(entry.Version, entry.AgeMS())

	return 0.4*age + 0.4*acc + 0.2*vel
}

// RecordOutcome records the reconciliation signal emitted by the Formal Track.
// signal must be one of "confirm", "patch", or "replace".
// Confirms increment accuracy; patches and replaces record a miss.
func (s *Scorer) RecordOutcome(resource, signal string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats, ok := s.accuracy[resource]
	if !ok {
		stats = &outcomeStats{}
		s.accuracy[resource] = stats
	}
	stats.total++
	if signal == "confirm" {
		stats.confirms++
	}
}

// accuracyScore returns the fraction of past speculations for this resource type that were
// CONFIRMed (i.e., the speculative data matched the upstream exactly).
// Returns a slightly optimistic 0.85 until at least 5 samples have been collected.
func (s *Scorer) accuracyScore(resource string) float64 {
	s.mu.RLock()
	stats, ok := s.accuracy[resource]
	s.mu.RUnlock()

	if !ok || stats.total < 5 {
		return 0.85 // optimistic bootstrap default
	}
	return float64(stats.confirms) / float64(stats.total)
}

// Accuracy returns a snapshot of accuracy stats for a resource, intended for observability.
// Returns (confirms, total, ratio).
func (s *Scorer) Accuracy(resource string) (confirms, total int, ratio float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stats, ok := s.accuracy[resource]
	if !ok {
		return 0, 0, 0.85
	}
	var r float64
	if stats.total > 0 {
		r = float64(stats.confirms) / float64(stats.total)
	}
	return stats.confirms, stats.total, r
}

// ageScore returns a confidence multiplier based on how old the vault entry is.
func ageScore(ageMs int64) float64 {
	switch {
	case ageMs < 1_000:
		return 1.00
	case ageMs < 5_000:
		return 0.85
	case ageMs < 15_000:
		return 0.65
	case ageMs < 30_000:
		return 0.40
	default:
		return 0.20
	}
}

// velocityScore estimates resource stability from the ratio of version number to age.
// A resource that accumulates many versions quickly is considered volatile.
// Very young entries (< 500ms) are treated as stable — they haven't had time to accumulate changes.
func velocityScore(version int, ageMs int64) float64 {
	if ageMs <= 0 || ageMs < 500 {
		return 1.00 // too young to judge; assume stable
	}
	// versions per minute
	rate := float64(version) / (float64(ageMs) / float64(time.Minute/time.Millisecond))
	switch {
	case rate < 1:
		return 1.00
	case rate < 5:
		return 0.80
	case rate < 20:
		return 0.55
	default:
		return 0.25
	}
}
