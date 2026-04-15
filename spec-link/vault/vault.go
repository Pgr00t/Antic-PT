// Package vault provides the State-Vault abstraction for Antic-PT.
// The Vault stores versioned resource snapshots for the Fast Track to serve.
package vault

import (
	"sync"
	"time"
)

// Entry is a single versioned snapshot inside the vault.
type Entry struct {
	Data      map[string]interface{}
	Version   int
	UpdatedAt time.Time
}

// AgeMS returns how many milliseconds ago this entry was written.
func (e *Entry) AgeMS() int64 {
	return time.Since(e.UpdatedAt).Milliseconds()
}

// Vault is the interface that every State-Vault driver must implement.
// In v1.0 only MemoryVault is provided; v1.1 will add Redis.
type Vault interface {
	// Get retrieves an entry. Returns nil if not found.
	Get(resource, id string) *Entry
	// Set writes or overwrites an entry, bumping the version.
	Set(resource, id string, data map[string]interface{}) *Entry
	// Delete removes an entry.
	Delete(resource, id string)
}

// ────────────────────────────────────────────────────────────────────────────
// MemoryVault — thread-safe, in-process implementation
// ────────────────────────────────────────────────────────────────────────────

// MemoryVault stores entries in a sync.Map-protected Go map.
type MemoryVault struct {
	mu      sync.RWMutex
	entries map[string]*Entry
}

// NewMemory creates an initialised MemoryVault.
func NewMemory() *MemoryVault {
	return &MemoryVault{
		entries: make(map[string]*Entry),
	}
}

// key returns the canonical vault key for a resource+id pair.
func key(resource, id string) string {
	return resource + ":" + id
}

// Get implements Vault.
func (v *MemoryVault) Get(resource, id string) *Entry {
	v.mu.RLock()
	defer v.mu.RUnlock()
	e, ok := v.entries[key(resource, id)]
	if !ok {
		return nil
	}
	// Return a shallow copy so callers cannot mutate vault state.
	cp := *e
	return &cp
}

// Set implements Vault.
func (v *MemoryVault) Set(resource, id string, data map[string]interface{}) *Entry {
	v.mu.Lock()
	defer v.mu.Unlock()

	k := key(resource, id)
	existing := v.entries[k]

	version := 1
	if existing != nil {
		version = existing.Version + 1
	}

	e := &Entry{
		Data:      data,
		Version:   version,
		UpdatedAt: time.Now(),
	}
	v.entries[k] = e

	cp := *e
	return &cp
}

// Seed writes an entry with a specific version (used for initialising demo data).
func (v *MemoryVault) Seed(resource, id string, data map[string]interface{}, version int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.entries[key(resource, id)] = &Entry{
		Data:      data,
		Version:   version,
		UpdatedAt: time.Now(),
	}
}

// Delete implements Vault.
func (v *MemoryVault) Delete(resource, id string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.entries, key(resource, id))
}
