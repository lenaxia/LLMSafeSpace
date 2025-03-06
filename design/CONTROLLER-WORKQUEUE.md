# Work Queue Processing

The Sandbox Controller uses a single work queue for all resource types, with a mechanism to determine the resource type from the queue key:

```go
// Controller manages the lifecycle of sandboxes and warm pools
type Controller struct {
    kubeClient        kubernetes.Interface
    llmsafespaceClient clientset.Interface
    restConfig        *rest.Config
    
    // Informers and listers
    sandboxInformer   informers.SandboxInformer
    sandboxLister     listers.SandboxLister
    sandboxSynced     cache.InformerSynced
    
    warmPoolInformer  informers.WarmPoolInformer
    warmPoolLister    listers.WarmPoolLister
    warmPoolSynced    cache.InformerSynced
    
    warmPodInformer   informers.WarmPodInformer
    warmPodLister     listers.WarmPodLister
    warmPodSynced     cache.InformerSynced
    
    profileInformer   informers.SandboxProfileInformer
    profileLister     listers.SandboxProfileLister
    profileSynced     cache.InformerSynced
    
    runtimeInformer   informers.RuntimeEnvironmentInformer
    runtimeLister     listers.RuntimeEnvironmentLister
    runtimeSynced     cache.InformerSynced
    
    podInformer       coreinformers.PodInformer
    podLister         corelisters.PodLister
    podSynced         cache.InformerSynced
    
    // Work queue
    workqueue         workqueue.RateLimitingInterface
    
    // Resource managers
    podManager        *PodManager
    serviceManager    *ServiceManager
    networkPolicyManager *NetworkPolicyManager
    volumeManager     *VolumeManager
    serviceAccountManager *ServiceAccountManager
    securityContextManager *SecurityContextManager
    namespaceManager  *NamespaceManager
    warmPodAllocator  *WarmPodAllocator
    
    // API service integration
    apiServiceClient  *APIServiceClient
    
    // Other utilities
    recorder          record.EventRecorder
    config            *config.Config
    
    // Shutdown handling
    stopCh            <-chan struct{}
    shutdownHandlers  []func() error
}

// processNextWorkItem processes the next item from the work queue
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
        
        // Determine resource type from key format
        // Format: <resource-type>/<namespace>/<name>
        parts := strings.SplitN(key, "/", 3)
        if len(parts) != 3 {
            c.workqueue.Forget(obj)
            return fmt.Errorf("invalid resource key: %s", key)
        }
        
        resourceType := parts[0]
        namespace := parts[1]
        name := parts[2]
        nsName := namespace + "/" + name
        
        // Call appropriate reconcile function based on resource type
        var err error
        switch resourceType {
        case "sandbox":
            err = c.reconcileSandbox(nsName)
        case "warmpool":
            err = c.reconcileWarmPool(nsName)
        case "warmpod":
            err = c.reconcileWarmPod(nsName)
        case "profile":
            err = c.reconcileSandboxProfile(nsName)
        case "runtime":
            err = c.reconcileRuntimeEnvironment(nsName)
        default:
            err = fmt.Errorf("unknown resource type: %s", resourceType)
        }
        
        if err != nil {
            // Check if we should requeue
            if shouldRequeue(err) {
                c.workqueue.AddRateLimited(key)
                return fmt.Errorf("error reconciling %s '%s': %s, requeuing", resourceType, nsName, err.Error())
            }
            
            // Don't requeue for permanent errors
            c.workqueue.Forget(obj)
            return fmt.Errorf("error reconciling %s '%s': %s, not requeuing", resourceType, nsName, err.Error())
        }
        
        c.workqueue.Forget(obj)
        return nil
    }(obj)
    
    if err != nil {
        klog.Errorf("Error processing item: %v", err)
    }
    
    return true
}

// enqueueResource adds a resource to the work queue with appropriate type prefix
func (c *Controller) enqueueResource(resourceType string, obj interface{}) {
    var key string
    var err error
    if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
        klog.Errorf("Error getting key for object: %v", err)
        return
    }
    c.workqueue.Add(resourceType + "/" + key)
}
```
