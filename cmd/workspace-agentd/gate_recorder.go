// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

// startupGate names the per-boot milestones tracked by gateRecorder.
const (
	gateOpencodeUp         = "opencode_up"
	gateProvidersConnected = "providers_connected"
	gateReadyzFirst200     = "readyz_first_200"
)

// gateRecorder fires exactly once per gate per agentd boot. It emits a
// structured zap log line and a Prometheus histogram observation the first
// time each gate transitions from "not yet reached" to "reached".
//
// Thread-safe: multiple goroutines may call MaybeRecord concurrently.
// After the first successful call for a given gate, subsequent calls for
// the same gate are no-ops.
type gateRecorder struct {
	mu      sync.Mutex
	reached map[string]bool
	boot    time.Time
	hist    *prometheus.HistogramVec
	logger  *zap.Logger
}

// newGateRecorder creates a recorder rooted at bootTime. hist must have a
// single label "gate". logger must be non-nil.
func newGateRecorder(bootTime time.Time, hist *prometheus.HistogramVec, logger *zap.Logger) *gateRecorder {
	return &gateRecorder{
		reached: make(map[string]bool),
		boot:    bootTime,
		hist:    hist,
		logger:  logger,
	}
}

// MaybeRecord records gate if and only if it has not been recorded before in
// this boot. Returns true on the first call for a given gate, false on all
// subsequent calls (allowing callers to detect the first transition).
//
// elapsed is computed relative to the boot time passed to newGateRecorder so
// measurements are stable even if the caller calls MaybeRecord at different
// wall-clock times.
func (g *gateRecorder) MaybeRecord(gate string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.reached[gate] {
		return false
	}
	g.reached[gate] = true
	elapsed := time.Since(g.boot).Seconds()
	g.hist.WithLabelValues(gate).Observe(elapsed)
	g.logger.Info("startup gate reached",
		zap.String("gate", gate),
		zap.Float64("elapsed_seconds", elapsed),
	)
	return true
}

// agentdGateDurationSeconds is the package-level Prometheus histogram used in
// production. Tests inject their own instances via newGateRecorder.
var agentdGateDurationSeconds = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "llmsafespace_agentd_gate_duration_seconds",
		Help:    "Time from agentd boot to each startup gate (opencode_up, providers_connected, readyz_first_200)",
		Buckets: []float64{1, 2, 5, 10, 15, 20, 30, 45, 60, 90, 120},
	},
	[]string{"gate"},
)

func init() {
	prometheus.MustRegister(agentdGateDurationSeconds)
}
