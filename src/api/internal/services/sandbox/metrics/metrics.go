package metrics

import (
	"fmt"
	"time"
	
	"github.com/prometheus/client_golang/prometheus"
)

// MetricsRecorder defines the interface for recording metrics
type MetricsRecorder interface {
	RecordSandboxCreation(runtime string, warmPodUsed bool)
	RecordSandboxTermination(runtime string)
	RecordOperationDuration(operation string, duration time.Duration)
}

// prometheusRecorder implements the MetricsRecorder interface using Prometheus
type prometheusRecorder struct {
	sandboxCreations  *prometheus.CounterVec
	sandboxDurations  *prometheus.HistogramVec
	warmPoolHits      prometheus.Counter
}

// NewPrometheusRecorder creates a new Prometheus metrics recorder
func NewPrometheusRecorder() MetricsRecorder {
	return &prometheusRecorder{
		sandboxCreations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sandbox_creations_total",
			Help: "Total number of sandboxes created",
		}, []string{"runtime", "warm_pool"}),
		
		sandboxDurations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sandbox_operation_duration_seconds",
			Help:    "Duration of sandbox operations",
			Buckets: prometheus.DefBuckets,
		}, []string{"operation"}),
		
		warmPoolHits: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "warm_pool_hits_total",
			Help: "Total number of warm pool hits",
		}),
	}
}

// RecordSandboxCreation records a sandbox creation event
func (r *prometheusRecorder) RecordSandboxCreation(runtime string, warmPodUsed bool) {
	labels := prometheus.Labels{
		"runtime":   runtime,
		"warm_pool": fmt.Sprintf("%t", warmPodUsed),
	}
	r.sandboxCreations.With(labels).Inc()
	
	if warmPodUsed {
		r.warmPoolHits.Inc()
	}
}

// RecordSandboxTermination records a sandbox termination event
func (r *prometheusRecorder) RecordSandboxTermination(runtime string) {
	// This could be expanded to track terminations by runtime or other attributes
}

// RecordOperationDuration records the duration of a sandbox operation
func (r *prometheusRecorder) RecordOperationDuration(operation string, duration time.Duration) {
	r.sandboxDurations.WithLabelValues(operation).Observe(duration.Seconds())
}
