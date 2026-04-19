package vault

import (
	"sync"
	"time"
)

// Snapshot is an immutable copy of a vault entry captured at Fast Track serve time.
// It is keyed by the request's Reconcile ID and used as the sole diff baseline
// for that request's Formal Track, regardless of what happens to the live vault.
type Snapshot struct {
	ReconcileID string
	Data        map[string]interface{}
	Version     int
	CapturedAt  time.Time
}

// SnapshotStore holds per-request vault snapshots in memory.
// Snapshots are deleted after Formal Track completion or on timeout.
// It is safe for concurrent use.
type SnapshotStore struct {
	mu sync.Map // map[reconcileID string]*Snapshot
}

// Capture stores an immutable snapshot for the given reconcile ID.
// The data map is deep-copied so subsequent vault mutations do not affect it.
func (s *SnapshotStore) Capture(reconcileID string, entry *Entry) *Snapshot {
	snap := &Snapshot{
		ReconcileID: reconcileID,
		Data:        copyData(entry.Data),
		Version:     entry.Version,
		CapturedAt:  time.Now(),
	}
	s.mu.Store(reconcileID, snap)
	return snap
}

// Get retrieves the snapshot for the given reconcile ID. Returns nil if not found.
func (s *SnapshotStore) Get(reconcileID string) *Snapshot {
	v, ok := s.mu.Load(reconcileID)
	if !ok {
		return nil
	}
	return v.(*Snapshot)
}

// Release deletes the snapshot for the given reconcile ID after Formal Track completes.
func (s *SnapshotStore) Release(reconcileID string) {
	s.mu.Delete(reconcileID)
}

// copyData performs a shallow copy of the top-level fields of a data map.
// This is sufficient for field-level diffing; nested object mutations are not expected.
func copyData(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
