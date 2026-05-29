package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkspaceOwner identifies the user who owns a Workspace.
type WorkspaceOwner struct {
	UserID string `json:"userID"`
}

// WorkspaceStorageConfig defines PVC configuration for a Workspace.
type WorkspaceStorageConfig struct {
	// +kubebuilder:validation:Pattern=^[0-9]+(Gi|Mi)$
	Size             string `json:"size"`
	StorageClassName string `json:"storageClassName,omitempty"`
	// +kubebuilder:validation:Enum=ReadWriteOnce;ReadWriteMany
	// +kubebuilder:default=ReadWriteOnce
	AccessMode string `json:"accessMode,omitempty"`
}

// WorkspacePackageSet defines runtime-specific packages installed on every pod start.
type WorkspacePackageSet struct {
	Runtime      string   `json:"runtime"`
	Requirements []string `json:"requirements"`
}

// WorkspaceNetworkAccess defines network access rules for workspace pods.
type WorkspaceNetworkAccess struct {
	Egress []WorkspaceEgressRule `json:"egress,omitempty"`
	// +kubebuilder:default=false
	Ingress bool `json:"ingress,omitempty"`
}

// WorkspaceEgressRule defines an egress domain rule.
type WorkspaceEgressRule struct {
	Domain string `json:"domain"`
}

// WorkspaceAutoSuspend configures automatic workspace suspension after idle.
type WorkspaceAutoSuspend struct {
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
	// +kubebuilder:default=86400
	// +kubebuilder:validation:Minimum=1
	IdleTimeoutSeconds int64 `json:"idleTimeoutSeconds,omitempty"`
}

// WorkspaceCredentialRef refers to a Kubernetes Secret holding agent credentials.
type WorkspaceCredentialRef struct {
	SecretName string `json:"secretName"`
}

// PodSecurityContext defines security context for the workspace pod.
type PodSecurityContext struct {
	RunAsUser      int64  `json:"runAsUser,omitempty"`
	RunAsGroup     int64  `json:"runAsGroup,omitempty"`
	SeccompProfile string `json:"seccompProfile,omitempty"`
}

// ResourceRequirements defines compute resource requirements for the workspace pod.
type ResourceRequirements struct {
	// +kubebuilder:validation:Pattern=^([0-9]+m|[0-9]+\.[0-9]+)$
	// +kubebuilder:default="500m"
	CPU string `json:"cpu,omitempty"`
	// +kubebuilder:validation:Pattern=^[0-9]+(Ki|Mi|Gi)$
	// +kubebuilder:default="512Mi"
	Memory string `json:"memory,omitempty"`
	// +kubebuilder:validation:Pattern=^[0-9]+(Ki|Mi|Gi)$
	// +kubebuilder:default="1Gi"
	EphemeralStorage string `json:"ephemeralStorage,omitempty"`
	CPUPinning       bool   `json:"cpuPinning,omitempty"`
}

// WorkspaceSpec defines the desired state of a Workspace.
type WorkspaceSpec struct {
	Owner WorkspaceOwner `json:"owner"`

	// Runtime is the runtime environment (e.g. "python:3.11").
	Runtime string `json:"runtime"`

	// +kubebuilder:validation:Enum=standard;high
	// +kubebuilder:default=standard
	SecurityLevel string `json:"securityLevel,omitempty"`

	Storage       WorkspaceStorageConfig  `json:"storage"`
	NetworkAccess *WorkspaceNetworkAccess `json:"networkAccess,omitempty"`
	AutoSuspend   *WorkspaceAutoSuspend   `json:"autoSuspend,omitempty"`

	// +kubebuilder:default=0
	TTLSecondsAfterSuspended int64 `json:"ttlSecondsAfterSuspended,omitempty"`

	Packages   []WorkspacePackageSet `json:"packages,omitempty"`
	InitScript string                `json:"initScript,omitempty"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=20
	// +kubebuilder:default=5
	MaxActiveSessions int32 `json:"maxActiveSessions,omitempty"`

	Credentials *WorkspaceCredentialRef `json:"credentials,omitempty"`

	// Pod lifecycle fields (absorbed from Sandbox):

	// Timeout is the max pod lifetime in seconds. 0 = no limit.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=86400
	Timeout int `json:"timeout,omitempty"`

	Resources         *ResourceRequirements `json:"resources,omitempty"`
	RestartGeneration int64                 `json:"restartGeneration,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10
	// +kubebuilder:default=3
	MaxRetries int32 `json:"maxRetries,omitempty"`

	PodSecurityContext *PodSecurityContext `json:"podSecurityContext,omitempty"`

	// AutoApprovePermissions controls whether permission requests from the agent
	// are automatically approved without user interaction. When true, the backend
	// replies "always" to all permission.asked events. Default: false.
	// +kubebuilder:default=false
	AutoApprovePermissions bool `json:"autoApprovePermissions,omitempty"`
}

// WorkspacePhase represents the lifecycle phase of a Workspace.
type WorkspacePhase string

