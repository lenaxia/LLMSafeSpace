// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package webhooks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// =============================================================================
// G2 — Workspace admission webhook (closes F1.2.1, F1.2.2, RT-2.18, RT-6.10, RT-6.1)
// =============================================================================
//
// The Workspace CRD shipped with no validating webhook. Phase 1 reconnaissance
// (F1.2.1) and Phase 2 live-cluster (RT-2.18) demonstrated that a user could
// CREATE a Workspace with `runtime: "evil.example.com/malicious:latest"` and
// the controller would pull and run that image. Same vector for the Status
// subresource (F1.2.2): a user with `workspaces` create/update could write
// `status.podIP="10.0.0.1"` on CREATE and the controller would happily proxy
// requests to that arbitrary IP.
//
// This test suite drives a new WorkspaceValidator that:
//   1. Rejects explicit image references whose registry is not in an
//      operator-supplied allow-list.
//   2. Rejects runtimes containing path-traversal / NUL / backslash.
//   3. Rejects storage.size above an operator-supplied maximum.
//   4. Rejects a non-empty Status block on CREATE.
//   5. Rejects a Spec change to status fields on UPDATE (defense in depth;
//      kube-apiserver's status subresource also enforces this).
//
// All tests use the same admission.Decoder helper from webhooks_test.go.

func newWorkspaceCreateRequest(t *testing.T, ws *v1.Workspace) admission.Request {
	t.Helper()
	raw, err := json.Marshal(ws)
	require.NoError(t, err)
	return admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Namespace: "default",
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
}

func newWorkspaceUpdateRequest(t *testing.T, oldWs, newWs *v1.Workspace) admission.Request {
	t.Helper()
	rawOld, err := json.Marshal(oldWs)
	require.NoError(t, err)
	rawNew, err := json.Marshal(newWs)
	require.NoError(t, err)
	return admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			Namespace: "default",
			Object:    runtime.RawExtension{Raw: rawNew},
			OldObject: runtime.RawExtension{Raw: rawOld},
		},
	}
}

// minimalValidWorkspace returns a Workspace that should pass the validator
// when the registry allow-list contains no images (i.e. operator only allows
// runtime references resolved via RuntimeEnvironment CRDs by name).
func minimalValidWorkspace() *v1.Workspace {
	return &v1.Workspace{
		TypeMeta:   metav1.TypeMeta{APIVersion: "llmsafespace.dev/v1", Kind: "Workspace"},
		ObjectMeta: metav1.ObjectMeta{Name: "ws-1", Namespace: "default"},
		Spec: v1.WorkspaceSpec{
			Owner:   v1.WorkspaceOwner{UserID: "u1"},
			Runtime: "python-3.11", // referenced by RuntimeEnvironment name
			Storage: v1.WorkspaceStorageConfig{Size: "5Gi"},
		},
	}
}

// --- F1.2.1 / RT-2.18 / RT-6.10: registry allow-list ---

func TestG2Workspace_DeniesArbitraryRegistry(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/lenaxia/", "registry.k8s.io/"},
		MaxStorageGi:           1024,
	}
	ws := minimalValidWorkspace()
	ws.Spec.Runtime = "evil.example.com/malicious:latest"
	resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
	assert.False(t, resp.Allowed)
	require.NotNil(t, resp.Result)
	assert.Contains(t, strings.ToLower(resp.Result.Message), "runtime")
	assert.Contains(t, strings.ToLower(resp.Result.Message), "registry")
}

func TestG2Workspace_AllowsAllowlistedRegistry(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/lenaxia/", "registry.k8s.io/"},
		MaxStorageGi:           1024,
	}
	ws := minimalValidWorkspace()
	ws.Spec.Runtime = "ghcr.io/lenaxia/llmsafespace/runtime-python:3.11"
	resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
	assert.True(t, resp.Allowed, "allow-listed registry must pass: %v", resp.Result)
}

