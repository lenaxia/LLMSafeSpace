# Combined Controller Design for SecureAgent

## Overview

The Combined Controller is a critical component of the SecureAgent platform, responsible for managing the lifecycle of both sandbox environments and warm pools. This document provides a detailed design for the controller, including Custom Resource Definitions (CRDs), reconciliation loops, and resource lifecycle management.

The controller manages both sandbox environments and pools of pre-initialized sandbox environments (warm pools) for faster startup times, providing a unified approach to resource management.

## Custom Resource Definitions (CRDs)

### 1. Sandbox CRD

The Sandbox CRD represents an isolated execution environment for running code.

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: sandboxes.llmsafespace.dev
spec:
  group: llmsafespace.dev
  names:
    kind: Sandbox
    plural: sandboxes
    singular: sandbox
    shortNames:
      - sb
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
              properties:
                runtime:
                  type: string
                  description: "Runtime environment (e.g., python:3.10)"
                securityLevel:
                  type: string
                  enum: ["standard", "high", "custom"]
                  default: "standard"
                  description: "Security level for the sandbox"
                timeout:
                  type: integer
                  minimum: 1
                  maximum: 3600
                  default: 300
                  description: "Timeout in seconds for sandbox operations"
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
                    ephemeralStorage:
                      type: string
                      pattern: "^[0-9]+(Ki|Mi|Gi)$"
                      default: "1Gi"
                      description: "Ephemeral storage limit"
                    cpuPinning:
                      type: boolean
                      default: false
                      description: "Enable CPU pinning for sensitive workloads"
                networkAccess:
                  type: object
                  properties:
                    egress:
                      type: array
                      items:
                        type: object
                        required:
                          - domain
                        properties:
                          domain:
                            type: string
                            description: "Domain name for egress filtering"
                          ports:
                            type: array
                            items:
                              type: object
                              properties:
                                port:
                                  type: integer
                                  minimum: 1
                                  maximum: 65535
                                protocol:
                                  type: string
                                  enum: ["TCP", "UDP"]
                                  default: "TCP"
                    ingress:
                      type: boolean
                      default: false
                      description: "Allow ingress traffic to sandbox"
                filesystem:
                  type: object
                  properties:
                    readOnlyRoot:
                      type: boolean
                      default: true
                      description: "Mount root filesystem as read-only"
                    writablePaths:
                      type: array
                      items:
                        type: string
                      default: ["/tmp", "/workspace"]
                      description: "Paths that should be writable"
                storage:
                  type: object
                  properties:
                    persistent:
                      type: boolean
                      default: false
                      description: "Enable persistent storage"
                    volumeSize:
                      type: string
                      pattern: "^[0-9]+(Ki|Mi|Gi)$"
                      default: "5Gi"
                      description: "Size of persistent volume"
                securityContext:
                  type: object
                  properties:
                    runAsUser:
                      type: integer
                      default: 1000
                      description: "User ID to run container processes"
                    runAsGroup:
                      type: integer
                      default: 1000
                      description: "Group ID to run container processes"
                    seccompProfile:
                      type: object
                      properties:
                        type:
                          type: string
                          enum: ["RuntimeDefault", "Localhost"]
                          default: "RuntimeDefault"
                        localhostProfile:
                          type: string
                          description: "Path to seccomp profile on node"
                profileRef:
                  type: object
                  properties:
                    name:
                      type: string
                      description: "Name of SandboxProfile to use"
                    namespace:
                      type: string
                      description: "Namespace of SandboxProfile"
            status:
              type: object
              properties:
                phase:
                  type: string
                  enum: ["Pending", "Creating", "Running", "Terminating", "Terminated", "Failed"]
                  description: "Current phase of the sandbox"
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
                podName:
                  type: string
                  description: "Name of the pod running the sandbox"
                podNamespace:
                  type: string
                  description: "Namespace of the pod running the sandbox"
                startTime:
                  type: string
                  format: date-time
                  description: "Time when the sandbox was started"
                endpoint:
                  type: string
                  description: "Internal endpoint for the sandbox"
                resources:
                  type: object
                  properties:
                    cpuUsage:
                      type: string
                      description: "Current CPU usage"
                    memoryUsage:
                      type: string
                      description: "Current memory usage"
      additionalPrinterColumns:
        - name: Runtime
          type: string
          jsonPath: .spec.runtime
        - name: Status
          type: string
          jsonPath: .status.phase
        - name: Age
          type: date
          jsonPath: .metadata.creationTimestamp
      subresources:
        status: {}
