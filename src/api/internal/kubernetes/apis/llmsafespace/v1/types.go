package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Sandbox is the Schema for the sandboxes API
type Sandbox struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxSpec   `json:"spec,omitempty"`
	Status SandboxStatus `json:"status,omitempty"`
}

// SandboxSpec defines the desired state of a Sandbox
type SandboxSpec struct {
	// Runtime environment (e.g., python:3.10)
	Runtime string `json:"runtime"`
	
	// Security level for the sandbox
	SecurityLevel string `json:"securityLevel,omitempty"`
	
	// Timeout in seconds for sandbox operations
	Timeout int `json:"timeout,omitempty"`
	
	// Resource limits for the sandbox
	Resources *ResourceRequirements `json:"resources,omitempty"`
	
	// Network access configuration
	NetworkAccess *NetworkAccess `json:"networkAccess,omitempty"`
	
	// Filesystem configuration
	Filesystem *FilesystemConfig `json:"filesystem,omitempty"`
	
	// Storage configuration
	Storage *StorageConfig `json:"storage,omitempty"`
	
	// Security context configuration
	SecurityContext *SecurityContext `json:"securityContext,omitempty"`
	
	// Reference to a SandboxProfile
	ProfileRef *ProfileReference `json:"profileRef,omitempty"`
}

// ResourceRequirements defines compute resource requirements
type ResourceRequirements struct {
	// CPU resource limit
	CPU string `json:"cpu,omitempty"`
	
	// Memory resource limit
	Memory string `json:"memory,omitempty"`
	
	// Ephemeral storage limit
	EphemeralStorage string `json:"ephemeralStorage,omitempty"`
	
	// Enable CPU pinning for sensitive workloads
	CPUPinning bool `json:"cpuPinning,omitempty"`
}

// NetworkAccess defines network access configuration
type NetworkAccess struct {
	// Egress rules
	Egress []EgressRule `json:"egress,omitempty"`
	
	// Allow ingress traffic to sandbox
	Ingress bool `json:"ingress,omitempty"`
}

// EgressRule defines an egress rule
type EgressRule struct {
	// Domain name for egress filtering
	Domain string `json:"domain"`
	
	// Ports allowed for this domain
	Ports []PortRule `json:"ports,omitempty"`
}

// PortRule defines a port rule
type PortRule struct {
	// Port number
	Port int `json:"port"`
	
	// Protocol (TCP or UDP)
	Protocol string `json:"protocol,omitempty"`
}

// FilesystemConfig defines filesystem configuration
type FilesystemConfig struct {
	// Mount root filesystem as read-only
	ReadOnlyRoot bool `json:"readOnlyRoot,omitempty"`
	
	// Paths that should be writable
	WritablePaths []string `json:"writablePaths,omitempty"`
}

// StorageConfig defines storage configuration
type StorageConfig struct {
	// Enable persistent storage
	Persistent bool `json:"persistent,omitempty"`
	
	// Size of persistent volume
	VolumeSize string `json:"volumeSize,omitempty"`
}

// SecurityContext defines security context configuration
type SecurityContext struct {
	// User ID to run container processes
	RunAsUser int64 `json:"runAsUser,omitempty"`
	
	// Group ID to run container processes
	RunAsGroup int64 `json:"runAsGroup,omitempty"`
	
	// Seccomp profile configuration
	SeccompProfile *SeccompProfile `json:"seccompProfile,omitempty"`
}

// SeccompProfile defines seccomp profile configuration
type SeccompProfile struct {
	// Type of seccomp profile
	Type string `json:"type"`
	
	// Path to seccomp profile on node
	LocalhostProfile string `json:"localhostProfile,omitempty"`
}

// ProfileReference defines a reference to a SandboxProfile
type ProfileReference struct {
	// Name of SandboxProfile to use
	Name string `json:"name"`
	
	// Namespace of SandboxProfile
	Namespace string `json:"namespace,omitempty"`
}

// SandboxStatus defines the observed state of a Sandbox
type SandboxStatus struct {
	// Current phase of the sandbox
	Phase string `json:"phase,omitempty"`
	
	// Conditions for the sandbox
	Conditions []SandboxCondition `json:"conditions,omitempty"`
	
	// Name of the pod running the sandbox
	PodName string `json:"podName,omitempty"`
	
	// Namespace of the pod running the sandbox
	PodNamespace string `json:"podNamespace,omitempty"`
	
	// Time when the sandbox was started
	StartTime *metav1.Time `json:"startTime,omitempty"`
	
	// Internal endpoint for the sandbox
	Endpoint string `json:"endpoint,omitempty"`
	
	// Resource usage information
	Resources *ResourceStatus `json:"resources,omitempty"`
	
	// Reference to a warm pod if one was used
	WarmPodRef *WarmPodReference `json:"warmPodRef,omitempty"`
}

