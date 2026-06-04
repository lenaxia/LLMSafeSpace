// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package metrics

import (
	"fmt"
	"time"

	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type Service struct {
	logger               pkginterfaces.LoggerInterface
	requestCounter       *prometheus.CounterVec
	requestDuration      *prometheus.HistogramVec
	responseSize         *prometheus.HistogramVec
	activeConnections    *prometheus.GaugeVec
	workspacesCreated    *prometheus.CounterVec
	workspacesTerminated *prometheus.CounterVec
	errorsTotal          *prometheus.CounterVec
	resourceUsage        *prometheus.GaugeVec
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

	svc.workspacesCreated = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "workspaces_created_total",
			Help: "Total number of workspaces created",
		},
		[]string{"runtime", "user_id"},
	)

	svc.workspacesTerminated = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "workspaces_terminated_total",
			Help: "Total number of workspaces terminated",
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
			Name: "workspace_resource_usage",
			Help: "Resource usage by workspaces",
		},
		[]string{"workspace_id", "resource_type"},
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

func (s *Service) RecordWorkspaceCreation(runtime, userID string) {
	s.workspacesCreated.WithLabelValues(runtime, userID).Inc()
}

func (s *Service) RecordWorkspaceTermination(runtime, reason string) {
	s.workspacesTerminated.WithLabelValues(runtime, reason).Inc()
}

func (s *Service) RecordError(errorType, endpoint, code string) {
	s.errorsTotal.WithLabelValues(errorType, endpoint, code).Inc()
}

func (s *Service) RecordResourceUsage(workspaceID string, cpu float64, memoryBytes int64) {
	s.resourceUsage.WithLabelValues(workspaceID, "cpu").Set(cpu)
	s.resourceUsage.WithLabelValues(workspaceID, "memory").Set(float64(memoryBytes))
}

func (s *Service) IncrementActiveConnections(connType, userID string) {
	s.activeConnections.WithLabelValues(connType, userID).Inc()
}

func (s *Service) DecrementActiveConnections(connType, userID string) {
	s.activeConnections.WithLabelValues(connType, userID).Dec()
}

// --- Epic 27b: Agent reload metrics ---

var (
	agentReloadTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_agent_reload_total",
			Help: "Total agent reload operations",
		},
		[]string{"result", "drained"},
	)
	agentReloadDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "llmsafespace_agent_reload_duration_ms",
			Help:    "Agent reload duration in milliseconds",
			Buckets: prometheus.ExponentialBuckets(100, 2, 12),
		},
		[]string{"drained"},
	)
	agentReloadDrainTimeouts = promauto.NewCounter(prometheus.CounterOpts{
		Name: "llmsafespace_agent_reload_drain_timeouts_total",
		Help: "Total drain timeout occurrences",
	})
	agentReloadBulkTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_agent_reload_bulk_total",
			Help: "Total bulk reload operations",
		},
		[]string{"outcome"},
	)
)

// RecordAgentReload records a reload operation result.
func (s *Service) RecordAgentReload(result string, durationMs int64, drained bool) {
	drainedStr := "false"
	if drained {
		drainedStr = "true"
	}
	agentReloadTotal.WithLabelValues(result, drainedStr).Inc()
	agentReloadDuration.WithLabelValues(drainedStr).Observe(float64(durationMs))
}

// RecordAgentReloadDrainTimeout records a drain timeout.
func (s *Service) RecordAgentReloadDrainTimeout(_ int64) {
	agentReloadDrainTimeouts.Inc()
}

// RecordAgentReloadBulk records a bulk reload operation.
func (s *Service) RecordAgentReloadBulk(total, succeeded, failed int) {
	outcome := "all_success"
	if failed > 0 && succeeded > 0 {
		outcome = "partial"
	} else if failed > 0 {
		outcome = "all_failed"
	}
	agentReloadBulkTotal.WithLabelValues(outcome).Inc()
}
