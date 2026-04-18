// Package vault integration tests for the Redis driver.
// These tests require a running Redis instance and are skipped automatically
// if the REDIS_URL environment variable is not set.
package vault_test

import (
	"os"
	"testing"
	"time"

	"antic-pt/spec-link/vault"
)

// redisURL returns the Redis URL from the environment, or skips the test.
func redisURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL not set; skipping Redis integration tests")
	}
	return url
}

func TestRedisVault_SetAndGet(t *testing.T) {
	v, err := vault.NewRedis(redisURL(t), 5000)
	if err != nil {
		t.Fatalf("failed to connect to Redis: %v", err)
	}

	data := map[string]interface{}{
		"name": "Alice",
		"role": "engineer",
	}

	entry := v.Set("user", "test-1", data)
	if entry == nil {
		t.Fatal("Set returned nil")
	}
	if entry.Version != 1 {
		t.Errorf("expected version 1, got %d", entry.Version)
	}

	got := v.Get("user", "test-1")
	if got == nil {
		t.Fatal("Get returned nil after Set")
	}
	if got.Data["name"] != "Alice" {
		t.Errorf("expected name Alice, got %v", got.Data["name"])
	}
}

func TestRedisVault_VersionIncrements(t *testing.T) {
	v, err := vault.NewRedis(redisURL(t), 5000)
	if err != nil {
		t.Fatalf("failed to connect to Redis: %v", err)
	}

	// Use a unique key to avoid pollution from other tests.
	resource, id := "counter", "test-incr"
	v.Delete(resource, id) // Clean state before test.

	e1 := v.Set(resource, id, map[string]interface{}{"v": 1})
	e2 := v.Set(resource, id, map[string]interface{}{"v": 2})
	e3 := v.Set(resource, id, map[string]interface{}{"v": 3})

	if e2.Version != e1.Version+1 {
		t.Errorf("expected version %d, got %d", e1.Version+1, e2.Version)
	}
	if e3.Version != e2.Version+1 {
		t.Errorf("expected version %d, got %d", e2.Version+1, e3.Version)
	}
}

func TestRedisVault_Delete(t *testing.T) {
	v, err := vault.NewRedis(redisURL(t), 5000)
	if err != nil {
		t.Fatalf("failed to connect to Redis: %v", err)
	}

	v.Set("user", "test-del", map[string]interface{}{"name": "Bob"})
	v.Delete("user", "test-del")

	got := v.Get("user", "test-del")
	if got != nil {
		t.Error("expected nil after Delete, but got an entry")
	}
}

func TestRedisVault_CacheMiss(t *testing.T) {
	v, err := vault.NewRedis(redisURL(t), 5000)
	if err != nil {
		t.Fatalf("failed to connect to Redis: %v", err)
	}

	got := v.Get("user", "nonexistent-key-xyz")
	if got != nil {
		t.Error("expected nil for nonexistent key")
	}
}

func TestRedisVault_TTLExpiry(t *testing.T) {
	// Use a very short TTL of 300ms to test expiry without a long wait.
	v, err := vault.NewRedis(redisURL(t), 300)
	if err != nil {
		t.Fatalf("failed to connect to Redis: %v", err)
	}

	v.Set("user", "test-ttl", map[string]interface{}{"name": "Expiring"})

	got := v.Get("user", "test-ttl")
	if got == nil {
		t.Fatal("expected entry before TTL expiry")
	}

	time.Sleep(400 * time.Millisecond)

	expired := v.Get("user", "test-ttl")
	if expired != nil {
		t.Error("expected nil after TTL expiry")
	}
}
