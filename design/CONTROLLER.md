# Sandbox Controller Design for LLMSafeSpace

## Overview

The Sandbox Controller is a critical component of the LLMSafeSpace platform, responsible for managing the lifecycle of both sandbox environments and warm pools. This document provides a detailed design for the controller, including Custom Resource Definitions (CRDs), reconciliation loops, and resource lifecycle management.

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

The Sandbox Controller is structured as follows:

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

// validateWarmPool validates the warm pool configuration
func (c *Controller) validateWarmPool(warmPool *llmsafespacev1.WarmPool) error {
    // Check if the runtime exists
    runtimeExists := false
    runtimes, err := c.runtimeLister.List(labels.Everything())
    if err != nil {
        return fmt.Errorf("failed to list runtimes: %v", err)
    }
    
    for _, runtime := range runtimes {
        if runtime.Spec.Image == warmPool.Spec.Runtime {
            runtimeExists = true
            break
        }
    }
    
    if !runtimeExists {
        return fmt.Errorf("runtime %s does not exist", warmPool.Spec.Runtime)
    }
    
    // Validate min and max size
    if warmPool.Spec.MinSize < 0 {
        return fmt.Errorf("minSize must be non-negative")
    }
    
    if warmPool.Spec.MaxSize < 0 {
        return fmt.Errorf("maxSize must be non-negative")
    }
    
    if warmPool.Spec.MaxSize > 0 && warmPool.Spec.MinSize > warmPool.Spec.MaxSize {
        return fmt.Errorf("minSize (%d) cannot be greater than maxSize (%d)", 
            warmPool.Spec.MinSize, warmPool.Spec.MaxSize)
    }
    
    // Validate security level
    if warmPool.Spec.SecurityLevel != "standard" && 
       warmPool.Spec.SecurityLevel != "high" && 
       warmPool.Spec.SecurityLevel != "custom" {
        return fmt.Errorf("invalid security level: %s", warmPool.Spec.SecurityLevel)
    }
    
    // Validate profile reference if specified
    if warmPool.Spec.ProfileRef != nil {
        profileNamespace := warmPool.Namespace
        if warmPool.Spec.ProfileRef.Namespace != "" {
            profileNamespace = warmPool.Spec.ProfileRef.Namespace
        }
        
        _, err := c.profileLister.SandboxProfiles(profileNamespace).Get(warmPool.Spec.ProfileRef.Name)
        if err != nil {
            if errors.IsNotFound(err) {
                return fmt.Errorf("profile %s/%s not found", profileNamespace, warmPool.Spec.ProfileRef.Name)
            }
            return fmt.Errorf("failed to get profile: %v", err)
        }
    }
    
    // Validate auto-scaling configuration if enabled
    if warmPool.Spec.AutoScaling != nil && warmPool.Spec.AutoScaling.Enabled {
        if warmPool.Spec.AutoScaling.TargetUtilization < 1 || warmPool.Spec.AutoScaling.TargetUtilization > 100 {
            return fmt.Errorf("targetUtilization must be between 1 and 100, got %d", 
                warmPool.Spec.AutoScaling.TargetUtilization)
        }
        
        if warmPool.Spec.AutoScaling.ScaleDownDelay < 0 {
            return fmt.Errorf("scaleDownDelay must be non-negative, got %d", 
                warmPool.Spec.AutoScaling.ScaleDownDelay)
        }
    }
    
    // Validate preload packages if specified
    if warmPool.Spec.PreloadPackages != nil && len(warmPool.Spec.PreloadPackages) > 0 {
        runtimeLang := strings.Split(warmPool.Spec.Runtime, ":")[0]
        
        for _, pkg := range warmPool.Spec.PreloadPackages {
            if !c.isPackageAllowed(pkg, warmPool.Spec.Runtime) {
                return fmt.Errorf("package %s is not allowed for runtime %s", pkg, runtimeLang)
            }
        }
    }
    
    return nil
}

// scaleDownWarmPool scales down a warm pool by removing the specified number of pods
func (c *Controller) scaleDownWarmPool(warmPool *llmsafespacev1.WarmPool, count int) error {
    if count <= 0 {
        return nil
    }
    
    // List all warm pods for this pool
    selector := labels.SelectorFromSet(labels.Set{
        "app": "llmsafespace",
        "component": "warmpod",
        "pool": warmPool.Name,
    })
    warmPods, err := c.warmPodLister.WarmPods(warmPool.Namespace).List(selector)
    if err != nil {
        return fmt.Errorf("failed to list warm pods: %v", err)
    }
    
    // Filter for available pods only
    var availablePods []*llmsafespacev1.WarmPod
    for _, pod := range warmPods {
        if pod.Status.Phase == "Ready" {
            availablePods = append(availablePods, pod)
        }
    }
    
    // Sort by creation time (oldest first)
    sort.Slice(availablePods, func(i, j int) bool {
        return availablePods[i].CreationTimestamp.Before(&availablePods[j].CreationTimestamp)
    })
    
    // Remove the oldest pods up to the count
    podsToRemove := availablePods
    if len(podsToRemove) > count {
        podsToRemove = podsToRemove[:count]
    }
    
    for _, pod := range podsToRemove {
        klog.V(3).Infof("Deleting warm pod %s/%s as part of scale down", pod.Namespace, pod.Name)
        err := c.llmsafespaceClient.LlmsafespaceV1().WarmPods(pod.Namespace).Delete(
            context.TODO(), pod.Name, metav1.DeleteOptions{})
        if err != nil && !errors.IsNotFound(err) {
            return fmt.Errorf("failed to delete warm pod %s: %v", pod.Name, err)
        }
        warmPoolPodsDeletedTotal.WithLabelValues(warmPool.Name, warmPool.Spec.Runtime, "scale_down").Inc()
    }
    
    return nil
}

// cleanupExpiredWarmPods removes warm pods that have exceeded their TTL
func (c *Controller) cleanupExpiredWarmPods(warmPool *llmsafespacev1.WarmPool) error {
    if warmPool.Spec.TTL <= 0 {
        return nil
    }
    
    // List all warm pods for this pool
    selector := labels.SelectorFromSet(labels.Set{
        "app": "llmsafespace",
        "component": "warmpod",
        "pool": warmPool.Name,
    })
    warmPods, err := c.warmPodLister.WarmPods(warmPool.Namespace).List(selector)
    if err != nil {
        return fmt.Errorf("failed to list warm pods: %v", err)
    }
    
    // Find expired pods (only consider Ready pods, not Assigned or Pending)
    var expiredPods []*llmsafespacev1.WarmPod
    ttlDuration := time.Duration(warmPool.Spec.TTL) * time.Second
    
    for _, pod := range warmPods {
        if pod.Status.Phase == "Ready" {
            // Use last heartbeat time to determine expiry
            lastHeartbeat := pod.Spec.LastHeartbeat.Time
            if time.Since(lastHeartbeat) > ttlDuration {
                expiredPods = append(expiredPods, pod)
            }
        }
    }
    
    // Delete expired pods
    for _, pod := range expiredPods {
        klog.V(3).Infof("Deleting expired warm pod %s/%s (TTL: %ds, age: %v)", 
            pod.Namespace, pod.Name, warmPool.Spec.TTL, time.Since(pod.Spec.LastHeartbeat.Time))
        
        err := c.llmsafespaceClient.LlmsafespaceV1().WarmPods(pod.Namespace).Delete(
            context.TODO(), pod.Name, metav1.DeleteOptions{})
        if err != nil && !errors.IsNotFound(err) {
            return fmt.Errorf("failed to delete expired warm pod %s: %v", pod.Name, err)
        }
        warmPoolPodsDeletedTotal.WithLabelValues(warmPool.Name, warmPool.Spec.Runtime, "expired").Inc()
    }
    
    return nil
}

// updateWarmPoolStatus updates the status of a warm pool with appropriate conditions
func (c *Controller) updateWarmPoolStatus(warmPool *llmsafespacev1.WarmPool, reason, message string) error {
    // Add or update condition
    now := metav1.Now()
    condition := llmsafespacev1.WarmPoolCondition{
        Type:               reason,
        Status:             "True",
        Reason:             reason,
        Message:            message,
        LastTransitionTime: now,
    }
    
    // Update or append the condition
    found := false
    for i, cond := range warmPool.Status.Conditions {
        if cond.Type == reason {
            warmPool.Status.Conditions[i] = condition
            found = true
            break
        }
    }
    
    if !found {
        warmPool.Status.Conditions = append(warmPool.Status.Conditions, condition)
    }
    
    // Update the status
    _, err := c.llmsafespaceClient.LlmsafespaceV1().WarmPools(warmPool.Namespace).UpdateStatus(
        context.TODO(), warmPool, metav1.UpdateOptions{})
    
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("warmpool", "update_status").Inc()
        return err
    }
    
    // Record event
    eventType := corev1.EventTypeNormal
    if strings.Contains(reason, "Failed") {
        eventType = corev1.EventTypeWarning
    }
    
    c.recorder.Event(warmPool, eventType, reason, message)
    
    return nil
}

// handleWarmPoolDeletion handles the deletion of a WarmPool
func (c *Controller) handleWarmPoolDeletion(warmPool *llmsafespacev1.WarmPool) error {
    startTime := time.Now()
    klog.V(2).Infof("Handling deletion of warm pool %s/%s", warmPool.Namespace, warmPool.Name)
    
    // List all warm pods for this pool
    selector := labels.SelectorFromSet(labels.Set{
        "app": "llmsafespace",
        "component": "warmpod",
        "pool": warmPool.Name,
    })
    warmPods, err := c.warmPodLister.WarmPods(warmPool.Namespace).List(selector)
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("warmpool", "list_pods_deletion").Inc()
        return fmt.Errorf("failed to list warm pods: %v", err)
    }
    
    // Check if there are still assigned pods
    hasAssignedPods := false
    for _, pod := range warmPods {
        if pod.Status.Phase == "Assigned" {
            hasAssignedPods = true
            break
        }
    }
    
    if hasAssignedPods && !c.config.ForceDeleteWarmPoolWithAssignedPods {
        return fmt.Errorf("warm pool has assigned pods, cannot delete until they are released")
    }
    
    // Delete all warm pods
    for _, pod := range warmPods {
        klog.V(3).Infof("Deleting warm pod %s/%s as part of warm pool deletion", 
            pod.Namespace, pod.Name)
        
        err := c.llmsafespaceClient.LlmsafespaceV1().WarmPods(pod.Namespace).Delete(
            context.TODO(), pod.Name, metav1.DeleteOptions{})
        if err != nil && !errors.IsNotFound(err) {
            reconciliationErrorsTotal.WithLabelValues("warmpool", "delete_pod").Inc()
            return fmt.Errorf("failed to delete warm pod %s: %v", pod.Name, err)
        }
        warmPoolPodsDeletedTotal.WithLabelValues(warmPool.Name, warmPool.Spec.Runtime, "pool_deletion").Inc()
    }
    
    // Check if all pods are gone
    if len(warmPods) > 0 {
        // Recheck to see if pods are actually gone
        remainingPods, err := c.warmPodLister.WarmPods(warmPool.Namespace).List(selector)
        if err != nil {
            reconciliationErrorsTotal.WithLabelValues("warmpool", "recheck_pods").Inc()
            return fmt.Errorf("failed to recheck warm pods: %v", err)
        }
        
        if len(remainingPods) > 0 {
            // Some pods still exist, wait for them to be deleted
            return fmt.Errorf("waiting for %d warm pods to be deleted", len(remainingPods))
        }
    }
    
    // Remove finalizer
    warmPool.Finalizers = removeString(warmPool.Finalizers, warmPoolFinalizer)
    _, err = c.llmsafespaceClient.LlmsafespaceV1().WarmPools(warmPool.Namespace).Update(
        context.TODO(), warmPool, metav1.UpdateOptions{})
    if err != nil {
        reconciliationErrorsTotal.WithLabelValues("warmpool", "remove_finalizer").Inc()
        return fmt.Errorf("failed to remove finalizer: %v", err)
    }
    
    klog.V(2).Infof("Successfully deleted warm pool %s/%s in %v", 
        warmPool.Namespace, warmPool.Name, time.Since(startTime))
    
    // Record deletion metric
    warmPoolsDeletedTotal.Inc()
    
    // Remove metrics for this warm pool
    warmPoolSizeGauge.DeleteLabelValues(warmPool.Name, warmPool.Spec.Runtime, "available")
    warmPoolSizeGauge.DeleteLabelValues(warmPool.Name, warmPool.Spec.Runtime, "assigned")
    warmPoolSizeGauge.DeleteLabelValues(warmPool.Name, warmPool.Spec.Runtime, "pending")
    warmPoolUtilizationGauge.DeleteLabelValues(warmPool.Name, warmPool.Spec.Runtime)
    
    return nil
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