const (
	WorkspacePhasePending     WorkspacePhase = "Pending"
	WorkspacePhaseCreating    WorkspacePhase = "Creating"
	WorkspacePhaseActive      WorkspacePhase = "Active"
	WorkspacePhaseSuspending  WorkspacePhase = "Suspending"
	WorkspacePhaseSuspended   WorkspacePhase = "Suspended"
	WorkspacePhaseResuming    WorkspacePhase = "Resuming"
	WorkspacePhaseTerminating WorkspacePhase = "Terminating"
	WorkspacePhaseTerminated  WorkspacePhase = "Terminated"
	WorkspacePhaseFailed      WorkspacePhase = "Failed"
)

type PVCState string

const (
	PVCStateNone    PVCState = ""        // no PVC yet
	PVCStateCluster PVCState = "cluster" // PVC exists on cluster
	PVCStateS3      PVCState = "s3"      // PVC offloaded to S3
)

// WorkspaceConditionType identifies a condition on a Workspace.
type WorkspaceConditionType string

const (
	WorkspaceConditionReady                WorkspaceConditionType = "Ready"
	WorkspaceConditionPVCReady             WorkspaceConditionType = "PVCReady"
	WorkspaceConditionPodRunning           WorkspaceConditionType = "PodRunning"
	WorkspaceConditionSuspended            WorkspaceConditionType = "Suspended"
	WorkspaceConditionCredentialsAvailable WorkspaceConditionType = "CredentialsAvailable"
	WorkspaceConditionAgentHealthy         WorkspaceConditionType = "AgentHealthy"
)

const (
	ReasonCredentialsValid          = "CredentialsValid"
	ReasonCredentialSecretNotFound  = "CredentialSecretNotFound"
	ReasonCredentialEmpty           = "CredentialEmpty"
	ReasonCredentialInvalid         = "CredentialInvalid"
	ReasonCredentialCheckError      = "CredentialCheckError"
	ReasonCredentialValidationError = "CredentialValidationError"
)

const (
	ReasonAgentHealthy      = "AgentHealthy"
	ReasonAgentUnhealthy    = "AgentUnhealthy"
	ReasonAgentDegraded     = "AgentDegraded"
	ReasonHealthCheckFailed = "HealthCheckFailed"
)

// WorkspaceCondition describes a condition of a Workspace.
type WorkspaceCondition struct {
	Type WorkspaceConditionType `json:"type"`
	// +kubebuilder:validation:Enum=True;False;Unknown
	Status             string      `json:"status"`
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
	Reason             string      `json:"reason,omitempty"`
	Message            string      `json:"message,omitempty"`
}

// AgentSessionStatus describes a session reported by the workspace agent.
type AgentSessionStatus struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status"` // "idle" | "busy"
}

// WorkspaceStatus defines the observed state of a Workspace.
type WorkspaceStatus struct {
	Phase              WorkspacePhase       `json:"phase,omitempty"`
	PVCName            string               `json:"pvcName,omitempty"`
	ActiveSessions     int32                `json:"activeSessions,omitempty"`
	LastActivityAt     *metav1.Time         `json:"lastActivityAt,omitempty"`
	SuspendedAt        *metav1.Time         `json:"suspendedAt,omitempty"`
	Conditions         []WorkspaceCondition `json:"conditions,omitempty"`
	Message            string               `json:"message,omitempty"`
	ObservedGeneration int64                `json:"observedGeneration,omitempty"`

	// Pod status fields (absorbed from Sandbox):
	PodName                   string       `json:"podName,omitempty"`
	PodNamespace              string       `json:"podNamespace,omitempty"`
	PodIP                     string       `json:"podIP,omitempty"`
	Endpoint                  string       `json:"endpoint,omitempty"`
	StartTime                 *metav1.Time `json:"startTime,omitempty"`
	RestartCount              int32        `json:"restartCount,omitempty"`
	TransientFailureCount     int32        `json:"transientFailureCount,omitempty"`
	LastTransientFailureAt    *metav1.Time `json:"lastTransientFailureAt,omitempty"`
	ObservedRestartGeneration int64        `json:"observedRestartGeneration,omitempty"`
	CredentialSecretHash      string       `json:"credentialSecretHash,omitempty"`
	LastHealthCheckAt         *metav1.Time `json:"lastHealthCheckAt,omitempty"`
	ConsecutiveHealthFailures int32        `json:"consecutiveHealthFailures,omitempty"`

	// Agent-reported fields (populated from agentd /v1/statusz scrape):
	Sessions       []AgentSessionStatus `json:"sessions,omitempty"`
	DiskUsedBytes  int64                `json:"diskUsedBytes,omitempty"`
	DiskTotalBytes int64                `json:"diskTotalBytes,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=ws
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Runtime",type="string",JSONPath=".spec.runtime"
// +kubebuilder:printcolumn:name="Storage",type="string",JSONPath=".spec.storage.size"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Workspace is the Schema for the workspaces API.
type Workspace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkspaceSpec   `json:"spec,omitempty"`
	Status WorkspaceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WorkspaceList contains a list of Workspace.
type WorkspaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workspace `json:"items"`
}
