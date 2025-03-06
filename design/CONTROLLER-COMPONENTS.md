# Shared Components and Utilities

The Sandbox Controller uses shared components and utilities across all reconciliation loops to avoid code duplication and ensure consistent behavior.

## 1. WarmPodAllocator

The WarmPodAllocator is responsible for finding and allocating warm pods to sandboxes:

```go
// WarmPodAllocator handles allocation of warm pods to sandboxes
type WarmPodAllocator struct {
    llmsafespaceClient clientset.Interface
    warmPoolLister     listers.WarmPoolLister
    warmPodLister      listers.WarmPodLister
    podLister          corelisters.PodLister
    recorder           record.EventRecorder
    metrics            *WarmPodMetrics
}

// WarmPodMetrics collects metrics for warm pod operations
type WarmPodMetrics struct {
    allocationAttemptsTotal    *prometheus.CounterVec
    allocationDurationSeconds  *prometheus.HistogramVec
    allocationResultTotal      *prometheus.CounterVec
}

// FindMatchingWarmPod finds a warm pod that matches the requirements
func (a *WarmPodAllocator) FindMatchingWarmPod(sandbox *llmsafespacev1.Sandbox) (*llmsafespacev1.WarmPod, error) {
    startTime := time.Now()
    defer func() {
        if a.metrics != nil {
            a.metrics.allocationDurationSeconds.WithLabelValues(
                sandbox.Spec.Runtime,
                sandbox.Spec.SecurityLevel,
            ).Observe(time.Since(startTime).Seconds())
        }
    }()
    
    // Check if sandbox requests a warm pod
    useWarmPod := false
    if sandbox.Annotations != nil {
        if value, ok := sandbox.Annotations["llmsafespace.dev/use-warm-pod"]; ok && value == "true" {
            useWarmPod = true
        }
    }
    
    if !useWarmPod {
        if a.metrics != nil {
            a.metrics.allocationAttemptsTotal.WithLabelValues(
                sandbox.Spec.Runtime,
                sandbox.Spec.SecurityLevel,
                "not_requested",
            ).Inc()
        }
        return nil, nil
    }
    
    if a.metrics != nil {
        a.metrics.allocationAttemptsTotal.WithLabelValues(
            sandbox.Spec.Runtime,
            sandbox.Spec.SecurityLevel,
            "requested",
        ).Inc()
    }
    
    // Find warm pools that match the runtime
    runtime := sandbox.Spec.Runtime
    securityLevel := sandbox.Spec.SecurityLevel
    
    // Use annotations if available for more precise matching
    if sandbox.Annotations != nil {
        if rt, ok := sandbox.Annotations["llmsafespace.dev/warm-pod-runtime"]; ok && rt != "" {
            runtime = rt
        }
        if sl, ok := sandbox.Annotations["llmsafespace.dev/warm-pod-security-level"]; ok && sl != "" {
            securityLevel = sl
        }
    }
    
    selector := labels.SelectorFromSet(labels.Set{
        "runtime": strings.Replace(runtime, ":", "-", -1),
    })
    
    pools, err := a.warmPoolLister.List(selector)
    if err != nil {
        if a.metrics != nil {
            a.metrics.allocationResultTotal.WithLabelValues(
                sandbox.Spec.Runtime,
                sandbox.Spec.SecurityLevel,
                "error",
            ).Inc()
        }
        return nil, err
    }
    
    // No matching pools
    if len(pools) == 0 {
        if a.metrics != nil {
            a.metrics.allocationResultTotal.WithLabelValues(
                sandbox.Spec.Runtime,
                sandbox.Spec.SecurityLevel,
                "no_matching_pools",
            ).Inc()
        }
        return nil, nil
    }
    
    // Find the best matching pool
    var bestPool *llmsafespacev1.WarmPool
    for i, pool := range pools {
        if pool.Status.AvailablePods > 0 {
            // Check if security level matches
            if pool.Spec.SecurityLevel == securityLevel {
                bestPool = &pools[i]
                break
            }
            
            // If no exact match yet, use this as a fallback
            if bestPool == nil {
                bestPool = &pools[i]
            }
        }
    }
    
    if bestPool == nil {
        if a.metrics != nil {
            a.metrics.allocationResultTotal.WithLabelValues(
                sandbox.Spec.Runtime,
                sandbox.Spec.SecurityLevel,
                "no_available_pods",
            ).Inc()
        }
        return nil, nil
    }
    
    // Find an available warm pod from the pool
    podSelector := labels.SelectorFromSet(labels.Set{
        "app": "llmsafespace",
        "component": "warmpod",
        "pool": bestPool.Name,
        "status": "ready",
    })
    
    warmPods, err := a.warmPodLister.WarmPods(bestPool.Namespace).List(podSelector)
    if err != nil {
        if a.metrics != nil {
            a.metrics.allocationResultTotal.WithLabelValues(
                sandbox.Spec.Runtime,
                sandbox.Spec.SecurityLevel,
                "error",
            ).Inc()
        }
        return nil, err
    }
    
    // Find a ready pod
    for _, pod := range warmPods {
        if pod.Status.Phase == "Ready" {
            if a.metrics != nil {
                a.metrics.allocationResultTotal.WithLabelValues(
                    sandbox.Spec.Runtime,
                    sandbox.Spec.SecurityLevel,
                    "success",
                ).Inc()
            }
            return &pod, nil
        }
    }
    
    if a.metrics != nil {
        a.metrics.allocationResultTotal.WithLabelValues(
            sandbox.Spec.Runtime,
            sandbox.Spec.SecurityLevel,
            "no_ready_pods",
        ).Inc()
    }
    return nil, nil
}

// ClaimWarmPod attempts to claim a warm pod for use with a sandbox
func (a *WarmPodAllocator) ClaimWarmPod(sandbox *llmsafespacev1.Sandbox, warmPod *llmsafespacev1.WarmPod) (bool, error) {
    startTime := time.Now()
    defer func() {
        if a.metrics != nil {
            a.metrics.allocationDurationSeconds.WithLabelValues(
                sandbox.Spec.Runtime,
                sandbox.Spec.SecurityLevel,
            ).Observe(time.Since(startTime).Seconds())
        }
    }()
    
    // Get the warm pod (fresh copy to avoid conflicts)
    freshWarmPod, err := a.llmsafespaceClient.LlmsafespaceV1().WarmPods(warmPod.Namespace).Get(
        context.TODO(), warmPod.Name, metav1.GetOptions{})
    if err != nil {
        return false, err
    }
    
    // Check if pod is still available
    if freshWarmPod.Status.Phase != "Ready" {
        return false, nil
    }
    
    // Try to update status to Assigned using optimistic concurrency
    warmPodCopy := freshWarmPod.DeepCopy()
    warmPodCopy.Status.Phase = "Assigned"
    warmPodCopy.Status.AssignedTo = string(sandbox.UID)
    warmPodCopy.Status.AssignedAt = metav1.Now()
    
    _, err = a.llmsafespaceClient.LlmsafespaceV1().WarmPods(warmPod.Namespace).UpdateStatus(
        context.TODO(), warmPodCopy, metav1.UpdateOptions{})
    if err != nil {
        return false, err
    }
    
    // Record event
    a.recorder.Event(warmPod, corev1.EventTypeNormal, "PodAssigned", 
        fmt.Sprintf("Warm pod assigned to sandbox %s", sandbox.Name))
    
    return true, nil
}
```

