// Package vault provides the State-Vault abstraction for Antic-PT.
// The Vault stores versioned resource snapshots that the Fast Track uses to
// serve immediate speculative responses.
package vault

import (
	"sync"
	"time"
)

// Entry represents a single versioned snapshot of a resource stored in the vault.
type Entry struct {
	// Data is the raw resource payload.
	Data map[string]interface{}
	// Version is the monotonic version number of this entry.
	Version int
	// UpdatedAt is the timestamp when this entry was last written.
	UpdatedAt time.Time
}

// AgeMS returns the age of the entry in milliseconds relative to the current time.
func (e *Entry) AgeMS() int64 {
	return time.Since(e.UpdatedAt).Milliseconds()
}

// Vault defines the interface for State-Vault implementations.
// A Vault must store and retrieve versioned resource snapshots.
type Vault interface {
	// Get retrieves an entry by resource type and identifier.
	// Returns nil if the entry does not exist.
	Get(resource, id string) *Entry

	// Set writes or overwrites an entry with a bumped version number.
	// Returns the newly created entry.
	Set(resource, id string, data map[string]interface{}) *Entry

	// Delete removes an entry from the vault.
	Delete(resource, id string)
}

// MemoryVault is a thread-safe, in-process implementation of the Vault interface.
// It uses a local map protected by a Read-Write mutex.
type MemoryVault struct {
	mu      sync.RWMutex
	entries map[string]*Entry
}

// NewMemory initializes and returns a new MemoryVault instance.
func NewMemory() *MemoryVault {
	return &MemoryVault{
		entries: make(map[string]*Entry),
	}
}

// key generates a canonical vault key for a resource and its identifier.
func key(resource, id string) string {
	return resource + ":" + id
}

// Get retrieves an entry from the memory vault.
func (v *MemoryVault) Get(resource, id string) *Entry {
	v.mu.RLock()
	defer v.mu.RUnlock()
	e, ok := v.entries[key(resource, id)]
	if !ok {
		return nil
	}
	// Return a copy to prevent external mutation of the vault's internal state.
	cp := *e
	return &cp
}

// Set writes a new resource payload to the vault, incrementing its version.
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

// Seed populates the vault with a specific entry version.
// This is primarily used for initializing demonstration or test data.
func (v *MemoryVault) Seed(resource, id string, data map[string]interface{}, version int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.entries[key(resource, id)] = &Entry{
		Data:      data,
		Version:   version,
		UpdatedAt: time.Now(),
	}
}

// Delete removes an entry from the memory vault.
func (v *MemoryVault) Delete(resource, id string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.entries, key(resource, id))
}
