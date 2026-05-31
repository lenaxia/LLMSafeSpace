// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func testLogger() *zap.Logger {
	l, _ := zap.NewDevelopment()
	return l
}

// --- healthzCache unit tests ---

func TestHealthzCache_InitialState(t *testing.T) {
	cache := newHealthzCache()
	snap := cache.Snapshot()

	assert.False(t, snap.Initialized, "new cache must not be initialized")
	assert.False(t, snap.Healthy, "new cache must not be healthy")
	assert.Equal(t, 0, snap.ConsecutiveFailures)
	assert.Empty(t, snap.Version)
	assert.Empty(t, snap.LastError)
}

func TestRefreshOnce_SuccessfulRefresh(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"healthy": true, "version": "v1.2.3"})
	}))
	defer mock.Close()

	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(mock.URL)

	client := &OpenCodeClient{password: "test", client: &http.Client{Timeout: 2 * time.Second}}
	cache := newHealthzCache()

	refreshOnce(context.Background(), client, cache, testLogger())

	snap := cache.Snapshot()
	assert.True(t, snap.Initialized)
	assert.True(t, snap.Healthy)
	assert.Equal(t, "v1.2.3", snap.Version)
	assert.Equal(t, 0, snap.ConsecutiveFailures)
	assert.Empty(t, snap.LastError)
	assert.WithinDuration(t, time.Now(), snap.LastRefreshedAt, 2*time.Second)
}

func TestRefreshOnce_FailedRefresh_IncrementCounter(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mock.Close()

	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(mock.URL)

	client := &OpenCodeClient{password: "test", client: &http.Client{Timeout: 2 * time.Second}}
	cache := newHealthzCache()

	refreshOnce(context.Background(), client, cache, testLogger())

	snap := cache.Snapshot()
	assert.True(t, snap.Initialized)
	assert.False(t, snap.Healthy, "first failure on uninitialized cache keeps healthy=false")
	assert.Equal(t, 1, snap.ConsecutiveFailures)
	assert.NotEmpty(t, snap.LastError)
}

func TestRefreshOnce_FailureThreshold_PreservesHealthyUntilThreshold(t *testing.T) {
	callCount := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"healthy": true, "version": "v1.0"})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer mock.Close()

	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(mock.URL)

	client := &OpenCodeClient{password: "test", client: &http.Client{Timeout: 2 * time.Second}}
	cache := newHealthzCache()

	// First call succeeds — healthy=true
	refreshOnce(context.Background(), client, cache, testLogger())
	assert.True(t, cache.Snapshot().Healthy)

	// Failures 1 and 2 — healthy stays true (threshold=3)
	refreshOnce(context.Background(), client, cache, testLogger())
	assert.True(t, cache.Snapshot().Healthy, "1 failure: healthy preserved")
	assert.Equal(t, 1, cache.Snapshot().ConsecutiveFailures)

	refreshOnce(context.Background(), client, cache, testLogger())
	assert.True(t, cache.Snapshot().Healthy, "2 failures: healthy preserved")
	assert.Equal(t, 2, cache.Snapshot().ConsecutiveFailures)

	// Failure 3 — threshold reached, healthy flips to false
	refreshOnce(context.Background(), client, cache, testLogger())
	assert.False(t, cache.Snapshot().Healthy, "3 failures: healthy must flip to false")
	assert.Equal(t, 3, cache.Snapshot().ConsecutiveFailures)
}

func TestRefreshOnce_Recovery_AfterThresholdFlip(t *testing.T) {
	callCount := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 3 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{"healthy": true, "version": "v2.0"})
		}
	}))
	defer mock.Close()

	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(mock.URL)

	client := &OpenCodeClient{password: "test", client: &http.Client{Timeout: 2 * time.Second}}
	cache := newHealthzCache()

	// 3 failures → unhealthy
	for i := 0; i < 3; i++ {
		refreshOnce(context.Background(), client, cache, testLogger())
	}
	assert.False(t, cache.Snapshot().Healthy)

	// Single success → recovery
	refreshOnce(context.Background(), client, cache, testLogger())
	snap := cache.Snapshot()
	assert.True(t, snap.Healthy, "single success must recover from unhealthy")
	assert.Equal(t, 0, snap.ConsecutiveFailures)
	assert.Equal(t, "v2.0", snap.Version)
	assert.Empty(t, snap.LastError)
}

func TestRefreshOnce_OpencodeReportsUnhealthy(t *testing.T) {
	// opencode itself says "not healthy" — different from a network error
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"healthy": false, "version": "v1.0"})
	}))
	defer mock.Close()

	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(mock.URL)

	client := &OpenCodeClient{password: "test", client: &http.Client{Timeout: 2 * time.Second}}
	cache := newHealthzCache()

	refreshOnce(context.Background(), client, cache, testLogger())

	snap := cache.Snapshot()
	assert.True(t, snap.Initialized)
	assert.False(t, snap.Healthy, "opencode reports unhealthy → cache reflects it immediately")
	assert.Equal(t, 0, snap.ConsecutiveFailures, "no network error → counter stays 0")
	assert.Empty(t, snap.LastError)
}