// validateSandboxProfile performs basic validation of a SandboxProfile
func (c *Controller) validateSandboxProfile(profile *llmsafespacev1.SandboxProfile) error {
    // Check if language is supported
    supportedLanguages := []string{"python", "nodejs", "go", "ruby", "java"}
    languageSupported := false
    for _, lang := range supportedLanguages {
        if profile.Spec.Language == lang {
            languageSupported = true
            break
        }
    }
    
    if !languageSupported {
        return fmt.Errorf("unsupported language: %s", profile.Spec.Language)
    }
    
    // Check security level
    if profile.Spec.SecurityLevel != "standard" && 
       profile.Spec.SecurityLevel != "high" && 
       profile.Spec.SecurityLevel != "custom" {
        return fmt.Errorf("invalid security level: %s", profile.Spec.SecurityLevel)
    }
    
    // Validate seccomp profile if specified
    if profile.Spec.SeccompProfile != "" {
        if !c.seccompProfileExists(profile.Spec.SeccompProfile) {
            return fmt.Errorf("seccomp profile not found: %s", profile.Spec.SeccompProfile)
        }
    }
    
    return nil
}

// validateProfileResourceDefaults validates the resource defaults in the profile
func (c *Controller) validateProfileResourceDefaults(profile *llmsafespacev1.SandboxProfile) error {
    if profile.Spec.ResourceDefaults == nil {
        return nil
    }
    
    // Validate CPU resource
    if profile.Spec.ResourceDefaults.CPU != "" {
        _, err := resource.ParseQuantity(profile.Spec.ResourceDefaults.CPU)
        if err != nil {
            return fmt.Errorf("invalid CPU resource: %v", err)
        }
    }
    
    // Validate memory resource
    if profile.Spec.ResourceDefaults.Memory != "" {
        _, err := resource.ParseQuantity(profile.Spec.ResourceDefaults.Memory)
        if err != nil {
            return fmt.Errorf("invalid memory resource: %v", err)
        }
    }
    
    // Validate ephemeral storage
    if profile.Spec.ResourceDefaults.EphemeralStorage != "" {
        _, err := resource.ParseQuantity(profile.Spec.ResourceDefaults.EphemeralStorage)
        if err != nil {
            return fmt.Errorf("invalid ephemeral storage resource: %v", err)
        }
    }
    
    // Check if resource defaults are within cluster limits
    if c.config.EnforceResourceLimits {
        if profile.Spec.ResourceDefaults.CPU != "" {
            cpu, _ := resource.ParseQuantity(profile.Spec.ResourceDefaults.CPU)
            maxCPU, _ := resource.ParseQuantity(c.config.MaxCPU)
            if cpu.Cmp(maxCPU) > 0 {
                return fmt.Errorf("CPU resource exceeds maximum allowed: %s > %s", 
                    profile.Spec.ResourceDefaults.CPU, c.config.MaxCPU)
            }
        }
        
        if profile.Spec.ResourceDefaults.Memory != "" {
            memory, _ := resource.ParseQuantity(profile.Spec.ResourceDefaults.Memory)
            maxMemory, _ := resource.ParseQuantity(c.config.MaxMemory)
            if memory.Cmp(maxMemory) > 0 {
                return fmt.Errorf("memory resource exceeds maximum allowed: %s > %s", 
                    profile.Spec.ResourceDefaults.Memory, c.config.MaxMemory)
            }
        }
    }
    
    return nil
}

// validateProfilePackages validates the pre-installed packages in the profile
func (c *Controller) validateProfilePackages(profile *llmsafespacev1.SandboxProfile) error {
    if profile.Spec.PreInstalledPackages == nil || len(profile.Spec.PreInstalledPackages) == 0 {
        return nil
    }
    
    // Check if packages are in the allowlist
    if c.config.EnforcePackageAllowlist {
        language := profile.Spec.Language
        allowlist, ok := c.config.PackageAllowlists[language]
        if !ok {
            return fmt.Errorf("no package allowlist defined for language: %s", language)
        }
        
        for _, pkg := range profile.Spec.PreInstalledPackages {
            if !isPackageInAllowlist(pkg, allowlist) {
                return fmt.Errorf("package %s is not in the allowlist for %s", pkg, language)
            }
        }
    }
    
    // Check for package compatibility issues
    if err := c.checkPackageCompatibility(profile.Spec.Language, profile.Spec.PreInstalledPackages); err != nil {
        return fmt.Errorf("package compatibility issue: %v", err)
    }
    
    return nil
}

// isPackageInAllowlist checks if a package is in the allowlist
func isPackageInAllowlist(pkg string, allowlist []string) bool {
    // Extract base package name (without version)
    basePkg := pkg
    if idx := strings.Index(pkg, "=="); idx > 0 {
        basePkg = pkg[:idx]
    } else if idx := strings.Index(pkg, "@"); idx > 0 {
        basePkg = pkg[:idx]
    }
    
    for _, allowed := range allowlist {
        if basePkg == allowed {
            return true
        }
    }
    
    return false
}

// checkPackageCompatibility checks for known compatibility issues between packages
func (c *Controller) checkPackageCompatibility(language string, packages []string) error {
    // This would check for known compatibility issues between packages
    // For example, conflicting versions or packages that don't work together
    
    // For Python, check for tensorflow and pytorch in the same environment
    if language == "python" {
        hasTensorflow := false
        hasPytorch := false
        
        for _, pkg := range packages {
            if strings.HasPrefix(pkg, "tensorflow") {
                hasTensorflow = true
            }
            if strings.HasPrefix(pkg, "torch") {
                hasPytorch = true
            }
        }
        
        if hasTensorflow && hasPytorch && c.config.WarnOnTfPytorchConflict {
            klog.Warningf("Profile includes both TensorFlow and PyTorch, which may cause memory issues")
        }
    }
    
    return nil
}

