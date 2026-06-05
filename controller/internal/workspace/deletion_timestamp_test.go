// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// --- isPodTerminating helper tests ---

func TestIsPodTerminating_NilPod(t *testing.T) {
	assert.False(t, isPodTerminating(nil))
}

func TestIsPodTerminating_NoDeletionTimestamp(t *testing.T) {
	pod := &corev1.Pod{}
	assert.False(t, isPodTerminating(pod))
}

func TestIsPodTerminating_WithDeletionTimestamp(t *testing.T) {
	now := metav1.Now()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			DeletionTimestamp: &now,
		},
	}
	assert.True(t, isPodTerminating(pod))
}

// --- handleCreating DeletionTimestamp guard tests ---

func TestHandleCreating_TerminatingPod_DoesNotWriteFailed(t *testing.T) {
	// US-23.1: A pod with DeletionTimestamp + Phase=Failed must NOT trigger
	// terminal Failed. This is the exact worklog 0100 incident pattern.
	scheme := testScheme(t)
	ws := makeWorkspace("ws-dying", "default", v1.WorkspacePhaseCreating)
	ws.UID = "ws-dying-uid"

	now := metav1.Now()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              podName("ws-dying", string(ws.UID)),
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{"test-finalizer"}, // required for DeletionTimestamp to be set
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed, // dying pod shows Failed briefly
		},
	}

	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws, pod).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fc, Scheme: scheme}

	result, err := r.handleCreating(context.Background(), ws)
	require.NoError(t, err)

	// Must NOT transition to Failed
	assert.NotEqual(t, v1.WorkspacePhaseFailed, ws.Status.Phase,
		"terminating pod must not trigger terminal Failed")
	// Must requeue to wait for pod to be reaped
	assert.Equal(t, requeueCreating, result.RequeueAfter)
}

func TestHandleCreating_TerminatingPod_RunningPhase_StillWaits(t *testing.T) {
	// Edge case: pod has DeletionTimestamp but Status.Phase is still Running
	// (mid-termination weird state). Should still wait, not trigger any failure.
	scheme := testScheme(t)
	ws := makeWorkspace("ws-midterm", "default", v1.WorkspacePhaseCreating)
	ws.UID = "ws-midterm-uid"

	now := metav1.Now()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              podName("ws-midterm", string(ws.UID)),
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{"test-finalizer"},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.1",
		},
	}

	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws, pod).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fc, Scheme: scheme}

	result, err := r.handleCreating(context.Background(), ws)
	require.NoError(t, err)

	// Must NOT transition to Active (pod is being deleted)
	assert.NotEqual(t, v1.WorkspacePhaseActive, ws.Status.Phase)
	assert.Equal(t, requeueCreating, result.RequeueAfter)
}

func TestHandleCreating_GenuineFailedPod_EntersRecovery(t *testing.T) {
	// Epic 24: a genuinely failed pod (no DeletionTimestamp) enters
	// recovery with classification instead of terminal Failed.
	scheme := testScheme(t)
	ws := makeWorkspace("ws-genuine", "default", v1.WorkspacePhaseCreating)
	ws.UID = "ws-genuine-uid"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName("ws-genuine", string(ws.UID)),
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
		},
	}

	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws, pod).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fc, Scheme: scheme}

	_, err := r.handleCreating(context.Background(), ws)
	require.NoError(t, err)

	assert.Equal(t, v1.WorkspacePhaseCreating, ws.Status.Phase,
		"failed pod enters recovery (stays in Creating with backoff), not terminal Failed")
	assert.Equal(t, int32(1), ws.Status.ConsecutiveFailures)
	assert.NotNil(t, ws.Status.NextRetryAt, "backoff must be set")
}

// --- handleActive DeletionTimestamp guard tests ---

func TestHandleActive_TerminatingPod_TransitionsToCreating(t *testing.T) {
	// US-23.1: In handleActive, a terminating pod should transition to
	// Creating (not count as transient failure).
	scheme := testScheme(t)
	ws := makeWorkspace("ws-active-dying", "default", v1.WorkspacePhaseActive)
	ws.UID = "ws-active-dying-uid"
	past := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	ws.Status.StartTime = &past
	ws.Status.PodIP = "10.0.0.1"
	ws.Status.Endpoint = "http://10.0.0.1:4096"
	ws.Status.TransientFailureCount = 0

	now := metav1.Now()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              podName("ws-active-dying", string(ws.UID)),
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{"test-finalizer"},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
		},
	}

	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws, pod, makePasswordSecret("ws-active-dying", "default")).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fc, Scheme: scheme}

	result, err := r.handleActive(context.Background(), ws)
	require.NoError(t, err)

	// Must transition to Creating, NOT increment TransientFailureCount
	assert.Equal(t, v1.WorkspacePhaseCreating, ws.Status.Phase)
	assert.Empty(t, ws.Status.PodIP, "PodIP must be cleared")
	assert.Empty(t, ws.Status.Endpoint, "Endpoint must be cleared")
	assert.Equal(t, int32(0), ws.Status.TransientFailureCount,
		"terminating pod must NOT increment transient failure count")
	assert.Equal(t, requeueCreating, result.RequeueAfter)
}

func TestHandleActive_NonRunningPod_NoDeletionTimestamp_RecoverTransient(t *testing.T) {
	// Regression: a non-running pod without DeletionTimestamp must still
	// trigger recovery (via enterRecovery with failure classification).
	scheme := testScheme(t)
	ws := makeWorkspace("ws-active-lost", "default", v1.WorkspacePhaseActive)
	ws.UID = "ws-active-lost-uid"
	past := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	ws.Status.StartTime = &past
	ws.Status.PodIP = "10.0.0.1"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName("ws-active-lost", string(ws.UID)),
			Namespace: "default",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
		},
	}

	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(ws, pod, makePasswordSecret("ws-active-lost", "default")).
		WithStatusSubresource(&v1.Workspace{}).
		Build()
	r := &WorkspaceReconciler{Client: fc, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "ws-active-lost", Namespace: "default"},
	})
	require.NoError(t, err)

	// Fetch updated workspace
	var updated v1.Workspace
	require.NoError(t, fc.Get(context.Background(), types.NamespacedName{Name: "ws-active-lost", Namespace: "default"}, &updated))

	assert.Equal(t, int32(1), updated.Status.ConsecutiveFailures,
		"non-terminating failed pod must trigger recovery with failure classification")
	assert.Equal(t, v1.WorkspacePhaseCreating, updated.Status.Phase,
		"workspace must remain in Creating phase (not terminal Failed)")
}
