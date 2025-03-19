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
		[]string{"runtime", "security_level", "warm_pool_used"},
	)
	
	// WarmPodsAvailable tracks the number of warm pods available
	WarmPodsAvailable = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "llmsafespace_warm_pods_available",
			Help: "Number of warm pods available",
		},
		[]string{"runtime", "pool"},
	)
	
	// WarmPodsAssigned tracks the number of warm pods assigned
	WarmPodsAssigned = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "llmsafespace_warm_pods_assigned",
			Help: "Number of warm pods assigned",
		},
		[]string{"runtime", "pool"},
	)
	
	// WarmPoolHitRatio tracks the ratio of sandbox creations that used a warm pod
	WarmPoolHitRatio = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "llmsafespace_warm_pool_hit_ratio",
			Help: "Ratio of sandbox creations that used a warm pod",
		},
		[]string{"runtime", "pool"},
	)
	
	// WarmPodRecycleCount tracks the number of times warm pods have been recycled
	WarmPodRecycleCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_warm_pod_recycle_count_total",
			Help: "Number of times warm pods have been recycled",
		},
		[]string{"runtime", "pool"},
	)
	
	// WarmPodTTLExceededCount tracks the number of warm pods that exceeded their TTL
	WarmPodTTLExceededCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llmsafespace_warm_pod_ttl_exceeded_total",
			Help: "Number of warm pods that exceeded their TTL",
		},
		[]string{"runtime", "pool"},
	)
)

func init() {
	// Register metrics with the controller-runtime metrics registry
	metrics.Registry.MustRegister(
		SandboxesCreated,
		SandboxesDeleted,
		SandboxesRunning,
		SandboxCreationDuration,
		WarmPodsAvailable,
		WarmPodsAssigned,
		WarmPoolHitRatio,
		WarmPodRecycleCount,
		WarmPodTTLExceededCount,
	)
}
