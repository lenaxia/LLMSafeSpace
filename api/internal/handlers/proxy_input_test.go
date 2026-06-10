// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8smocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	agentoc "github.com/lenaxia/llmsafespace/pkg/agent/opencode"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func recvWithTimeout(t *testing.T, sub *subscriber, what string) WorkspaceSSEEvent {
	t.Helper()
	select {
	case evt := <-sub.ch:
		return evt
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s event", what)
		return WorkspaceSSEEvent{}
	}
}

func newInputTestEnv(t *testing.T) *testEnv {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok, "Basic Auth should be present")
		assert.Equal(t, "opencode", user)
		assert.Equal(t, "test-password", pass)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"method": r.Method,
			"path":   r.URL.Path,
		})
	})

	env.handler.dialect = &agentoc.Dialect{}

	proxy := env.router.Group("/api/v1/workspaces/:id")
	{
		proxy.GET("/question", env.handler.ListQuestions)
		proxy.POST("/question/:requestID/reply", env.handler.QuestionReply)
		proxy.POST("/question/:requestID/reject", env.handler.QuestionReject)
		proxy.GET("/permission", env.handler.ListPermissions)
		proxy.POST("/permission/:requestID/reply", env.handler.PermissionReply)
	}

	return env
}

func TestProxyInput_ListQuestions(t *testing.T) {
	env := newInputTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/question", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "GET", resp["method"])
	assert.Equal(t, "/question", resp["path"])
}

func TestProxyInput_QuestionReply(t *testing.T) {
	env := newInputTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")

	body := strings.NewReader(`{"answers":[["Go"]]}`)
	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/question/que_abc123/reply", body)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "POST", resp["method"])
	assert.Equal(t, "/question/que_abc123/reply", resp["path"])
}

func TestProxyInput_QuestionReject(t *testing.T) {
	env := newInputTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")

	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/question/que_abc123/reject", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "/question/que_abc123/reject", resp["path"])
}

func TestProxyInput_ListPermissions(t *testing.T) {
	env := newInputTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/permission", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "GET", resp["method"])
	assert.Equal(t, "/permission", resp["path"])
}

func TestProxyInput_PermissionReply(t *testing.T) {
	env := newInputTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")

	body := strings.NewReader(`{"reply":"always"}`)
	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/permission/per_xyz789/reply", body)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "POST", resp["method"])
	assert.Equal(t, "/permission/per_xyz789/reply", resp["path"])
}

func TestProxyInput_InvalidQuestionID_NoPrefix(t *testing.T) {
	env := newInputTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")

	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/question/invalid/reply", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid question request ID format")
}

func TestProxyInput_InvalidQuestionID_WrongPrefix(t *testing.T) {
	env := newInputTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")

	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/question/per_abc/reply", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestProxyInput_InvalidPermissionID(t *testing.T) {
	env := newInputTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")

	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/permission/que_abc/reply", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid permission request ID format")
}

func TestProxyInput_WorkspaceNotActive(t *testing.T) {
	env := newInputTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-suspended", "", string(v1.WorkspacePhaseSuspended), "ws-suspended")

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-suspended/question", nil)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestProxyInput_WorkspaceNotFound(t *testing.T) {
	env := newInputTestEnv(t)
	env.wsMock.On("Get", "ws-nonexistent", metav1.GetOptions{}).Return(nil, fmt.Errorf("not found")).Once()

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-nonexistent/question", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestProxyInput_BodyForwardedCorrectly(t *testing.T) {
	var receivedBody string
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		_, _, _ = r.BasicAuth()
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`true`))
	})
	env.handler.dialect = &agentoc.Dialect{}

	proxy := env.router.Group("/api/v1/workspaces/:id")
	proxy.POST("/question/:requestID/reply", env.handler.QuestionReply)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")

	body := `{"answers":[["Go","Rust"]]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/workspaces/ws-1/question/que_abc123/reply", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, body, receivedBody)
}

func TestProxyInput_DialectNil(t *testing.T) {
	env := newTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/workspaces/:id/question", env.handler.ListQuestions)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/workspaces/ws-1/question", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "dialect not configured")
}

// ===== US-16.3: Normalized Event Emission Tests =====

func makeEnvelope(eventType string, props map[string]interface{}) string {
	propsJSON, _ := json.Marshal(props)
	envelope, _ := json.Marshal(map[string]interface{}{
		"type":       eventType,
		"properties": json.RawMessage(propsJSON),
	})
	return string(envelope)
}

func TestNormalizedEvents_QuestionAsked(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	envelope := makeEnvelope("question.asked", map[string]interface{}{
		"id":        "que_abc",
		"sessionID": "ses_xyz",
		"questions": []map[string]interface{}{
			{
				"question": "Pick?",
				"header":   "H",
				"options":  []map[string]string{{"label": "A", "description": "a"}},
			},
		},
	})
	handler.onRawEvent("ws-1", "question.asked", envelope)

	evt1 := recvWithTimeout(t, sub, "opencode.event")
	assert.Equal(t, "opencode.event", evt1.Type)
	assert.Equal(t, "question.asked", evt1.EventType)

	evt2 := recvWithTimeout(t, sub, "agent.question")
	assert.Equal(t, "agent.question", evt2.Type)
	data, err := json.Marshal(evt2.Data)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"id":"que_abc"`)
	assert.Contains(t, string(data), `"session_id":"ses_xyz"`)
}

