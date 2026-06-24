// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package wsstate

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// US-45.6: Redis-backed wsConfig.
//
// Workspace config (MaxActiveSessions + AutoApprovePermissions) is
// fetched from the Workspace CRD on first access and cached. Moving to
// Redis ensures all replicas share the same config view — previously,
// a config change on replica A's cache wouldn't propagate to replica B
// until B's own watcher fired.
//
// Same fail-through pattern as pwCache (US-45.4): Redis is a cache, the
// source of truth is the Workspace CRD. On Redis error, GetWorkspaceConfig
// returns (zero, false) so the caller falls back to fetching the CRD.
//
// Design differences from pwCache:
// - TTL = 5 minutes (config can change more often than passwords)
// - JSON-serialized Config struct (not a plaintext string)

// configKey returns the canonical Redis key for a workspace's cached config.
func configKey(workspaceID string) string {
	return fmt.Sprintf("ws:{%s}:config", workspaceID)
}

// ---------------------------------------------------------------------------
// GetWorkspaceConfig / SetWorkspaceConfig
// ---------------------------------------------------------------------------

func TestRedisStore_GetWorkspaceConfig_Miss_ReturnsFalse(t *testing.T) {
	store, _, _, cleanup := setupRedisStore(t)
	defer cleanup()

	_, ok := store.GetWorkspaceConfig(context.Background(), "ws-1")
	assert.False(t, ok, "miss must return false so caller falls back to CRD fetch")
}

func TestRedisStore_SetThenGetWorkspaceConfig_RoundTrips(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.SetWorkspaceConfig(context.Background(), "ws-1", Config{MaxActiveSessions: 7, AutoApprovePermissions: true})

	cfg, ok := store.GetWorkspaceConfig(context.Background(), "ws-1")
	require.True(t, ok)
	assert.Equal(t, 7, cfg.MaxActiveSessions)
	assert.True(t, cfg.AutoApprovePermissions)
	assert.True(t, mr.Exists(configKey("ws-1")), "config must be stored in Redis as JSON with TTL")
}

func TestRedisStore_SetWorkspaceConfig_TTLSet(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.SetWorkspaceConfig(context.Background(), "ws-1", Config{MaxActiveSessions: 3})

	ttl := mr.TTL(configKey("ws-1"))
	assert.Greater(t, ttl, time.Duration(0), "cached config must have a TTL")
	assert.LessOrEqual(t, ttl, DefaultConfigTTL+time.Second,
		"TTL must not exceed the configured ConfigTTL")
}

func TestRedisStore_SetWorkspaceConfig_OverwritesPreviousValue(t *testing.T) {
	store, _, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.SetWorkspaceConfig(context.Background(), "ws-1", Config{MaxActiveSessions: 3, AutoApprovePermissions: false})
	store.SetWorkspaceConfig(context.Background(), "ws-1", Config{MaxActiveSessions: 10, AutoApprovePermissions: true})

	cfg, ok := store.GetWorkspaceConfig(context.Background(), "ws-1")
	require.True(t, ok)
	assert.Equal(t, 10, cfg.MaxActiveSessions)
	assert.True(t, cfg.AutoApprovePermissions)
}

func TestRedisStore_GetWorkspaceConfig_DifferentWorkspaces_Isolated(t *testing.T) {
	store, _, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.SetWorkspaceConfig(context.Background(), "ws-1", Config{MaxActiveSessions: 5})
	store.SetWorkspaceConfig(context.Background(), "ws-2", Config{MaxActiveSessions: 20, AutoApprovePermissions: true})

	cfg1, ok1 := store.GetWorkspaceConfig(context.Background(), "ws-1")
	require.True(t, ok1)
	assert.Equal(t, 5, cfg1.MaxActiveSessions)
	assert.False(t, cfg1.AutoApprovePermissions)

	cfg2, ok2 := store.GetWorkspaceConfig(context.Background(), "ws-2")
	require.True(t, ok2)
	assert.Equal(t, 20, cfg2.MaxActiveSessions)
	assert.True(t, cfg2.AutoApprovePermissions)
}

// ---------------------------------------------------------------------------
// Serialization edge cases
// ---------------------------------------------------------------------------

func TestRedisStore_SetWorkspaceConfig_ZeroValueConfig_RoundTrips(t *testing.T) {
	store, _, _, cleanup := setupRedisStore(t)
	defer cleanup()

	// Zero-value Config: MaxActiveSessions=0, AutoApprovePermissions=false.
	// This is a valid cache entry — must NOT be confused with a miss.
	store.SetWorkspaceConfig(context.Background(), "ws-1", Config{})

	cfg, ok := store.GetWorkspaceConfig(context.Background(), "ws-1")
	require.True(t, ok, "zero-value Config must round-trip — it is a valid cache entry, not a miss")
	assert.Equal(t, 0, cfg.MaxActiveSessions)
	assert.False(t, cfg.AutoApprovePermissions)
}

