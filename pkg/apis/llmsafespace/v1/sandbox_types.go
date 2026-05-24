package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SandboxSpec defines the desired state of a Sandbox.
type SandboxSpec struct {
	// Runtime environment (e.g., python:3.10).
	Runtime string `json:"runtime"`

	// Security level for the sandbox.
	// +kubebuilder:validation:Enum=standard;high;custom
	// +kubebuilder:default=standard
	SecurityLevel string `json:"securityLevel,omitempty"`

	// Timeout in seconds for sandbox operations.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3600
	// +kubebuilder:default=300
	Timeout int `json:"timeout,omitempty"`

	// Resources defines compute resource requirements.
	Resources *ResourceRequirements `json:"resources,omitempty"`

	// NetworkAccess defines network access configuration.
	NetworkAccess *NetworkAccess `json:"networkAccess,omitempty"`

	// Filesystem defines filesystem configuration.
	Filesystem *FilesystemConfig `json:"filesystem,omitempty"`

	// Storage defines persistent storage configuration.
	Storage *StorageConfig `json:"storage,omitempty"`

	// SecurityContext defines security context configuration for the pod.
	SecurityContext *SecurityContext `json:"securityContext,omitempty"`

	// ProfileRef references a SandboxProfile.
	ProfileRef *ProfileReference `json:"profileRef,omitempty"`

	// WorkspaceRef is the name of the Workspace this sandbox is attached to.
	WorkspaceRef string `json:"workspaceRef,omitempty"`

	// RestartGeneration is incremented by the API service when a user
	// requests a sandbox restart (POST /sandboxes/:id/restart) or when
	// credential rotation triggers an automatic restart (fix #3). The
	// controller compares this to Status.ObservedRestartGeneration; when
	// spec > status, it gracefully deletes the pod and reverts to Pending.
	RestartGeneration int64 `json:"restartGeneration,omitempty"`
}

// ResourceRequirements defines compute resource requirements.
type ResourceRequirements struct {
	// CPU resource limit.
	// +kubebuilder:validation:Pattern=^([0-9]+m|[0-9]+\.[0-9]+)$
	// +kubebuilder:default="500m"
	CPU string `json:"cpu,omitempty"`

	// Memory resource limit.
	// +kubebuilder:validation:Pattern=^[0-9]+(Ki|Mi|Gi)$
	// +kubebuilder:default="512Mi"
	Memory string `json:"memory,omitempty"`

	// EphemeralStorage limit.
	// +kubebuilder:validation:Pattern=^[0-9]+(Ki|Mi|Gi)$
	// +kubebuilder:default="1Gi"
	EphemeralStorage string `json:"ephemeralStorage,omitempty"`

	// CPUPinning enables CPU pinning for sensitive workloads.
	CPUPinning bool `json:"cpuPinning,omitempty"`
}

// NetworkAccess defines network access configuration.
type NetworkAccess struct {
	// Egress rules.
	Egress []EgressRule `json:"egress,omitempty"`

	// Ingress allows ingress traffic to the sandbox when true.
	// +kubebuilder:default=false
	Ingress bool `json:"ingress,omitempty"`
}

// EgressRule defines an egress rule.
type EgressRule struct {
	// Domain name for egress filtering.
	Domain string `json:"domain"`

	// Ports allowed for this domain.
	Ports []PortRule `json:"ports,omitempty"`
}

// PortRule defines a port rule.
type PortRule struct {
	// Port number.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int `json:"port"`

	// Protocol (TCP or UDP).
	// +kubebuilder:validation:Enum=TCP;UDP
	// +kubebuilder:default="TCP"
	Protocol string `json:"protocol,omitempty"`
}

// FilesystemConfig defines filesystem configuration.
type FilesystemConfig struct {
	// ReadOnlyRoot mounts the root filesystem as read-only.
	// +kubebuilder:default=true
	ReadOnlyRoot bool `json:"readOnlyRoot,omitempty"`

	// WritablePaths lists paths that should be writable.
	// +kubebuilder:default={"/tmp","/workspace"}
	WritablePaths []string `json:"writablePaths,omitempty"`
}

// StorageConfig defines storage configuration.
type StorageConfig struct {
	// Persistent enables persistent storage.
	// +kubebuilder:default=false
	Persistent bool `json:"persistent,omitempty"`

	// VolumeSize is the size of the persistent volume.
	// +kubebuilder:validation:Pattern=^[0-9]+(Ki|Mi|Gi)$
	// +kubebuilder:default="5Gi"
	VolumeSize string `json:"volumeSize,omitempty"`
}

// SecurityContext defines security context configuration.
type SecurityContext struct {
	// RunAsUser is the UID to run container processes as.
	// +kubebuilder:default=1000
	RunAsUser int64 `json:"runAsUser,omitempty"`

	// RunAsGroup is the GID to run container processes as.
	// +kubebuilder:default=1000
	RunAsGroup int64 `json:"runAsGroup,omitempty"`

	// SeccompProfile configures the seccomp profile.
	SeccompProfile *SeccompProfile `json:"seccompProfile,omitempty"`
}

