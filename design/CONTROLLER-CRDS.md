# Custom Resource Definitions (CRDs)

## 1. Sandbox CRD

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

## 2. SandboxProfile CRD

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

## 3. WarmPool CRD

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

## 4. WarmPod CRD

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

## 5. RuntimeEnvironment CRD

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
