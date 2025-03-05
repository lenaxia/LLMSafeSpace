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
        return nil
    }
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("sandboxprofile", "get").Inc()
        return err
    }
    
    // Deep copy to avoid modifying cache
    profile = profile.DeepCopy()
    
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
    
    // Update status to valid
    c.updateProfileStatus(profile, "ValidationSucceeded", "Profile is valid and ready to use", true)
    
    return nil
}

// validateProfileWithRuntimes validates that the profile is compatible with at least one runtime
func (c *Controller) validateProfileWithRuntimes(profile *llmsafespacev1.SandboxProfile) error {
    // Get all runtime environments for the specified language
    runtimes, err := c.runtimeLister.List(labels.SelectorFromSet(labels.Set{
        "language": profile.Spec.Language,
    }))
    
    if err != nil {
        return fmt.Errorf("failed to list runtimes: %v", err)
    }
    
    if len(runtimes) == 0 {
        return fmt.Errorf("no runtime environments found for language: %s", profile.Spec.Language)
    }
    
    // Check compatibility with at least one runtime
    compatible := false
    for _, runtime := range runtimes {
        if c.isProfileCompatibleWithRuntime(profile, runtime) {
            compatible = true
            break
        }
    }
    
    if !compatible {
        return fmt.Errorf("profile is not compatible with any available runtime for language: %s", profile.Spec.Language)
    }
    
    return nil
}

// isProfileCompatibleWithRuntime checks if a profile is compatible with a specific runtime
func (c *Controller) isProfileCompatibleWithRuntime(profile *llmsafespacev1.SandboxProfile, runtime *llmsafespacev1.RuntimeEnvironment) bool {
    // Check if all pre-installed packages required by the profile are available in the runtime
    for _, pkg := range profile.Spec.PreInstalledPackages {
        found := false
        for _, rtPkg := range runtime.Spec.PreInstalledPackages {
            if pkg == rtPkg {
                found = true
                break
            }
        }
        if !found {
            return false
        }
    }
    
    // Check if the runtime supports the security level required by the profile
    if profile.Spec.SecurityLevel == "high" {
        hasGvisor := false
        for _, feature := range runtime.Spec.SecurityFeatures {
            if feature == "gvisor" {
                hasGvisor = true
                break
            }
        }
        if !hasGvisor {
            return false
        }
    }
    
    return true
}

// validateProfileSecurityPolicies validates the security policies in the profile
func (c *Controller) validateProfileSecurityPolicies(profile *llmsafespacev1.SandboxProfile) error {
    // Validate seccomp profile if specified
    if profile.Spec.SeccompProfile != "" {
        // Check if the seccomp profile exists and is valid
        if !c.seccompProfileExists(profile.Spec.SeccompProfile) {
            return fmt.Errorf("seccomp profile not found: %s", profile.Spec.SeccompProfile)
        }
    }
    
    // Validate filesystem configuration
    if profile.Spec.FilesystemConfig != nil {
        // Check for conflicting paths
        for _, readOnlyPath := range profile.Spec.FilesystemConfig.ReadOnlyPaths {
            for _, writablePath := range profile.Spec.FilesystemConfig.WritablePaths {
                if strings.HasPrefix(writablePath, readOnlyPath) || strings.HasPrefix(readOnlyPath, writablePath) {
                    return fmt.Errorf("conflicting paths in filesystem config: %s (read-only) and %s (writable)", 
                        readOnlyPath, writablePath)
                }
            }
        }
    }
    
    return nil
}

// validateProfileNetworkPolicies validates the network policies in the profile
func (c *Controller) validateProfileNetworkPolicies(profile *llmsafespacev1.SandboxProfile) error {
    if profile.Spec.NetworkPolicies == nil {
        return nil
    }
    
    for _, policy := range profile.Spec.NetworkPolicies {
        for _, rule := range policy.Rules {
            // Validate CIDR if specified
            if rule.CIDR != "" {
                if _, _, err := net.ParseCIDR(rule.CIDR); err != nil {
                    return fmt.Errorf("invalid CIDR in network policy: %s", rule.CIDR)
                }
            }
            
            // Validate ports
            for _, port := range rule.Ports {
                if port.Port < 1 || port.Port > 65535 {
                    return fmt.Errorf("invalid port in network policy: %d", port.Port)
                }
            }
        }
    }
    
    return nil
}

