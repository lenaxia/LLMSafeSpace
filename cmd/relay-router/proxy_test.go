// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testWsIDHeader = "X-Workspace-ID"

// ---------------------------------------------------------------------------
// Helper: create a fleet + relay + test upstream
// ---------------------------------------------------------------------------

func setupRouterTest(t *testing.T, upstreamHandler http.HandlerFunc) (*relayFleet, *detector429, *routerMetrics, *httptest.Server, *httptest.Server) {
	t.Helper()

	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)

	fleet := newRelayFleet(3, 5*time.Minute)
	metrics := newRouterMetrics()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(relay.Close)

	relayPort := extractPort(relay.URL)
	det := newDetector429(fleet, 0.5, relayPort)

	fleet.UpdatePeers([]PeerEntry{
		{ID: "test-relay", WgIP: extractHost(relay.URL), Provider: "oci", State: "healthy"},
	})

	return fleet, det, metrics, upstream, relay
}

func extractPort(url string) int {
	parts := strings.Split(url, ":")
	if len(parts) < 3 {
		return 8080
	}
	port := 0
	for _, c := range parts[2] {
		port = port*10 + int(c-'0')
	}
	return port
}

func extractHost(url string) string {
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "https://")
	parts := strings.Split(url, ":")
	return parts[0]
}

// ---------------------------------------------------------------------------
// Proxy forwarding tests
// ---------------------------------------------------------------------------

func TestRouterProxy_ForwardsToRelay(t *testing.T) {
	fleet, _, metrics, _, relay := setupRouterTest(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called when relay is healthy")
	})

	port := extractPort(relay.URL)
	fb, _ := newFallbackProxy("https://upstream.example.com", 0.5, 1)
	proxy := newRouterProxy(fleet, newDetector429(fleet, 0.5, port), metrics, port, fb)

	router := httptest.NewServer(proxy)
	t.Cleanup(router.Close)

	resp, err := http.Get(router.URL + "/v1/data")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, `{"ok":true}`, string(body))
}

