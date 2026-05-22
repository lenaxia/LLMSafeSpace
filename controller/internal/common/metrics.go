package common

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// SandboxesCreated tracks the number of sandboxes created
	SandboxesCreated = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_sandboxes_created_total",
			Help: "Number of sandboxes created",
		},
		[]string{"runtime", "security_level"},
	)

	// SandboxesDeleted tracks the number of sandboxes deleted
	SandboxesDeleted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_sandboxes_deleted_total",
			Help: "Number of sandboxes deleted",
		},
		[]string{"runtime", "security_level"},
	)

	// SandboxesRunning tracks the number of sandboxes currently running
	SandboxesRunning = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "llmsafespace_sandboxes_running",
			Help: "Number of sandboxes currently running",
		},
		[]string{"runtime", "security_level"},
	)

	// SandboxCreationDuration tracks the time taken to create a sandbox
	SandboxCreationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "llmsafespace_sandbox_creation_duration_seconds",
			Help:    "Time taken to create a sandbox",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
		},
		[]string{"runtime", "security_level"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		SandboxesCreated,
		SandboxesDeleted,
		SandboxesRunning,
		SandboxCreationDuration,
	)
}
