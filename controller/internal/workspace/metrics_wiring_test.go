// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// ---- test metric constructors ----
// Each test gets isolated prometheus instances so observations from one test
// cannot bleed into another. Never use the package-level metrics vars here.

func newTestCounterVec(name string, labels []string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{Name: name}, labels)
}

func newTestGaugeVec(name string, labels []string) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name}, labels)
}

func newTestHistogramVec(name string, labels []string) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Name: name, Buckets: []float64{1, 5, 10, 60}},
		labels,
	)
}

// ---- gathering helpers ----

func counterValue(t *testing.T, c *prometheus.CounterVec, lv ...string) float64 {
	t.Helper()
	m := &dto.Metric{}
	require.NoError(t, c.WithLabelValues(lv...).Write(m))
	return m.GetCounter().GetValue()
}

func gaugeVecValue(t *testing.T, g *prometheus.GaugeVec, lv ...string) float64 {
	t.Helper()
	m := &dto.Metric{}
	require.NoError(t, g.WithLabelValues(lv...).Write(m))
	return m.GetGauge().GetValue()
}

func histCount(t *testing.T, h *prometheus.HistogramVec, lv ...string) uint64 {
	t.Helper()
	m := &dto.Metric{}
	require.NoError(t, h.WithLabelValues(lv...).(prometheus.Histogram).Write(m))
	return m.GetHistogram().GetSampleCount()
}

// ---- ReconciliationDuration ----

func TestReconcileDurationObservedOnSuccess(t *testing.T) {
	hist := newTestHistogramVec("test_reconcile_dur_ok", []string{"resource", "status"})
	observeReconcileDurationInto(hist, "Workspace", "ok", 10*time.Millisecond)
	assert.Equal(t, uint64(1), histCount(t, hist, "Workspace", "ok"))
}

func TestReconcileDurationObservedOnError(t *testing.T) {
	hist := newTestHistogramVec("test_reconcile_dur_err", []string{"resource", "status"})
	observeReconcileDurationInto(hist, "Workspace", "error", 5*time.Millisecond)
	assert.Equal(t, uint64(1), histCount(t, hist, "Workspace", "error"))
}

// ---- ReconciliationErrors ----

func TestReconcileErrorCounted(t *testing.T) {
	ctr := newTestCounterVec("test_reconcile_errs", []string{"resource", "error_type"})
	countReconcileErrorInto(ctr, "Workspace", "get_failed")
	assert.Equal(t, float64(1), counterValue(t, ctr, "Workspace", "get_failed"))
}

// ---- WorkspacesDeleted ----

func TestWorkspacesDeletedIncremented(t *testing.T) {
	ctr := newTestCounterVec("test_ws_deleted", []string{"runtime", "security_level"})
	ws := &v1.Workspace{}
	ws.Spec.Runtime = "python"
	ws.Spec.SecurityLevel = "standard"
	incrementWorkspacesDeletedInto(ctr, ws)
	assert.Equal(t, float64(1), counterValue(t, ctr, "python", "standard"))
}

// ---- enterRecovery metric helpers ----

func TestRecordRecoveryMetrics_IncrementsAttempts(t *testing.T) {
	attempts := newTestCounterVec("test_rec_attempts_a", []string{"failure_class"})
	backoffHist := newTestHistogramVec("test_rec_backoff_a", []string{"failure_class"})
	safeModeGauge := newTestGaugeVec("test_safe_mode_a", []string{"workspace_id"})
	failedCtr := newTestCounterVec("test_ws_failed_a", []string{"reason"})

	ws := &v1.Workspace{}
	ws.UID = "ws-uid-a"
	ws.Status.ConsecutiveFailures = 1
	ws.Status.SafeMode = false
	now := metav1.Now()
	ws.Status.NextRetryAt = nil
	ws.Status.LastFailureAt = &now

	recordRecoveryMetricsInto(ws, FailureClassProcess, attempts, backoffHist, safeModeGauge, failedCtr)

	assert.Equal(t, float64(1), counterValue(t, attempts, string(FailureClassProcess)))
}