func TestG2Workspace_AllowsRuntimeEnvironmentRefByName(t *testing.T) {
	// "python-3.11" has no '/' so it's a RuntimeEnvironment name lookup at
	// reconcile time, not a direct image. The webhook can't verify the
	// RuntimeEnvironment exists (cluster I/O during admission is risky),
	// so it simply accepts non-image-shaped runtimes.
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: nil, // no allow-list → block ALL explicit images
		MaxStorageGi:           1024,
	}
	ws := minimalValidWorkspace()
	ws.Spec.Runtime = "python-3.11"
	resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
	assert.True(t, resp.Allowed, "RuntimeEnvironment-name reference must pass: %v", resp.Result)
}

func TestG2Workspace_DeniesEmptyRuntime(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/"},
		MaxStorageGi:           1024,
	}
	ws := minimalValidWorkspace()
	ws.Spec.Runtime = ""
	resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
	assert.False(t, resp.Allowed)
	assert.Contains(t, strings.ToLower(resp.Result.Message), "runtime")
}

// --- RT-6.1: traversal characters in runtime ---

func TestG2Workspace_DeniesTraversalRuntime(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/"},
		MaxStorageGi:           1024,
	}
	for _, payload := range []string{
		"../../etc/passwd",
		"ghcr.io/../../../etc/passwd",
		"runtime\x00null",
		"runtime\\backslash",
		"runtime with space",
		"evil.example.com:65535/img@sha256:" + strings.Repeat("a", 64), // un-allow-listed registry; explicit reject
	} {
		t.Run(payload, func(t *testing.T) {
			ws := minimalValidWorkspace()
			ws.Spec.Runtime = payload
			resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
			assert.False(t, resp.Allowed,
				"runtime payload %q must be rejected", payload)
		})
	}
}

// --- RT-6.1: storage upper bound ---

func TestG2Workspace_DeniesAbsurdStorageSize(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/"},
		MaxStorageGi:           1024,
	}
	ws := minimalValidWorkspace()
	ws.Spec.Storage.Size = "999999Gi"
	resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
	assert.False(t, resp.Allowed)
	assert.Contains(t, strings.ToLower(resp.Result.Message), "storage")
}

func TestG2Workspace_AllowsBoundedStorageSize(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/"},
		MaxStorageGi:           1024,
	}
	for _, size := range []string{"1Gi", "100Gi", "1024Gi", "256Mi"} {
		ws := minimalValidWorkspace()
		ws.Spec.Storage.Size = size
		resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
		assert.True(t, resp.Allowed,
			"storage %s must pass: %v", size, resp.Result)
	}
}

func TestG2Workspace_DeniesMalformedStorageSize(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/"},
		MaxStorageGi:           1024,
	}
	for _, size := range []string{"", "huge", "1TB", "-5Gi", "5GB"} {
		t.Run(size, func(t *testing.T) {
			ws := minimalValidWorkspace()
			ws.Spec.Storage.Size = size
			resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
			assert.False(t, resp.Allowed,
				"storage size %q must be rejected", size)
		})
	}
}

// --- F1.2.9: storageClassName allow-list ---

func TestG2Workspace_DeniesUnallowedStorageClass(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                  admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries:   []string{"ghcr.io/"},
		MaxStorageGi:             1024,
		AllowedStorageClassNames: []string{"longhorn", "gp3"},
	}
	ws := minimalValidWorkspace()
	ws.Spec.Storage.StorageClassName = "host-path-attacker"
	resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
	assert.False(t, resp.Allowed)
	assert.Contains(t, strings.ToLower(resp.Result.Message), "storageclass")
}

func TestG2Workspace_AllowsAllowedStorageClass(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                  admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries:   []string{"ghcr.io/"},
		MaxStorageGi:             1024,
		AllowedStorageClassNames: []string{"longhorn", "gp3"},
	}
	ws := minimalValidWorkspace()
	ws.Spec.Storage.StorageClassName = "longhorn"
	resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
	assert.True(t, resp.Allowed, "allow-listed storage class must pass: %v", resp.Result)
}

