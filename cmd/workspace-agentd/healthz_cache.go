// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

const (
	readinessRefreshInterval  = 5 * time.Second
	readinessRefreshTimeout   = 4 * time.Second
	readinessFailureThreshold = 3
)

// healthzCacheSnapshot is an immutable point-in-time view of the readiness
// cache. Reads are lock-free via atomic.Pointer; writes are by the single
// refresher goroutine only.
type healthzCacheSnapshot struct {
	Healthy             bool
	Version             string
	LastRefreshedAt     time.Time
	ConsecutiveFailures int
	LastError           string
	Initialized         bool
}

// healthzCache holds the latest readiness observation from opencode's
// /global/health endpoint. A single background goroutine writes it;
// any number of readers can call Snapshot() concurrently without locks.
type healthzCache struct {
	snapshot atomic.Pointer[healthzCacheSnapshot]
}

func newHealthzCache() *healthzCache {
	c := &healthzCache{}
	c.snapshot.Store(&healthzCacheSnapshot{Healthy: false, Initialized: false})
	return c
}

// Snapshot returns the current cache state. Lock-free atomic load.
func (c *healthzCache) Snapshot() healthzCacheSnapshot {
	return *c.snapshot.Load()
}

// refreshIsHealthyLoop runs from agentd boot until ctx is cancelled.
// It refreshes the cache every readinessRefreshInterval by calling
// client.IsHealthy. An immediate refresh fires on boot so /v1/readyz
// has a meaningful answer within seconds of startup.
func refreshIsHealthyLoop(ctx context.Context, client *OpenCodeClient, cache *healthzCache, logger *zap.Logger) {
	tick := time.NewTicker(readinessRefreshInterval)
	defer tick.Stop()

	// Immediate first refresh on boot.
	refreshOnce(ctx, client, cache, logger)

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			refreshOnce(ctx, client, cache, logger)
		}
	}
}

// refreshOnce performs a single IsHealthy call with a timeout and updates
// the cache atomically. Panics in the opencode client are recovered to
// prevent the refresher goroutine from dying.
func refreshOnce(ctx context.Context, client *OpenCodeClient, cache *healthzCache, logger *zap.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("panic in readiness refresh", zap.Any("recover", r))
			prev := cache.Snapshot()
			next := healthzCacheSnapshot{
				Initialized:         prev.Initialized,
				LastRefreshedAt:     time.Now(),
				Version:             prev.Version,
				Healthy:             prev.Healthy,
				ConsecutiveFailures: prev.ConsecutiveFailures + 1,
				LastError:           "panic in refresh",
			}
			if next.ConsecutiveFailures >= readinessFailureThreshold {
				next.Healthy = false
			}
			cache.snapshot.Store(&next)
		}
	}()

	refreshCtx, cancel := context.WithTimeout(ctx, readinessRefreshTimeout)
	defer cancel()

	prev := cache.Snapshot()
	healthy, version, err := client.IsHealthy(refreshCtx)

	next := healthzCacheSnapshot{
		Initialized:         true,
		LastRefreshedAt:     time.Now(),
		Version:             prev.Version,
		Healthy:             prev.Healthy,
		ConsecutiveFailures: prev.ConsecutiveFailures,
		LastError:           prev.LastError,
	}

	if err != nil {
		next.ConsecutiveFailures = prev.ConsecutiveFailures + 1
		next.LastError = err.Error()
		if next.ConsecutiveFailures >= readinessFailureThreshold {
			next.Healthy = false
		}
		logger.Warn("readyz refresh failed",
			zap.Int("consecutiveFailures", next.ConsecutiveFailures),
			zap.Error(err))
	} else {
		next.Healthy = healthy
		next.Version = version
		next.ConsecutiveFailures = 0
		next.LastError = ""
	}

	cache.snapshot.Store(&next)
}
