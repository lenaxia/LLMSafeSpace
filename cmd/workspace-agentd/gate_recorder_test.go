// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
)

// newTestGateHist creates a fresh, isolated HistogramVec for gate tests.
// Using fresh instances per test prevents cross-test observation bleed.
func newTestGateHist() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "test_agentd_gate_duration_seconds",
		Buckets: []float64{0.001, 0.01, 0.1, 1, 5, 10, 30, 60, 120},
	}, []string{"gate"})
}

func gateCount(t *testing.T, hist *prometheus.HistogramVec, gate string) uint64 {
	t.Helper()
	m := &dto.Metric{}
	require.NoError(t, hist.WithLabelValues(gate).(prometheus.Histogram).Write(m))
	return m.GetHistogram().GetSampleCount()
}

func newRecorder(t *testing.T) (*gateRecorder, *prometheus.HistogramVec) {
	t.Helper()
	hist := newTestGateHist()
	gr := newGateRecorder(time.Now(), hist, zaptest.NewLogger(t))
	return gr, hist
}

// ---- core idempotency tests ----

func TestGateRecorderFirstCallReturnsTrue(t *testing.T) {
	gr, _ := newRecorder(t)
	assert.True(t, gr.MaybeRecord(gateOpencodeUp))
}

func TestGateRecorderSubsequentCallsReturnFalse(t *testing.T) {
	gr, _ := newRecorder(t)
	require.True(t, gr.MaybeRecord(gateOpencodeUp))
	for range 5 {
		assert.False(t, gr.MaybeRecord(gateOpencodeUp))
	}
}

func TestGateRecorderObservesExactlyOnce(t *testing.T) {
	gr, hist := newRecorder(t)
	for range 10 {
		gr.MaybeRecord(gateOpencodeUp)
	}
	assert.EqualValues(t, 1, gateCount(t, hist, gateOpencodeUp))
}

// ---- gate independence ----

func TestGateRecorderIndependentGates(t *testing.T) {
	gr, hist := newRecorder(t)
	gr.MaybeRecord(gateOpencodeUp)
	gr.MaybeRecord(gateOpencodeUp)
	gr.MaybeRecord(gateProvidersConnected)

	assert.EqualValues(t, 1, gateCount(t, hist, gateOpencodeUp))
	assert.EqualValues(t, 1, gateCount(t, hist, gateProvidersConnected))
	assert.EqualValues(t, 0, gateCount(t, hist, gateReadyzFirst200))
}

func TestGateRecorderAllThreeGates(t *testing.T) {
	gr, hist := newRecorder(t)
	gr.MaybeRecord(gateOpencodeUp)
	gr.MaybeRecord(gateProvidersConnected)
	gr.MaybeRecord(gateReadyzFirst200)

	assert.EqualValues(t, 1, gateCount(t, hist, gateOpencodeUp))
	assert.EqualValues(t, 1, gateCount(t, hist, gateProvidersConnected))
	assert.EqualValues(t, 1, gateCount(t, hist, gateReadyzFirst200))
}

// ---- elapsed correctness ----

func TestGateRecorderElapsedIsPositive(t *testing.T) {
	hist := newTestGateHist()
	boot := time.Now().Add(-100 * time.Millisecond)
	gr := newGateRecorder(boot, hist, zaptest.NewLogger(t))
	gr.MaybeRecord(gateOpencodeUp)

	m := &dto.Metric{}
	require.NoError(t, hist.WithLabelValues(gateOpencodeUp).(prometheus.Histogram).Write(m))
	assert.Greater(t, m.GetHistogram().GetSampleSum(), 0.0)
}

// ---- healthy-flip adversarial tests ----

// TestGateRecorderHealthyFlipDoesNotRefire verifies: healthy → unhealthy → healthy
// only fires once.
func TestGateRecorderHealthyFlipDoesNotRefire(t *testing.T) {
	gr, hist := newRecorder(t)
	gr.MaybeRecord(gateOpencodeUp) // first healthy
	// (unhealthy period — caller does not call MaybeRecord)
	gr.MaybeRecord(gateOpencodeUp) // second healthy — must be no-op
	assert.EqualValues(t, 1, gateCount(t, hist, gateOpencodeUp))
}

func TestGateRecorderProvidersConnectedFlipDoesNotRefire(t *testing.T) {
	gr, hist := newRecorder(t)
	gr.MaybeRecord(gateProvidersConnected)
	gr.MaybeRecord(gateProvidersConnected)
	assert.EqualValues(t, 1, gateCount(t, hist, gateProvidersConnected))
}

