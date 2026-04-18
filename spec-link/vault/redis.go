// Package vault provides the State-Vault abstraction for Antic-PT.
// This file implements a Redis-backed driver for the Vault interface,
// enabling persistent, distributed speculative caching across multiple
// Spec-Link proxy instances.
package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisVault is a Redis-backed implementation of the Vault interface.
// It stores resource data and version counters as separate keys, using
// atomic INCR for safe version bumping across distributed instances.
//
// Key layout in Redis:
//
//	data:<resource>:<id>    → JSON-encoded map[string]interface{}
//	version:<resource>:<id> → integer (monotonic version counter)
type RedisVault struct {
	client     *redis.Client
	defaultTTL time.Duration
}

// NewRedis creates and validates a new RedisVault connected to the given URL.
// The defaultTTLMs parameter controls how long vault entries live in Redis
// before expiring. A value of 0 disables TTL (keys live forever).
//
// The URL format follows the Redis URI spec: redis://<user>:<password>@<host>:<port>/<db>
func NewRedis(url string, defaultTTLMs int) (*RedisVault, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("invalid Redis URL %q: %w", url, err)
	}

	client := redis.NewClient(opts)

	// Validate connection at startup to fail fast on misconfiguration.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("could not connect to Redis at %q: %w", url, err)
	}

	return &RedisVault{
		client:     client,
		defaultTTL: time.Duration(defaultTTLMs) * time.Millisecond,
	}, nil
}

// dataKey returns the Redis key used to store a resource's JSON payload.
func dataKey(resource, id string) string {
	return "antic:data:" + resource + ":" + id
}

// versionKey returns the Redis key used to store a resource's version counter.
func versionKey(resource, id string) string {
	return "antic:version:" + resource + ":" + id
}

// Get retrieves a vault entry from Redis.
// Returns nil if the entry does not exist or has expired.
func (v *RedisVault) Get(resource, id string) *Entry {
	ctx := context.Background()

	// Fetch data and version in a single pipeline round trip.
	pipe := v.client.Pipeline()
	dataCmd := pipe.Get(ctx, dataKey(resource, id))
	versionCmd := pipe.Get(ctx, versionKey(resource, id))
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil
	}

	dataStr, err := dataCmd.Result()
	if err != nil {
		// Cache miss — key does not exist or has expired.
		return nil
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(dataStr), &data); err != nil {
		return nil
	}

	version := 0
	if vStr, err := versionCmd.Result(); err == nil {
		fmt.Sscanf(vStr, "%d", &version)
	}

	// We estimate UpdatedAt from the remaining TTL of the data key.
	// This gives a best-effort age for the speculative metadata.
	ttlRemaining := v.client.TTL(ctx, dataKey(resource, id)).Val()
	updatedAt := time.Now()
	if v.defaultTTL > 0 && ttlRemaining > 0 {
		updatedAt = time.Now().Add(-(v.defaultTTL - ttlRemaining))
	}

	return &Entry{
		Data:      data,
		Version:   version,
		UpdatedAt: updatedAt,
	}
}

// Set writes a resource payload to Redis, atomically incrementing its version.
// The entry is stored with the configured default TTL.
func (v *RedisVault) Set(resource, id string, data map[string]interface{}) *Entry {
	ctx := context.Background()

	payload, err := json.Marshal(data)
	if err != nil {
		return nil
	}

	// Atomically increment the version counter and store the new data.
	newVersion, err := v.client.Incr(ctx, versionKey(resource, id)).Result()
	if err != nil {
		return nil
	}

	pipe := v.client.Pipeline()
	pipe.Set(ctx, dataKey(resource, id), payload, v.defaultTTL)
	if v.defaultTTL > 0 {
		pipe.Expire(ctx, versionKey(resource, id), v.defaultTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil
	}

	return &Entry{
		Data:      data,
		Version:   int(newVersion),
		UpdatedAt: time.Now(),
	}
}

// Delete removes a vault entry from Redis by deleting both the data and version keys.
func (v *RedisVault) Delete(resource, id string) {
	ctx := context.Background()
	v.client.Del(ctx, dataKey(resource, id), versionKey(resource, id))
}

// Close shuts down the underlying Redis client connection pool.
func (v *RedisVault) Close() error {
	return v.client.Close()
}
