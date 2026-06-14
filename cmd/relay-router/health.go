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

// healthChecker periodically health-checks each relay VM over the
// WireGuard tunnel via GET http://<wgIP>:8080/healthz.
type healthChecker struct {
	fleet     *relayFleet
	client    *http.Client
	interval  time.Duration
	relayPort int
}

func newHealthChecker(fleet *relayFleet, interval, timeout time.Duration, relayPort int) *healthChecker {
	return &healthChecker{
		fleet:     fleet,
		client:    &http.Client{Timeout: timeout},
		interval:  interval,
		relayPort: relayPort,
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
		go func(id, wgIP string) {
			defer wg.Done()
			hc.checkOne(ctx, id, wgIP)
		}(s.ID, s.WgIP)
	}
	wg.Wait()
}

func (hc *healthChecker) checkOne(ctx context.Context, relayID, wgIP string) {
	if wgIP == "" {
		return
	}

	url := fmt.Sprintf("http://%s:%d/healthz", wgIP, hc.relayPort)
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
	resp.Body.Close()

	hc.fleet.RecordHealthCheck(relayID, resp.StatusCode == http.StatusOK)
}
