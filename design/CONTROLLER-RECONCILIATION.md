# Reconciliation Loops

The controller implements multiple reconciliation loops, one for each resource type, but shares common utilities and clients across all loops.

## 1. Sandbox Reconciliation Loop

The Sandbox reconciliation loop is responsible for ensuring that the actual state of sandbox resources matches the desired state defined in the Sandbox CR.

The Sandbox reconciliation loop integrates directly with warm pod management:

```go
func (c *Controller) reconcileSandbox(key string) error {
    namespace, name, err := cache.SplitMetaNamespaceKey(key)
    if err != nil {
        return err
    }
    
    // Get Sandbox resource
    sandbox, err := c.sandboxLister.Sandboxes(namespace).Get(name)
    if errors.IsNotFound(err) {
        // Sandbox was deleted, nothing to do
        return nil
    }
    if err != nil {
        return err
    }
    
    // Deep copy to avoid modifying cache
    sandbox = sandbox.DeepCopy()
    
    // Check if sandbox is being deleted
    if !sandbox.DeletionTimestamp.IsZero() {
        return c.handleSandboxDeletion(sandbox)
    }
    
    // Add finalizer if not present
    if !containsString(sandbox.Finalizers, sandboxFinalizer) {
        sandbox.Finalizers = append(sandbox.Finalizers, sandboxFinalizer)
        sandbox, err = c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(namespace).Update(context.TODO(), sandbox, metav1.UpdateOptions{})
        if err != nil {
            return err
        }
    }
    
    // Check if sandbox is using a warm pod
    if sandbox.Status.WarmPodRef != nil {
        return c.handleSandboxWithWarmPod(sandbox)
    }
    
    // Check if sandbox has requested a warm pod but hasn't been assigned one yet
    if sandbox.Annotations != nil && sandbox.Annotations["llmsafespace.dev/use-warm-pod"] == "true" && 
       sandbox.Status.WarmPodRef == nil && sandbox.Status.Phase == "" {
        // Try to find and assign a warm pod
        return c.handleWarmPodRequest(sandbox)
    }
    
    // Process based on current phase
    switch sandbox.Status.Phase {
    case "":
        // New sandbox
        return c.initializeSandbox(sandbox)
    case "Pending":
        return c.createSandboxResources(sandbox)
    case "Creating":
        return c.checkSandboxReadiness(sandbox)
    case "Running":
        return c.ensureSandboxRunning(sandbox)
    case "Terminating":
        return c.cleanupSandboxResources(sandbox)
    case "Failed":
        // Handle failed state (retry or manual intervention)
        return nil
    default:
        // Unknown phase
        c.recorder.Event(sandbox, corev1.EventTypeWarning, "UnknownPhase", 
            fmt.Sprintf("Sandbox has unknown phase: %s", sandbox.Status.Phase))
        return fmt.Errorf("unknown sandbox phase: %s", sandbox.Status.Phase)
    }
}

// handleWarmPodRequest attempts to find and assign a warm pod to a sandbox
func (c *Controller) handleWarmPodRequest(sandbox *llmsafespacev1.Sandbox) error {
    // Mark that we're processing the warm pod request
    sandboxCopy := sandbox.DeepCopy()
    if sandboxCopy.Annotations == nil {
        sandboxCopy.Annotations = make(map[string]string)
    }
    sandboxCopy.Annotations["llmsafespace.dev/warm-pod-pending"] = "true"
    
    sandbox, err := c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).Update(
        context.TODO(), sandboxCopy, metav1.UpdateOptions{})
    if err != nil {
        return err
    }
    
    // Try to find a matching warm pod
    warmPod, err := c.warmPodAllocator.FindMatchingWarmPod(sandbox)
    if err != nil {
        c.recorder.Event(sandbox, corev1.EventTypeWarning, "WarmPodAllocationFailed", 
            fmt.Sprintf("Failed to find matching warm pod: %v", err))
        
        // Update annotation to indicate warm pod was not used
        sandboxCopy = sandbox.DeepCopy()
        sandboxCopy.Annotations["llmsafespace.dev/warm-pod-used"] = "false"
        delete(sandboxCopy.Annotations, "llmsafespace.dev/warm-pod-pending")
        
        _, err = c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).Update(
            context.TODO(), sandboxCopy, metav1.UpdateOptions{})
        if err != nil {
            return err
        }
        
        // Continue with normal sandbox creation
        return c.initializeSandbox(sandbox)
    }
    
    if warmPod == nil {
        c.recorder.Event(sandbox, corev1.EventTypeNormal, "NoWarmPodAvailable", 
            "No matching warm pod available, creating sandbox from scratch")
        
        // Update annotation to indicate warm pod was not used
        sandboxCopy = sandbox.DeepCopy()
        sandboxCopy.Annotations["llmsafespace.dev/warm-pod-used"] = "false"
        delete(sandboxCopy.Annotations, "llmsafespace.dev/warm-pod-pending")
        
        _, err = c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).Update(
            context.TODO(), sandboxCopy, metav1.UpdateOptions{})
        if err != nil {
            return err
        }
        
        // Continue with normal sandbox creation
        return c.initializeSandbox(sandbox)
    }
    
    // Try to claim the warm pod
    claimed, err := c.warmPodAllocator.ClaimWarmPod(sandbox, warmPod)
    if err != nil {
        c.recorder.Event(sandbox, corev1.EventTypeWarning, "WarmPodClaimFailed", 
            fmt.Sprintf("Failed to claim warm pod: %v", err))
        
        // Update annotation to indicate warm pod was not used
        sandboxCopy = sandbox.DeepCopy()
        sandboxCopy.Annotations["llmsafespace.dev/warm-pod-used"] = "false"
        delete(sandboxCopy.Annotations, "llmsafespace.dev/warm-pod-pending")
        
        _, err = c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).Update(
            context.TODO(), sandboxCopy, metav1.UpdateOptions{})
        if err != nil {
            return err
        }
        
        // Continue with normal sandbox creation
        return c.initializeSandbox(sandbox)
    }
    
    if !claimed {
        c.recorder.Event(sandbox, corev1.EventTypeNormal, "WarmPodNotClaimed", 
            "Warm pod was no longer available, creating sandbox from scratch")
        
        // Update annotation to indicate warm pod was not used
        sandboxCopy = sandbox.DeepCopy()
        sandboxCopy.Annotations["llmsafespace.dev/warm-pod-used"] = "false"
        delete(sandboxCopy.Annotations, "llmsafespace.dev/warm-pod-pending")
        
        _, err = c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).Update(
            context.TODO(), sandboxCopy, metav1.UpdateOptions{})
        if err != nil {
            return err
        }
        
        // Continue with normal sandbox creation
        return c.initializeSandbox(sandbox)
    }
    
    // Warm pod was successfully claimed, update sandbox with reference
    sandboxCopy = sandbox.DeepCopy()
    sandboxCopy.Status.WarmPodRef = &llmsafespacev1.WarmPodReference{
        Name: warmPod.Name,
        Namespace: warmPod.Namespace,
    }
    sandboxCopy.Annotations["llmsafespace.dev/warm-pod-used"] = "true"
    delete(sandboxCopy.Annotations, "llmsafespace.dev/warm-pod-pending")
    
    // Update status with warm pod reference
    _, err = c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).UpdateStatus(
        context.TODO(), sandboxCopy, metav1.UpdateOptions{})
    if err != nil {
        return err
    }
    
    c.recorder.Event(sandbox, corev1.EventTypeNormal, "WarmPodAssigned", 
        fmt.Sprintf("Assigned warm pod %s/%s to sandbox", warmPod.Namespace, warmPod.Name))
    
    // Record warm pod usage metric
    warmPoolHitRatio.WithLabelValues(sandbox.Spec.Runtime).Set(1.0)
    
    // Requeue to handle the sandbox with warm pod
    return c.handleSandboxWithWarmPod(sandboxCopy)
}

// handleSandboxWithWarmPod handles a sandbox that's using a warm pod
func (c *Controller) handleSandboxWithWarmPod(sandbox *llmsafespacev1.Sandbox) error {
    // Get the warm pod
    warmPod, err := c.warmPodLister.WarmPods(sandbox.Status.WarmPodRef.Namespace).Get(sandbox.Status.WarmPodRef.Name)
    if err != nil {
        if errors.IsNotFound(err) {
            // Warm pod was deleted, fall back to regular creation
            sandbox.Status.WarmPodRef = nil
            sandbox.Status.Phase = "Pending"
            _, err = c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).UpdateStatus(
                context.TODO(), sandbox, metav1.UpdateOptions{})
            return err
        }
        return err
    }
    
    // Get the underlying pod
    pod, err := c.podLister.Pods(warmPod.Status.PodNamespace).Get(warmPod.Status.PodName)
    if err != nil {
        if errors.IsNotFound(err) {
            // Pod was deleted, fall back to regular creation
            sandbox.Status.WarmPodRef = nil
            sandbox.Status.Phase = "Pending"
            _, err = c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).UpdateStatus(
                context.TODO(), sandbox, metav1.UpdateOptions{})
            return err
        }
        return err
    }
    
    // Update the pod labels and annotations to match the sandbox
    podCopy := pod.DeepCopy()
    if podCopy.Labels == nil {
        podCopy.Labels = make(map[string]string)
    }
    podCopy.Labels["sandbox-id"] = sandbox.Name
    podCopy.Labels["sandbox-uid"] = string(sandbox.UID)
    
    // Add owner reference to the pod
    podCopy.OwnerReferences = []metav1.OwnerReference{
        *metav1.NewControllerRef(sandbox, llmsafespacev1.SchemeGroupVersion.WithKind("Sandbox")),
    }
    
    // Update the pod
    _, err = c.kubeClient.CoreV1().Pods(pod.Namespace).Update(
        context.TODO(), podCopy, metav1.UpdateOptions{})
    if err != nil {
        return err
    }
    
    // Create service for the pod
    service, err := c.serviceManager.EnsureService(sandbox, pod.Namespace, pod.Name)
    if err != nil {
        return err
    }
    
    // Update network policies if needed
    if err := c.networkPolicyManager.EnsureNetworkPolicies(sandbox, pod.Namespace); err != nil {
        return err
    }
    
    // Update sandbox status
    sandbox.Status.Phase = "Running"
    sandbox.Status.PodName = pod.Name
    sandbox.Status.PodNamespace = pod.Namespace
    sandbox.Status.Endpoint = fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, service.Namespace)
    sandbox.Status.StartTime = metav1.Now()
    
    _, err = c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).UpdateStatus(
        context.TODO(), sandbox, metav1.UpdateOptions{})
    
    return err
}
```

