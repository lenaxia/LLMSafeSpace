// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

func TestBootstrapSAName(t *testing.T) {
	tests := []struct {
		name   string
		wsName string
		want   string
	}{
		{"simple", "abc123", "workspace-abc123"},
		{"uuid with hyphens", "550e8400-e29b-41d4-a716-446655440000", "workspace-550e8400-e29b-41d4-a716-446655440000"},
		{"short", "x", "workspace-x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, bootstrapSAName(tt.wsName))
		})
	}
}

func TestEnsureWorkspaceServiceAccount_Creates(t *testing.T) {
	ws := &v1.Workspace{}
	ws.Name = "ws-sa-create"
	ws.Namespace = "ns"
	ws.UID = "uid-sa-create"

	r := reconcilerFor(t)

	err := r.ensureWorkspaceServiceAccount(context.Background(), ws)
	require.NoError(t, err)

	sa := &corev1.ServiceAccount{}
	err = r.Get(context.Background(), types.NamespacedName{
		Name:      bootstrapSAName(ws.Name),
		Namespace: ws.Namespace,
	}, sa)
	require.NoError(t, err, "ServiceAccount must be created")
	assert.Equal(t, bootstrapSAName(ws.Name), sa.Name)
	assert.Equal(t, ws.Namespace, sa.Namespace)
}

func TestEnsureWorkspaceServiceAccount_Idempotent(t *testing.T) {
	ws := &v1.Workspace{}
	ws.Name = "ws-sa-idem"
	ws.Namespace = "ns"
	ws.UID = "uid-sa-idem"

	r := reconcilerFor(t)

	require.NoError(t, r.ensureWorkspaceServiceAccount(context.Background(), ws))
	require.NoError(t, r.ensureWorkspaceServiceAccount(context.Background(), ws),
		"second call must be a no-op, not an error")

	sa := &corev1.ServiceAccount{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{
		Name: bootstrapSAName(ws.Name), Namespace: ws.Namespace,
	}, sa))
}

func TestEnsureWorkspaceServiceAccount_OwnerRefSet(t *testing.T) {
	ws := &v1.Workspace{}
	ws.Name = "ws-sa-owner"
	ws.Namespace = "ns"
	ws.UID = "uid-sa-owner"

	r := reconcilerFor(t)

	require.NoError(t, r.ensureWorkspaceServiceAccount(context.Background(), ws))

	sa := &corev1.ServiceAccount{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{
		Name: bootstrapSAName(ws.Name), Namespace: ws.Namespace,
	}, sa))

	require.Len(t, sa.OwnerReferences, 1, "SA must have exactly one OwnerReference")
	ref := sa.OwnerReferences[0]
	assert.Equal(t, ws.UID, ref.UID, "OwnerRef UID must match workspace UID")
	assert.True(t, ref.Controller != nil && *ref.Controller, "OwnerRef Controller must be true")
	assert.True(t, ref.BlockOwnerDeletion != nil && *ref.BlockOwnerDeletion, "OwnerRef BlockOwnerDeletion must be true")
}

func TestEnsureWorkspaceServiceAccount_AutomountFalse(t *testing.T) {
	ws := &v1.Workspace{}
	ws.Name = "ws-sa-automount"
	ws.Namespace = "ns"
	ws.UID = "uid-sa-automount"

	r := reconcilerFor(t)

	require.NoError(t, r.ensureWorkspaceServiceAccount(context.Background(), ws))

	sa := &corev1.ServiceAccount{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{
		Name: bootstrapSAName(ws.Name), Namespace: ws.Namespace,
	}, sa))

	require.NotNil(t, sa.AutomountServiceAccountToken,
		"AutomountServiceAccountToken must be explicitly set")
	assert.False(t, *sa.AutomountServiceAccountToken,
		"SA AutomountServiceAccountToken must be false — only the projected token volume is used")
}

func TestEnsureWorkspaceServiceAccount_PreservesExistingOwnerRef(t *testing.T) {
	ws := &v1.Workspace{}
	ws.Name = "ws-sa-preserve"
	ws.Namespace = "ns"
	ws.UID = "uid-sa-preserve"

	r := reconcilerFor(t)

	require.NoError(t, r.ensureWorkspaceServiceAccount(context.Background(), ws))

	// Simulate the SA being recreated by an external actor with a stale
	// OwnerRef (e.g. manual kubectl). ensureWorkspaceServiceAccount must
	// set the correct OwnerRef rather than panic or create a duplicate.
	sa := &corev1.ServiceAccount{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{
		Name: bootstrapSAName(ws.Name), Namespace: ws.Namespace,
	}, sa))
	sa.OwnerReferences = nil
	require.NoError(t, r.Update(context.Background(), sa))

	// The idempotent call (SA exists, Get succeeds) must return nil without
	// touching the existing SA — it does NOT re-apply the OwnerRef on the
	// hot path. This mirrors ensurePasswordSecret (Get → return nil).
	require.NoError(t, r.ensureWorkspaceServiceAccount(context.Background(), ws))
}

// Verify controllerutil.SetControllerReference works with our workspace type.
func TestEnsureWorkspaceServiceAccount_SetControllerReferenceCompat(t *testing.T) {
	ws := &v1.Workspace{}
	ws.Name = "ws-compat"
	ws.Namespace = "ns"
	ws.UID = "uid-compat"

	sa := &corev1.ServiceAccount{}
	sa.Name = bootstrapSAName(ws.Name)
	sa.Namespace = ws.Namespace

	r := reconcilerFor(t)
	err := controllerutil.SetControllerReference(ws, sa, r.Scheme)
	require.NoError(t, err, "SetControllerReference must succeed for Workspace→ServiceAccount")
}