// handleSandboxProfileDeletion handles the deletion of a SandboxProfile
func (c *Controller) handleSandboxProfileDeletion(profile *llmsafespacev1.SandboxProfile) error {
    // Check if this profile is being used by any sandboxes
    sandboxes, err := c.sandboxLister.List(labels.Everything())
    if err != nil {
        return fmt.Errorf("failed to list sandboxes: %v", err)
    }
    
    for _, sandbox := range sandboxes {
        if sandbox.Spec.ProfileRef != nil && 
           sandbox.Spec.ProfileRef.Name == profile.Name &&
           (sandbox.Spec.ProfileRef.Namespace == profile.Namespace || sandbox.Spec.ProfileRef.Namespace == "") {
            return fmt.Errorf("profile is still in use by sandbox %s/%s", sandbox.Namespace, sandbox.Name)
        }
    }
    
    // Check if this profile is being used by any warm pools
    warmPools, err := c.warmPoolLister.List(labels.Everything())
    if err != nil {
        return fmt.Errorf("failed to list warm pools: %v", err)
    }
    
    for _, pool := range warmPools {
        if pool.Spec.ProfileRef != nil && 
           pool.Spec.ProfileRef.Name == profile.Name &&
           (pool.Spec.ProfileRef.Namespace == profile.Namespace || pool.Spec.ProfileRef.Namespace == "") {
            return fmt.Errorf("profile is still in use by warm pool %s/%s", pool.Namespace, pool.Name)
        }
    }
    
    // Remove finalizer
    profile.Finalizers = removeString(profile.Finalizers, sandboxProfileFinalizer)
    _, err = c.llmsafespaceClient.LlmsafespaceV1().SandboxProfiles(profile.Namespace).Update(context.TODO(), profile, metav1.UpdateOptions{})
    if err != nil {
        return fmt.Errorf("failed to remove finalizer: %v", err)
    }
    
    klog.V(2).Infof("Successfully deleted SandboxProfile %s/%s", profile.Namespace, profile.Name)
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

// validateRuntimeImage validates the runtime image
func (c *Controller) validateRuntimeImage(runtime *llmsafespacev1.RuntimeEnvironment) error {
    // Check if the image exists in the registry
    imageExists, err := c.imageRegistry.ImageExists(runtime.Spec.Image)
    if err != nil {
        return fmt.Errorf("failed to check if image exists: %v", err)
    }
    
    if !imageExists {
        return fmt.Errorf("image %s does not exist in registry", runtime.Spec.Image)
    }
    
    // Check image digest to ensure it matches expected value if specified
    if runtime.Spec.ImageDigest != "" {
        digest, err := c.imageRegistry.GetImageDigest(runtime.Spec.Image)
        if err != nil {
            return fmt.Errorf("failed to get image digest: %v", err)
        }
        
        if digest != runtime.Spec.ImageDigest {
            return fmt.Errorf("image digest mismatch: expected %s, got %s", runtime.Spec.ImageDigest, digest)
        }
    }
    
    // Check for image vulnerabilities if scanner is configured
    if c.config.EnableVulnerabilityScanning {
        vulnerabilities, err := c.imageScanner.ScanImage(runtime.Spec.Image)
        if err != nil {
            return fmt.Errorf("failed to scan image for vulnerabilities: %v", err)
        }
        
        // Check if there are critical vulnerabilities
        if vulnerabilities.HasCritical() {
            return fmt.Errorf("image has critical vulnerabilities: %v", vulnerabilities.CriticalCount)
        }
        
        // Update runtime with vulnerability information
        runtime.Status.VulnerabilityStatus = &llmsafespacev1.VulnerabilityStatus{
            LastScanTime: metav1.Now(),
            CriticalCount: vulnerabilities.CriticalCount,
            HighCount: vulnerabilities.HighCount,
            MediumCount: vulnerabilities.MediumCount,
            LowCount: vulnerabilities.LowCount,
        }
    }
    
    return nil
}

// validateRuntimeCompatibility checks if the runtime is compatible with the current system
func (c *Controller) validateRuntimeCompatibility(runtime *llmsafespacev1.RuntimeEnvironment) error {
    // Check if the runtime requires features that are available in the cluster
    for _, feature := range runtime.Spec.SecurityFeatures {
        if feature == "gvisor" && !c.isGvisorAvailable() {
            return fmt.Errorf("gvisor runtime is required but not available in the cluster")
        }
        
        if feature == "seccomp" && !c.isSeccompAvailable() {
            return fmt.Errorf("seccomp is required but not available in the cluster")
        }
        
        if feature == "apparmor" && !c.isApparmorAvailable() {
            return fmt.Errorf("apparmor is required but not available in the cluster")
        }
    }
    
    // Check resource requirements against cluster capacity
    if runtime.Spec.ResourceRequirements != nil {
        // Get cluster capacity
        nodes, err := c.kubeClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
        if err != nil {
            return fmt.Errorf("failed to list nodes: %v", err)
        }
        
        // Check if there's at least one node that can accommodate the minimum resource requirements
        minCpuReq, err := resource.ParseQuantity(runtime.Spec.ResourceRequirements.MinCpu)
        if err != nil {
            return fmt.Errorf("invalid CPU requirement: %v", err)
        }
        
        minMemReq, err := resource.ParseQuantity(runtime.Spec.ResourceRequirements.MinMemory)
        if err != nil {
            return fmt.Errorf("invalid memory requirement: %v", err)
        }
        
        canSchedule := false
        for _, node := range nodes.Items {
            allocatableCpu := node.Status.Allocatable.Cpu()
            allocatableMem := node.Status.Allocatable.Memory()
            
            if allocatableCpu.Cmp(minCpuReq) >= 0 && allocatableMem.Cmp(minMemReq) >= 0 {
                canSchedule = true
                break
            }
        }
        
        if !canSchedule {
            return fmt.Errorf("no nodes available that meet minimum resource requirements: CPU %s, Memory %s", 
                runtime.Spec.ResourceRequirements.MinCpu, runtime.Spec.ResourceRequirements.MinMemory)
        }
    }
    
    // Check for specific node features if required
    if runtime.Spec.RequiredNodeFeatures != nil && len(runtime.Spec.RequiredNodeFeatures) > 0 {
        nodes, err := c.kubeClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
        if err != nil {
            return fmt.Errorf("failed to list nodes: %v", err)
        }
        
        featureAvailable := make(map[string]bool)
        for _, feature := range runtime.Spec.RequiredNodeFeatures {
            featureAvailable[feature] = false
        }
        
        for _, node := range nodes.Items {
            for feature := range featureAvailable {
                if _, ok := node.Labels[feature]; ok {
                    featureAvailable[feature] = true
                }
            }
        }
        
        for feature, available := range featureAvailable {
            if !available {
                return fmt.Errorf("required node feature %s is not available in the cluster", feature)
            }
        }
    }
    
    return nil
}

// validateRuntimeSecurity validates the security aspects of the runtime
func (c *Controller) validateRuntimeSecurity(runtime *llmsafespacev1.RuntimeEnvironment) error {
    // Check if the runtime image has known vulnerabilities
    if c.config.EnableVulnerabilityScanning {
        vulnerabilities, err := c.imageScanner.ScanImage(runtime.Spec.Image)
        if err != nil {
            return fmt.Errorf("failed to scan image for vulnerabilities: %v", err)
        }
        
        if c.config.BlockCriticalVulnerabilities && vulnerabilities.HasCritical() {
            return fmt.Errorf("image has %d critical vulnerabilities", vulnerabilities.CriticalCount)
        }
    }
    
    // Verify that the runtime supports required security features
    if runtime.Spec.SecurityLevel == "high" {
        // For high security, verify specific security features
        requiredFeatures := []string{"seccomp", "apparmor", "no-new-privs"}
        for _, feature := range requiredFeatures {
            found := false
            for _, supportedFeature := range runtime.Spec.SecurityFeatures {
                if supportedFeature == feature {
                    found = true
                    break
                }
            }
            
            if !found {
                return fmt.Errorf("high security level requires %s feature, but it's not supported by this runtime", feature)
            }
        }
    }
    
    // Verify seccomp profile if specified
    if runtime.Spec.SeccompProfile != "" {
        if !c.seccompProfileExists(runtime.Spec.SeccompProfile) {
            return fmt.Errorf("specified seccomp profile %s does not exist", runtime.Spec.SeccompProfile)
        }
    }
    
    // Verify AppArmor profile if specified
    if runtime.Spec.AppArmorProfile != "" {
        if !c.apparmorProfileExists(runtime.Spec.AppArmorProfile) {
            return fmt.Errorf("specified AppArmor profile %s does not exist", runtime.Spec.AppArmorProfile)
        }
    }
    
    // Check if runtime runs as non-root by default
    if !runtime.Spec.RunAsNonRoot {
        klog.Warningf("Runtime %s does not run as non-root by default, this is a security risk", runtime.Name)
        if c.config.RequireNonRootRuntimes {
            return fmt.Errorf("runtime must run as non-root")
        }
    }
    
    return nil
}

// handleRuntimeEnvironmentDeletion handles the deletion of a RuntimeEnvironment
func (c *Controller) handleRuntimeEnvironmentDeletion(runtime *llmsafespacev1.RuntimeEnvironment) error {
    // Check if this runtime is being used by any sandboxes
    sandboxes, err := c.sandboxLister.List(labels.Everything())
    if err != nil {
        return fmt.Errorf("failed to list sandboxes: %v", err)
    }
    
    for _, sandbox := range sandboxes {
        if sandbox.Spec.Runtime == runtime.Spec.Image {
            return fmt.Errorf("runtime is still in use by sandbox %s/%s", sandbox.Namespace, sandbox.Name)
        }
    }
    
    // Check if this runtime is being used by any warm pools
    warmPools, err := c.warmPoolLister.List(labels.Everything())
    if err != nil {
        return fmt.Errorf("failed to list warm pools: %v", err)
    }
    
    for _, pool := range warmPools {
        if pool.Spec.Runtime == runtime.Spec.Image {
            return fmt.Errorf("runtime is still in use by warm pool %s/%s", pool.Namespace, pool.Name)
        }
    }
    
    // Remove finalizer
    runtime.Finalizers = removeString(runtime.Finalizers, runtimeEnvironmentFinalizer)
    _, err = c.llmsafespaceClient.LlmsafespaceV1().RuntimeEnvironments().Update(context.TODO(), runtime, metav1.UpdateOptions{})
    if err != nil {
        return fmt.Errorf("failed to remove finalizer: %v", err)
    }
    
    klog.V(2).Infof("Successfully deleted RuntimeEnvironment %s", runtime.Name)
    return nil
}

// isGvisorAvailable checks if gVisor is available in the cluster
func (c *Controller) isGvisorAvailable() bool {
    runtimeClass, err := c.kubeClient.NodeV1().RuntimeClasses().Get(context.TODO(), "gvisor", metav1.GetOptions{})
    return err == nil && runtimeClass != nil
}

// isSeccompAvailable checks if seccomp is available in the cluster
func (c *Controller) isSeccompAvailable() bool {
    // Check if seccomp is enabled in the kubelet configuration
    // This is a simplified check - in a real implementation, this would involve
    // checking kubelet configuration or node properties
    return c.config.SeccompEnabled
}

// isApparmorAvailable checks if AppArmor is available in the cluster
func (c *Controller) isApparmorAvailable() bool {
    // Check if AppArmor is enabled in the kubelet configuration
    // This is a simplified check - in a real implementation, this would involve
    // checking kubelet configuration or node properties
    return c.config.AppArmorEnabled
}

// seccompProfileExists checks if a seccomp profile exists
func (c *Controller) seccompProfileExists(profilePath string) bool {
    // This would check if the seccomp profile exists in the system
    // For now, we'll just return true for common profiles
    return strings.HasPrefix(profilePath, "profiles/") || 
           profilePath == "runtime/default" || 
           profilePath == "localhost/default"
}

// apparmorProfileExists checks if an AppArmor profile exists
func (c *Controller) apparmorProfileExists(profileName string) bool {
    // This would check if the AppArmor profile exists in the system
    // For now, we'll just return true for common profiles
    return strings.HasPrefix(profileName, "runtime/") || 
           profileName == "runtime/default" || 
           profileName == "localhost/default"
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

The Sandbox Controller uses shared components and utilities across all reconciliation loops to avoid code duplication and ensure consistent behavior.

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
    
    // VolumeManager handles the creation, update, and deletion of volumes for sandboxes
    type VolumeManager struct {
        kubeClient kubernetes.Interface
        config     *config.Config
        recorder   record.EventRecorder
    }

    // NewVolumeManager creates a new VolumeManager
    func NewVolumeManager(kubeClient kubernetes.Interface, config *config.Config, recorder record.EventRecorder) *VolumeManager {
        return &VolumeManager{
            kubeClient: kubeClient,
            config:     config,
            recorder:   recorder,
        }
    }

    // EnsurePersistentVolumeClaim ensures that a PVC exists for a sandbox
    func (v *VolumeManager) EnsurePersistentVolumeClaim(sandbox *llmsafespacev1.Sandbox) error {
        startTime := time.Now()
        defer func() {
            volumeOperationDurationSeconds.WithLabelValues("ensure_pvc").Observe(time.Since(startTime).Seconds())
        }()
    
        if sandbox.Spec.Storage == nil || !sandbox.Spec.Storage.Persistent {
            return nil
        }
    
        // Determine namespace
        namespace := sandbox.Namespace
        if v.config.NamespaceIsolation {
            namespace = fmt.Sprintf("sandbox-%s", sandbox.UID)
        }
    
        // Determine volume size
        volumeSize := "5Gi" // Default size
        if sandbox.Spec.Storage.VolumeSize != "" {
            volumeSize = sandbox.Spec.Storage.VolumeSize
        }
    
        // Determine storage class
        storageClass := v.config.DefaultStorageClass
        if sandbox.Spec.Storage.StorageClass != "" {
            storageClass = sandbox.Spec.Storage.StorageClass
        }
    
        // Create PVC
        pvc := &corev1.PersistentVolumeClaim{
            ObjectMeta: metav1.ObjectMeta{
                Name: fmt.Sprintf("sandbox-%s-data", sandbox.Name),
                Namespace: namespace,
                Labels: map[string]string{
                    "app": "llmsafespace",
                    "component": "sandbox-storage",
                    "sandbox-id": sandbox.Name,
                    "sandbox-uid": string(sandbox.UID),
                },
                Annotations: map[string]string{
                    "llmsafespace.dev/sandbox-id": sandbox.Name,
                    "llmsafespace.dev/sandbox-uid": string(sandbox.UID),
                },
                OwnerReferences: []metav1.OwnerReference{
                    *metav1.NewControllerRef(sandbox, llmsafespacev1.SchemeGroupVersion.WithKind("Sandbox")),
                },
            },
            Spec: corev1.PersistentVolumeClaimSpec{
                AccessModes: []corev1.PersistentVolumeAccessMode{
                    corev1.ReadWriteOnce,
                },
                Resources: corev1.ResourceRequirements{
                    Requests: corev1.ResourceList{
                        corev1.ResourceStorage: resource.MustParse(volumeSize),
                    },
                },
            },
        }
    
        // Set storage class if specified
        if storageClass != "" {
            pvc.Spec.StorageClassName = &storageClass
        }
    
        // Try to get existing PVC
        existing, err := v.kubeClient.CoreV1().PersistentVolumeClaims(namespace).Get(
            context.TODO(), pvc.Name, metav1.GetOptions{})
    
        if err != nil {
            if errors.IsNotFound(err) {
                // Create new PVC
                _, err = v.kubeClient.CoreV1().PersistentVolumeClaims(namespace).Create(
                    context.TODO(), pvc, metav1.CreateOptions{})
                if err != nil {
                    volumeOperationsTotal.WithLabelValues("create", "failed").Inc()
                    return fmt.Errorf("failed to create PVC: %v", err)
                }
                volumeOperationsTotal.WithLabelValues("create", "success").Inc()
            
                // Record event
                v.recorder.Event(sandbox, corev1.EventTypeNormal, "PVCCreated", 
                    fmt.Sprintf("Created persistent volume claim %s", pvc.Name))
            
                return nil
            }
            return fmt.Errorf("failed to get PVC: %v", err)
        }
    
        // PVC already exists, check if it needs to be updated
        needsUpdate := false
    
        // Check if storage size needs to be updated
        requestedSize := resource.MustParse(volumeSize)
        currentSize := existing.Spec.Resources.Requests[corev1.ResourceStorage]
        if requestedSize.Cmp(currentSize) > 0 {
            // Requested size is larger than current size
            existing.Spec.Resources.Requests[corev1.ResourceStorage] = requestedSize
            needsUpdate = true
        }
    
        // Check if storage class needs to be updated
        if storageClass != "" && (existing.Spec.StorageClassName == nil || *existing.Spec.StorageClassName != storageClass) {
            // Cannot update storage class of an existing PVC
            v.recorder.Event(sandbox, corev1.EventTypeWarning, "PVCStorageClassMismatch", 
                fmt.Sprintf("Cannot update storage class of existing PVC %s", pvc.Name))
        }
    
        // Update PVC if needed
        if needsUpdate {
            _, err = v.kubeClient.CoreV1().PersistentVolumeClaims(namespace).Update(
                context.TODO(), existing, metav1.UpdateOptions{})
            if err != nil {
                volumeOperationsTotal.WithLabelValues("update", "failed").Inc()
                return fmt.Errorf("failed to update PVC: %v", err)
            }
            volumeOperationsTotal.WithLabelValues("update", "success").Inc()
        
            // Record event
            v.recorder.Event(sandbox, corev1.EventTypeNormal, "PVCUpdated", 
                fmt.Sprintf("Updated persistent volume claim %s", pvc.Name))
        }
    
        return nil
    }

    // GetVolumeMounts returns the volume mounts for a sandbox
    func (v *VolumeManager) GetVolumeMounts(sandbox *llmsafespacev1.Sandbox) ([]corev1.Volume, []corev1.VolumeMount) {
        volumes := []corev1.Volume{
            {
                Name: "workspace",
                VolumeSource: corev1.VolumeSource{
                    EmptyDir: &corev1.EmptyDirVolumeSource{},
                },
            },
            {
                Name: "tmp",
                VolumeSource: corev1.VolumeSource{
                    EmptyDir: &corev1.EmptyDirVolumeSource{},
                },
            },
        }
    
        volumeMounts := []corev1.VolumeMount{
            {
                Name:      "workspace",
                MountPath: "/workspace",
            },
            {
                Name:      "tmp",
                MountPath: "/tmp",
            },
        }
    
        // Add persistent storage if enabled
        if sandbox.Spec.Storage != nil && sandbox.Spec.Storage.Persistent {
            volumes = append(volumes, corev1.Volume{
                Name: "persistent-data",
                VolumeSource: corev1.VolumeSource{
                    PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
                        ClaimName: fmt.Sprintf("sandbox-%s-data", sandbox.Name),
                    },
                },
            })
        
            volumeMounts = append(volumeMounts, corev1.VolumeMount{
                Name:      "persistent-data",
                MountPath: "/data",
            })
        }
    
        // Add additional writable paths if specified
        if sandbox.Spec.Filesystem != nil && len(sandbox.Spec.Filesystem.WritablePaths) > 0 {
            for i, path := range sandbox.Spec.Filesystem.WritablePaths {
                // Skip default writable paths
                if path == "/tmp" || path == "/workspace" || path == "/data" {
                    continue
                }
            
                // Create a volume for this path
                volumeName := fmt.Sprintf("writable-%d", i)
                volumes = append(volumes, corev1.Volume{
                    Name: volumeName,
                    VolumeSource: corev1.VolumeSource{
                        EmptyDir: &corev1.EmptyDirVolumeSource{},
                    },
                })
            
                volumeMounts = append(volumeMounts, corev1.VolumeMount{
                    Name:      volumeName,
                    MountPath: path,
                })
            }
        }
    
        // Add shared volumes if specified
        if sandbox.Spec.Storage != nil && len(sandbox.Spec.Storage.SharedVolumes) > 0 {
            for i, sharedVolume := range sandbox.Spec.Storage.SharedVolumes {
                volumeName := fmt.Sprintf("shared-%d", i)
                volumes = append(volumes, corev1.Volume{
                    Name: volumeName,
                    VolumeSource: corev1.VolumeSource{
                        PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
                            ClaimName: sharedVolume.ClaimName,
                            ReadOnly:  sharedVolume.ReadOnly,
                        },
                    },
                })
            
                volumeMounts = append(volumeMounts, corev1.VolumeMount{
                    Name:      volumeName,
                    MountPath: sharedVolume.MountPath,
                    ReadOnly:  sharedVolume.ReadOnly,
                })
            }
        }
    
        return volumes, volumeMounts
    }

    // DeletePersistentVolumeClaim deletes the PVC for a sandbox
    func (v *VolumeManager) DeletePersistentVolumeClaim(sandbox *llmsafespacev1.Sandbox) error {
        startTime := time.Now()
        defer func() {
            volumeOperationDurationSeconds.WithLabelValues("delete_pvc").Observe(time.Since(startTime).Seconds())
        }()
    
        if sandbox.Spec.Storage == nil || !sandbox.Spec.Storage.Persistent {
            return nil
        }
    
        // Determine namespace
        namespace := sandbox.Namespace
        if v.config.NamespaceIsolation {
            namespace = fmt.Sprintf("sandbox-%s", sandbox.UID)
        }
    
        // If pod namespace is specified in status, use that
        if sandbox.Status.PodNamespace != "" {
            namespace = sandbox.Status.PodNamespace
        }
    
        // Delete PVC
        pvcName := fmt.Sprintf("sandbox-%s-data", sandbox.Name)
        err := v.kubeClient.CoreV1().PersistentVolumeClaims(namespace).Delete(
            context.TODO(), pvcName, metav1.DeleteOptions{})
    
        if err != nil {
            if errors.IsNotFound(err) {
                return nil
            }
            volumeOperationsTotal.WithLabelValues("delete", "failed").Inc()
            return fmt.Errorf("failed to delete PVC: %v", err)
        }
    
        volumeOperationsTotal.WithLabelValues("delete", "success").Inc()
    
        // Record event
        v.recorder.Event(sandbox, corev1.EventTypeNormal, "PVCDeleted", 
            fmt.Sprintf("Deleted persistent volume claim %s", pvcName))
    
        return nil
    }

    // MonitorVolumeUsage monitors the usage of persistent volumes
    func (v *VolumeManager) MonitorVolumeUsage(sandbox *llmsafespacev1.Sandbox) {
        if sandbox.Spec.Storage == nil || !sandbox.Spec.Storage.Persistent {
            return
        }
    
        // Determine namespace
        namespace := sandbox.Namespace
        if v.config.NamespaceIsolation {
            namespace = fmt.Sprintf("sandbox-%s", sandbox.UID)
        }
    
        // If pod namespace is specified in status, use that
        if sandbox.Status.PodNamespace != "" {
            namespace = sandbox.Status.PodNamespace
        }
    
        // Start a goroutine to monitor volume usage
        go func() {
            ticker := time.NewTicker(1 * time.Minute)
            defer ticker.Stop()
        
            for {
                select {
                case <-ticker.C:
                    // Check if sandbox still exists
                    _, err := v.kubeClient.CoreV1().Pods(namespace).Get(
                        context.TODO(), sandbox.Status.PodName, metav1.GetOptions{})
                    if err != nil {
                        if errors.IsNotFound(err) {
                            // Pod no longer exists, stop monitoring
                            return
                        }
                        klog.Warningf("Failed to get pod for volume usage monitoring: %v", err)
                        continue
                    }
                
                    // Get volume usage
                    usage, err := v.getVolumeUsage(sandbox)
                    if err != nil {
                        klog.Warningf("Failed to get volume usage: %v", err)
                        continue
                    }
                
                    // Update metrics
                    persistentVolumeUsageGauge.WithLabelValues(sandbox.Name, namespace).Set(float64(usage))
                
                    // Check if usage is approaching limit
                    volumeSize := "5Gi" // Default size
                    if sandbox.Spec.Storage.VolumeSize != "" {
                        volumeSize = sandbox.Spec.Storage.VolumeSize
                    }
                
                    sizeQuantity, err := resource.ParseQuantity(volumeSize)
                    if err != nil {
                        klog.Warningf("Failed to parse volume size: %v", err)
                        continue
                    }
                
                    sizeBytes := sizeQuantity.Value()
                    usagePercent := float64(usage) / float64(sizeBytes) * 100
                
                    // Update resource utilization metric
                    resourceLimitUtilizationGauge.WithLabelValues("storage", sandbox.Name, namespace).Set(usagePercent)
                
                    // Warn if usage is above threshold
                    if usagePercent > 80 {
                        v.recorder.Event(sandbox, corev1.EventTypeWarning, "HighVolumeUsage", 
                            fmt.Sprintf("Persistent volume usage is high: %.1f%%", usagePercent))
                    }
                }
            }
        }()
    }

    // getVolumeUsage gets the usage of a persistent volume in bytes
    func (v *VolumeManager) getVolumeUsage(sandbox *llmsafespacev1.Sandbox) (int64, error) {
        // In a real implementation, this would execute a command in the pod to get disk usage
        // For now, we'll return a simulated value
    
        // This is a placeholder implementation
        // In a real system, you would exec into the pod and run a command like:
        // du -sb /data | cut -f1
    
        // Simulate increasing usage over time
        elapsedSeconds := time.Since(sandbox.Status.StartTime.Time).Seconds()
        usageBytes := int64(1024*1024*10 + int(elapsedSeconds*1000)) // Start with 10MB and grow 1KB per second
    
        return usageBytes, nil
    }

    // volumeOperationDurationSeconds tracks the duration of volume operations
    var volumeOperationDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_volume_operation_duration_seconds",
            Help: "Duration of volume operations in seconds",
            Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5},
        },
        []string{"operation"},
    )
    
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