### Sandbox State Machine

The Sandbox resource follows a state machine pattern with the following phases:

1. **Pending**: Initial state after creation, validation in progress
2. **Creating**: Resources are being created
3. **Running**: Sandbox is operational
4. **Terminating**: Sandbox is being cleaned up
5. **Terminated**: Sandbox has been successfully terminated
6. **Failed**: Sandbox creation or operation failed

```
┌─────────┐     ┌──────────┐     ┌─────────┐     ┌─────────────┐     ┌───────────┐
│         │     │          │     │         │     │             │     │           │
│ Pending ├────►│ Creating ├────►│ Running ├────►│ Terminating ├────►│ Terminated│
│         │     │          │     │         │     │             │     │           │
└────┬────┘     └────┬─────┘     └────┬────┘     └──────┬──────┘     └───────────┘
     │               │                │                 │
     │               │                │                 │
     ▼               ▼                ▼                 ▼
┌─────────┐     ┌─────────┐     ┌─────────┐     ┌─────────┐
│         │     │         │     │         │     │         │
│ Failed  │     │ Failed  │     │ Failed  │     │ Failed  │
│         │     │         │     │         │     │         │
└─────────┘     └─────────┘     └─────────┘     └─────────┘
```

## 2. WarmPool Reconciliation Loop