// SeccompProfile defines seccomp profile configuration.
type SeccompProfile struct {
	// Type of seccomp profile.
	// +kubebuilder:validation:Enum=RuntimeDefault;Localhost
	// +kubebuilder:default="RuntimeDefault"
	Type string `json:"type"`

	// LocalhostProfile is the path to the seccomp profile on the node.
	LocalhostProfile string `json:"localhostProfile,omitempty"`
}

// ProfileReference references a SandboxProfile.
type ProfileReference struct {
	// Name of the SandboxProfile.
	Name string `json:"name"`

	// Namespace of the SandboxProfile.
	Namespace string `json:"namespace,omitempty"`
}

// SandboxStatus defines the observed state of a Sandbox.
type SandboxStatus struct {
	// Phase is the current lifecycle phase of the sandbox.
	// +kubebuilder:validation:Enum=Pending;Creating;Running;Suspending;Suspended;Resuming;Terminating;Terminated;Failed
	Phase string `json:"phase,omitempty"`

	// Conditions of the sandbox.
	Conditions []SandboxCondition `json:"conditions,omitempty"`

	// PodName of the pod running the sandbox.
	PodName string `json:"podName,omitempty"`

	// PodNamespace of the pod running the sandbox.
	PodNamespace string `json:"podNamespace,omitempty"`

	// StartTime is when the sandbox was started.
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// Endpoint is the internal endpoint for the sandbox.
	Endpoint string `json:"endpoint,omitempty"`

	// Resources is the resource usage information.
	Resources *ResourceStatus `json:"resources,omitempty"`

	// PodIP is the IP address of the running sandbox pod.
	PodIP string `json:"podIP,omitempty"`

	// LastActivityAt is the timestamp of the most recent API activity.
	// Updated by the API server (not the controller).
	LastActivityAt *metav1.Time `json:"lastActivityAt,omitempty"`

	// RestartCount is the cumulative number of pod restarts the controller
	// has performed on this sandbox over its lifetime, regardless of cause
	// (transient pod-loss recovery, user-initiated restart, credential
	// rotation, etc). Never resets while the sandbox exists. Surfaced as a
	// metric and as a debugging aid; clients should not key behaviour on
	// the absolute value.
	RestartCount int32 `json:"restartCount,omitempty"`

	// TransientFailureCount is the running count of consecutive transient
	// pod-loss events that the controller has self-healed by reverting to
	// Pending (see design/SANDBOX-LIFECYCLE.md §4.1, fix #2). It increments
	// each time the controller observes "pod missing while phase=Running"
	// and decides to retry instead of failing. It resets to 0 once the
	// sandbox stays in Running for the recovery-stable window.
	//
	// When this counter reaches MaxTransientFailures (3), the next pod-loss
	// event marks the sandbox Failed terminally; recovery requires
	// POST /sandboxes/:id/retry (fix #5).
	TransientFailureCount int32 `json:"transientFailureCount,omitempty"`

	// LastTransientFailureAt is the wall-clock time of the most recent
	// transient pod-loss recovery. Used by the reset-on-stable-Running
	// logic to compute "how long has the pod been healthy since the last
	// transient event". Nil when no transient failure has occurred.
	LastTransientFailureAt *metav1.Time `json:"lastTransientFailureAt,omitempty"`

	// ObservedRestartGeneration is the last Spec.RestartGeneration value
	// the controller has acted on. When Spec.RestartGeneration >
	// Status.ObservedRestartGeneration, the controller gracefully deletes
	// the pod and reverts to Pending for recreation.
	ObservedRestartGeneration int64 `json:"observedRestartGeneration,omitempty"`
}

// SandboxCondition is a condition of a sandbox.
type SandboxCondition struct {
	// Type of condition.
	Type string `json:"type"`

	// Status of the condition (True, False, Unknown).
	// +kubebuilder:validation:Enum=True;False;Unknown
	Status string `json:"status"`

	// Reason for the condition.
	Reason string `json:"reason,omitempty"`

	// Message explaining the condition.
	Message string `json:"message,omitempty"`

	// LastTransitionTime is the last time the condition transitioned.
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}

// ResourceStatus defines resource usage status.
type ResourceStatus struct {
	// CPUUsage is the current CPU usage.
	CPUUsage string `json:"cpuUsage,omitempty"`

	// MemoryUsage is the current memory usage.
	MemoryUsage string `json:"memoryUsage,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=sb
// +kubebuilder:printcolumn:name="Runtime",type="string",JSONPath=".spec.runtime"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Sandbox is the Schema for the sandboxes API.
type Sandbox struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SandboxSpec   `json:"spec,omitempty"`
	Status SandboxStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SandboxList contains a list of Sandbox.
type SandboxList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Sandbox `json:"items"`
}