## 2. Resource Management

The controller creates and manages resources for both sandboxes and warm pools:

```go
func (c *Controller) createSandboxResources(sandbox *llmsafespacev1.Sandbox) error {
    // Update phase to Creating
    if sandbox.Status.Phase != "Creating" {
        sandbox.Status.Phase = "Creating"
        sandbox, err := c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).UpdateStatus(
            context.TODO(), sandbox, metav1.UpdateOptions{})
        if err != nil {
            return err
        }
    }
    
    // Check if there's a matching warm pod available
    warmPod, err := c.warmPodAllocator.FindMatchingWarmPod(sandbox)
    if err != nil {
        klog.Warningf("Error finding matching warm pod: %v", err)
        // Continue with normal creation
    } else if warmPod != nil {
        // Use the warm pod for this sandbox
        return c.assignWarmPodToSandbox(sandbox, warmPod)
    }
    
    // No matching warm pod, create a new sandbox from scratch
    
    // Get runtime environment
    runtimeEnv, err := c.getRuntimeEnvironment(sandbox.Spec.Runtime)
    if err != nil {
        c.updateSandboxStatus(sandbox, "Failed", "RuntimeNotFound", 
            fmt.Sprintf("Runtime environment not found: %v", err))
        return err
    }
    
    // Get sandbox profile if specified
    var profile *llmsafespacev1.SandboxProfile
    if sandbox.Spec.ProfileRef != nil {
        profileNamespace := sandbox.Namespace
        if sandbox.Spec.ProfileRef.Namespace != "" {
            profileNamespace = sandbox.Spec.ProfileRef.Namespace
        }
        
        profile, err = c.profileLister.SandboxProfiles(profileNamespace).Get(sandbox.Spec.ProfileRef.Name)
        if err != nil {
            c.updateSandboxStatus(sandbox, "Failed", "ProfileNotFound", 
                fmt.Sprintf("Sandbox profile not found: %v", err))
            return err
        }
    }
    
    // Create namespace if using namespace isolation
    if c.config.NamespaceIsolation {
        if err := c.namespaceManager.EnsureNamespace(sandbox); err != nil {
            return err
        }
    }
    
    // Create service account
    if err := c.serviceAccountManager.EnsureServiceAccount(sandbox); err != nil {
        return err
    }
    
    // Create pod
    pod, err := c.podManager.EnsurePod(sandbox, runtimeEnv, profile)
    if err != nil {
        return err
    }
    
    // Create service
    service, err := c.serviceManager.EnsureService(sandbox)
    if err != nil {
        return err
    }
    
    // Create network policies
    if err := c.networkPolicyManager.EnsureNetworkPolicies(sandbox); err != nil {
        return err
    }
    
    // Update status with pod and service information
    sandbox.Status.PodName = pod.Name
    sandbox.Status.PodNamespace = pod.Namespace
    sandbox.Status.Endpoint = fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, service.Namespace)
    
    _, err = c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).UpdateStatus(
        context.TODO(), sandbox, metav1.UpdateOptions{})
    
    return err
}
```

