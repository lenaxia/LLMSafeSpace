package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RuntimeEnvironmentSpec defines the desired state of a RuntimeEnvironment.
type RuntimeEnvironmentSpec struct {
	// Image is the container image for this runtime.
	Image string `json:"image"`

	// Language is the programming language (e.g., python, nodejs).
	Language string `json:"language"`

	// Version of the language runtime.
	Version string `json:"version,omitempty"`

	// Tags for categorizing runtimes.
	Tags []string `json:"tags,omitempty"`

	// PreInstalledPackages installed in this runtime image.
	PreInstalledPackages []string `json:"preInstalledPackages,omitempty"`

	// PackageManager is the default package manager (e.g., pip, npm).
	PackageManager string `json:"packageManager,omitempty"`

	// SecurityFeatures supported by this runtime.
	SecurityFeatures []string `json:"securityFeatures,omitempty"`

	// ResourceRequirements describes the recommended resource requirements
	// for this runtime.
	ResourceRequirements *RuntimeResourceRequirements `json:"resourceRequirements,omitempty"`

	// RequiresCredentials indicates that this runtime needs LLM provider
	// credentials to function. When true, sandbox creation rejects requests
	// where the workspace has no credential secret set.
	RequiresCredentials bool `json:"requiresCredentials,omitempty"`
}

// RuntimeResourceRequirements defines resource requirements for a runtime.
type RuntimeResourceRequirements struct {
	// MinCPU is the minimum CPU requirement.
	MinCPU string `json:"minCpu,omitempty"`

	// MinMemory is the minimum memory requirement.
	MinMemory string `json:"minMemory,omitempty"`

	// RecommendedCPU is the recommended CPU.
	RecommendedCPU string `json:"recommendedCpu,omitempty"`

	// RecommendedMemory is the recommended memory.
	RecommendedMemory string `json:"recommendedMemory,omitempty"`
}

// RuntimeEnvironmentStatus defines the observed state of a RuntimeEnvironment.
type RuntimeEnvironmentStatus struct {
	// Available indicates whether this runtime is available for use.
	Available bool `json:"available,omitempty"`

	// LastValidated is the last time this runtime was validated.
	LastValidated *metav1.Time `json:"lastValidated,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=rte
// +kubebuilder:printcolumn:name="Language",type="string",JSONPath=".spec.language"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".spec.version"
// +kubebuilder:printcolumn:name="Available",type="boolean",JSONPath=".status.available"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// RuntimeEnvironment is the Schema for the runtimeenvironments API.
type RuntimeEnvironment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RuntimeEnvironmentSpec   `json:"spec,omitempty"`
	Status RuntimeEnvironmentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RuntimeEnvironmentList contains a list of RuntimeEnvironment.
type RuntimeEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RuntimeEnvironment `json:"items"`
}