func TestNormalizedEvents_QuestionResolved(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	envelope := makeEnvelope("question.replied", map[string]interface{}{
		"id":        "que_abc",
		"sessionID": "ses_xyz",
		"answers":   [][]string{{"Go"}},
	})
	handler.onRawEvent("ws-1", "question.replied", envelope)

	evt1 := recvWithTimeout(t, sub, "opencode.event")
	assert.Equal(t, "opencode.event", evt1.Type)

	evt2 := recvWithTimeout(t, sub, "agent.question.resolved")
	assert.Equal(t, "agent.question.resolved", evt2.Type)
	data := evt2.Data.(map[string]string)
	assert.Equal(t, "que_abc", data["request_id"])
	assert.Equal(t, "ses_xyz", data["session_id"])
}

func TestNormalizedEvents_QuestionRejected(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	envelope := makeEnvelope("question.rejected", map[string]interface{}{
		"id":        "que_abc",
		"sessionID": "ses_xyz",
	})
	handler.onRawEvent("ws-1", "question.rejected", envelope)

	recvWithTimeout(t, sub, "opencode.event")
	evt2 := recvWithTimeout(t, sub, "agent.question.resolved")
	assert.Equal(t, "agent.question.resolved", evt2.Type)
}

func TestNormalizedEvents_PermissionAsked(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()
	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)
	ws := &v1.Workspace{
		Spec:   v1.WorkspaceSpec{AutoApprovePermissions: false},
		Status: v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive, PodIP: "10.0.0.1"},
	}
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(ws, nil)

	handler, _ := NewProxyHandler(k8sMock, &testLogger{}, "default", nil, nil)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	envelope := makeEnvelope("permission.asked", map[string]interface{}{
		"id":         "per_abc",
		"sessionID":  "ses_xyz",
		"permission": "shell",
		"patterns":   []string{"rm -rf /tmp"},
	})
	handler.onRawEvent("ws-1", "permission.asked", envelope)

	recvWithTimeout(t, sub, "opencode.event")
	evt2 := recvWithTimeout(t, sub, "agent.permission")
	assert.Equal(t, "agent.permission", evt2.Type)
	data, _ := json.Marshal(evt2.Data)
	assert.Contains(t, string(data), `"id":"per_abc"`)
	assert.Contains(t, string(data), `"permission":"shell"`)
}

func TestNormalizedEvents_PermissionResolved(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	envelope := makeEnvelope("permission.replied", map[string]interface{}{
		"id":        "per_abc",
		"sessionID": "ses_xyz",
		"reply":     "always",
	})
	handler.onRawEvent("ws-1", "permission.replied", envelope)

	recvWithTimeout(t, sub, "opencode.event")
	evt2 := recvWithTimeout(t, sub, "agent.permission.resolved")
	assert.Equal(t, "agent.permission.resolved", evt2.Type)
	data := evt2.Data.(map[string]string)
	assert.Equal(t, "per_abc", data["request_id"])
	assert.Equal(t, "always", data["reply"])
}

func TestNormalizedEvents_RawEventAlwaysPublished(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	envelope := makeEnvelope("session.diff", map[string]interface{}{"some": "data"})
	handler.onRawEvent("ws-1", "session.diff", envelope)

	evt := recvWithTimeout(t, sub, "opencode.event")
	assert.Equal(t, "opencode.event", evt.Type)
	assert.Equal(t, "session.diff", evt.EventType)

	select {
	case <-sub.ch:
		t.Fatal("unexpected second event for unrelated event type")
	default:
	}
}

