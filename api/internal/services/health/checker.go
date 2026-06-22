// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package health runs the API server's periodic dependency health probe.
//
// One goroutine pings each registered dependency (Postgres, Redis) on a
// fixed interval and reports the outcome to two metric families:
//
//   - llmsafespaces_dependency_up{dependency} — 1 = healthy, 0 = unhealthy.
//   - llmsafespaces_db_pool_active_connections / _idle / _max — refreshed
//     so the connection-pool dashboard panel does not freeze on the
//     boot-time snapshot.
//
// The probe is deliberately stateless from the caller's perspective: the
// only side effect is metric updates. /readyz remains the synchronous
// authority for ingress-controller readiness — this loop exists only so
// dashboards have a continuous signal.
package health

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/lenaxia/llmsafespaces/api/internal/logger"
	"github.com/lenaxia/llmsafespaces/api/internal/services/metrics"
)

// Pingable is satisfied by anything with a context-aware Ping. The
// database and cache services already implement this, which keeps the
// checker free of concrete-type imports and easy to fake in tests.
type Pingable interface {
	Ping(ctx context.Context) error
}

// PoolStatsSource is satisfied by anything that can report database/sql
// pool counters. The database service exposes a DB *sql.DB field that
// satisfies this; we depend on an interface rather than the concrete type
// to keep the package import-graph clean and the test setup trivial.
type PoolStatsSource interface {
	Stats() sql.DBStats
}

// Checker periodically pings each registered dependency and refreshes the
// db-pool gauges. It is constructed once during App.Run() and stopped via
// Stop() during shutdown. Stop() is idempotent and safe to call from any
// goroutine.
type Checker struct {
	logger     *logger.Logger
	deps       map[string]Pingable
	poolSource PoolStatsSource
	interval   time.Duration
	pingTO     time.Duration

	wg     sync.WaitGroup
	once   sync.Once
	stopCh chan struct{}
}

// Config configures the dependency health checker.
type Config struct {
	// Dependencies maps a dependency label (e.g. "postgres", "redis")
	// to a Pingable. The label appears verbatim as the
	// llmsafespaces_dependency_up{dependency} value.
	Dependencies map[string]Pingable

	// PoolSource, when non-nil, is queried every Interval to refresh
	// the db-pool connection gauges. nil disables the refresh.
	PoolSource PoolStatsSource

	// Interval between probe rounds. Defaults to 15s if zero.
	Interval time.Duration

	// PingTimeout caps each individual Ping. Defaults to 2s if zero.
	PingTimeout time.Duration
}

// NewChecker constructs a dependency health checker. It does not start
// any goroutine; call Start to begin probing.
func NewChecker(log *logger.Logger, cfg Config) *Checker {
	interval := cfg.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	pingTO := cfg.PingTimeout
	if pingTO <= 0 {
		pingTO = 2 * time.Second
	}
	deps := make(map[string]Pingable, len(cfg.Dependencies))
	for k, v := range cfg.Dependencies {
		if v == nil {
			continue
		}
		deps[k] = v
	}
	return &Checker{
		logger:     log,
		deps:       deps,
		poolSource: cfg.PoolSource,
		interval:   interval,
		pingTO:     pingTO,
		stopCh:     make(chan struct{}),
	}
}

// Start runs the probe loop on a new goroutine. The loop exits when
// either ctx is canceled or Stop() is called. The first probe runs
// immediately so the metric is populated as soon as the API is up.
func (c *Checker) Start(ctx context.Context) {
	c.wg.Add(1)
	go c.run(ctx)
}

// Stop signals the probe loop to exit and waits for it to finish.
// Idempotent.
func (c *Checker) Stop() {
	c.once.Do(func() { close(c.stopCh) })
	c.wg.Wait()
}

func (c *Checker) run(ctx context.Context) {
	defer c.wg.Done()
	c.probeOnce(ctx) // immediate first probe
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-t.C:
			c.probeOnce(ctx)
		}
	}
}

func (c *Checker) probeOnce(parent context.Context) {
	for name, dep := range c.deps {
		ctx, cancel := context.WithTimeout(parent, c.pingTO)
		err := dep.Ping(ctx)
		cancel()
		metrics.RecordDependencyUp(name, err == nil)
		if err != nil && c.logger != nil {
			c.logger.Warn("dependency probe failed",
				"dependency", name,
				"error", err.Error(),
			)
		}
	}
	if c.poolSource != nil {
		s := c.poolSource.Stats()
		metrics.RecordDBPoolStats(s.InUse, s.Idle, s.MaxOpenConnections)
	}
}