The WarmPool reconciliation loop manages the lifecycle of warm pools, ensuring that the desired number of warm pods are available.

```go
// reconcileWarmPool ensures the actual state of a warm pool matches the desired state
func (c *Controller) reconcileWarmPool(key string) error {
    startTime := time.Now()
    defer func() {
        reconciliationDurationSeconds.WithLabelValues("warmpool", "success").Observe(time.Since(startTime).Seconds())
    }()
    
    namespace, name, err := cache.SplitMetaNamespaceKey(key)
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("warmpool", "key_split").Inc()
        return err
    }
    
    // Get WarmPool resource
    warmPool, err := c.warmPoolLister.WarmPools(namespace).Get(name)
    if errors.IsNotFound(err) {
        // WarmPool was deleted, nothing to do
        klog.V(4).Infof("WarmPool %s/%s not found, likely deleted", namespace, name)
        return nil
    }
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("warmpool", "get").Inc()
        return err
    }
    
    // Deep copy to avoid modifying cache
    warmPool = warmPool.DeepCopy()
    
    // Add finalizer if not present
    if !containsString(warmPool.Finalizers, warmPoolFinalizer) {
        warmPool.Finalizers = append(warmPool.Finalizers, warmPoolFinalizer)
        warmPool, err = c.llmsafespaceClient.LlmsafespaceV1().WarmPools(namespace).Update(context.TODO(), warmPool, metav1.UpdateOptions{})
        if err != nil {
            reconciliationErrorsTotal.WithLabelValues("warmpool", "update_finalizer").Inc()
            return err
        }
    }
    
    // Check if warm pool is being deleted
    if !warmPool.DeletionTimestamp.IsZero() {
        return c.handleWarmPoolDeletion(warmPool)
    }
    
    // Validate the warm pool configuration
    if err := c.validateWarmPool(warmPool); err != nil {
        c.updateWarmPoolStatus(warmPool, "ValidationFailed", fmt.Sprintf("Warm pool validation failed: %v", err))
        reconciliationErrorsTotal.WithLabelValues("warmpool", "validation").Inc()
        return err
    }
    
    // List all warm pods for this pool
    selector := labels.SelectorFromSet(labels.Set{
        "app": "llmsafespace",
        "component": "warmpod",
        "pool": warmPool.Name,
    })
    warmPods, err := c.warmPodLister.WarmPods(namespace).List(selector)
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("warmpool", "list_pods").Inc()
        return err
    }
    
    // Count pods by status
    var availablePods, assignedPods, pendingPods int
    for _, pod := range warmPods {
        switch pod.Status.Phase {
        case "Ready":
            availablePods++
        case "Assigned":
            assignedPods++
        case "Pending":
            pendingPods++
        }
    }
    
    // Update status
    warmPool.Status.AvailablePods = availablePods
    warmPool.Status.AssignedPods = assignedPods
    warmPool.Status.PendingPods = pendingPods
    
    // Calculate utilization for metrics
    totalPods := availablePods + assignedPods + pendingPods
    if totalPods > 0 {
        utilization := float64(assignedPods) / float64(totalPods)
        warmPoolUtilizationGauge.WithLabelValues(warmPool.Name, warmPool.Spec.Runtime).Set(utilization)
    }
    
    // Update warm pool metrics
    warmPoolSizeGauge.WithLabelValues(warmPool.Name, warmPool.Spec.Runtime, "available").Set(float64(availablePods))
    warmPoolSizeGauge.WithLabelValues(warmPool.Name, warmPool.Spec.Runtime, "assigned").Set(float64(assignedPods))
    warmPoolSizeGauge.WithLabelValues(warmPool.Name, warmPool.Spec.Runtime, "pending").Set(float64(pendingPods))
    
    // Scale up if needed
    if availablePods < warmPool.Spec.MinSize {
        neededPods := warmPool.Spec.MinSize - availablePods
        klog.V(2).Infof("Scaling up warm pool %s/%s: creating %d pods", namespace, name, neededPods)
        
        for i := 0; i < neededPods; i++ {
            if err := c.createWarmPod(warmPool); err != nil {
                reconciliationErrorsTotal.WithLabelValues("warmpool", "create_pod").Inc()
                return err
            }
        }
        warmPool.Status.LastScaleTime = metav1.Now()
        warmPoolScalingOperationsTotal.WithLabelValues(warmPool.Name, warmPool.Spec.Runtime, "up").Inc()
    }
    
    // Scale down if needed and autoscaling is enabled
    if warmPool.Spec.AutoScaling != nil && warmPool.Spec.AutoScaling.Enabled {
        maxSize := warmPool.Spec.MaxSize
        if maxSize > 0 && availablePods > maxSize {
            // Calculate how many pods to remove
            excessPods := availablePods - maxSize
            klog.V(2).Infof("Scaling down warm pool %s/%s: removing %d pods", namespace, name, excessPods)
            
            // Find oldest pods to remove
            if err := c.scaleDownWarmPool(warmPool, excessPods); err != nil {
                reconciliationErrorsTotal.WithLabelValues("warmpool", "scale_down").Inc()
                return err
            }
            
            warmPool.Status.LastScaleTime = metav1.Now()
            warmPoolScalingOperationsTotal.WithLabelValues(warmPool.Name, warmPool.Spec.Runtime, "down").Inc()
        }
        
        // Check if we should scale based on utilization
        if warmPool.Spec.AutoScaling.TargetUtilization > 0 && totalPods > 0 {
            utilization := float64(assignedPods) / float64(totalPods) * 100
            targetUtilization := float64(warmPool.Spec.AutoScaling.TargetUtilization)
            
            // If utilization is too high, scale up (if not at max size)
            if utilization > targetUtilization && (maxSize == 0 || totalPods < maxSize) {
                // Calculate how many pods to add based on utilization
                desiredTotal := int(math.Ceil(float64(assignedPods) / (targetUtilization / 100)))
                podsToAdd := desiredTotal - totalPods
                
                // Limit to max size if specified
                if maxSize > 0 && totalPods+podsToAdd > maxSize {
                    podsToAdd = maxSize - totalPods
                }
                
                if podsToAdd > 0 {
                    klog.V(2).Infof("Auto-scaling up warm pool %s/%s: adding %d pods due to high utilization (%.2f%%)", 
                        namespace, name, podsToAdd, utilization)
                    
                    for i := 0; i < podsToAdd; i++ {
                        if err := c.createWarmPod(warmPool); err != nil {
                            reconciliationErrorsTotal.WithLabelValues("warmpool", "autoscale_up").Inc()
                            return err
                        }
                    }
                    
                    warmPool.Status.LastScaleTime = metav1.Now()
                    warmPoolScalingOperationsTotal.WithLabelValues(warmPool.Name, warmPool.Spec.Runtime, "autoscale_up").Inc()
                }
            }
            
            // If utilization is too low, scale down (if above min size and after scale down delay)
            scaleDownDelay := 300 // Default 5 minutes
            if warmPool.Spec.AutoScaling.ScaleDownDelay > 0 {
                scaleDownDelay = warmPool.Spec.AutoScaling.ScaleDownDelay
            }
            
            lastScaleTime := warmPool.Status.LastScaleTime.Time
            if utilization < targetUtilization && availablePods > warmPool.Spec.MinSize && 
               time.Since(lastScaleTime) > time.Duration(scaleDownDelay)*time.Second {
                
                // Calculate how many pods to remove based on utilization
                desiredAvailable := int(math.Ceil(float64(assignedPods) / (targetUtilization / 100))) - assignedPods
                podsToRemove := availablePods - desiredAvailable
                
                // Ensure we don't go below min size
                if availablePods-podsToRemove < warmPool.Spec.MinSize {
                    podsToRemove = availablePods - warmPool.Spec.MinSize
                }
                
                if podsToRemove > 0 {
                    klog.V(2).Infof("Auto-scaling down warm pool %s/%s: removing %d pods due to low utilization (%.2f%%)", 
                        namespace, name, podsToRemove, utilization)
                    
                    if err := c.scaleDownWarmPool(warmPool, podsToRemove); err != nil {
                        reconciliationErrorsTotal.WithLabelValues("warmpool", "autoscale_down").Inc()
                        return err
                    }
                    
                    warmPool.Status.LastScaleTime = metav1.Now()
                    warmPoolScalingOperationsTotal.WithLabelValues(warmPool.Name, warmPool.Spec.Runtime, "autoscale_down").Inc()
                }
            }
        }
    }
    
    // Check for expired pods
    if warmPool.Spec.TTL > 0 {
        if err := c.cleanupExpiredWarmPods(warmPool); err != nil {
            reconciliationErrorsTotal.WithLabelValues("warmpool", "cleanup_expired").Inc()
            return err
        }
    }
    
    // Update status
    _, err = c.llmsafespaceClient.LlmsafespaceV1().WarmPools(namespace).UpdateStatus(
        context.TODO(), warmPool, metav1.UpdateOptions{})
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("warmpool", "update_status").Inc()
        return err
    }
    
    return nil
}
```

