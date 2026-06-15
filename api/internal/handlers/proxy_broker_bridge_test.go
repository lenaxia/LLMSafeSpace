// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/api/internal/services/eventbroker"
	apitypes "github.com/lenaxia/llmsafespace/api/internal/types"
	k8smocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
)

// TestPublishWorkspaceEvent_BridgePropagatesToBothBrokers proves the S28.5
// strangler-pattern migration: publishWorkspaceEvent fans out to BOTH the
// legacy WorkspaceEventBroker and the new UserEventBroker so subscribers on
// either path receive the event. This makes userBroker.SubscribeWorkspace
// non-dead code (it was previously unreferenced in production per worklog 170
// item S28.5).
func TestPublishWorkspaceEvent_BridgePropagatesToBothBrokers(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil, nil)
	require.NoError(t, err)

	handler.broker = eventbroker.NewWorkspaceEventBroker()
	handler.userBroker = eventbroker.NewUserEventBroker()

	legacySub := handler.broker.Subscribe("ws-bridge")
	defer handler.broker.Unsubscribe("ws-bridge", legacySub)

	userSub, subErr := handler.userBroker.SubscribeWorkspace("ws-bridge")
	require.NoError(t, subErr)
	defer handler.userBroker.UnsubscribeWorkspace("ws-bridge", userSub)

	evt := apitypes.WorkspaceSSEEvent{Type: "workspace.phase", WorkspaceID: "ws-bridge", Phase: "Active"}
	handler.publishWorkspaceEvent("ws-bridge", evt)

	select {
	case got := <-legacySub.Ch:
		assert.Equal(t, "workspace.phase", got.Type)
	case <-time.After(time.Second):
		t.Fatal("legacy broker subscriber did not receive the bridged event")
	}

	select {
	case got := <-userSub.Ch:
		assert.Equal(t, "workspace.phase", got.Type)
		assert.Equal(t, "Active", got.Phase)
	case <-time.After(time.Second):
		t.Fatal("userBroker.SubscribeWorkspace subscriber did not receive the bridged event (S28.5 dead-code regression)")
	}
}

// TestPublishWorkspaceEvent_NilUserBrokerDoesNotPanic proves the bridge is
// safe in tests / older deployments that only wire the legacy broker.
func TestPublishWorkspaceEvent_NilUserBrokerDoesNotPanic(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil, nil)
	require.NoError(t, err)

	handler.broker = eventbroker.NewWorkspaceEventBroker()
	handler.userBroker = nil

	sub := handler.broker.Subscribe("ws-x")
	defer handler.broker.Unsubscribe("ws-x", sub)

	assert.NotPanics(t, func() {
		handler.publishWorkspaceEvent("ws-x", apitypes.WorkspaceSSEEvent{Type: "test"})
	})

	select {
	case <-sub.Ch:
	case <-time.After(time.Second):
		t.Fatal("legacy subscriber must still receive event when userBroker is nil")
	}
}

// TestPublishWorkspaceEvent_NilLegacyBrokerDoesNotPanic proves the bridge is
// safe for forward-compatibility: once the legacy broker is removed entirely,
// callers continue to work via the userBroker path alone.
func TestPublishWorkspaceEvent_NilLegacyBrokerDoesNotPanic(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil, nil)
	require.NoError(t, err)

	handler.broker = nil
	handler.userBroker = eventbroker.NewUserEventBroker()

	sub, subErr := handler.userBroker.SubscribeWorkspace("ws-y")
	require.NoError(t, subErr)
	defer handler.userBroker.UnsubscribeWorkspace("ws-y", sub)

	assert.NotPanics(t, func() {
		handler.publishWorkspaceEvent("ws-y", apitypes.WorkspaceSSEEvent{Type: "test"})
	})

	select {
	case <-sub.Ch:
	case <-time.After(time.Second):
		t.Fatal("userBroker subscriber must receive event when legacy broker is nil")
	}
}
