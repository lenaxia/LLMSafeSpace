// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package health

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/lenaxia/llmsafespaces/api/internal/logger"
	"github.com/lenaxia/llmsafespaces/api/internal/services/metrics"
)

type fakePingable struct {
	err   error
	calls int64
}

func (f *fakePingable) Ping(ctx context.Context) error {
	atomic.AddInt64(&f.calls, 1)
	return f.err
}

type fakePoolSource struct {
	stats sql.DBStats
	calls int64
}

func (f *fakePoolSource) Stats() sql.DBStats {
	atomic.AddInt64(&f.calls, 1)
	return f.stats
}

func gatherFamilyHealth(t *testing.T, name string) *dto.MetricFamily {
	t.Helper()
	mfs, err := metrics.GatherDefault()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf
		}
	}
	return nil
}

func gaugeByLabelHealth(mf *dto.MetricFamily, key, val string) (float64, bool) {
	if mf == nil {
		return 0, false
	}
	for _, m := range mf.GetMetric() {
		for _, l := range m.GetLabel() {
			if l.GetName() == key && l.GetValue() == val {
				return m.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

func newTestLogger(t *testing.T) *logger.Logger {
	t.Helper()
	l, err := logger.New(true, "debug", "console")
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}
	return l
}

func TestChecker_RecordsDependencyUpForHealthyAndUnhealthy(t *testing.T) {
	log := newTestLogger(t)

	healthy := &fakePingable{}
	unhealthy := &fakePingable{err: errors.New("boom")}

	c := NewChecker(log, Config{
		Dependencies: map[string]Pingable{
			"postgres": healthy,
			"redis":    unhealthy,
		},
		Interval:    50 * time.Millisecond,
		PingTimeout: 200 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	defer c.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&healthy.calls) >= 1 && atomic.LoadInt64(&unhealthy.calls) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mf := gatherFamilyHealth(t, "llmsafespaces_dependency_up")
	if mf == nil {
		t.Fatal("llmsafespaces_dependency_up not emitted")
	}
	if v, ok := gaugeByLabelHealth(mf, "dependency", "postgres"); !ok || v != 1 {
		t.Fatalf("postgres dependency_up: ok=%v value=%v want 1", ok, v)
	}
	if v, ok := gaugeByLabelHealth(mf, "dependency", "redis"); !ok || v != 0 {
		t.Fatalf("redis dependency_up: ok=%v value=%v want 0", ok, v)
	}
}

func TestChecker_StopIsIdempotent(t *testing.T) {
	log := newTestLogger(t)
	c := NewChecker(log, Config{
		Dependencies: map[string]Pingable{"postgres": &fakePingable{}},
		Interval:     50 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)

	c.Stop()
	c.Stop() // must not panic or deadlock
}

func TestChecker_PoolSourceRefreshesPoolGauges(t *testing.T) {
	log := newTestLogger(t)
	pool := &fakePoolSource{stats: sql.DBStats{InUse: 7, Idle: 3, MaxOpenConnections: 25}}

	c := NewChecker(log, Config{
		Dependencies: map[string]Pingable{},
		PoolSource:   pool,
		Interval:     50 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	defer c.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&pool.calls) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt64(&pool.calls) < 1 {
		t.Fatal("pool source never queried")
	}

	mf := gatherFamilyHealth(t, "llmsafespaces_db_pool_active_connections")
	if mf == nil {
		t.Fatal("active connections gauge missing")
	}
	if got := mf.GetMetric()[0].GetGauge().GetValue(); got != 7 {
		t.Fatalf("active connections: got %v want 7", got)
	}
}

func TestChecker_NilDependenciesAreSkipped(t *testing.T) {
	log := newTestLogger(t)
	c := NewChecker(log, Config{
		Dependencies: map[string]Pingable{"postgres": nil},
		Interval:     50 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	c.Stop()
	// success = no panic
}
