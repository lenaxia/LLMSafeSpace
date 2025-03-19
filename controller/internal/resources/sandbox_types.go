package resources

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SandboxSpec defines the desired state of a Sandbox
type SandboxSpec struct {
	// Runtime environment (e.g., python:3.10)
	Runtime string `json:"runtime"`
	
	// Security level for the sandbox
	// +kubebuilder:validation:Enum=standard;high;custom
	// +kubebuilder:default=standard
	SecurityLevel string `json:"securityLevel,omitempty"`
	
	// Timeout in seconds for sandbox operations
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3600
	// +kubebuilder:default=300
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
	// +kubebuilder:validation:Pattern=^([0-9]+m|[0-9]+\.[0-9]+)$
	// +kubebuilder:default="500m"
	CPU string `json:"cpu,omitempty"`
	
	// Memory resource limit
	// +kubebuilder:validation:Pattern=^[0-9]+(Ki|Mi|Gi)$
	// +kubebuilder:default="512Mi"
	Memory string `json:"memory,omitempty"`
	
	// Ephemeral storage limit
	// +kubebuilder:validation:Pattern=^[0-9]+(Ki|Mi|Gi)$
	// +kubebuilder:default="1Gi"
	EphemeralStorage string `json:"ephemeralStorage,omitempty"`
	
	// Enable CPU pinning for sensitive workloads
	CPUPinning bool `json:"cpuPinning,omitempty"`
}

// NetworkAccess defines network access configuration
type NetworkAccess struct {
	// Egress rules
	Egress []EgressRule `json:"egress,omitempty"`
	
	// Allow ingress traffic to sandbox
	// +kubebuilder:default=false
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
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int `json:"port"`
	
	// Protocol (TCP or UDP)
	// +kubebuilder:validation:Enum=TCP;UDP
	// +kubebuilder:default="TCP"
	Protocol string `json:"protocol,omitempty"`
}

// FilesystemConfig defines filesystem configuration
type FilesystemConfig struct {
	// Mount root filesystem as read-only
	// +kubebuilder:default=true
	ReadOnlyRoot bool `json:"readOnlyRoot,omitempty"`
	
	// Paths that should be writable
	// +kubebuilder:default={"/tmp","/workspace"}
	WritablePaths []string `json:"writablePaths,omitempty"`
}

// StorageConfig defines storage configuration
type StorageConfig struct {
	// Enable persistent storage
	// +kubebuilder:default=false
	Persistent bool `json:"persistent,omitempty"`
	
	// Size of persistent volume
	// +kubebuilder:validation:Pattern=^[0-9]+(Ki|Mi|Gi)$
	// +kubebuilder:default="5Gi"
	VolumeSize string `json:"volumeSize,omitempty"`
}

// SecurityContext defines security context configuration
type SecurityContext struct {
	// User ID to run container processes
	// +kubebuilder:default=1000
	RunAsUser int64 `json:"runAsUser,omitempty"`
	
	// Group ID to run container processes
	// +kubebuilder:default=1000
	RunAsGroup int64 `json:"runAsGroup,omitempty"`
	
	// Seccomp profile configuration
	SeccompProfile *SeccompProfile `json:"seccompProfile,omitempty"`
}

// SeccompProfile defines seccomp profile configuration
type SeccompProfile struct {
	// Type of seccomp profile
	// +kubebuilder:validation:Enum=RuntimeDefault;Localhost
	// +kubebuilder:default="RuntimeDefault"
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
	// +kubebuilder:validation:Enum=Pending;Creating;Running;Terminating;Terminated;Failed
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
}

// SandboxCondition defines a condition of the sandbox
type SandboxCondition struct {
	// Type of condition
	Type string `json:"type"`
	
	// Status of the condition (True, False, Unknown)
	// +kubebuilder:validation:Enum=True;False;Unknown
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

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Runtime",type="string",JSONPath=".spec.runtime"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Sandbox is the Schema for the sandboxes API
type Sandbox struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxSpec   `json:"spec,omitempty"`
	Status SandboxStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SandboxList contains a list of Sandbox
type SandboxList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Sandbox `json:"items"`
}
