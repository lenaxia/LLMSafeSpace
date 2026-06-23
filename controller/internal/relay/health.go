// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HealthChecker scrapes the relay-router's /metrics endpoint to determine
// per-relay health and traffic metrics.
type HealthChecker struct {
	routerURL  string
	httpClient *http.Client
}

// NewHealthChecker creates a HealthChecker targeting the given router URL.
func NewHealthChecker(routerURL string) *HealthChecker {
	return &HealthChecker{
		routerURL:  strings.TrimRight(routerURL, "/"),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// RelayHealth represents the observed health of a single relay VM
// as reported by the router's /metrics endpoint.
type RelayHealth struct {
	ID            string
	Healthy       bool
	ActiveStreams int64
	Requests      int64
	Requests429   int64
	EgressBytes   int64
}

// HealthReport aggregates health data for the entire fleet.
type HealthReport struct {
	Relays         map[string]*RelayHealth
	FallbackActive bool
}

// Scrape fetches and parses the router's Prometheus metrics.
func (h *HealthChecker) Scrape(ctx context.Context) (*HealthReport, error) {
	url := h.routerURL + "/metrics"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build metrics request: %w", err)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scrape router metrics: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("router metrics returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read metrics body: %w", err)
	}

	return parseHealthMetrics(string(body)), nil
}

// parseHealthMetrics extracts relay health data from Prometheus text format.
//
// Wire contract: this parser must read what cmd/relay-router/metrics.go
// emits — see TestParseHealthMetrics_RouterEmittedFormat for the pinned
// shape. The router emits the relay ID under the "relay" label and the
// HTTP status under "status" on relay_router_requests_total. There is no
// separate relay_router_requests_429_total metric: the 429 count is the
// status="429" line of relay_router_requests_total.
func parseHealthMetrics(raw string) *HealthReport {
	report := &HealthReport{
		Relays: make(map[string]*RelayHealth),
	}

	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		value := extractMetricValue(line)

		if strings.HasPrefix(line, "relay_router_fallback_active") {
			report.FallbackActive = value > 0
			continue
		}

		id := extractMetricLabel(line, "relay")
		if id == "" {
			continue
		}

		relay := report.Relays[id]
		if relay == nil {
			relay = &RelayHealth{ID: id}
			report.Relays[id] = relay
		}

		switch {
		case strings.HasPrefix(line, "relay_router_relay_healthy{"):
			relay.Healthy = value > 0
		case strings.HasPrefix(line, "relay_router_active_streams{"):
			relay.ActiveStreams = value
		case strings.HasPrefix(line, "relay_router_requests_total{"):
			// Router emits this per-status. Sum across statuses for total
			// requests, and pull the status="429" subset separately.
			relay.Requests += value
			if extractMetricLabel(line, "status") == "429" {
				relay.Requests429 += value
			}
		case strings.HasPrefix(line, "relay_router_relay_egress_bytes{"):
			relay.EgressBytes = value
		}
	}

	return report
}

func extractMetricLabel(line, label string) string {
	prefix := label + "=\""
	start := strings.Index(line, prefix)
	if start < 0 {
		return ""
	}
	start += len(prefix)
	end := strings.IndexByte(line[start:], '"')
	if end < 0 {
		return ""
	}
	return line[start : start+end]
}

func extractMetricValue(line string) int64 {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0
	}
	var n int64
	last := parts[len(parts)-1]
	for _, c := range last {
		if c >= '0' && c <= '9' {
			n = n*10 + int64(c-'0')
		} else {
			break
		}
	}
	return n
}
