# Warm Pod Management

The controller includes functionality for managing warm pods, including creation, assignment, and recycling.

## WarmPodAllocator

```go
// WarmPodAllocator handles allocation of warm pods to sandboxes
type WarmPodAllocator struct {
    llmsafespaceClient clientset.Interface
    warmPoolLister     listers.WarmPoolLister
    warmPodLister      listers.WarmPodLister
    podLister          corelisters.PodLister
    recorder           record.EventRecorder
}

// FindMatchingWarmPod finds a warm pod that matches the requirements
func (a *WarmPodAllocator) FindMatchingWarmPod(sandbox *llmsafespacev1.Sandbox) (*llmsafespacev1.WarmPod, error) {
    // Find warm pools that match the runtime
    runtime := sandbox.Spec.Runtime
    selector := labels.SelectorFromSet(labels.Set{
        "runtime": strings.Replace(runtime, ":", "-", -1),
    })
    
    pools, err := a.warmPoolLister.List(selector)
    if err != nil {
        return nil, err
    }
    
    // No matching pools
    if len(pools) == 0 {
        return nil, nil
    }
    
    // Find the best matching pool
    var bestPool *llmsafespacev1.WarmPool
    for i, pool := range pools {
        if pool.Status.AvailablePods > 0 {
            // Check if security level matches
            if pool.Spec.SecurityLevel == sandbox.Spec.SecurityLevel {
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
        return nil, nil
    }
    
    // Find an available warm pod from the pool
    podSelector := labels.SelectorFromSet(labels.Set{
        "app": "llmsafespace",
        "component": "warmpod",
        "pool": bestPool.Name,
    })
    
    warmPods, err := a.warmPodLister.WarmPods(bestPool.Namespace).List(podSelector)
    if err != nil {
        return nil, err
    }
    
    // Find a ready pod
    for _, pod := range warmPods {
        if pod.Status.Phase == "Ready" {
            return &pod, nil
        }
    }
    
    return nil, nil
}
```

## Pod Recycling

The controller implements pod recycling to reuse warm pods after a sandbox is deleted:

