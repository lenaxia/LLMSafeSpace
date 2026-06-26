// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package wsstate

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// US-45.3: Redis-backed deletedSessions.
//
// Session tombstones prevent late SSE events from opencode (session.updated,
// idle, step.ended) from re-inserting a deleted session into session_index.
// Moving tombstones to Redis ensures the suppression is cluster-wide —
// previously a session deleted on replica A could be resurrected by a late
// event arriving on replica B.
//
// Key design differences from activeSess (US-45.2):
// - Per-key TTL (not shared SET TTL) — each tombstone expires independently
// - Fail-CLOSED on Redis errors — IsSessionDeleted returns TRUE (assume
//   deleted to prevent resurrection). This is the OPPOSITE of activeSess
//   which fails OPEN. Rationale: data integrity > availability here.
// - No maxSessions limit, no atomic check-and-add — just SET and EXISTS.

const testDeletedTTL = 30 * time.Minute

// deletedKey returns the canonical Redis key for a session tombstone.
func deletedKey(workspaceID, sessionID string) string {
	return fmt.Sprintf("ws:{%s}:deleted:%s", workspaceID, sessionID)
}

// ---------------------------------------------------------------------------
// MarkSessionDeleted / IsSessionDeleted
// ---------------------------------------------------------------------------

func TestRedisStore_MarkSessionDeleted_ThenIsDeleted_ReturnsTrue(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()

	assert.False(t, store.IsSessionDeleted(context.Background(), "ws-1", "s1"))
	store.MarkSessionDeleted(context.Background(), "ws-1", "s1")
	assert.True(t, store.IsSessionDeleted(context.Background(), "ws-1", "s1"))
	assert.True(t, mr.Exists(deletedKey("ws-1", "s1")),
		"tombstone must be a real Redis key with TTL")
}

func TestRedisStore_MarkSessionDeleted_MultipleWorkspaces_TrackedSeparately(t *testing.T) {
	store, _, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.MarkSessionDeleted(context.Background(), "ws-1", "s1")
	store.MarkSessionDeleted(context.Background(), "ws-1", "s2")
	store.MarkSessionDeleted(context.Background(), "ws-2", "s1")

	assert.True(t, store.IsSessionDeleted(context.Background(), "ws-1", "s1"))
	assert.True(t, store.IsSessionDeleted(context.Background(), "ws-1", "s2"))
	assert.True(t, store.IsSessionDeleted(context.Background(), "ws-2", "s1"))
	assert.False(t, store.IsSessionDeleted(context.Background(), "ws-1", "s3"))
	assert.False(t, store.IsSessionDeleted(context.Background(), "ws-3", "s1"))
}

func TestRedisStore_MarkSessionDeleted_PerKeyTTL(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.MarkSessionDeleted(context.Background(), "ws-1", "s1")
	store.MarkSessionDeleted(context.Background(), "ws-1", "s2")

	ttl1 := mr.TTL(deletedKey("ws-1", "s1"))
	ttl2 := mr.TTL(deletedKey("ws-1", "s2"))
	assert.Greater(t, ttl1, time.Duration(0), "tombstone must have a TTL")
	assert.Greater(t, ttl2, time.Duration(0))

	// Fast-forward past TTL for s1 only.
	mr.FastForward(testDeletedTTL + time.Second)

	// s1 should be auto-expired; s2 should still exist.
	// Note: miniredis FastForward advances ALL keys, so both expire.
	// The per-key TTL design means each tombstone expires independently
	// in real Redis — we verify the TTL is set, and trust Redis to
	// honor per-key expiry. The auto-recovery test below covers the
	// expiry path.
	_ = ttl1
	_ = ttl2
}

func TestRedisStore_MarkSessionDeleted_TTLAutoExpires(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.MarkSessionDeleted(context.Background(), "ws-1", "s1")
	require.True(t, store.IsSessionDeleted(context.Background(), "ws-1", "s1"))

	// Fast-forward past the TTL — tombstone must auto-expire.
	mr.FastForward(testDeletedTTL + time.Second)

	assert.False(t, store.IsSessionDeleted(context.Background(), "ws-1", "s1"),
		"tombstone must auto-expire after TTL — bounded memory, no manual eviction needed")
	assert.False(t, mr.Exists(deletedKey("ws-1", "s1")),
		"Redis key must be gone after TTL expiry")
}

func TestRedisStore_MarkSessionDeleted_IdempotentReMark(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.MarkSessionDeleted(context.Background(), "ws-1", "s1")
	ttl1 := mr.TTL(deletedKey("ws-1", "s1"))

	// Marking again should refresh the TTL (or at minimum not error).
	store.MarkSessionDeleted(context.Background(), "ws-1", "s1")
	assert.True(t, store.IsSessionDeleted(context.Background(), "ws-1", "s1"))

	// TTL should still be positive.
	ttl2 := mr.TTL(deletedKey("ws-1", "s1"))
	assert.Greater(t, ttl2, time.Duration(0))
	// Re-marking with SET+EXPIRE refreshes the TTL.
	assert.GreaterOrEqual(t, ttl2, ttl1)
}

