package resources

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SandboxProfileSpec defines the desired state of a SandboxProfile
type SandboxProfileSpec struct {
	// Target language for this profile
	Language string `json:"language"`
	
	// Base security level for this profile
	// +kubebuilder:validation:Enum=standard;high;custom
	// +kubebuilder:default=standard
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
	// +kubebuilder:validation:Enum=egress;ingress
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

// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Language",type="string",JSONPath=".spec.language"
// +kubebuilder:printcolumn:name="SecurityLevel",type="string",JSONPath=".spec.securityLevel"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// SandboxProfile is the Schema for the sandboxprofiles API
type SandboxProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SandboxProfileSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// SandboxProfileList contains a list of SandboxProfile
type SandboxProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxProfile `json:"items"`
}
