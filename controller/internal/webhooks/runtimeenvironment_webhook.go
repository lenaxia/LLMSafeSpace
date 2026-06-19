// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package webhooks

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// +kubebuilder:webhook:path=/validate-llmsafespaces-dev-v1-runtimeenvironment,mutating=false,failurePolicy=fail,groups=llmsafespaces.dev,resources=runtimeenvironments,verbs=create;update,versions=v1,name=vruntimeenvironment.kb.io,sideEffects=None,admissionReviewVersions=v1

// RuntimeEnvironmentValidator validates RuntimeEnvironment resources.
//
// F1.2.10 (Epic 17): pre-fix the validator only checked that
// Spec.Image was non-empty. A user with permission to create
// RuntimeEnvironment CRDs could supply
// `image: evil.example.com/malicious:latest` and any subsequent
// Workspace using that runtime would pull and run the attacker
// image. The validator now applies the same registry allow-list
// the workspace webhook uses (see workspace_webhook.go).
//
// The Decoder MUST be set at construction time (controller-runtime
// v0.15+ removed the InjectDecoder DI callback). A nil Decoder
// causes Handle to panic with nil-pointer-deref on every admission
// request.
type RuntimeEnvironmentValidator struct {
	Decoder                admission.Decoder
	AllowedImageRegistries []string // mirrors WorkspaceValidator
}

// Handle validates the RuntimeEnvironment resource.
func (v *RuntimeEnvironmentValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	if v.Decoder == nil {
		return admission.Errored(http.StatusInternalServerError,
			fmt.Errorf("runtimeenvironment webhook: decoder not configured"))
	}

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

	// F1.2.10 — same registry allow-list + traversal/whitespace
	// checks as the workspace webhook (G2). The image MUST contain
	// a slash (RuntimeEnvironment images are always explicit refs;
	// there's no name-based lookup). Reject leading dash, URL
	// schemes, traversal, etc. via reuse of the workspace
	// validator's helpers.
	if runtimeRefHasTraversal(runtimeEnv.Spec.Image) {
		return admission.Denied(
			"spec.image contains forbidden characters (path-traversal, whitespace, NUL or backslash)")
	}
	if !runtimeRunSafePattern.MatchString(runtimeEnv.Spec.Image) {
		return admission.Denied(
			"spec.image contains characters outside the allowed set [a-zA-Z0-9._/:@-]")
	}
	if !strings.Contains(runtimeEnv.Spec.Image, "/") {
		return admission.Denied(
			"spec.image must be an explicit container image reference (must contain '/')")
	}
	matched := false
	for _, prefix := range v.AllowedImageRegistries {
		if prefix == "" {
			continue
		}
		normalized := prefix
		if !strings.HasSuffix(normalized, "/") {
			normalized += "/"
		}
		if strings.HasPrefix(runtimeEnv.Spec.Image, normalized) {
			matched = true
			break
		}
	}
	if !matched {
		allowed := strings.Join(v.AllowedImageRegistries, ", ")
		if allowed == "" {
			allowed = "(none — operator must populate webhooks.allowedImageRegistries)"
		}
		return admission.Denied(fmt.Sprintf(
			"spec.image %q registry is not in the allow-list. Allowed prefixes: %s",
			runtimeEnv.Spec.Image, allowed))
	}

	return admission.Allowed("runtime environment is valid")
}

// InjectDecoder retained for backwards compatibility (see RuntimeEnvironmentValidator).
func (v *RuntimeEnvironmentValidator) InjectDecoder(d admission.Decoder) error {
	v.Decoder = d
	return nil
}
