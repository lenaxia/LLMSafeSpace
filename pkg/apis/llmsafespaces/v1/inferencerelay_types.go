// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InferenceRelayConditionType identifies a condition on an InferenceRelay.
type InferenceRelayConditionType string

const (
	InferenceRelayConditionReady              InferenceRelayConditionType = "Ready"
	InferenceRelayConditionDegraded           InferenceRelayConditionType = "Degraded"
	InferenceRelayConditionProvisioningFailed InferenceRelayConditionType = "ProvisioningFailed"
	InferenceRelayConditionRotating           InferenceRelayConditionType = "Rotating"
	InferenceRelayConditionFallbackActive     InferenceRelayConditionType = "FallbackActive"
)

// RelayInstanceState represents the lifecycle state of a relay VM.
type RelayInstanceState string

const (
	RelayStateProvisioning       RelayInstanceState = "provisioning"
	RelayStateHealthy            RelayInstanceState = "healthy"
	RelayStateDraining           RelayInstanceState = "draining"
	RelayStateUnhealthy          RelayInstanceState = "unhealthy"
	RelayStateQuotaExhausted     RelayInstanceState = "quota-exhausted"
	RelayStateTerminated         RelayInstanceState = "terminated"
	RelayStateProvisioningFailed RelayInstanceState = "provisioning-failed"
)

// FallbackConfig configures direct-to-upstream routing when all relay VMs
// are unhealthy. Rate-limited to avoid worsening IP throttling.
type FallbackConfig struct {
	// Enabled enables direct fallback when all relays are down.
	// If false, the router returns 502 to all requests when no relays are healthy.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// Rate is the maximum request rate to the upstream in fallback mode
	// (requests per second, global across all workspaces).
	// +kubebuilder:default=0.5
	Rate float64 `json:"rate,omitempty"`

	// MaxConcurrent is the maximum in-flight requests to the upstream
	// in fallback mode.
	// +kubebuilder:default=1
	MaxConcurrent int `json:"maxConcurrent,omitempty"`
}

// RelayProviderSpec configures a single cloud provider relay VM.
type RelayProviderSpec struct {
	// Provider is the cloud provider name.
	// +kubebuilder:validation:Enum=aws;oci;gcp
	Provider string `json:"provider"`

	// Region is the provider region (e.g. "us-east-1", "us-ashburn-1", "us-central1-a").
	// AWS: any region (t4g.micro available globally).
	// OCI: must be the tenancy home region for Always Free eligibility.
	// GCP: must be us-west1, us-central1, or us-east1 for Always Free eligibility.
	Region string `json:"region"`

	// CredentialsRef references a K8s Secret containing provider credentials.
	// Must be in the controller's namespace. The validating webhook checks
	// that the Secret exists and contains the required keys:
	//   aws: accessKeyId, secretAccessKey, region
	//   oci: tenancy, user, fingerprint, key, region
	//   gcp: service-account-json
	// +kubebuilder:validation:MinLength=1
	CredentialsRef corev1.LocalObjectReference `json:"credentialsRef"`

	// Shape overrides the default shape.
	//   aws default: t4g.micro (2 vCPU Graviton2, 1 GB, Arm64 — paid ~$7/mo)
	//   oci default: VM.Standard.A1.Flex (2 OCPU, 12 GB, Arm)
	//   gcp default: e2-micro (0.25 shared vCPU, 1 GB)
	// +optional
	Shape string `json:"shape,omitempty"`
}

// HealthCheckConfig configures active health-checking of relay VMs.
type HealthCheckConfig struct {
	// Interval between health checks per relay VM.
	// +kubebuilder:default="15s"
	Interval metav1.Duration `json:"interval,omitempty"`

	// Health check request timeout.
	// +kubebuilder:default="5s"
	Timeout metav1.Duration `json:"timeout,omitempty"`

	// Consecutive failures before marking unhealthy.
	// +kubebuilder:default=3
	UnhealthyThreshold int `json:"unhealthyThreshold,omitempty"`

	// Time to stay unhealthy before destroy + reprovision.
	// +kubebuilder:default="15m"
	ReplacementTimeout metav1.Duration `json:"replacementTimeout,omitempty"`
}

