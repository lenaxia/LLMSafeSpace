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
			Name: "llmsafespace_sandbox_startup_duration_seconds",
			Help: "Time taken for a sandbox to start up",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
		},
		[]string{"runtime", "warm_pod_used"},
	)
	
	// Warm pool metrics
	WarmPoolSizeGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "llmsafespace_warmpool_size",
			Help: "Current size of warm pools",
		},
		[]string{"pool", "runtime", "status"},
	)
	
	WarmPoolUtilizationGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "llmsafespace_warmpool_utilization",
			Help: "Utilization ratio of warm pools (assigned pods / total pods)",
		},
		[]string{"pool", "runtime"},
	)
	
	WarmPoolAssignmentDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "llmsafespace_warmpool_assignment_duration_seconds",
			Help: "Time taken to assign a warm pod to a sandbox",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
		},
		[]string{"pool", "runtime"},
	)
	
	WarmPoolRecycleTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_warmpool_recycle_total",
			Help: "Total number of warm pods recycled",
		},
		[]string{"pool", "runtime", "success"},
	)
	
	// Controller metrics
	ReconciliationDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "llmsafespace_reconciliation_duration_seconds",
			Help: "Duration of reconciliation in seconds",
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
	// Register sandbox metrics
	prometheus.MustRegister(SandboxesCreatedTotal)
	prometheus.MustRegister(SandboxesDeletedTotal)
	prometheus.MustRegister(SandboxesFailedTotal)
	prometheus.MustRegister(SandboxStartupDurationSeconds)
	
	// Register warm pool metrics
	prometheus.MustRegister(WarmPoolSizeGauge)
	prometheus.MustRegister(WarmPoolUtilizationGauge)
	prometheus.MustRegister(WarmPoolAssignmentDurationSeconds)
	prometheus.MustRegister(WarmPoolRecycleTotal)
	
	// Register controller metrics
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