## 3. WarmPod Reconciliation Loop

The WarmPod reconciliation loop manages the lifecycle of individual warm pods.

```go
// reconcileWarmPod ensures the actual state of a warm pod matches the desired state
func (c *Controller) reconcileWarmPod(key string) error {
    namespace, name, err := cache.SplitMetaNamespaceKey(key)
    if err != nil {
        return err
    }
    
    // Get WarmPod resource
    warmPod, err := c.warmPodLister.WarmPods(namespace).Get(name)
    if errors.IsNotFound(err) {
        // WarmPod was deleted, nothing to do
        return nil
    }
    if err != nil {
        return err
    }
    
    // Deep copy to avoid modifying cache
    warmPod = warmPod.DeepCopy()
    
    // Check if pod exists
    pod, err := c.podLister.Pods(warmPod.Status.PodNamespace).Get(warmPod.Status.PodName)
    if errors.IsNotFound(err) {
        // Pod was deleted, update status
        warmPod.Status.Phase = "Terminating"
        _, err = c.llmsafespaceClient.LlmsafespaceV1().WarmPods(namespace).UpdateStatus(
            context.TODO(), warmPod, metav1.UpdateOptions{})
        return err
    }
    if err != nil {
        return err
    }
    
    // Update status based on pod status
    if pod.Status.Phase == corev1.PodRunning {
        // Check readiness
        isReady := isPodReady(pod)
        
        if isReady && warmPod.Status.Phase == "Pending" {
            warmPod.Status.Phase = "Ready"
            warmPod.Spec.LastHeartbeat = metav1.Now()
            
            // Run any preload scripts if this is the first time the pod is ready
            if err := c.runPreloadScripts(warmPod, pod); err != nil {
                return err
            }
        }
    } else if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
        // Pod is no longer usable
        warmPod.Status.Phase = "Terminating"
    }
    
    // Update status
    _, err = c.llmsafespaceClient.LlmsafespaceV1().WarmPods(namespace).UpdateStatus(
        context.TODO(), warmPod, metav1.UpdateOptions{})
    
    return err
}
```

