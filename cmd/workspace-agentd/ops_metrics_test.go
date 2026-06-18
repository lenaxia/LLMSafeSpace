// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

// US-44.8: Operational Monitoring (Prometheus).
//
// Tests use the package-level pkgOpsMetrics singleton because promauto
// registers on the default Prometheus registry — constructing multiple
// instances would panic on duplicate registration.

func TestOpsMetrics_RestartsCounter_RecordsByReason(t *testing.T) {
	pkgOpsMetrics.RecordRestart("ws-test-r1", "env_secrets")
	pkgOpsMetrics.RecordRestart("ws-test-r1", "env_secrets")
	pkgOpsMetrics.RecordRestart("ws-test-r1", "crash")
	pkgOpsMetrics.RecordRestart("ws-test-r2", "user_requested")

	assert.Equal(t, 2.0, testutil.ToFloat64(pkgOpsMetrics.restartsTotal.WithLabelValues("ws-test-r1", "env_secrets")))
	assert.Equal(t, 1.0, testutil.ToFloat64(pkgOpsMetrics.restartsTotal.WithLabelValues("ws-test-r1", "crash")))
	assert.Equal(t, 1.0, testutil.ToFloat64(pkgOpsMetrics.restartsTotal.WithLabelValues("ws-test-r2", "user_requested")))
}

func TestOpsMetrics_MemoryGauge_RecordsLatestValue(t *testing.T) {
	pkgOpsMetrics.SetMemoryUsage("ws-test-mem", 1500*1024*1024)
	assert.Equal(t, 1500.0*1024*1024, testutil.ToFloat64(pkgOpsMetrics.memoryBytes.WithLabelValues("ws-test-mem")))

	pkgOpsMetrics.SetMemoryUsage("ws-test-mem", 2000*1024*1024)
	assert.Equal(t, 2000.0*1024*1024, testutil.ToFloat64(pkgOpsMetrics.memoryBytes.WithLabelValues("ws-test-mem")))
}

func TestOpsMetrics_ActiveSessionsGauge_RecordsLatestValue(t *testing.T) {
	pkgOpsMetrics.SetActiveSessions("ws-test-sess", 3)
	assert.Equal(t, 3.0, testutil.ToFloat64(pkgOpsMetrics.activeSessions.WithLabelValues("ws-test-sess")))

	pkgOpsMetrics.SetActiveSessions("ws-test-sess", 1)
	assert.Equal(t, 1.0, testutil.ToFloat64(pkgOpsMetrics.activeSessions.WithLabelValues("ws-test-sess")))
}

func TestOpsMetrics_ContextTokensGauge_RecordsLatestValue(t *testing.T) {
	pkgOpsMetrics.SetContextTokens("ws-test-tok", 50000)
	assert.Equal(t, 50000.0, testutil.ToFloat64(pkgOpsMetrics.contextTokens.WithLabelValues("ws-test-tok")))

	pkgOpsMetrics.SetContextTokens("ws-test-tok", 120000)
	assert.Equal(t, 120000.0, testutil.ToFloat64(pkgOpsMetrics.contextTokens.WithLabelValues("ws-test-tok")))
}

func TestOpsMetrics_UpdateFromTracker_SetsGauges(t *testing.T) {
	tracker := newSessionStatusTracker()
	tracker.set("ses_trk1", "busy")
	tracker.set("ses_trk2", "busy")
	tracker.set("ses_trk3", "idle")
	tracker.setPromptTokens("ses_trk1", 30000)
	tracker.setPromptTokens("ses_trk2", 20000)
	tracker.setPromptTokens("ses_trk3", 10000)

	pkgOpsMetrics.UpdateFromTracker("ws-test-trk", tracker)

	assert.Equal(t, 2.0, testutil.ToFloat64(pkgOpsMetrics.activeSessions.WithLabelValues("ws-test-trk")),
		"active sessions = busy count")
	assert.Equal(t, 60000.0, testutil.ToFloat64(pkgOpsMetrics.contextTokens.WithLabelValues("ws-test-trk")),
		"context tokens = sum of all session prompt tokens")
}

func TestOpsMetrics_UpdateFromTracker_NilTracker_NoPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		pkgOpsMetrics.UpdateFromTracker("ws-test-nil", nil)
	})
}