## 3. Pod Creation and Configuration

The Pod is the primary resource for the sandbox execution environment. It is configured with appropriate security settings, resource limits, and volume mounts.

```go
func (p *PodManager) createPod(sandbox *llmsafespacev1.Sandbox, runtime *llmsafespacev1.RuntimeEnvironment, profile *llmsafespacev1.SandboxProfile) (*corev1.Pod, error) {
    // Determine namespace
    namespace := sandbox.Namespace
    if p.config.NamespaceIsolation {
        namespace = fmt.Sprintf("sandbox-%s", sandbox.UID)
    }
    
    // Configure security context
    securityContext := p.securityContextManager.GetSecurityContext(sandbox, runtime, profile)
    
    // Configure resource limits
    resources := p.getResourceRequirements(sandbox, runtime, profile)
    
    // Configure volume mounts
    volumes, volumeMounts := p.getVolumesAndMounts(sandbox)
    
    // Configure environment variables
    envVars := p.getEnvironmentVariables(sandbox, runtime)
    
    // Create pod
    pod := &corev1.Pod{
        ObjectMeta: metav1.ObjectMeta{
            Name: fmt.Sprintf("sandbox-%s", sandbox.Name),
            Namespace: namespace,
            Labels: map[string]string{
                "app": "llmsafespace",
                "component": "sandbox",
                "sandbox-id": sandbox.Name,
                "sandbox-uid": string(sandbox.UID),
            },
            OwnerReferences: []metav1.OwnerReference{
                *metav1.NewControllerRef(sandbox, llmsafespacev1.SchemeGroupVersion.WithKind("Sandbox")),
            },
        },
        Spec: corev1.PodSpec{
            ServiceAccountName: fmt.Sprintf("sandbox-%s", sandbox.Name),
            SecurityContext: &corev1.PodSecurityContext{
                RunAsUser: &securityContext.RunAsUser,
                RunAsGroup: &securityContext.RunAsGroup,
                FSGroup: &securityContext.FSGroup,
            },
            Containers: []corev1.Container{
                {
                    Name: "sandbox",
                    Image: runtime.Spec.Image,
                    Resources: resources,
                    SecurityContext: &corev1.SecurityContext{
                        AllowPrivilegeEscalation: &securityContext.AllowPrivilegeEscalation,
                        ReadOnlyRootFilesystem: &securityContext.ReadOnlyRootFilesystem,
                        Capabilities: &corev1.Capabilities{
                            Drop: []corev1.Capability{"ALL"},
                        },
                        SeccompProfile: &corev1.SeccompProfile{
                            Type: securityContext.SeccompProfileType,
                            LocalhostProfile: securityContext.SeccompProfilePath,
                        },
                    },
                    Env: envVars,
                    VolumeMounts: volumeMounts,
                    Ports: []corev1.ContainerPort{
                        {
                            Name: "http",
                            ContainerPort: 8080,
                            Protocol: corev1.ProtocolTCP,
                        },
                    },
                    LivenessProbe: &corev1.Probe{
                        ProbeHandler: corev1.ProbeHandler{
                            HTTPGet: &corev1.HTTPGetAction{
                                Path: "/health",
                                Port: intstr.FromInt(8080),
                            },
                        },
                        InitialDelaySeconds: 5,
                        PeriodSeconds: 10,
                    },
                    ReadinessProbe: &corev1.Probe{
                        ProbeHandler: corev1.ProbeHandler{
                            HTTPGet: &corev1.HTTPGetAction{
                                Path: "/ready",
                                Port: intstr.FromInt(8080),
                            },
                        },
                        InitialDelaySeconds: 2,
                        PeriodSeconds: 5,
                    },
                },
            },
            Volumes: volumes,
            RestartPolicy: corev1.RestartPolicyAlways,
            TerminationGracePeriodSeconds: &sandbox.Spec.Timeout,
        },
    }
    
    // Apply runtime class if using gVisor
    if sandbox.Spec.SecurityLevel == "high" {
        pod.Spec.RuntimeClassName = ptr.To("gvisor")
    }
    
    // Apply node selector if configured
    if p.config.NodeSelector != nil {
        pod.Spec.NodeSelector = p.config.NodeSelector
    }
    
    // Apply tolerations if configured
    if p.config.Tolerations != nil {
        pod.Spec.Tolerations = p.config.Tolerations
    }
    
    // Apply CPU pinning if requested
    if sandbox.Spec.Resources != nil && sandbox.Spec.Resources.CPUPinning {
        if pod.Spec.NodeSelector == nil {
            pod.Spec.NodeSelector = make(map[string]string)
        }
        pod.Spec.NodeSelector["cpu-pinning"] = "true"
        
        if pod.Annotations == nil {
            pod.Annotations = make(map[string]string)
        }
        pod.Annotations["cpu-pinning.llmsafespace.dev/enabled"] = "true"
    }
    
    return p.kubeClient.CoreV1().Pods(namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
}
```

