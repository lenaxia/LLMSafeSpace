# Adding Warm Pool Support to SecureAgent

This document outlines the design and implementation plan for adding warm pool support to SecureAgent, allowing pods of specific runtime environments to be kept "warm" for immediate use.

## 1. Architecture Changes

### New Custom Resource Definitions (CRDs)

#### WarmPool CRD

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: warmpools.llmsafespace.dev
spec:
  group: llmsafespace.dev
  names:
    kind: WarmPool
    plural: warmpools
    singular: warmpool
    shortNames:
      - wp
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          required:
            - spec
          properties:
            spec:
              type: object
              required:
                - runtime
                - minSize
              properties:
                runtime:
                  type: string
                  description: "Runtime environment (e.g., python:3.10)"
                minSize:
                  type: integer
                  minimum: 0
                  description: "Minimum number of warm pods to maintain"
                maxSize:
                  type: integer
                  minimum: 0
                  description: "Maximum number of warm pods to maintain (0 for unlimited)"
                securityLevel:
                  type: string
                  enum: ["standard", "high", "custom"]
                  default: "standard"
                  description: "Security level for warm pods"
                ttl:
                  type: integer
                  minimum: 0
                  default: 3600
                  description: "Time-to-live for unused warm pods in seconds (0 for no expiry)"
                resources:
                  type: object
                  properties:
                    cpu:
                      type: string
                      pattern: "^([0-9]+m|[0-9]+\\.[0-9]+)$"
                      default: "500m"
                      description: "CPU resource limit"
                    memory:
                      type: string
                      pattern: "^[0-9]+(Ki|Mi|Gi)$"
                      default: "512Mi"
                      description: "Memory resource limit"
                profileRef:
                  type: object
                  properties:
                    name:
                      type: string
                      description: "Name of SandboxProfile to use"
                    namespace:
                      type: string
                      description: "Namespace of SandboxProfile"
                preloadPackages:
                  type: array
                  items:
                    type: string
                  description: "Packages to preinstall in warm pods"
                preloadScripts:
                  type: array
                  items:
                    type: object
                    properties:
                      name:
                        type: string
                      content:
                        type: string
                  description: "Scripts to run during pod initialization"
                autoScaling:
                  type: object
                  properties:
                    enabled:
                      type: boolean
                      default: false
                    targetUtilization:
                      type: integer
                      minimum: 1
                      maximum: 100
                      default: 80
                    scaleDownDelay:
                      type: integer
                      minimum: 0
                      default: 300
                      description: "Seconds to wait before scaling down"
            status:
              type: object
              properties:
                availablePods:
                  type: integer
                  description: "Number of warm pods available for immediate use"
                assignedPods:
                  type: integer
                  description: "Number of warm pods currently assigned to sandboxes"
                pendingPods:
                  type: integer
                  description: "Number of warm pods being created"
                lastScaleTime:
                  type: string
                  format: date-time
                  description: "Last time the pool was scaled"
                conditions:
                  type: array
                  items:
                    type: object
                    required:
                      - type
                      - status
                    properties:
                      type:
                        type: string
                        description: "Type of condition"
                      status:
                        type: string
                        enum: ["True", "False", "Unknown"]
                      reason:
                        type: string
                        description: "Reason for the condition"
                      message:
                        type: string
                        description: "Message explaining the condition"
                      lastTransitionTime:
                        type: string
                        format: date-time
                        description: "Last time the condition transitioned"
