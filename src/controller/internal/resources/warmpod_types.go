package resources

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
	// +kubebuilder:validation:Enum=Pending;Ready;Assigned;Terminating
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

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Pool",type="string",JSONPath=".spec.poolRef.name"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Pod",type="string",JSONPath=".status.podName"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// WarmPod is the Schema for the warmpods API
type WarmPod struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WarmPodSpec   `json:"spec,omitempty"`
	Status WarmPodStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WarmPodList contains a list of WarmPod
type WarmPodList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WarmPod `json:"items"`
}
