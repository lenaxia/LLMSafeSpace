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

// detector429 implements two-tier 429 detection per the design:
// Tier 1 — Immediate probe: on first 429 from a relay, probe GET /models.
//
//	If probe also 429, mark relay suspect.
//
// Tier 2 — Storm detection: if 429 rate exceeds threshold over the window
//
//	OR 3 consecutive probes return 429, mark relay draining.
type detector429 struct {
	fleet          *relayFleet
	client         *http.Client
	max429Rate     float64
	maxConsecutive int
	mu             sync.Mutex
	probedRelays   map[string]bool
}

func newDetector429(fleet *relayFleet, max429Rate float64, _ int) *detector429 {
	return &detector429{
		fleet:          fleet,
		client:         &http.Client{Timeout: 5 * time.Second},
		max429Rate:     max429Rate,
		maxConsecutive: 3,
		probedRelays:   make(map[string]bool),
	}
}

// OnResponse is called after each proxied response. If the response is 429,
// it triggers an immediate probe (Tier 1).
func (d *detector429) OnResponse(ctx context.Context, relayID string, statusCode int) {
	if statusCode != http.StatusTooManyRequests {
		d.fleet.Clear429State(relayID)
		d.mu.Lock()
		delete(d.probedRelays, relayID)
		d.mu.Unlock()
		return
	}

	d.mu.Lock()
	alreadyProbed := d.probedRelays[relayID]
	d.mu.Unlock()

	if !alreadyProbed {
		d.probeRelay(ctx, relayID)
	}
}

// probeRelay sends GET /models to the relay. If it returns 429, marks
// the relay suspect and increments the consecutive probe counter.
// If consecutive probes reach maxConsecutive, marks relay draining.
func (d *detector429) probeRelay(ctx context.Context, relayID string) {
	endpoint := d.fleet.GetEndpoint(relayID)
	if endpoint == "" {
		return
	}

	d.mu.Lock()
	d.probedRelays[relayID] = true
	d.mu.Unlock()

	url := fmt.Sprintf("http://%s/models", endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		d.fleet.Mark429Suspect(relayID)
		d.checkStorm(relayID)
	} else {
		d.fleet.Clear429State(relayID)
		d.mu.Lock()
		delete(d.probedRelays, relayID)
		d.mu.Unlock()
	}
}

// checkStorm evaluates whether a relay should be marked draining based on
// consecutive probe failures OR windowed 429 rate exceeding the threshold.
func (d *detector429) checkStorm(relayID string) {
	rate, consecutive := d.fleet.Relay429Rate(relayID)
	if consecutive >= d.maxConsecutive {
		d.fleet.Mark429Draining(relayID, fmt.Sprintf("429-consecutive-probes: %d probes failed (threshold=%d)", consecutive, d.maxConsecutive))
		return
	}
	if rate >= d.max429Rate {
		d.fleet.Mark429Draining(relayID, fmt.Sprintf("429-storm: windowed rate=%.2f threshold=%.2f", rate, d.max429Rate))
	}
}

// runPeriodicCheck evaluates all relays for 429 storm conditions on a timer.
// This catches slow-burn 429 storms that don't trigger the immediate probe path.
func (d *detector429) runPeriodicCheck(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.checkAllStorms()
		}
	}
}

func (d *detector429) checkAllStorms() {
	statuses := d.fleet.HealthyRelays()
	for _, s := range statuses {
		if s.Draining429 {
			continue
		}
		rate, _ := d.fleet.Relay429Rate(s.ID)
		if rate >= d.max429Rate {
			d.fleet.Mark429Draining(s.ID, fmt.Sprintf("429-storm: windowed rate=%.2f threshold=%.2f", rate, d.max429Rate))
		}
	}
}
