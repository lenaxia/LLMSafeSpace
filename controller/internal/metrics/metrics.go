package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/klog/v2"
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

// SetupMetrics initializes and registers all metrics
func SetupMetrics() {
	prometheus.MustRegister(SandboxesCreatedTotal)
	prometheus.MustRegister(SandboxesDeletedTotal)
	prometheus.MustRegister(SandboxesFailedTotal)
	prometheus.MustRegister(SandboxStartupDurationSeconds)
	prometheus.MustRegister(ReconciliationDurationSeconds)
	prometheus.MustRegister(ReconciliationErrorsTotal)

	// Start metrics server
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		klog.Info("Starting metrics server on :8080")
		if err := http.ListenAndServe(":8080", nil); err != nil {
			klog.Errorf("Failed to start metrics server: %v", err)
		}
	}()
}
