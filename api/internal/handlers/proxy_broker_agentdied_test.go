// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/api/internal/services/eventbroker"
	"github.com/lenaxia/llmsafespaces/api/internal/services/sse"
	apitypes "github.com/lenaxia/llmsafespaces/api/internal/types"
	k8smocks "github.com/lenaxia/llmsafespaces/mocks/kubernetes"
)

// TestProxy_OnAgentDied_PublishesAgentDiedToBroker verifies the broker-side
// bridge: invoking onAgentDied(workspaceID) publishes a WorkspaceSSEEvent with
// Type="agent_died" and Data={"reason":"unknown"} that a workspace subscriber
// receives. This proves the wiring onAgentDied -> publishWorkspaceEvent ->
// userBroker.PublishToWorkspace -> subscriber channel.
func TestProxy_OnAgentDied_PublishesAgentDiedToBroker(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil, nil)
	require.NoError(t, err)

	handler.userBroker = eventbroker.NewUserEventBroker()

	sub, subErr := handler.userBroker.SubscribeWorkspace("ws-died")
	require.NoError(t, subErr)
	defer handler.userBroker.UnsubscribeWorkspace("ws-died", sub)

	handler.onAgentDied("ws-died")

	select {
	case got := <-sub.Ch:
		assert.Equal(t, "agent_died", got.Type)
		assert.Equal(t, "ws-died", got.WorkspaceID)
		raw, mErr := json.Marshal(got.Data)
		require.NoError(t, mErr)
		assert.JSONEq(t, `{"reason":"unknown"}`, string(raw))
	case <-time.After(time.Second):
		t.Fatal("broker subscriber must receive agent_died event")
	}
}

// TestProxy_OnAgentDied_NilBrokerDoesNotPanic verifies nil-safety when the
// broker is not wired (e.g. unit tests or before Start).
func TestProxy_OnAgentDied_NilBrokerDoesNotPanic(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil, nil)
	require.NoError(t, err)

	handler.userBroker = nil

	assert.NotPanics(t, func() { handler.onAgentDied("ws-x") })
}

// TestProxy_OnAgentDied_AlsoPublishedToUserChannel (worklog 371 M2) verifies
// that agent_died is published via PublishToUser (the replay-capable channel),
// not just PublishToWorkspace. A frontend reconnecting AFTER the agent died
// must receive the event via replay; without this dual-publish, the
// reconnecting user sees no warning and believes the workspace is healthy.
func TestProxy_OnAgentDied_AlsoPublishedToUserChannel(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil, nil)
	require.NoError(t, err)

	handler.userBroker = eventbroker.NewUserEventBroker()
	handler.userBroker.RecordWorkspaceOwner("ws-m2", "user-m2")

	// Subscribe to the USER channel (has replay buffer).
	userSub, subErr := handler.userBroker.SubscribeUser("user-m2")
	require.NoError(t, subErr)
	defer handler.userBroker.UnsubscribeUser("user-m2", userSub)

	handler.onAgentDied("ws-m2")

	select {
	case got := <-userSub.Ch:
		assert.Equal(t, "agent_died", got.Type)
		assert.Equal(t, "ws-m2", got.WorkspaceID)
		raw, mErr := json.Marshal(got.Data)
		require.NoError(t, mErr)
		assert.JSONEq(t, `{"reason":"unknown"}`, string(raw))
	case <-time.After(time.Second):
		t.Fatal("user-channel subscriber must receive agent_died (M2 dual-publish)")
	}
}