// ensureDefaultDenyPolicy ensures that the default deny policy exists
func (n *NetworkPolicyManager) ensureDefaultDenyPolicy(sandbox *llmsafespacev1.Sandbox, namespace string) error {
    defaultDenyPolicy := &networkingv1.NetworkPolicy{
        ObjectMeta: metav1.ObjectMeta{
            Name: fmt.Sprintf("sandbox-%s-default-deny", sandbox.Name),
            Namespace: namespace,
            Labels: map[string]string{
                "app": "llmsafespace",
                "component": "network-policy",
                "sandbox-id": sandbox.Name,
                "sandbox-uid": string(sandbox.UID),
                "policy-type": "default-deny",
            },
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
    
    // Try to get existing policy
    existing, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Get(
        context.TODO(), defaultDenyPolicy.Name, metav1.GetOptions{})
    
    if err != nil {
        if errors.IsNotFound(err) {
            // Create new policy
            _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Create(
                context.TODO(), defaultDenyPolicy, metav1.CreateOptions{})
            if err != nil {
                return fmt.Errorf("failed to create default deny policy: %v", err)
            }
            return nil
        }
        return fmt.Errorf("failed to get default deny policy: %v", err)
    }
    
    // Update existing policy if needed
    if !reflect.DeepEqual(existing.Spec, defaultDenyPolicy.Spec) || 
       !reflect.DeepEqual(existing.Labels, defaultDenyPolicy.Labels) {
        
        existing.Spec = defaultDenyPolicy.Spec
        existing.Labels = defaultDenyPolicy.Labels
        
        _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Update(
            context.TODO(), existing, metav1.UpdateOptions{})
        if err != nil {
            return fmt.Errorf("failed to update default deny policy: %v", err)
        }
    }
    
    return nil
}

// ensureAPIServicePolicy ensures that the API service access policy exists
func (n *NetworkPolicyManager) ensureAPIServicePolicy(sandbox *llmsafespacev1.Sandbox, namespace string) error {
    apiServicePolicy := &networkingv1.NetworkPolicy{
        ObjectMeta: metav1.ObjectMeta{
            Name: fmt.Sprintf("sandbox-%s-api-access", sandbox.Name),
            Namespace: namespace,
            Labels: map[string]string{
                "app": "llmsafespace",
                "component": "network-policy",
                "sandbox-id": sandbox.Name,
                "sandbox-uid": string(sandbox.UID),
                "policy-type": "api-access",
            },
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
    
    // Try to get existing policy
    existing, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Get(
        context.TODO(), apiServicePolicy.Name, metav1.GetOptions{})
    
    if err != nil {
        if errors.IsNotFound(err) {
            // Create new policy
            _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Create(
                context.TODO(), apiServicePolicy, metav1.CreateOptions{})
            if err != nil {
                return fmt.Errorf("failed to create API service policy: %v", err)
            }
            return nil
        }
        return fmt.Errorf("failed to get API service policy: %v", err)
    }
    
    // Update existing policy if needed
    if !reflect.DeepEqual(existing.Spec, apiServicePolicy.Spec) || 
       !reflect.DeepEqual(existing.Labels, apiServicePolicy.Labels) {
        
        existing.Spec = apiServicePolicy.Spec
        existing.Labels = apiServicePolicy.Labels
        
        _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Update(
            context.TODO(), existing, metav1.UpdateOptions{})
        if err != nil {
            return fmt.Errorf("failed to update API service policy: %v", err)
        }
    }
    
    return nil
}

// ensureDNSAccessPolicy ensures that the DNS access policy exists
func (n *NetworkPolicyManager) ensureDNSAccessPolicy(sandbox *llmsafespacev1.Sandbox, namespace string) error {
    dnsPolicy := &networkingv1.NetworkPolicy{
        ObjectMeta: metav1.ObjectMeta{
            Name: fmt.Sprintf("sandbox-%s-dns-access", sandbox.Name),
            Namespace: namespace,
            Labels: map[string]string{
                "app": "llmsafespace",
                "component": "network-policy",
                "sandbox-id": sandbox.Name,
                "sandbox-uid": string(sandbox.UID),
                "policy-type": "dns-access",
            },
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
                            NamespaceSelector: &metav1.LabelSelector{
                                MatchLabels: map[string]string{
                                    "kubernetes.io/metadata.name": "kube-system",
                                },
                            },
                            PodSelector: &metav1.LabelSelector{
                                MatchLabels: map[string]string{
                                    "k8s-app": "kube-dns",
                                },
                            },
                        },
                    },
                    Ports: []networkingv1.NetworkPolicyPort{
                        {
                            Port: &intstr.IntOrString{
                                Type: intstr.Int,
                                IntVal: 53,
                            },
                            Protocol: &[]corev1.Protocol{corev1.ProtocolUDP}[0],
                        },
                        {
                            Port: &intstr.IntOrString{
                                Type: intstr.Int,
                                IntVal: 53,
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
    
    // Try to get existing policy
    existing, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Get(
        context.TODO(), dnsPolicy.Name, metav1.GetOptions{})
    
    if err != nil {
        if errors.IsNotFound(err) {
            // Create new policy
            _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Create(
                context.TODO(), dnsPolicy, metav1.CreateOptions{})
            if err != nil {
                return fmt.Errorf("failed to create DNS access policy: %v", err)
            }
            return nil
        }
        return fmt.Errorf("failed to get DNS access policy: %v", err)
    }
    
    // Update existing policy if needed
    if !reflect.DeepEqual(existing.Spec, dnsPolicy.Spec) || 
       !reflect.DeepEqual(existing.Labels, dnsPolicy.Labels) {
        
        existing.Spec = dnsPolicy.Spec
        existing.Labels = dnsPolicy.Labels
        
        _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Update(
            context.TODO(), existing, metav1.UpdateOptions{})
        if err != nil {
            return fmt.Errorf("failed to update DNS access policy: %v", err)
        }
    }
    
    return nil
}

// ensureEgressPolicies ensures that egress policies exist for the specified domains
func (n *NetworkPolicyManager) ensureEgressPolicies(sandbox *llmsafespacev1.Sandbox, namespace string) error {
    if sandbox.Spec.NetworkAccess == nil || len(sandbox.Spec.NetworkAccess.Egress) == 0 {
        return nil
    }
    
    // Create a policy for each domain group (up to 10 domains per policy to avoid too large policies)
    domainGroups := make([][]llmsafespacev1.DomainRule, 0)
    currentGroup := make([]llmsafespacev1.DomainRule, 0)
    
    for _, rule := range sandbox.Spec.NetworkAccess.Egress {
        currentGroup = append(currentGroup, rule)
        
        if len(currentGroup) >= 10 {
            domainGroups = append(domainGroups, currentGroup)
            currentGroup = make([]llmsafespacev1.DomainRule, 0)
        }
    }
    
    if len(currentGroup) > 0 {
        domainGroups = append(domainGroups, currentGroup)
    }
    
    // Create or update policies for each domain group
    for i, group := range domainGroups {
        policyName := fmt.Sprintf("sandbox-%s-egress-%d", sandbox.Name, i)
        
        // Create egress policy for this group
        egressPolicy := &networkingv1.NetworkPolicy{
            ObjectMeta: metav1.ObjectMeta{
                Name: policyName,
                Namespace: namespace,
                Labels: map[string]string{
                    "app": "llmsafespace",
                    "component": "network-policy",
                    "sandbox-id": sandbox.Name,
                    "sandbox-uid": string(sandbox.UID),
                    "policy-type": "egress",
                    "policy-group": fmt.Sprintf("%d", i),
                },
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
                        Ports: n.getEgressPorts(group),
                    },
                },
                PolicyTypes: []networkingv1.PolicyType{
                    networkingv1.PolicyTypeEgress,
                },
            },
        }
        
        // Try to get existing policy
        existing, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Get(
            context.TODO(), policyName, metav1.GetOptions{})
        
        if err != nil {
            if errors.IsNotFound(err) {
                // Create new policy
                _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Create(
                    context.TODO(), egressPolicy, metav1.CreateOptions{})
                if err != nil {
                    return fmt.Errorf("failed to create egress policy %s: %v", policyName, err)
                }
            } else {
                return fmt.Errorf("failed to get egress policy %s: %v", policyName, err)
            }
        } else {
            // Update existing policy if needed
            if !reflect.DeepEqual(existing.Spec, egressPolicy.Spec) || 
               !reflect.DeepEqual(existing.Labels, egressPolicy.Labels) {
                
                existing.Spec = egressPolicy.Spec
                existing.Labels = egressPolicy.Labels
                
                _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Update(
                    context.TODO(), existing, metav1.UpdateOptions{})
                if err != nil {
                    return fmt.Errorf("failed to update egress policy %s: %v", policyName, err)
                }
            }
        }
        
        // Add annotations with domain information for auditing
        domains := make([]string, 0)
        for _, rule := range group {
            domains = append(domains, rule.Domain)
        }
        
        // Update annotations with domain information
        if existing != nil {
            patchData := map[string]interface{}{
                "metadata": map[string]interface{}{
                    "annotations": map[string]string{
                        "llmsafespace.dev/allowed-domains": strings.Join(domains, ","),
                    },
                },
            }
            
            patchBytes, err := json.Marshal(patchData)
            if err != nil {
                return fmt.Errorf("failed to marshal patch data: %v", err)
            }
            
            _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Patch(
                context.TODO(), policyName, types.MergePatchType, patchBytes, metav1.PatchOptions{})
            if err != nil {
                return fmt.Errorf("failed to patch egress policy %s: %v", policyName, err)
            }
        }
    }
    
    // Clean up any old policies that are no longer needed
    selector := labels.SelectorFromSet(labels.Set{
        "app": "llmsafespace",
        "component": "network-policy",
        "sandbox-id": sandbox.Name,
        "policy-type": "egress",
    })
    
    policies, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).List(
        context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
    if err != nil {
        return fmt.Errorf("failed to list egress policies: %v", err)
    }
    
    for _, policy := range policies.Items {
        groupStr, ok := policy.Labels["policy-group"]
        if !ok {
            continue
        }
        
        group, err := strconv.Atoi(groupStr)
        if err != nil {
            continue
        }
        
        if group >= len(domainGroups) {
            // This policy is no longer needed
            err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Delete(
                context.TODO(), policy.Name, metav1.DeleteOptions{})
            if err != nil && !errors.IsNotFound(err) {
                return fmt.Errorf("failed to delete old egress policy %s: %v", policy.Name, err)
            }
        }
    }
    
    return nil
}

// getEgressPorts returns the list of ports for the egress policy
func (n *NetworkPolicyManager) getEgressPorts(rules []llmsafespacev1.DomainRule) []networkingv1.NetworkPolicyPort {
    // Check if any rules have specific ports
    hasSpecificPorts := false
    for _, rule := range rules {
        if len(rule.Ports) > 0 {
            hasSpecificPorts = true
            break
        }
    }
    
    // If no specific ports, use default HTTP/HTTPS ports
    if !hasSpecificPorts {
        return []networkingv1.NetworkPolicyPort{
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
        }
    }
    
    // Collect all unique ports from all rules
    portMap := make(map[string]networkingv1.NetworkPolicyPort)
    for _, rule := range rules {
        for _, port := range rule.Ports {
            protocol := corev1.ProtocolTCP
            if port.Protocol == "UDP" {
                protocol = corev1.ProtocolUDP
            }
            
            key := fmt.Sprintf("%d-%s", port.Port, protocol)
            portMap[key] = networkingv1.NetworkPolicyPort{
                Port: &intstr.IntOrString{
                    Type: intstr.Int,
                    IntVal: int32(port.Port),
                },
                Protocol: &protocol,
            }
        }
    }
    
    // Convert map to slice
    ports := make([]networkingv1.NetworkPolicyPort, 0, len(portMap))
    for _, port := range portMap {
        ports = append(ports, port)
    }
    
    return ports
}

// deleteEgressPolicies deletes all egress policies for a sandbox
func (n *NetworkPolicyManager) deleteEgressPolicies(sandbox *llmsafespacev1.Sandbox, namespace string) error {
    selector := labels.SelectorFromSet(labels.Set{
        "app": "llmsafespace",
        "component": "network-policy",
        "sandbox-id": sandbox.Name,
        "policy-type": "egress",
    })
    
    policies, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).List(
        context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
    if err != nil {
        return fmt.Errorf("failed to list egress policies: %v", err)
    }
    
    for _, policy := range policies.Items {
        err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Delete(
            context.TODO(), policy.Name, metav1.DeleteOptions{})
        if err != nil && !errors.IsNotFound(err) {
            return fmt.Errorf("failed to delete egress policy %s: %v", policy.Name, err)
        }
    }
    
    return nil
}

// ensureIngressPolicies ensures that ingress policies exist if ingress is enabled
func (n *NetworkPolicyManager) ensureIngressPolicies(sandbox *llmsafespacev1.Sandbox, namespace string) error {
    if sandbox.Spec.NetworkAccess == nil || !sandbox.Spec.NetworkAccess.Ingress {
        return nil
    }
    
    ingressPolicy := &networkingv1.NetworkPolicy{
        ObjectMeta: metav1.ObjectMeta{
            Name: fmt.Sprintf("sandbox-%s-ingress", sandbox.Name),
            Namespace: namespace,
            Labels: map[string]string{
                "app": "llmsafespace",
                "component": "network-policy",
                "sandbox-id": sandbox.Name,
                "sandbox-uid": string(sandbox.UID),
                "policy-type": "ingress",
            },
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
            PolicyTypes: []networkingv1.PolicyType{
                networkingv1.PolicyTypeIngress,
            },
        },
    }
    
    // Try to get existing policy
    existing, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Get(
        context.TODO(), ingressPolicy.Name, metav1.GetOptions{})
    
    if err != nil {
        if errors.IsNotFound(err) {
            // Create new policy
            _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Create(
                context.TODO(), ingressPolicy, metav1.CreateOptions{})
            if err != nil {
                return fmt.Errorf("failed to create ingress policy: %v", err)
            }
            return nil
        }
        return fmt.Errorf("failed to get ingress policy: %v", err)
    }
    
    // Update existing policy if needed
    if !reflect.DeepEqual(existing.Spec, ingressPolicy.Spec) || 
       !reflect.DeepEqual(existing.Labels, ingressPolicy.Labels) {
        
        existing.Spec = ingressPolicy.Spec
        existing.Labels = ingressPolicy.Labels
        
        _, err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Update(
            context.TODO(), existing, metav1.UpdateOptions{})
        if err != nil {
            return fmt.Errorf("failed to update ingress policy: %v", err)
        }
    }
    
    return nil
}

// deleteIngressPolicies deletes all ingress policies for a sandbox
func (n *NetworkPolicyManager) deleteIngressPolicies(sandbox *llmsafespacev1.Sandbox, namespace string) error {
    selector := labels.SelectorFromSet(labels.Set{
        "app": "llmsafespace",
        "component": "network-policy",
        "sandbox-id": sandbox.Name,
        "policy-type": "ingress",
    })
    
    policies, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).List(
        context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
    if err != nil {
        return fmt.Errorf("failed to list ingress policies: %v", err)
    }
    
    for _, policy := range policies.Items {
        err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Delete(
            context.TODO(), policy.Name, metav1.DeleteOptions{})
        if err != nil && !errors.IsNotFound(err) {
            return fmt.Errorf("failed to delete ingress policy %s: %v", policy.Name, err)
        }
    }
    
    return nil
}

// DeleteNetworkPolicies deletes all network policies for a sandbox
func (n *NetworkPolicyManager) DeleteNetworkPolicies(sandbox *llmsafespacev1.Sandbox) error {
    startTime := time.Now()
    defer func() {
        networkPolicyOperationDurationSeconds.WithLabelValues("delete").Observe(time.Since(startTime).Seconds())
    }()
    
    // Determine namespace
    namespace := sandbox.Namespace
    if n.config.NamespaceIsolation {
        namespace = fmt.Sprintf("sandbox-%s", sandbox.UID)
    }
    
    // If pod namespace is specified in status, use that
    if sandbox.Status.PodNamespace != "" {
        namespace = sandbox.Status.PodNamespace
    }
    
    // Delete all network policies for this sandbox
    selector := labels.SelectorFromSet(labels.Set{
        "app": "llmsafespace",
        "component": "network-policy",
        "sandbox-id": sandbox.Name,
    })
    
    policies, err := n.kubeClient.NetworkingV1().NetworkPolicies(namespace).List(
        context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
    if err != nil {
        if errors.IsNotFound(err) {
            return nil
        }
        networkPolicyOperationsTotal.WithLabelValues("delete", "failed").Inc()
        return fmt.Errorf("failed to list network policies: %v", err)
    }
    
    for _, policy := range policies.Items {
        err = n.kubeClient.NetworkingV1().NetworkPolicies(namespace).Delete(
            context.TODO(), policy.Name, metav1.DeleteOptions{})
        if err != nil && !errors.IsNotFound(err) {
            networkPolicyOperationsTotal.WithLabelValues("delete", "failed").Inc()
            return fmt.Errorf("failed to delete network policy %s: %v", policy.Name, err)
        }
    }
    
    networkPolicyOperationsTotal.WithLabelValues("delete", "success").Inc()
    return nil
}

// networkPolicyOperationDurationSeconds tracks the duration of network policy operations
var networkPolicyOperationDurationSeconds = prometheus.NewHistogramVec(
    prometheus.HistogramOpts{
        Name: "llmsafespace_network_policy_operation_duration_seconds",
        Help: "Duration of network policy operations in seconds",
        Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5},
    },
    []string{"operation"},
)
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

// hasSandboxSecurityEvents checks if a sandbox has recorded security events
func (c *Controller) hasSandboxSecurityEvents(sandbox *llmsafespacev1.Sandbox) bool {
    // Check for security events in sandbox status or audit logs
    for _, condition := range sandbox.Status.Conditions {
        if strings.HasPrefix(condition.Type, "Security") && condition.Status == "True" {
            return true
        }
    }
    
    // Check for security events in annotations
    if sandbox.Annotations != nil {
        securityEvents := []string{
            "llmsafespace.dev/security-event",
            "llmsafespace.dev/seccomp-violation",
            "llmsafespace.dev/apparmor-violation",
            "llmsafespace.dev/capability-violation",
            "llmsafespace.dev/syscall-violation",
            "llmsafespace.dev/network-violation",
        }
        
        for _, eventKey := range securityEvents {
            if _, ok := sandbox.Annotations[eventKey]; ok {
                return true
            }
        }
    }
    
    // Check audit logs for security events if audit logging is enabled
    if c.config.EnableAuditLogging {
        events, err := c.auditLogger.GetSecurityEvents(sandbox.UID, time.Hour)
        if err != nil {
            klog.Warningf("Failed to get security events for sandbox %s: %v", sandbox.Name, err)
            return false
        }
        
        return len(events) > 0
    }
    
    return false
}

// hasInstalledUntrustedPackages checks if a sandbox installed packages not in the allowlist
// Returns a list of untrusted packages and a boolean indicating if any were found
func (c *Controller) hasInstalledUntrustedPackages(sandbox *llmsafespacev1.Sandbox) ([]string, bool) {
    untrustedPackages := []string{}
    
    // Check if we have package installation tracking in annotations
    if sandbox.Annotations != nil {
        if packagesStr, ok := sandbox.Annotations["llmsafespace.dev/installed-packages"]; ok {
            packages := strings.Split(packagesStr, ",")
            
            // Check each package against the allowlist
            for _, pkg := range packages {
                if !c.isPackageAllowed(pkg, sandbox.Spec.Runtime) {
                    untrustedPackages = append(untrustedPackages, pkg)
                }
            }
        }
    }
    
    // If package tracking is enabled, check the package manager logs
    if c.config.EnablePackageTracking {
        installedPackages, err := c.packageTracker.GetInstalledPackages(sandbox)
        if err != nil {
            klog.Warningf("Failed to get installed packages for sandbox %s: %v", sandbox.Name, err)
        } else {
            for _, pkg := range installedPackages {
                if !c.isPackageAllowed(pkg, sandbox.Spec.Runtime) && !containsString(untrustedPackages, pkg) {
                    untrustedPackages = append(untrustedPackages, pkg)
                }
            }
        }
    }
    
    return untrustedPackages, len(untrustedPackages) > 0
}

// isPackageAllowed checks if a package is in the allowlist for a runtime
func (c *Controller) isPackageAllowed(pkg, runtime string) bool {
    // Extract runtime language from the runtime string
    runtimeLang := strings.Split(runtime, ":")[0]
    
    // Extract base package name (without version)
    basePkg := pkg
    if idx := strings.Index(pkg, "=="); idx > 0 {
        basePkg = pkg[:idx]
    } else if idx := strings.Index(pkg, "@"); idx > 0 {
        basePkg = pkg[:idx]
    }
    
    // Check against configured allowlist
    allowlist, ok := c.config.PackageAllowlists[runtimeLang]
    if !ok {
        // Fall back to default allowlists if no specific one is configured
        defaultAllowlists := map[string][]string{
            "python": {"numpy", "pandas", "matplotlib", "scikit-learn", "tensorflow", "torch", "requests"},
            "nodejs": {"axios", "express", "lodash", "moment", "react", "vue"},
            "go": {"github.com/gorilla/mux", "github.com/gin-gonic/gin", "github.com/spf13/cobra"},
            "ruby": {"rails", "sinatra", "rspec", "puma"},
            "java": {"org.springframework", "com.google.guava", "org.apache.commons"},
        }
        
        allowlist, ok = defaultAllowlists[runtimeLang]
        if !ok {
            // If no default allowlist exists for this runtime, deny all packages
            return false
        }
    }
    
    // Check if the package is in the allowlist
    for _, allowed := range allowlist {
        if basePkg == allowed || strings.HasPrefix(basePkg, allowed+".") {
            return true
        }
    }
    
    // Check if the package is in the global allowlist
    for _, allowed := range c.config.GlobalPackageAllowlist {
        if basePkg == allowed || strings.HasPrefix(basePkg, allowed+".") {
            return true
        }
    }
    
    // If package verification is enabled, check package signature
    if c.config.EnablePackageVerification {
        verified, err := c.packageVerifier.VerifyPackage(pkg, runtimeLang)
        if err != nil {
            klog.Warningf("Failed to verify package %s: %v", pkg, err)
        } else if verified {
            return true
        }
    }
    
    return false
}

// hasModifiedSystemFiles checks if a sandbox modified any system files
// Returns a list of modified files and a boolean indicating if any were found
func (c *Controller) hasModifiedSystemFiles(sandbox *llmsafespacev1.Sandbox) ([]string, bool) {
    modifiedFiles := []string{}
    
    // Check if there's a record of file modifications in annotations
    if sandbox.Annotations != nil {
        if filesStr, ok := sandbox.Annotations["llmsafespace.dev/modified-system-files"]; ok {
            modifiedFiles = strings.Split(filesStr, ",")
            return modifiedFiles, len(modifiedFiles) > 0
        }
    }
    
    // If file integrity monitoring is enabled, check for modified files
    if c.config.EnableFileIntegrityMonitoring {
        pod, err := c.podLister.Pods(sandbox.Status.PodNamespace).Get(sandbox.Status.PodName)
        if err != nil {
            klog.Warningf("Failed to get pod for sandbox %s: %v", sandbox.Name, err)
            return modifiedFiles, false
        }
        
        // Get list of modified files from the file integrity monitor
        files, err := c.fileIntegrityMonitor.GetModifiedFiles(pod)
        if err != nil {
            klog.Warningf("Failed to get modified files for pod %s: %v", pod.Name, err)
            return modifiedFiles, false
        }
        
        // Check each file against the list of protected system paths
        for _, file := range files {
            for _, protectedPath := range c.config.ProtectedSystemPaths {
                if strings.HasPrefix(file, protectedPath) {
                    modifiedFiles = append(modifiedFiles, file)
                    break
                }
            }
        }
    }
    
    return modifiedFiles, len(modifiedFiles) > 0
}

// hadExcessiveResourceUsage checks if a sandbox had excessive resource usage
// Returns a boolean indicating if usage was excessive and a reason string
func (c *Controller) hadExcessiveResourceUsage(sandbox *llmsafespacev1.Sandbox) (bool, string) {
    // Define thresholds
    cpuThreshold := 0.9  // 90% CPU usage
    memThreshold := 0.9  // 90% memory usage
    diskThreshold := 0.9 // 90% disk usage
    
    // Override with config values if set
    if c.config.ResourceThresholds.CPU > 0 {
        cpuThreshold = c.config.ResourceThresholds.CPU
    }
    if c.config.ResourceThresholds.Memory > 0 {
        memThreshold = c.config.ResourceThresholds.Memory
    }
    if c.config.ResourceThresholds.Disk > 0 {
        diskThreshold = c.config.ResourceThresholds.Disk
    }
    
    // Check annotations for resource usage records
    if sandbox.Annotations != nil {
        // Check CPU usage
        if cpuUsageStr, ok := sandbox.Annotations["llmsafespace.dev/max-cpu-usage"]; ok {
            cpuUsage, err := strconv.ParseFloat(cpuUsageStr, 64)
            if err == nil && cpuUsage > cpuThreshold {
                return true, fmt.Sprintf("CPU usage %.2f exceeds threshold %.2f", cpuUsage, cpuThreshold)
            }
        }
        
        // Check memory usage
        if memUsageStr, ok := sandbox.Annotations["llmsafespace.dev/max-memory-usage"]; ok {
            memUsage, err := strconv.ParseFloat(memUsageStr, 64)
            if err == nil && memUsage > memThreshold {
                return true, fmt.Sprintf("Memory usage %.2f exceeds threshold %.2f", memUsage, memThreshold)
            }
        }
        
        // Check disk usage
        if diskUsageStr, ok := sandbox.Annotations["llmsafespace.dev/max-disk-usage"]; ok {
            diskUsage, err := strconv.ParseFloat(diskUsageStr, 64)
            if err == nil && diskUsage > diskThreshold {
                return true, fmt.Sprintf("Disk usage %.2f exceeds threshold %.2f", diskUsage, diskThreshold)
            }
        }
    }
    
    // If metrics collection is enabled, check historical metrics
    if c.config.EnableMetricsCollection {
        // Get pod metrics
        pod, err := c.podLister.Pods(sandbox.Status.PodNamespace).Get(sandbox.Status.PodName)
        if err != nil {
            klog.Warningf("Failed to get pod for sandbox %s: %v", sandbox.Name, err)
            return false, ""
        }
        
        // Get resource usage metrics
        metrics, err := c.metricsClient.GetPodMetrics(pod.Namespace, pod.Name)
        if err != nil {
            klog.Warningf("Failed to get metrics for pod %s: %v", pod.Name, err)
            return false, ""
        }
        
        // Check if any metric exceeds thresholds
        if metrics.MaxCPUUsage > cpuThreshold {
            return true, fmt.Sprintf("Historical CPU usage %.2f exceeds threshold %.2f", 
                metrics.MaxCPUUsage, cpuThreshold)
        }
        
        if metrics.MaxMemoryUsage > memThreshold {
            return true, fmt.Sprintf("Historical memory usage %.2f exceeds threshold %.2f", 
                metrics.MaxMemoryUsage, memThreshold)
        }
        
        if metrics.MaxDiskUsage > diskThreshold {
            return true, fmt.Sprintf("Historical disk usage %.2f exceeds threshold %.2f", 
                metrics.MaxDiskUsage, diskThreshold)
        }
    }
    
    return false, ""
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

// hasActiveProcesses checks if a warm pod has active processes that would make recycling unsafe
func (c *Controller) hasActiveProcesses(warmPod *llmsafespacev1.WarmPod) bool {
    // Skip this check if process checking is disabled
    if !c.config.CheckActiveProcessesBeforeRecycling {
        return false
    }
    
    pod, err := c.podLister.Pods(warmPod.Status.PodNamespace).Get(warmPod.Status.PodName)
    if err != nil {
        klog.Warningf("Failed to get pod for warm pod %s: %v", warmPod.Name, err)
        return false
    }
    
    // Execute ps command in the pod to check for active processes
    execReq := c.kubeClient.CoreV1().RESTClient().Post().
        Resource("pods").
        Name(pod.Name).
        Namespace(pod.Namespace).
        SubResource("exec").
        VersionedParams(&corev1.PodExecOptions{
            Command: []string{"/bin/sh", "-c", "ps aux | grep -v 'ps aux\\|grep\\|sleep\\|sh -c\\|sandbox-init' | wc -l"},
            Stdin:   false,
            Stdout:  true,
            Stderr:  true,
            TTY:     false,
            Container: "sandbox",
        }, scheme.ParameterCodec)
    
    // Execute the command
    exec, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", execReq.URL())
    if err != nil {
        klog.Warningf("Failed to create executor for process check: %v", err)
        return false
    }
    
    var stdout, stderr bytes.Buffer
    err = exec.Stream(remotecommand.StreamOptions{
        Stdout: &stdout,
        Stderr: &stderr,
    })
    
    if err != nil {
        klog.Warningf("Failed to execute process check: %v", err)
        return false
    }
    
    // Parse the output to get the number of active processes
    processCount, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
    if err != nil {
        klog.Warningf("Failed to parse process count: %v", err)
        return false
    }
    
    // If there are more than the expected baseline processes, consider it active
    return processCount > c.config.BaselineProcessCount
}

// hasActiveNetworkConnections checks if a warm pod has active network connections
func (c *Controller) hasActiveNetworkConnections(warmPod *llmsafespacev1.WarmPod) bool {
    // Skip this check if network connection checking is disabled
    if !c.config.CheckNetworkConnectionsBeforeRecycling {
        return false
    }
    
    pod, err := c.podLister.Pods(warmPod.Status.PodNamespace).Get(warmPod.Status.PodName)
    if err != nil {
        klog.Warningf("Failed to get pod for warm pod %s: %v", warmPod.Name, err)
        return false
    }
    
    // Execute netstat command in the pod to check for active connections
    execReq := c.kubeClient.CoreV1().RESTClient().Post().
        Resource("pods").
        Name(pod.Name).
        Namespace(pod.Namespace).
        SubResource("exec").
        VersionedParams(&corev1.PodExecOptions{
            Command: []string{"/bin/sh", "-c", "netstat -tuln | grep -v '127.0.0.1\\|::1' | grep ESTABLISHED | wc -l"},
            Stdin:   false,
            Stdout:  true,
            Stderr:  true,
            TTY:     false,
            Container: "sandbox",
        }, scheme.ParameterCodec)
    
    // Execute the command
    exec, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", execReq.URL())
    if err != nil {
        klog.Warningf("Failed to create executor for network check: %v", err)
        return false
    }
    
    var stdout, stderr bytes.Buffer
    err = exec.Stream(remotecommand.StreamOptions{
        Stdout: &stdout,
        Stderr: &stderr,
    })
    
    if err != nil {
        klog.Warningf("Failed to execute network check: %v", err)
        return false
    }
    
    // Parse the output to get the number of active connections
    connectionCount, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
    if err != nil {
        klog.Warningf("Failed to parse connection count: %v", err)
        return false
    }
    
    // If there are any active connections, consider it active
    return connectionCount > 0
}

// hasFileLocks checks if a warm pod has any file locks that would make recycling unsafe
func (c *Controller) hasFileLocks(warmPod *llmsafespacev1.WarmPod) bool {
    // Skip this check if file lock checking is disabled
    if !c.config.CheckFileLocksBeforeRecycling {
        return false
    }
    
    pod, err := c.podLister.Pods(warmPod.Status.PodNamespace).Get(warmPod.Status.PodName)
    if err != nil {
        klog.Warningf("Failed to get pod for warm pod %s: %v", warmPod.Name, err)
        return false
    }
    
    // Execute lsof command in the pod to check for file locks
    execReq := c.kubeClient.CoreV1().RESTClient().Post().
        Resource("pods").
        Name(pod.Name).
        Namespace(pod.Namespace).
        SubResource("exec").
        VersionedParams(&corev1.PodExecOptions{
            Command: []string{"/bin/sh", "-c", "lsof -F | grep -v '/dev\\|/proc\\|/sys\\|/tmp' | wc -l"},
            Stdin:   false,
            Stdout:  true,
            Stderr:  true,
            TTY:     false,
            Container: "sandbox",
        }, scheme.ParameterCodec)
    
    // Execute the command
    exec, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", execReq.URL())
    if err != nil {
        klog.Warningf("Failed to create executor for file lock check: %v", err)
        return false
    }
    
    var stdout, stderr bytes.Buffer
    err = exec.Stream(remotecommand.StreamOptions{
        Stdout: &stdout,
        Stderr: &stderr,
    })
    
    if err != nil {
        klog.Warningf("Failed to execute file lock check: %v", err)
        return false
    }
    
    // Parse the output to get the number of file locks
    lockCount, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
    if err != nil {
        klog.Warningf("Failed to parse lock count: %v", err)
        return false
    }
    
    // If there are more than the expected baseline file locks, consider it unsafe
    return lockCount > c.config.BaselineFileLockCount
}

// hasMemoryLeaks checks if a warm pod has any memory leaks that would make recycling unsafe
func (c *Controller) hasMemoryLeaks(warmPod *llmsafespacev1.WarmPod) bool {
    // Skip this check if memory leak checking is disabled
    if !c.config.CheckMemoryLeaksBeforeRecycling {
        return false
    }
    
    pod, err := c.podLister.Pods(warmPod.Status.PodNamespace).Get(warmPod.Status.PodName)
    if err != nil {
        klog.Warningf("Failed to get pod for warm pod %s: %v", warmPod.Name, err)
        return false
    }
    
    // Get memory usage metrics
    metrics, err := c.metricsClient.GetPodMetrics(pod.Namespace, pod.Name)
    if err != nil {
        klog.Warningf("Failed to get metrics for pod %s: %v", pod.Name, err)
        return false
    }
    
    // Check if memory usage is increasing over time (potential leak)
    if metrics.MemoryGrowthRate > c.config.MemoryLeakThreshold {
        klog.V(2).Infof("Pod %s shows signs of memory leak: growth rate %.2f exceeds threshold %.2f", 
            pod.Name, metrics.MemoryGrowthRate, c.config.MemoryLeakThreshold)
        return true
    }
    
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

// APIServiceClient handles communication with the API service
type APIServiceClient struct {
    baseURL     string
    httpClient  *http.Client
    authToken   string
    retryConfig RetryConfig
    metrics     *APIServiceMetrics
}

// RetryConfig defines the retry behavior for API service requests
type RetryConfig struct {
    MaxRetries  int
    InitialWait time.Duration
    MaxWait     time.Duration
}

// APIServiceMetrics collects metrics for API service interactions
type APIServiceMetrics struct {
    requestsTotal      *prometheus.CounterVec
    latencySeconds     *prometheus.HistogramVec
    retryAttemptsTotal *prometheus.CounterVec
}

// NewAPIServiceClient creates a new API service client
func NewAPIServiceClient(baseURL, authToken string, metrics *APIServiceMetrics) *APIServiceClient {
    return &APIServiceClient{
        baseURL:    baseURL,
        authToken:  authToken,
        httpClient: &http.Client{
            Timeout: 10 * time.Second,
            Transport: &http.Transport{
                MaxIdleConns:        100,
                MaxIdleConnsPerHost: 20,
                IdleConnTimeout:     90 * time.Second,
                TLSHandshakeTimeout: 10 * time.Second,
            },
        },
        retryConfig: RetryConfig{
            MaxRetries:  3,
            InitialWait: 100 * time.Millisecond,
            MaxWait:     2 * time.Second,
        },
        metrics: metrics,
    }
}

// NotifySandboxStatus notifies the API service about sandbox status changes
func (c *APIServiceClient) NotifySandboxStatus(sandbox *llmsafespacev1.Sandbox) error {
    startTime := time.Now()
    defer func() {
        c.metrics.latencySeconds.WithLabelValues("notify_status").Observe(time.Since(startTime).Seconds())
    }()
    
    url := fmt.Sprintf("%s/internal/sandboxes/%s/status", c.baseURL, sandbox.Name)
    
    payload := map[string]interface{}{
        "status":       sandbox.Status.Phase,
        "podName":      sandbox.Status.PodName,
        "podNamespace": sandbox.Status.PodNamespace,
        "endpoint":     sandbox.Status.Endpoint,
        "startTime":    sandbox.Status.StartTime,
        "conditions":   sandbox.Status.Conditions,
        "resources":    sandbox.Status.Resources,
        "warmPodRef":   sandbox.Status.WarmPodRef,
    }
    
    jsonPayload, err := json.Marshal(payload)
    if err != nil {
        c.metrics.requestsTotal.WithLabelValues("notify_status", "error").Inc()
        return fmt.Errorf("failed to marshal payload: %v", err)
    }
    
    var resp *http.Response
    var retryCount int
    
    // Retry loop
    for retryCount = 0; retryCount <= c.retryConfig.MaxRetries; retryCount++ {
        if retryCount > 0 {
            // Record retry attempt
            c.metrics.retryAttemptsTotal.WithLabelValues("notify_status").Inc()
            
            // Wait before retrying with exponential backoff
            waitTime := c.retryConfig.InitialWait * time.Duration(1<<uint(retryCount-1))
            if waitTime > c.retryConfig.MaxWait {
                waitTime = c.retryConfig.MaxWait
            }
            time.Sleep(waitTime)
        }
        
        req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
        if err != nil {
            continue // Retry on request creation error
        }
        
        req.Header.Set("Content-Type", "application/json")
        req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.authToken))
        req.Header.Set("User-Agent", "LLMSafeSpace-Controller/1.0")
        
        // Add context with timeout
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        req = req.WithContext(ctx)
        
        resp, err = c.httpClient.Do(req)
        cancel() // Cancel the context to release resources
        
        if err != nil {
            klog.Warningf("API request failed (attempt %d/%d): %v", 
                retryCount+1, c.retryConfig.MaxRetries+1, err)
            continue // Retry on connection error
        }
        
        // Break on success or non-retryable errors
        if resp.StatusCode < 500 {
            break
        }
        
        // Close response body before retrying
        resp.Body.Close()
    }
    
    // Check if all retries were exhausted
    if retryCount > c.retryConfig.MaxRetries {
        c.metrics.requestsTotal.WithLabelValues("notify_status", "error").Inc()
        return fmt.Errorf("failed to send request after %d retries", c.retryConfig.MaxRetries)
    }
    
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        c.metrics.requestsTotal.WithLabelValues("notify_status", "error").Inc()
        
        // Try to read error response
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
    }
    
    c.metrics.requestsTotal.WithLabelValues("notify_status", "success").Inc()
    return nil
}

// RequestWarmPod requests a warm pod from the API service
func (c *APIServiceClient) RequestWarmPod(runtime, securityLevel string, resourceRequirements *llmsafespacev1.ResourceRequirements) (*llmsafespacev1.WarmPod, error) {
    startTime := time.Now()
    defer func() {
        c.metrics.latencySeconds.WithLabelValues("request_warmpod").Observe(time.Since(startTime).Seconds())
    }()
    
    url := fmt.Sprintf("%s/internal/warmpods/allocate", c.baseURL)
    
    payload := map[string]interface{}{
        "runtime":       runtime,
        "securityLevel": securityLevel,
    }
    
    // Add resource requirements if specified
    if resourceRequirements != nil {
        payload["resources"] = resourceRequirements
    }
    
    jsonPayload, err := json.Marshal(payload)
    if err != nil {
        c.metrics.requestsTotal.WithLabelValues("request_warmpod", "error").Inc()
        return nil, fmt.Errorf("failed to marshal payload: %v", err)
    }
    
    var resp *http.Response
    var retryCount int
    
    // Retry loop
    for retryCount = 0; retryCount <= c.retryConfig.MaxRetries; retryCount++ {
        if retryCount > 0 {
            // Record retry attempt
            c.metrics.retryAttemptsTotal.WithLabelValues("request_warmpod").Inc()
            
            // Wait before retrying with exponential backoff
            waitTime := c.retryConfig.InitialWait * time.Duration(1<<uint(retryCount-1))
            if waitTime > c.retryConfig.MaxWait {
                waitTime = c.retryConfig.MaxWait
            }
            time.Sleep(waitTime)
        }
        
        req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
        if err != nil {
            continue // Retry on request creation error
        }
        
        req.Header.Set("Content-Type", "application/json")
        req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.authToken))
        req.Header.Set("User-Agent", "LLMSafeSpace-Controller/1.0")
        
        // Add context with timeout
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        req = req.WithContext(ctx)
        
        resp, err = c.httpClient.Do(req)
        cancel() // Cancel the context to release resources
        
        if err != nil {
            klog.Warningf("API request failed (attempt %d/%d): %v", 
                retryCount+1, c.retryConfig.MaxRetries+1, err)
            continue // Retry on connection error
        }
        
        // Break on success, not found, or non-retryable errors
        if resp.StatusCode == http.StatusNotFound || resp.StatusCode < 500 {
            break
        }
        
        // Close response body before retrying
        resp.Body.Close()
    }
    
    // Check if all retries were exhausted
    if retryCount > c.retryConfig.MaxRetries {
        c.metrics.requestsTotal.WithLabelValues("request_warmpod", "error").Inc()
        return nil, fmt.Errorf("failed to send request after %d retries", c.retryConfig.MaxRetries)
    }
    
    defer resp.Body.Close()
    
    if resp.StatusCode == http.StatusNotFound {
        c.metrics.requestsTotal.WithLabelValues("request_warmpod", "not_found").Inc()
        return nil, nil // No warm pod available
    }
    
    if resp.StatusCode != http.StatusOK {
        c.metrics.requestsTotal.WithLabelValues("request_warmpod", "error").Inc()
        
        // Try to read error response
        body, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
    }
    
    var result struct {
        WarmPod *llmsafespacev1.WarmPod `json:"warmPod"`
    }
    
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        c.metrics.requestsTotal.WithLabelValues("request_warmpod", "error").Inc()
        return nil, fmt.Errorf("failed to decode response: %v", err)
    }
    
    c.metrics.requestsTotal.WithLabelValues("request_warmpod", "success").Inc()
    return result.WarmPod, nil
}

// ReleaseWarmPod notifies the API service that a warm pod has been released
func (c *APIServiceClient) ReleaseWarmPod(warmPodName, warmPodNamespace string, recycled bool) error {
    startTime := time.Now()
    defer func() {
        c.metrics.latencySeconds.WithLabelValues("release_warmpod").Observe(time.Since(startTime).Seconds())
    }()
    
    url := fmt.Sprintf("%s/internal/warmpods/release", c.baseURL)
    
    payload := map[string]interface{}{
        "name":      warmPodName,
        "namespace": warmPodNamespace,
        "recycled":  recycled,
    }
    
    jsonPayload, err := json.Marshal(payload)
    if err != nil {
        c.metrics.requestsTotal.WithLabelValues("release_warmpod", "error").Inc()
        return fmt.Errorf("failed to marshal payload: %v", err)
    }
    
    var resp *http.Response
    var retryCount int
    
    // Retry loop
    for retryCount = 0; retryCount <= c.retryConfig.MaxRetries; retryCount++ {
        if retryCount > 0 {
            // Record retry attempt
            c.metrics.retryAttemptsTotal.WithLabelValues("release_warmpod").Inc()
            
            // Wait before retrying with exponential backoff
            waitTime := c.retryConfig.InitialWait * time.Duration(1<<uint(retryCount-1))
            if waitTime > c.retryConfig.MaxWait {
                waitTime = c.retryConfig.MaxWait
            }
            time.Sleep(waitTime)
        }
        
        req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
        if err != nil {
            continue // Retry on request creation error
        }
        
        req.Header.Set("Content-Type", "application/json")
        req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.authToken))
        req.Header.Set("User-Agent", "LLMSafeSpace-Controller/1.0")
        
        // Add context with timeout
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        req = req.WithContext(ctx)
        
        resp, err = c.httpClient.Do(req)
        cancel() // Cancel the context to release resources
        
        if err != nil {
            klog.Warningf("API request failed (attempt %d/%d): %v", 
                retryCount+1, c.retryConfig.MaxRetries+1, err)
            continue // Retry on connection error
        }
        
        // Break on success or non-retryable errors
        if resp.StatusCode < 500 {
            break
        }
        
        // Close response body before retrying
        resp.Body.Close()
    }
    
    // Check if all retries were exhausted
    if retryCount > c.retryConfig.MaxRetries {
        c.metrics.requestsTotal.WithLabelValues("release_warmpod", "error").Inc()
        return fmt.Errorf("failed to send request after %d retries", c.retryConfig.MaxRetries)
    }
    
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        c.metrics.requestsTotal.WithLabelValues("release_warmpod", "error").Inc()
        
        // Try to read error response
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
    }
    
    c.metrics.requestsTotal.WithLabelValues("release_warmpod", "success").Inc()
    return nil
}