func TestRecordRecoveryMetrics_RecordsBackoff(t *testing.T) {
	attempts := newTestCounterVec("test_rec_attempts_b", []string{"failure_class"})
	backoffHist := newTestHistogramVec("test_rec_backoff_b", []string{"failure_class"})
	safeModeGauge := newTestGaugeVec("test_safe_mode_b", []string{"workspace_id"})
	failedCtr := newTestCounterVec("test_ws_failed_b", []string{"reason"})

	ws := &v1.Workspace{}
	ws.UID = "ws-uid-b"
	ws.Status.ConsecutiveFailures = 2
	ws.Status.SafeMode = false
	next := metav1.NewTime(time.Now().Add(10 * time.Second))
	ws.Status.NextRetryAt = &next

	recordRecoveryMetricsInto(ws, FailureClassInfrastructure, attempts, backoffHist, safeModeGauge, failedCtr)

	assert.Equal(t, uint64(1), histCount(t, backoffHist, string(FailureClassInfrastructure)))
}

func TestRecordRecoveryMetrics_SafeMode_SetsGauge(t *testing.T) {
	attempts := newTestCounterVec("test_rec_attempts_c", []string{"failure_class"})
	backoffHist := newTestHistogramVec("test_rec_backoff_c", []string{"failure_class"})
	safeModeGauge := newTestGaugeVec("test_safe_mode_c", []string{"workspace_id"})
	failedCtr := newTestCounterVec("test_ws_failed_c", []string{"reason"})

	ws := &v1.Workspace{}
	ws.UID = "ws-uid-c"
	ws.Status.ConsecutiveFailures = 6
	ws.Status.SafeMode = true

	recordRecoveryMetricsInto(ws, FailureClassProcess, attempts, backoffHist, safeModeGauge, failedCtr)

	assert.Equal(t, float64(1), gaugeVecValue(t, safeModeGauge, "ws-uid-c"))
	assert.Equal(t, float64(1), counterValue(t, failedCtr, string(FailureClassProcess)))
}

func TestRecordRecoveryMetrics_NoSafeMode_GaugeZero(t *testing.T) {
	attempts := newTestCounterVec("test_rec_attempts_d", []string{"failure_class"})
	backoffHist := newTestHistogramVec("test_rec_backoff_d", []string{"failure_class"})
	safeModeGauge := newTestGaugeVec("test_safe_mode_d", []string{"workspace_id"})
	failedCtr := newTestCounterVec("test_ws_failed_d", []string{"reason"})

	ws := &v1.Workspace{}
	ws.UID = "ws-uid-d"
	ws.Status.ConsecutiveFailures = 1
	ws.Status.SafeMode = false

	safeModeGauge.WithLabelValues("ws-uid-d").Set(1)

	recordRecoveryMetricsInto(ws, FailureClassProcess, attempts, backoffHist, safeModeGauge, failedCtr)

	m := &dto.Metric{}
	err := safeModeGauge.WithLabelValues("ws-uid-d").Write(m)
	if err == nil {
		assert.Equal(t, float64(0), m.GetGauge().GetValue())
	}
}

// ---- WorkspaceActiveSeconds ----

func TestAccumulateActiveSeconds_NormalCase(t *testing.T) {
	wsActive := newTestCounterVec("test_ws_act_secs_1", []string{"workspace_id", "user_id", "runtime", "security_level"})
	userActive := newTestCounterVec("test_usr_act_secs_1", []string{"user_id", "runtime", "security_level"})

	ws := &v1.Workspace{}
	ws.Name = "ws-abc"
	ws.Labels = map[string]string{"user-id": "user-1"}
	ws.Spec.Runtime = "python"
	ws.Spec.SecurityLevel = "standard"
	now := metav1.Now()
	ws.Status.StartTime = &now

	accumulateActiveSecondsInto(ws, 30*time.Second, wsActive, userActive)

	assert.InDelta(t, 30.0, counterValue(t, wsActive, "ws-abc", "user-1", "python", "standard"), 0.01)
	assert.InDelta(t, 30.0, counterValue(t, userActive, "user-1", "python", "standard"), 0.01)
}