func TestRefreshOnce_Timeout_TreatedAsFailure(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second) // longer than readinessRefreshTimeout
	}))
	defer mock.Close()

	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(mock.URL)

	client := &OpenCodeClient{password: "test", client: &http.Client{Timeout: readinessRefreshTimeout}}
	cache := newHealthzCache()

	ctx, cancel := context.WithTimeout(context.Background(), readinessRefreshTimeout+time.Second)
	defer cancel()

	refreshOnce(ctx, client, cache, testLogger())

	snap := cache.Snapshot()
	assert.True(t, snap.Initialized)
	assert.Equal(t, 1, snap.ConsecutiveFailures, "timeout must count as failure")
	assert.NotEmpty(t, snap.LastError)
}

func TestRefreshOnce_PanicRecovery(t *testing.T) {
	// Use a mock that will cause a panic by closing the server before the request
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("simulated opencode panic")
	}))
	defer mock.Close()

	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(mock.URL)

	client := &OpenCodeClient{password: "test", client: &http.Client{Timeout: 2 * time.Second}}
	cache := newHealthzCache()

	// Should not panic — recovered internally
	assert.NotPanics(t, func() {
		refreshOnce(context.Background(), client, cache, testLogger())
	})

	snap := cache.Snapshot()
	assert.Equal(t, 1, snap.ConsecutiveFailures)
}

func TestHealthzCache_ConcurrentReads_RaceFree(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"healthy": true, "version": "v1.0"})
	}))
	defer mock.Close()

	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(mock.URL)

	client := &OpenCodeClient{password: "test", client: &http.Client{Timeout: 2 * time.Second}}
	cache := newHealthzCache()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Writer goroutine
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				refreshOnce(ctx, client, cache, testLogger())
				time.Sleep(time.Millisecond)
			}
		}
	}()

	// Concurrent readers
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				snap := cache.Snapshot()
				_ = snap.Healthy // force read
			}
		}()
	}
	wg.Wait()
}

// --- refreshIsHealthyLoop tests ---

func TestRefreshIsHealthyLoop_ExitsOnContextCancel(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"healthy": true, "version": "v1.0"})
	}))
	defer mock.Close()

	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(mock.URL)

	client := &OpenCodeClient{password: "test", client: &http.Client{Timeout: 2 * time.Second}}
	cache := newHealthzCache()

	ctx, cancel := context.WithCancel(context.Background())

	var done atomic.Bool
	go func() {
		refreshIsHealthyLoop(ctx, client, cache, testLogger())
		done.Store(true)
	}()

	// Wait for at least one refresh
	time.Sleep(100 * time.Millisecond)
	assert.True(t, cache.Snapshot().Initialized, "immediate refresh should have fired")

	cancel()
	time.Sleep(100 * time.Millisecond)
	assert.True(t, done.Load(), "goroutine must exit within 100ms of context cancellation")
}

func TestRefreshIsHealthyLoop_ImmediateFirstRefresh(t *testing.T) {
	var callCount atomic.Int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"healthy": true, "version": "v1.0"})
	}))
	defer mock.Close()

	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(mock.URL)

	client := &OpenCodeClient{password: "test", client: &http.Client{Timeout: 2 * time.Second}}
	cache := newHealthzCache()

	ctx, cancel := context.WithCancel(context.Background())
	go refreshIsHealthyLoop(ctx, client, cache, testLogger())

	// The immediate refresh should fire within 100ms (not waiting for the 5s tick)
	time.Sleep(200 * time.Millisecond)
	cancel()

	assert.True(t, cache.Snapshot().Initialized)
	assert.GreaterOrEqual(t, callCount.Load(), int32(1), "at least one refresh must fire immediately on boot")
}

func TestRefreshIsHealthyLoop_RefreshesOnTick(t *testing.T) {
	// This test verifies the loop refreshes periodically. We can't easily
	// control the ticker without a fake clock, so we just verify multiple
	// refreshes happen within a reasonable window.
	var callCount atomic.Int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"healthy": true, "version": "v1.0"})
	}))
	defer mock.Close()

	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(mock.URL)

	client := &OpenCodeClient{password: "test", client: &http.Client{Timeout: 2 * time.Second}}
	cache := newHealthzCache()

	ctx, cancel := context.WithCancel(context.Background())
	go refreshIsHealthyLoop(ctx, client, cache, testLogger())

	// Wait for 2 ticks (5s each) + immediate = at least 3 calls
	time.Sleep(11 * time.Second)
	cancel()

	assert.GreaterOrEqual(t, callCount.Load(), int32(3),
		"expected at least 3 refreshes (1 immediate + 2 ticks) in 11s")
}

func TestRefreshOnce_VersionPreservedOnFailure(t *testing.T) {
	callCount := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"healthy": true, "version": "v3.0"})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer mock.Close()

	origAddr := agentAddr
	defer func() { setAgentAddr(origAddr) }()
	setAgentAddr(mock.URL)

	client := &OpenCodeClient{password: "test", client: &http.Client{Timeout: 2 * time.Second}}
	cache := newHealthzCache()

	// Success sets version
	refreshOnce(context.Background(), client, cache, testLogger())
	assert.Equal(t, "v3.0", cache.Snapshot().Version)

	// Failure preserves version
	refreshOnce(context.Background(), client, cache, testLogger())
	assert.Equal(t, "v3.0", cache.Snapshot().Version, "version must be preserved on failure")
}

// --- Benchmark ---

func BenchmarkHealthzCache_Snapshot(b *testing.B) {
	cache := newHealthzCache()
	cache.snapshot.Store(&healthzCacheSnapshot{
		Healthy:         true,
		Version:         "v1.0",
		Initialized:     true,
		LastRefreshedAt: time.Now(),
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cache.Snapshot()
	}
}