// ---------------------------------------------------------------------------
// InvalidateWorkspaceConfig
// ---------------------------------------------------------------------------

func TestRedisStore_InvalidateWorkspaceConfig_RemovesEntry(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()
	store.SetWorkspaceConfig(context.Background(), "ws-1", Config{MaxActiveSessions: 5})
	store.SetWorkspaceConfig(context.Background(), "ws-2", Config{MaxActiveSessions: 3})

	store.InvalidateWorkspaceConfig(context.Background(), "ws-1")

	_, ok := store.GetWorkspaceConfig(context.Background(), "ws-1")
	assert.False(t, ok)
	assert.False(t, mr.Exists(configKey("ws-1")))
	cfg, ok := store.GetWorkspaceConfig(context.Background(), "ws-2")
	require.True(t, ok)
	assert.Equal(t, 3, cfg.MaxActiveSessions)
}

func TestRedisStore_InvalidateWorkspaceConfig_OnMissingEntry_IsNoOp(t *testing.T) {
	store, _, _, cleanup := setupRedisStore(t)
	defer cleanup()
	store.InvalidateWorkspaceConfig(context.Background(), "ws-never-was")
	_, ok := store.GetWorkspaceConfig(context.Background(), "ws-never-was")
	assert.False(t, ok)
}

// ---------------------------------------------------------------------------
// TTL expiry
// ---------------------------------------------------------------------------

func TestRedisStore_CachedConfig_TTLAutoExpires(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()
	store.SetWorkspaceConfig(context.Background(), "ws-1", Config{MaxActiveSessions: 5})
	require.True(t, func() bool {
		_, ok := store.GetWorkspaceConfig(context.Background(), "ws-1")
		return ok
	}())

	mr.FastForward(DefaultConfigTTL + time.Second)

	_, ok := store.GetWorkspaceConfig(context.Background(), "ws-1")
	assert.False(t, ok, "cached config must auto-expire after TTL")
	assert.False(t, mr.Exists(configKey("ws-1")))
}

// ---------------------------------------------------------------------------
// Fail-through on Redis errors
// ---------------------------------------------------------------------------

func TestRedisStore_GetWorkspaceConfig_RedisDown_ReturnsFalse(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewRedisStore(client, testActiveSessTTL)

	store.SetWorkspaceConfig(context.Background(), "ws-1", Config{MaxActiveSessions: 5})
	mr.Close()

	_, ok := store.GetWorkspaceConfig(context.Background(), "ws-1")
	assert.False(t, ok, "Redis-down must return false — caller falls back to CRD fetch")
	_ = client.Close()
}

func TestRedisStore_SetWorkspaceConfig_RedisDown_NoPanic(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewRedisStore(client, testActiveSessTTL)

	mr.Close()

	assert.NotPanics(t, func() {
		store.SetWorkspaceConfig(context.Background(), "ws-1", Config{MaxActiveSessions: 5})
	})
	_ = client.Close()
}

func TestRedisStore_InvalidateWorkspaceConfig_RedisDown_NoPanic(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewRedisStore(client, testActiveSessTTL)

	mr.Close()

	assert.NotPanics(t, func() {
		store.InvalidateWorkspaceConfig(context.Background(), "ws-1")
	})
	_ = client.Close()
}

// ---------------------------------------------------------------------------
// InvalidateAll integration
// ---------------------------------------------------------------------------

func TestRedisStore_InvalidateAll_ClearsRedisConfigCache(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.SetWorkspaceConfig(context.Background(), "ws-1", Config{MaxActiveSessions: 5})
	require.True(t, mr.Exists(configKey("ws-1")))

	store.InvalidateAll(context.Background(), "ws-1")

	_, ok := store.GetWorkspaceConfig(context.Background(), "ws-1")
	assert.False(t, ok)
	assert.False(t, mr.Exists(configKey("ws-1")))
}

// ---------------------------------------------------------------------------
// JSON serialization correctness (verify the stored format)
// ---------------------------------------------------------------------------

func TestRedisStore_SetWorkspaceConfig_StoresValidJSON(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()

	cfg := Config{MaxActiveSessions: 15, AutoApprovePermissions: true}
	store.SetWorkspaceConfig(context.Background(), "ws-1", cfg)

	raw, err := mr.Get(configKey("ws-1"))
	require.NoError(t, err)

	var decoded Config
	require.NoError(t, json.Unmarshal([]byte(raw), &decoded))
	assert.Equal(t, cfg, decoded, "stored value must be valid JSON that round-trips to the same Config")
}
