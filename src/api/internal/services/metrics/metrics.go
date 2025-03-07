package metrics

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Service manages application metrics
type Service struct {
	requestCounter     *prometheus.CounterVec
	requestDuration    *prometheus.HistogramVec
	responseSize       *prometheus.HistogramVec
	activeConnections  *prometheus.GaugeVec
	warmPoolHitRatio   *prometheus.GaugeVec
	sandboxesCreated   *prometheus.CounterVec
	sandboxesTerminated *prometheus.CounterVec
	executionsTotal    *prometheus.CounterVec
	executionDuration  *prometheus.HistogramVec
}

// New creates a new metrics service
func New() *Service {
	requestCounter := promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "api_requests_total",
			Help: "Total number of API requests",
		},
		[]string{"method", "endpoint", "status"},
	)

	requestDuration := promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "api_request_duration_seconds",
			Help:    "API request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "endpoint"},
	)

	responseSize := promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "api_response_size_bytes",
			Help:    "API response size in bytes",
			Buckets: prometheus.ExponentialBuckets(100, 10, 8),
		},
		[]string{"method", "endpoint"},
	)

	activeConnections := promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "api_active_connections",
			Help: "Number of active connections",
		},
		[]string{"type"},
	)

	warmPoolHitRatio := promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "warm_pool_hit_ratio",
			Help: "Ratio of sandbox creations that used a warm pod",
		},
		[]string{"runtime"},
	)

	sandboxesCreated := promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sandboxes_created_total",
			Help: "Total number of sandboxes created",
		},
		[]string{"runtime", "warm_pod_used"},
	)

	sandboxesTerminated := promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sandboxes_terminated_total",
			Help: "Total number of sandboxes terminated",
		},
		[]string{"runtime"},
	)

	executionsTotal := promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "executions_total",
			Help: "Total number of code/command executions",
		},
		[]string{"type", "runtime", "status"},
	)

	executionDuration := promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "execution_duration_seconds",
			Help:    "Execution duration in seconds",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 10),
		},
		[]string{"type", "runtime"},
	)

	return &Service{
		requestCounter:      requestCounter,
		requestDuration:     requestDuration,
		responseSize:        responseSize,
		activeConnections:   activeConnections,
		warmPoolHitRatio:    warmPoolHitRatio,
		sandboxesCreated:    sandboxesCreated,
		sandboxesTerminated: sandboxesTerminated,
		executionsTotal:     executionsTotal,
		executionDuration:   executionDuration,
	}
}

// RecordRequest records metrics for an API request
func (s *Service) RecordRequest(method, endpoint string, status int, duration time.Duration, size int) {
	s.requestCounter.WithLabelValues(method, endpoint, fmt.Sprintf("%d", status)).Inc()
	s.requestDuration.WithLabelValues(method, endpoint).Observe(duration.Seconds())
	s.responseSize.WithLabelValues(method, endpoint).Observe(float64(size))
}

// RecordSandboxCreation records metrics for sandbox creation
func (s *Service) RecordSandboxCreation(runtime string, warmPodUsed bool) {
	s.sandboxesCreated.WithLabelValues(runtime, fmt.Sprintf("%t", warmPodUsed)).Inc()
}

// RecordSandboxTermination records metrics for sandbox termination
func (s *Service) RecordSandboxTermination(runtime string) {
	s.sandboxesTerminated.WithLabelValues(runtime).Inc()
}

// RecordExecution records metrics for code/command execution
func (s *Service) RecordExecution(execType, runtime, status string, duration time.Duration) {
	s.executionsTotal.WithLabelValues(execType, runtime, status).Inc()
	s.executionDuration.WithLabelValues(execType, runtime).Observe(duration.Seconds())
}

// UpdateWarmPoolHitRatio updates the warm pool hit ratio
func (s *Service) UpdateWarmPoolHitRatio(runtime string, ratio float64) {
	s.warmPoolHitRatio.WithLabelValues(runtime).Set(ratio)
}

// IncrementActiveConnections increments the active connections counter
func (s *Service) IncrementActiveConnections(connType string) {
	s.activeConnections.WithLabelValues(connType).Inc()
}

// IncActiveConnections increments the active connections counter
func (s *Service) IncActiveConnections() {
	s.activeConnections.WithLabelValues("ws").Inc()
}

// DecrementActiveConnections decrements the active connections counter
func (s *Service) DecrementActiveConnections(connType string) {
	s.activeConnections.WithLabelValues(connType).Dec()
}

// DecActiveConnections decrements the active connections counter
func (s *Service) DecActiveConnections() {
	s.activeConnections.WithLabelValues("ws").Dec()
}
