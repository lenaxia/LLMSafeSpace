// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package webhooks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// newScheme returns a runtime.Scheme with both clientgo and llmsafespaces types.
func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, v1.AddToScheme(s))
	return s
}

func newAdmissionRequest(t *testing.T, obj runtime.Object) admission.Request {
	t.Helper()
	raw, err := json.Marshal(obj)
	require.NoError(t, err)
	return admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Namespace: "default",
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
}

func TestRuntimeEnvironmentValidator_Allowed(t *testing.T) {
	s := newScheme(t)
	v := &RuntimeEnvironmentValidator{Decoder: admission.NewDecoder(s), AllowedImageRegistries: []string{"docker.io/", "ghcr.io/"}}
	re := &v1.RuntimeEnvironment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespaces.dev/v1", Kind: "RuntimeEnvironment"},
		ObjectMeta: metav1.ObjectMeta{Name: "py311"},
		Spec: v1.RuntimeEnvironmentSpec{
			Image:    "docker.io/library/python:3.11-slim",
			Language: "python",
		},
	}
	resp := v.Handle(context.Background(), newAdmissionRequest(t, re))
	assert.True(t, resp.Allowed)
}

func TestRuntimeEnvironmentValidator_DeniesEmptyImage(t *testing.T) {
	s := newScheme(t)
	v := &RuntimeEnvironmentValidator{Decoder: admission.NewDecoder(s), AllowedImageRegistries: []string{"docker.io/", "ghcr.io/"}}
	re := &v1.RuntimeEnvironment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespaces.dev/v1", Kind: "RuntimeEnvironment"},
		ObjectMeta: metav1.ObjectMeta{Name: "x"},
		Spec:       v1.RuntimeEnvironmentSpec{Image: "", Language: "python"},
	}
	resp := v.Handle(context.Background(), newAdmissionRequest(t, re))
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "image is required")
}

func TestRuntimeEnvironmentValidator_DeniesEmptyLanguage(t *testing.T) {
	s := newScheme(t)
	v := &RuntimeEnvironmentValidator{Decoder: admission.NewDecoder(s), AllowedImageRegistries: []string{"docker.io/", "ghcr.io/"}}
	re := &v1.RuntimeEnvironment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespaces.dev/v1", Kind: "RuntimeEnvironment"},
		ObjectMeta: metav1.ObjectMeta{Name: "x"},
		Spec:       v1.RuntimeEnvironmentSpec{Image: "ghcr.io/lenaxia/img", Language: ""},
	}
	resp := v.Handle(context.Background(), newAdmissionRequest(t, re))
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "language is required")
}

// TestInjectDecoder verifies the legacy InjectDecoder no-op still sets the
// Decoder field. Required for tests or callers still using the old DI path.
func TestInjectDecoder(t *testing.T) {
	s := newScheme(t)
	dec := admission.NewDecoder(s)

	t.Run("RuntimeEnvironment", func(t *testing.T) {
		v := &RuntimeEnvironmentValidator{}
		require.NoError(t, v.InjectDecoder(dec))
		assert.NotNil(t, v.Decoder)
	})
}
