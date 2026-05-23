package webhooks

import (
	"context"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// +kubebuilder:webhook:path=/validate-llmsafespace-dev-v1-sandboxprofile,mutating=false,failurePolicy=fail,groups=llmsafespace.dev,resources=sandboxprofiles,verbs=create;update,versions=v1,name=vsandboxprofile.kb.io,sideEffects=None,admissionReviewVersions=v1

// SandboxProfileValidator validates SandboxProfile resources.
//
// The Decoder MUST be set at construction time (controller-runtime v0.15+
// removed the InjectDecoder DI callback). A nil Decoder causes Handle to
// panic with nil-pointer-deref on every admission request.
type SandboxProfileValidator struct {
	Decoder admission.Decoder
}

// Handle validates the SandboxProfile resource.
func (v *SandboxProfileValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	profile := &v1.SandboxProfile{}
	if err := v.Decoder.Decode(req, profile); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if profile.Spec.Language == "" {
		return admission.Denied("language is required")
	}

	for _, policy := range profile.Spec.NetworkPolicies {
		if policy.Type != "egress" && policy.Type != "ingress" {
			return admission.Denied("network policy type must be either 'egress' or 'ingress'")
		}
	}

	return admission.Allowed("sandbox profile is valid")
}

// InjectDecoder retained for backwards compatibility (see SandboxValidator).
func (v *SandboxProfileValidator) InjectDecoder(d admission.Decoder) error {
	v.Decoder = d
	return nil
}
