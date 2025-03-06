package resources

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WarmPoolSpec defines the desired state of a WarmPool
type WarmPoolSpec struct {
	// Runtime environment (e.g., python:3.10)
	Runtime string `json:"runtime"`
	
	// Minimum number of warm pods to maintain
	// +kubebuilder:validation:Minimum=0
	MinSize int `json:"minSize"`
	
	// Maximum number of warm pods to maintain (0 for unlimited)
	// +kubebuilder:validation:Minimum=0
	MaxSize int `json:"maxSize,omitempty"`
	
	// Security level for warm pods
	// +kubebuilder:validation:Enum=standard;high;custom
	// +kubebuilder:default=standard
	SecurityLevel string `json:"securityLevel,omitempty"`
	
	// Time-to-live for unused warm pods in seconds (0 for no expiry)
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=3600
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
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`
	
	// Target utilization percentage
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=80
	TargetUtilization int `json:"targetUtilization,omitempty"`
	
	// Seconds to wait before scaling down
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=300
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
	// +kubebuilder:validation:Enum=True;False;Unknown
	Status string `json:"status"`
	
	// Reason for the condition
	Reason string `json:"reason,omitempty"`
	
	// Message explaining the condition
	Message string `json:"message,omitempty"`
	
	// Last time the condition transitioned
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Runtime",type="string",JSONPath=".spec.runtime"
// +kubebuilder:printcolumn:name="Available",type="integer",JSONPath=".status.availablePods"
// +kubebuilder:printcolumn:name="Assigned",type="integer",JSONPath=".status.assignedPods"
// +kubebuilder:printcolumn:name="Pending",type="integer",JSONPath=".status.pendingPods"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// WarmPool is the Schema for the warmpools API
type WarmPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WarmPoolSpec   `json:"spec,omitempty"`
	Status WarmPoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WarmPoolList contains a list of WarmPool
type WarmPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WarmPool `json:"items"`
}