## 4. SandboxProfile Reconciliation Loop

The SandboxProfile reconciliation loop ensures that profile resources are properly validated and available for use by Sandboxes.

```go
func (c *Controller) reconcileSandboxProfile(key string) error {
    startTime := time.Now()
    defer func() {
        reconciliationDurationSeconds.WithLabelValues("sandboxprofile", "success").Observe(time.Since(startTime).Seconds())
    }()
    
    namespace, name, err := cache.SplitMetaNamespaceKey(key)
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("sandboxprofile", "key_split").Inc()
        return err
    }
    
    // Get SandboxProfile resource
    profile, err := c.profileLister.SandboxProfiles(namespace).Get(name)
    if errors.IsNotFound(err) {
        // Profile was deleted, nothing to do
        klog.V(4).Infof("SandboxProfile %s/%s not found, likely deleted", namespace, name)
        return nil
    }
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("sandboxprofile", "get").Inc()
        return err
    }
    
    // Deep copy to avoid modifying cache
    profile = profile.DeepCopy()
    
    // Add finalizer if not present
    if !containsString(profile.Finalizers, sandboxProfileFinalizer) {
        profile.Finalizers = append(profile.Finalizers, sandboxProfileFinalizer)
        profile, err = c.llmsafespaceClient.LlmsafespaceV1().SandboxProfiles(namespace).Update(context.TODO(), profile, metav1.UpdateOptions{})
        if err != nil {
            reconciliationErrorsTotal.WithLabelValues("sandboxprofile", "update_finalizer").Inc()
            return err
        }
    }
    
    // Check if profile is being deleted
    if !profile.DeletionTimestamp.IsZero() {
        return c.handleSandboxProfileDeletion(profile)
    }
    
    // Basic validation
    if err := c.validateSandboxProfile(profile); err != nil {
        c.updateProfileStatus(profile, "ValidationFailed", fmt.Sprintf("Profile validation failed: %v", err), false)
        reconciliationErrorsTotal.WithLabelValues("sandboxprofile", "validation").Inc()
        return err
    }
    
    // Validate against available runtime environments
    if err := c.validateProfileWithRuntimes(profile); err != nil {
        c.updateProfileStatus(profile, "RuntimeValidationFailed", fmt.Sprintf("Profile is not compatible with any runtime: %v", err), false)
        reconciliationErrorsTotal.WithLabelValues("sandboxprofile", "runtime_validation").Inc()
        return err
    }
    
    // Validate security policies
    if err := c.validateProfileSecurityPolicies(profile); err != nil {
        c.updateProfileStatus(profile, "SecurityValidationFailed", fmt.Sprintf("Profile security validation failed: %v", err), false)
        reconciliationErrorsTotal.WithLabelValues("sandboxprofile", "security_validation").Inc()
        return err
    }
    
    // Validate network policies
    if err := c.validateProfileNetworkPolicies(profile); err != nil {
        c.updateProfileStatus(profile, "NetworkValidationFailed", fmt.Sprintf("Profile network policy validation failed: %v", err), false)
        reconciliationErrorsTotal.WithLabelValues("sandboxprofile", "network_validation").Inc()
        return err
    }
    
    // Validate resource defaults
    if err := c.validateProfileResourceDefaults(profile); err != nil {
        c.updateProfileStatus(profile, "ResourceValidationFailed", fmt.Sprintf("Profile resource defaults validation failed: %v", err), false)
        reconciliationErrorsTotal.WithLabelValues("sandboxprofile", "resource_validation").Inc()
        return err
    }
    
    // Validate pre-installed packages
    if err := c.validateProfilePackages(profile); err != nil {
        c.updateProfileStatus(profile, "PackageValidationFailed", fmt.Sprintf("Profile package validation failed: %v", err), false)
        reconciliationErrorsTotal.WithLabelValues("sandboxprofile", "package_validation").Inc()
        return err
    }
    
    // Update status to valid
    c.updateProfileStatus(profile, "ValidationSucceeded", "Profile is valid and ready to use", true)
    
    // Record metric for successful validation
    sandboxProfileValidationsTotal.WithLabelValues(profile.Spec.Language, profile.Spec.SecurityLevel).Inc()
    
    return nil
}
```

