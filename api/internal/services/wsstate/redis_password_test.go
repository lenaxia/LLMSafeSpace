// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package wsstate

import (
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// US-45.4: Redis-backed pwCache.
//
// Workspace passwords are stable (only change on workspace recreate),
// so they cache well. Moving to Redis eliminates per-replica staleness
// on phase changes: previously, replica A could clear its cache on a
// 401 while replica B kept serving the old password and got 502s. With
// a shared cache, all replicas agree.
//
// Hash-tagged key ws:{workspace_id}:pw forces co-location with activeSess
// and deletedSessions keys for future cluster migration.
//
// Fail-through-to-K8s policy: on Redis error, GetCachedPassword returns
// (empty, false) so the caller (ProxyHandler.getPassword) falls back to
// fetching the K8s Secret directly. Redis is a performance optimization;
// on outage we pay the cost of fetching from K8s.
//
// Design differences from activeSess (US-45.2) and deletedSessions (US-45.3):
// - TTL = 1 hour (passwords are stable, longer than 30min for active/deleted)
// - String value, not SET member or standalone tombstone
// - InvalidatePassword = DEL on key (single source of truth)
// - No fail-closed: this is a CACHE, not authoritative state. The K8s
//   Secret fetch is the source of truth.

// passwordKey returns the canonical Redis key for a workspace's cached password.
func passwordKey(workspaceID string) string {
	return fmt.Sprintf("ws:{%s}:pw", workspaceID)
}

// ---------------------------------------------------------------------------
// GetCachedPassword / SetCachedPassword
// ---------------------------------------------------------------------------

func TestRedisStore_GetCachedPassword_Miss_ReturnsFalse(t *testing.T) {
	store, _, _, cleanup := setupRedisStore(t)
	defer cleanup()

	_, ok := store.GetCachedPassword("ws-1")
	assert.False(t, ok, "miss must return false so caller falls back to K8s Secret fetch")
}

func TestRedisStore_SetThenGetCachedPassword_RoundTrips(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.SetCachedPassword("ws-1", "hunter2")

	pw, ok := store.GetCachedPassword("ws-1")
	require.True(t, ok)
	assert.Equal(t, "hunter2", pw)
	assert.True(t, mr.Exists(passwordKey("ws-1")), "password must be stored in Redis with TTL")
}

func TestRedisStore_SetCachedPassword_TTLSet(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.SetCachedPassword("ws-1", "pw")

	ttl := mr.TTL(passwordKey("ws-1"))
	assert.Greater(t, ttl, time.Duration(0), "cached password must have a TTL — passwords are stable but not forever")
	// The TTL is at most DefaultPasswordTTL (1 hour); miniredis precision is ms.
	assert.LessOrEqual(t, ttl, DefaultPasswordTTL+time.Second,
		"TTL must not exceed the configured PasswordTTL — caching staleness is bounded")
}

func TestRedisStore_SetCachedPassword_OverwritesPreviousValue(t *testing.T) {
	store, _, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.SetCachedPassword("ws-1", "old")
	store.SetCachedPassword("ws-1", "new")

	pw, ok := store.GetCachedPassword("ws-1")
	require.True(t, ok)
	assert.Equal(t, "new", pw, "second SetCachedPassword must overwrite the first")
}

func TestRedisStore_GetCachedPassword_DifferentWorkspaces_Isolated(t *testing.T) {
	store, _, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.SetCachedPassword("ws-1", "alpha")
	store.SetCachedPassword("ws-2", "beta")

	pw1, ok1 := store.GetCachedPassword("ws-1")
	require.True(t, ok1)
	assert.Equal(t, "alpha", pw1)
	pw2, ok2 := store.GetCachedPassword("ws-2")
	require.True(t, ok2)
	assert.Equal(t, "beta", pw2)
}

// ---------------------------------------------------------------------------
// InvalidatePassword
// ---------------------------------------------------------------------------

func TestRedisStore_InvalidatePassword_RemovesEntry(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()
	store.SetCachedPassword("ws-1", "pw1")
	store.SetCachedPassword("ws-2", "pw2")

	store.InvalidatePassword("ws-1")

	_, ok := store.GetCachedPassword("ws-1")
	assert.False(t, ok)
	assert.False(t, mr.Exists(passwordKey("ws-1")),
		"InvalidatePassword must DEL the Redis key entirely — replicas hitting Redis on miss will fall through to K8s")
	pw, ok := store.GetCachedPassword("ws-2")
	require.True(t, ok, "InvalidatePassword must be scoped to one workspace")
	assert.Equal(t, "pw2", pw)
}

func TestRedisStore_InvalidatePassword_OnMissingEntry_IsNoOp(t *testing.T) {
	store, _, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.InvalidatePassword("ws-never-was")

	_, ok := store.GetCachedPassword("ws-never-was")
	assert.False(t, ok)
}

// ---------------------------------------------------------------------------
// TTL expiry (auto-recovery from cache staleness)
// ---------------------------------------------------------------------------

func TestRedisStore_CachedPassword_TTLAutoExpires(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()
	store.SetCachedPassword("ws-1", "oldpw")
	require.True(t, func() bool {
		_, ok := store.GetCachedPassword("ws-1")
		return ok
	}())

	// Fast-forward past the TTL — cached password must expire so the
	// next request re-fetches from K8s (password may have rotated).
	mr.FastForward(DefaultPasswordTTL + time.Second)

	_, ok := store.GetCachedPassword("ws-1")
	assert.False(t, ok,
		"cached password must auto-expire after TTL — bounded staleness, password rotation eventually surfaces")
	assert.False(t, mr.Exists(passwordKey("ws-1")))
}

// ---------------------------------------------------------------------------
// Fail-through-to-K8s on Redis errors
// ---------------------------------------------------------------------------

// TestRedisStore_GetCachedPassword_RedisDown_ReturnsFalse verifies the
// fail-through policy: on Redis error, GetCachedPassword returns
// (empty, false) so the caller falls back to fetching from K8s. This
// is NOT fail-closed (no false data) and NOT fail-open (no return true).
// It is "fail-through" — Redis is a cache, the source of truth is K8s.
func TestRedisStore_GetCachedPassword_RedisDown_ReturnsFalse(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewRedisStore(client, testActiveSessTTL)

	// Seed the cache while Redis is up.
	store.SetCachedPassword("ws-1", "pw")

	// Tear down Redis.
	mr.Close()

	pw, ok := store.GetCachedPassword("ws-1")
	assert.False(t, ok,
		"Redis-down must return false for GetCachedPassword — caller falls back to K8s Secret fetch")
	assert.Empty(t, pw, "no false data must be returned on Redis error")
	_ = client.Close()
}

// TestRedisStore_SetCachedPassword_RedisDown_NoPanic verifies graceful
// degradation: on Redis error, SetCachedPassword silently fails. The
// next read returns a miss and falls through to K8s.
func TestRedisStore_SetCachedPassword_RedisDown_NoPanic(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewRedisStore(client, testActiveSessTTL)

	mr.Close()

	assert.NotPanics(t, func() {
		store.SetCachedPassword("ws-1", "pw")
	}, "SetCachedPassword must not panic on Redis-down — silently fail and continue")
	_ = client.Close()
}

func TestRedisStore_InvalidatePassword_RedisDown_NoPanic(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewRedisStore(client, testActiveSessTTL)

	mr.Close()

	assert.NotPanics(t, func() {
		store.InvalidatePassword("ws-1")
	}, "InvalidatePassword must not panic on Redis-down")
	_ = client.Close()
}

// ---------------------------------------------------------------------------
// InvalidateAll integration (password cleared via Redis)
// ---------------------------------------------------------------------------

func TestRedisStore_InvalidateAll_ClearsRedisPasswordCache(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.SetCachedPassword("ws-1", "pw")
	require.True(t, mr.Exists(passwordKey("ws-1")))

	store.InvalidateAll("ws-1")

	_, ok := store.GetCachedPassword("ws-1")
	assert.False(t, ok, "InvalidateAll must clear the Redis-backed password cache")
	assert.False(t, mr.Exists(passwordKey("ws-1")))
}
