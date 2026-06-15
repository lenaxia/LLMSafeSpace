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

func registerTestMetrics(t *testing.T) prometheus.Gauge {
	t.Helper()
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_relay_gauge"})
	return g
}

func getGaugeValue(g prometheus.Gauge) float64 {
	m := &dto.Metric{}
	_ = g.(prometheus.Metric).Write(m)
	if m.Gauge != nil {
		return m.Gauge.GetValue()
	}
	return -1
}

func TestSetRelayHealthyReplicas(t *testing.T) {
	// Register metrics in a test registry to avoid global state pollution
	registry := prometheus.NewRegistry()
	_ = metrics.RegisterWith(registry)

	// The package-level gauge is already registered globally; calling
	// the function should not panic and should set the value.
	setRelayHealthyReplicas(3)

	m := &dto.Metric{}
	_ = metrics.RelayHealthyReplicas.Write(m)
	assert.Equal(t, float64(3), m.Gauge.GetValue())

	setRelayHealthyReplicas(0)
	_ = metrics.RelayHealthyReplicas.Write(m)
	assert.Equal(t, float64(0), m.Gauge.GetValue())
}

func TestSetRelayProvisioningFailed(t *testing.T) {
	setRelayProvisioningFailed("oci", true)
	m := &dto.Metric{}
	gauge, err := metrics.RelayProvisioningFailed.GetMetricWithLabelValues("oci")
	assert.NoError(t, err)
	_ = gauge.Write(m)
	assert.Equal(t, float64(1), m.Gauge.GetValue())

	setRelayProvisioningFailed("oci", false)
	_ = gauge.Write(m)
	assert.Equal(t, float64(0), m.Gauge.GetValue())
}

func TestSetRelayDraining(t *testing.T) {
	setRelayDraining("aws", true)
	gauge, err := metrics.RelayDraining.GetMetricWithLabelValues("aws")
	assert.NoError(t, err)
	m := &dto.Metric{}
	_ = gauge.Write(m)
	assert.Equal(t, float64(1), m.Gauge.GetValue())

	setRelayDraining("aws", false)
	_ = gauge.Write(m)
	assert.Equal(t, float64(0), m.Gauge.GetValue())
}

func TestSetRelayQuotaExhausted(t *testing.T) {
	setRelayQuotaExhausted("gcp", true)
	gauge, err := metrics.RelayQuotaExhausted.GetMetricWithLabelValues("gcp")
	assert.NoError(t, err)
	m := &dto.Metric{}
	_ = gauge.Write(m)
	assert.Equal(t, float64(1), m.Gauge.GetValue())
}

func TestRecordRotation(t *testing.T) {
	recordRotation("oci", "manual")
	recordRotation("oci", "manual")
	counter, err := metrics.RelayRotationTotal.GetMetricWithLabelValues("oci", "manual")
	assert.NoError(t, err)
	m := &dto.Metric{}
	_ = counter.Write(m)
	assert.Equal(t, float64(2), m.Counter.GetValue())

	recordRotation("aws", "429")
	counter2, err := metrics.RelayRotationTotal.GetMetricWithLabelValues("aws", "429")
	assert.NoError(t, err)
	_ = counter2.Write(m)
	assert.Equal(t, float64(1), m.Counter.GetValue())
}

func TestObserveProvisionDuration(t *testing.T) {
	observeProvisionDuration("oci", 42.5)
	hist, err := metrics.RelayProvisionDurationSeconds.GetMetricWithLabelValues("oci")
	assert.NoError(t, err)
	m := &dto.Metric{}
	_ = hist.(prometheus.Metric).Write(m)
	assert.NotNil(t, m.Histogram)
	assert.Equal(t, uint64(1), m.Histogram.GetSampleCount())
	assert.InDelta(t, 42.5, m.Histogram.GetSampleSum(), 0.01)
}

func TestGaugeValueHelper(t *testing.T) {
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test"})
	g.Set(99.0)
	assert.Equal(t, 99.0, getGaugeValue(g))
}
