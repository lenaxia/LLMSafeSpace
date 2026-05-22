package metrics

import (
	"fmt"
	"time"

	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type Service struct {
	logger              pkginterfaces.LoggerInterface
	requestCounter      *prometheus.CounterVec
	requestDuration     *prometheus.HistogramVec
	responseSize        *prometheus.HistogramVec
	activeConnections   *prometheus.GaugeVec
	sandboxesCreated    *prometheus.CounterVec
	sandboxesTerminated *prometheus.CounterVec
	errorsTotal         *prometheus.CounterVec
	resourceUsage       *prometheus.GaugeVec
}

func New(log pkginterfaces.LoggerInterface) *Service {
	svc := &Service{
		logger: log.With("component", "metrics-service"),
	}

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
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15),
		},
		[]string{"method", "endpoint"},
	)

	svc.responseSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "api_response_size_bytes",
			Help:    "API response size in bytes",
			Buckets: prometheus.ExponentialBuckets(100, 10, 8),
		},
		[]string{"method", "endpoint"},
	)

	svc.activeConnections = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "api_active_connections",
			Help: "Number of active connections",
		},
		[]string{"type", "user_id"},
	)

	svc.sandboxesCreated = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sandboxes_created_total",
			Help: "Total number of sandboxes created",
		},
		[]string{"runtime", "user_id"},
	)

	svc.sandboxesTerminated = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sandboxes_terminated_total",
			Help: "Total number of sandboxes terminated",
		},
		[]string{"runtime", "reason"},
	)

	svc.errorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "api_errors_total",
			Help: "Total number of API errors",
		},
		[]string{"type", "endpoint", "code"},
	)

	svc.resourceUsage = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "sandbox_resource_usage",
			Help: "Resource usage by sandboxes",
		},
		[]string{"sandbox_id", "resource_type"},
	)

	return svc
}

func (s *Service) Start() error {
	s.logger.Info("Starting metrics service")
	return nil
}

func (s *Service) Stop() error {
	s.logger.Info("Stopping metrics service")
	return nil
}

func (s *Service) RecordRequest(method, path string, status int, duration time.Duration, size int) {
	s.requestCounter.WithLabelValues(method, path, fmt.Sprintf("%d", status)).Inc()
	s.requestDuration.WithLabelValues(method, path).Observe(duration.Seconds())
	s.responseSize.WithLabelValues(method, path).Observe(float64(size))
}

func (s *Service) RecordSandboxCreation(runtime, userID string) {
	s.sandboxesCreated.WithLabelValues(runtime, userID).Inc()
}

func (s *Service) RecordSandboxTermination(runtime, reason string) {
	s.sandboxesTerminated.WithLabelValues(runtime, reason).Inc()
}

func (s *Service) RecordError(errorType, endpoint, code string) {
	s.errorsTotal.WithLabelValues(errorType, endpoint, code).Inc()
}

func (s *Service) RecordResourceUsage(sandboxID string, cpu float64, memoryBytes int64) {
	s.resourceUsage.WithLabelValues(sandboxID, "cpu").Set(cpu)
	s.resourceUsage.WithLabelValues(sandboxID, "memory").Set(float64(memoryBytes))
}

func (s *Service) IncrementActiveConnections(connType, userID string) {
	s.activeConnections.WithLabelValues(connType, userID).Inc()
}

func (s *Service) DecrementActiveConnections(connType, userID string) {
	s.activeConnections.WithLabelValues(connType, userID).Dec()
}
