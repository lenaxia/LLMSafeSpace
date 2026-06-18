// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/lenaxia/llmsafespaces/pkg/agentd"
)

// healthzHandler returns the http.HandlerFunc serving GET /v1/healthz.
//
// US-22.1 contract: process-only liveness. The handler MUST NOT make any
// HTTP calls to opencode. If this handler executes, the workspace-agentd
// process is alive and able to respond to HTTP — which is exactly the
// signal kubelet's liveness probe needs.
//
// Pre-US-22.1, the handler called client.IsHealthy() (which HTTP-GETs
// opencode's /global/health). When opencode was busy under SSE load,
// IsHealthy timed out, kubelet's liveness probe failed repeatedly, and
// after FailureThreshold=6 the kubelet killed the pod even though
// agentd itself was healthy. Worklog 0096 documented the failure mode;
// this implementation eliminates it by removing the opencode dependency
// from the liveness path entirely.
//
// Performance contract: p99 < 100ms. Implementation is allocation-light
// (just one json.Encode and a clock read); all observed latency is from
// json encoding and the OS-level HTTP layer, not from in-handler logic.
//
// Response shape is agentd.HealthzResponse, unchanged from pre-US-22.1
// for kubelet compatibility. Healthy is always true when the handler
// executes (a dead process can't respond, by definition); the field
// exists for forward-compat. Version reports the agentd build version
// (not opencode's) — see buildVersion. UptimeSeconds reports time since
// startedAt was captured at agentd startup.
func healthzHandler(startedAt time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(agentd.HealthzResponse{
			Healthy:       true,
			Version:       buildVersion,
			UptimeSeconds: int(time.Since(startedAt).Seconds()),
		})
	}
}
