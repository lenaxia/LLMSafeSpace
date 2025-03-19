package metrics

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// Service manages application metrics
type Service struct {
	logger              *logger.Logger
	requestCounter      *prometheus.CounterVec
	requestDuration     *prometheus.HistogramVec
	responseSize       *prometheus.HistogramVec
	activeConnections  *prometheus.GaugeVec
	warmPoolHitRatio   *prometheus.GaugeVec
	sandboxesCreated   *prometheus.CounterVec
	sandboxesTerminated *prometheus.CounterVec
	executionsTotal    *prometheus.CounterVec
	executionDuration  *prometheus.HistogramVec
	errorsTotal        *prometheus.CounterVec
	packageInstalls    *prometheus.CounterVec
	fileOperations     *prometheus.CounterVec
	resourceUsage      *prometheus.GaugeVec
	warmPoolUtilization *prometheus.GaugeVec
	warmPoolScaling     *prometheus.CounterVec
}

// New creates a new metrics service
func New(logger *logger.Logger) *Service {
	svc := &Service{
		logger: logger.With("component", "metrics-service"),
	}

	// Request metrics
	svc.requestCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "api_requests_total",
			Help: "Total number of API requests",
		},
		[]string{"method", "endpoint", "status"},
	)

	svc.requestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "api_request_duration_seconds",
			Help:    "API request duration in seconds",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // From 1ms to ~16s
		},
		[]string{"method", "endpoint"},
	)

	svc.responseSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "api_response_size_bytes",
			Help:    "API response size in bytes",
			Buckets: prometheus.ExponentialBuckets(100, 10, 8), // From 100B to ~1GB
		},
		[]string{"method", "endpoint"},
	)

	// Connection metrics
	svc.activeConnections = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "api_active_connections",
			Help: "Number of active connections",
		},
		[]string{"type", "user_id"},
	)

	// Sandbox metrics
	svc.sandboxesCreated = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sandboxes_created_total",
			Help: "Total number of sandboxes created",
		},
		[]string{"runtime", "warm_pod_used", "user_id"},
	)

	svc.sandboxesTerminated = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sandboxes_terminated_total",
			Help: "Total number of sandboxes terminated",
		},
		[]string{"runtime", "reason"},
	)

	// Execution metrics
	svc.executionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "executions_total",
			Help: "Total number of code/command executions",
		},
		[]string{"type", "runtime", "status", "user_id"},
	)

	svc.executionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "execution_duration_seconds",
			Help:    "Execution duration in seconds",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 10),
		},
		[]string{"type", "runtime"},
	)

	// Error metrics
	svc.errorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "api_errors_total",
			Help: "Total number of API errors",
		},
		[]string{"type", "endpoint", "code"},
	)

	// Package installation metrics
	svc.packageInstalls = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "package_installations_total",
			Help: "Total number of package installations",
		},
		[]string{"runtime", "manager", "status"},
	)

	// File operation metrics
	svc.fileOperations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "file_operations_total",
			Help: "Total number of file operations",
		},
		[]string{"operation", "status"},
	)

	// Resource usage metrics
	svc.resourceUsage = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "sandbox_resource_usage",
			Help: "Resource usage by sandboxes",
		},
		[]string{"sandbox_id", "resource_type"},
	)

	// Warm pool metrics
	svc.warmPoolHitRatio = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "warm_pool_hit_ratio",
			Help: "Ratio of sandbox creations that used a warm pod",
		},
		[]string{"runtime"},
	)

	svc.warmPoolUtilization = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "warm_pool_utilization",
			Help: "Current utilization of warm pools",
		},
		[]string{"runtime", "pool_name"},
	)

	svc.warmPoolScaling = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "warm_pool_scaling_operations_total",
			Help: "Total number of warm pool scaling operations",
		},
		[]string{"runtime", "operation", "reason"},
	)

	return svc
}

// Start initializes the metrics service
func (s *Service) Start() error {
	s.logger.Info("Starting metrics service")
	return nil
}

// Stop cleans up the metrics service
func (s *Service) Stop() error {
	s.logger.Info("Stopping metrics service")
	return nil
}

// RecordRequest records metrics for an API request
func (s *Service) RecordRequest(method, path string, status int, duration time.Duration, size int) {
	s.requestCounter.WithLabelValues(method, path, fmt.Sprintf("%d", status)).Inc()
	s.requestDuration.WithLabelValues(method, path).Observe(duration.Seconds())
	s.responseSize.WithLabelValues(method, path).Observe(float64(size))
}

// RecordSandboxCreation records metrics for sandbox creation
func (s *Service) RecordSandboxCreation(runtime string, warmPodUsed bool, userID string) {
	s.sandboxesCreated.WithLabelValues(
		runtime,
		fmt.Sprintf("%t", warmPodUsed),
		userID,
	).Inc()
}

// RecordSandboxTermination records metrics for sandbox termination
func (s *Service) RecordSandboxTermination(runtime, reason string) {
	s.sandboxesTerminated.WithLabelValues(runtime, reason).Inc()
}

// RecordExecution records metrics for code/command execution
func (s *Service) RecordExecution(execType, runtime, status, userID string, duration time.Duration) {
	s.executionsTotal.WithLabelValues(execType, runtime, status, userID).Inc()
	s.executionDuration.WithLabelValues(execType, runtime).Observe(duration.Seconds())
}

// RecordError records an API error
func (s *Service) RecordError(errorType, endpoint, code string) {
	s.errorsTotal.WithLabelValues(errorType, endpoint, code).Inc()
}

// RecordPackageInstallation records a package installation
func (s *Service) RecordPackageInstallation(runtime, manager, status string) {
	s.packageInstalls.WithLabelValues(runtime, manager, status).Inc()
}

// RecordFileOperation records a file operation
func (s *Service) RecordFileOperation(operation, status string) {
	s.fileOperations.WithLabelValues(operation, status).Inc()
}

// RecordResourceUsage records sandbox resource usage
func (s *Service) RecordResourceUsage(sandboxID string, cpu float64, memoryBytes int64) {
	s.resourceUsage.WithLabelValues(sandboxID, "cpu").Set(cpu)
	s.resourceUsage.WithLabelValues(sandboxID, "memory").Set(float64(memoryBytes))
}

// RecordWarmPoolMetrics records warm pool metrics
func (s *Service) RecordWarmPoolMetrics(runtime, poolName string, utilization float64) {
	s.warmPoolUtilization.WithLabelValues(runtime, poolName).Set(utilization)
}

// RecordWarmPoolScaling records a warm pool scaling operation
func (s *Service) RecordWarmPoolScaling(runtime, operation, reason string) {
	s.warmPoolScaling.WithLabelValues(runtime, operation, reason).Inc()
}

// IncrementActiveConnections increments the active connections counter
func (s *Service) IncrementActiveConnections(connType, userID string) {
	s.activeConnections.WithLabelValues(connType, userID).Inc()
}

// DecrementActiveConnections decrements the active connections counter
func (s *Service) DecrementActiveConnections(connType, userID string) {
	s.activeConnections.WithLabelValues(connType, userID).Dec()
}

// UpdateWarmPoolHitRatio updates the warm pool hit ratio metric
func (s *Service) UpdateWarmPoolHitRatio(runtime string, ratio float64) {
	s.warmPoolHitRatio.WithLabelValues(runtime).Set(ratio)
}