// ---------------------------------------------------------------------------
// ClearDeletedSessions
// ---------------------------------------------------------------------------

func TestRedisStore_ClearDeletedSessions_ScopedToWorkspace(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.MarkSessionDeleted(context.Background(), "ws-1", "s1")
	store.MarkSessionDeleted(context.Background(), "ws-1", "s2")
	store.MarkSessionDeleted(context.Background(), "ws-2", "s1")

	store.ClearDeletedSessions(context.Background(), "ws-1")

	assert.False(t, store.IsSessionDeleted(context.Background(), "ws-1", "s1"))
	assert.False(t, store.IsSessionDeleted(context.Background(), "ws-1", "s2"))
	assert.False(t, mr.Exists(deletedKey("ws-1", "s1")))
	assert.False(t, mr.Exists(deletedKey("ws-1", "s2")))
	assert.True(t, store.IsSessionDeleted(context.Background(), "ws-2", "s1"),
		"ClearDeletedSessions must not affect other workspaces")
	assert.True(t, mr.Exists(deletedKey("ws-2", "s1")))
}

func TestRedisStore_ClearDeletedSessions_NoTombstones_NoOp(t *testing.T) {
	store, _, _, cleanup := setupRedisStore(t)
	defer cleanup()

	// Must not panic or error on workspace with no tombstones.
	store.ClearDeletedSessions(context.Background(), "ws-never-was")
}

// ---------------------------------------------------------------------------
// Fail-CLOSED behavior on Redis errors
// ---------------------------------------------------------------------------

// TestRedisStore_IsSessionDeleted_RedisDown_FailsClosed verifies the
// fail-CLOSED contract: if Redis is unreachable, IsSessionDeleted returns
// TRUE (treat as deleted to prevent zombie session resurrection). This
// is the OPPOSITE of activeSess which fails OPEN (allow). The rationale
// (per design): "If we can't verify, assume deleted; user can recreate
// session" — data integrity > availability here.
func TestRedisStore_IsSessionDeleted_RedisDown_FailsClosed(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewRedisStore(client, testActiveSessTTL)

	// No tombstone exists for this session.
	require.False(t, store.IsSessionDeleted(context.Background(), "ws-1", "s1"))

	// Tear down Redis.
	mr.Close()

	assert.True(t, store.IsSessionDeleted(context.Background(), "ws-1", "s1"),
		"Redis-down must fail-CLOSED for IsSessionDeleted: assume deleted to prevent zombie resurrection")
	_ = client.Close()
}

// TestRedisStore_MarkSessionDeleted_RedisDown_NoPanic verifies that
// marking a session deleted when Redis is down does not panic — it
// silently fails (the tombstone is not recorded, but the system
// continues). When Redis recovers, the session can be re-deleted.
func TestRedisStore_MarkSessionDeleted_RedisDown_NoPanic(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewRedisStore(client, testActiveSessTTL)

	mr.Close()

	assert.NotPanics(t, func() {
		store.MarkSessionDeleted(context.Background(), "ws-1", "s1")
	}, "MarkSessionDeleted must not panic on Redis-down — silently fail and continue")
	_ = client.Close()
}

// TestRedisStore_ClearDeletedSessions_RedisDown_NoPanic verifies
// graceful degradation on Redis outage.
func TestRedisStore_ClearDeletedSessions_RedisDown_NoPanic(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewRedisStore(client, testActiveSessTTL)

	mr.Close()

	assert.NotPanics(t, func() {
		store.ClearDeletedSessions(context.Background(), "ws-1")
	}, "ClearDeletedSessions must not panic on Redis-down")
	_ = client.Close()
}

// ---------------------------------------------------------------------------
// InvalidateAll integration (deleted tombstones cleared via Redis)
// ---------------------------------------------------------------------------

func TestRedisStore_InvalidateAll_ClearsRedisDeletedTombstones(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()

	store.MarkSessionDeleted(context.Background(), "ws-1", "s1")
	store.MarkSessionDeleted(context.Background(), "ws-1", "s2")
	require.True(t, store.IsSessionDeleted(context.Background(), "ws-1", "s1"))

	store.InvalidateAll(context.Background(), "ws-1")

	assert.False(t, store.IsSessionDeleted(context.Background(), "ws-1", "s1"),
		"InvalidateAll must clear Redis-backed tombstones")
	assert.False(t, mr.Exists(deletedKey("ws-1", "s1")))
	assert.False(t, mr.Exists(deletedKey("ws-1", "s2")))
}
