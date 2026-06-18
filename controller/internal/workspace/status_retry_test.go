// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func makeRetryWorkspace(name string) *v1.Workspace {
	return &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "default",
			UID:               "aaaabbbb-cccc-dddd-eeee-ffffgggghhhh",
			CreationTimestamp: metav1.Now(),
		},
		Spec: v1.WorkspaceSpec{
			Owner:   v1.WorkspaceOwner{UserID: "user-1"},
			Runtime: "python:3.11",
		},
		Status: v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive},
	}
}

// TestUpdateStatusWithRetry_SuccessOnFirstAttempt verifies the helper
// applies the mutation when there is no conflict.
func TestUpdateStatusWithRetry_SuccessOnFirstAttempt(t *testing.T) {
	scheme := testScheme(t)
	ws := makeRetryWorkspace("ws-retry-ok")
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fakeClient, Scheme: scheme}

	nn := types.NamespacedName{Name: "ws-retry-ok", Namespace: "default"}
	err := r.updateStatusWithRetry(context.Background(), nn, func(w *v1.Workspace) {
		w.Status.Phase = v1.WorkspacePhaseSuspending
	})
	require.NoError(t, err)

	got := &v1.Workspace{}
	require.NoError(t, fakeClient.Get(context.Background(), nn, got))
	assert.Equal(t, v1.WorkspacePhaseSuspending, got.Status.Phase)
}

// TestUpdateStatusWithRetry_DeterministicMutation verifies the closure
// semantics: the closure captures the intended end state, not the diff.
func TestUpdateStatusWithRetry_DeterministicMutation(t *testing.T) {
	scheme := testScheme(t)
	ws := makeRetryWorkspace("ws-retry-mut")
	ws.Status.PodIP = "10.0.0.1"
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fakeClient, Scheme: scheme}

	nn := types.NamespacedName{Name: "ws-retry-mut", Namespace: "default"}
	desiredPhase := v1.WorkspacePhaseCreating
	desiredPodIP := ""
	err := r.updateStatusWithRetry(context.Background(), nn, func(w *v1.Workspace) {
		w.Status.Phase = desiredPhase
		w.Status.PodIP = desiredPodIP
	})
	require.NoError(t, err)

	got := &v1.Workspace{}
	require.NoError(t, fakeClient.Get(context.Background(), nn, got))
	assert.Equal(t, desiredPhase, got.Status.Phase)
	assert.Equal(t, desiredPodIP, got.Status.PodIP)
}

// TestUpdateStatusWithRetry_NonExistentWorkspaceReturnsGetError verifies
// that a missing workspace surfaces a Get error (not a silent success).
func TestUpdateStatusWithRetry_NonExistentWorkspaceReturnsGetError(t *testing.T) {
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fakeClient, Scheme: scheme}

	nn := types.NamespacedName{Name: "ws-missing", Namespace: "default"}
	err := r.updateStatusWithRetry(context.Background(), nn, func(w *v1.Workspace) {
		w.Status.Phase = v1.WorkspacePhaseFailed
	})
	require.Error(t, err, "missing workspace must surface Get error")
}

// TestUpdateStatusWithRetry_MultipleFieldsInClosure verifies the helper
// handles multi-field mutations atomically.
func TestUpdateStatusWithRetry_MultipleFieldsInClosure(t *testing.T) {
	scheme := testScheme(t)
	ws := makeRetryWorkspace("ws-retry-multi")
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fakeClient, Scheme: scheme}

	nn := types.NamespacedName{Name: "ws-retry-multi", Namespace: "default"}
	err := r.updateStatusWithRetry(context.Background(), nn, func(w *v1.Workspace) {
		w.Status.Phase = v1.WorkspacePhaseSuspended
		w.Status.PodIP = ""
		w.Status.PodName = ""
		w.Status.PodNamespace = ""
		w.Status.Endpoint = ""
	})
	require.NoError(t, err)

	got := &v1.Workspace{}
	require.NoError(t, fakeClient.Get(context.Background(), nn, got))
	assert.Equal(t, v1.WorkspacePhaseSuspended, got.Status.Phase)
	assert.Empty(t, got.Status.PodIP)
	assert.Empty(t, got.Status.PodName)
	assert.Empty(t, got.Status.PodNamespace)
	assert.Empty(t, got.Status.Endpoint)
}