```

### 2. SandboxProfile CRD

The SandboxProfile CRD defines reusable security and configuration profiles for sandboxes.

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: sandboxprofiles.llmsafespace.dev
spec:
  group: llmsafespace.dev
  names:
    kind: SandboxProfile
    plural: sandboxprofiles
    singular: sandboxprofile
    shortNames:
      - sbp
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
                - language
              properties:
                language:
                  type: string
                  description: "Target language for this profile"
                securityLevel:
                  type: string
                  enum: ["standard", "high", "custom"]
                  default: "standard"
                  description: "Base security level for this profile"
                seccompProfile:
                  type: string
                  description: "Path to seccomp profile for this language"
                networkPolicies:
                  type: array
                  items:
                    type: object
                    properties:
                      type:
                        type: string
                        enum: ["egress", "ingress"]
                      rules:
                        type: array
                        items:
                          type: object
                          properties:
                            domain:
                              type: string
                            cidr:
                              type: string
                            ports:
                              type: array
                              items:
                                type: object
                                properties:
                                  port:
                                    type: integer
                                  protocol:
                                    type: string
                                    enum: ["TCP", "UDP"]
                preInstalledPackages:
                  type: array
                  items:
                    type: string
                  description: "Packages pre-installed in this profile"
                resourceDefaults:
                  type: object
                  properties:
                    cpu:
                      type: string
                    memory:
                      type: string
                    ephemeralStorage:
                      type: string
                filesystemConfig:
                  type: object
                  properties:
                    readOnlyPaths:
                      type: array
                      items:
                        type: string
                    writablePaths:
                      type: array
                      items:
                        type: string
      additionalPrinterColumns:
        - name: Language
          type: string
          jsonPath: .spec.language
        - name: SecurityLevel
          type: string
          jsonPath: .spec.securityLevel
        - name: Age
          type: date
          jsonPath: .metadata.creationTimestamp
```

### 3. WarmPool CRD

The WarmPool CRD defines a pool of pre-initialized sandbox environments.

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
      additionalPrinterColumns:
        - name: Runtime
          type: string
          jsonPath: .spec.runtime
        - name: Available
          type: integer
          jsonPath: .status.availablePods
        - name: Assigned
          type: integer
          jsonPath: .status.assignedPods
        - name: Pending
          type: integer
          jsonPath: .status.pendingPods
        - name: Age
          type: date
          jsonPath: .metadata.creationTimestamp
      subresources:
        status: {}
```

### 4. WarmPod CRD

The WarmPod CRD represents an individual pre-initialized pod in a warm pool.

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
      additionalPrinterColumns:
        - name: Pool
          type: string
          jsonPath: .spec.poolRef.name
        - name: Phase
          type: string
          jsonPath: .status.phase
        - name: Pod
          type: string
          jsonPath: .status.podName
        - name: Age
          type: date
          jsonPath: .metadata.creationTimestamp
      subresources:
        status: {}
```

### 5. RuntimeEnvironment CRD

The RuntimeEnvironment CRD defines available runtime environments for sandboxes.

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: runtimeenvironments.llmsafespace.dev
spec:
  group: llmsafespace.dev
  names:
    kind: RuntimeEnvironment
    plural: runtimeenvironments
    singular: runtimeenvironment
    shortNames:
      - rte
  scope: Cluster
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
                - image
                - language
              properties:
                image:
                  type: string
                  description: "Container image for this runtime"
                language:
                  type: string
                  description: "Programming language (e.g., python, nodejs)"
                version:
                  type: string
                  description: "Version of the language runtime"
                tags:
                  type: array
                  items:
                    type: string
                  description: "Tags for categorizing runtimes"
                preInstalledPackages:
                  type: array
                  items:
                    type: string
                  description: "Packages pre-installed in this runtime"
                packageManager:
                  type: string
                  description: "Default package manager (e.g., pip, npm)"
                securityFeatures:
                  type: array
                  items:
                    type: string
                  description: "Security features supported by this runtime"
                resourceRequirements:
                  type: object
                  properties:
                    minCpu:
                      type: string
                    minMemory:
                      type: string
                    recommendedCpu:
                      type: string
                    recommendedMemory:
                      type: string
            status:
              type: object
              properties:
                available:
                  type: boolean
                  description: "Whether this runtime is available"
                lastValidated:
                  type: string
                  format: date-time
                  description: "Last time this runtime was validated"
      additionalPrinterColumns:
        - name: Language
          type: string
          jsonPath: .spec.language
        - name: Version
          type: string
          jsonPath: .spec.version
        - name: Available
          type: boolean
          jsonPath: .status.available
        - name: Age
          type: date
          jsonPath: .metadata.creationTimestamp
      subresources:
        status: {}
