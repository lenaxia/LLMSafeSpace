// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// TestHandleDeletion_ClearsPeerConfigMap pins the deletion-cleanup
// fix from worklog 0467+0468: when the InferenceRelay CR is deleted,
// the controller must write {"relays":[]} to the peer ConfigMap so the
// relay-router observes the cleared fleet.
//
// The CM has no ownerReference (worklog 0468 fix) so it persists across
// CR deletions; the empty-list write here propagates cleanly through
// kubelet's volume-mount sync to the router pod, which drops the
// orphaned relays from its in-memory fleet.
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

	// The ConfigMap must contain an empty relay list now. The CM has no
	// ownerReference (worklog 0468) so it persists after handleDeletion;
	// kubelet propagates the cleared content to the router pod's volume
	// mount.
	cm := &corev1.ConfigMap{}
	getErr := fakeClient.Get(context.Background(),
		types.NamespacedName{Name: routerPeersConfigMap, Namespace: "test-ns"}, cm)
	require.NoError(t, getErr,
		"peer ConfigMap must still exist after handleDeletion (no ownerReference, no GC)")
	assert.Equal(t, `{"relays":[]}`, cm.Data["peers.json"],
		"handleDeletion must write empty peer list to the CM so kubelet "+
			"propagates the cleared content to the relay-router pod's volume "+
			"mount and the router drops orphaned relays from its in-memory "+
			"fleet (worklog 0467+0468)")

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

// TestHandleDeletion_PeerCMUpdateFails_StillRemovesFinalizer pins the
// best-effort error semantics flagged in PR #334 review: if the CM-clear
// fails (e.g. transient API server error during graceful shutdown),
// handleDeletion must NOT block on it. Terminating EC2 instances and
// removing the finalizer is more important than this cosmetic
// router-cache cleanup.
//
// A regression that converts the logger.Error to `return err` would
// strand the InferenceRelay CR in a deletion loop forever, leaving the
// already-destroyed EC2 instances orphaned in the spec but the CR
// undeletable.
func TestHandleDeletion_PeerCMUpdateFails_StillRemovesFinalizer(t *testing.T) {
	scheme := testScheme(t)

	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet"},
		Status: v1.InferenceRelayStatus{
			Instances: []v1.RelayInstanceStatus{
				{ID: "i-ccc", Provider: "aws", Region: "us-west-2", State: string(v1.RelayStateHealthy)},
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
			"peers.json": `{"relays":[{"id":"i-ccc","endpoint":"1.2.3.4:8080","provider":"aws","state":"healthy","token":"t"}]}`,
		},
	}

	awsDriver := &stubDriver{}

	// Inject an error specifically on the ConfigMap Update path. The CM
	// already exists (so syncPeerConfigMap takes the Update branch, not
	// the Create branch). Other Update calls (e.g. removing the finalizer
	// on the InferenceRelay) must NOT be affected.
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(relay, existingCM).
		WithStatusSubresource(&v1.InferenceRelay{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if _, isCM := obj.(*corev1.ConfigMap); isCM {
					return errInjectedCMUpdate
				}
				return c.Update(ctx, obj, opts...)
			},
		}).
		Build()
	r := &InferenceRelayReconciler{
		Client:    fakeClient,
		Scheme:    scheme,
		Namespace: "test-ns",
		Drivers:   map[string]ProviderDriver{"aws": awsDriver},
	}

	_, err := r.handleDeletion(context.Background(), relay)
	require.NoError(t, err,
		"handleDeletion must NOT propagate a CM-clear failure — terminating EC2 "+
			"instances and removing the finalizer is more important than this "+
			"cosmetic router-cache cleanup")

	// Finalizer must be removed despite the CM update error
	updated := &v1.InferenceRelay{}
	require.NoError(t, fakeClient.Get(context.Background(),
		types.NamespacedName{Name: "relay-fleet"}, updated))
	assert.False(t, controllerutil.ContainsFinalizer(updated, InferenceRelayFinalizer),
		"finalizer must still be removed even when CM-clear fails")

	// EC2 destroy must still have run
	require.Len(t, awsDriver.destroyCalls, 1)
	assert.Equal(t, "i-ccc", awsDriver.destroyCalls[0].ID)
}

var errInjectedCMUpdate = errors.New("injected: API server unavailable for ConfigMap Update")

// TestHandleDeletion_PeerCMHasNoOwnerRef pins the lifecycle property that
// addresses the GC race observed in worklog 0468: the CM written by
// syncPeerConfigMap (including the empty-list write during deletion) must
// have NO ownerReference to the InferenceRelay CR. With ownerRef, GC would
// delete the CM as soon as the finalizer is removed, racing with kubelet's
// volume-mount sync — kubelet typically loses, leaving the relay-router
// pod with the stale pre-deletion peer list until pod restart.
//
// This test asserts the controller-driven lifecycle: after handleDeletion
// completes, the CM still exists with empty peers (not removed by GC,
// because there is no GC trigger).
func TestHandleDeletion_PeerCMHasNoOwnerRef(t *testing.T) {
	scheme := testScheme(t)
	relay := &v1.InferenceRelay{
		ObjectMeta: metav1.ObjectMeta{Name: "relay-fleet"},
		Status: v1.InferenceRelayStatus{
			Instances: []v1.RelayInstanceStatus{
				{ID: "i-ddd", Provider: "aws", Region: "us-west-2", State: string(v1.RelayStateHealthy)},
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
			"peers.json": `{"relays":[{"id":"i-ddd","endpoint":"1.2.3.4:8080","provider":"aws","state":"healthy","token":"t"}]}`,
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

	cm := &corev1.ConfigMap{}
	require.NoError(t, fakeClient.Get(context.Background(),
		types.NamespacedName{Name: routerPeersConfigMap, Namespace: "test-ns"}, cm))
	assert.Empty(t, cm.OwnerReferences,
		"peer ConfigMap must NOT have an ownerReference — owner-ref races "+
			"with kubelet volume-mount sync on CR deletion and orphans "+
			"relays in the router's in-memory fleet (worklog 0468)")
	assert.Equal(t, `{"relays":[]}`, cm.Data["peers.json"])
}