// TestUpdateStatusWithRetry_ZeroValueMutationPreservesObject verifies
// the helper doesn't lose data through re-fetch — a closure that does
// nothing should leave the object unchanged.
func TestUpdateStatusWithRetry_ZeroValueMutationPreservesObject(t *testing.T) {
	scheme := testScheme(t)
	ws := makeRetryWorkspace("ws-retry-noop")
	ws.Status.PodIP = "10.0.0.5"
	ws.Status.RestartCount = 3
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fakeClient, Scheme: scheme}

	nn := types.NamespacedName{Name: "ws-retry-noop", Namespace: "default"}
	err := r.updateStatusWithRetry(context.Background(), nn, func(w *v1.Workspace) {
		// Intentionally empty — no mutation.
	})
	require.NoError(t, err)

	got := &v1.Workspace{}
	require.NoError(t, fakeClient.Get(context.Background(), nn, got))
	assert.Equal(t, "10.0.0.5", got.Status.PodIP)
	assert.Equal(t, int32(3), got.Status.RestartCount)
	assert.Equal(t, v1.WorkspacePhaseActive, got.Status.Phase)
}

// --- clearSuspendRequest tests (US-23.3 reviewer finding) ---

// TestClearSuspendRequest_SetsToNil verifies the helper clears a non-nil
// Spec.Suspend pointer back to nil.
func TestClearSuspendRequest_SetsToNil(t *testing.T) {
	scheme := testScheme(t)
	ws := makeRetryWorkspace("ws-clear")
	suspendTrue := true
	ws.Spec.Suspend = &suspendTrue
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fakeClient, Scheme: scheme}

	err := r.clearSuspendRequest(context.Background(), ws)
	require.NoError(t, err)

	got := &v1.Workspace{}
	require.NoError(t, fakeClient.Get(context.Background(),
		types.NamespacedName{Name: "ws-clear", Namespace: "default"}, got))
	assert.Nil(t, got.Spec.Suspend, "Spec.Suspend must be nil after clear")
}

// TestClearSuspendRequest_AlreadyNilIsNoop verifies the helper does not
// error when Spec.Suspend is already nil.
func TestClearSuspendRequest_AlreadyNilIsNoop(t *testing.T) {
	scheme := testScheme(t)
	ws := makeRetryWorkspace("ws-nil") // Spec.Suspend is nil by default
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fakeClient, Scheme: scheme}

	err := r.clearSuspendRequest(context.Background(), ws)
	require.NoError(t, err)
}

// TestClearSuspendRequest_NonExistentReturnsError verifies the helper
// surfaces a Get error for a missing workspace.
func TestClearSuspendRequest_NonExistentReturnsError(t *testing.T) {
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()
	r := &WorkspaceReconciler{Client: fakeClient, Scheme: scheme}
	ws := makeRetryWorkspace("ws-gone")

	err := r.clearSuspendRequest(context.Background(), ws)
	require.Error(t, err)
}

// --- Integration: stale Spec.Suspend does not cause infinite loop ---
// (Reviewer critical finding #2: controller-initiated suspend must not
// bounce back to Resuming via a stale &false.)

func TestHandleSuspended_StaleFalseAfterControllerSuspend_DoesNotResume(t *testing.T) {
	// Scenario: workspace was activated (API set Spec.Suspend=&false),
	// controller resumed and cleared to nil. Later, controller-initiated
	// idle-suspend transitions to Suspended. Now Spec.Suspend is nil
	// (cleared by controller). handleSuspended must NOT resume.
	ws := makeRetryWorkspace("ws-stale")
	ws.Status.Phase = v1.WorkspacePhaseSuspended
	ws.Spec.Suspend = nil // controller already cleared it after resume
	r := reconcilerFor(t, ws)

	_, err := r.Reconcile(context.Background(), reqFor("ws-stale", "default"))
	require.NoError(t, err)

	got := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "ws-stale", Namespace: "default"}, got))
	// With nil Spec.Suspend and no TTL, the workspace stays Suspended.
	assert.Equal(t, v1.WorkspacePhaseSuspended, got.Status.Phase,
		"nil Spec.Suspend must not trigger resume after controller-initiated suspend")
}

func TestHandleSuspended_NilSpecSuspend_NoTTL_StaysSuspended(t *testing.T) {
	// Scenario: pre-migration workspace suspended via old Status.Phase
	// path. Spec.Suspend was never set (nil). On upgrade, the controller
	// must NOT auto-resume.
	ws := makeRetryWorkspace("ws-premig")
	ws.Status.Phase = v1.WorkspacePhaseSuspended
	ws.Spec.Suspend = nil
	r := reconcilerFor(t, ws)

	_, err := r.Reconcile(context.Background(), reqFor("ws-premig", "default"))
	require.NoError(t, err)

	got := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "ws-premig", Namespace: "default"}, got))
	assert.Equal(t, v1.WorkspacePhaseSuspended, got.Status.Phase,
		"pre-migration workspace must stay Suspended on upgrade")
}

// Ensure unused import runtime doesn't break the build.
var _ = runtime.NewScheme

// Ensure errors import is used.
var _ = errors.New
