package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// Sandbox metrics
	SandboxesCreatedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "llmsafespace_sandboxes_created_total",
			Help: "Total number of sandboxes created",
		},
	)

	SandboxesDeletedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "llmsafespace_sandboxes_deleted_total",
			Help: "Total number of sandboxes deleted",
		},
	)

	SandboxesFailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_sandboxes_failed_total",
			Help: "Total number of sandboxes that failed to create",
		},
		[]string{"reason"},
	)

	SandboxStartupDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "llmsafespace_sandbox_startup_duration_seconds",
			Help:    "Time taken for a sandbox to start up",
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
	prometheus.MustRegister(SandboxesCreatedTotal)
	prometheus.MustRegister(SandboxesDeletedTotal)
	prometheus.MustRegister(SandboxesFailedTotal)
	prometheus.MustRegister(SandboxStartupDurationSeconds)
	prometheus.MustRegister(ReconciliationDurationSeconds)
	prometheus.MustRegister(ReconciliationErrorsTotal)
}