func TestG2Workspace_AllowsEmptyStorageClass_WhenAllowlistConfigured(t *testing.T) {
	// Empty StorageClassName means "use cluster default" — that is the
	// operator's choice and is always permitted, even with an allow-list.
	v := &WorkspaceValidator{
		Decoder:                  admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries:   []string{"ghcr.io/"},
		MaxStorageGi:             1024,
		AllowedStorageClassNames: []string{"longhorn"},
	}
	ws := minimalValidWorkspace()
	ws.Spec.Storage.StorageClassName = ""
	resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
	assert.True(t, resp.Allowed, "empty storage class must default-pass: %v", resp.Result)
}

// --- F1.2.2: status forge on CREATE ---

func TestG2Workspace_DeniesNonEmptyStatusOnCreate(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/"},
		MaxStorageGi:           1024,
	}
	for _, mutate := range []func(*v1.Workspace){
		func(w *v1.Workspace) { w.Status.Phase = v1.WorkspacePhaseActive },
		func(w *v1.Workspace) { w.Status.PodIP = "169.254.169.254" },
		func(w *v1.Workspace) { w.Status.PodName = "attacker-pod" },
		func(w *v1.Workspace) { w.Status.PodNamespace = "kube-system" },
		func(w *v1.Workspace) { w.Status.Endpoint = "http://attacker.example.com" },
	} {
		ws := minimalValidWorkspace()
		mutate(ws)
		resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
		assert.False(t, resp.Allowed,
			"non-empty status on CREATE must be rejected (mutated field)")
		if resp.Result != nil {
			assert.Contains(t, strings.ToLower(resp.Result.Message), "status",
				"rejection reason must mention 'status'")
		}
	}
}

// --- F1.2.2: status forge on UPDATE through the spec endpoint ---

func TestG2Workspace_DeniesStatusFieldChangeOnSpecUpdate(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/"},
		MaxStorageGi:           1024,
	}
	oldWs := minimalValidWorkspace()
	oldWs.Status.PodIP = "10.0.0.1" // legitimately set by controller
	newWs := oldWs.DeepCopy()
	newWs.Status.PodIP = "169.254.169.254" // attacker forge

	resp := v.Handle(context.Background(), newWorkspaceUpdateRequest(t, oldWs, newWs))
	assert.False(t, resp.Allowed,
		"changes to .status fields on UPDATE through the spec endpoint must be rejected")
}

// --- F1.2.2: spec UPDATE that doesn't touch status passes ---

func TestG2Workspace_AllowsSpecOnlyUpdate(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/"},
		MaxStorageGi:           1024,
	}
	oldWs := minimalValidWorkspace()
	oldWs.Status.PodIP = "10.0.0.1"
	newWs := oldWs.DeepCopy()
	newWs.Spec.Storage.Size = "20Gi"

	resp := v.Handle(context.Background(), newWorkspaceUpdateRequest(t, oldWs, newWs))
	assert.True(t, resp.Allowed,
		"spec-only update must pass: %v", resp.Result)
}

// --- Defense in depth: nil-decoder doesn't panic ---

func TestG2Workspace_NilDecoderDoesNotPanic(t *testing.T) {
	v := &WorkspaceValidator{Decoder: nil}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil-decoder webhook must not panic, got: %v", r)
		}
	}()
	resp := v.Handle(context.Background(), admission.Request{})
	assert.False(t, resp.Allowed)
}

// --- Validator follow-up: UPDATE with empty OldObject ---