// SandboxCondition defines a condition of the sandbox
type SandboxCondition struct {
	// Type of condition
	Type string `json:"type"`
	
	// Status of the condition (True, False, Unknown)
	Status string `json:"status"`
	
	// Reason for the condition
	Reason string `json:"reason,omitempty"`
	
	// Message explaining the condition
	Message string `json:"message,omitempty"`
	
	// Last time the condition transitioned
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}

// ResourceStatus defines resource usage status
type ResourceStatus struct {
	// Current CPU usage
	CPUUsage string `json:"cpuUsage,omitempty"`
	
	// Current memory usage
	MemoryUsage string `json:"memoryUsage,omitempty"`
}

// WarmPodReference defines a reference to a WarmPod
type WarmPodReference struct {
	// Name of the WarmPod
	Name string `json:"name"`
	
	// Namespace of the WarmPod
	Namespace string `json:"namespace,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SandboxList contains a list of Sandbox
type SandboxList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Sandbox `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WarmPool is the Schema for the warmpools API
type WarmPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WarmPoolSpec   `json:"spec,omitempty"`
	Status WarmPoolStatus `json:"status,omitempty"`
}

// WarmPoolSpec defines the desired state of a WarmPool
type WarmPoolSpec struct {
	// Runtime environment (e.g., python:3.10)
	Runtime string `json:"runtime"`
	
	// Minimum number of warm pods to maintain
	MinSize int `json:"minSize"`
	
	// Maximum number of warm pods to maintain (0 for unlimited)
	MaxSize int `json:"maxSize,omitempty"`
	
	// Security level for warm pods
	SecurityLevel string `json:"securityLevel,omitempty"`
	
	// Time-to-live for unused warm pods in seconds (0 for no expiry)
	TTL int `json:"ttl,omitempty"`
	
	// Resource limits for warm pods
	Resources *ResourceRequirements `json:"resources,omitempty"`
	
	// Reference to a SandboxProfile
	ProfileRef *ProfileReference `json:"profileRef,omitempty"`
	
	// Packages to preinstall in warm pods
	PreloadPackages []string `json:"preloadPackages,omitempty"`
	
	// Scripts to run during pod initialization
	PreloadScripts []PreloadScript `json:"preloadScripts,omitempty"`
	
	// Auto-scaling configuration
	AutoScaling *AutoScalingConfig `json:"autoScaling,omitempty"`
}

// PreloadScript defines a script to run during pod initialization
type PreloadScript struct {
	// Name of the script
	Name string `json:"name"`
	
	// Content of the script
	Content string `json:"content"`
}

// AutoScalingConfig defines auto-scaling configuration
type AutoScalingConfig struct {
	// Enable auto-scaling
	Enabled bool `json:"enabled,omitempty"`
	
	// Target utilization percentage
	TargetUtilization int `json:"targetUtilization,omitempty"`
	
	// Seconds to wait before scaling down
	ScaleDownDelay int `json:"scaleDownDelay,omitempty"`
}

// WarmPoolStatus defines the observed state of a WarmPool
type WarmPoolStatus struct {
	// Number of warm pods available for immediate use
	AvailablePods int `json:"availablePods,omitempty"`
	
	// Number of warm pods currently assigned to sandboxes
	AssignedPods int `json:"assignedPods,omitempty"`
	
	// Number of warm pods being created
	PendingPods int `json:"pendingPods,omitempty"`
	
	// Last time the pool was scaled
	LastScaleTime *metav1.Time `json:"lastScaleTime,omitempty"`
	
	// Conditions for the warm pool
	Conditions []WarmPoolCondition `json:"conditions,omitempty"`
}

// WarmPoolCondition defines a condition of the warm pool
type WarmPoolCondition struct {
	// Type of condition
	Type string `json:"type"`
	
	// Status of the condition (True, False, Unknown)
	Status string `json:"status"`
	
	// Reason for the condition
	Reason string `json:"reason,omitempty"`
	
	// Message explaining the condition
	Message string `json:"message,omitempty"`
	
	// Last time the condition transitioned
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WarmPoolList contains a list of WarmPool
type WarmPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WarmPool `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WarmPod is the Schema for the warmpods API
type WarmPod struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WarmPodSpec   `json:"spec,omitempty"`
	Status WarmPodStatus `json:"status,omitempty"`
}

// WarmPodSpec defines the desired state of a WarmPod
type WarmPodSpec struct {
	// Reference to the WarmPool this pod belongs to
	PoolRef PoolReference `json:"poolRef"`
	
	// Time when this warm pod was created
	CreationTimestamp *metav1.Time `json:"creationTimestamp,omitempty"`
	
	// Last time the pod reported it was healthy
	LastHeartbeat *metav1.Time `json:"lastHeartbeat,omitempty"`
}

// PoolReference defines a reference to a WarmPool
type PoolReference struct {
	// Name of the WarmPool this pod belongs to
	Name string `json:"name"`
	
	// Namespace of the WarmPool
	Namespace string `json:"namespace,omitempty"`
}

// WarmPodStatus defines the observed state of a WarmPod
type WarmPodStatus struct {
	// Current phase of the warm pod
	Phase string `json:"phase,omitempty"`
	
	// Name of the underlying pod
	PodName string `json:"podName,omitempty"`
	
	// Namespace of the underlying pod
	PodNamespace string `json:"podNamespace,omitempty"`
	
	// ID of the sandbox this pod is assigned to (if any)
	AssignedTo string `json:"assignedTo,omitempty"`
	
	// Time when this pod was assigned to a sandbox
	AssignedAt *metav1.Time `json:"assignedAt,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WarmPodList contains a list of WarmPod
type WarmPodList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WarmPod `json:"items"`
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// RuntimeEnvironment is the Schema for the runtimeenvironments API
type RuntimeEnvironment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RuntimeEnvironmentSpec   `json:"spec,omitempty"`
	Status RuntimeEnvironmentStatus `json:"status,omitempty"`
}