// TestProxy_OnAgentDied_UserChannelReplaySurvivesReconnect (worklog 371 M2)
// verifies the replay capability: subscribe AFTER agent_died fires, then
// call Replay from the last-known ID. The event must be delivered — this
// is the exact scenario M2 fixes (frontend reconnects after agent death).
func TestProxy_OnAgentDied_UserChannelReplaySurvivesReconnect(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil, nil)
	require.NoError(t, err)

	handler.userBroker = eventbroker.NewUserEventBroker()
	handler.userBroker.RecordWorkspaceOwner("ws-m2r", "user-m2r")

	// Fire agent_died with NO active user subscriber (simulates: user is
	// disconnected at the moment of death).
	handler.onAgentDied("ws-m2r")

	// Now the user "reconnects": subscribe fresh.
	reconnectedSub, subErr := handler.userBroker.SubscribeUser("user-m2r")
	require.NoError(t, subErr)
	defer handler.userBroker.UnsubscribeUser("user-m2r", reconnectedSub)

	// Drain the live channel (the subscriber may receive the event live
	// since PublishToUser both buffers and delivers to current subs).
	select {
	case <-reconnectedSub.Ch:
	case <-time.After(200 * time.Millisecond):
	}

	// Replay from ID 0 (everything). The agent_died event must be in the
	// replay buffer. The bool return from Replay is gapDetected (not "found"),
	// so assert on len(entries) instead.
	entries, _ := handler.userBroker.Replay("user-m2r", 0)
	var sawAgentDied bool
	for _, e := range entries {
		if e.Event.Type == "agent_died" {
			sawAgentDied = true
			assert.Equal(t, "ws-m2r", e.Event.WorkspaceID)
		}
	}
	assert.True(t, sawAgentDied,
		"agent_died must be in the user-channel replay buffer so reconnecting frontends receive it (M2)")
}

// TestProxy_OnAgentDied_EventShapeMatchesFrontendContract is a contract test:
// the marshaled JSON shape must match what the frontend AgentDiedEvent type
// narrows on ({"type":"agent_died","workspace_id":"...","data":{"reason":"..."}}).
func TestProxy_OnAgentDied_EventShapeMatchesFrontendContract(t *testing.T) {
	evt := apitypes.WorkspaceSSEEvent{
		Type:        "agent_died",
		WorkspaceID: "ws-died",
		Data:        map[string]string{"reason": "unknown"},
	}
	raw, err := json.Marshal(evt)
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"type":"agent_died","workspace_id":"ws-died","data":{"reason":"unknown"}}`,
		string(raw))
}

// TestProxy_TrackerToBroker_AgentDied_E2E is the end-to-end integration test
// for US-44.1c: it wires a real *sse.Tracker to a real ProxyHandler's
// onAgentDied callback (the single SetOnAgentDied wiring line), with a real
// UserEventBroker + workspace subscriber, then kills the upstream SSE stream
// (EOF after data) and asserts the subscriber receives a WorkspaceSSEEvent with
// Type="agent_died". This proves the full chain tracker -> onAgentDied ->
// publishWorkspaceEvent -> userBroker.PublishToWorkspace -> subscriber.
func TestProxy_TrackerToBroker_AgentDied_E2E(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", `{"type":"session.status","properties":{"sessionID":"sess-1","status":{"type":"busy"}}}`)
		flusher.Flush()
	}))
	t.Cleanup(server.Close)

	httpClient := &http.Client{
		Transport: &redirectTransport{server: server},
		Timeout:   5 * time.Second,
	}

	k8sMock := k8smocks.NewMockKubernetesClient()
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", httpClient, nil)
	require.NoError(t, err)

	handler.userBroker = eventbroker.NewUserEventBroker()

	sub, subErr := handler.userBroker.SubscribeWorkspace("ws-e2e")
	require.NoError(t, subErr)
	defer handler.userBroker.UnsubscribeWorkspace("ws-e2e", sub)

	tracker := sse.NewTracker(httpClient, &testLogger{}, func(workspaceID, sessionID string) {})
	tracker.SetPasswordGetter(fakePWProvider{pw: "test-pw"})
	tracker.SetPodIPResolver(func(workspaceID string) string { return "10.0.0.1" })
	tracker.SetOnAgentDied(handler.onAgentDied)
	t.Cleanup(tracker.Stop)

	tracker.EnsureWatching("ws-e2e")

	select {
	case got := <-sub.Ch:
		assert.Equal(t, "agent_died", got.Type)
		assert.Equal(t, "ws-e2e", got.WorkspaceID)
		raw, mErr := json.Marshal(got.Data)
		require.NoError(t, mErr)
		assert.JSONEq(t, `{"reason":"unknown"}`, string(raw))
	case <-time.After(3 * time.Second):
		t.Fatal("broker subscriber must receive agent_died after upstream SSE EOF post-data")
	}
}