func TestNormalizedEvents_ParseError_NoNormalizedEvent(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	envelope := makeEnvelope("question.asked", map[string]interface{}{"invalid": true})
	handler.onRawEvent("ws-1", "question.asked", envelope)

	evt := recvWithTimeout(t, sub, "opencode.event")
	assert.Equal(t, "opencode.event", evt.Type)

	select {
	case <-sub.ch:
		t.Fatal("should not publish normalized event on parse error")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestNormalizedEvents_BrokerNil_NoPanic(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.dialect = &agentoc.Dialect{}

	envelope := `{"type":"question.asked","properties":{"id":"que_abc","sessionID":"ses_xyz","questions":[]}}`
	handler.onRawEvent("ws-1", "question.asked", envelope)
}

// ===== US-16.3 integration: real wiring through SSETracker.processEvent =====

func makePermissionAskedEvent(reqID, sessionID, permission string, patterns []string) string {
	props := map[string]interface{}{
		"id":         reqID,
		"sessionID":  sessionID,
		"permission": permission,
		"patterns":   patterns,
	}
	propsJSON, _ := json.Marshal(props)
	envelope, _ := json.Marshal(map[string]interface{}{
		"type":       "permission.asked",
		"properties": json.RawMessage(propsJSON),
	})
	return string(envelope)
}

func makeQuestionAskedEvent(reqID, sessionID string) string {
	props := map[string]interface{}{
		"id":        reqID,
		"sessionID": sessionID,
		"questions": []map[string]interface{}{
			{
				"question": "Pick?",
				"header":   "H",
				"options": []map[string]string{
					{"label": "A", "description": "a"},
				},
			},
		},
	}
	propsJSON, _ := json.Marshal(props)
	envelope, _ := json.Marshal(map[string]interface{}{
		"type":       "question.asked",
		"properties": json.RawMessage(propsJSON),
	})
	return string(envelope)
}

func makeResolutionEvent(eventType, reqID, sessionID, reply string) string {
	props := map[string]interface{}{
		"id":        reqID,
		"sessionID": sessionID,
	}
	if reply != "" {
		props["reply"] = reply
	}
	propsJSON, _ := json.Marshal(props)
	envelope, _ := json.Marshal(map[string]interface{}{
		"type":       eventType,
		"properties": json.RawMessage(propsJSON),
	})
	return string(envelope)
}

func TestNormalizedEvents_E2E_PermissionAsked_ViaProcessEvent(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()
	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)
	ws := &v1.Workspace{
		Spec:   v1.WorkspaceSpec{AutoApprovePermissions: false},
		Status: v1.WorkspaceStatus{Phase: v1.WorkspacePhaseActive, PodIP: "10.0.0.1"},
	}
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(ws, nil)

	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil, nil)
	require.NoError(t, err)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	tracker := newTestSSETracker(func(string, string) {})
	tracker.SetOnRawEvent(handler.onRawEvent)

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	envelope := makePermissionAskedEvent("per_abc", "ses_xyz", "shell", []string{"rm -rf /tmp"})
	tracker.processEvent("ws-1", envelope)

	evt1 := recvWithTimeout(t, sub, "opencode.event (permission.asked)")
	assert.Equal(t, "opencode.event", evt1.Type)
	assert.Equal(t, "permission.asked", evt1.EventType)

	evt2 := recvWithTimeout(t, sub, "agent.permission")
	assert.Equal(t, "agent.permission", evt2.Type)
	data, mErr := json.Marshal(evt2.Data)
	require.NoError(t, mErr)
	assert.Contains(t, string(data), `"id":"per_abc"`)
	assert.Contains(t, string(data), `"session_id":"ses_xyz"`)
	assert.Contains(t, string(data), `"permission":"shell"`)
}

func TestNormalizedEvents_E2E_QuestionAsked_ViaProcessEvent(t *testing.T) {
	handler, err := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	require.NoError(t, err)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	tracker := newTestSSETracker(func(string, string) {})
	tracker.SetOnRawEvent(handler.onRawEvent)

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	envelope := makeQuestionAskedEvent("que_abc", "ses_xyz")
	tracker.processEvent("ws-1", envelope)

	evt1 := recvWithTimeout(t, sub, "opencode.event (question.asked)")
	assert.Equal(t, "opencode.event", evt1.Type)
	assert.Equal(t, "question.asked", evt1.EventType)

	evt2 := recvWithTimeout(t, sub, "agent.question")
	assert.Equal(t, "agent.question", evt2.Type)
	data, mErr := json.Marshal(evt2.Data)
	require.NoError(t, mErr)
	assert.Contains(t, string(data), `"id":"que_abc"`)
	assert.Contains(t, string(data), `"session_id":"ses_xyz"`)
}

func TestNormalizedEvents_E2E_PermissionResolved_ViaProcessEvent(t *testing.T) {
	handler, err := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	require.NoError(t, err)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	tracker := newTestSSETracker(func(string, string) {})
	tracker.SetOnRawEvent(handler.onRawEvent)

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	envelope := makeResolutionEvent("permission.replied", "per_abc", "ses_xyz", "always")
	tracker.processEvent("ws-1", envelope)

	recvWithTimeout(t, sub, "opencode.event (permission.replied)")
	evt2 := recvWithTimeout(t, sub, "agent.permission.resolved")
	assert.Equal(t, "agent.permission.resolved", evt2.Type)
	data := evt2.Data.(map[string]string)
	assert.Equal(t, "per_abc", data["request_id"])
	assert.Equal(t, "ses_xyz", data["session_id"])
	assert.Equal(t, "always", data["reply"])
}

func TestNormalizedEvents_E2E_QuestionResolved_ViaProcessEvent(t *testing.T) {
	handler, err := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	require.NoError(t, err)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	tracker := newTestSSETracker(func(string, string) {})
	tracker.SetOnRawEvent(handler.onRawEvent)

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	envelope := makeResolutionEvent("question.replied", "que_abc", "ses_xyz", "")
	tracker.processEvent("ws-1", envelope)

	recvWithTimeout(t, sub, "opencode.event (question.replied)")
	evt2 := recvWithTimeout(t, sub, "agent.question.resolved")
	assert.Equal(t, "agent.question.resolved", evt2.Type)
	data := evt2.Data.(map[string]string)
	assert.Equal(t, "que_abc", data["request_id"])
	assert.Equal(t, "ses_xyz", data["session_id"])
}
