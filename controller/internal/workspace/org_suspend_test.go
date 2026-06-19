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

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// fakeOrgStatusClient is a test OrgStatusClient with a fixed (status, ok)
// response and a call counter so tests can assert the client was/was-not
// consulted.
type fakeOrgStatusClient struct {
	status string
	ok     bool
	calls  int
}

func (f *fakeOrgStatusClient) GetOrgStatus(_ context.Context, _ string) (string, bool) {
	f.calls++
	return f.status, f.ok
}

func makeOrgWorkspace(name, namespace, orgID string, phase v1.WorkspacePhase) *v1.Workspace {
	ws := makeWorkspace(name, namespace, phase)
	ws.Spec.Owner.OrgID = orgID
	return ws
}

// TestReconcile_Active_OrgSuspended_TransitionsToSuspending is the core D20
// e2e: an Active workspace owned by a suspended org transitions to Suspending
// on reconcile (pod will be killed, PVC retained).
func TestReconcile_Active_OrgSuspended_TransitionsToSuspending(t *testing.T) {
	ws := makeOrgWorkspace("ws-org-susp", "default", "org-1", v1.WorkspacePhaseActive)
	ws.Status.PodIP = "10.0.0.1"
	now := metav1.Now()
	ws.Status.StartTime = &now
	pod := makeRunningPod(podName("ws-org-susp", string(ws.UID)), "default", "10.0.0.1")
	r := reconcilerFor(t, ws, pod)
	r.OrgStatusClient = &fakeOrgStatusClient{status: "suspended", ok: true}

	_, err := r.Reconcile(context.Background(), reqFor("ws-org-susp", "default"))
	require.NoError(t, err)

	got := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "ws-org-susp", Namespace: "default"}, got))
	assert.Equal(t, v1.WorkspacePhaseSuspending, got.Status.Phase,
		"suspended org must drive Active→Suspending")
}

// TestReconcile_Active_OrgActive_StaysActive verifies an active org leaves the
// workspace in the normal Active reconcile path (requeues, does not suspend).
func TestReconcile_Active_OrgActive_StaysActive(t *testing.T) {
	ws := makeOrgWorkspace("ws-org-ok", "default", "org-1", v1.WorkspacePhaseActive)
	ws.Status.PodIP = "10.0.0.1"
	now := metav1.Now()
	ws.Status.StartTime = &now
	pod := makeRunningPod(podName("ws-org-ok", string(ws.UID)), "default", "10.0.0.1")
	pwSecret := makePasswordSecret("ws-org-ok", "default")
	r := reconcilerFor(t, ws, pod, pwSecret)
	fc := &fakeOrgStatusClient{status: "active", ok: true}
	r.OrgStatusClient = fc

	result, err := r.Reconcile(context.Background(), reqFor("ws-org-ok", "default"))
	require.NoError(t, err)
	assert.Equal(t, requeueActive, result.RequeueAfter, "active org should follow the normal Active requeue")

	got := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "ws-org-ok", Namespace: "default"}, got))
	assert.Equal(t, v1.WorkspacePhaseActive, got.Status.Phase, "active org must NOT suspend the workspace")
	assert.GreaterOrEqual(t, fc.calls, 1, "client must have been consulted")
}

// TestReconcile_Active_OrgLookupFails_StaysActive verifies the D20 fail-safe:
// when the org status cannot be determined (API unreachable, no cache), the
// workspace keeps running rather than being suspended on an assumption.
func TestReconcile_Active_OrgLookupFails_StaysActive(t *testing.T) {
	ws := makeOrgWorkspace("ws-org-failopen", "default", "org-1", v1.WorkspacePhaseActive)
	ws.Status.PodIP = "10.0.0.1"
	now := metav1.Now()
	ws.Status.StartTime = &now
	pod := makeRunningPod(podName("ws-org-failopen", string(ws.UID)), "default", "10.0.0.1")
	pwSecret := makePasswordSecret("ws-org-failopen", "default")
	r := reconcilerFor(t, ws, pod, pwSecret)
	r.OrgStatusClient = &fakeOrgStatusClient{status: "", ok: false}

	result, err := r.Reconcile(context.Background(), reqFor("ws-org-failopen", "default"))
	require.NoError(t, err)
	assert.Equal(t, requeueActive, result.RequeueAfter, "fail-open should follow the normal Active requeue")

	got := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "ws-org-failopen", Namespace: "default"}, got))
	assert.Equal(t, v1.WorkspacePhaseActive, got.Status.Phase, "fail-open must NOT suspend the workspace")
}