## 5. RuntimeEnvironment Reconciliation Loop

The RuntimeEnvironment reconciliation loop validates runtime environments and ensures they are available for use.

```go
func (c *Controller) reconcileRuntimeEnvironment(key string) error {
    startTime := time.Now()
    defer func() {
        reconciliationDurationSeconds.WithLabelValues("runtimeenvironment", "success").Observe(time.Since(startTime).Seconds())
    }()
    
    _, name, err := cache.SplitMetaNamespaceKey(key)
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("runtimeenvironment", "key_split").Inc()
        return err
    }
    
    // Get RuntimeEnvironment resource
    runtime, err := c.runtimeLister.Get(name)
    if errors.IsNotFound(err) {
        // Runtime was deleted, nothing to do
        klog.V(4).Infof("RuntimeEnvironment %s not found, likely deleted", name)
        return nil
    }
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("runtimeenvironment", "get").Inc()
        return err
    }
    
    // Deep copy to avoid modifying cache
    runtime = runtime.DeepCopy()
    
    // Add finalizer if not present
    if !containsString(runtime.Finalizers, runtimeEnvironmentFinalizer) {
        runtime.Finalizers = append(runtime.Finalizers, runtimeEnvironmentFinalizer)
        runtime, err = c.llmsafespaceClient.LlmsafespaceV1().RuntimeEnvironments().Update(context.TODO(), runtime, metav1.UpdateOptions{})
        if err != nil {
            reconciliationErrorsTotal.WithLabelValues("runtimeenvironment", "update_finalizer").Inc()
            return err
        }
    }
    
    // Check if runtime is being deleted
    if !runtime.DeletionTimestamp.IsZero() {
        return c.handleRuntimeEnvironmentDeletion(runtime)
    }
    
    // Perform comprehensive validation
    if err := c.validateRuntimeImage(runtime); err != nil {
        c.updateRuntimeStatus(runtime, false, "ImageValidationFailed", fmt.Sprintf("Runtime image validation failed: %v", err))
        reconciliationErrorsTotal.WithLabelValues("runtimeenvironment", "image_validation").Inc()
        return err
    }
    
    // Validate compatibility with system
    if err := c.validateRuntimeCompatibility(runtime); err != nil {
        c.updateRuntimeStatus(runtime, false, "CompatibilityCheckFailed", fmt.Sprintf("Runtime compatibility check failed: %v", err))
        reconciliationErrorsTotal.WithLabelValues("runtimeenvironment", "compatibility").Inc()
        return err
    }
    
    // Validate security features
    if err := c.validateRuntimeSecurity(runtime); err != nil {
        c.updateRuntimeStatus(runtime, false, "SecurityValidationFailed", fmt.Sprintf("Runtime security validation failed: %v", err))
        reconciliationErrorsTotal.WithLabelValues("runtimeenvironment", "security").Inc()
        return err
    }
    
    // Update status to available
    c.updateRuntimeStatus(runtime, true, "ValidationSucceeded", "Runtime environment is valid and available")
    
    // Record metric for successful validation
    runtimeEnvironmentValidationsTotal.WithLabelValues(runtime.Spec.Language, runtime.Spec.Version, "success").Inc()
    
    return nil
}
```
