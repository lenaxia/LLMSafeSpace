package resources

import (
	"context"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/validate-llmsafespace-dev-v1-warmpod,mutating=false,failurePolicy=fail,groups=llmsafespace.dev,resources=warmpods,verbs=create;update,versions=v1,name=vwarmpod.kb.io,sideEffects=None,admissionReviewVersions=v1

// WarmPodValidator validates WarmPod resources
type WarmPodValidator struct {
	Client  client.Client
	decoder *admission.Decoder
}

// Handle validates the WarmPod resource
func (v *WarmPodValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	warmPod := &WarmPod{}
	
	err := v.decoder.Decode(req, warmPod)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	
	// Validate pool reference
	poolName := warmPod.Spec.PoolRef.Name
	if poolName == "" {
		return admission.Denied("pool reference name is required")
	}
	
	poolNamespace := warmPod.Spec.PoolRef.Namespace
	if poolNamespace == "" {
		poolNamespace = req.Namespace
	}
	
	// Check if the referenced pool exists
	pool := &WarmPool{}
	err = v.Client.Get(ctx, client.ObjectKey{
		Namespace: poolNamespace,
		Name:      poolName,
	}, pool)
	
	if err != nil {
		return admission.Denied("referenced warm pool not found")
	}
	
	return admission.Allowed("warm pod is valid")
}

// InjectDecoder injects the decoder
func (v *WarmPodValidator) InjectDecoder(d *admission.Decoder) error {
	v.decoder = d
	return nil
}
