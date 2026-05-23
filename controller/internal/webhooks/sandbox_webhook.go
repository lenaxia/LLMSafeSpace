// Package webhooks contains admission webhook validators for llmsafespace
// CRDs. Validators are registered against the controller-runtime webhook
// server at startup; see controller/main.go.
package webhooks

import (
	"context"
	"fmt"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
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

// Handle validates the Sandbox resource.
func (v *SandboxValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	sandbox := &v1.Sandbox{}

	if err := v.Decoder.Decode(req, sandbox); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if sandbox.Spec.Runtime == "" {
		return admission.Denied("runtime is required")
	}

	if sandbox.Spec.NetworkAccess != nil {
		for _, rule := range sandbox.Spec.NetworkAccess.Egress {
			if rule.Domain == "" {
				return admission.Denied("egress rule domain is required")
			}
		}
	}

	if sandbox.Spec.ProfileRef != nil {
		profileName := sandbox.Spec.ProfileRef.Name
		profileNamespace := sandbox.Spec.ProfileRef.Namespace
		if profileNamespace == "" {
			profileNamespace = req.Namespace
		}

		profile := &v1.SandboxProfile{}
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

// InjectDecoder retains the legacy dependency-injection entry point as a
// no-op-style setter for callers/tests that still construct validators
// without supplying a Decoder.
func (v *SandboxValidator) InjectDecoder(d admission.Decoder) error {
	v.Decoder = d
	return nil
}
