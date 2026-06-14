// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeepalive_ProbesUpstream(t *testing.T) {
	var probeCount atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("keepalive probed %s, want /models", r.URL.Path)
		}
		probeCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	metrics := newRelayMetrics()
	ka := newKeepalive(upstream.URL, &http.Client{}, 20*time.Millisecond, metrics)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	ka.run(ctx)

	got := probeCount.Load()
	if got < 3 {
		t.Errorf("expected at least 3 probes in 150ms, got %d", got)
	}
}

func TestKeepalive_IncrementsCounter(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	metrics := newRelayMetrics()
	ka := newKeepalive(upstream.URL, &http.Client{}, 20*time.Millisecond, metrics)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ka.run(ctx)

	var buf strings.Builder
	metrics.writePrometheus(&buf)

	if !strings.Contains(buf.String(), "relay_keepalive_total") {
		t.Errorf("expected keepalive counter in metrics\ngot:\n%s", buf.String())
	}
	got := metrics.keepaliveTotal.Load()
	if got < 2 {
		t.Errorf("expected at least 2 keepalive probes recorded, got %d", got)
	}
}

func TestKeepalive_HandlesUpstreamFailure(t *testing.T) {
	metrics := newRelayMetrics()
	ka := newKeepalive("http://127.0.0.1:1", &http.Client{Timeout: 50 * time.Millisecond}, 20*time.Millisecond, metrics)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		ka.run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("keepalive did not stop within timeout")
	}

	got := metrics.keepaliveTotal.Load()
	if got < 2 {
		t.Errorf("expected keepalive to keep probing despite upstream failure, got %d probes", got)
	}
}

func TestKeepalive_StopsOnContextCancel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	metrics := newRelayMetrics()
	ka := newKeepalive(upstream.URL, &http.Client{}, 10*time.Millisecond, metrics)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		ka.run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("keepalive did not stop after context cancel")
	}
}

func TestKeepalive_DoesNotRecordRequestMetric(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	metrics := newRelayMetrics()
	ka := newKeepalive(upstream.URL, &http.Client{}, 10*time.Millisecond, metrics)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	ka.run(ctx)

	var buf strings.Builder
	metrics.writePrometheus(&buf)
	out := buf.String()

	if strings.Contains(out, `relay_requests_total{status="200"}`) {
		t.Errorf("keepalive should not increment relay_requests_total\ngot:\n%s", out)
	}
}
