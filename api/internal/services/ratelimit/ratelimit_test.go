// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apilogger "github.com/lenaxia/llmsafespace/api/internal/logger"
)

func testLogger(t *testing.T) *apilogger.Logger {
	t.Helper()
	log, _ := apilogger.New(true, "debug", "console")
	return log
}

// newRedisService wires a real Service against a miniredis instance. The
// returned cleanup closes the redis client explicitly because the rate
// limiter backend borrows (and therefore never closes) the client.
func newRedisService(t *testing.T) (*Service, *miniredis.Miniredis, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := NewWithRedisClient(testLogger(t), client)
	return svc, mr, func() {
		_ = svc.Stop()
		_ = client.Close()
		mr.Close()
	}
}

func TestNewConstructors(t *testing.T) {
	log := testLogger(t)
	require.NotNil(t, NewWithMemory(log))
	require.NotNil(t, NewWithBackend(log, NewMemoryBackend()))
	require.NotNil(t, NewWithBackend(log, NewMemoryBackend(), WithCleanup(time.Second, time.Second)))
}

func TestStartStop(t *testing.T) {
	svc := NewWithMemory(testLogger(t))
	assert.NoError(t, svc.Start())
	assert.NoError(t, svc.Stop())
}

func TestAllow_UnderLimit(t *testing.T) {
	svc := NewWithMemory(testLogger(t))
	assert.True(t, svc.Allow("key1", 100, 20))
}

func TestAllow_OverLimit(t *testing.T) {
	svc := NewWithMemory(testLogger(t))
	assert.True(t, svc.Allow("k", 1, 2))
	assert.True(t, svc.Allow("k", 1, 2))
	assert.False(t, svc.Allow("k", 1, 2))
}

func TestAllow_WindowReset(t *testing.T) {
	svc := NewWithMemory(testLogger(t))
	assert.True(t, svc.Allow("k", 10, 1))
	assert.False(t, svc.Allow("k", 10, 1))
	time.Sleep(150 * time.Millisecond)
	assert.True(t, svc.Allow("k", 10, 1))
}

func TestAllow_Concurrent(t *testing.T) {
	svc := NewWithMemory(testLogger(t))
	const burst = 50
	const goroutines = 500
	var allowed int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if svc.Allow("ck", 0, burst) {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	close(start)
	wg.Wait()
	assert.Equal(t, int64(burst), allowed)
}

func TestInMemory_CleanupStaleBuckets(t *testing.T) {
	svc := NewWithMemory(testLogger(t), WithCleanup(50*time.Millisecond, 10*time.Millisecond))
	assert.True(t, svc.Allow("stale", 1, 1))
	require.Equal(t, 1, svc.LocalBucketCount())

	time.Sleep(80 * time.Millisecond)

	assert.True(t, svc.Allow("fresh", 1, 1))
	assert.Equal(t, 1, svc.LocalBucketCount())

	svc.mu.Lock()
	_, hasStale := svc.localBuckets["stale"]
	svc.mu.Unlock()
	assert.False(t, hasStale)
}

func TestIncrement_Redis(t *testing.T) {
	svc, _, cleanup := newRedisService(t)
	defer cleanup()
	ctx := context.Background()

	n, err := svc.Increment(ctx, "fw:test", 1, 60*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	n, err = svc.Increment(ctx, "fw:test", 1, 60*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
}

func TestIncrement_Concurrent_Redis(t *testing.T) {
	svc, _, cleanup := newRedisService(t)
	defer cleanup()
	ctx := context.Background()

	const N = 100
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.Increment(ctx, "fw:conc", 1, 60*time.Second)
		}()
	}
	wg.Wait()

	final, err := svc.Increment(ctx, "fw:conc", 1, 60*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(N+1), final)
}

func TestIncrement_Memory(t *testing.T) {
	svc := NewWithMemory(testLogger(t))
	ctx := context.Background()

	n, err := svc.Increment(ctx, "fw:mem", 1, 60*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	n, err = svc.Increment(ctx, "fw:mem", 1, 60*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
}

// TestIncrement_FixedWindowExpiryReset_Redis verifies that a fixed-window
// counter resets once its TTL elapses: after the window expires, the next
// increment starts a fresh window at 1 instead of accumulating.
func TestIncrement_FixedWindowExpiryReset_Redis(t *testing.T) {
	svc, mr, cleanup := newRedisService(t)
	defer cleanup()
	ctx := context.Background()

	n, err := svc.Increment(ctx, "fw:expire", 1, 100*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	n, err = svc.Increment(ctx, "fw:expire", 1, 100*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	// Advance miniredis past the PEXPIRE so the key is evicted.
	mr.FastForward(200 * time.Millisecond)

	n, err = svc.Increment(ctx, "fw:expire", 1, 100*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "counter should reset after window expiry")
}

// TestIncrement_FixedWindowExpiryReset_Memory is the in-memory equivalent:
// once the counter's expiry passes, the next increment starts over at 1.
func TestIncrement_FixedWindowExpiryReset_Memory(t *testing.T) {
	svc := NewWithMemory(testLogger(t))
	ctx := context.Background()

	n, err := svc.Increment(ctx, "fw:expiremem", 1, 50*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	time.Sleep(80 * time.Millisecond)

	n, err = svc.Increment(ctx, "fw:expiremem", 1, 50*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "counter should reset after expiry")
}

func TestGetTTL_Redis(t *testing.T) {
	svc, _, cleanup := newRedisService(t)
	defer cleanup()
	ctx := context.Background()

	ttl, err := svc.GetTTL(ctx, "missing")
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), ttl)

	_, err = svc.Increment(ctx, "ttl:test", 1, 60*time.Second)
	require.NoError(t, err)
	ttl, err = svc.GetTTL(ctx, "ttl:test")
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0))
	assert.LessOrEqual(t, ttl, 60*time.Second)
}

func TestGetTTL_Memory(t *testing.T) {
	svc := NewWithMemory(testLogger(t))
	ctx := context.Background()

	ttl, err := svc.GetTTL(ctx, "missing")
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), ttl)

	_, err = svc.Increment(ctx, "ttl:mem", 1, 60*time.Second)
	require.NoError(t, err)
	ttl, err = svc.GetTTL(ctx, "ttl:mem")
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0))
}

