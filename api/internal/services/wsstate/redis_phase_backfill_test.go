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

// US-45.7: Redis-backed priorPhase.
// US-45.8: Redis-backed parentBackfilled.
//
// Both are simple string/boolean keys with long TTLs (24h). These are
// the last two sections to migrate before US-45.9 removes the
// InMemoryStore entirely.

// ---------------------------------------------------------------------------
// Prior phase tracking (US-45.7)
// ---------------------------------------------------------------------------

func priorPhaseKey(workspaceID string) string {
	return fmt.Sprintf("ws:{%s}:phase", workspaceID)
}

func TestRedisStore_GetPriorPhase_NoEntry_ReturnsFalse(t *testing.T) {
	store, _, _, cleanup := setupRedisStore(t)
	defer cleanup()
	_, ok := store.GetPriorPhase(context.Background(), "ws-1")
	assert.False(t, ok)
}

func TestRedisStore_SetThenGetPriorPhase_RoundTrips(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()
	store.SetPriorPhase(context.Background(), "ws-1", "Active")
	phase, ok := store.GetPriorPhase(context.Background(), "ws-1")
	require.True(t, ok)
	assert.Equal(t, "Active", phase)
	assert.True(t, mr.Exists(priorPhaseKey("ws-1")))
}

func TestRedisStore_SetPriorPhase_Overwrites(t *testing.T) {
	store, _, _, cleanup := setupRedisStore(t)
	defer cleanup()
	store.SetPriorPhase(context.Background(), "ws-1", "Creating")
	store.SetPriorPhase(context.Background(), "ws-1", "Active")
	phase, ok := store.GetPriorPhase(context.Background(), "ws-1")
	require.True(t, ok)
	assert.Equal(t, "Active", phase)
}

func TestRedisStore_DeletePriorPhase_RemovesEntry(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()
	store.SetPriorPhase(context.Background(), "ws-1", "Active")
	store.DeletePriorPhase(context.Background(), "ws-1")
	_, ok := store.GetPriorPhase(context.Background(), "ws-1")
	assert.False(t, ok)
	assert.False(t, mr.Exists(priorPhaseKey("ws-1")))
}

func TestRedisStore_PriorPhase_TTL24Hours(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()
	store.SetPriorPhase(context.Background(), "ws-1", "Active")
	ttl := mr.TTL(priorPhaseKey("ws-1"))
	assert.Greater(t, ttl, 23*time.Hour, "priorPhase TTL must be ~24 hours")
}

func TestRedisStore_GetPriorPhase_RedisDown_AssumesActiveToAvoidCacheWipe(t *testing.T) {
	// C4 (worklog 371): GetPriorPhase on Redis error returns ("Active", true)
	// — NOT ("", false). The pre-fix ("", false) caused onPhaseChange to
	// treat the error as first-invocation and call invalidateCaches, wiping
	// activeSess + deletedSessions + pwCache + wsConfig across all replicas
	// on a transient Redis blip. Assuming Active→Active (the steady-state
	// common case) limits the damage to InvalidateWorkspaceConfig.
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewRedisStore(client, testActiveSessTTL)
	store.SetPriorPhase(context.Background(), "ws-1", "Active")
	mr.Close()
	phase, ok := store.GetPriorPhase(context.Background(), "ws-1")
	assert.True(t, ok, "Redis-down must assume hadPrior=true (Active→Active) to avoid mass cache wipe")
	assert.Equal(t, "Active", phase, "Redis-down must assume prior phase is Active (the common case)")
	_ = client.Close()
}

func TestRedisStore_SetPriorPhase_RedisDown_NoPanic(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewRedisStore(client, testActiveSessTTL)
	mr.Close()
	assert.NotPanics(t, func() { store.SetPriorPhase(context.Background(), "ws-1", "Active") })
	_ = client.Close()
}

// ---------------------------------------------------------------------------
// Parent-backfill marker (US-45.8)
// ---------------------------------------------------------------------------

func backfilledKey(workspaceID string) string {
	return fmt.Sprintf("ws:{%s}:backfilled", workspaceID)
}

func TestRedisStore_GetParentBackfilled_DefaultFalse(t *testing.T) {
	store, _, _, cleanup := setupRedisStore(t)
	defer cleanup()
	assert.False(t, store.GetParentBackfilled(context.Background(), "ws-1"))
}

func TestRedisStore_SetParentBackfilled_ThenGet_ReturnsTrue(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()
	store.SetParentBackfilled(context.Background(), "ws-1")
	assert.True(t, store.GetParentBackfilled(context.Background(), "ws-1"))
	assert.True(t, mr.Exists(backfilledKey("ws-1")))
	assert.False(t, store.GetParentBackfilled(context.Background(), "ws-2"))
}

func TestRedisStore_DeleteParentBackfilled_RemovesMarker(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()
	store.SetParentBackfilled(context.Background(), "ws-1")
	store.DeleteParentBackfilled(context.Background(), "ws-1")
	assert.False(t, store.GetParentBackfilled(context.Background(), "ws-1"))
	assert.False(t, mr.Exists(backfilledKey("ws-1")))
}

func TestRedisStore_ParentBackfilled_TTL24Hours(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()
	store.SetParentBackfilled(context.Background(), "ws-1")
	ttl := mr.TTL(backfilledKey("ws-1"))
	assert.Greater(t, ttl, 23*time.Hour, "backfilled TTL must be ~24 hours")
}

func TestRedisStore_GetParentBackfilled_RedisDown_ReturnsFalse(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewRedisStore(client, testActiveSessTTL)
	store.SetParentBackfilled(context.Background(), "ws-1")
	mr.Close()
	assert.False(t, store.GetParentBackfilled(context.Background(), "ws-1"),
		"Redis-down returns false — allows backfill to retry")
	_ = client.Close()
}

func TestRedisStore_SetParentBackfilled_RedisDown_NoPanic(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewRedisStore(client, testActiveSessTTL)
	mr.Close()
	assert.NotPanics(t, func() { store.SetParentBackfilled(context.Background(), "ws-1") })
	_ = client.Close()
}

// ---------------------------------------------------------------------------
// InvalidateAll integration
// ---------------------------------------------------------------------------

func TestRedisStore_InvalidateAll_ClearsRedisPriorPhase(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()
	store.SetPriorPhase(context.Background(), "ws-1", "Active")
	store.InvalidateAll(context.Background(), "ws-1")
	// priorPhase SURVIVES InvalidateAll per US-45.1 contract.
	phase, ok := store.GetPriorPhase(context.Background(), "ws-1")
	assert.True(t, ok, "priorPhase must survive InvalidateAll")
	assert.Equal(t, "Active", phase)
	assert.True(t, mr.Exists(priorPhaseKey("ws-1")))
}

func TestRedisStore_InvalidateAll_ClearsRedisParentBackfilled(t *testing.T) {
	store, mr, _, cleanup := setupRedisStore(t)
	defer cleanup()
	store.SetParentBackfilled(context.Background(), "ws-1")
	require.True(t, mr.Exists(backfilledKey("ws-1")))
	store.InvalidateAll(context.Background(), "ws-1")
	assert.False(t, store.GetParentBackfilled(context.Background(), "ws-1"))
	assert.False(t, mr.Exists(backfilledKey("ws-1")))
}