// TestReconcile_Active_PersonalWorkspace_NotConsulted verifies that a personal
// workspace (no OrgID) never triggers the org-status lookup even when the
// client is configured.
func TestReconcile_Active_PersonalWorkspace_NotConsulted(t *testing.T) {
	ws := makeWorkspace("ws-personal", "default", v1.WorkspacePhaseActive) // no OrgID
	ws.Status.PodIP = "10.0.0.1"
	now := metav1.Now()
	ws.Status.StartTime = &now
	pod := makeRunningPod(podName("ws-personal", string(ws.UID)), "default", "10.0.0.1")
	pwSecret := makePasswordSecret("ws-personal", "default")
	r := reconcilerFor(t, ws, pod, pwSecret)
	fc := &fakeOrgStatusClient{status: "suspended", ok: true} // would suspend if consulted
	r.OrgStatusClient = fc

	_, err := r.Reconcile(context.Background(), reqFor("ws-personal", "default"))
	require.NoError(t, err)

	assert.Equal(t, 0, fc.calls, "personal workspace must not consult the org-status client")
	got := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "ws-personal", Namespace: "default"}, got))
	assert.Equal(t, v1.WorkspacePhaseActive, got.Status.Phase, "personal workspace must not be org-suspended")
}

// TestReconcile_Active_OrgSuspended_NilClient_NoSuspend verifies the feature
// is off when the client is nil (--api-service-url unset): an org workspace is
// NOT suspended even if the org would be suspended.
func TestReconcile_Active_OrgSuspended_NilClient_NoSuspend(t *testing.T) {
	ws := makeOrgWorkspace("ws-org-noclient", "default", "org-1", v1.WorkspacePhaseActive)
	ws.Status.PodIP = "10.0.0.1"
	now := metav1.Now()
	ws.Status.StartTime = &now
	pod := makeRunningPod(podName("ws-org-noclient", string(ws.UID)), "default", "10.0.0.1")
	pwSecret := makePasswordSecret("ws-org-noclient", "default")
	r := reconcilerFor(t, ws, pod, pwSecret)
	// r.OrgStatusClient is nil by default

	result, err := r.Reconcile(context.Background(), reqFor("ws-org-noclient", "default"))
	require.NoError(t, err)
	assert.Equal(t, requeueActive, result.RequeueAfter)

	got := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "ws-org-noclient", Namespace: "default"}, got))
	assert.Equal(t, v1.WorkspacePhaseActive, got.Status.Phase, "nil client must not suspend")
}

// TestReconcile_Active_OrgSuspended_DoesNotAutoResume verifies D20's no-auto-
// resume guarantee: once a workspace is Suspended, an active org does not
// cause the controller to resume it. (handleSuspended only resumes on an
// explicit Spec.Suspend=false, never on org status.)
func TestReconcile_Active_OrgSuspended_DoesNotAutoResume(t *testing.T) {
	// Start the workspace as Suspended (already org-suspended earlier).
	ws := makeOrgWorkspace("ws-org-noresume", "default", "org-1", v1.WorkspacePhaseSuspended)
	pwSecret := makePasswordSecret("ws-org-noresume", "default")
	r := reconcilerFor(t, ws, pwSecret)
	r.OrgStatusClient = &fakeOrgStatusClient{status: "active", ok: true} // org now active

	_, err := r.Reconcile(context.Background(), reqFor("ws-org-noresume", "default"))
	require.NoError(t, err)

	got := &v1.Workspace{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: "ws-org-noresume", Namespace: "default"}, got))
	assert.Equal(t, v1.WorkspacePhaseSuspended, got.Status.Phase,
		"controller must NOT auto-resume a suspended workspace when the org reactivates")
}
