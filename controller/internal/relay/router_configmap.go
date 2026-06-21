// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relay

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PeerEntry is the JSON shape for one relay VM in the ConfigMap.
// Must match cmd/relay-router/fleet.go PeerEntry exactly. The Endpoint is the
// relay VM's public IP/host:port that the router dials over HTTP. Token is the
// per-VM shared secret the router presents in the X-Relay-Token header.
type PeerEntry struct {
	ID       string `json:"id"`
	Endpoint string `json:"endpoint"`
	Provider string `json:"provider"`
	State    string `json:"state"`
	Token    string `json:"token"`
}

// PeerConfig is the JSON shape of the relay-router-peers ConfigMap.
type PeerConfig struct {
	Relays []PeerEntry `json:"relays"`
}

// syncPeerConfigMap creates or updates the relay-router-peers ConfigMap
// with the current set of relay VMs.
//
// The CM is intentionally NOT given an ownerReference to the InferenceRelay
// CR. If it were, GC would delete the CM as soon as the CR's finalizer
// is removed, racing with kubelet's volume-mount sync. The window between
// "controller writes empty list" and "GC deletes CM" is typically too short
// for kubelet to propagate the cleared content to the relay-router pod's
// volume mount, leaving the router with stale peer data until pod restart.
//
// By managing the CM lifecycle directly (no ownerRef), the empty-list write
// from handleDeletion stays in the CM and propagates cleanly. A subsequent
// CR creation re-uses the same CM, overwriting the empty list with the
// fresh fleet. See worklog 0468 for the discovery that motivated this.
//
// The `owner` parameter is preserved to keep the API stable, but is unused
// for the CM ownerReference.
func syncPeerConfigMap(ctx context.Context, c client.Client, namespace string, owner client.Object, peers []PeerEntry) error {
	_ = owner // intentionally unused — see function-level comment
	data, err := json.Marshal(PeerConfig{Relays: peers})
	if err != nil {
		return fmt.Errorf("marshal peer config: %w", err)
	}

	cmName := types.NamespacedName{Name: routerPeersConfigMap, Namespace: namespace}
	existing := &corev1.ConfigMap{}

	if err := c.Get(ctx, cmName, existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("get peers ConfigMap: %w", err)
		}

		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      routerPeersConfigMap,
				Namespace: namespace,
			},
			Data: map[string]string{
				"peers.json": string(data),
			},
		}

		return c.Create(ctx, cm)
	}

	currentData, ok := existing.Data["peers.json"]
	if ok && currentData == string(data) {
		return nil
	}

	if existing.Data == nil {
		existing.Data = make(map[string]string)
	}
	existing.Data["peers.json"] = string(data)
	return c.Update(ctx, existing)
}
