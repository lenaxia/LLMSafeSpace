// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseHealthMetrics_RouterEmittedFormat pins the wire contract between
// cmd/relay-router/metrics.go (producer) and controller/internal/relay/health.go
// (consumer). The router emits the relay ID under the "relay" label; the
// controller parser must read the same label.
//
// This regression test exists because of worklog 0464: the parser was
// originally written to read an "id" label which the router never produced,
// so HealthChecker.Scrape() returned an empty Relays map in production.
// Result: no relay ever transitioned out of "provisioning" because the
// controller never observed "healthy=true" from the router.
//
// If you change either side of this contract, this test will fail. Update
// both sides together, or you will reintroduce the worklog 0464 bug.
func TestParseHealthMetrics_RouterEmittedFormat(t *testing.T) {
	// This is the exact format produced by routerMetrics.writePrometheus() in
	// cmd/relay-router/metrics.go (verified by cmd/relay-router/proxy_test.go
	// TestRouterMetrics_PrometheusOutput). Single relay, two status codes,
	// one health point, one egress point.
	raw := `# HELP relay_router_requests_total Total requests routed per relay by HTTP status
# TYPE relay_router_requests_total counter
relay_router_requests_total{relay="i-0123456789abcdef0",status="200"} 12847
relay_router_requests_total{relay="i-0123456789abcdef0",status="429"} 2
# HELP relay_router_active_streams Current in-flight streaming connections per relay
# TYPE relay_router_active_streams gauge
relay_router_active_streams{relay="i-0123456789abcdef0"} 3
# HELP relay_router_relay_healthy Router health view of each relay (1=healthy, 0=unhealthy)
# TYPE relay_router_relay_healthy gauge
relay_router_relay_healthy{relay="i-0123456789abcdef0"} 1
# HELP relay_router_relay_egress_bytes Per-relay egress bytes aggregated from relay metrics
# TYPE relay_router_relay_egress_bytes counter
relay_router_relay_egress_bytes{relay="i-0123456789abcdef0"} 149546362
# HELP relay_router_fallback_active 1 when router is in direct fallback mode
# TYPE relay_router_fallback_active gauge
relay_router_fallback_active 0
`

	report := parseHealthMetrics(raw)

	require.NotNil(t, report)
	assert.False(t, report.FallbackActive)

	// The relay must be present, keyed by the instance ID extracted from the
	// "relay" label. Empty-keyed entries indicate the parser failed to find
	// the label (the worklog 0464 failure mode).
	require.Contains(t, report.Relays, "i-0123456789abcdef0",
		"parser failed to extract the relay ID from the router's 'relay' label — "+
			"this is the worklog 0464 bug")
	relay := report.Relays["i-0123456789abcdef0"]
	assert.Equal(t, "i-0123456789abcdef0", relay.ID)
	assert.True(t, relay.Healthy, "relay must be marked healthy when relay_router_relay_healthy{relay=...} 1")
	assert.Equal(t, int64(3), relay.ActiveStreams)
	assert.Equal(t, int64(149546362), relay.EgressBytes)

	// requests_total is emitted per-status by the router. The parser must sum
	// across status codes for total requests, and pull the 429 line by status
	// label (since the router does not emit a separate requests_429_total metric).
	assert.Equal(t, int64(12847+2), relay.Requests, "Requests must sum status=200 + status=429")
	assert.Equal(t, int64(2), relay.Requests429, "Requests429 must be the count of status=\"429\" lines")
}

// TestParseHealthMetrics_MultipleRelays verifies the parser correctly
// distinguishes multiple relays by the "relay" label.
func TestParseHealthMetrics_MultipleRelays(t *testing.T) {
	raw := `relay_router_relay_healthy{relay="i-aaaa"} 1
relay_router_relay_healthy{relay="i-bbbb"} 0
relay_router_active_streams{relay="i-aaaa"} 5
relay_router_active_streams{relay="i-bbbb"} 0
relay_router_relay_egress_bytes{relay="i-aaaa"} 1000
relay_router_relay_egress_bytes{relay="i-bbbb"} 2000
relay_router_fallback_active 0
`
	report := parseHealthMetrics(raw)

	require.Contains(t, report.Relays, "i-aaaa")
	require.Contains(t, report.Relays, "i-bbbb")
	assert.True(t, report.Relays["i-aaaa"].Healthy)
	assert.False(t, report.Relays["i-bbbb"].Healthy)
	assert.Equal(t, int64(5), report.Relays["i-aaaa"].ActiveStreams)
	assert.Equal(t, int64(0), report.Relays["i-bbbb"].ActiveStreams)
	assert.Equal(t, int64(1000), report.Relays["i-aaaa"].EgressBytes)
	assert.Equal(t, int64(2000), report.Relays["i-bbbb"].EgressBytes)
}

// TestParseHealthMetrics_FallbackActiveSet verifies the fallback gauge.
func TestParseHealthMetrics_FallbackActiveSet(t *testing.T) {
	raw := `relay_router_fallback_active 1`
	report := parseHealthMetrics(raw)
	assert.True(t, report.FallbackActive)
}

// TestParseHealthMetrics_EmptyAndComments verifies the parser tolerates the
// HELP/TYPE comment lines and blank lines from real Prometheus output.
func TestParseHealthMetrics_EmptyAndComments(t *testing.T) {
	report := parseHealthMetrics("")
	assert.Empty(t, report.Relays)
	assert.False(t, report.FallbackActive)

	report = parseHealthMetrics(`
# HELP some_metric Help text
# TYPE some_metric counter

`)
	assert.Empty(t, report.Relays)
}

// TestParseHealthMetrics_LineWithoutKnownLabel verifies that lines without
// the expected "relay" label are silently skipped (e.g. provider-level
// rollups or unrelated metrics that may share the prefix).
func TestParseHealthMetrics_LineWithoutKnownLabel(t *testing.T) {
	// No "relay" label — must be skipped without panic and without inserting
	// an empty-keyed entry.
	raw := `relay_router_relay_healthy{provider="aws"} 1
relay_router_fallback_active 0
`
	report := parseHealthMetrics(raw)
	assert.NotContains(t, report.Relays, "")
}
