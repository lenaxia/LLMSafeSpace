# Sandbox Controller Design for SecureAgent

## Overview

The Sandbox Controller is a critical component of the SecureAgent platform, responsible for managing the lifecycle of sandbox environments. This document provides a detailed design for the controller, including Custom Resource Definitions (CRDs), reconciliation loops, and resource lifecycle management.

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

### 3. RuntimeEnvironment CRD

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

3. **Resource Managers**
   - PodManager
   - NetworkPolicyManager
   - ServiceManager
   - VolumeManager
   - SecurityContextManager

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

### 1. Sandbox Reconciliation Loop

The Sandbox reconciliation loop is the core of the controller, responsible for ensuring that the actual state of sandbox resources matches the desired state defined in the Sandbox CR.

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

### 2. SandboxProfile Reconciliation Loop

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

### 3. RuntimeEnvironment Reconciliation Loop

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

## Resource Lifecycle Management

### 1. Sandbox Resource Creation

The controller creates the following resources for each Sandbox:

1. **Namespace** (optional, for high isolation)
2. **Pod** for the sandbox execution environment
3. **Service** for internal communication
4. **NetworkPolicy** for network isolation
5. **PersistentVolumeClaim** (if persistent storage is enabled)
6. **ServiceAccount** with appropriate permissions

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

## Monitoring and Metrics

The controller exposes Prometheus metrics for monitoring its operation and the state of managed resources.

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
)
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

The Sandbox Controller is a critical component of the SecureAgent platform, responsible for managing the lifecycle of sandbox environments. It leverages Kubernetes custom resources and controllers to provide a secure, isolated environment for executing code.

The controller's design follows Kubernetes best practices, including:

1. **Declarative API**: Using CRDs to define the desired state of sandboxes
2. **Reconciliation Loop**: Continuously working to ensure the actual state matches the desired state
3. **Eventual Consistency**: Handling transient errors with retries and backoff
4. **Operator Pattern**: Encapsulating operational knowledge in the controller
5. **Defense in Depth**: Implementing multiple layers of security

This design provides a robust foundation for the SecureAgent platform, enabling secure code execution for LLM agents while maintaining flexibility and ease of use.
