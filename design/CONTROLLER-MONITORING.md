# Monitoring and Metrics

The Sandbox Controller exposes Prometheus metrics for monitoring its operation and the state of managed resources:

```go
func setupMetrics() {
    // Register metrics
    prometheus.MustRegister(sandboxesCreatedTotal)
    prometheus.MustRegister(sandboxesDeletedTotal)
    prometheus.MustRegister(sandboxesFailedTotal)
    prometheus.MustRegister(reconciliationDurationSeconds)
    prometheus.MustRegister(reconciliationErrorsTotal)
    prometheus.MustRegister(workqueueDepthGauge)
    prometheus.MustRegister(workqueueLatencySeconds)
    prometheus.MustRegister(workqueueWorkDurationSeconds)
    
    // Register warm pool metrics
    prometheus.MustRegister(warmPoolSizeGauge)
    prometheus.MustRegister(warmPoolAssignmentDurationSeconds)
    prometheus.MustRegister(warmPoolCreationDurationSeconds)
    prometheus.MustRegister(warmPoolRecycleTotal)
    prometheus.MustRegister(warmPoolHitRatio)
    
    // Start metrics server
    http.Handle("/metrics", promhttp.Handler())
    go func() {
        klog.Info("Starting metrics server on :8080")
        if err := http.ListenAndServe(":8080", nil); err != nil {
            klog.Errorf("Failed to start metrics server: %v", err)
        }
    }()
}

// Metric definitions
var (
    // Sandbox metrics
    sandboxesCreatedTotal = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "llmsafespace_sandboxes_created_total",
            Help: "Total number of sandboxes created",
        },
    )
    
    sandboxesDeletedTotal = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "llmsafespace_sandboxes_deleted_total",
            Help: "Total number of sandboxes deleted",
        },
    )
    
    sandboxesFailedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_sandboxes_failed_total",
            Help: "Total number of sandboxes that failed to create",
        },
        []string{"reason"},
    )
    
    sandboxStartupDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_sandbox_startup_duration_seconds",
            Help: "Time taken for a sandbox to start up",
            Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
        },
        []string{"runtime", "warm_pod_used"},
    )
    
    sandboxExecutionsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_sandbox_executions_total",
            Help: "Total number of code/command executions in sandboxes",
        },
        []string{"runtime", "execution_type", "status"},
    )
    
    sandboxExecutionDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_sandbox_execution_duration_seconds",
            Help: "Duration of code/command executions in sandboxes",
            Buckets: prometheus.ExponentialBuckets(0.01, 2, 15),
        },
        []string{"runtime", "execution_type"},
    )
    
    // Warm pool metrics
    warmPoolsCreatedTotal = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "llmsafespace_warmpools_created_total",
            Help: "Total number of warm pools created",
        },
    )
    
    warmPoolsDeletedTotal = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "llmsafespace_warmpools_deleted_total",
            Help: "Total number of warm pools deleted",
        },
    )
    
    warmPoolSizeGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_warmpool_size",
            Help: "Current size of warm pools",
        },
        []string{"pool", "runtime", "status"},
    )
    
    warmPoolUtilizationGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_warmpool_utilization",
            Help: "Utilization ratio of warm pools (assigned pods / total pods)",
        },
        []string{"pool", "runtime"},
    )
    
    warmPoolAssignmentDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_warmpool_assignment_duration_seconds",
            Help: "Time taken to assign a warm pod to a sandbox",
            Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
        },
        []string{"pool", "runtime"},
    )
    
    warmPoolCreationDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_warmpool_creation_duration_seconds",
            Help: "Time taken to create a warm pod",
            Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30},
        },
        []string{"pool", "runtime"},
    )
    
    warmPoolRecycleTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_warmpool_recycle_total",
            Help: "Total number of warm pods recycled",
        },
        []string{"pool", "runtime", "success"},
    )
    
    warmPoolRecycleDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_warmpool_recycle_duration_seconds",
            Help: "Time taken to recycle a warm pod",
            Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30},
        },
        []string{"pool", "runtime"},
    )
    
    warmPoolHitRatio = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_warmpool_hit_ratio",
            Help: "Ratio of sandbox creations that used a warm pod",
        },
        []string{"runtime"},
    )
    
    warmPoolPodsDeletedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_warmpool_pods_deleted_total",
            Help: "Total number of warm pods deleted",
        },
        []string{"pool", "runtime", "reason"},
    )
    
    warmPoolScalingOperationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_warmpool_scaling_operations_total",
            Help: "Total number of warm pool scaling operations",
        },
        []string{"pool", "runtime", "operation"},
    )
    
    warmPodRecycleDecisionDurationSeconds = prometheus.NewHistogram(
        prometheus.HistogramOpts{
            Name: "llmsafespace_warmpod_recycle_decision_duration_seconds",
            Help: "Time taken to decide whether to recycle a warm pod",
            Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5},
        },
    )
    
    warmPodRecycleDecisionsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_warmpod_recycle_decisions_total",
            Help: "Total number of warm pod recycle decisions",
        },
        []string{"reason", "decision"},
    )
    
    // Runtime environment metrics
    runtimeEnvironmentValidationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_runtime_environment_validations_total",
            Help: "Total number of runtime environment validations",
        },
        []string{"language", "version", "result"},
    )
    
    runtimeEnvironmentUsageTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_runtime_environment_usage_total",
            Help: "Total number of times each runtime environment is used",
        },
        []string{"language", "version"},
    )
    
    // Sandbox profile metrics
    sandboxProfileValidationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_sandbox_profile_validations_total",
            Help: "Total number of sandbox profile validations",
        },
        []string{"language", "security_level"},
    )
    
    sandboxProfileUsageTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_sandbox_profile_usage_total",
            Help: "Total number of times each sandbox profile is used",
        },
        []string{"profile", "namespace"},
    )
    
    // Security metrics
    securityEventsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_security_events_total",
            Help: "Total number of security events detected",
        },
        []string{"event_type", "severity", "runtime"},
    )
    
    seccompViolationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_seccomp_violations_total",
            Help: "Total number of seccomp violations",
        },
        []string{"syscall", "runtime", "action"},
    )
    
    networkViolationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_network_violations_total",
            Help: "Total number of network policy violations",
        },
        []string{"direction", "destination", "runtime"},
    )
    
    // Resource usage metrics
    resourceUsageGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_resource_usage",
            Help: "Resource usage by sandboxes and warm pools",
        },
        []string{"resource_type", "component", "namespace"},
    )
    
    resourceLimitUtilizationGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_resource_limit_utilization",
            Help: "Resource utilization as a percentage of limit",
        },
        []string{"resource_type", "sandbox_id", "namespace"},
    )
    
    // API service integration metrics
    apiServiceRequestsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_api_service_requests_total",
            Help: "Total number of requests from the API service",
        },
        []string{"request_type", "status"},
    )
    
    apiServiceLatencySeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_api_service_latency_seconds",
            Help: "Latency of API service requests",
            Buckets: prometheus.DefBuckets,
        },
        []string{"request_type"},
    )
    
    // Volume metrics
    volumeOperationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_volume_operations_total",
            Help: "Total number of volume operations",
        },
        []string{"operation", "status"},
    )
    
    persistentVolumeUsageGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_persistent_volume_usage_bytes",
            Help: "Usage of persistent volumes in bytes",
        },
        []string{"sandbox_id", "namespace"},
    )
    
    // Network policy metrics
    networkPolicyOperationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_network_policy_operations_total",
            Help: "Total number of network policy operations",
        },
        []string{"operation", "status"},
    )
    
    // Controller metrics
    reconciliationDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_reconciliation_duration_seconds",
            Help: "Duration of reconciliation in seconds",
            Buckets: prometheus.DefBuckets,
        },
        []string{"resource", "status"},
    )
    
    reconciliationErrorsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_reconciliation_errors_total",
            Help: "Total number of reconciliation errors",
        },
        []string{"resource", "error_type"},
    )
    
    workqueueDepthGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_workqueue_depth",
            Help: "Current depth of the work queue",
        },
        []string{"queue"},
    )
    
    workqueueLatencySeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_workqueue_latency_seconds",
            Help: "How long an item stays in the work queue before being processed",
            Buckets: prometheus.DefBuckets,
        },
        []string{"queue"},
    )
    
    workqueueWorkDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_workqueue_work_duration_seconds",
            Help: "How long processing an item from the work queue takes",
            Buckets: prometheus.DefBuckets,
        },
        []string{"queue"},
    )
    
    controllerSyncCountTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_controller_sync_count_total",
            Help: "Total number of sync operations performed by the controller",
        },
        []string{"resource"},
    )
    
    controllerResourceCountGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_controller_resource_count",
            Help: "Current count of resources managed by the controller",
        },
        []string{"resource"},
    )
)
```