// seccompProfileExists checks if a seccomp profile exists
func (c *Controller) seccompProfileExists(profilePath string) bool {
    // This would check if the seccomp profile exists in the system
    // For now, we'll just return true for common profiles
    return strings.HasPrefix(profilePath, "profiles/") || 
           profilePath == "runtime/default" || 
           profilePath == "localhost/default"
}

// updateProfileStatus updates the status of a SandboxProfile with appropriate conditions
func (c *Controller) updateProfileStatus(profile *llmsafespacev1.SandboxProfile, reason, message string, valid bool) error {
    // Add or update condition
    now := metav1.Now()
    condition := llmsafespacev1.ProfileCondition{
        Type:               reason,
        Status:             metav1.ConditionTrue,
        LastTransitionTime: now,
        Reason:             reason,
        Message:            message,
    }
    
    // Update or append the condition
    found := false
    for i, cond := range profile.Status.Conditions {
        if cond.Type == reason {
            profile.Status.Conditions[i] = condition
            found = true
            break
        }
    }
    
    if !found {
        profile.Status.Conditions = append(profile.Status.Conditions, condition)
    }
    
    // Update the valid status
    profile.Status.Valid = valid
    
    _, err := c.llmsafespaceClient.LlmsafespaceV1().SandboxProfiles(profile.Namespace).UpdateStatus(
        context.TODO(), profile, metav1.UpdateOptions{})
    
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("sandboxprofile", "status_update").Inc()
        return err
    }
    
    // Record event based on validity
    eventType := corev1.EventTypeNormal
    if !valid {
        eventType = corev1.EventTypeWarning
    }
    
    c.recorder.Event(profile, eventType, reason, message)
    
    return nil
}
```

### 5. RuntimeEnvironment Reconciliation Loop

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
        return nil
    }
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("runtimeenvironment", "get").Inc()
        return err
    }
    
    // Deep copy to avoid modifying cache
    runtime = runtime.DeepCopy()
    
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
    
    return nil
}

// validateRuntimeCompatibility checks if the runtime is compatible with the current system
func (c *Controller) validateRuntimeCompatibility(runtime *llmsafespacev1.RuntimeEnvironment) error {
    // Check if the runtime requires features that are available in the cluster
    for _, feature := range runtime.Spec.SecurityFeatures {
        if feature == "gvisor" && !c.isGvisorAvailable() {
            return fmt.Errorf("gvisor runtime is required but not available in the cluster")
        }
    }
    
    // Check resource requirements against cluster capacity
    if runtime.Spec.ResourceRequirements != nil {
        // Validate that the minimum resource requirements can be met
        // This could involve checking node capacities or resource quotas
    }
    
    return nil
}

// validateRuntimeSecurity validates the security aspects of the runtime
func (c *Controller) validateRuntimeSecurity(runtime *llmsafespacev1.RuntimeEnvironment) error {
    // Check if the runtime image has known vulnerabilities
    // This could integrate with container scanning tools
    
    // Verify that the runtime supports required security features
    // like seccomp, AppArmor, or SELinux profiles
    
    return nil
}

// updateRuntimeStatus updates the status of a RuntimeEnvironment with appropriate conditions
func (c *Controller) updateRuntimeStatus(runtime *llmsafespacev1.RuntimeEnvironment, available bool, reason, message string) error {
    runtime.Status.Available = available
    runtime.Status.LastValidated = metav1.Now()
    
    // Add or update condition
    now := metav1.Now()
    condition := llmsafespacev1.RuntimeEnvironmentCondition{
        Type:               reason,
        Status:             metav1.ConditionTrue,
        LastTransitionTime: now,
        Reason:             reason,
        Message:            message,
    }
    
    // Update or append the condition
    found := false
    for i, cond := range runtime.Status.Conditions {
        if cond.Type == reason {
            runtime.Status.Conditions[i] = condition
            found = true
            break
        }
    }
    
    if !found {
        runtime.Status.Conditions = append(runtime.Status.Conditions, condition)
    }
    
    _, err := c.llmsafespaceClient.LlmsafespaceV1().RuntimeEnvironments().UpdateStatus(
        context.TODO(), runtime, metav1.UpdateOptions{})
    
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("runtimeenvironment", "status_update").Inc()
        return err
    }
    
    // Record event based on availability
    eventType := corev1.EventTypeNormal
    if !available {
        eventType = corev1.EventTypeWarning
    }
    
    c.recorder.Event(runtime, eventType, reason, message)
    
    return nil
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

// shouldRecyclePod determines if a warm pod should be recycled
func (c *Controller) shouldRecyclePod(warmPod *llmsafespacev1.WarmPod, sandbox *llmsafespacev1.Sandbox) bool {
    // Get the pool
    pool, err := c.warmPoolLister.WarmPools(warmPod.Spec.PoolRef.Namespace).Get(warmPod.Spec.PoolRef.Name)
    if err != nil {
        klog.Warningf("Failed to get warm pool %s/%s: %v", 
            warmPod.Spec.PoolRef.Namespace, warmPod.Spec.PoolRef.Name, err)
        return false
    }
    
    // Check if the pool still exists and needs more pods
    if pool.Status.AvailablePods < pool.Spec.MinSize {
        // Check if the pod has been running for too long
        if warmPod.Spec.CreationTimestamp.Add(24 * time.Hour).Before(time.Now()) {
            klog.V(4).Infof("Pod %s is too old (>24h), not recycling", warmPod.Status.PodName)
            return false
        }
        
        // Check if the sandbox has security events that would make recycling unsafe
        if c.hasSandboxSecurityEvents(sandbox) {
            klog.V(2).Infof("Sandbox %s has security events, not recycling pod %s", 
                sandbox.Name, warmPod.Status.PodName)
            return false
        }
        
        // Check if the sandbox installed packages
        if c.hasInstalledUntrustedPackages(sandbox) {
            klog.V(2).Infof("Sandbox %s installed untrusted packages, not recycling pod %s", 
                sandbox.Name, warmPod.Status.PodName)
            return false
        }
        
        // Check if the sandbox modified system files
        if c.hasModifiedSystemFiles(sandbox) {
            klog.V(2).Infof("Sandbox %s modified system files, not recycling pod %s", 
                sandbox.Name, warmPod.Status.PodName)
            return false
        }
        
        // Check resource usage history
        if c.hadExcessiveResourceUsage(sandbox) {
            klog.V(2).Infof("Sandbox %s had excessive resource usage, not recycling pod %s", 
                sandbox.Name, warmPod.Status.PodName)
            return false
        }
        
        // Check if the pod has been recycled too many times
        recycleCount := c.getPodRecycleCount(warmPod)
        if recycleCount >= c.config.MaxPodRecycleCount {
            klog.V(2).Infof("Pod %s has been recycled %d times (max: %d), not recycling", 
                warmPod.Status.PodName, recycleCount, c.config.MaxPodRecycleCount)
            return false
        }
        
        klog.V(4).Infof("Pod %s is eligible for recycling", warmPod.Status.PodName)
        return true
    }
    
    klog.V(4).Infof("Pool %s has sufficient pods, not recycling pod %s", 
        pool.Name, warmPod.Status.PodName)
    return false
}

// hasSandboxSecurityEvents checks if a sandbox has recorded security events
func (c *Controller) hasSandboxSecurityEvents(sandbox *llmsafespacev1.Sandbox) bool {
    // Check for security events in sandbox status or audit logs
    for _, condition := range sandbox.Status.Conditions {
        if strings.HasPrefix(condition.Type, "Security") && condition.Status == "True" {
            return true
        }
    }
    
    // Could also check external security monitoring systems
    return false
}

// hasInstalledUntrustedPackages checks if a sandbox installed packages not in the allowlist
func (c *Controller) hasInstalledUntrustedPackages(sandbox *llmsafespacev1.Sandbox) bool {
    // This would check if the sandbox installed any packages not in the allowlist
    // For now, we'll check if the sandbox has a package installation record
    
    // Check if we have package installation tracking in annotations
    if sandbox.Annotations != nil {
        if packagesStr, ok := sandbox.Annotations["llmsafespace.dev/installed-packages"]; ok {
            packages := strings.Split(packagesStr, ",")
            
            // Check each package against the allowlist
            for _, pkg := range packages {
                if !c.isPackageAllowed(pkg, sandbox.Spec.Runtime) {
                    return true
                }
            }
        }
    }
    
    return false
}

// isPackageAllowed checks if a package is in the allowlist for a runtime
func (c *Controller) isPackageAllowed(pkg, runtime string) bool {
    // This would check against a configured allowlist
    // For now, we'll assume common packages are allowed
    allowedPackages := map[string][]string{
        "python": {"numpy", "pandas", "matplotlib", "scikit-learn", "tensorflow", "torch", "requests"},
        "nodejs": {"axios", "express", "lodash", "moment", "react", "vue"},
    }
    
    runtimeLang := strings.Split(runtime, ":")[0]
    allowed, ok := allowedPackages[runtimeLang]
    if !ok {
        return false
    }
    
    for _, allowedPkg := range allowed {
        if pkg == allowedPkg || strings.HasPrefix(pkg, allowedPkg+"==") {
            return true
        }
    }
    
    return false
}

// hasModifiedSystemFiles checks if a sandbox modified any system files
func (c *Controller) hasModifiedSystemFiles(sandbox *llmsafespacev1.Sandbox) bool {
    // This would check if the sandbox modified any system files
    // In a real implementation, this could use file integrity monitoring
    
    // For now, we'll check if there's a record of file modifications in annotations
    if sandbox.Annotations != nil {
        if _, ok := sandbox.Annotations["llmsafespace.dev/modified-system-files"]; ok {
            return true
        }
    }
    
    return false
}

// hadExcessiveResourceUsage checks if a sandbox had excessive resource usage
func (c *Controller) hadExcessiveResourceUsage(sandbox *llmsafespacev1.Sandbox) bool {
    // This would check if the sandbox had excessive resource usage
    // For now, we'll check if there's a record of resource usage in annotations
    if sandbox.Annotations != nil {
        if cpuUsageStr, ok := sandbox.Annotations["llmsafespace.dev/max-cpu-usage"]; ok {
            cpuUsage, err := strconv.ParseFloat(cpuUsageStr, 64)
            if err == nil && cpuUsage > 0.9 { // 90% CPU usage threshold
                return true
            }
        }
        
        if memUsageStr, ok := sandbox.Annotations["llmsafespace.dev/max-memory-usage"]; ok {
            memUsage, err := strconv.ParseFloat(memUsageStr, 64)
            if err == nil && memUsage > 0.9 { // 90% memory usage threshold
                return true
            }
        }
    }
    
    return false
}

// getPodRecycleCount gets the number of times a pod has been recycled
func (c *Controller) getPodRecycleCount(warmPod *llmsafespacev1.WarmPod) int {
    if warmPod.Annotations == nil {
        return 0
    }
    
    countStr, ok := warmPod.Annotations["llmsafespace.dev/recycle-count"]
    if !ok {
        return 0
    }
    
    count, err := strconv.Atoi(countStr)
    if err != nil {
        return 0
    }
    
    return count
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

// cleanupPodEnvironment cleans up the pod environment for recycling
func (c *Controller) cleanupPodEnvironment(pod *corev1.Pod) error {
    // Execute cleanup script in the pod
    execReq := c.kubeClient.CoreV1().RESTClient().Post().
        Resource("pods").
        Name(pod.Name).
        Namespace(pod.Namespace).
        SubResource("exec").
        VersionedParams(&corev1.PodExecOptions{
            Command: []string{"/bin/sh", "-c", c.config.PodCleanupScript},
            Stdin:   false,
            Stdout:  true,
            Stderr:  true,
            TTY:     false,
            Container: "sandbox",
        }, scheme.ParameterCodec)
    
    // Execute the command
    exec, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", execReq.URL())
    if err != nil {
        return fmt.Errorf("failed to create executor: %v", err)
    }
    
    var stdout, stderr bytes.Buffer
    err = exec.Stream(remotecommand.StreamOptions{
        Stdout: &stdout,
        Stderr: &stderr,
    })
    
    if err != nil {
        return fmt.Errorf("cleanup script failed: %v, stderr: %s", err, stderr.String())
    }
    
    if stderr.Len() > 0 {
        klog.Warningf("Cleanup script for pod %s/%s produced stderr: %s", 
            pod.Namespace, pod.Name, stderr.String())
    }
    
    klog.V(4).Infof("Cleanup script for pod %s/%s completed: %s", 
        pod.Namespace, pod.Name, stdout.String())
    
    return nil
}

// verifyPodCleanup verifies that the pod is properly cleaned up
func (c *Controller) verifyPodCleanup(pod *corev1.Pod) error {
    // Execute verification script in the pod
    execReq := c.kubeClient.CoreV1().RESTClient().Post().
        Resource("pods").
        Name(pod.Name).
        Namespace(pod.Namespace).
        SubResource("exec").
        VersionedParams(&corev1.PodExecOptions{
            Command: []string{"/bin/sh", "-c", c.config.PodVerifyScript},
            Stdin:   false,
            Stdout:  true,
            Stderr:  true,
            TTY:     false,
            Container: "sandbox",
        }, scheme.ParameterCodec)
    
    // Execute the command
    exec, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", execReq.URL())
    if err != nil {
        return fmt.Errorf("failed to create verification executor: %v", err)
    }
    
    var stdout, stderr bytes.Buffer
    err = exec.Stream(remotecommand.StreamOptions{
        Stdout: &stdout,
        Stderr: &stderr,
    })
    
    if err != nil {
        return fmt.Errorf("verification script failed: %v, stderr: %s", err, stderr.String())
    }
    
    // Check verification output
    if strings.Contains(stdout.String(), "VERIFICATION_FAILED") {
        return fmt.Errorf("pod verification failed: %s", stdout.String())
    }
    
    return nil
}

// reinitializePod reinitializes a pod after recycling
func (c *Controller) reinitializePod(pod *corev1.Pod, warmPod *llmsafespacev1.WarmPod) error {
    // Get the warm pool
    pool, err := c.warmPoolLister.WarmPools(warmPod.Spec.PoolRef.Namespace).Get(warmPod.Spec.PoolRef.Name)
    if err != nil {
        return fmt.Errorf("failed to get warm pool: %v", err)
    }
    
    // Run preload scripts if defined
    if pool.Spec.PreloadScripts != nil && len(pool.Spec.PreloadScripts) > 0 {
        for _, script := range pool.Spec.PreloadScripts {
            if err := c.runPreloadScript(pod, script.Name, script.Content); err != nil {
                return fmt.Errorf("failed to run preload script %s: %v", script.Name, err)
            }
        }
    }
    
    return nil
}

// runPreloadScript runs a preload script in a pod
func (c *Controller) runPreloadScript(pod *corev1.Pod, scriptName, scriptContent string) error {
    // Create a temporary script file
    tempScript := fmt.Sprintf("/tmp/%s.sh", scriptName)
    
    // Write script to the pod
    execReq := c.kubeClient.CoreV1().RESTClient().Post().
        Resource("pods").
        Name(pod.Name).
        Namespace(pod.Namespace).
        SubResource("exec").
        VersionedParams(&corev1.PodExecOptions{
            Command: []string{"/bin/sh", "-c", fmt.Sprintf("cat > %s << 'EOF'\n%s\nEOF\nchmod +x %s", 
                tempScript, scriptContent, tempScript)},
            Stdin:   false,
            Stdout:  true,
            Stderr:  true,
            TTY:     false,
            Container: "sandbox",
        }, scheme.ParameterCodec)
    
    exec, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", execReq.URL())
    if err != nil {
        return fmt.Errorf("failed to create executor for script creation: %v", err)
    }
    
    var stdout, stderr bytes.Buffer
    err = exec.Stream(remotecommand.StreamOptions{
        Stdout: &stdout,
        Stderr: &stderr,
    })
    
    if err != nil {
        return fmt.Errorf("failed to write script: %v, stderr: %s", err, stderr.String())
    }
    
    // Execute the script
    execReq = c.kubeClient.CoreV1().RESTClient().Post().
        Resource("pods").
        Name(pod.Name).
        Namespace(pod.Namespace).
        SubResource("exec").
        VersionedParams(&corev1.PodExecOptions{
            Command: []string{"/bin/sh", tempScript},
            Stdin:   false,
            Stdout:  true,
            Stderr:  true,
            TTY:     false,
            Container: "sandbox",
        }, scheme.ParameterCodec)
    
    exec, err = remotecommand.NewSPDYExecutor(c.restConfig, "POST", execReq.URL())
    if err != nil {
        return fmt.Errorf("failed to create executor for script execution: %v", err)
    }
    
    stdout.Reset()
    stderr.Reset()
    err = exec.Stream(remotecommand.StreamOptions{
        Stdout: &stdout,
        Stderr: &stderr,
    })
    
    if err != nil {
        return fmt.Errorf("script execution failed: %v, stderr: %s", err, stderr.String())
    }
    
    klog.V(4).Infof("Preload script %s for pod %s/%s completed: %s", 
        scriptName, pod.Namespace, pod.Name, stdout.String())
    
    // Clean up the script
    execReq = c.kubeClient.CoreV1().RESTClient().Post().
        Resource("pods").
        Name(pod.Name).
        Namespace(pod.Namespace).
        SubResource("exec").
        VersionedParams(&corev1.PodExecOptions{
            Command: []string{"/bin/sh", "-c", fmt.Sprintf("rm -f %s", tempScript)},
            Stdin:   false,
            Stdout:  true,
            Stderr:  true,
            TTY:     false,
            Container: "sandbox",
        }, scheme.ParameterCodec)
    
    exec, err = remotecommand.NewSPDYExecutor(c.restConfig, "POST", execReq.URL())
    if err != nil {
        klog.Warningf("Failed to create executor for script cleanup: %v", err)
        return nil // Non-fatal error
    }
    
    stdout.Reset()
    stderr.Reset()
    _ = exec.Stream(remotecommand.StreamOptions{
        Stdout: &stdout,
        Stderr: &stderr,
    })
    
    return nil
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

// APIServiceClient handles communication with the API service
type APIServiceClient struct {
    baseURL     string
    httpClient  *http.Client
    authToken   string
}

// NewAPIServiceClient creates a new API service client
func NewAPIServiceClient(baseURL, authToken string) *APIServiceClient {
    return &APIServiceClient{
        baseURL:    baseURL,
        authToken:  authToken,
        httpClient: &http.Client{
            Timeout: 10 * time.Second,
        },
    }
}

// NotifySandboxStatus notifies the API service about sandbox status changes
func (c *APIServiceClient) NotifySandboxStatus(sandbox *llmsafespacev1.Sandbox) error {
    startTime := time.Now()
    defer func() {
        apiServiceLatencySeconds.WithLabelValues("notify_status").Observe(time.Since(startTime).Seconds())
    }()
    
    url := fmt.Sprintf("%s/internal/sandboxes/%s/status", c.baseURL, sandbox.Name)
    
    payload := map[string]interface{}{
        "status":      sandbox.Status.Phase,
        "podName":     sandbox.Status.PodName,
        "podNamespace": sandbox.Status.PodNamespace,
        "endpoint":    sandbox.Status.Endpoint,
        "startTime":   sandbox.Status.StartTime,
        "conditions":  sandbox.Status.Conditions,
    }
    
    jsonPayload, err := json.Marshal(payload)
    if err != nil {
        apiServiceRequestsTotal.WithLabelValues("notify_status", "error").Inc()
        return fmt.Errorf("failed to marshal payload: %v", err)
    }
    
    req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
    if err != nil {
        apiServiceRequestsTotal.WithLabelValues("notify_status", "error").Inc()
        return fmt.Errorf("failed to create request: %v", err)
    }
    
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.authToken))
    
    resp, err := c.httpClient.Do(req)
    if err != nil {
        apiServiceRequestsTotal.WithLabelValues("notify_status", "error").Inc()
        return fmt.Errorf("failed to send request: %v", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        apiServiceRequestsTotal.WithLabelValues("notify_status", "error").Inc()
        return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
    }
    
    apiServiceRequestsTotal.WithLabelValues("notify_status", "success").Inc()
    return nil
}

// RequestWarmPod requests a warm pod from the API service
func (c *APIServiceClient) RequestWarmPod(runtime, securityLevel string) (*llmsafespacev1.WarmPod, error) {
    startTime := time.Now()
    defer func() {
        apiServiceLatencySeconds.WithLabelValues("request_warmpod").Observe(time.Since(startTime).Seconds())
    }()
    
    url := fmt.Sprintf("%s/internal/warmpods/allocate", c.baseURL)
    
    payload := map[string]string{
        "runtime":       runtime,
        "securityLevel": securityLevel,
    }
    
    jsonPayload, err := json.Marshal(payload)
    if err != nil {
        apiServiceRequestsTotal.WithLabelValues("request_warmpod", "error").Inc()
        return nil, fmt.Errorf("failed to marshal payload: %v", err)
    }
    
    req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
    if err != nil {
        apiServiceRequestsTotal.WithLabelValues("request_warmpod", "error").Inc()
        return nil, fmt.Errorf("failed to create request: %v", err)
    }
    
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.authToken))
    
    resp, err := c.httpClient.Do(req)
    if err != nil {
        apiServiceRequestsTotal.WithLabelValues("request_warmpod", "error").Inc()
        return nil, fmt.Errorf("failed to send request: %v", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode == http.StatusNotFound {
        apiServiceRequestsTotal.WithLabelValues("request_warmpod", "not_found").Inc()
        return nil, nil // No warm pod available
    }
    
    if resp.StatusCode != http.StatusOK {
        apiServiceRequestsTotal.WithLabelValues("request_warmpod", "error").Inc()
        return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
    }
    
    var result struct {
        WarmPod *llmsafespacev1.WarmPod `json:"warmPod"`
    }
    
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        apiServiceRequestsTotal.WithLabelValues("request_warmpod", "error").Inc()
        return nil, fmt.Errorf("failed to decode response: %v", err)
    }
    
    apiServiceRequestsTotal.WithLabelValues("request_warmpod", "success").Inc()
    return result.WarmPod, nil
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
    
    warmPoolRecycleDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_warmpool_recycle_duration_seconds",
            Help: "Time taken to recycle a warm pod",
            Buckets: prometheus.DefBuckets,
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
    
    // Security metrics
    securityEventsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_security_events_total",
            Help: "Total number of security events detected",
        },
        []string{"event_type", "severity", "runtime"},
    )
    
    // Resource usage metrics
    resourceUsageGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_resource_usage",
            Help: "Resource usage by sandboxes and warm pools",
        },
        []string{"resource_type", "component", "namespace"},
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
    
    // Network policy metrics
    networkPolicyOperationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_network_policy_operations_total",
            Help: "Total number of network policy operations",
        },
        []string{"operation", "status"},
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
    
    // Execute all registered shutdown handlers
    var shutdownErrors []error
    for _, handler := range c.shutdownHandlers {
        if err := handler(); err != nil {
            shutdownErrors = append(shutdownErrors, err)
            klog.Errorf("Error during shutdown: %v", err)
        }
    }
    
    // Wait for work queue to drain
    c.workqueue.ShutDown()
    klog.Info("Work queue shut down")
    
    // Wait for in-progress reconciliations to complete
    // This could be implemented with a wait group that tracks active reconciliations
    
    // Log final metrics before shutdown
    workqueueDepthGauge.WithLabelValues("controller").Set(0)
    
    if len(shutdownErrors) > 0 {
        return fmt.Errorf("errors during shutdown: %v", shutdownErrors)
    }
    
    klog.Info("Controller shutdown completed successfully")
    return nil
}

// RegisterShutdownHandler registers a function to be called during shutdown
func (c *Controller) RegisterShutdownHandler(handler func() error) {
    c.shutdownHandlers = append(c.shutdownHandlers, handler)
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
