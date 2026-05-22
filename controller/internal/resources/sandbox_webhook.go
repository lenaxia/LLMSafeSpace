package resources

import (
	"context"
	"fmt"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/validate-llmsafespace-dev-v1-sandbox,mutating=false,failurePolicy=fail,groups=llmsafespace.dev,resources=sandboxes,verbs=create;update,versions=v1,name=vsandbox.kb.io,sideEffects=None,admissionReviewVersions=v1

// SandboxValidator validates Sandbox resources.
//
// The Decoder MUST be set at construction time (controller-runtime v0.15+
// removed the InjectDecoder dependency-injection callback). A nil Decoder
// causes Handle to panic with a nil-pointer-deref on every admission request.
type SandboxValidator struct {
	Client  client.Client
	Decoder admission.Decoder
}

// Handle validates the Sandbox resource
func (v *SandboxValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	sandbox := &Sandbox{}

	err := v.Decoder.Decode(req, sandbox)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Validate runtime exists
	if sandbox.Spec.Runtime == "" {
		return admission.Denied("runtime is required")
	}

	// Validate resource limits
	if sandbox.Spec.Resources != nil {
		// Add custom validation logic for resources
	}

	// Validate network access
	if sandbox.Spec.NetworkAccess != nil {
		for _, rule := range sandbox.Spec.NetworkAccess.Egress {
			if rule.Domain == "" {
				return admission.Denied("egress rule domain is required")
			}
		}
	}

	// Validate profile reference
	if sandbox.Spec.ProfileRef != nil {
		// Check if the referenced profile exists
		profileName := sandbox.Spec.ProfileRef.Name
		profileNamespace := sandbox.Spec.ProfileRef.Namespace
		if profileNamespace == "" {
			profileNamespace = req.Namespace
		}

		profile := &SandboxProfile{}
		err := v.Client.Get(ctx, client.ObjectKey{
			Namespace: profileNamespace,
			Name:      profileName,
		}, profile)

		if err != nil {
			return admission.Denied(fmt.Sprintf("referenced profile %s/%s not found", profileNamespace, profileName))
		}
	}

	return admission.Allowed("sandbox is valid")
}

// InjectDecoder injects the decoder. Retained as a no-op for backwards
// compatibility with code or tests still calling it; new code should set
// the exported Decoder field directly.
func (v *SandboxValidator) InjectDecoder(d admission.Decoder) error {
	v.Decoder = d
	return nil
}
