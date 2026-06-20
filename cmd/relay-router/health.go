// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// healthChecker periodically health-checks each relay VM via
// GET http://<endpoint>/healthz. The /healthz endpoint is exempt from token
// auth on the relay-proxy side, so the checker does not need the per-VM token.
type healthChecker struct {
	fleet    *relayFleet
	client   *http.Client
	interval time.Duration
}

func newHealthChecker(fleet *relayFleet, interval, timeout time.Duration, _ int) *healthChecker {
	return &healthChecker{
		fleet:    fleet,
		client:   &http.Client{Timeout: timeout},
		interval: interval,
	}
}

func (hc *healthChecker) run(ctx context.Context) {
	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()

	hc.checkAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hc.checkAll(ctx)
		}
	}
}

func (hc *healthChecker) checkAll(ctx context.Context) {
	statuses := hc.fleet.HealthyRelays()
	var wg sync.WaitGroup
	for _, s := range statuses {
		wg.Add(1)
		go func(id, endpoint string) {
			defer wg.Done()
			hc.checkOne(ctx, id, endpoint)
		}(s.ID, s.Endpoint)
	}
	wg.Wait()
}

func (hc *healthChecker) checkOne(ctx context.Context, relayID, endpoint string) {
	if endpoint == "" {
		return
	}

	url := fmt.Sprintf("http://%s/healthz", endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		hc.fleet.RecordHealthCheck(relayID, false)
		return
	}

	resp, err := hc.client.Do(req)
	if err != nil {
		hc.fleet.RecordHealthCheck(relayID, false)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	hc.fleet.RecordHealthCheck(relayID, resp.StatusCode == http.StatusOK)
}