func TestSlidingWindow_ProperCount(t *testing.T) {
	svc, _, cleanup := newRedisService(t)
	defer cleanup()
	ctx := context.Background()
	windowKey := "sw:test"
	window := 10 * time.Second
	base := time.Now().UnixNano()

	for i := 0; i < 5; i++ {
		score := base + int64(i)
		require.NoError(t, svc.AddToWindow(ctx, windowKey, score, fmt.Sprintf("m%d", i), 60*time.Second))
	}
	oldScore := base - int64(2*window)
	require.NoError(t, svc.AddToWindow(ctx, windowKey, oldScore, "old", 60*time.Second))

	cutoff := base - window.Nanoseconds()

	count, err := svc.CountInWindow(ctx, windowKey, cutoff, base+5)
	require.NoError(t, err)
	assert.Equal(t, 5, count)

	wide, err := svc.CountInWindow(ctx, windowKey, oldScore-1, base+5)
	require.NoError(t, err)
	assert.Equal(t, 6, wide)

	require.NoError(t, svc.RemoveFromWindow(ctx, windowKey, cutoff))
	wide2, err := svc.CountInWindow(ctx, windowKey, oldScore-1, base+5)
	require.NoError(t, err)
	assert.Equal(t, 5, wide2)
}

func TestSlidingWindow_Memory(t *testing.T) {
	svc := NewWithMemory(testLogger(t))
	ctx := context.Background()
	windowKey := "sw:mem"
	window := 10 * time.Second
	base := time.Now().UnixNano()

	for i := 0; i < 3; i++ {
		score := base + int64(i)
		require.NoError(t, svc.AddToWindow(ctx, windowKey, score, fmt.Sprintf("m%d", i), 60*time.Second))
	}
	oldScore := base - int64(2*window)
	require.NoError(t, svc.AddToWindow(ctx, windowKey, oldScore, "old", 60*time.Second))

	cutoff := base - window.Nanoseconds()
	count, err := svc.CountInWindow(ctx, windowKey, cutoff, base+3)
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	require.NoError(t, svc.RemoveFromWindow(ctx, windowKey, cutoff))
	wide, err := svc.CountInWindow(ctx, windowKey, oldScore-1, base+3)
	require.NoError(t, err)
	assert.Equal(t, 3, wide)
}