// TestG2Workspace_UpdateWithEmptyOldObjectFailsClosed proves the fix
// for the validator-found bypass: an UPDATE request with no
// req.OldObject.Raw used to silently skip the status-mutation check.
// We now fail closed — any non-zero status on the new object during
// such an UPDATE is rejected as if the prior status were zero.
func TestG2Workspace_UpdateWithEmptyOldObjectFailsClosed(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/"},
		MaxStorageGi:           1024,
	}
	newWs := minimalValidWorkspace()
	newWs.Status.PodIP = "169.254.169.254"

	rawNew, err := json.Marshal(newWs)
	require.NoError(t, err)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			Namespace: "default",
			Object:    runtime.RawExtension{Raw: rawNew},
			// OldObject deliberately empty — covers the bypass path.
		},
	}
	resp := v.Handle(context.Background(), req)
	assert.False(t, resp.Allowed,
		"UPDATE with empty OldObject and non-zero new status must be rejected")
}

func TestG2Workspace_UpdateWithEmptyOldObjectAndZeroStatusPasses(t *testing.T) {
	// Same code path, but with zero status the operation is benign.
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/"},
		MaxStorageGi:           1024,
	}
	newWs := minimalValidWorkspace()
	rawNew, err := json.Marshal(newWs)
	require.NoError(t, err)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			Namespace: "default",
			Object:    runtime.RawExtension{Raw: rawNew},
		},
	}
	resp := v.Handle(context.Background(), req)
	assert.True(t, resp.Allowed,
		"UPDATE with empty OldObject and zero status must pass: %v", resp.Result)
}

// --- Validator follow-up: length caps ---

func TestG2Workspace_DeniesRuntimeOver512Chars(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/"},
		MaxStorageGi:           1024,
	}
	ws := minimalValidWorkspace()
	ws.Spec.Runtime = "ghcr.io/" + strings.Repeat("a", 510)
	resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
	assert.False(t, resp.Allowed)
	if resp.Result != nil {
		assert.Contains(t, strings.ToLower(resp.Result.Message), "length")
	}
}

func TestG2Workspace_DeniesStorageClassNameOver253Chars(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/"},
		MaxStorageGi:           1024,
	}
	ws := minimalValidWorkspace()
	ws.Spec.Storage.StorageClassName = strings.Repeat("a", 254)
	resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
	assert.False(t, resp.Allowed)
}

// --- Validator follow-up: allow-list prefix without trailing slash ---

// TestG2Workspace_AllowlistPrefixWithoutSlashIsNormalized proves the
// validator-found misconfiguration class. Operator writes
// "ghcr.io/lenaxia" (no slash) in values.yaml; we treat it as
// "ghcr.io/lenaxia/" so an attacker cannot smuggle
// "ghcr.io/lenaxia.attacker.com/foo" past the prefix match.
func TestG2Workspace_AllowlistPrefixWithoutSlashIsNormalized(t *testing.T) {
	v := &WorkspaceValidator{
		Decoder:                admission.NewDecoder(newScheme(t)),
		AllowedImageRegistries: []string{"ghcr.io/lenaxia"}, // no trailing slash
		MaxStorageGi:           1024,
	}
	// Legitimate ghcr.io/lenaxia/... still passes because we normalise
	// the prefix to add the slash.
	t.Run("legitimate match still passes", func(t *testing.T) {
		ws := minimalValidWorkspace()
		ws.Spec.Runtime = "ghcr.io/lenaxia/runtime:1.0"
		resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
		assert.True(t, resp.Allowed,
			"normalised prefix should match legitimate use: %v", resp.Result)
	})
	// Attempt to smuggle via suffix attack on the prefix.
	t.Run("prefix-suffix homograph is rejected", func(t *testing.T) {
		ws := minimalValidWorkspace()
		ws.Spec.Runtime = "ghcr.io/lenaxia.attacker.com/foo"
		resp := v.Handle(context.Background(), newWorkspaceCreateRequest(t, ws))
		assert.False(t, resp.Allowed,
			"slash-normalised prefix must reject suffix attack")
	})
}
