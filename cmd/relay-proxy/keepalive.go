// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"net/http"
	"time"
)

type keepalive struct {
	upstreamURL string
	client      *http.Client
	interval    time.Duration
	metrics     *relayMetrics
}

func newKeepalive(upstreamURL string, client *http.Client, interval time.Duration, metrics *relayMetrics) *keepalive {
	return &keepalive{
		upstreamURL: upstreamURL,
		client:      client,
		interval:    interval,
		metrics:     metrics,
	}
}

func (k *keepalive) run(ctx context.Context) {
	ticker := time.NewTicker(k.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			k.probe(ctx)
		}
	}
}

func (k *keepalive) probe(ctx context.Context) {
	url := k.upstreamURL + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}

	resp, err := k.client.Do(req)
	if err != nil {
		k.metrics.recordKeepalive()
		return
	}
	defer func() { _ = resp.Body.Close() }()
	k.metrics.recordKeepalive()
}
