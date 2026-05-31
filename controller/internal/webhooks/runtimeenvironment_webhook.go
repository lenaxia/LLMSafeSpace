// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package webhooks

import (
	"context"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// +kubebuilder:webhook:path=/validate-llmsafespace-dev-v1-runtimeenvironment,mutating=false,failurePolicy=fail,groups=llmsafespace.dev,resources=runtimeenvironments,verbs=create;update,versions=v1,name=vruntimeenvironment.kb.io,sideEffects=None,admissionReviewVersions=v1

// RuntimeEnvironmentValidator validates RuntimeEnvironment resources.
//
// The Decoder MUST be set at construction time (controller-runtime v0.15+
// removed the InjectDecoder DI callback). A nil Decoder causes Handle to
// panic with nil-pointer-deref on every admission request.
type RuntimeEnvironmentValidator struct {
	Decoder admission.Decoder
}

// Handle validates the RuntimeEnvironment resource.
func (v *RuntimeEnvironmentValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	runtimeEnv := &v1.RuntimeEnvironment{}
	if err := v.Decoder.Decode(req, runtimeEnv); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if runtimeEnv.Spec.Image == "" {
		return admission.Denied("image is required")
	}

	if runtimeEnv.Spec.Language == "" {
		return admission.Denied("language is required")
	}

	return admission.Allowed("runtime environment is valid")
}

// InjectDecoder retained for backwards compatibility (see SandboxValidator).
func (v *RuntimeEnvironmentValidator) InjectDecoder(d admission.Decoder) error {
	v.Decoder = d
	return nil
}