func TestRouterProxy_FallbackWhenNoRelays(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	metrics := newRouterMetrics()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"from":"upstream"}`))
	}))
	t.Cleanup(upstream.Close)

	fb, err := newFallbackProxy(upstream.URL, 100.0, 5)
	require.NoError(t, err)

	proxy := newRouterProxy(fleet, newDetector429(fleet, 0.5, 8080), metrics, 8080, fb)

	router := httptest.NewServer(proxy)
	t.Cleanup(router.Close)

	resp, err := http.Get(router.URL + "/v1/data")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, fallbackHeaderValue, resp.Header.Get(fallbackHeader))
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, `{"from":"upstream"}`, string(body))
}

func TestRouterProxy_Returns502WhenNoRelayNoFallback(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	metrics := newRouterMetrics()

	proxy := newRouterProxy(fleet, newDetector429(fleet, 0.5, 8080), metrics, 8080, nil)

	router := httptest.NewServer(proxy)
	t.Cleanup(router.Close)

	resp, err := http.Get(router.URL + "/v1/data")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

func TestRouterProxy_StripsWorkspaceHeader(t *testing.T) {
	var receivedHeaders http.Header
	fleet := newRelayFleet(3, 5*time.Minute)
	metrics := newRouterMetrics()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		receivedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(relay.Close)

	fleet.UpdatePeers([]PeerEntry{
		{ID: "test-relay", WgIP: extractHost(relay.URL), Provider: "oci", State: "healthy"},
	})

	port := extractPort(relay.URL)
	fb, _ := newFallbackProxy("https://upstream.example.com", 0.5, 1)
	proxy := newRouterProxy(fleet, newDetector429(fleet, 0.5, port), metrics, port, fb)

	router := httptest.NewServer(proxy)
	t.Cleanup(router.Close)

	req, _ := http.NewRequest(http.MethodGet, router.URL+"/v1/data", nil)
	req.Header.Set(testWsIDHeader, "ws-12345")
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Empty(t, receivedHeaders.Get(testWsIDHeader), "X-Workspace-ID must be stripped before forwarding to relay")
}

// ---------------------------------------------------------------------------
// Fallback proxy tests
// ---------------------------------------------------------------------------

func TestFallbackProxy_RateLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	fp, err := newFallbackProxy(upstream.URL, 1.0, 1)
	require.NoError(t, err)

	server := httptest.NewServer(fp)
	t.Cleanup(server.Close)

	resp1, _ := http.Get(server.URL + "/test")
	resp1.Body.Close()
	assert.Equal(t, http.StatusOK, resp1.StatusCode)

	resp2, _ := http.Get(server.URL + "/test")
	resp2.Body.Close()
	assert.Equal(t, http.StatusTooManyRequests, resp2.StatusCode)
	assert.Equal(t, "2", resp2.Header.Get("Retry-After"))
}

func TestFallbackProxy_ConcurrencyLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	fp, err := newFallbackProxy(upstream.URL, 1000.0, 1)
	require.NoError(t, err)

	server := httptest.NewServer(fp)
	t.Cleanup(server.Close)

	var wg sync.WaitGroup
	statusCodes := make([]int, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, _ := http.Get(server.URL + "/test")
			if resp != nil {
				statusCodes[idx] = resp.StatusCode
				resp.Body.Close()
			}
		}(i)
	}
	wg.Wait()

	okCount := 0
	rateLimited := 0
	for _, code := range statusCodes {
		if code == http.StatusOK {
			okCount++
		}
		if code == http.StatusTooManyRequests {
			rateLimited++
		}
	}
	assert.Equal(t, 1, okCount, "only 1 request should succeed (maxConcurrent=1)")
	assert.True(t, rateLimited >= 1, "at least 1 request should be rate-limited")
}

func TestFallbackProxy_SetsFallbackHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	fp, err := newFallbackProxy(upstream.URL, 1000.0, 5)
	require.NoError(t, err)

	server := httptest.NewServer(fp)
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, fallbackHeaderValue, resp.Header.Get(fallbackHeader))
}

func TestFallbackProxy_StreamsResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		chunks := []string{"data: a\n\n", "data: b\n\n"}
		for _, chunk := range chunks {
			_, _ = w.Write([]byte(chunk))
			flusher.Flush()
			time.Sleep(20 * time.Millisecond)
		}
	}))
	t.Cleanup(upstream.Close)

	fp, err := newFallbackProxy(upstream.URL, 1000.0, 5)
	require.NoError(t, err)

	transport := &http.Transport{DisableCompression: true}
	client := &http.Client{Transport: transport}

	server := httptest.NewServer(fp)
	t.Cleanup(server.Close)

	resp, err := client.Get(server.URL + "/stream")
	require.NoError(t, err)
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lines = append(lines, line)
		}
	}
	assert.Len(t, lines, 2)
	assert.Equal(t, "data: a", lines[0])
	assert.Equal(t, "data: b", lines[1])
}

func TestFallbackProxy_InvalidURL(t *testing.T) {
	_, err := newFallbackProxy("", 0.5, 1)
	assert.Error(t, err)

	_, err = newFallbackProxy("ftp://bad", 0.5, 1)
	assert.Error(t, err)

	_, err = newFallbackProxy("http://", 0.5, 1)
	assert.Error(t, err)
}

func TestFallbackProxy_UpstreamError(t *testing.T) {
	_, err := newFallbackProxy("http://127.0.0.1:1", 1000.0, 5)
	require.NoError(t, err)

	fp, _ := newFallbackProxy("http://127.0.0.1:1", 1000.0, 5)
	server := httptest.NewServer(fp)
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/test")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// Health checker tests
// ---------------------------------------------------------------------------

func TestHealthChecker_MarksHealthyOnSuccess(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(relay.Close)

	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", WgIP: extractHost(relay.URL), Provider: "oci", State: "healthy"},
	})

	hc := newHealthChecker(fleet, 20*time.Millisecond, 1*time.Second, extractPort(relay.URL))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	hc.run(ctx)

	statuses := fleet.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Healthy)
}

func TestHealthChecker_MarksUnhealthyOnFailure(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", WgIP: "127.0.0.1", Provider: "oci", State: "healthy"},
	})

	hc := newHealthChecker(fleet, 20*time.Millisecond, 50*time.Millisecond, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	hc.run(ctx)

	statuses := fleet.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Healthy, "should be unhealthy after 3 consecutive failures")
}

func TestHealthChecker_StopsOnContextCancel(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(relay.Close)

	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", WgIP: extractHost(relay.URL), Provider: "oci", State: "healthy"},
	})

	hc := newHealthChecker(fleet, 10*time.Millisecond, 1*time.Second, extractPort(relay.URL))
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		hc.run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("health checker did not stop")
	}
}

// ---------------------------------------------------------------------------
// Detector tests
// ---------------------------------------------------------------------------

func TestDetector_OnNon429ClearsState(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(relay.Close)

	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", WgIP: extractHost(relay.URL), Provider: "oci", State: "healthy"},
	})

	det := newDetector429(fleet, 0.5, extractPort(relay.URL))

	fleet.RecordRequest("r1", 429)
	det.OnResponse(context.Background(), "r1", 429)

	det.OnResponse(context.Background(), "r1", 200)

	statuses := fleet.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Draining429)
}

func TestDetector_StormDetection(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
		{ID: "r2", WgIP: "10.42.42.3", Provider: "gcp", State: "healthy"},
	})

	det := newDetector429(fleet, 0.5, 8080)

	for i := 0; i < 20; i++ {
		fleet.RecordRequest("r1", 429)
	}

	det.checkAllStorms()

	statuses := fleet.HealthyRelays()
	var r1Status RelayStatus
	for _, s := range statuses {
		if s.ID == "r1" {
			r1Status = s
		}
	}
	assert.True(t, r1Status.Draining429, "r1 should be draining after 100%% 429 storm detected by checkAllStorms")

	id, _, ok := fleet.SelectRelay()
	require.True(t, ok)
	assert.Equal(t, "r2", id, "r1 should be excluded after 429 storm")
}

func TestDetector_CheckAllStorms(t *testing.T) {
	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", WgIP: "10.42.42.2", Provider: "oci", State: "healthy"},
	})

	for i := 0; i < 20; i++ {
		fleet.RecordRequest("r1", 429)
	}

	det := newDetector429(fleet, 0.5, 8080)
	det.checkAllStorms()

	statuses := fleet.HealthyRelays()
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Draining429, "relay should be draining after storm")
}

// ---------------------------------------------------------------------------
// Metrics tests
// ---------------------------------------------------------------------------

func TestRouterMetrics_PrometheusFormat(t *testing.T) {
	m := newRouterMetrics()
	m.recordRequest("r1", 200)
	m.recordRequest("r1", 429)
	m.setActiveStreams("r1", 3)
	m.setRelayHealthy("r1", true)
	m.setRelayEgress("r1", 10240)
	m.setFallbackActive(false)

	var sb strings.Builder
	m.writePrometheus(&sb)
	out := sb.String()

	assert.Contains(t, out, `relay_router_requests_total{relay="r1",status="200"} 1`)
	assert.Contains(t, out, `relay_router_requests_total{relay="r1",status="429"} 1`)
	assert.Contains(t, out, `relay_router_active_streams{relay="r1"} 3`)
	assert.Contains(t, out, `relay_router_relay_healthy{relay="r1"} 1`)
	assert.Contains(t, out, `relay_router_relay_egress_bytes{relay="r1"} 10240`)
	assert.Contains(t, out, `relay_router_fallback_active 0`)
}

func TestRouterMetrics_FallbackActive(t *testing.T) {
	m := newRouterMetrics()
	m.setFallbackActive(true)

	var sb strings.Builder
	m.writePrometheus(&sb)
	assert.Contains(t, sb.String(), "relay_router_fallback_active 1")
}

func TestRouterMetrics_MultipleRelays(t *testing.T) {
	m := newRouterMetrics()
	m.recordRequest("oci-1", 200)
	m.recordRequest("gcp-1", 200)
	m.recordRequest("gcp-1", 429)
	m.setRelayHealthy("oci-1", true)
	m.setRelayHealthy("gcp-1", false)

	var sb strings.Builder
	m.writePrometheus(&sb)
	out := sb.String()

	assert.Contains(t, out, `relay="oci-1"`)
	assert.Contains(t, out, `relay="gcp-1"`)
	assert.Contains(t, out, `relay_router_relay_healthy{relay="oci-1"} 1`)
	assert.Contains(t, out, `relay_router_relay_healthy{relay="gcp-1"} 0`)
}

// ---------------------------------------------------------------------------
// E2E integration test
// ---------------------------------------------------------------------------

func TestE2E_RouterForwardsAndMetrics(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models":["gpt-4"]}`))
	}))
	t.Cleanup(relay.Close)

	fleet := newRelayFleet(3, 5*time.Minute)
	fleet.UpdatePeers([]PeerEntry{
		{ID: "test-relay", WgIP: extractHost(relay.URL), Provider: "oci", State: "healthy"},
	})

	metrics := newRouterMetrics()
	relayPort := extractPort(relay.URL)
	detector := newDetector429(fleet, 0.5, relayPort)
	fb, _ := newFallbackProxy("https://upstream.example.com", 0.5, 1)
	proxy := newRouterProxy(fleet, detector, metrics, relayPort, fb)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		statuses := fleet.HealthyRelays()
		for _, s := range statuses {
			metrics.setRelayHealthy(s.ID, s.Healthy)
			metrics.setActiveStreams(s.ID, s.ActiveStreams)
		}
		w.Header().Set("Content-Type", "text/plain")
		metrics.writePrometheus(w)
	})
	mux.Handle("/", proxy)

	router := httptest.NewServer(mux)
	t.Cleanup(router.Close)

	resp, err := http.Get(router.URL + "/v1/models")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, `{"models":["gpt-4"]}`, string(body))

	metricsResp, err := http.Get(router.URL + "/metrics")
	require.NoError(t, err)
	metricsBody, _ := io.ReadAll(metricsResp.Body)
	metricsResp.Body.Close()

	metricsStr := string(metricsBody)
	assert.Contains(t, metricsStr, `relay_router_requests_total{relay="test-relay",status="200"} 1`)
	assert.Contains(t, metricsStr, `relay_router_relay_healthy{relay="test-relay"} 1`)
}

func TestE2E_FallbackTransitionsBackToRelay(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"from":"relay"}`))
	}))
	t.Cleanup(relay.Close)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"from":"upstream"}`))
	}))
	t.Cleanup(upstream.Close)

	fleet := newRelayFleet(3, 5*time.Minute)
	metrics := newRouterMetrics()
	relayPort := extractPort(relay.URL)
	detector := newDetector429(fleet, 0.5, relayPort)
	fb, _ := newFallbackProxy(upstream.URL, 1000.0, 5)
	proxy := newRouterProxy(fleet, detector, metrics, relayPort, fb)

	server := httptest.NewServer(proxy)
	t.Cleanup(server.Close)

	resp, _ := http.Get(server.URL + "/test")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, `{"from":"upstream"}`, string(body), "should fallback when no relays")

	fleet.UpdatePeers([]PeerEntry{
		{ID: "r1", WgIP: extractHost(relay.URL), Provider: "oci", State: "healthy"},
	})

	resp2, _ := http.Get(server.URL + "/test")
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	assert.Equal(t, `{"from":"relay"}`, string(body2), "should use relay once available")
}
