package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SandboxProfileSpec defines the desired state of a SandboxProfile.
type SandboxProfileSpec struct {
	// Language targets a specific runtime language.
	Language string `json:"language"`

	// SecurityLevel is the base security level for this profile.
	// +kubebuilder:validation:Enum=standard;high;custom
	// +kubebuilder:default=standard
	SecurityLevel string `json:"securityLevel,omitempty"`

	// SeccompProfile is the path to the seccomp profile for this language.
	SeccompProfile string `json:"seccompProfile,omitempty"`

	// NetworkPolicies for this profile.
	NetworkPolicies []NetworkPolicy `json:"networkPolicies,omitempty"`

	// PreInstalledPackages in this profile.
	PreInstalledPackages []string `json:"preInstalledPackages,omitempty"`

	// ResourceDefaults for sandboxes using this profile.
	ResourceDefaults *ResourceDefaults `json:"resourceDefaults,omitempty"`

	// FilesystemConfig for sandboxes using this profile.
	FilesystemConfig *ProfileFilesystemConfig `json:"filesystemConfig,omitempty"`
}

// NetworkPolicy defines a network policy.
type NetworkPolicy struct {
	// Type of policy (egress or ingress).
	// +kubebuilder:validation:Enum=egress;ingress
	Type string `json:"type"`

	// Rules for this policy.
	Rules []NetworkRule `json:"rules,omitempty"`
}

// NetworkRule defines a network rule.
type NetworkRule struct {
	// Domain for this rule.
	Domain string `json:"domain,omitempty"`

	// CIDR for this rule.
	CIDR string `json:"cidr,omitempty"`

	// Ports for this rule.
	Ports []PortRule `json:"ports,omitempty"`
}

// ResourceDefaults defines default resource requirements.
type ResourceDefaults struct {
	// CPU resource limit default.
	CPU string `json:"cpu,omitempty"`

	// Memory resource limit default.
	Memory string `json:"memory,omitempty"`

	// EphemeralStorage limit default.
	EphemeralStorage string `json:"ephemeralStorage,omitempty"`
}

// ProfileFilesystemConfig defines filesystem configuration for a profile.
type ProfileFilesystemConfig struct {
	// ReadOnlyPaths lists paths that should be read-only.
	ReadOnlyPaths []string `json:"readOnlyPaths,omitempty"`

	// WritablePaths lists paths that should be writable.
	WritablePaths []string `json:"writablePaths,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=sbp
// +kubebuilder:printcolumn:name="Language",type="string",JSONPath=".spec.language"
// +kubebuilder:printcolumn:name="SecurityLevel",type="string",JSONPath=".spec.securityLevel"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// SandboxProfile is the Schema for the sandboxprofiles API.
type SandboxProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SandboxProfileSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// SandboxProfileList contains a list of SandboxProfile.
type SandboxProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxProfile `json:"items"`
}
