// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package workspace

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// secretNamesFor returns the three ephemeral Secret names cleanupFailedWorkspaceSecrets
// targets, so the test and the production code agree on the naming scheme.
func secretNamesFor(workspaceName string) []string {
	return []string{
		fmt.Sprintf("workspace-secrets-%s", workspaceName),
		fmt.Sprintf("workspace-creds-%s", workspaceName),
		fmt.Sprintf("workspace-pw-%s", workspaceName),
	}
}

// makeOwnedSecret builds a Secret in the workspace namespace with the given
// name, for seeding the fake client before a cleanup run.
func makeOwnedSecret(name, ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Data:       map[string][]byte{"k": []byte("v")},
	}
}

// TestCleanupFailedWorkspaceSecrets_DeletesAllThree — regression for Bug 12
// (worklog 0085: Secrets persisting 45+ hours after a workspace failed). When a
// workspace enters Failed, all three per-workspace Secret kinds must be deleted.
// Value: prevents credential/cost leaks from Secrets outliving a dead workspace.
// Failure mode: one or more Secrets retained → stale creds linger + quota cost.
// Expected: after cleanup, none of the three Secrets exist in the namespace.
func TestCleanupFailedWorkspaceSecrets_DeletesAllThree(t *testing.T) {
	const wsName = "ws-fail"
	ws := &v1.Workspace{ObjectMeta: metav1.ObjectMeta{Name: wsName, Namespace: "ns"}}

	// Seed all three Secret kinds for this workspace.
	objs := make([]runtime.Object, 0, 4)
	for _, n := range secretNamesFor(wsName) {
		objs = append(objs, makeOwnedSecret(n, "ns"))
	}
	// Seed an unrelated workspace's secret to prove cleanup is scoped.
	other := makeOwnedSecret(fmt.Sprintf("workspace-secrets-%s", "other-ws"), "ns")
	objs = append(objs, other)

	r := reconcilerFor(t, objs...)

	r.cleanupFailedWorkspaceSecrets(context.Background(), ws)

	for _, name := range secretNamesFor(wsName) {
		got := &corev1.Secret{}
		err := r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "ns"}, got)
		assert.Error(t, err, "Secret %q must be deleted for Failed workspace", name)
	}
	// The unrelated workspace's secret must survive — cleanup is workspace-scoped.
	got := &corev1.Secret{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: other.Name, Namespace: "ns"}, got),
		"cleanup must not delete another workspace's Secrets")
}

// TestCleanupFailedWorkspaceSecrets_IdempotentWhenAlreadyGone — cleanup must
// be a no-op (not an error) when the Secrets are already absent. Value: a
// re-reconcile of an already-failed workspace must not error-loop. Failure
// mode: spurious errors on re-reconcile. Expected: no error, no panic, and the
// missing Secrets stay missing.
func TestCleanupFailedWorkspaceSecrets_IdempotentWhenAlreadyGone(t *testing.T) {
	ws := &v1.Workspace{ObjectMeta: metav1.ObjectMeta{Name: "ws-empty", Namespace: "ns"}}
	r := reconcilerFor(t) // no secrets seeded

	require.NotPanics(t, func() {
		r.cleanupFailedWorkspaceSecrets(context.Background(), ws)
	}, "cleanup of absent Secrets must not panic")
}