// RotationConfig configures automatic destroy-and-recreate on 429 detection.
type RotationConfig struct {
	// Enabled enables destroy-and-recreate when the router detects 429 storms.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// Max429Rate is the 429 fraction (of total responses) that triggers rotation.
	// +kubebuilder:default=0.5
	Max429Rate float64 `json:"max429Rate,omitempty"`

	// DetectionWindow is the rolling window for counting 429s.
	// +kubebuilder:default="5m"
	DetectionWindow metav1.Duration `json:"detectionWindow,omitempty"`

	// Cooldown is the minimum time between rotations on the same provider slot.
	// +kubebuilder:default="30m"
	Cooldown metav1.Duration `json:"cooldown,omitempty"`
}

// InferenceRelaySpec defines the desired state of an InferenceRelay.
type InferenceRelaySpec struct {
	// UpstreamURL is the LLM provider endpoint the relays proxy to.
	// Defaults to opencode.ai/zen/v1 so a default deploy produces working
	// free-model inference out of the box via the anonymous `public` key
	// (which authorizes inference for any model Zen flags `allowAnonymous` —
	// verified 2026-06-20, worklog 0420 correction). Operators pointing the
	// fleet at a paid gateway or a non-Zen upstream should override this
	// (and configure upstreamAuth.keySecret if that upstream requires a real
	// key). The earlier default to ai.thekao.cloud (PR #298) was based on the
	// now-disproven A23 premise; reverted.
	// +kubebuilder:default="https://opencode.ai/zen/v1"
	UpstreamURL string `json:"upstreamURL"`

	// Providers configures the relay VMs. The default fleet is 1 AWS
	// (paid primary) + 1 OCI (free secondary). GCP can be added as an
	// optional paid provider for IP diversity.
	// +kubebuilder:validation:MinItems=1
	Providers []RelayProviderSpec `json:"providers"`

	// HealthCheck configures active health-checking of relay VMs.
	HealthCheck HealthCheckConfig `json:"healthCheck,omitempty"`

	// Rotation configures automatic destroy-and-recreate on 429 detection.
	Rotation RotationConfig `json:"rotation,omitempty"`

	// Fallback configures direct-to-upstream routing when all relay VMs
	// are unhealthy. Rate-limited to avoid worsening IP throttling.
	Fallback FallbackConfig `json:"fallback,omitempty"`
}

// RelayInstanceStatus represents the observed state of a single relay VM.
type RelayInstanceStatus struct {
	ID                   string       `json:"id"`
	Provider             string       `json:"provider"`
	Region               string       `json:"region"`
	PublicIP             string       `json:"publicIP"`
	State                string       `json:"state"`
	Healthy              bool         `json:"healthy"`
	LastCheck            *metav1.Time `json:"lastCheck,omitempty"`
	Requests429          int          `json:"429Count,omitempty"`
	TotalRequests        int          `json:"totalRequests,omitempty"`
	EgressBytes          int64        `json:"egressBytes,omitempty"`
	ProvisioningAttempts int          `json:"provisioningAttempts,omitempty"`
	LastProvisionError   string       `json:"lastProvisionError,omitempty"`
}

// InferenceRelayStatus defines the observed state of an InferenceRelay.
type InferenceRelayStatus struct {
	// Instances is the observed state of all managed relay VMs.
	Instances []RelayInstanceStatus `json:"instances,omitempty"`

	// HealthyReplicas is the count of instances currently passing health checks.
	HealthyReplicas int `json:"healthyReplicas"`

	// Conditions reflects the overall relay fleet health.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastRotation is the time of the most recent destroy-and-recreate.
	LastRotation *metav1.Time `json:"lastRotation,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=irelay
// +kubebuilder:printcolumn:name="Healthy",type=integer,JSONPath=".status.healthyReplicas"
// +kubebuilder:printcolumn:name="Upstream",type=string,JSONPath=".spec.upstreamURL"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// InferenceRelay represents the managed relay VM fleet. The controller
// provisions, health-checks, and replaces relay VMs on AWS (paid primary),
// OCI (free secondary), and optionally GCP (paid tertiary) to maintain
// inference availability. Workspace pods route through the in-cluster
// relay-router, which distributes traffic across healthy relay VMs over
// HTTP with per-VM token auth (worklog 0442 — WireGuard removed).
type InferenceRelay struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InferenceRelaySpec   `json:"spec,omitempty"`
	Status InferenceRelayStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// InferenceRelayList contains a list of InferenceRelay.
type InferenceRelayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InferenceRelay `json:"items"`
}