## 4. Network Policy Configuration

Network policies are created to enforce network isolation between sandboxes and control egress traffic.

```go
// NetworkPolicyManager handles the creation, update, and deletion of network policies
type NetworkPolicyManager struct {
    kubeClient kubernetes.Interface
    config     *config.Config
    recorder   record.EventRecorder
}

// NewNetworkPolicyManager creates a new NetworkPolicyManager
func NewNetworkPolicyManager(kubeClient kubernetes.Interface, config *config.Config, recorder record.EventRecorder) *NetworkPolicyManager {
    return &NetworkPolicyManager{
        kubeClient: kubeClient,
        config:     config,
        recorder:   recorder,
    }
}

// EnsureNetworkPolicies ensures that all required network policies exist for a sandbox
func (n *NetworkPolicyManager) EnsureNetworkPolicies(sandbox *llmsafespacev1.Sandbox, podNamespace string) error {
    startTime := time.Now()
    defer func() {
        networkPolicyOperationDurationSeconds.WithLabelValues("ensure").Observe(time.Since(startTime).Seconds())
    }()
    
    // Determine namespace
    namespace := podNamespace
    if namespace == "" {
        namespace = sandbox.Namespace
        if n.config.NamespaceIsolation {
            namespace = fmt.Sprintf("sandbox-%s", sandbox.UID)
        }
    }
    
    // Create default deny policy
    if err := n.ensureDefaultDenyPolicy(sandbox, namespace); err != nil {
        networkPolicyOperationsTotal.WithLabelValues("create_default_deny", "failed").Inc()
        return fmt.Errorf("failed to ensure default deny policy: %v", err)
    }
    networkPolicyOperationsTotal.WithLabelValues("create_default_deny", "success").Inc()
    
    // Create API service access policy
    if err := n.ensureAPIServicePolicy(sandbox, namespace); err != nil {
        networkPolicyOperationsTotal.WithLabelValues("create_api_access", "failed").Inc()
        return fmt.Errorf("failed to ensure API service access policy: %v", err)
    }
    networkPolicyOperationsTotal.WithLabelValues("create_api_access", "success").Inc()
    
    // Create DNS access policy
    if err := n.ensureDNSAccessPolicy(sandbox, namespace); err != nil {
        networkPolicyOperationsTotal.WithLabelValues("create_dns_access", "failed").Inc()
        return fmt.Errorf("failed to ensure DNS access policy: %v", err)
    }
    networkPolicyOperationsTotal.WithLabelValues("create_dns_access", "success").Inc()
    
    // Create egress policies if specified
    if sandbox.Spec.NetworkAccess != nil && len(sandbox.Spec.NetworkAccess.Egress) > 0 {
        if err := n.ensureEgressPolicies(sandbox, namespace); err != nil {
            networkPolicyOperationsTotal.WithLabelValues("create_egress", "failed").Inc()
            return fmt.Errorf("failed to ensure egress policies: %v", err)
        }
        networkPolicyOperationsTotal.WithLabelValues("create_egress", "success").Inc()
    } else {
        // Delete any existing egress policies if no egress is specified
        if err := n.deleteEgressPolicies(sandbox, namespace); err != nil {
            networkPolicyOperationsTotal.WithLabelValues("delete_egress", "failed").Inc()
            return fmt.Errorf("failed to delete egress policies: %v", err)
        }
        networkPolicyOperationsTotal.WithLabelValues("delete_egress", "success").Inc()
    }
    
    // Create ingress policies if specified
    if sandbox.Spec.NetworkAccess != nil && sandbox.Spec.NetworkAccess.Ingress {
        if err := n.ensureIngressPolicies(sandbox, namespace); err != nil {
            networkPolicyOperationsTotal.WithLabelValues("create_ingress", "failed").Inc()
            return fmt.Errorf("failed to ensure ingress policies: %v", err)
        }
        networkPolicyOperationsTotal.WithLabelValues("create_ingress", "success").Inc()
    } else {
        // Delete any existing ingress policies if ingress is not enabled
        if err := n.deleteIngressPolicies(sandbox, namespace); err != nil {
            networkPolicyOperationsTotal.WithLabelValues("delete_ingress", "failed").Inc()
            return fmt.Errorf("failed to delete ingress policies: %v", err)
        }
        networkPolicyOperationsTotal.WithLabelValues("delete_ingress", "success").Inc()
    }
    
    return nil
}
```

