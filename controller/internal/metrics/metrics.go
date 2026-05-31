// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// Workspace metrics
	WorkspaceesCreatedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "llmsafespace_workspacees_created_total",
			Help: "Total number of workspacees created",
		},
	)

	WorkspaceesDeletedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "llmsafespace_workspacees_deleted_total",
			Help: "Total number of workspacees deleted",
		},
	)

	WorkspaceesFailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_workspacees_failed_total",
			Help: "Total number of workspacees that failed to create",
		},
		[]string{"reason"},
	)

	WorkspaceStartupDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "llmsafespace_workspace_startup_duration_seconds",
			Help:    "Time taken for a workspace to start up",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
		},
		[]string{"runtime"},
	)

	// Controller metrics
	ReconciliationDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "llmsafespace_reconciliation_duration_seconds",
			Help:    "Duration of reconciliation in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"resource", "status"},
	)

	ReconciliationErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_reconciliation_errors_total",
			Help: "Total number of reconciliation errors",
		},
		[]string{"resource", "error_type"},
	)
)

// SetupMetrics registers all custom metrics with the default Prometheus
// registry. The controller-runtime manager's metrics server (configured via
// Metrics.BindAddress) automatically serves these alongside its own metrics
// at /metrics.
//
// Do NOT start a separate HTTP server here: doing so would race with
// controller-runtime's metrics server for the same port and cause "bind:
// address already in use" panics.
func SetupMetrics() {
	prometheus.MustRegister(WorkspaceesCreatedTotal)
	prometheus.MustRegister(WorkspaceesDeletedTotal)
	prometheus.MustRegister(WorkspaceesFailedTotal)
	prometheus.MustRegister(WorkspaceStartupDurationSeconds)
	prometheus.MustRegister(ReconciliationDurationSeconds)
	prometheus.MustRegister(ReconciliationErrorsTotal)
}