func TestAccumulateActiveSeconds_ZeroElapsed_NoOp(t *testing.T) {
	wsActive := newTestCounterVec("test_ws_act_secs_2", []string{"workspace_id", "user_id", "runtime", "security_level"})
	userActive := newTestCounterVec("test_usr_act_secs_2", []string{"user_id", "runtime", "security_level"})

	ws := &v1.Workspace{}
	ws.Name = "ws-xyz"
	ws.Labels = map[string]string{"user-id": "u2"}
	ws.Spec.Runtime = "go"
	ws.Spec.SecurityLevel = "standard"
	now := metav1.Now()
	ws.Status.StartTime = &now

	accumulateActiveSecondsInto(ws, 0, wsActive, userActive)
	// Zero elapsed → no observation made, counter stays zero.
	assert.Equal(t, float64(0), counterValue(t, wsActive, "ws-xyz", "u2", "go", "standard"))
}

func TestAccumulateActiveSeconds_NoStartTime_NoOp(t *testing.T) {
	wsActive := newTestCounterVec("test_ws_act_secs_3", []string{"workspace_id", "user_id", "runtime", "security_level"})
	userActive := newTestCounterVec("test_usr_act_secs_3", []string{"user_id", "runtime", "security_level"})

	ws := &v1.Workspace{} // no StartTime
	ws.Name = "ws-nostarttime"
	ws.Labels = map[string]string{"user-id": "u3"}
	ws.Spec.Runtime = "python"
	ws.Spec.SecurityLevel = "standard"

	accumulateActiveSecondsInto(ws, 10*time.Second, wsActive, userActive)
	assert.Equal(t, float64(0), counterValue(t, wsActive, "ws-nostarttime", "u3", "python", "standard"))
}

// ---- WorkspaceStorageBytes ----

func TestSetStorageBytes_ParsesGiB(t *testing.T) {
	storageVec := newTestGaugeVec("test_storage_bytes_1", []string{"workspace_id", "user_id"})
	ws := &v1.Workspace{}
	ws.Name = "ws-st"
	ws.Labels = map[string]string{"user-id": "u4"}
	ws.Spec.Storage.Size = "10Gi"

	setStorageBytesInto(ws, storageVec)

	expected := float64(10 * 1024 * 1024 * 1024)
	assert.InDelta(t, expected, gaugeVecValue(t, storageVec, "ws-st", "u4"), 1.0)
}

func TestSetStorageBytes_ParsesMiB(t *testing.T) {
	storageVec := newTestGaugeVec("test_storage_bytes_2", []string{"workspace_id", "user_id"})
	ws := &v1.Workspace{}
	ws.Name = "ws-small"
	ws.Labels = map[string]string{"user-id": "u5"}
	ws.Spec.Storage.Size = "512Mi"

	setStorageBytesInto(ws, storageVec)

	expected := float64(512 * 1024 * 1024)
	assert.InDelta(t, expected, gaugeVecValue(t, storageVec, "ws-small", "u5"), 1.0)
}

func TestSetStorageBytes_EmptySize_NoOp(t *testing.T) {
	storageVec := newTestGaugeVec("test_storage_bytes_3", []string{"workspace_id", "user_id"})
	ws := &v1.Workspace{}
	ws.Name = "ws-empty"
	ws.Labels = map[string]string{"user-id": "u6"}
	ws.Spec.Storage.Size = ""

	// Must not panic.
	setStorageBytesInto(ws, storageVec)
	assert.Equal(t, float64(0), gaugeVecValue(t, storageVec, "ws-empty", "u6"))
}

func TestSetStorageBytes_InvalidSize_NoOp(t *testing.T) {
	storageVec := newTestGaugeVec("test_storage_bytes_4", []string{"workspace_id", "user_id"})
	ws := &v1.Workspace{}
	ws.Name = "ws-bad"
	ws.Labels = map[string]string{"user-id": "u7"}
	ws.Spec.Storage.Size = "not-a-size"

	// Must not panic.
	setStorageBytesInto(ws, storageVec)
	assert.Equal(t, float64(0), gaugeVecValue(t, storageVec, "ws-bad", "u7"))
}
