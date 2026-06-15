// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lenaxia/llmsafespace/controller/internal/metrics"
)

// setRelayHealthyReplicas sets the gauge for the current count of healthy relays.
func setRelayHealthyReplicas(n int) {
	metrics.RelayHealthyReplicas.Set(float64(n))
}

// setRelayProvisioningFailed sets the circuit-breaker gauge for a provider.
func setRelayProvisioningFailed(provider string, tripped bool) {
	val := 0.0
	if tripped {
		val = 1.0
	}
	metrics.RelayProvisioningFailed.WithLabelValues(provider).Set(val)
}

// setRelayDraining sets the drain state gauge for a provider.
func setRelayDraining(provider string, draining bool) {
	val := 0.0
	if draining {
		val = 1.0
	}
	metrics.RelayDraining.WithLabelValues(provider).Set(val)
}

// setRelayQuotaExhausted sets the egress quota exhausted gauge for a provider.
func setRelayQuotaExhausted(provider string, exhausted bool) {
	val := 0.0
	if exhausted {
		val = 1.0
	}
	metrics.RelayQuotaExhausted.WithLabelValues(provider).Set(val)
}

// recordRotation increments the rotation counter.
func recordRotation(provider, reason string) {
	metrics.RelayRotationTotal.WithLabelValues(provider, reason).Inc()
}

// observeProvisionDuration records provisioning time for a provider.
func observeProvisionDuration(provider string, seconds float64) {
	metrics.RelayProvisionDurationSeconds.WithLabelValues(provider).Observe(seconds)
}

// ─── Into variants for isolated unit testing ────────────────────────────────

func setRelayHealthyReplicasInto(g prometheus.Gauge, n int) {
	g.Set(float64(n))
}

func setRelayProvisioningFailedInto(gv *prometheus.GaugeVec, provider string, tripped bool) {
	val := 0.0
	if tripped {
		val = 1.0
	}
	gv.WithLabelValues(provider).Set(val)
}

func setRelayDrainingInto(gv *prometheus.GaugeVec, provider string, draining bool) {
	val := 0.0
	if draining {
		val = 1.0
	}
	gv.WithLabelValues(provider).Set(val)
}

func setRelayQuotaExhaustedInto(gv *prometheus.GaugeVec, provider string, exhausted bool) {
	val := 0.0
	if exhausted {
		val = 1.0
	}
	gv.WithLabelValues(provider).Set(val)
}

func recordRotationInto(cv *prometheus.CounterVec, provider, reason string) {
	cv.WithLabelValues(provider, reason).Inc()
}

func observeProvisionDurationInto(hv *prometheus.HistogramVec, provider string, seconds float64) {
	hv.WithLabelValues(provider).Observe(seconds)
}
