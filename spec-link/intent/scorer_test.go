package intent_test

import (
	"testing"
	"time"

	"antic-pt/spec-link/intent"
	"antic-pt/spec-link/vault"
)

// makeEntry is a helper that builds a vault.Entry with a given age and version.
func makeEntry(ageMs int64, version int) *vault.Entry {
	return &vault.Entry{
		Data:      map[string]interface{}{"key": "value"},
		Version:   version,
		UpdatedAt: time.Now().Add(-time.Duration(ageMs) * time.Millisecond),
	}
}

func TestScore_NilEntry(t *testing.T) {
	s := intent.NewScorer()
	if got := s.Score("user", nil); got != 0.0 {
		t.Errorf("expected 0.0 for nil entry, got %f", got)
	}
}

func TestScore_FreshEntry(t *testing.T) {
	s := intent.NewScorer()
	// A very fresh entry (100ms old, version 1) should score high.
	entry := makeEntry(100, 1)
	score := s.Score("user", entry)
	if score < 0.85 {
		t.Errorf("expected high score for fresh entry, got %f", score)
	}
}

func TestScore_StaleEntry(t *testing.T) {
	s := intent.NewScorer()
	// A 60-second-old entry should score well below the default threshold.
	entry := makeEntry(60_000, 1)
	score := s.Score("user", entry)
	if score >= 0.75 {
		t.Errorf("expected low score for stale entry (60s), got %f", score)
	}
}

func TestScore_AccuracyImproves(t *testing.T) {
	s := intent.NewScorer()
	entry := makeEntry(500, 1) // fresh entry

	// Before any data: optimistic default (0.85 accuracy component)
	baseline := s.Score("user", entry)

	// Record 10 perfect confirms → accuracy should climb to 1.0
	for i := 0; i < 10; i++ {
		s.RecordOutcome("user", "confirm")
	}
	afterConfirms := s.Score("user", entry)

	if afterConfirms < baseline {
		t.Errorf("score should increase after confirms: baseline=%f after=%f", baseline, afterConfirms)
	}
}

func TestScore_AccuracyDegrades(t *testing.T) {
	s := intent.NewScorer()
	entry := makeEntry(500, 1)

	// Record 10 replaces → accuracy is 0/10 = 0.0
	for i := 0; i < 10; i++ {
		s.RecordOutcome("user", "replace")
	}
	score := s.Score("user", entry)

	// Even with a fresh entry, bad accuracy should crush the score below threshold.
	if score >= 0.75 {
		t.Errorf("expected low score after 10 replaces, got %f", score)
	}
}

func TestScore_HighVelocityPenalty(t *testing.T) {
	s := intent.NewScorer()
	// A resource at version 1000 that is only 5 seconds old is very volatile.
	highVel := makeEntry(5_000, 1000)
	lowVel := makeEntry(5_000, 1)

	if s.Score("user", highVel) >= s.Score("user", lowVel) {
		t.Error("high-velocity entry should score lower than low-velocity entry")
	}
}

func TestRecordOutcome_ThreadSafe(t *testing.T) {
	s := intent.NewScorer()
	done := make(chan struct{})

	for i := 0; i < 100; i++ {
		go func(i int) {
			if i%2 == 0 {
				s.RecordOutcome("user", "confirm")
			} else {
				s.RecordOutcome("user", "patch")
			}
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 100; i++ {
		<-done
	}

	confirms, total, _ := s.Accuracy("user")
	if total != 100 {
		t.Errorf("expected 100 total outcomes, got %d", total)
	}
	if confirms != 50 {
		t.Errorf("expected 50 confirms, got %d", confirms)
	}
}