```

#### WarmPod CRD

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: warmpods.llmsafespace.dev
spec:
  group: llmsafespace.dev
  names:
    kind: WarmPod
    plural: warmpods
    singular: warmpod
    shortNames:
      - wpod
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          required:
            - spec
          properties:
            spec:
              type: object
              required:
                - poolRef
              properties:
                poolRef:
                  type: object
                  required:
                    - name
                  properties:
                    name:
                      type: string
                      description: "Name of the WarmPool this pod belongs to"
                    namespace:
                      type: string
                      description: "Namespace of the WarmPool"
                creationTimestamp:
                  type: string
                  format: date-time
                  description: "Time when this warm pod was created"
                lastHeartbeat:
                  type: string
                  format: date-time
                  description: "Last time the pod reported it was healthy"
            status:
              type: object
              properties:
                phase:
                  type: string
                  enum: ["Pending", "Ready", "Assigned", "Terminating"]
                  description: "Current phase of the warm pod"
                podName:
                  type: string
                  description: "Name of the underlying pod"
                podNamespace:
                  type: string
                  description: "Namespace of the underlying pod"
                assignedTo:
                  type: string
                  description: "ID of the sandbox this pod is assigned to (if any)"
                assignedAt:
                  type: string
                  format: date-time
                  description: "Time when this pod was assigned to a sandbox"
```

## 2. Controller Changes

### WarmPool Controller

The WarmPool controller manages the lifecycle of warm pools:

```go
// WarmPoolController manages the lifecycle of warm pools
type WarmPoolController struct {
    kubeClient        kubernetes.Interface
    llmsafespaceClient clientset.Interface
    warmPoolLister    listers.WarmPoolLister
    warmPoolSynced    cache.InformerSynced
    warmPodLister     listers.WarmPodLister
    warmPodSynced     cache.InformerSynced
    podLister         corelisters.PodLister
    podSynced         cache.InformerSynced
    workqueue         workqueue.RateLimitingInterface
    recorder          record.EventRecorder
}

// reconcileWarmPool ensures the actual state of a warm pool matches the desired state
func (c *WarmPoolController) reconcileWarmPool(key string) error {
    namespace, name, err := cache.SplitMetaNamespaceKey(key)
    if err != nil {
        return err
    }
    
    // Get WarmPool resource
    warmPool, err := c.warmPoolLister.WarmPools(namespace).Get(name)
    if errors.IsNotFound(err) {
        // WarmPool was deleted, nothing to do
        return nil
    }
    if err != nil {
        return err
    }
    
    // Deep copy to avoid modifying cache
    warmPool = warmPool.DeepCopy()
    
    // List all warm pods for this pool
    selector := labels.SelectorFromSet(labels.Set{
        "app": "llmsafespace",
        "component": "warmpod",
        "pool": warmPool.Name,
    })
    warmPods, err := c.warmPodLister.WarmPods(namespace).List(selector)
    if err != nil {
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
    
    // Scale up if needed
    if availablePods < warmPool.Spec.MinSize {
        neededPods := warmPool.Spec.MinSize - availablePods
        for i := 0; i < neededPods; i++ {
            if err := c.createWarmPod(warmPool); err != nil {
                return err
            }
        }
        warmPool.Status.LastScaleTime = metav1.Now()
    }
    
    // Scale down if needed and autoscaling is enabled
    if warmPool.Spec.AutoScaling != nil && warmPool.Spec.AutoScaling.Enabled {
        maxSize := warmPool.Spec.MaxSize
        if maxSize > 0 && availablePods > maxSize {
            // Find oldest pods to remove
            // ...
        }
    }
    
    // Update status
    _, err = c.llmsafespaceClient.LlmsafespaceV1().WarmPools(namespace).UpdateStatus(
        context.TODO(), warmPool, metav1.UpdateOptions{})
    
    return err
}

// createWarmPod creates a new warm pod for the given pool
func (c *WarmPoolController) createWarmPod(pool *llmsafespacev1.WarmPool) error {
    // Create WarmPod custom resource
    warmPod := &llmsafespacev1.WarmPod{
        ObjectMeta: metav1.ObjectMeta{
            GenerateName: fmt.Sprintf("%s-", pool.Name),
            Namespace: pool.Namespace,
            Labels: map[string]string{
                "app": "llmsafespace",
                "component": "warmpod",
                "pool": pool.Name,
            },
            OwnerReferences: []metav1.OwnerReference{
                *metav1.NewControllerRef(pool, llmsafespacev1.SchemeGroupVersion.WithKind("WarmPool")),
            },
        },
        Spec: llmsafespacev1.WarmPodSpec{
            PoolRef: llmsafespacev1.PoolReference{
                Name: pool.Name,
                Namespace: pool.Namespace,
            },
            CreationTimestamp: metav1.Now(),
        },
        Status: llmsafespacev1.WarmPodStatus{
            Phase: "Pending",
        },
    }
    
    // Create the WarmPod resource
    warmPod, err := c.llmsafespaceClient.LlmsafespaceV1().WarmPods(pool.Namespace).Create(
        context.TODO(), warmPod, metav1.CreateOptions{})
    if err != nil {
        return err
    }
    
    // Create the actual pod
    pod, err := c.createPodForWarmPod(warmPod, pool)
    if err != nil {
        return err
    }
    
    // Update WarmPod status with pod info
    warmPod.Status.PodName = pod.Name
    warmPod.Status.PodNamespace = pod.Namespace
    
    _, err = c.llmsafespaceClient.LlmsafespaceV1().WarmPods(warmPod.Namespace).UpdateStatus(
        context.TODO(), warmPod, metav1.UpdateOptions{})
    
    return err
}
```

