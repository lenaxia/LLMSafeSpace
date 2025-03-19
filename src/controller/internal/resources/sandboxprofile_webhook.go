package resources

import (
	"context"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/validate-llmsafespace-dev-v1-sandboxprofile,mutating=false,failurePolicy=fail,groups=llmsafespace.dev,resources=sandboxprofiles,verbs=create;update,versions=v1,name=vsandboxprofile.kb.io,sideEffects=None,admissionReviewVersions=v1

// SandboxProfileValidator validates SandboxProfile resources
type SandboxProfileValidator struct {
	decoder *admission.Decoder
}

// Handle validates the SandboxProfile resource
func (v *SandboxProfileValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	profile := &SandboxProfile{}
	
	err := v.decoder.Decode(req, profile)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	
	// Validate language exists
	if profile.Spec.Language == "" {
		return admission.Denied("language is required")
	}
	
	// Validate network policies
	for _, policy := range profile.Spec.NetworkPolicies {
		if policy.Type != "egress" && policy.Type != "ingress" {
			return admission.Denied("network policy type must be either 'egress' or 'ingress'")
		}
	}
	
	return admission.Allowed("sandbox profile is valid")
}

// InjectDecoder injects the decoder
func (v *SandboxProfileValidator) InjectDecoder(d *admission.Decoder) error {
	v.decoder = d
	return nil
}
