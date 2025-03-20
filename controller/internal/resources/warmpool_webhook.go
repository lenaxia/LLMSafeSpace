package resources

import (
	"context"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/validate-llmsafespace-dev-v1-warmpool,mutating=false,failurePolicy=fail,groups=llmsafespace.dev,resources=warmpools,verbs=create;update,versions=v1,name=vwarmpool.kb.io,sideEffects=None,admissionReviewVersions=v1

// WarmPoolValidator validates WarmPool resources
type WarmPoolValidator struct {
	Client  client.Client
	decoder *admission.Decoder
}

// Handle validates the WarmPool resource
func (v *WarmPoolValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	warmPool := &WarmPool{}
	
	err := v.decoder.Decode(req, warmPool)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	
	// Validate runtime exists
	if warmPool.Spec.Runtime == "" {
		return admission.Denied("runtime is required")
	}
	
	// Validate min and max size
	if warmPool.Spec.MinSize < 0 {
		return admission.Denied("minSize must be non-negative")
	}
	
	if warmPool.Spec.MaxSize < 0 {
		return admission.Denied("maxSize must be non-negative")
	}
	
	if warmPool.Spec.MaxSize > 0 && warmPool.Spec.MinSize > warmPool.Spec.MaxSize {
		return admission.Denied("minSize cannot be greater than maxSize")
	}
	
	// Validate profile reference
	if warmPool.Spec.ProfileRef != nil {
		profileName := warmPool.Spec.ProfileRef.Name
		profileNamespace := warmPool.Spec.ProfileRef.Namespace
		if profileNamespace == "" {
			profileNamespace = req.Namespace
		}
		
		profile := &SandboxProfile{}
		err := v.Client.Get(ctx, client.ObjectKey{
			Namespace: profileNamespace,
			Name:      profileName,
		}, profile)
		
		if err != nil {
			return admission.Denied("referenced profile not found")
		}
	}
	
	return admission.Allowed("warm pool is valid")
}

// InjectDecoder injects the decoder
func (v *WarmPoolValidator) InjectDecoder(d *admission.Decoder) error {
	v.decoder = d
	return nil
}
