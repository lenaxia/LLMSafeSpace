// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"

	"github.com/lenaxia/llmsafespace/controller/internal/metrics"
)

// ─── Helpers for isolated testing ───────────────────────────────────────────

func newTestGauge(name string) prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{Name: name})
}

func newTestGaugeVec(name, help string, labels ...string) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help}, labels)
}

func newTestCounterVec(name, help string, labels ...string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
}

func gaugeValue(g prometheus.Gauge) float64 {
	m := &dto.Metric{}
	_ = g.(prometheus.Metric).Write(m)
	if m.Gauge != nil {
		return m.Gauge.GetValue()
	}
	return -1
}

func gaugeVecValue(gv *prometheus.GaugeVec, labels ...string) float64 {
	g, _ := gv.GetMetricWithLabelValues(labels...)
	return gaugeValue(g)
}

func counterVecValue(cv *prometheus.CounterVec, labels ...string) float64 {
	c, _ := cv.GetMetricWithLabelValues(labels...)
	m := &dto.Metric{}
	_ = c.(prometheus.Metric).Write(m)
	if m.Counter != nil {
		return m.Counter.GetValue()
	}
	return -1
}

// ─── Isolated unit tests using fresh collectors ─────────────────────────────

func TestSetRelayHealthyReplicas_SetsValue(t *testing.T) {
	g := newTestGauge("test_healthy")
	setRelayHealthyReplicasInto(g, 3)
	assert.Equal(t, 3.0, gaugeValue(g))

	setRelayHealthyReplicasInto(g, 0)
	assert.Equal(t, 0.0, gaugeValue(g))
}

func TestSetRelayProvisioningFailed_TogglesGauge(t *testing.T) {
	gv := newTestGaugeVec("test_prov_failed", "test", "provider")
	setRelayProvisioningFailedInto(gv, "oci", true)
	assert.Equal(t, 1.0, gaugeVecValue(gv, "oci"))

	setRelayProvisioningFailedInto(gv, "oci", false)
	assert.Equal(t, 0.0, gaugeVecValue(gv, "oci"))
}

func TestSetRelayDraining_TogglesGauge(t *testing.T) {
	gv := newTestGaugeVec("test_draining", "test", "provider")
	setRelayDrainingInto(gv, "aws", true)
	assert.Equal(t, 1.0, gaugeVecValue(gv, "aws"))

	setRelayDrainingInto(gv, "aws", false)
	assert.Equal(t, 0.0, gaugeVecValue(gv, "aws"))
}

func TestSetRelayQuotaExhausted_TogglesGauge(t *testing.T) {
	gv := newTestGaugeVec("test_quota", "test", "provider")
	setRelayQuotaExhaustedInto(gv, "gcp", true)
	assert.Equal(t, 1.0, gaugeVecValue(gv, "gcp"))

	setRelayQuotaExhaustedInto(gv, "gcp", false)
	assert.Equal(t, 0.0, gaugeVecValue(gv, "gcp"))
}

func TestRecordRotation_AccumulatesCounter(t *testing.T) {
	cv := newTestCounterVec("test_rotation", "test", "provider", "reason")
	recordRotationInto(cv, "oci", "manual")
	recordRotationInto(cv, "oci", "manual")
	assert.Equal(t, 2.0, counterVecValue(cv, "oci", "manual"))

	recordRotationInto(cv, "aws", "429")
	assert.Equal(t, 1.0, counterVecValue(cv, "aws", "429"))
	assert.Equal(t, 2.0, counterVecValue(cv, "oci", "manual"), "oci counter should be unaffected")
}

func TestObserveProvisionDuration_RecordsHistogram(t *testing.T) {
	hv := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "test_provision_seconds",
	}, []string{"provider"})
	observeProvisionDurationInto(hv, "oci", 42.5)

	hist, _ := hv.GetMetricWithLabelValues("oci")
	m := &dto.Metric{}
	_ = hist.(prometheus.Metric).Write(m)
	assert.NotNil(t, m.Histogram)
	assert.Equal(t, uint64(1), m.Histogram.GetSampleCount())
	assert.InDelta(t, 42.5, m.Histogram.GetSampleSum(), 0.01)
}

// ─── Integration test: verify global metrics are registered ─────────────────

func TestMetricsRegisteredInCollectors(t *testing.T) {
	// Verify all relay metrics are in collectors() (they must be to appear at /metrics)
	allCollectors := metrics.AllCollectors()
	names := make(map[string]bool)
	for _, c := range allCollectors {
		// Each collector can describe itself
		descCh := make(chan *prometheus.Desc, 10)
		c.Describe(descCh)
		close(descCh)
		for desc := range descCh {
			names[desc.String()] = true
		}
	}

	expectedMetrics := []string{
		"llmsafespace_relay_healthy_replicas",
		"llmsafespace_relay_provisioning_failed",
		"llmsafespace_relay_draining",
		"llmsafespace_relay_quota_exhausted",
		"llmsafespace_relay_provision_duration_seconds",
		"llmsafespace_relay_rotation_total",
	}

	for _, name := range expectedMetrics {
		found := false
		for k := range names {
			if containsSubstring(k, name) {
				found = true
				break
			}
		}
		assert.True(t, found, "metric %q should be registered in collectors", name)
	}
}

func containsSubstring(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		(len(haystack) > 0 && len(needle) > 0 &&
			(indexOf(haystack, needle) >= 0)))
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