// GetWarmPoolStats gets statistics about warm pools from the API service
func (c *APIServiceClient) GetWarmPoolStats() (map[string]interface{}, error) {
    startTime := time.Now()
    defer func() {
        c.metrics.latencySeconds.WithLabelValues("get_warmpool_stats").Observe(time.Since(startTime).Seconds())
    }()
    
    url := fmt.Sprintf("%s/internal/warmpools/stats", c.baseURL)
    
    var resp *http.Response
    var retryCount int
    
    // Retry loop
    for retryCount = 0; retryCount <= c.retryConfig.MaxRetries; retryCount++ {
        if retryCount > 0 {
            // Record retry attempt
            c.metrics.retryAttemptsTotal.WithLabelValues("get_warmpool_stats").Inc()
            
            // Wait before retrying with exponential backoff
            waitTime := c.retryConfig.InitialWait * time.Duration(1<<uint(retryCount-1))
            if waitTime > c.retryConfig.MaxWait {
                waitTime = c.retryConfig.MaxWait
            }
            time.Sleep(waitTime)
        }
        
        req, err := http.NewRequest("GET", url, nil)
        if err != nil {
            continue // Retry on request creation error
        }
        
        req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.authToken))
        req.Header.Set("User-Agent", "LLMSafeSpace-Controller/1.0")
        
        // Add context with timeout
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        req = req.WithContext(ctx)
        
        resp, err = c.httpClient.Do(req)
        cancel() // Cancel the context to release resources
        
        if err != nil {
            klog.Warningf("API request failed (attempt %d/%d): %v", 
                retryCount+1, c.retryConfig.MaxRetries+1, err)
            continue // Retry on connection error
        }
        
        // Break on success or non-retryable errors
        if resp.StatusCode < 500 {
            break
        }
        
        // Close response body before retrying
        resp.Body.Close()
    }
    
    // Check if all retries were exhausted
    if retryCount > c.retryConfig.MaxRetries {
        c.metrics.requestsTotal.WithLabelValues("get_warmpool_stats", "error").Inc()
        return nil, fmt.Errorf("failed to send request after %d retries", c.retryConfig.MaxRetries)
    }
    
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        c.metrics.requestsTotal.WithLabelValues("get_warmpool_stats", "error").Inc()
        
        // Try to read error response
        body, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
    }
    
    var result map[string]interface{}
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        c.metrics.requestsTotal.WithLabelValues("get_warmpool_stats", "error").Inc()
        return nil, fmt.Errorf("failed to decode response: %v", err)
    }
    
    c.metrics.requestsTotal.WithLabelValues("get_warmpool_stats", "success").Inc()
    return result, nil
}