## 5. Sandbox Cleanup

When a Sandbox is deleted, the controller cleans up all associated resources.

```go
func (c *Controller) handleSandboxDeletion(sandbox *llmsafespacev1.Sandbox) error {
    startTime := time.Now()
    klog.V(2).Infof("Handling deletion of sandbox %s", sandbox.Name)
    
    // Update phase to Terminating if not already
    if sandbox.Status.Phase != "Terminating" {
        sandbox.Status.Phase = "Terminating"
        sandbox, err := c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).UpdateStatus(
            context.TODO(), sandbox, metav1.UpdateOptions{})
        if err != nil {
            reconciliationErrorsTotal.WithLabelValues("sandbox", "update_status").Inc()
            return err
        }
    }
    
    // Record deletion metric
    sandboxesDeletedTotal.Inc()
    
    // Check if this sandbox was using a warm pod
    if sandbox.Status.WarmPodRef != nil {
        // Get the warm pod
        warmPod, err := c.warmPodLister.WarmPods(sandbox.Status.WarmPodRef.Namespace).Get(sandbox.Status.WarmPodRef.Name)
        if err != nil && !errors.IsNotFound(err) {
            reconciliationErrorsTotal.WithLabelValues("sandbox", "get_warmpod").Inc()
            return err
        }
        
        if warmPod != nil {
            // Check if we should recycle this pod
            if c.shouldRecyclePod(warmPod, sandbox) {
                klog.V(2).Infof("Recycling warm pod %s from sandbox %s", warmPod.Name, sandbox.Name)
                return c.recyclePod(warmPod, sandbox)
            } else {
                klog.V(2).Infof("Deleting warm pod %s from sandbox %s", warmPod.Name, sandbox.Name)
                // Delete the warm pod
                err = c.llmsafespaceClient.LlmsafespaceV1().WarmPods(warmPod.Namespace).Delete(
                    context.TODO(), warmPod.Name, metav1.DeleteOptions{})
                if err != nil && !errors.IsNotFound(err) {
                    reconciliationErrorsTotal.WithLabelValues("sandbox", "delete_warmpod").Inc()
                    return err
                }
            }
        }
    }
    
    // Create a wait group to track all deletion operations
    var wg sync.WaitGroup
    var deletionErrors []error
    var errMutex sync.Mutex
    
    // Helper function to add errors
    addError := func(err error) {
        errMutex.Lock()
        deletionErrors = append(deletionErrors, err)
        errMutex.Unlock()
    }
    
    // Delete pod
    if sandbox.Status.PodName != "" {
        podNamespace := sandbox.Status.PodNamespace
        if podNamespace == "" {
            podNamespace = sandbox.Namespace
        }
        
        wg.Add(1)
        go func() {
            defer wg.Done()
            klog.V(3).Infof("Deleting pod %s/%s for sandbox %s", podNamespace, sandbox.Status.PodName, sandbox.Name)
            err := c.kubeClient.CoreV1().Pods(podNamespace).Delete(
                context.TODO(), sandbox.Status.PodName, metav1.DeleteOptions{})
            if err != nil && !errors.IsNotFound(err) {
                klog.Warningf("Failed to delete pod %s/%s: %v", podNamespace, sandbox.Status.PodName, err)
                reconciliationErrorsTotal.WithLabelValues("sandbox", "delete_pod").Inc()
                addError(fmt.Errorf("failed to delete pod: %v", err))
            }
        }()
    }
    
    // Delete service
    wg.Add(1)
    go func() {
        defer wg.Done()
        klog.V(3).Infof("Deleting service for sandbox %s", sandbox.Name)
        err := c.serviceManager.DeleteService(sandbox)
        if err != nil {
            klog.Warningf("Failed to delete service for sandbox %s: %v", sandbox.Name, err)
            reconciliationErrorsTotal.WithLabelValues("sandbox", "delete_service").Inc()
            addError(fmt.Errorf("failed to delete service: %v", err))
        }
    }()
    
    // Delete network policies
    wg.Add(1)
    go func() {
        defer wg.Done()
        klog.V(3).Infof("Deleting network policies for sandbox %s", sandbox.Name)
        err := c.networkPolicyManager.DeleteNetworkPolicies(sandbox)
        if err != nil {
            klog.Warningf("Failed to delete network policies for sandbox %s: %v", sandbox.Name, err)
            reconciliationErrorsTotal.WithLabelValues("sandbox", "delete_network_policies").Inc()
            networkPolicyOperationsTotal.WithLabelValues("delete", "failed").Inc()
            addError(fmt.Errorf("failed to delete network policies: %v", err))
        } else {
            networkPolicyOperationsTotal.WithLabelValues("delete", "success").Inc()
        }
    }()
    
    // Delete service account
    wg.Add(1)
    go func() {
        defer wg.Done()
        klog.V(3).Infof("Deleting service account for sandbox %s", sandbox.Name)
        err := c.serviceAccountManager.DeleteServiceAccount(sandbox)
        if err != nil {
            klog.Warningf("Failed to delete service account for sandbox %s: %v", sandbox.Name, err)
            reconciliationErrorsTotal.WithLabelValues("sandbox", "delete_service_account").Inc()
            addError(fmt.Errorf("failed to delete service account: %v", err))
        }
    }()
    
    // Delete PVC if it exists
    if sandbox.Spec.Storage != nil && sandbox.Spec.Storage.Persistent {
        wg.Add(1)
        go func() {
            defer wg.Done()
            klog.V(3).Infof("Deleting PVC for sandbox %s", sandbox.Name)
            err := c.volumeManager.DeletePersistentVolumeClaim(sandbox)
            if err != nil {
                klog.Warningf("Failed to delete PVC for sandbox %s: %v", sandbox.Name, err)
                reconciliationErrorsTotal.WithLabelValues("sandbox", "delete_pvc").Inc()
                volumeOperationsTotal.WithLabelValues("delete", "failed").Inc()
                addError(fmt.Errorf("failed to delete PVC: %v", err))
            } else {
                volumeOperationsTotal.WithLabelValues("delete", "success").Inc()
            }
        }()
    }
    
    // Wait for all deletion operations to complete
    wg.Wait()
    
    // Delete namespace if using namespace isolation
    if c.config.NamespaceIsolation {
        klog.V(3).Infof("Deleting namespace for sandbox %s", sandbox.Name)
        err := c.namespaceManager.DeleteNamespace(sandbox)
        if err != nil {
            klog.Warningf("Failed to delete namespace for sandbox %s: %v", sandbox.Name, err)
            reconciliationErrorsTotal.WithLabelValues("sandbox", "delete_namespace").Inc()
            deletionErrors = append(deletionErrors, fmt.Errorf("failed to delete namespace: %v", err))
        }
    }
    
    // Check if there were any errors during deletion
    if len(deletionErrors) > 0 {
        // Log all errors but continue with finalizer removal
        for _, err := range deletionErrors {
            klog.Errorf("Error during sandbox %s deletion: %v", sandbox.Name, err)
        }
        
        // If there are critical errors that should prevent finalizer removal, return here
        // For now, we'll continue with finalizer removal to avoid stuck resources
    }
    
    // Remove finalizer
    sandbox.Finalizers = removeString(sandbox.Finalizers, sandboxFinalizer)
    _, err := c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).Update(
        context.TODO(), sandbox, metav1.UpdateOptions{})
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("sandbox", "remove_finalizer").Inc()
        return err
    }
    
    klog.V(2).Infof("Successfully deleted sandbox %s in %v", sandbox.Name, time.Since(startTime))
    
    // Record deletion duration
    reconciliationDurationSeconds.WithLabelValues("sandbox", "deletion").Observe(time.Since(startTime).Seconds())
    
    return nil
}
```
