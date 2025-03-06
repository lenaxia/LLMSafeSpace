# Controller Architecture

## Component Structure

The Sandbox Controller is structured as follows:

1. **Main Controller Process**
   - Initializes Kubernetes client and informers
   - Sets up reconciliation loops
   - Manages leader election for HA deployments
   - Handles graceful shutdown

2. **Reconcilers**
   - SandboxReconciler
   - SandboxProfileReconciler
   - RuntimeEnvironmentReconciler
   - WarmPoolReconciler
   - WarmPodReconciler

3. **Resource Managers**
   - PodManager
   - NetworkPolicyManager
   - ServiceManager
   - VolumeManager
   - SecurityContextManager
   - WarmPoolManager

4. **Utilities**
   - EventRecorder
   - MetricsCollector
   - TemplateRenderer
   - ValidationHelper
   - WarmPodAllocator

### Controller Initialization Flow

```go
func main() {
    // Parse command-line flags
    flag.Parse()
    
    // Set up logging
    setupLogging()
    
    // Create Kubernetes client
    config, err := rest.InClusterConfig()
    if err != nil {
        klog.Fatalf("Error getting Kubernetes config: %v", err)
    }
    
    kubeClient, err := kubernetes.NewForConfig(config)
    if err != nil {
        klog.Fatalf("Error creating Kubernetes client: %v", err)
    }
    
    llmsafespaceClient, err := clientset.NewForConfig(config)
    if err != nil {
        klog.Fatalf("Error creating LLMSafeSpace client: %v", err)
    }
    
    // Set up informer factories
    kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*30)
    llmsafespaceInformerFactory := informers.NewSharedInformerFactory(llmsafespaceClient, time.Second*30)
    
    // Create controller
    controller := NewController(
        kubeClient,
        llmsafespaceClient,
        kubeInformerFactory,
        llmsafespaceInformerFactory,
    )
    
    // Set up leader election if enabled
    if *enableLeaderElection {
        setupLeaderElection(kubeClient, controller)
    } else {
        // Start controller directly
        controller.Run(*workers, stopCh)
    }
}
```