// ReportSecurityEvent reports a security event to the API service
func (c *APIServiceClient) ReportSecurityEvent(sandboxID string, eventType string, severity string, details map[string]interface{}) error {
    startTime := time.Now()
    defer func() {
        c.metrics.latencySeconds.WithLabelValues("report_security_event").Observe(time.Since(startTime).Seconds())
    }()
    
    url := fmt.Sprintf("%s/internal/security/events", c.baseURL)
    
    payload := map[string]interface{}{
        "sandboxID": sandboxID,
        "eventType": eventType,
        "severity":  severity,
        "details":   details,
        "timestamp": time.Now().Format(time.RFC3339),
    }
    
    jsonPayload, err := json.Marshal(payload)
    if err != nil {
        c.metrics.requestsTotal.WithLabelValues("report_security_event", "error").Inc()
        return fmt.Errorf("failed to marshal payload: %v", err)
    }
    
    var resp *http.Response
    var retryCount int
    
    // Retry loop
    for retryCount = 0; retryCount <= c.retryConfig.MaxRetries; retryCount++ {
        if retryCount > 0 {
            // Record retry attempt
            c.metrics.retryAttemptsTotal.WithLabelValues("report_security_event").Inc()
            
            // Wait before retrying with exponential backoff
            waitTime := c.retryConfig.InitialWait * time.Duration(1<<uint(retryCount-1))
            if waitTime > c.retryConfig.MaxWait {
                waitTime = c.retryConfig.MaxWait
            }
            time.Sleep(waitTime)
        }
        
        req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
        if err != nil {
            continue // Retry on request creation error
        }
        
        req.Header.Set("Content-Type", "application/json")
        req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.authToken))
        req.Header.Set("User-Agent", "LLMSafeSpace-Controller/1.0")
        
        // Add context with timeout
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        req = req.WithContext(ctx)
        
        resp, err = c.httpClient.Do(req)
        cancel() // Cancel the context to release resources
        
        if err != nil {
            klog.Warningf("API request failed (attempt %d/%d): %v", 
                retryCount+1, c.retryConfig.MaxRetries+1, err)
            continue // Retry on connection error
        }
        
        // Break on success or non-retryable errors
        if resp.StatusCode < 500 {
            break
        }
        
        // Close response body before retrying
        resp.Body.Close()
    }
    
    // Check if all retries were exhausted
    if retryCount > c.retryConfig.MaxRetries {
        c.metrics.requestsTotal.WithLabelValues("report_security_event", "error").Inc()
        return fmt.Errorf("failed to send request after %d retries", c.retryConfig.MaxRetries)
    }
    
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
        c.metrics.requestsTotal.WithLabelValues("report_security_event", "error").Inc()
        
        // Try to read error response
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
    }
    
    c.metrics.requestsTotal.WithLabelValues("report_security_event", "success").Inc()
    return nil
}

