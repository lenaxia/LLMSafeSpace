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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

func makeSuspendWorkspace(name string) *v1.Workspace {
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

// TestClearSuspendRequest_SetsToNil verifies the helper clears a non-nil
// Spec.Suspend pointer back to nil.
func TestClearSuspendRequest_SetsToNil(t *testing.T) {
	scheme := testScheme(t)
	ws := makeSuspendWorkspace("ws-clear")
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
	ws := makeSuspendWorkspace("ws-nil") // Spec.Suspend is nil by default
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
	ws := makeSuspendWorkspace("ws-gone")

	err := r.clearSuspendRequest(context.Background(), ws)
	require.Error(t, err)
}

// TestClearSuspendRequest_ClearsFalsePointer verifies the helper clears
// a &false pointer (the resume-request case), not just &true.
func TestClearSuspendRequest_ClearsFalsePointer(t *testing.T) {
	scheme := testScheme(t)
	ws := makeSuspendWorkspace("ws-clear-false")
	suspendFalse := false
	ws.Spec.Suspend = &suspendFalse
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
		types.NamespacedName{Name: "ws-clear-false", Namespace: "default"}, got))
	assert.Nil(t, got.Spec.Suspend, "&false pointer must be cleared to nil")
}
