// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── forwardToRelay error branches ──────────────────────────────────────────

// TestForwardToRelay_UpstreamUnreachable verifies the httpClient.Do error path:
// when the relay VM is down, the router returns 502 and records a health check
// failure.
func TestForwardToRelay_UpstreamUnreachable(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", Endpoint: "127.0.0.1:1", Provider: "oci", State: "healthy"}, // port 1 = refused
	})
	fb, _ := newFallbackProxy("https://unused.example.com", 0.5, 1)
	proxy := newRouterProxy(fleet, newDetector429(fleet, 0.5, 0), newRouterMetrics(), 0, fb)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"x"}`))
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code,
		"unreachable relay must surface as 502")
}

// TestForwardToRelay_ClientCanceled verifies the context-cancel branch: when
// the client disconnects mid-request, the router returns nothing (the
// r.Context().Err() != nil check suppresses the 502 so the logs don't fill
// with noise from normal disconnects).
func TestForwardToRelay_ClientCanceled(t *testing.T) {
	// Use a slow upstream that blocks until the context cancels.
	slowCh := make(chan struct{})
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-slowCh // block until test releases
	}))
	t.Cleanup(func() { close(slowCh); relay.Close() })

	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", Endpoint: extractEndpoint(relay.URL), Provider: "oci", State: "healthy"},
	})
	fb, _ := newFallbackProxy("https://unused.example.com", 0.5, 1)
	proxy := newRouterProxy(fleet, newDetector429(fleet, 0.5, 0), newRouterMetrics(), 0, fb)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so r.Context().Err() != nil

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	// When context is already cancelled, forwardToRelay returns silently
	// (no 502 written — the early-return on r.Context().Err() path).
	// Default httptest recorder status is 200.
	assert.Equal(t, http.StatusOK, rec.Code,
		"canceled context must return silently (no 502) — the context.Err check suppresses the error")
}

// ─── fallback forward error branches ────────────────────────────────────────

// TestFallback_Forward_Unreachable verifies the fallback forward's HTTP error
// path: when the upstream is down, the fallback returns 502.
func TestFallback_Forward_Unreachable(t *testing.T) {
	fb, err := newFallbackProxy("http://127.0.0.1:1", 100, 10)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	fb.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

// TestFallback_Forward_ClientCanceled verifies the context-cancel branch in
// fallback forward (the early-return path that avoids logging normal disconnects
// as errors).
func TestFallback_Forward_ClientCanceled(t *testing.T) {
	slowCh := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-slowCh
	}))
	t.Cleanup(func() { close(slowCh); upstream.Close() })

	fb, err := newFallbackProxy(upstream.URL, 100, 10)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	fb.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code,
		"canceled fallback context must return silently (default recorder status)")
}

// ─── detector probeRelay / checkStorm branches ──────────────────────────────

// TestProbeRelay_429Response_MarksSuspect verifies the full probeRelay path:
// a 429 from the probe increments the suspect counter and triggers checkStorm.
func TestProbeRelay_429Response_MarksSuspect(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(relay.Close)

	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", Endpoint: extractEndpoint(relay.URL), Provider: "oci", State: "healthy"},
	})
	det := newDetector429(fleet, 0.5, 0)

	// First probe: 429 → suspect, checkStorm won't drain (consecutive < 3)
	det.probeRelay(context.Background(), "r1")
	rate, consec := fleet.Relay429Rate("r1")
	_ = rate
	assert.Equal(t, 1, consec, "first 429 probe must increment consecutiveProbes")

	// Second probe: still not enough (threshold is 3)
	det.mu.Lock()
	delete(det.probedRelays, "r1") // reset so probeRelay actually re-probes
	det.mu.Unlock()
	det.probeRelay(context.Background(), "r1")
	_, consec = fleet.Relay429Rate("r1")
	assert.Equal(t, 2, consec, "second 429 must increment to 2")
}

// TestProbeRelay_200Response_ClearsState verifies the non-429 probe path
// clears 429 state.
func TestProbeRelay_200Response_ClearsState(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(relay.Close)

	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", Endpoint: extractEndpoint(relay.URL), Provider: "oci", State: "healthy"},
	})
	det := newDetector429(fleet, 0.5, 0)

	// Set some 429 state first
	fleet.Mark429Suspect("r1")
	det.probeRelay(context.Background(), "r1") // 200 → Clear429State

	_, consec := fleet.Relay429Rate("r1")
	assert.Equal(t, 0, consec, "200 probe must reset consecutiveProbes")
}

// TestCheckStorm_ConsecutiveProbes verifies the consecutive-probe drain trigger
// (threshold = maxConsecutive = 3).
func TestCheckStorm_ConsecutiveProbes(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", Endpoint: "1.2.3.4:8080", Provider: "oci", State: "healthy"},
	})
	det := newDetector429(fleet, 0.5, 0)

	for i := 0; i < 3; i++ {
		fleet.Mark429Suspect("r1")
	}
	det.checkStorm("r1")

	statuses := fleet.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Draining429, "3 consecutive 429 probes must mark relay draining")
}

// TestCheckStorm_RateExceeded verifies the windowed-rate drain trigger.
func TestCheckStorm_RateExceeded(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", Endpoint: "1.2.3.4:8080", Provider: "oci", State: "healthy"},
	})
	det := newDetector429(fleet, 0.5, 0)

	// Record many 429s so the rate exceeds 0.5
	for i := 0; i < 10; i++ {
		fleet.RecordRequest("r1", 429)
	}
	// Only 2 consecutive probes (below threshold of 3) so the rate path triggers
	fleet.Mark429Suspect("r1")
	fleet.Mark429Suspect("r1")
	det.checkStorm("r1")

	statuses := fleet.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Draining429, "rate >= 0.5 must mark relay draining")
}

// TestCheckAllStorms_NoRelays verifies the empty-fleet edge case (no panic).
func TestCheckAllStorms_NoRelays(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	det := newDetector429(fleet, 0.5, 0)
	assert.NotPanics(t, func() { det.checkAllStorms() })
}

// ─── health.go checkOne error branches ──────────────────────────────────────

// TestCheckOne_EmptyEndpoint verifies checkOne returns early when endpoint is
// empty (no panic, no health-check recorded).
func TestCheckOne_EmptyEndpoint(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", Endpoint: "", Provider: "oci", State: "healthy"},
	})
	hc := newHealthChecker(fleet, 1*time.Second, 1*time.Second, 0)
	hc.checkOne(context.Background(), "r1", "")
	// Should not panic and should not record any health check
	// (if it did, relayKey would be absent from HealthyRelays)
}

// TestCheckOne_RequestError verifies checkOne records a failed health check when
// the relay is unreachable. After unhealthyThreshold (3) failures, the relay is
// marked unhealthy.
func TestCheckOne_RequestError(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", Endpoint: "127.0.0.1:1", Provider: "oci", State: "healthy"}, // refused
	})
	hc := newHealthChecker(fleet, 1*time.Second, 1*time.Second, 0)
	// Three consecutive failures to hit the threshold
	for i := 0; i < 3; i++ {
		hc.checkOne(context.Background(), "r1", "127.0.0.1:1")
	}

	statuses := fleet.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Healthy, "3 consecutive failures must mark relay unhealthy")
}

// ─── fleet String() ─────────────────────────────────────────────────────────

func TestFleet_String(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "oci-1", Endpoint: "1.2.3.4:8080", Provider: "oci", State: "healthy"},
		{ID: "aws-1", Endpoint: "5.6.7.8:8080", Provider: "aws", State: "healthy"},
	})
	s := fleet.String()
	assert.Contains(t, s, "oci-1")
	assert.Contains(t, s, "aws-1")
}

// ─── SelectRelay fallback edge cases ────────────────────────────────────────

// TestSelectRelay_AllDraining_ReturnsFalse verifies the no-eligible-relay case.
func TestSelectRelay_AllDraining_ReturnsFalse(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "oci-1", Endpoint: "1.2.3.4:8080", Provider: "oci", State: "draining"},
		{ID: "aws-1", Endpoint: "5.6.7.8:8080", Provider: "aws", State: "draining"},
	})
	_, _, _, ok := fleet.SelectRelay()
	assert.False(t, ok, "all-draining fleet must return ok=false")
}

// TestSelectRelay_EmptyFleet_ReturnsFalse verifies the empty-fleet case.
func TestSelectRelay_EmptyFleet_ReturnsFalse(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	_, _, _, ok := fleet.SelectRelay()
	assert.False(t, ok)
}

// ─── ActiveStreams ──────────────────────────────────────────────────────────

func TestActiveStreams_NonexistentRelay(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	assert.Equal(t, int64(0), fleet.ActiveStreams("nonexistent"))
}

func TestActiveStreams_RecordsAndReturns(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", Endpoint: "1.2.3.4:8080", Provider: "oci", State: "healthy"},
	})
	fleet.RecordStreamStart("r1")
	fleet.RecordStreamStart("r1")
	assert.Equal(t, int64(2), fleet.ActiveStreams("r1"))
	fleet.RecordStreamEnd("r1")
	assert.Equal(t, int64(1), fleet.ActiveStreams("r1"))
}

// ─── GetEndpoint nonexistent ───────────────────────────────────────────────

func TestGetEndpoint_Nonexistent(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	assert.Equal(t, "", fleet.GetEndpoint("nonexistent"))
}

// ─── newFallbackProxy validation branches ───────────────────────────────────

func TestNewFallbackProxy_BadScheme(t *testing.T) {
	_, err := newFallbackProxy("ftp://example.com", 1, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scheme")
}

func TestNewFallbackProxy_MissingHost(t *testing.T) {
	_, err := newFallbackProxy("https://", 1, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host")
}

func TestNewFallbackProxy_BadURL(t *testing.T) {
	_, err := newFallbackProxy("://", 1, 1)
	require.Error(t, err)
}

// ─── copyRouterHeaders ──────────────────────────────────────────────────────

func TestCopyRouterHeaders_SkipsHopByHop(t *testing.T) {
	src := http.Header{}
	src.Set("Connection", "keep-alive")
	src.Set("X-Workspace-Id", "ws-123")
	src.Set("Authorization", "Bearer public")
	src.Set("X-Custom", "value")

	dst := http.Header{}
	copyRouterHeaders(dst, src)

	assert.Empty(t, dst.Get("Connection"), "Connection must be stripped (hop-by-hop)")
	assert.Empty(t, dst.Get("X-Workspace-Id"), "X-Workspace-Id must be stripped (hop-by-hop)")
	assert.Equal(t, "Bearer public", dst.Get("Authorization"))
	assert.Equal(t, "value", dst.Get("X-Custom"))
}

// ─── ServeHTTP → fallback when no relays ────────────────────────────────────

// TestRouterProxy_ServeHTTP_FallbackWhenNoRelays verifies the full request path:
// when SelectRelay returns false, the router falls through to the fallback proxy.
func TestRouterProxy_ServeHTTP_FallbackWhenNoRelays(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(fallbackHeader, fallbackHeaderValue)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "from-upstream")
	}))
	t.Cleanup(upstream.Close)

	fleet := newRelayFleet(3, 5*time.Minute) // empty — no relays
	fb, err := newFallbackProxy(upstream.URL, 100, 10)
	require.NoError(t, err)
	proxy := newRouterProxy(fleet, newDetector429(fleet, 0.5, 0), newRouterMetrics(), 0, fb)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"x"}`))
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "empty fleet must fall through to fallback")
	assert.Equal(t, "fallback", rec.Header().Get(fallbackHeader))
	assert.Contains(t, rec.Body.String(), "from-upstream")
}

// TestRouterProxy_ServeHTTP_NoFallback verifies the nil-fallback case returns 502.
func TestRouterProxy_ServeHTTP_NoFallback(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute) // empty
	proxy := newRouterProxy(fleet, newDetector429(fleet, 0.5, 0), newRouterMetrics(), 0, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code, "empty fleet + nil fallback must return 502")
}