### WarmPod Controller

The WarmPod controller manages the lifecycle of individual warm pods:

```go
// reconcileWarmPod ensures the actual state of a warm pod matches the desired state
func (c *WarmPodController) reconcileWarmPod(key string) error {
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

## 3. API Service Changes

### Sandbox Creation Logic

Modify the sandbox creation logic to use warm pods when available:

```go
// createSandbox creates a new sandbox, potentially using a warm pod
func (s *SandboxService) createSandbox(ctx context.Context, req *api.CreateSandboxRequest) (*api.Sandbox, error) {
    // Check if there's a matching warm pod available
    warmPod, err := s.findMatchingWarmPod(ctx, req)
    if err != nil {
        return nil, err
    }
    
    if warmPod != nil {
        // Use the warm pod for this sandbox
        return s.createSandboxFromWarmPod(ctx, req, warmPod)
    }
    
    // No matching warm pod, create a new sandbox from scratch
    return s.createSandboxFromScratch(ctx, req)
}

// findMatchingWarmPod finds a warm pod that matches the requirements
func (s *SandboxService) findMatchingWarmPod(ctx context.Context, req *api.CreateSandboxRequest) (*llmsafespacev1.WarmPod, error) {
    // Find warm pools that match the runtime
    pools, err := s.llmsafespaceClient.LlmsafespaceV1().WarmPools("").List(ctx, metav1.ListOptions{
        LabelSelector: fmt.Sprintf("runtime=%s", req.Runtime),
    })
    if err != nil {
        return nil, err
    }
    
    // No matching pools
    if len(pools.Items) == 0 {
        return nil, nil
    }
    
    // Find the best matching pool
    var bestPool *llmsafespacev1.WarmPool
    for i, pool := range pools.Items {
        if pool.Status.AvailablePods > 0 {
            // Check if security level matches
            if pool.Spec.SecurityLevel == req.SecurityLevel {
                bestPool = &pools.Items[i]
                break
            }
            
            // If no exact match yet, use this as a fallback
            if bestPool == nil {
                bestPool = &pools.Items[i]
            }
        }
    }
    
    if bestPool == nil {
        return nil, nil
    }
    
    // Find an available warm pod from the pool
    pods, err := s.llmsafespaceClient.LlmsafespaceV1().WarmPods(bestPool.Namespace).List(ctx, metav1.ListOptions{
        LabelSelector: fmt.Sprintf("pool=%s,status=Ready", bestPool.Name),
    })
    if err != nil {
        return nil, err
    }
    
    if len(pods.Items) == 0 {
        return nil, nil
    }
    
    // Use the first available pod
    return &pods.Items[0], nil
}
```

## 4. Sandbox Controller Changes

Modify the sandbox controller to handle warm pod references:

```go
// reconcileSandbox ensures the actual state of a sandbox matches the desired state
func (c *Controller) reconcileSandbox(key string) error {
    // ... existing code ...
    
    // Check if sandbox is using a warm pod
    if sandbox.Status.WarmPodRef != nil {
        return c.handleSandboxWithWarmPod(sandbox)
    }
    
    // ... existing code for regular sandbox creation ...
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

## 5. Cleanup and Recycling

Add logic to handle cleanup and recycling of warm pods:

```go
// handleSandboxDeletion handles the deletion of a sandbox
func (c *Controller) handleSandboxDeletion(sandbox *llmsafespacev1.Sandbox) error {
    // Check if this sandbox was using a warm pod
    if sandbox.Status.WarmPodRef != nil {
        // Get the warm pod
        warmPod, err := c.warmPodLister.WarmPods(sandbox.Status.WarmPodRef.Namespace).Get(sandbox.Status.WarmPodRef.Name)
        if err != nil && !errors.IsNotFound(err) {
            return err
        }
        
        if warmPod != nil {
            // Check if we should recycle this pod
            if c.shouldRecyclePod(warmPod, sandbox) {
                return c.recyclePod(warmPod, sandbox)
            } else {
                // Delete the warm pod
                err = c.llmsafespaceClient.LlmsafespaceV1().WarmPods(warmPod.Namespace).Delete(
                    context.TODO(), warmPod.Name, metav1.DeleteOptions{})
                if err != nil && !errors.IsNotFound(err) {
                    return err
                }
            }
        }
    }
    
    // ... existing cleanup code ...
    
    return nil
}

// shouldRecyclePod determines if a warm pod should be recycled
func (c *Controller) shouldRecyclePod(warmPod *llmsafespacev1.WarmPod, sandbox *llmsafespacev1.Sandbox) bool {
    // Get the pool
    pool, err := c.warmPoolLister.WarmPools(warmPod.Spec.PoolRef.Namespace).Get(warmPod.Spec.PoolRef.Name)
    if err != nil {
        return false
    }
    
    // Check if the pool still exists and needs more pods
    if pool.Status.AvailablePods < pool.Spec.MinSize {
        // Check if the pod has been running for too long
        if warmPod.Spec.CreationTimestamp.Add(24 * time.Hour).Before(time.Now()) {
            // Pod is too old, don't recycle
            return false
        }
        
        // Check if the sandbox did anything that would make recycling unsafe
        // (e.g., installed untrusted packages, modified system files)
        // This would require additional tracking in the sandbox status
        
        return true
    }
    
    return false
}

// recyclePod recycles a warm pod for reuse
func (c *Controller) recyclePod(warmPod *llmsafespacev1.WarmPod, sandbox *llmsafespacev1.Sandbox) error {
    // Get the pod
    pod, err := c.podLister.Pods(warmPod.Status.PodNamespace).Get(warmPod.Status.PodName)
    if err != nil {
        return err
    }
    
    // Update the pod labels to remove sandbox association
    podCopy := pod.DeepCopy()
    delete(podCopy.Labels, "sandbox-id")
    delete(podCopy.Labels, "sandbox-uid")
    
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
        return err
    }
    
    // Clean up the pod environment
    if err := c.cleanupPodEnvironment(pod); err != nil {
        return err
    }
    
    // Update warm pod status
    warmPod.Status.Phase = "Ready"
    warmPod.Status.AssignedTo = ""
    warmPod.Status.AssignedAt = metav1.Time{}
    warmPod.Spec.LastHeartbeat = metav1.Now()
    
    _, err = c.llmsafespaceClient.LlmsafespaceV1().WarmPods(warmPod.Namespace).UpdateStatus(
        context.TODO(), warmPod, metav1.UpdateOptions{})
    
    return err
}
```

## 6. Configuration and Management

### API Endpoints

Add API endpoints to manage warm pools:

```go
// WarmPoolHandler handles API requests for warm pools
type WarmPoolHandler struct {
    llmsafespaceClient clientset.Interface
    config           *config.Config
}

// ListWarmPools lists all warm pools
func (h *WarmPoolHandler) ListWarmPools(w http.ResponseWriter, r *http.Request) {
    // List warm pools
    pools, err := h.llmsafespaceClient.LlmsafespaceV1().WarmPools("").List(r.Context(), metav1.ListOptions{})
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    // Convert to API response
    response := make([]api.WarmPool, 0, len(pools.Items))
    for _, pool := range pools.Items {
        response = append(response, convertWarmPoolToAPI(&pool))
    }
    
    // Write response
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(response)
}

// CreateWarmPool creates a new warm pool
func (h *WarmPoolHandler) CreateWarmPool(w http.ResponseWriter, r *http.Request) {
    // Parse request
    var req api.CreateWarmPoolRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    
    // Create warm pool
    pool := &llmsafespacev1.WarmPool{
        ObjectMeta: metav1.ObjectMeta{
            Name: req.Name,
            Namespace: h.config.WarmPoolNamespace,
        },
        Spec: llmsafespacev1.WarmPoolSpec{
            Runtime: req.Runtime,
            MinSize: req.MinSize,
            MaxSize: req.MaxSize,
            SecurityLevel: req.SecurityLevel,
            TTL: req.TTL,
            Resources: &llmsafespacev1.ResourceRequirements{
                CPU: req.Resources.Cpu,
                Memory: req.Resources.Memory,
            },
            PreloadPackages: req.PreloadPackages,
            PreloadScripts: convertPreloadScripts(req.PreloadScripts),
            AutoScaling: &llmsafespacev1.AutoScaling{
                Enabled: req.AutoScaling.Enabled,
                TargetUtilization: req.AutoScaling.TargetUtilization,
                ScaleDownDelay: req.AutoScaling.ScaleDownDelay,
            },
        },
    }
    
    // Create the resource
    pool, err := h.llmsafespaceClient.LlmsafespaceV1().WarmPools(h.config.WarmPoolNamespace).Create(
        r.Context(), pool, metav1.CreateOptions{})
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    // Write response
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusCreated)
    json.NewEncoder(w).Encode(convertWarmPoolToAPI(pool))
}
```

## 7. SDK Changes

Update the SDK to support warm pools:

```python
class WarmPool:
    """Represents a warm pool of sandbox environments."""
    
    def __init__(self, client, data):
        self._client = client
        self._data = data
    
    @property
    def name(self) -> str:
        """Get the warm pool name."""
        return self._data.get("name", "")
    
    @property
    def runtime(self) -> str:
        """Get the runtime environment."""
        return self._data.get("runtime", "")
    
    @property
    def min_size(self) -> int:
        """Get the minimum pool size."""
        return self._data.get("minSize", 0)
    
    @property
    def max_size(self) -> int:
        """Get the maximum pool size."""
        return self._data.get("maxSize", 0)
    
    @property
    def available_pods(self) -> int:
        """Get the number of available pods."""
        return self._data.get("availablePods", 0)
    
    @property
    def assigned_pods(self) -> int:
        """Get the number of assigned pods."""
        return self._data.get("assignedPods", 0)
    
    def scale(self, min_size: int = None, max_size: int = None) -> 'WarmPool':
        """Scale the warm pool."""
        updates = {}
        if min_size is not None:
            updates["minSize"] = min_size
        if max_size is not None:
            updates["maxSize"] = max_size
        
        if updates:
            self._data = self._client._make_request(
                "PATCH", 
                f"/warmpools/{self.name}",
                json=updates
            )
        
        return self

class Client:
    """Client for the SecureAgent API."""
    
    # ... existing methods ...
    
    def list_warm_pools(self) -> List[WarmPool]:
        """List all warm pools."""
        response = self._make_request("GET", "/warmpools")
        return [WarmPool(self, data) for data in response]
    
    def get_warm_pool(self, name: str) -> WarmPool:
        """Get a warm pool by name."""
        response = self._make_request("GET", f"/warmpools/{name}")
        return WarmPool(self, response)
    
    def create_warm_pool(
        self, 
        name: str, 
        runtime: str, 
        min_size: int = 1, 
        max_size: int = 10,
        security_level: str = "standard",
        preload_packages: List[str] = None,
        auto_scaling: bool = False
    ) -> WarmPool:
        """Create a new warm pool."""
        payload = {
            "name": name,
            "runtime": runtime,
            "minSize": min_size,
            "maxSize": max_size,
            "securityLevel": security_level,
            "autoScaling": {
                "enabled": auto_scaling
            }
        }
        
        if preload_packages:
            payload["preloadPackages"] = preload_packages
        
        response = self._make_request("POST", "/warmpools", json=payload)
        return WarmPool(self, response)
    
    def delete_warm_pool(self, name: str) -> bool:
        """Delete a warm pool."""
        self._make_request("DELETE", f"/warmpools/{name}")
        return True
```

## 8. Metrics and Monitoring

Add metrics for warm pools:

```go
// Metric definitions
var (
    warmPoolSizeGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_warmpool_size",
            Help: "Current size of warm pools",
        },
        []string{"pool", "runtime", "status"},
    )
    
    warmPoolAssignmentDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_warmpool_assignment_duration_seconds",
            Help: "Time taken to assign a warm pod to a sandbox",
            Buckets: prometheus.DefBuckets,
        },
        []string{"pool", "runtime"},
    )
    
    warmPoolCreationDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_warmpool_creation_duration_seconds",
            Help: "Time taken to create a warm pod",
            Buckets: prometheus.DefBuckets,
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
    
    warmPoolHitRatio = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_warmpool_hit_ratio",
            Help: "Ratio of sandbox creations that used a warm pod",
        },
        []string{"runtime"},
    )
)
```

## 9. Usage Examples

### Creating a Warm Pool

```bash
# Using the CLI
llmsafespace warm-pool create --name python-pool --runtime python:3.10 --min-size 5 --max-size 20

# Using the API
curl -X POST https://api.llmsafespace.dev/api/v1/warmpools \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "python-pool",
    "runtime": "python:3.10",
    "minSize": 5,
    "maxSize": 20,
    "securityLevel": "standard",
    "preloadPackages": ["numpy", "pandas"],
    "autoScaling": {
      "enabled": true,
      "targetUtilization": 80,
      "scaleDownDelay": 300
    }
  }'
```

### Using Warm Pools with the SDK

```python
from llmsafespace import Sandbox

# This will use a warm pod if available
sandbox = Sandbox(runtime="python:3.10")

# You can also explicitly opt out of using warm pods
sandbox = Sandbox(runtime="python:3.10", use_warm_pool=False)
```

## 10. Implementation Plan

1. **Phase 1: Core Infrastructure**
   - Implement WarmPool and WarmPod CRDs
   - Implement basic controller functionality for managing warm pools
   - Add metrics and monitoring

2. **Phase 2: Integration**
   - Modify sandbox creation logic to use warm pods
   - Implement pod recycling and cleanup
   - Update API endpoints and SDK

3. **Phase 3: Optimization**
   - Implement auto-scaling based on usage patterns
   - Add preloading of common packages and initialization scripts
   - Optimize pod recycling for faster turnaround

4. **Phase 4: Documentation and Testing**
   - Update documentation
   - Add examples
   - Comprehensive testing of warm pool functionality

## 11. Benefits

1. **Reduced Startup Time**: Sandboxes can be created almost instantly by using pre-initialized pods
2. **Improved User Experience**: Faster response times for interactive applications
3. **Resource Efficiency**: Better resource utilization through pod recycling
4. **Predictable Performance**: Consistent startup times for critical applications
5. **Customization**: Pre-installed packages and initialization scripts for common use cases

## 12. Conclusion

Adding warm pool support to SecureAgent significantly improves the user experience by reducing sandbox startup times. This feature is particularly valuable for interactive applications where users expect quick responses. The implementation leverages Kubernetes native concepts and integrates seamlessly with the existing architecture while maintaining the security and isolation guarantees of the platform.
