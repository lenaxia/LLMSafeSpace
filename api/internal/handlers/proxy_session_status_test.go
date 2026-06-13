package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	k8smocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
)

func newHandlerWithMockK8s(t *testing.T) *ProxyHandler {
	t.Helper()
	handler, err := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	require.NoError(t, err)
	return handler
}

func TestOnSessionIdle_PublishesToUserBroker(t *testing.T) {
	handler := newHandlerWithMockK8s(t)

	broker := NewUserEventBroker()
	broker.RecordWorkspaceOwner("ws-1", "user-1")
	handler.userBroker = broker

	handler.activeMu.Lock()
	handler.activeSess["ws-1"] = map[string]bool{"s1": true}
	handler.activeMu.Unlock()

	sub, err := broker.SubscribeUser("user-1")
	require.NoError(t, err)
	defer broker.UnsubscribeUser("user-1", sub)

	handler.onSessionIdle("ws-1", "s1")

	select {
	case evt := <-sub.ch:
		assert.Equal(t, "session.status", evt.Type)
		assert.Equal(t, "idle", evt.Status)
		assert.Equal(t, "s1", evt.SessionID)
		assert.Equal(t, "ws-1", evt.WorkspaceID)
	default:
		t.Fatal("expected user-scoped SSE event for session.status idle")
	}
}

func TestOnSessionActive_PublishesToUserBroker(t *testing.T) {
	handler := newHandlerWithMockK8s(t)

	handler.wsConfigMu.Lock()
	handler.wsConfig["ws-1"] = &workspaceConfig{workspaceID: "ws-1", maxActiveSessions: 5}
	handler.wsConfigMu.Unlock()

	broker := NewUserEventBroker()
	broker.RecordWorkspaceOwner("ws-1", "user-1")
	handler.userBroker = broker

	sub, err := broker.SubscribeUser("user-1")
	require.NoError(t, err)
	defer broker.UnsubscribeUser("user-1", sub)

	handler.onSessionActive("ws-1", "s1")

	select {
	case evt := <-sub.ch:
		assert.Equal(t, "session.status", evt.Type)
		assert.Equal(t, "busy", evt.Status)
		assert.Equal(t, "s1", evt.SessionID)
		assert.Equal(t, "ws-1", evt.WorkspaceID)
	default:
		t.Fatal("expected user-scoped SSE event for session.status busy")
	}
}

func TestOnSessionIdle_SkipsUserBrokerWhenOwnerUnknown(t *testing.T) {
	handler := newHandlerWithMockK8s(t)

	broker := NewUserEventBroker()
	handler.userBroker = broker

	handler.activeMu.Lock()
	handler.activeSess["ws-unknown"] = map[string]bool{"s1": true}
	handler.activeMu.Unlock()

	sub, err := broker.SubscribeUser("user-1")
	require.NoError(t, err)
	defer broker.UnsubscribeUser("user-1", sub)

	handler.onSessionIdle("ws-unknown", "s1")

	select {
	case <-sub.ch:
		t.Fatal("should not publish to user broker when owner unknown")
	default:
	}
}

func TestOnSessionIdle_NoPanicWhenUserBrokerNil(t *testing.T) {
	handler := newHandlerWithMockK8s(t)

	handler.activeMu.Lock()
	handler.activeSess["ws-1"] = map[string]bool{"s1": true}
	handler.activeMu.Unlock()

	assert.NotPanics(t, func() {
		handler.onSessionIdle("ws-1", "s1")
	})
}

func TestOnSessionActive_NoPanicWhenUserBrokerNil(t *testing.T) {
	handler := newHandlerWithMockK8s(t)

	handler.wsConfigMu.Lock()
	handler.wsConfig["ws-1"] = &workspaceConfig{workspaceID: "ws-1", maxActiveSessions: 5}
	handler.wsConfigMu.Unlock()

	assert.NotPanics(t, func() {
		handler.onSessionActive("ws-1", "s1")
	})
}