// TestGetWindowEntries_SlidingWindow verifies that GetWindowEntries returns
// the members of a sliding window in score order, including after pruning old
// entries. Scores are kept well within double precision (Redis stores ZADD
// scores as float64) so cutoff boundaries are exact.
func TestGetWindowEntries_SlidingWindow(t *testing.T) {
	svc, _, cleanup := newRedisService(t)
	defer cleanup()
	ctx := context.Background()
	windowKey := "sw:entries"

	members := []string{"m0", "m1", "m2", "m3"}
	scores := []int64{1_000, 2_000, 3_000, 4_000}
	for i := range members {
		require.NoError(t, svc.AddToWindow(ctx, windowKey, scores[i], members[i], 60*time.Second))
	}

	// All members, score-ordered.
	entries, err := svc.GetWindowEntries(ctx, windowKey, 0, -1)
	require.NoError(t, err)
	assert.Equal(t, []string{"m0", "m1", "m2", "m3"}, entries)

	// Prune the oldest two members (score <= 2000) and re-read the tail.
	require.NoError(t, svc.RemoveFromWindow(ctx, windowKey, 2_000))
	entries, err = svc.GetWindowEntries(ctx, windowKey, 0, -1)
	require.NoError(t, err)
	assert.Equal(t, []string{"m2", "m3"}, entries)
}

// TestGetWindowEntries_Memory is the in-memory equivalent.
func TestGetWindowEntries_Memory(t *testing.T) {
	svc := NewWithMemory(testLogger(t))
	ctx := context.Background()
	windowKey := "sw:entries:mem"

	members := []string{"m0", "m1", "m2", "m3"}
	scores := []int64{1_000, 2_000, 3_000, 4_000}
	for i := range members {
		require.NoError(t, svc.AddToWindow(ctx, windowKey, scores[i], members[i], 60*time.Second))
	}

	entries, err := svc.GetWindowEntries(ctx, windowKey, 0, -1)
	require.NoError(t, err)
	assert.Equal(t, []string{"m0", "m1", "m2", "m3"}, entries)

	require.NoError(t, svc.RemoveFromWindow(ctx, windowKey, 2_000))
	entries, err = svc.GetWindowEntries(ctx, windowKey, 0, -1)
	require.NoError(t, err)
	assert.Equal(t, []string{"m2", "m3"}, entries)
}

// TestRedisBackend_ErrorPath verifies that the backend surfaces an error
// (rather than silently returning a zero/nil result) when Redis is
// unavailable.
func TestRedisBackend_ErrorPath(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	// Point the client at the miniredis, then immediately tear the server
	// down so every subsequent command fails. MaxRetries=0 + a short dial
	// timeout keeps the failure fast and deterministic.
	client := redis.NewClient(&redis.Options{
		Addr:        mr.Addr(),
		MaxRetries:  0,
		DialTimeout: 100 * time.Millisecond,
	})
	mr.Close()

	svc := NewWithRedisClient(testLogger(t), client)
	ctx := context.Background()

	_, err = svc.Increment(ctx, "down", 1, time.Second)
	assert.Error(t, err, "expected error when Redis is unavailable")

	err = svc.AddToWindow(ctx, "down", 1, "x", time.Second)
	assert.Error(t, err)

	_, err = svc.CountInWindow(ctx, "down", 0, 1)
	assert.Error(t, err)

	_, err = svc.GetTTL(ctx, "down")
	assert.Error(t, err)

	_ = client.Close()
}

// TestMemoryBackend_CleanupStaleEntries verifies that the memoryBackend evicts
// counters/windows that have not been accessed for longer than the configured
// TTL, bounding map growth (the same leak class as the token-bucket cleanup).
func TestMemoryBackend_CleanupStaleEntries(t *testing.T) {
	backend := NewMemoryBackendWithCleanup(50*time.Millisecond, 10*time.Millisecond).(*memoryBackend)
	svc := NewWithBackend(testLogger(t), backend)
	ctx := context.Background()

	_, err := svc.Increment(ctx, "stale-counter", 1, 5*time.Second)
	require.NoError(t, err)
	require.NoError(t, svc.AddToWindow(ctx, "stale-window", 1, "m", 5*time.Second))

	assert.Equal(t, 1, backend.counterCount(), "counter present before cleanup")
	assert.Equal(t, 1, backend.windowCount(), "window present before cleanup")

	// Wait long enough for the idle TTL to elapse, then touch a *different*
	// key — the access sweeps and purges the stale entries.
	time.Sleep(80 * time.Millisecond)

	_, err = svc.Increment(ctx, "fresh-counter", 1, 5*time.Second)
	require.NoError(t, err)

	assert.Equal(t, 1, backend.counterCount(), "stale counter evicted, only fresh remains")
	assert.Equal(t, 0, backend.windowCount(), "stale window evicted")
}
