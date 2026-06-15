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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// PeerEntry is the JSON shape for one relay VM in the ConfigMap.
// Must match cmd/relay-router/fleet.go PeerEntry exactly.
type PeerEntry struct {
	ID       string `json:"id"`
	WgIP     string `json:"wgIP"`
	Provider string `json:"provider"`
	State    string `json:"state"`
}

// PeerConfig is the JSON shape of the relay-router-peers ConfigMap.
type PeerConfig struct {
	Relays []PeerEntry `json:"relays"`
}

// syncPeerConfigMap creates or updates the relay-router-peers ConfigMap
// with the current set of relay VMs.
func syncPeerConfigMap(ctx context.Context, c client.Client, namespace string, owner client.Object, peers []PeerEntry) error {
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

		if owner != nil {
			if err := controllerutil.SetControllerReference(owner, cm, c.Scheme()); err != nil {
				return fmt.Errorf("set owner reference: %w", err)
			}
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
