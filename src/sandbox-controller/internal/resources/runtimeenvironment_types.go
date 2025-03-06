package resources

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:scope=Cluster
// +kubebuilder:printcolumn:name="Language",type="string",JSONPath=".spec.language"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".spec.version"
// +kubebuilder:printcolumn:name="Available",type="boolean",JSONPath=".status.available"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// RuntimeEnvironment is the Schema for the runtimeenvironments API
type RuntimeEnvironment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RuntimeEnvironmentSpec   `json:"spec,omitempty"`
	Status RuntimeEnvironmentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RuntimeEnvironmentList contains a list of RuntimeEnvironment
type RuntimeEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RuntimeEnvironment `json:"items"`
}
