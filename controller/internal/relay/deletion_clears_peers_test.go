// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// TestHandleDeletion_ClearsPeerConfigMap pins the deletion-cleanup
// fix for worklog 0467+: when the InferenceRelay CR is deleted, the
// controller must write {"relays":[]} to the peer ConfigMap before
// removing the finalizer (and thus before owner-reference cascade
// deletes the CM).
//
// Without this, kubelet's "optional ConfigMap" volume mount semantics
// keep the stale file in the relay-router pod's /etc/relay-router/
// directory after the CM is deleted. The router's pollPeerConfig sees
// the same stale content forever (until pod restart) and continues to
// list orphaned relays in metrics + select them for routing.
//
// Writing the empty list explicitly before deleting the CR forces
// kubelet to update the volume mount with the cleared content. After
// the file change is observed by the router, owner-reference cleanup
// then removes the CM safely.
func TestHandleDeletion_ClearsPeerConfigMap(t *testing.T) {
	scheme := testScheme(t)

	// Seed: CR with one healthy relay, and an existing peer ConfigMap
	// populated with that relay (the steady state before deletion).
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet"},
		Status: v1.InferenceRelayStatus{
			Instances: []v1.RelayInstanceStatus{
				{ID: "i-aaa", Provider: "aws", Region: "us-west-2", State: string(v1.RelayStateHealthy)},
			},
		},
	}
	controllerutil.AddFinalizer(relay, InferenceRelayFinalizer)

	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routerPeersConfigMap,
			Namespace: "test-ns",
		},
		Data: map[string]string{
			"peers.json": `{"relays":[{"id":"i-aaa","endpoint":"1.2.3.4:8080","provider":"aws","state":"healthy","token":"t"}]}`,
		},
	}

	awsDriver := &stubDriver{}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(relay, existingCM).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()
	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers:   map[string]ProviderDriver{"aws": awsDriver},
	}

	_, err := r.handleDeletion(context.Background(), relay)
	require.NoError(t, err)

	// The ConfigMap must contain an empty relay list now (regardless of
	// whether owner-reference deletion has run yet — the controller wrote
	// the empty list explicitly).
	cm := &corev1.ConfigMap{}
	getErr := fakeClient.Get(context.Background(),
		types.NamespacedName{Name: routerPeersConfigMap, Namespace: "test-ns"}, cm)
	require.NoError(t, getErr,
		"peer ConfigMap must still exist after handleDeletion (before owner-reference cleanup)")
	assert.Equal(t, `{"relays":[]}`, cm.Data["peers.json"],
		"handleDeletion must write empty peer list to the CM before removing "+
			"the finalizer; otherwise kubelet's optional-CM volume mount keeps "+
			"the stale file in the relay-router pod and orphan relays linger "+
			"until pod restart (worklog 0467 follow-up)")

	// Finalizer must also be removed (existing behavior preserved)
	updated := &v1.InferenceRelay{}
	require.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated))
	assert.False(t, controllerutil.ContainsFinalizer(updated, InferenceRelayFinalizer))

	// Driver must have been called to destroy the EC2 instance
	require.Len(t, awsDriver.destroyCalls, 1)
	assert.Equal(t, "i-aaa", awsDriver.destroyCalls[0].ID)
}

// TestHandleDeletion_NoConfigMap_StillSucceeds verifies that if the CM
// already doesn't exist (e.g. controller re-runs deletion after a partial
// previous cleanup), handleDeletion does not error.
func TestHandleDeletion_NoConfigMap_StillSucceeds(t *testing.T) {
	scheme := testScheme(t)
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet"},
		Status: v1.InferenceRelayStatus{
			Instances: []v1.RelayInstanceStatus{
				{ID: "i-bbb", Provider: "aws", Region: "us-west-2", State: string(v1.RelayStateHealthy)},
			},
		},
	}
	controllerutil.AddFinalizer(relay, InferenceRelayFinalizer)

	awsDriver := &stubDriver{}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(relay).
		WithStatusSubresource(&v1.InferenceRelay{}).
		Build()
	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers:   map[string]ProviderDriver{"aws": awsDriver},
	}

	_, err := r.handleDeletion(context.Background(), relay)
	require.NoError(t, err, "handleDeletion must succeed even if the peer CM is already absent")

	updated := &v1.InferenceRelay{}
	require.NoError(t, fakeClient.Get(context.Background(), types.NamespacedName{Name: "relay-fleet"}, updated))
	assert.False(t, controllerutil.ContainsFinalizer(updated, InferenceRelayFinalizer))
}