// NewAPIServiceMetrics creates a new APIServiceMetrics instance
func NewAPIServiceMetrics() *APIServiceMetrics {
    return &APIServiceMetrics{
        requestsTotal: prometheus.NewCounterVec(
            prometheus.CounterOpts{
                Name: "llmsafespace_api_service_requests_total",
                Help: "Total number of requests to the API service",
            },
            []string{"request_type", "status"},
        ),
        latencySeconds: prometheus.NewHistogramVec(
            prometheus.HistogramOpts{
                Name: "llmsafespace_api_service_latency_seconds",
                Help: "Latency of API service requests",
                Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5},
            },
            []string{"request_type"},
        ),
        retryAttemptsTotal: prometheus.NewCounterVec(
            prometheus.CounterOpts{
                Name: "llmsafespace_api_service_retry_attempts_total",
                Help: "Total number of retry attempts for API service requests",
            },
            []string{"request_type"},
        ),
    }
}

// RegisterMetrics registers the API service metrics with Prometheus
func (m *APIServiceMetrics) RegisterMetrics() {
    prometheus.MustRegister(m.requestsTotal)
    prometheus.MustRegister(m.latencySeconds)
    prometheus.MustRegister(m.retryAttemptsTotal)
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

The Sandbox Controller exposes Prometheus metrics for monitoring its operation and the state of managed resources:

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
    // Sandbox metrics
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
    
    sandboxesFailedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_sandboxes_failed_total",
            Help: "Total number of sandboxes that failed to create",
        },
        []string{"reason"},
    )
    
    sandboxStartupDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_sandbox_startup_duration_seconds",
            Help: "Time taken for a sandbox to start up",
            Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
        },
        []string{"runtime", "warm_pod_used"},
    )
    
    sandboxExecutionsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_sandbox_executions_total",
            Help: "Total number of code/command executions in sandboxes",
        },
        []string{"runtime", "execution_type", "status"},
    )
    
    sandboxExecutionDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_sandbox_execution_duration_seconds",
            Help: "Duration of code/command executions in sandboxes",
            Buckets: prometheus.ExponentialBuckets(0.01, 2, 15),
        },
        []string{"runtime", "execution_type"},
    )
    
    // Warm pool metrics
    warmPoolsCreatedTotal = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "llmsafespace_warmpools_created_total",
            Help: "Total number of warm pools created",
        },
    )
    
    warmPoolsDeletedTotal = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "llmsafespace_warmpools_deleted_total",
            Help: "Total number of warm pools deleted",
        },
    )
    
    warmPoolSizeGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_warmpool_size",
            Help: "Current size of warm pools",
        },
        []string{"pool", "runtime", "status"},
    )
    
    warmPoolUtilizationGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_warmpool_utilization",
            Help: "Utilization ratio of warm pools (assigned pods / total pods)",
        },
        []string{"pool", "runtime"},
    )
    
    warmPoolAssignmentDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_warmpool_assignment_duration_seconds",
            Help: "Time taken to assign a warm pod to a sandbox",
            Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
        },
        []string{"pool", "runtime"},
    )
    
    warmPoolCreationDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "llmsafespace_warmpool_creation_duration_seconds",
            Help: "Time taken to create a warm pod",
            Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30},
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
            Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30},
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
    
    warmPoolPodsDeletedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_warmpool_pods_deleted_total",
            Help: "Total number of warm pods deleted",
        },
        []string{"pool", "runtime", "reason"},
    )
    
    warmPoolScalingOperationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_warmpool_scaling_operations_total",
            Help: "Total number of warm pool scaling operations",
        },
        []string{"pool", "runtime", "operation"},
    )
    
    warmPodRecycleDecisionDurationSeconds = prometheus.NewHistogram(
        prometheus.HistogramOpts{
            Name: "llmsafespace_warmpod_recycle_decision_duration_seconds",
            Help: "Time taken to decide whether to recycle a warm pod",
            Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5},
        },
    )
    
    warmPodRecycleDecisionsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_warmpod_recycle_decisions_total",
            Help: "Total number of warm pod recycle decisions",
        },
        []string{"reason", "decision"},
    )
    
    // Runtime environment metrics
    runtimeEnvironmentValidationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_runtime_environment_validations_total",
            Help: "Total number of runtime environment validations",
        },
        []string{"language", "version", "result"},
    )
    
    runtimeEnvironmentUsageTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_runtime_environment_usage_total",
            Help: "Total number of times each runtime environment is used",
        },
        []string{"language", "version"},
    )
    
    // Sandbox profile metrics
    sandboxProfileValidationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_sandbox_profile_validations_total",
            Help: "Total number of sandbox profile validations",
        },
        []string{"language", "security_level"},
    )
    
    sandboxProfileUsageTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_sandbox_profile_usage_total",
            Help: "Total number of times each sandbox profile is used",
        },
        []string{"profile", "namespace"},
    )
    
    // Security metrics
    securityEventsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_security_events_total",
            Help: "Total number of security events detected",
        },
        []string{"event_type", "severity", "runtime"},
    )
    
    seccompViolationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_seccomp_violations_total",
            Help: "Total number of seccomp violations",
        },
        []string{"syscall", "runtime", "action"},
    )
    
    networkViolationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_network_violations_total",
            Help: "Total number of network policy violations",
        },
        []string{"direction", "destination", "runtime"},
    )
    
    // Resource usage metrics
    resourceUsageGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_resource_usage",
            Help: "Resource usage by sandboxes and warm pools",
        },
        []string{"resource_type", "component", "namespace"},
    )
    
    resourceLimitUtilizationGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_resource_limit_utilization",
            Help: "Resource utilization as a percentage of limit",
        },
        []string{"resource_type", "sandbox_id", "namespace"},
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
    
    persistentVolumeUsageGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_persistent_volume_usage_bytes",
            Help: "Usage of persistent volumes in bytes",
        },
        []string{"sandbox_id", "namespace"},
    )
    
    // Network policy metrics
    networkPolicyOperationsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_network_policy_operations_total",
            Help: "Total number of network policy operations",
        },
        []string{"operation", "status"},
    )
    
    // Controller metrics
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
    
    controllerSyncCountTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "llmsafespace_controller_sync_count_total",
            Help: "Total number of sync operations performed by the controller",
        },
        []string{"resource"},
    )
    
    controllerResourceCountGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "llmsafespace_controller_resource_count",
            Help: "Current count of resources managed by the controller",
        },
        []string{"resource"},
    )
)
```

## Error Handling and Recovery

The Sandbox Controller implements comprehensive error handling and recovery mechanisms:

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

The Sandbox Controller is a critical component of the LLMSafeSpace platform, responsible for managing the lifecycle of both sandbox environments and warm pools. By integrating these closely related functions into a single controller, we achieve better coordination, simplified architecture, and more efficient resource usage.

The controller's design follows Kubernetes best practices, including:

1. **Declarative API**: Using CRDs to define the desired state of resources
2. **Reconciliation Loop**: Continuously working to ensure the actual state matches the desired state
3. **Eventual Consistency**: Handling transient errors with retries and backoff
4. **Operator Pattern**: Encapsulating operational knowledge in the controller
5. **Defense in Depth**: Implementing multiple layers of security

The warm pool functionality significantly improves the user experience by reducing sandbox startup times. By maintaining pools of pre-initialized pods, the system can respond to sandbox creation requests much more quickly, which is particularly valuable for interactive use cases where users expect immediate feedback.

The enhanced security features in the controller ensure that sandbox environments are properly isolated and that warm pod recycling is done safely. The comprehensive validation of runtime environments and sandbox profiles ensures that only compatible and secure configurations are used.

The controller's integration with the API service provides a seamless experience for users, with efficient allocation of warm pods and real-time status updates. The robust error handling and metrics collection enable effective monitoring and troubleshooting of the system.

The volume management and network policy components provide fine-grained control over data persistence and network access, allowing for flexible yet secure sandbox configurations. The graceful shutdown procedures ensure that resources are properly cleaned up when the controller is terminated.

Overall, the Sandbox Controller approach provides a robust foundation for the LLMSafeSpace platform, enabling secure code execution for LLM agents while maintaining flexibility, performance, and ease of use. The design addresses all key requirements for a production-grade system, including security, scalability, observability, and reliability.