// RuntimeEnvironmentSpec defines the desired state of a RuntimeEnvironment
type RuntimeEnvironmentSpec struct {
	// Container image for this runtime
	Image string `json:"image"`
	
	// Programming language (e.g., python, nodejs)
	Language string `json:"language"`
	
	// Version of the language runtime
	Version string `json:"version,omitempty"`
	
	// Tags for categorizing runtimes
	Tags []string `json:"tags,omitempty"`
	
	// Packages pre-installed in this runtime
	PreInstalledPackages []string `json:"preInstalledPackages,omitempty"`
	
	// Default package manager (e.g., pip, npm)
	PackageManager string `json:"packageManager,omitempty"`
	
	// Security features supported by this runtime
	SecurityFeatures []string `json:"securityFeatures,omitempty"`
	
	// Resource requirements for this runtime
	ResourceRequirements *RuntimeResourceRequirements `json:"resourceRequirements,omitempty"`
}

// RuntimeResourceRequirements defines resource requirements for a runtime
type RuntimeResourceRequirements struct {
	// Minimum CPU requirement
	MinCPU string `json:"minCpu,omitempty"`
	
	// Minimum memory requirement
	MinMemory string `json:"minMemory,omitempty"`
	
	// Recommended CPU requirement
	RecommendedCPU string `json:"recommendedCpu,omitempty"`
	
	// Recommended memory requirement
	RecommendedMemory string `json:"recommendedMemory,omitempty"`
}

// RuntimeEnvironmentStatus defines the observed state of a RuntimeEnvironment
type RuntimeEnvironmentStatus struct {
	// Whether this runtime is available
	Available bool `json:"available,omitempty"`
	
	// Last time this runtime was validated
	LastValidated *metav1.Time `json:"lastValidated,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// RuntimeEnvironmentList contains a list of RuntimeEnvironment
type RuntimeEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RuntimeEnvironment `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SandboxProfile is the Schema for the sandboxprofiles API
type SandboxProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SandboxProfileSpec `json:"spec,omitempty"`
}

// SandboxProfileSpec defines the desired state of a SandboxProfile
type SandboxProfileSpec struct {
	// Target language for this profile
	Language string `json:"language"`
	
	// Base security level for this profile
	SecurityLevel string `json:"securityLevel,omitempty"`
	
	// Path to seccomp profile for this language
	SeccompProfile string `json:"seccompProfile,omitempty"`
	
	// Network policies for this profile
	NetworkPolicies []NetworkPolicy `json:"networkPolicies,omitempty"`
	
	// Packages pre-installed in this profile
	PreInstalledPackages []string `json:"preInstalledPackages,omitempty"`
	
	// Default resource requirements
	ResourceDefaults *ResourceDefaults `json:"resourceDefaults,omitempty"`
	
	// Filesystem configuration
	FilesystemConfig *ProfileFilesystemConfig `json:"filesystemConfig,omitempty"`
}

// NetworkPolicy defines a network policy
type NetworkPolicy struct {
	// Type of policy (egress or ingress)
	Type string `json:"type"`
	
	// Rules for this policy
	Rules []NetworkRule `json:"rules,omitempty"`
}

// NetworkRule defines a network rule
type NetworkRule struct {
	// Domain for this rule
	Domain string `json:"domain,omitempty"`
	
	// CIDR for this rule
	CIDR string `json:"cidr,omitempty"`
	
	// Ports for this rule
	Ports []PortRule `json:"ports,omitempty"`
}

// ResourceDefaults defines default resource requirements
type ResourceDefaults struct {
	// Default CPU resource limit
	CPU string `json:"cpu,omitempty"`
	
	// Default memory resource limit
	Memory string `json:"memory,omitempty"`
	
	// Default ephemeral storage limit
	EphemeralStorage string `json:"ephemeralStorage,omitempty"`
}

// ProfileFilesystemConfig defines filesystem configuration for a profile
type ProfileFilesystemConfig struct {
	// Paths that should be read-only
	ReadOnlyPaths []string `json:"readOnlyPaths,omitempty"`
	
	// Paths that should be writable
	WritablePaths []string `json:"writablePaths,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SandboxProfileList contains a list of SandboxProfile
type SandboxProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxProfile `json:"items"`
}
