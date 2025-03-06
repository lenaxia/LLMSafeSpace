# High Availability and Scaling

## 1. Leader Election

For high availability, the controller supports leader election to ensure only one instance is active at a time.

```go
func setupLeaderElection(kubeClient kubernetes.Interface, controller *Controller) {
    // Create a new resource lock
    lock := &resourcelock.LeaseLock{
        LeaseMeta: metav1.ObjectMeta{
            Name: "llmsafespace-controller",
            Namespace: "llmsafespace",
        },
        Client: kubeClient.CoordinationV1(),
        LeaseDuration: 15 * time.Second,
        RenewDeadline: 10 * time.Second,
        RetryPeriod: 2 * time.Second,
    }
    
    // Get hostname for leader identity
    hostname, err := os.Hostname()
    if err != nil {
        klog.Fatalf("Unable to get hostname: %v", err)
    }
    id := hostname + "_" + string(uuid.NewUUID())
    
    // Start leader election
    leaderelection.RunOrDie(context.Background(), leaderelection.LeaderElectionConfig{
        Lock: lock,
        ReleaseOnCancel: true,
        LeaseDuration: 15 * time.Second,
        RenewDeadline: 10 * time.Second,
        RetryPeriod: 2 * time.Second,
        Callbacks: leaderelection.LeaderCallbacks{
            OnStartedLeading: func(ctx context.Context) {
                klog.Infof("Started leading as %s", id)
                controller.Run(*workers, ctx.Done())
            },
            OnStoppedLeading: func() {
                klog.Infof("Leader election lost for %s", id)
                // Perform graceful shutdown
                if err := controller.Shutdown(); err != nil {
                    klog.Errorf("Error during controller shutdown: %v", err)
                }
                os.Exit(0)
            },
            OnNewLeader: func(identity string) {
                if identity != id {
                    klog.Infof("New leader elected: %s", identity)
                }
            },
        },
    })
}

// Shutdown performs a graceful shutdown of the controller
func (c *Controller) Shutdown() error {
    klog.Info("Shutting down controller")
    shutdownStartTime := time.Now()
    
    // Signal active reconciliations to stop
    close(c.stopCh)
    
    // Set shutdown flag to prevent new reconciliations
    atomic.StoreInt32(&c.isShuttingDown, 1)
    
    // Wait for active reconciliations to complete with timeout
    shutdownTimeout := c.config.ShutdownTimeoutSeconds
    if shutdownTimeout <= 0 {
        shutdownTimeout = 30 // Default 30 seconds
    }
    
    // Create a context with timeout for shutdown operations
    ctx, cancel := context.WithTimeout(context.Background(), time.Duration(shutdownTimeout)*time.Second)
    defer cancel()
    
    // Wait for active reconciliations to complete
    klog.Infof("Waiting for %d active reconciliations to complete", c.activeReconciliations.Load())
    
    // Create a ticker to log progress
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()
    
    // Wait for active reconciliations to complete or timeout
    for c.activeReconciliations.Load() > 0 {
        select {
        case <-ctx.Done():
            klog.Warningf("Shutdown timeout reached with %d active reconciliations still running", 
                c.activeReconciliations.Load())
            goto shutdownContinue
        case <-ticker.C:
            klog.Infof("Still waiting for %d active reconciliations to complete", 
                c.activeReconciliations.Load())
        case <-time.After(100 * time.Millisecond):
            // Check frequently but don't spam logs
        }
    }
    
shutdownContinue:
    // Wait for work queue to drain with timeout
    klog.Info("Shutting down work queue")
    c.workqueue.ShutDown()
    
    // Wait for queue to drain with timeout
    queueDrainCtx, queueDrainCancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer queueDrainCancel()
    
    ticker = time.NewTicker(1 * time.Second)
    defer ticker.Stop()
    
    for c.workqueue.Len() > 0 {
        select {
        case <-queueDrainCtx.Done():
            klog.Warningf("Queue drain timeout reached with %d items still in queue", 
                c.workqueue.Len())
            break
        case <-ticker.C:
            klog.Infof("Still waiting for work queue to drain, %d items remaining", 
                c.workqueue.Len())
        }
    }
    
    klog.Info("Work queue shut down")
    
    // Execute all registered shutdown handlers
    var shutdownErrors []error
    for _, handler := range c.shutdownHandlers {
        handlerName := runtime.FuncForPC(reflect.ValueOf(handler).Pointer()).Name()
        klog.Infof("Executing shutdown handler: %s", handlerName)
        
        handlerCtx, handlerCancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer handlerCancel()
        
        // Create a channel to receive the handler result
        resultCh := make(chan error, 1)
        
        // Execute the handler in a goroutine
        go func() {
            resultCh <- handler()
        }()
        
        // Wait for the handler to complete or timeout
        select {
        case err := <-resultCh:
            if err != nil {
                shutdownErrors = append(shutdownErrors, fmt.Errorf("%s: %v", handlerName, err))
                klog.Errorf("Error in shutdown handler %s: %v", handlerName, err)
            }
        case <-handlerCtx.Done():
            shutdownErrors = append(shutdownErrors, fmt.Errorf("%s: timed out", handlerName))
            klog.Errorf("Shutdown handler %s timed out", handlerName)
        }
    }
    
    // Close connections to the Kubernetes API server
    klog.Info("Closing Kubernetes API connections")
    if c.kubeClient != nil {
        if restClient, ok := c.kubeClient.(*rest.RESTClient); ok {
            restClient.Close()
        }
    }
    
    // Close metrics server if running
    if c.metricsServer != nil {
        klog.Info("Stopping metrics server")
        if err := c.metricsServer.Close(); err != nil {
            shutdownErrors = append(shutdownErrors, fmt.Errorf("metrics server: %v", err))
            klog.Errorf("Error stopping metrics server: %v", err)
        }
    }
    
    // Log final metrics before shutdown
    workqueueDepthGauge.WithLabelValues("controller").Set(0)
    
    // Record shutdown duration
    shutdownDuration := time.Since(shutdownStartTime)
    klog.Infof("Controller shutdown completed in %v", shutdownDuration)
    
    if len(shutdownErrors) > 0 {
        return fmt.Errorf("errors during shutdown: %v", shutdownErrors)
    }
    
    return nil
}

// RegisterShutdownHandler registers a function to be called during shutdown
func (c *Controller) RegisterShutdownHandler(handler func() error) {
    c.shutdownHandlers = append(c.shutdownHandlers, handler)
}

// SetupSignalHandler sets up signal handling for graceful shutdown
func (c *Controller) SetupSignalHandler() {
    // Set up signal handling
    signalCh := make(chan os.Signal, 2)
    signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)
    
    go func() {
        sig := <-signalCh
        klog.Infof("Received signal: %v", sig)
        klog.Info("Initiating graceful shutdown")
        
        // Start shutdown process
        if err := c.Shutdown(); err != nil {
            klog.Errorf("Error during shutdown: %v", err)
            os.Exit(1)
        }
        
        klog.Info("Graceful shutdown completed successfully")
        os.Exit(0)
    }()
}

// beginReconciliation marks the start of a reconciliation and increments the active count
func (c *Controller) beginReconciliation(resource string) {
    c.activeReconciliations.Add(1)
    controllerSyncCountTotal.WithLabelValues(resource).Inc()
}

// endReconciliation marks the end of a reconciliation and decrements the active count
func (c *Controller) endReconciliation() {
    c.activeReconciliations.Add(-1)
}

// isShuttingDown returns true if the controller is in the process of shutting down
func (c *Controller) isShuttingDown() bool {
    return atomic.LoadInt32(&c.isShuttingDown) == 1
}
```

## 2. Horizontal Scaling

The controller can be horizontally scaled by increasing the number of replicas in the deployment. Only one instance will be active at a time due to leader election.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: llmsafespace-controller
  namespace: llmsafespace
spec:
  replicas: 2  # For high availability
  selector:
    matchLabels:
      app: llmsafespace
      component: controller
  template:
    metadata:
      labels:
        app: llmsafespace
        component: controller
    spec:
      containers:
      - name: controller
        image: llmsafespace/controller:latest
        args:
        - "--enable-leader-election=true"
        - "--workers=5"
        resources:
          requests:
            cpu: 100m
            memory: 256Mi
          limits:
            cpu: 500m
            memory: 512Mi
```
