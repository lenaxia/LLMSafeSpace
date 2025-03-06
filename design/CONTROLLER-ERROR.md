# Error Handling and Recovery

## Transient Errors

For transient errors, the controller uses exponential backoff and retries.

```go
func (c *Controller) processNextWorkItem() bool {
    obj, shutdown := c.workqueue.Get()
    if shutdown {
        return false
    }
    
    err := func(obj interface{}) error {
        defer c.workqueue.Done(obj)
        
        var key string
        var ok bool
        if key, ok = obj.(string); !ok {
            c.workqueue.Forget(obj)
            return fmt.Errorf("expected string in workqueue but got %#v", obj)
        }
        
        err := c.reconcileSandbox(key)
        if err != nil {
            // Check if we should requeue
            if shouldRequeue(err) {
                c.workqueue.AddRateLimited(key)
                return fmt.Errorf("error reconciling sandbox '%s': %s, requeuing", key, err.Error())
            }
            
            // Don't requeue for permanent errors
            c.workqueue.Forget(obj)
            return fmt.Errorf("error reconciling sandbox '%s': %s, not requeuing", key, err.Error())
        }
        
        c.workqueue.Forget(obj)
        return nil
    }(obj)
    
    if err != nil {
        klog.Errorf("Error processing item: %v", err)
    }
    
    return true
}

func shouldRequeue(err error) bool {
    // Check for specific error types that should be requeued
    if errors.IsServerTimeout(err) || errors.IsTimeout(err) || errors.IsTooManyRequests(err) {
        return true
    }
    
    // Check for network errors
    if isNetworkError(err) {
        return true
    }
    
    // Check for resource conflict errors
    if errors.IsConflict(err) {
        return true
    }
    
    return false
}
```

## Permanent Errors

For permanent errors, the controller updates the Sandbox status to Failed and records an event.

```go
func (c *Controller) updateSandboxStatus(sandbox *llmsafespacev1.Sandbox, phase, reason, message string) error {
    // Deep copy to avoid modifying cache
    sandbox = sandbox.DeepCopy()
    
    // Update status
    sandbox.Status.Phase = phase
    
    // Add condition
    now := metav1.Now()
    condition := llmsafespacev1.SandboxCondition{
        Type: reason,
        Status: "True",
        Reason: reason,
        Message: message,
        LastTransitionTime: now,
    }
    
    // Check if condition already exists
    for i, cond := range sandbox.Status.Conditions {
        if cond.Type == reason {
            if cond.Status == "True" && cond.Message == message {
                // Condition already exists with same status and message
                return nil
            }
            // Update existing condition
            sandbox.Status.Conditions[i] = condition
            _, err := c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).UpdateStatus(
                context.TODO(), sandbox, metav1.UpdateOptions{})
            
            // Record event
            c.recorder.Event(sandbox, corev1.EventTypeWarning, reason, message)
            
            return err
        }
    }
    
    // Add new condition
    sandbox.Status.Conditions = append(sandbox.Status.Conditions, condition)
    
    _, err := c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).UpdateStatus(
        context.TODO(), sandbox, metav1.UpdateOptions{})
    
    // Record event
    if phase == "Failed" {
        c.recorder.Event(sandbox, corev1.EventTypeWarning, reason, message)
    } else {
        c.recorder.Event(sandbox, corev1.EventTypeNormal, reason, message)
    }
    
    return err
}
```
