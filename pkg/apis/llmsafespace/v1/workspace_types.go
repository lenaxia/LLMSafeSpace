package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkspaceOwner identifies the user who owns a Workspace.
type WorkspaceOwner struct {
	// UserID is the owning user's ID.
	UserID string `json:"userID"`
}

// WorkspaceStorageConfig defines PVC configuration for a Workspace.
type WorkspaceStorageConfig struct {
	// Size is the PVC storage request (e.g. "10Gi").
	// +kubebuilder:validation:Pattern=^[0-9]+(Gi|Mi)$
	Size string `json:"size"`

	// StorageClassName is optional; empty uses the cluster default.
	StorageClassName string `json:"storageClassName,omitempty"`

	// AccessMode defaults to ReadWriteOnce.
	// +kubebuilder:validation:Enum=ReadWriteOnce;ReadWriteMany
	// +kubebuilder:default=ReadWriteOnce
	AccessMode string `json:"accessMode,omitempty"`
}

// WorkspacePackageSet defines runtime-specific packages installed on every pod start.
type WorkspacePackageSet struct {
	// Runtime selector (e.g. "python:3.11").
	Runtime string `json:"runtime"`

	// Requirements lists package identifiers (e.g. pip package names).
	Requirements []string `json:"requirements"`
}

// WorkspaceNetworkAccess defines network access rules for Workspace sandbox pods.
type WorkspaceNetworkAccess struct {
	// Egress defines outbound domain rules.
	Egress []WorkspaceEgressRule `json:"egress,omitempty"`

	// Ingress allows ingress traffic to sandbox pods when true.
	// +kubebuilder:default=false
	Ingress bool `json:"ingress,omitempty"`
}

// WorkspaceEgressRule defines an egress domain rule.
type WorkspaceEgressRule struct {
	// Domain name for egress filtering.
	Domain string `json:"domain"`
}

// WorkspaceAutoSuspend configures automatic workspace suspension after idle.
type WorkspaceAutoSuspend struct {
	// Enabled activates auto-suspension.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// IdleTimeoutSeconds is the idle period before suspension.
	// +kubebuilder:default=3600
	// +kubebuilder:validation:Minimum=1
	IdleTimeoutSeconds int64 `json:"idleTimeoutSeconds,omitempty"`
}

// WorkspaceCredentialRef refers to a Kubernetes Secret holding agent credentials.
type WorkspaceCredentialRef struct {
	// SecretName is the name of the Secret.
	SecretName string `json:"secretName"`
}

// WorkspaceSpec defines the desired state of a Workspace.
type WorkspaceSpec struct {
	// Owner is the user who owns this workspace.
	Owner WorkspaceOwner `json:"owner"`

	// DefaultRuntime is the default runtime environment (e.g. "python:3.11").
	DefaultRuntime string `json:"defaultRuntime,omitempty"`

	// SecurityLevel is standard or high.
	// +kubebuilder:validation:Enum=standard;high
	// +kubebuilder:default=standard
	SecurityLevel string `json:"securityLevel,omitempty"`

	// Storage defines PVC configuration.
	Storage WorkspaceStorageConfig `json:"storage"`

	// NetworkAccess defines egress and ingress rules for sandbox pods.
	NetworkAccess *WorkspaceNetworkAccess `json:"networkAccess,omitempty"`

	// AutoSuspend configures idle-based automatic suspension.
	AutoSuspend *WorkspaceAutoSuspend `json:"autoSuspend,omitempty"`

	// TTLSecondsAfterSuspended auto-deletes a suspended workspace after this
	// many seconds. 0 means never auto-delete.
	// +kubebuilder:default=0
	TTLSecondsAfterSuspended int64 `json:"ttlSecondsAfterSuspended,omitempty"`

	// Packages lists runtime-specific packages installed on every pod start.
	Packages []WorkspacePackageSet `json:"packages,omitempty"`

	// InitScript is a shell script run by the init container on every pod start.
	InitScript string `json:"initScript,omitempty"`

	// MaxActiveSessions is the maximum number of concurrent active sessions.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=20
	// +kubebuilder:default=5
	MaxActiveSessions int32 `json:"maxActiveSessions,omitempty"`

	// Credentials is an optional reference to a Secret holding agent credentials.
	Credentials *WorkspaceCredentialRef `json:"credentials,omitempty"`
}

// WorkspacePhase represents the lifecycle phase of a Workspace.
type WorkspacePhase string

const (
	WorkspacePhasePending     WorkspacePhase = "Pending"
	WorkspacePhaseActive      WorkspacePhase = "Active"
	WorkspacePhaseSuspending  WorkspacePhase = "Suspending"
	WorkspacePhaseSuspended   WorkspacePhase = "Suspended"
	WorkspacePhaseResuming    WorkspacePhase = "Resuming"
	WorkspacePhaseTerminating WorkspacePhase = "Terminating"
	WorkspacePhaseTerminated  WorkspacePhase = "Terminated"
	WorkspacePhaseFailed      WorkspacePhase = "Failed"
)

// WorkspaceConditionType identifies a condition on a Workspace.
type WorkspaceConditionType string

const (
	WorkspaceConditionReady     WorkspaceConditionType = "Ready"
	WorkspaceConditionPVCReady  WorkspaceConditionType = "PVCReady"
	WorkspaceConditionSuspended WorkspaceConditionType = "Suspended"
)

// WorkspaceCondition describes a condition of a Workspace.
//
// Status is a plain string (not corev1.ConditionStatus). The deployed YAML
// schema declares an enum of "True"/"False"/"Unknown"; using a plain string
// keeps this package free of the heavyweight k8s.io/api/core/v1 dependency
// without changing the JSON shape.
type WorkspaceCondition struct {
	// Type of condition.
	Type WorkspaceConditionType `json:"type"`

	// Status of the condition (True, False, Unknown).
	// +kubebuilder:validation:Enum=True;False;Unknown
	Status string `json:"status"`

	// LastTransitionTime is the last time the condition transitioned.
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`

	// Reason for the condition.
	Reason string `json:"reason,omitempty"`

	// Message explaining the condition.
	Message string `json:"message,omitempty"`
}

// WorkspaceStatus defines the observed state of a Workspace.
type WorkspaceStatus struct {
	// Phase is the current lifecycle phase.
	Phase WorkspacePhase `json:"phase,omitempty"`

	// PVCName is the name of the bound PersistentVolumeClaim.
	PVCName string `json:"pvcName,omitempty"`

	// ActiveSessions is the count of active sandbox sessions.
	ActiveSessions int32 `json:"activeSessions,omitempty"`

	// LastActivityAt is the timestamp of the most recent activity.
	LastActivityAt *metav1.Time `json:"lastActivityAt,omitempty"`

	// SuspendedAt is the timestamp when the workspace was suspended.
	SuspendedAt *metav1.Time `json:"suspendedAt,omitempty"`

	// Conditions of the workspace.
	Conditions []WorkspaceCondition `json:"conditions,omitempty"`

	// Message provides additional human-readable status detail.
	Message string `json:"message,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=ws
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
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