```

## Controller Architecture

### Component Structure

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

## Controller Architecture

### Component Structure

The Combined Controller is structured as follows:

1. **Main Controller Process**
   - Initializes Kubernetes client and informers
   - Sets up reconciliation loops for all resource types
   - Manages leader election for HA deployments
   - Handles graceful shutdown

2. **Reconcilers**
   - SandboxReconciler
   - WarmPoolReconciler
   - WarmPodReconciler
   - SandboxProfileReconciler
   - RuntimeEnvironmentReconciler

3. **Resource Managers**
   - PodManager
   - NetworkPolicyManager
   - ServiceManager
   - VolumeManager
   - SecurityContextManager
   - WarmPodAllocator

4. **Utilities**
   - EventRecorder
   - MetricsCollector
   - TemplateRenderer
   - ValidationHelper

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

## Reconciliation Loops

The controller implements multiple reconciliation loops, one for each resource type, but shares common utilities and clients across all loops.

### 1. Sandbox Reconciliation Loop

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

#### Sandbox State Machine

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

### 2. WarmPool Reconciliation Loop

The WarmPool reconciliation loop manages the lifecycle of warm pools, ensuring that the desired number of warm pods are available.

```go
// reconcileWarmPool ensures the actual state of a warm pool matches the desired state
func (c *Controller) reconcileWarmPool(key string) error {
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
func (c *Controller) createWarmPod(pool *llmsafespacev1.WarmPool) error {
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

### 3. WarmPod Reconciliation Loop

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

### 4. SandboxProfile Reconciliation Loop

The SandboxProfile reconciliation loop ensures that profile resources are properly validated and available for use by Sandboxes.

```go
func (c *Controller) reconcileSandboxProfile(key string) error {
    namespace, name, err := cache.SplitMetaNamespaceKey(key)
    if err != nil {
        return err
    }
    
    // Get SandboxProfile resource
    profile, err := c.profileLister.SandboxProfiles(namespace).Get(name)
    if errors.IsNotFound(err) {
        // Profile was deleted, nothing to do
        return nil
    }
    if err != nil {
        return err
    }
    
    // Deep copy to avoid modifying cache
    profile = profile.DeepCopy()
    
    // Validate profile
    if err := c.validateSandboxProfile(profile); err != nil {
        c.recorder.Event(profile, corev1.EventTypeWarning, "ValidationFailed", 
            fmt.Sprintf("Profile validation failed: %v", err))
        return err
    }
    
    // Update status if needed
    // ...
    
    return nil
}
```

### 5. RuntimeEnvironment Reconciliation Loop

The RuntimeEnvironment reconciliation loop validates runtime environments and ensures they are available for use.

```go
func (c *Controller) reconcileRuntimeEnvironment(key string) error {
    _, name, err := cache.SplitMetaNamespaceKey(key)
    if err != nil {
        return err
    }
    
    // Get RuntimeEnvironment resource
    runtime, err := c.runtimeLister.Get(name)
    if errors.IsNotFound(err) {
        // Runtime was deleted, nothing to do
        return nil
    }
    if err != nil {
        return err
    }
    
    // Deep copy to avoid modifying cache
    runtime = runtime.DeepCopy()
    
    // Check if image exists
    if err := c.validateRuntimeImage(runtime); err != nil {
        // Update status to unavailable
        runtime.Status.Available = false
        runtime.Status.LastValidated = metav1.Now()
        
        _, err = c.llmsafespaceClient.LlmsafespaceV1().RuntimeEnvironments().UpdateStatus(
            context.TODO(), runtime, metav1.UpdateOptions{})
        
        c.recorder.Event(runtime, corev1.EventTypeWarning, "ValidationFailed", 
            fmt.Sprintf("Runtime validation failed: %v", err))
        return err
    }
    
    // Update status to available
    runtime.Status.Available = true
    runtime.Status.LastValidated = metav1.Now()
    
    _, err = c.llmsafespaceClient.LlmsafespaceV1().RuntimeEnvironments().UpdateStatus(
        context.TODO(), runtime, metav1.UpdateOptions{})
    
    return err
}
```

## Shared Components and Utilities

The combined controller uses shared components and utilities across all reconciliation loops to avoid code duplication and ensure consistent behavior.

### 1. WarmPodAllocator

The WarmPodAllocator is responsible for finding and allocating warm pods to sandboxes:

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

### 2. Resource Management

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
    
    // Create persistent volume claim if needed
    if sandbox.Spec.Storage != nil && sandbox.Spec.Storage.Persistent {
        if err := c.volumeManager.EnsurePersistentVolumeClaim(sandbox); err != nil {
            return err
        }
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

// assignWarmPodToSandbox assigns a warm pod to a sandbox
func (c *Controller) assignWarmPodToSandbox(sandbox *llmsafespacev1.Sandbox, warmPod *llmsafespacev1.WarmPod) error {
    // Update warm pod status to Assigned
    warmPodCopy := warmPod.DeepCopy()
    warmPodCopy.Status.Phase = "Assigned"
    warmPodCopy.Status.AssignedTo = string(sandbox.UID)
    warmPodCopy.Status.AssignedAt = metav1.Now()
    
    _, err := c.llmsafespaceClient.LlmsafespaceV1().WarmPods(warmPod.Namespace).UpdateStatus(
        context.TODO(), warmPodCopy, metav1.UpdateOptions{})
    if err != nil {
        return err
    }
    
    // Update sandbox to reference the warm pod
    sandboxCopy := sandbox.DeepCopy()
    sandboxCopy.Status.WarmPodRef = &llmsafespacev1.WarmPodReference{
        Name: warmPod.Name,
        Namespace: warmPod.Namespace,
    }
    
    _, err = c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).UpdateStatus(
        context.TODO(), sandboxCopy, metav1.UpdateOptions{})
    
    // Record metrics for warm pod assignment
    warmPoolAssignmentDurationSeconds.WithLabelValues(
        warmPod.Spec.PoolRef.Name,
        strings.Split(sandbox.Spec.Runtime, ":")[0],
    ).Observe(time.Since(warmPod.CreationTimestamp.Time).Seconds())
    
    return err
}
```

### 2. Pod Creation and Configuration

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

### 3. Network Policy Configuration

Network policies are created to enforce network isolation between sandboxes and control egress traffic.

```go
func (n *NetworkPolicyManager) createNetworkPolicies(sandbox *llmsafespacev1.Sandbox) error {
    // Determine namespace
    namespace := sandbox.Namespace
    if n.config.NamespaceIsolation {
        namespace = fmt.Sprintf("sandbox-%s", sandbox.UID)
    }
    
    // Create default deny policy
    defaultDenyPolicy := &networkingv1.NetworkPolicy{
        ObjectMeta: metav1.ObjectMeta{
            Name: fmt.Sprintf("sandbox-%s-default-deny", sandbox.Name),
            Namespace: namespace,
            OwnerReferences: []metav1.OwnerReference{
                *metav1.NewControllerRef(sandbox, llmsafespacev1.SchemeGroupVersion.WithKind("Sandbox")),
            },
        },
        Spec: networkingv1.NetworkPolicySpec{
            PodSelector: metav1.LabelSelector{
                MatchLabels: map[string]string{
                    "sandbox-id": sandbox.Name,
                },
            },
            PolicyTypes: []networkingv1.PolicyType{
                networkingv1.PolicyTypeIngress,
                networkingv1.PolicyTypeEgress,
            },
        },
    }
    
    _, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Create(
        context.TODO(), defaultDenyPolicy, metav1.CreateOptions{})
    if err != nil && !errors.IsAlreadyExists(err) {
        return err
    }
    
    // Create API service access policy
    apiServicePolicy := &networkingv1.NetworkPolicy{
        ObjectMeta: metav1.ObjectMeta{
            Name: fmt.Sprintf("sandbox-%s-api-access", sandbox.Name),
            Namespace: namespace,
            OwnerReferences: []metav1.OwnerReference{
                *metav1.NewControllerRef(sandbox, llmsafespacev1.SchemeGroupVersion.WithKind("Sandbox")),
            },
        },
        Spec: networkingv1.NetworkPolicySpec{
            PodSelector: metav1.LabelSelector{
                MatchLabels: map[string]string{
                    "sandbox-id": sandbox.Name,
                },
            },
            Ingress: []networkingv1.NetworkPolicyIngressRule{
                {
                    From: []networkingv1.NetworkPolicyPeer{
                        {
                            PodSelector: &metav1.LabelSelector{
                                MatchLabels: map[string]string{
                                    "app": "llmsafespace",
                                    "component": "api",
                                },
                            },
                            NamespaceSelector: &metav1.LabelSelector{
                                MatchLabels: map[string]string{
                                    "name": "llmsafespace",
                                },
                            },
                        },
                    },
                    Ports: []networkingv1.NetworkPolicyPort{
                        {
                            Port: &intstr.IntOrString{
                                Type: intstr.Int,
                                IntVal: 8080,
                            },
                            Protocol: &[]corev1.Protocol{corev1.ProtocolTCP}[0],
                        },
                    },
                },
            },
            Egress: []networkingv1.NetworkPolicyEgressRule{
                {
                    To: []networkingv1.NetworkPolicyPeer{
                        {
                            PodSelector: &metav1.LabelSelector{
                                MatchLabels: map[string]string{
                                    "app": "llmsafespace",
                                    "component": "api",
                                },
                            },
                            NamespaceSelector: &metav1.LabelSelector{
                                MatchLabels: map[string]string{
                                    "name": "llmsafespace",
                                },
                            },
                        },
                    },
                },
            },
            PolicyTypes: []networkingv1.PolicyType{
                networkingv1.PolicyTypeIngress,
                networkingv1.PolicyTypeEgress,
            },
        },
    }
    
    _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Create(
        context.TODO(), apiServicePolicy, metav1.CreateOptions{})
    if err != nil && !errors.IsAlreadyExists(err) {
        return err
    }
    
    // Create egress policies if specified
    if sandbox.Spec.NetworkAccess != nil && len(sandbox.Spec.NetworkAccess.Egress) > 0 {
        egressPolicy := &networkingv1.NetworkPolicy{
            ObjectMeta: metav1.ObjectMeta{
                Name: fmt.Sprintf("sandbox-%s-egress", sandbox.Name),
                Namespace: namespace,
                OwnerReferences: []metav1.OwnerReference{
                    *metav1.NewControllerRef(sandbox, llmsafespacev1.SchemeGroupVersion.WithKind("Sandbox")),
                },
            },
            Spec: networkingv1.NetworkPolicySpec{
                PodSelector: metav1.LabelSelector{
                    MatchLabels: map[string]string{
                        "sandbox-id": sandbox.Name,
                    },
                },
                Egress: []networkingv1.NetworkPolicyEgressRule{
                    {
                        To: []networkingv1.NetworkPolicyPeer{
                            {
                                IPBlock: &networkingv1.IPBlock{
                                    CIDR: "0.0.0.0/0",
                                    Except: []string{
                                        "10.0.0.0/8",
                                        "172.16.0.0/12",
                                        "192.168.0.0/16",
                                    },
                                },
                            },
                        },
                        Ports: []networkingv1.NetworkPolicyPort{
                            {
                                Port: &intstr.IntOrString{
                                    Type: intstr.Int,
                                    IntVal: 443,
                                },
                                Protocol: &[]corev1.Protocol{corev1.ProtocolTCP}[0],
                            },
                            {
                                Port: &intstr.IntOrString{
                                    Type: intstr.Int,
                                    IntVal: 80,
                                },
                                Protocol: &[]corev1.Protocol{corev1.ProtocolTCP}[0],
                            },
                        },
                    },
                },
                PolicyTypes: []networkingv1.PolicyType{
                    networkingv1.PolicyTypeEgress,
                },
            },
        }
        
        _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Create(
            context.TODO(), egressPolicy, metav1.CreateOptions{})
        if err != nil && !errors.IsAlreadyExists(err) {
            return err
        }
    }
    
    return nil
}
```

### 4. Sandbox Cleanup

When a Sandbox is deleted, the controller cleans up all associated resources.

```go
func (c *Controller) handleSandboxDeletion(sandbox *llmsafespacev1.Sandbox) error {
    // Update phase to Terminating if not already
    if sandbox.Status.Phase != "Terminating" {
        sandbox.Status.Phase = "Terminating"
        sandbox, err := c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).UpdateStatus(
            context.TODO(), sandbox, metav1.UpdateOptions{})
        if err != nil {
            return err
        }
    }
    
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
    
    // Delete pod
    if sandbox.Status.PodName != "" {
        podNamespace := sandbox.Status.PodNamespace
        if podNamespace == "" {
            podNamespace = sandbox.Namespace
        }
        
        err := c.kubeClient.CoreV1().Pods(podNamespace).Delete(
            context.TODO(), sandbox.Status.PodName, metav1.DeleteOptions{})
        if err != nil && !errors.IsNotFound(err) {
            return err
        }
    }
    
    // Delete service
    err := c.serviceManager.DeleteService(sandbox)
    if err != nil {
        return err
    }
    
    // Delete network policies
    err = c.networkPolicyManager.DeleteNetworkPolicies(sandbox)
    if err != nil {
        return err
    }
    
    // Delete service account
    err = c.serviceAccountManager.DeleteServiceAccount(sandbox)
    if err != nil {
        return err
    }
    
    // Delete PVC if it exists
    if sandbox.Spec.Storage != nil && sandbox.Spec.Storage.Persistent {
        err = c.volumeManager.DeletePersistentVolumeClaim(sandbox)
        if err != nil {
            return err
        }
    }
    
    // Delete namespace if using namespace isolation
    if c.config.NamespaceIsolation {
        err = c.namespaceManager.DeleteNamespace(sandbox)
        if err != nil {
            return err
        }
    }
    
    // Remove finalizer
    sandbox.Finalizers = removeString(sandbox.Finalizers, sandboxFinalizer)
    _, err = c.llmsafespaceClient.LlmsafespaceV1().Sandboxes(sandbox.Namespace).Update(
        context.TODO(), sandbox, metav1.UpdateOptions{})
    
    return err
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
    
    // Record metrics for pod recycling
    warmPoolRecycleTotal.WithLabelValues(
        warmPod.Spec.PoolRef.Name,
        strings.Split(sandbox.Spec.Runtime, ":")[0],
        "true",
    ).Inc()
    
    return err
}
```

### 5. Warm Pod Management

The controller includes functionality for managing warm pods, including creation, assignment, and recycling.

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

## Error Handling and Recovery

### 1. Transient Errors

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

### 2. Permanent Errors

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

## Work Queue Processing

The combined controller uses a single work queue for all resource types, with a mechanism to determine the resource type from the queue key:

```go
// Controller manages the lifecycle of sandboxes and warm pools
type Controller struct {
    kubeClient        kubernetes.Interface
    llmsafespaceClient clientset.Interface
    
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
    warmPodAllocator  *WarmPodAllocator
    
    // Other utilities
    recorder          record.EventRecorder
    config            *config.Config
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

## Monitoring and Metrics

The combined controller exposes Prometheus metrics for monitoring its operation and the state of managed resources:

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
    
    sandboxesFailedTotal = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "llmsafespace_sandboxes_failed_total",
            Help: "Total number of sandboxes that failed to create",
        },
    )
    
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
    
    // Warm pool metrics
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