```go
// shouldRecyclePod determines if a warm pod should be recycled
func (c *Controller) shouldRecyclePod(warmPod *llmsafespacev1.WarmPod, sandbox *llmsafespacev1.Sandbox) bool {
    startTime := time.Now()
    defer func() {
        warmPodRecycleDecisionDurationSeconds.Observe(time.Since(startTime).Seconds())
    }()
    
    // Get the pool
    pool, err := c.warmPoolLister.WarmPools(warmPod.Spec.PoolRef.Namespace).Get(warmPod.Spec.PoolRef.Name)
    if err != nil {
        klog.Warningf("Failed to get warm pool %s/%s: %v", 
            warmPod.Spec.PoolRef.Namespace, warmPod.Spec.PoolRef.Name, err)
        warmPodRecycleDecisionsTotal.WithLabelValues("pool_not_found", "false").Inc()
        return false
    }
    
    // Check if recycling is disabled for this pool
    if pool.Spec.DisablePodRecycling {
        klog.V(4).Infof("Pod recycling is disabled for pool %s, not recycling pod %s", 
            pool.Name, warmPod.Status.PodName)
        warmPodRecycleDecisionsTotal.WithLabelValues("recycling_disabled", "false").Inc()
        return false
    }
    
    // Check if the pool still exists and needs more pods
    if pool.Status.AvailablePods < pool.Spec.MinSize {
        // Check if the pod has been running for too long
        maxPodAge := 24 * time.Hour
        if pool.Spec.MaxPodAge > 0 {
            maxPodAge = time.Duration(pool.Spec.MaxPodAge) * time.Second
        }
        
        if warmPod.Spec.CreationTimestamp.Add(maxPodAge).Before(time.Now()) {
            klog.V(4).Infof("Pod %s is too old (>%v), not recycling", 
                warmPod.Status.PodName, maxPodAge)
            warmPodRecycleDecisionsTotal.WithLabelValues("pod_too_old", "false").Inc()
            return false
        }
        
        // Check if the sandbox has security events that would make recycling unsafe
        if c.hasSandboxSecurityEvents(sandbox) {
            klog.V(2).Infof("Sandbox %s has security events, not recycling pod %s", 
                sandbox.Name, warmPod.Status.PodName)
            warmPodRecycleDecisionsTotal.WithLabelValues("security_events", "false").Inc()
            
            // Record security event metric
            securityEventsTotal.WithLabelValues("sandbox", "recycle_blocked").Inc()
            
            // Add annotation to track security event
            if sandbox.Annotations == nil {
                sandbox.Annotations = make(map[string]string)
            }
            sandbox.Annotations["llmsafespace.dev/security-event-timestamp"] = time.Now().Format(time.RFC3339)
            sandbox.Annotations["llmsafespace.dev/security-event-type"] = "recycle_blocked"
            
            // Update the sandbox with the new annotations
            _, err := c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).Update(
                context.TODO(), sandbox, metav1.UpdateOptions{})
            if err != nil {
                klog.Warningf("Failed to update sandbox %s with security event annotation: %v", 
                    sandbox.Name, err)
            }
            
            return false
        }
        
        // Check if the sandbox installed packages
        untrustedPackages, hasUntrusted := c.hasInstalledUntrustedPackages(sandbox)
        if hasUntrusted {
            klog.V(2).Infof("Sandbox %s installed untrusted packages (%s), not recycling pod %s", 
                sandbox.Name, strings.Join(untrustedPackages, ", "), warmPod.Status.PodName)
            warmPodRecycleDecisionsTotal.WithLabelValues("untrusted_packages", "false").Inc()
            return false
        }
        
        // Check if the sandbox modified system files
        modifiedFiles, hasModified := c.hasModifiedSystemFiles(sandbox)
        if hasModified {
            klog.V(2).Infof("Sandbox %s modified system files (%s), not recycling pod %s", 
                sandbox.Name, strings.Join(modifiedFiles, ", "), warmPod.Status.PodName)
            warmPodRecycleDecisionsTotal.WithLabelValues("modified_system_files", "false").Inc()
            return false
        }
        
        // Check resource usage history
        if excessive, reason := c.hadExcessiveResourceUsage(sandbox); excessive {
            klog.V(2).Infof("Sandbox %s had excessive resource usage (%s), not recycling pod %s", 
                sandbox.Name, reason, warmPod.Status.PodName)
            warmPodRecycleDecisionsTotal.WithLabelValues("excessive_resource_usage", "false").Inc()
            return false
        }
        
        // Check if the pod has been recycled too many times
        recycleCount := c.getPodRecycleCount(warmPod)
        maxRecycleCount := c.config.MaxPodRecycleCount
        if pool.Spec.MaxPodRecycleCount > 0 {
            maxRecycleCount = pool.Spec.MaxPodRecycleCount
        }
        
        if recycleCount >= maxRecycleCount {
            klog.V(2).Infof("Pod %s has been recycled %d times (max: %d), not recycling", 
                warmPod.Status.PodName, recycleCount, maxRecycleCount)
            warmPodRecycleDecisionsTotal.WithLabelValues("max_recycle_count_reached", "false").Inc()
            return false
        }
        
        // Check if the pod has been running for the minimum time before recycling
        minRuntime := c.config.MinPodRuntimeBeforeRecycle
        if pool.Spec.MinPodRuntimeBeforeRecycle > 0 {
            minRuntime = pool.Spec.MinPodRuntimeBeforeRecycle
        }
        
        if minRuntime > 0 {
            podRuntime := time.Since(warmPod.Spec.CreationTimestamp.Time)
            if podRuntime < time.Duration(minRuntime)*time.Second {
                klog.V(4).Infof("Pod %s has only been running for %v (min: %v), not recycling", 
                    warmPod.Status.PodName, podRuntime, time.Duration(minRuntime)*time.Second)
                warmPodRecycleDecisionsTotal.WithLabelValues("min_runtime_not_reached", "false").Inc()
                return false
            }
        }
        
        // Check if the pod has any active processes that would make recycling unsafe
        if c.hasActiveProcesses(warmPod) {
            klog.V(2).Infof("Pod %s has active processes, not recycling", warmPod.Status.PodName)
            warmPodRecycleDecisionsTotal.WithLabelValues("active_processes", "false").Inc()
            return false
        }
        
        // Check if the pod has network connections that would make recycling unsafe
        if c.hasActiveNetworkConnections(warmPod) {
            klog.V(2).Infof("Pod %s has active network connections, not recycling", warmPod.Status.PodName)
            warmPodRecycleDecisionsTotal.WithLabelValues("active_network_connections", "false").Inc()
            return false
        }
        
        // Check if the pod has any file locks that would make recycling unsafe
        if c.hasFileLocks(warmPod) {
            klog.V(2).Infof("Pod %s has file locks, not recycling", warmPod.Status.PodName)
            warmPodRecycleDecisionsTotal.WithLabelValues("file_locks", "false").Inc()
            return false
        }
        
        // Check if the pod has any memory leaks that would make recycling unsafe
        if c.hasMemoryLeaks(warmPod) {
            klog.V(2).Infof("Pod %s has potential memory leaks, not recycling", warmPod.Status.PodName)
            warmPodRecycleDecisionsTotal.WithLabelValues("memory_leaks", "false").Inc()
            return false
        }
        
        // All checks passed, pod is eligible for recycling
        klog.V(4).Infof("Pod %s is eligible for recycling", warmPod.Status.PodName)
        warmPodRecycleDecisionsTotal.WithLabelValues("eligible", "true").Inc()
        return true
    }
    
    klog.V(4).Infof("Pool %s has sufficient pods, not recycling pod %s", 
        pool.Name, warmPod.Status.PodName)
    warmPodRecycleDecisionsTotal.WithLabelValues("sufficient_pods", "false").Inc()
    return false
}

// recyclePod recycles a warm pod for reuse
func (c *Controller) recyclePod(warmPod *llmsafespacev1.WarmPod, sandbox *llmsafespacev1.Sandbox) error {
    startTime := time.Now()
    
    // Get the pod
    pod, err := c.podLister.Pods(warmPod.Status.PodNamespace).Get(warmPod.Status.PodName)
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("warmpod", "get_pod").Inc()
        return err
    }
    
    klog.V(2).Infof("Recycling pod %s from sandbox %s", pod.Name, sandbox.Name)
    
    // Update the pod labels to remove sandbox association
    podCopy := pod.DeepCopy()
    delete(podCopy.Labels, "sandbox-id")
    delete(podCopy.Labels, "sandbox-uid")
    
    // Add recycling annotation to track history
    if podCopy.Annotations == nil {
        podCopy.Annotations = make(map[string]string)
    }
    
    // Increment recycle count
    recycleCount := 0
    if countStr, ok := podCopy.Annotations["llmsafespace.dev/recycle-count"]; ok {
        if count, err := strconv.Atoi(countStr); err == nil {
            recycleCount = count
        }
    }
    podCopy.Annotations["llmsafespace.dev/recycle-count"] = strconv.Itoa(recycleCount + 1)
    podCopy.Annotations["llmsafespace.dev/last-recycled"] = time.Now().Format(time.RFC3339)
    podCopy.Annotations["llmsafespace.dev/last-sandbox"] = sandbox.Name
    
    // Remove owner reference to the sandbox
    var newOwnerRefs []metav1.OwnerReference
    for _, ref := range podCopy.OwnerReferences {
        if ref.UID != sandbox.UID {
            newOwnerRefs = append(newOwnerRefs, ref)
        }
    }
    podCopy.OwnerReferences = newOwnerRefs
    
    // Update the pod
    _, err = c.kubeClient.CoreV1().Pods(pod.Namespace).Update(
        context.TODO(), podCopy, metav1.UpdateOptions{})
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("warmpod", "update_pod").Inc()
        return err
    }
    
    // Clean up the pod environment
    if err := c.cleanupPodEnvironment(pod); err != nil {
        reconciliationErrorsTotal.WithLabelValues("warmpod", "cleanup").Inc()
        
        // Log the error but continue with recycling
        klog.Warningf("Error cleaning up pod environment: %v", err)
        
        // Record failed recycling metric
        warmPoolRecycleTotal.WithLabelValues(
            warmPod.Spec.PoolRef.Name,
            strings.Split(sandbox.Spec.Runtime, ":")[0],
            "false",
        ).Inc()
        
        // If cleanup fails, we should not recycle the pod
        return fmt.Errorf("failed to clean up pod environment: %v", err)
    }
    
    // Run verification checks to ensure the pod is clean
    if err := c.verifyPodCleanup(pod); err != nil {
        reconciliationErrorsTotal.WithLabelValues("warmpod", "verify_cleanup").Inc()
        
        klog.Warningf("Pod cleanup verification failed: %v", err)
        
        // Record failed recycling metric
        warmPoolRecycleTotal.WithLabelValues(
            warmPod.Spec.PoolRef.Name,
            strings.Split(sandbox.Spec.Runtime, ":")[0],
            "false",
        ).Inc()
        
        return fmt.Errorf("pod cleanup verification failed: %v", err)
    }
    
    // Reinitialize the pod if needed
    if err := c.reinitializePod(pod, warmPod); err != nil {
        reconciliationErrorsTotal.WithLabelValues("warmpod", "reinitialize").Inc()
        return err
    }
    
    // Update warm pod status
    warmPodCopy := warmPod.DeepCopy()
    warmPodCopy.Status.Phase = "Ready"
    warmPodCopy.Status.AssignedTo = ""
    warmPodCopy.Status.AssignedAt = metav1.Time{}
    warmPodCopy.Spec.LastHeartbeat = metav1.Now()
    
    // Add annotations to track recycling history
    if warmPodCopy.Annotations == nil {
        warmPodCopy.Annotations = make(map[string]string)
    }
    warmPodCopy.Annotations["llmsafespace.dev/last-recycled"] = time.Now().Format(time.RFC3339)
    warmPodCopy.Annotations["llmsafespace.dev/last-sandbox"] = sandbox.Name
    
    _, err = c.llmsafespaceClient.LlmsafespaceV1().WarmPods(warmPod.Namespace).Update(
        context.TODO(), warmPodCopy, metav1.UpdateOptions{})
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("warmpod", "update_status").Inc()
        return err
    }
    
    // Record successful recycling metric
    warmPoolRecycleTotal.WithLabelValues(
        warmPod.Spec.PoolRef.Name,
        strings.Split(sandbox.Spec.Runtime, ":")[0],
        "true",
    ).Inc()
    
    // Record recycling duration
    warmPoolRecycleDurationSeconds.WithLabelValues(
        warmPod.Spec.PoolRef.Name,
        strings.Split(sandbox.Spec.Runtime, ":")[0],
    ).Observe(time.Since(startTime).Seconds())
    
    c.recorder.Event(warmPod, corev1.EventTypeNormal, "PodRecycled", 
        fmt.Sprintf("Pod %s successfully recycled from sandbox %s", pod.Name, sandbox.Name))
    
    klog.V(2).Infof("Successfully recycled pod %s from sandbox %s in %v", 
        pod.Name, sandbox.Name, time.Since(startTime))
    
    return nil
}
```
