// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// startupBuckets covers the full observed range of workspace startup latency:
// fast-path cache hits (~1s) through pathological cold starts (5min).
var startupBuckets = []float64{1, 3, 5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 300}

var (
	// ---- Workspace lifecycle counters ----

	// WorkspacesCreatedTotal counts workspace creations, labeled by runtime
	// and security_level. Replaces the former no-label WorkspaceesCreatedTotal
	// and the duplicate in common/metrics.go (now deleted).
	WorkspacesCreatedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_workspaces_created_total",
			Help: "Total number of workspaces created",
		},
		[]string{"runtime", "security_level"},
	)

	// WorkspacesDeletedTotal counts workspace deletions.
	WorkspacesDeletedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_workspaces_deleted_total",
			Help: "Total number of workspaces deleted",
		},
		[]string{"runtime", "security_level"},
	)

	// WorkspacesRunning is a live gauge of workspaces in Active phase.
	WorkspacesRunning = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "llmsafespace_workspaces_running",
			Help: "Number of workspaces currently in Active phase",
		},
		[]string{"runtime", "security_level"},
	)

	// WorkspacesFailedTotal counts workspaces that entered Failed phase.
	WorkspacesFailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_workspaces_failed_total",
			Help: "Total number of workspaces that entered Failed phase",
		},
		[]string{"reason"},
	)

	// ---- Startup / resume latency histograms ----

	// WorkspaceCreateDurationSeconds measures end-to-end first-create latency.
	// The anchor is the llmsafespace.dev/requested-at annotation written by the
	// API at POST /workspaces time; falls back to controller-first-reconcile if
	// the annotation is absent.
	//
	// Labels:
	//   has_packages    — "true" if spec.packages is non-empty
	//   has_init_script — "true" if spec.initScript is non-empty
	WorkspaceCreateDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "llmsafespace_workspace_create_duration_seconds",
			Help:    "Wall-clock time from workspace creation request to phase=Active",
			Buckets: startupBuckets,
		},
		[]string{"has_packages", "has_init_script"},
	)

	// WorkspaceResumeDurationSeconds measures end-to-end resume latency from
	// Resuming phase entry to Active phase.
	//
	// Labels:
	//   resume_type — "first_resume" (RestartCount==0) | "subsequent_resume"
	WorkspaceResumeDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "llmsafespace_workspace_resume_duration_seconds",
			Help:    "Wall-clock time from Resuming phase entry to phase=Active",
			Buckets: startupBuckets,
		},
		[]string{"resume_type"},
	)

	// WorkspaceInitContainerDurationSeconds measures time spent in the
	// workspace-setup init container (package installs + initScript).
	// Derived from pod initContainerStatuses[workspace-setup].startedAt /
	// finishedAt timestamps. Only emitted when the init container ran.
	WorkspaceInitContainerDurationSeconds = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "llmsafespace_workspace_init_container_duration_seconds",
			Help:    "Time spent in workspace-setup init container (package install + initScript)",
			Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60, 120, 300},
		},
	)

	// ---- Controller internals ----

	// ReconciliationDurationSeconds measures per-reconcile wall-clock time.
	ReconciliationDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "llmsafespace_reconciliation_duration_seconds",
			Help:    "Duration of workspace reconciliation loops",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"resource", "status"},
	)

	// ReconciliationErrorsTotal counts reconciliation errors by type.
	ReconciliationErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_reconciliation_errors_total",
			Help: "Total number of reconciliation errors",
		},
		[]string{"resource", "error_type"},
	)
)

// collectors returns every Collector defined in this package.
// Both SetupMetrics and RegisterWith use this list; adding a new metric
// here is the only change required to have it registered everywhere.
func collectors() []prometheus.Collector {
	return []prometheus.Collector{
		WorkspacesCreatedTotal,
		WorkspacesDeletedTotal,
		WorkspacesRunning,
		WorkspacesFailedTotal,
		WorkspaceCreateDurationSeconds,
		WorkspaceResumeDurationSeconds,
		WorkspaceInitContainerDurationSeconds,
		ReconciliationDurationSeconds,
		ReconciliationErrorsTotal,
	}
}

// SetupMetrics registers all metrics with the default Prometheus registry.
// Called once from controller main(). The controller-runtime manager's
// metrics server serves these alongside its own metrics at /metrics.
//
// Do NOT start a separate HTTP server here: doing so would race with
// controller-runtime's metrics server for the same port and cause
// "bind: address already in use" panics.
func SetupMetrics() {
	for _, c := range collectors() {
		prometheus.MustRegister(c)
	}
}

// RegisterWith registers all metrics with the supplied registry.
// Used by tests that need an isolated registry to avoid cross-test
// pollution from the global default registry.
func RegisterWith(reg prometheus.Registerer) error {
	for _, c := range collectors() {
		if err := reg.Register(c); err != nil {
			return err
		}
	}
	return nil
}