func TestGateRecorderReadyzFirstFlipDoesNotRefire(t *testing.T) {
	gr, hist := newRecorder(t)
	gr.MaybeRecord(gateReadyzFirst200)
	gr.MaybeRecord(gateReadyzFirst200)
	gr.MaybeRecord(gateReadyzFirst200)
	assert.EqualValues(t, 1, gateCount(t, hist, gateReadyzFirst200))
}

// ---- edge cases ----

func TestGateRecorderNoPanicWhenNeverFired(t *testing.T) {
	gr, hist := newRecorder(t)
	assert.NotPanics(t, func() { gr.MaybeRecord(gateOpencodeUp) })
	assert.EqualValues(t, 0, gateCount(t, hist, gateProvidersConnected))
}

// TestGateRecorderConcurrentSafety verifies exactly one observation under
// concurrent writers.
func TestGateRecorderConcurrentSafety(t *testing.T) {
	gr, hist := newRecorder(t)
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			gr.MaybeRecord(gateOpencodeUp)
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, gateCount(t, hist, gateOpencodeUp))
}

// ---- structured log field tests ----

// TestGateRecorderLogsStructuredFields verifies the log entry contains
// typed "gate" (string) and "elapsed_seconds" (float64) fields.
func TestGateRecorderLogsStructuredFields(t *testing.T) {
	hist := newTestGateHist()
	core, logs := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	boot := time.Now().Add(-50 * time.Millisecond)
	gr := newGateRecorder(boot, hist, logger)

	gr.MaybeRecord(gateOpencodeUp)

	require.Len(t, logs.All(), 1)
	entry := logs.All()[0]
	assert.Equal(t, "startup gate reached", entry.Message)

	fieldMap := entry.ContextMap()
	assert.Equal(t, gateOpencodeUp, fieldMap["gate"],
		"must log gate field")
	elapsed, ok := fieldMap["elapsed_seconds"]
	assert.True(t, ok, "must log elapsed_seconds field")
	assert.Greater(t, elapsed, 0.0, "elapsed_seconds must be positive")
}

func TestGateRecorderSecondCallDoesNotLog(t *testing.T) {
	hist := newTestGateHist()
	core, logs := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	gr := newGateRecorder(time.Now(), hist, logger)

	gr.MaybeRecord(gateOpencodeUp)
	gr.MaybeRecord(gateOpencodeUp)

	assert.Len(t, logs.All(), 1, "second call must not emit a log line")
}

// ---- refreshIsHealthyLoop / refreshOnce integration ----

func TestRefreshOnceRecordsOpencodeUp(t *testing.T) {
	hist := newTestGateHist()
	gr := newGateRecorder(time.Now(), hist, zaptest.NewLogger(t))

	srv := fakeHealthServer(t, func(_ int) (bool, string) { return true, "v1" })
	cache := newHealthzCache()
	client := &OpenCodeClient{password: "", client: srv.Client()}
	setAgentAddr(srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Three refreshes — gate must fire once.
	for range 3 {
		refreshOnce(ctx, client, cache, zap.NewNop(), gr)
	}
	assert.EqualValues(t, 1, gateCount(t, hist, gateOpencodeUp))
}

func TestRefreshOnceDoesNotRecordGateWhenUnhealthy(t *testing.T) {
	hist := newTestGateHist()
	gr := newGateRecorder(time.Now(), hist, zaptest.NewLogger(t))

	srv := fakeHealthServer(t, func(_ int) (bool, string) { return false, "" })
	cache := newHealthzCache()
	client := &OpenCodeClient{password: "", client: srv.Client()}
	setAgentAddr(srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for range 5 {
		refreshOnce(ctx, client, cache, zap.NewNop(), gr)
	}
	assert.EqualValues(t, 0, gateCount(t, hist, gateOpencodeUp))
}

func TestRefreshOnceNilGateRecorderNoPanic(t *testing.T) {
	srv := fakeHealthServer(t, func(_ int) (bool, string) { return true, "v1" })
	cache := newHealthzCache()
	client := &OpenCodeClient{password: "", client: srv.Client()}
	setAgentAddr(srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	assert.NotPanics(t, func() {
		refreshOnce(ctx, client, cache, zap.NewNop(), nil)
	})
}

// ---- helpers ----

// fakeHealthServer returns an httptest.Server answering /global/health.
// The handler func receives the call index (0-based) and returns healthy + version.
func fakeHealthServer(t *testing.T, fn func(call int) (bool, string)) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/global/health" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		idx := n
		n++
		mu.Unlock()
		h, v := fn(idx)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Healthy bool   `json:"healthy"`
			Version string `json:"version"`
		}{Healthy: h, Version: v})
	}))
	t.Cleanup(srv.Close)
	return srv
}
