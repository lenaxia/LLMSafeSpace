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
	sandboxCreations    *prometheus.CounterVec
	sandboxTerminations *prometheus.CounterVec
	operationDurations  *prometheus.HistogramVec
	warmPoolHits        prometheus.Counter
	warmPoolMisses      prometheus.Counter
}

// NewPrometheusRecorder creates a new Prometheus metrics recorder
func NewPrometheusRecorder() MetricsRecorder {
	recorder := &prometheusRecorder{
		sandboxCreations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sandbox_creations_total",
			Help: "Total number of sandboxes created",
		}, []string{"runtime", "warm_pod_used"}),
		
		sandboxTerminations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sandbox_terminations_total",
			Help: "Total number of sandboxes terminated",
		}, []string{"runtime"}),
		
		operationDurations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sandbox_operation_duration_seconds",
			Help:    "Duration of sandbox operations",
			Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60},
		}, []string{"operation"}),
		
		warmPoolHits: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "warm_pool_hits_total",
			Help: "Total number of warm pool hits",
		}),
		
		warmPoolMisses: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "warm_pool_misses_total",
			Help: "Total number of warm pool misses",
		}),
	}
	
	// Register metrics with Prometheus
	prometheus.MustRegister(recorder.sandboxCreations)
	prometheus.MustRegister(recorder.sandboxTerminations)
	prometheus.MustRegister(recorder.operationDurations)
	prometheus.MustRegister(recorder.warmPoolHits)
	prometheus.MustRegister(recorder.warmPoolMisses)
	
	return recorder
}

// RecordSandboxCreation records a sandbox creation event
func (r *prometheusRecorder) RecordSandboxCreation(runtime string, warmPodUsed bool) {
	labels := prometheus.Labels{
		"runtime":       runtime,
		"warm_pod_used": fmt.Sprintf("%t", warmPodUsed),
	}
	r.sandboxCreations.With(labels).Inc()
	
	if warmPodUsed {
		r.warmPoolHits.Inc()
	} else {
		r.warmPoolMisses.Inc()
	}
}

// RecordSandboxTermination records a sandbox termination event
func (r *prometheusRecorder) RecordSandboxTermination(runtime string) {
	r.sandboxTerminations.WithLabelValues(runtime).Inc()
}

// RecordOperationDuration records the duration of a sandbox operation
func (r *prometheusRecorder) RecordOperationDuration(operation string, duration time.Duration) {
	r.operationDurations.WithLabelValues(operation).Observe(duration.Seconds())
}

// noopRecorder implements the MetricsRecorder interface but does nothing
type noopRecorder struct{}

// NewNoopRecorder creates a new no-op metrics recorder
func NewNoopRecorder() MetricsRecorder {
	return &noopRecorder{}
}

// RecordSandboxCreation is a no-op implementation
func (r *noopRecorder) RecordSandboxCreation(runtime string, warmPodUsed bool) {}

// RecordSandboxTermination is a no-op implementation
func (r *noopRecorder) RecordSandboxTermination(runtime string) {}

// RecordOperationDuration is a no-op implementation
func (r *noopRecorder) RecordOperationDuration(operation string, duration time.Duration) {}