## Error Handling and Recovery

The combined controller implements comprehensive error handling and recovery mechanisms:

```go
// updateSandboxStatus updates the status of a sandbox with appropriate conditions
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

// shouldRequeue determines if an error should cause requeuing
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

## High Availability and Scaling

### 1. Leader Election

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
    
    // Start leader election
    leaderelection.RunOrDie(context.Background(), leaderelection.LeaderElectionConfig{
        Lock: lock,
        ReleaseOnCancel: true,
        LeaseDuration: 15 * time.Second,
        RenewDeadline: 10 * time.Second,
        RetryPeriod: 2 * time.Second,
        Callbacks: leaderelection.LeaderCallbacks{
            OnStartedLeading: func(ctx context.Context) {
                controller.Run(*workers, ctx.Done())
            },
            OnStoppedLeading: func() {
                klog.Info("Leader election lost")
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
```

### 2. Horizontal Scaling

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

## Conclusion

The Combined Controller is a critical component of the SecureAgent platform, responsible for managing the lifecycle of both sandbox environments and warm pools. By integrating these closely related functions into a single controller, we achieve better coordination, simplified architecture, and more efficient resource usage.

The controller's design follows Kubernetes best practices, including:

1. **Declarative API**: Using CRDs to define the desired state of resources
2. **Reconciliation Loop**: Continuously working to ensure the actual state matches the desired state
3. **Eventual Consistency**: Handling transient errors with retries and backoff
4. **Operator Pattern**: Encapsulating operational knowledge in the controller
5. **Defense in Depth**: Implementing multiple layers of security

The warm pool functionality significantly improves the user experience by reducing sandbox startup times. By maintaining pools of pre-initialized pods, the system can respond to sandbox creation requests much more quickly, which is particularly valuable for interactive use cases where users expect immediate feedback.

The combined controller approach provides a robust foundation for the SecureAgent platform, enabling secure code execution for LLM agents while maintaining flexibility and ease of use.
