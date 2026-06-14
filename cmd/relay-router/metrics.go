// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// routerMetrics holds Prometheus-format metrics for the relay-router.
type routerMetrics struct {
	mu sync.Mutex

	requestsTotal  map[string]map[string]int64
	activeStreams  map[string]int64
	relayHealthy   map[string]bool
	relayEgress    map[string]int64
	fallbackActive bool
}

func newRouterMetrics() *routerMetrics {
	return &routerMetrics{
		requestsTotal: make(map[string]map[string]int64),
		activeStreams: make(map[string]int64),
		relayHealthy:  make(map[string]bool),
		relayEgress:   make(map[string]int64),
	}
}

func (m *routerMetrics) recordRequest(relayID string, statusCode int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	status := fmt.Sprintf("%d", statusCode)
	if m.requestsTotal[relayID] == nil {
		m.requestsTotal[relayID] = make(map[string]int64)
	}
	m.requestsTotal[relayID][status]++
}

func (m *routerMetrics) setActiveStreams(relayID string, count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeStreams[relayID] = count
}

func (m *routerMetrics) setRelayHealthy(relayID string, healthy bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.relayHealthy[relayID] = healthy
}

func (m *routerMetrics) setRelayEgress(relayID string, bytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.relayEgress[relayID] = bytes
}

func (m *routerMetrics) setFallbackActive(active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fallbackActive = active
}

func (m *routerMetrics) writePrometheus(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var b strings.Builder

	b.WriteString("# HELP relay_router_requests_total Total requests routed per relay by HTTP status\n")
	b.WriteString("# TYPE relay_router_requests_total counter\n")

	relayIDs := make([]string, 0, len(m.requestsTotal))
	for id := range m.requestsTotal {
		relayIDs = append(relayIDs, id)
	}
	sort.Strings(relayIDs)
	for _, id := range relayIDs {
		statuses := make([]string, 0, len(m.requestsTotal[id]))
		for s := range m.requestsTotal[id] {
			statuses = append(statuses, s)
		}
		sort.Strings(statuses)
		for _, s := range statuses {
			fmt.Fprintf(&b, "relay_router_requests_total{relay=\"%s\",status=\"%s\"} %d\n", id, s, m.requestsTotal[id][s])
		}
	}

	b.WriteString("# HELP relay_router_active_streams Current in-flight streaming connections per relay\n")
	b.WriteString("# TYPE relay_router_active_streams gauge\n")
	for _, id := range sortedKeys(m.activeStreams) {
		fmt.Fprintf(&b, "relay_router_active_streams{relay=\"%s\"} %d\n", id, m.activeStreams[id])
	}

	b.WriteString("# HELP relay_router_relay_healthy Router health view of each relay (1=healthy, 0=unhealthy)\n")
	b.WriteString("# TYPE relay_router_relay_healthy gauge\n")
	for _, id := range sortedBoolKeys(m.relayHealthy) {
		val := 0
		if m.relayHealthy[id] {
			val = 1
		}
		fmt.Fprintf(&b, "relay_router_relay_healthy{relay=\"%s\"} %d\n", id, val)
	}

	b.WriteString("# HELP relay_router_relay_egress_bytes Per-relay egress bytes aggregated from relay metrics\n")
	b.WriteString("# TYPE relay_router_relay_egress_bytes counter\n")
	for _, id := range sortedKeys(m.relayEgress) {
		fmt.Fprintf(&b, "relay_router_relay_egress_bytes{relay=\"%s\"} %d\n", id, m.relayEgress[id])
	}

	b.WriteString("# HELP relay_router_fallback_active 1 when router is in direct fallback mode\n")
	b.WriteString("# TYPE relay_router_fallback_active gauge\n")
	val := 0
	if m.fallbackActive {
		val = 1
	}
	fmt.Fprintf(&b, "relay_router_fallback_active %d\n", val)

	_, _ = io.WriteString(w, b.String())
}

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedBoolKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
