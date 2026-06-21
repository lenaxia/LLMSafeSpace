// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// TestHandleSuspending_ClearsRecoveryState — US-24.8 F22: suspend must clear
// all recovery state so resume starts fresh. The existing
// TestReconcile_Suspending_DeletesPodAndTransitions only asserts Phase=Suspended
// + PodIP="" + SuspendedAt!=nil; it does NOT verify the recovery fields are
// cleared. A regression that left ConsecutiveFailures or NextRetryAt set would
// cause the resumed workspace to immediately enter recovery.
// Value: prevents a zombie recovery state from carrying over to resume.
// Failure mode: resumed workspace immediately re-enters Failed/recovery.
// Expected: ConsecutiveFailures=0, NextRetryAt=nil, LastFailureClass="",
// LastFailureAt=nil, ActiveSessions=0, Sessions=nil after suspend.
func TestHandleSuspending_ClearsRecoveryState(t *testing.T) {
	ws := makeWorkspace("ws-recovery-clear", "default", v1.WorkspacePhaseSuspending)
	expectedPodName := podName("ws-recovery-clear", string(ws.UID))
	pod := makeRunningPod(expectedPodName, "default", "10.0.0.1")

	// Pre-populate recovery state — simulate a workspace that was in recovery
	// before being suspended.
	ws.Status.ConsecutiveFailures = 3
	ws.Status.ControllerRestartCount = 2
	future := metav1.NewTime(metav1.Now().Add(60))
	ws.Status.NextRetryAt = &future
	ws.Status.LastFailureClass = "transient"
	pastFail := metav1.NewTime(metav1.Now().Add(-120))
	ws.Status.LastFailureAt = &pastFail
	ws.Status.ActiveSessions = 5
	ws.Status.Sessions = []v1.AgentSessionStatus{{ID: "ses-1"}, {ID: "ses-2"}}

	r := reconcilerFor(t, ws, pod)

	_, err := r.Reconcile(context.Background(), reqFor("ws-recovery-clear", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "ws-recovery-clear", Namespace: "default"}, updated))

	assert.Equal(t, v1.WorkspacePhaseSuspended, updated.Status.Phase)
	assert.Equal(t, int32(0), updated.Status.ConsecutiveFailures,
		"ConsecutiveFailures must be cleared so resume starts fresh (US-24.8 F22)")
	assert.Equal(t, int32(0), updated.Status.ControllerRestartCount,
		"ControllerRestartCount must be cleared")
	assert.Nil(t, updated.Status.NextRetryAt,
		"NextRetryAt must be nil so resume does not back off")
	assert.Empty(t, updated.Status.LastFailureClass,
		"LastFailureClass must be cleared")
	assert.Nil(t, updated.Status.LastFailureAt,
		"LastFailureAt must be cleared")
	assert.Equal(t, int32(0), updated.Status.ActiveSessions,
		"ActiveSessions must be reset")
	assert.Nil(t, updated.Status.Sessions,
		"Sessions must be cleared")
}

// TestHandleSuspending_ClearsPodFields — the existing test only checks PodIP
// is cleared. The pod name and namespace must also be cleared so a stale
// reference does not point at a deleted pod after resume re-creates one.
// Value: prevents stale pod-name references after suspend. Failure mode:
// status points at a deleted pod → API 404s on proxy attempts. Expected:
// PodName, PodNamespace, PodIP, Endpoint all empty.
func TestHandleSuspending_ClearsPodFields(t *testing.T) {
	ws := makeWorkspace("ws-pod-clear", "default", v1.WorkspacePhaseSuspending)
	expectedPodName := podName("ws-pod-clear", string(ws.UID))
	pod := makeRunningPod(expectedPodName, "default", "10.0.0.99")

	ws.Status.PodName = expectedPodName
	ws.Status.PodNamespace = "default"
	ws.Status.PodIP = "10.0.0.99"
	ws.Status.Endpoint = "http://10.0.0.99:4096"

	r := reconcilerFor(t, ws, pod)

	_, err := r.Reconcile(context.Background(), reqFor("ws-pod-clear", "default"))
	require.NoError(t, err)

	updated := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "ws-pod-clear", Namespace: "default"}, updated))

	assert.Empty(t, updated.Status.PodName, "PodName must be cleared on suspend")
	assert.Empty(t, updated.Status.PodNamespace, "PodNamespace must be cleared")
	assert.Empty(t, updated.Status.PodIP, "PodIP must be cleared")
	assert.Empty(t, updated.Status.Endpoint, "Endpoint must be cleared")
}
