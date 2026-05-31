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

// recvWithTimeout reads from ch with a 2-second deadline; fails the test if
// the channel does not deliver in time. Used by the E2E broker integration
// tests to surface dropped events as a fast failure rather than a hang.
func recvWithTimeout(t *testing.T, ch chan WorkspaceSSEEvent, what string) WorkspaceSSEEvent {
	t.Helper()
	select {
	case evt := <-ch:
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

	// Set dialect on the handler
	env.handler.dialect = &agentoc.Dialect{}

	// Register input routes
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
	// Set up workspace mock to return error for "ws-nonexistent"
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
	// handler.dialect is nil by default in newTestEnv
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
//
// These tests drive onRawEvent directly with the production wire format —
// the full opencode SSE envelope {"type":"...","properties":{...}}. They
// complement the broader E2E tests further down the file that drive
// SSETracker.processEvent (the real upstream caller of onRawEvent).

// makeEnvelope wraps inner properties JSON in the opencode SSE envelope.
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

	ch := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", ch)

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

	// Should receive 2 events: raw opencode.event + normalized agent.question
	evt1 := recvWithTimeout(t, ch, "opencode.event")
	assert.Equal(t, "opencode.event", evt1.Type)
	assert.Equal(t, "question.asked", evt1.EventType)

	evt2 := recvWithTimeout(t, ch, "agent.question")
	assert.Equal(t, "agent.question", evt2.Type)
	// Verify the data is a parsed QuestionRequest
	data, err := json.Marshal(evt2.Data)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"id":"que_abc"`)
	assert.Contains(t, string(data), `"session_id":"ses_xyz"`)
}

func TestNormalizedEvents_QuestionResolved(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	ch := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", ch)

	envelope := makeEnvelope("question.replied", map[string]interface{}{
		"id":        "que_abc",
		"sessionID": "ses_xyz",
		"answers":   [][]string{{"Go"}},
	})
	handler.onRawEvent("ws-1", "question.replied", envelope)

	// Raw event
	evt1 := recvWithTimeout(t, ch, "opencode.event")
	assert.Equal(t, "opencode.event", evt1.Type)

	// Normalized resolved event
	evt2 := recvWithTimeout(t, ch, "agent.question.resolved")
	assert.Equal(t, "agent.question.resolved", evt2.Type)
	data := evt2.Data.(map[string]string)
	assert.Equal(t, "que_abc", data["request_id"])
	assert.Equal(t, "ses_xyz", data["session_id"])
}

func TestNormalizedEvents_QuestionRejected(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	ch := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", ch)

	envelope := makeEnvelope("question.rejected", map[string]interface{}{
		"id":        "que_abc",
		"sessionID": "ses_xyz",
	})
	handler.onRawEvent("ws-1", "question.rejected", envelope)

	recvWithTimeout(t, ch, "opencode.event") // raw
	evt2 := recvWithTimeout(t, ch, "agent.question.resolved")
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

	ch := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", ch)

	envelope := makeEnvelope("permission.asked", map[string]interface{}{
		"id":         "per_abc",
		"sessionID":  "ses_xyz",
		"permission": "shell",
		"patterns":   []string{"rm -rf /tmp"},
	})
	handler.onRawEvent("ws-1", "permission.asked", envelope)

	recvWithTimeout(t, ch, "opencode.event") // raw
	evt2 := recvWithTimeout(t, ch, "agent.permission")
	assert.Equal(t, "agent.permission", evt2.Type)
	data, _ := json.Marshal(evt2.Data)
	assert.Contains(t, string(data), `"id":"per_abc"`)
	assert.Contains(t, string(data), `"permission":"shell"`)
}

func TestNormalizedEvents_PermissionResolved(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	ch := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", ch)

	envelope := makeEnvelope("permission.replied", map[string]interface{}{
		"id":        "per_abc",
		"sessionID": "ses_xyz",
		"reply":     "always",
	})
	handler.onRawEvent("ws-1", "permission.replied", envelope)

	recvWithTimeout(t, ch, "opencode.event") // raw
	evt2 := recvWithTimeout(t, ch, "agent.permission.resolved")
	assert.Equal(t, "agent.permission.resolved", evt2.Type)
	data := evt2.Data.(map[string]string)
	assert.Equal(t, "per_abc", data["request_id"])
	assert.Equal(t, "always", data["reply"])
}

func TestNormalizedEvents_RawEventAlwaysPublished(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	ch := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", ch)

	// Unrelated event — only raw should be published
	envelope := makeEnvelope("session.diff", map[string]interface{}{"some": "data"})
	handler.onRawEvent("ws-1", "session.diff", envelope)

	evt := recvWithTimeout(t, ch, "opencode.event")
	assert.Equal(t, "opencode.event", evt.Type)
	assert.Equal(t, "session.diff", evt.EventType)

	// Channel should be empty (no normalized event for session.diff)
	select {
	case <-ch:
		t.Fatal("unexpected second event for unrelated event type")
	default:
		// expected
	}
}

func TestNormalizedEvents_ParseError_NoNormalizedEvent(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	ch := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", ch)

	// Malformed question event — properties present but missing required fields
	envelope := makeEnvelope("question.asked", map[string]interface{}{"invalid": true})
	handler.onRawEvent("ws-1", "question.asked", envelope)

	// Raw event still published
	evt := recvWithTimeout(t, ch, "opencode.event")
	assert.Equal(t, "opencode.event", evt.Type)

	// No normalized event (parse failed)
	select {
	case <-ch:
		t.Fatal("should not publish normalized event on parse error")
	case <-time.After(100 * time.Millisecond):
		// expected
	}
}

func TestNormalizedEvents_BrokerNil_NoPanic(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.dialect = &agentoc.Dialect{}
	// broker is nil

	// Should not panic
	envelope := `{"type":"question.asked","properties":{"id":"que_abc","sessionID":"ses_xyz","questions":[]}}`
	handler.onRawEvent("ws-1", "question.asked", envelope)
}

// ===== US-16.3 integration: real wiring through SSETracker.processEvent =====
//
// These tests drive the production entry point — SSETracker.processEvent — with
// real opencode SSE envelope data, exactly as the live wire format delivers it
// (envelope-with-properties). They prove that the
//   tracker.processEvent → onRawEvent → emitNormalizedInputEvent → broker.Publish
// chain produces normalized agent.* events for browser subscribers.
//
// The earlier TestNormalizedEvents_* tests pass flat properties directly to
// onRawEvent and therefore enforced an incorrect contract — the production
// pipeline always passes the full envelope. These integration tests catch the
// envelope-vs-properties bug observed in worklog 0072.

// makePermissionAskedEvent builds a real opencode permission.asked envelope.
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

// makeQuestionAskedEvent builds a real opencode question.asked envelope.
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

// makeResolutionEvent builds a real opencode question.replied/permission.replied envelope.
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
	// Configure auto-approve OFF so the normalized event is published
	// (auto-approve path consumes the event before it reaches the broker).
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

	ch := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", ch)

	envelope := makePermissionAskedEvent("per_abc", "ses_xyz", "shell", []string{"rm -rf /tmp"})
	tracker.processEvent("ws-1", envelope)

	// Always-published raw event
	evt1 := recvWithTimeout(t, ch, "opencode.event (permission.asked)")
	assert.Equal(t, "opencode.event", evt1.Type)
	assert.Equal(t, "permission.asked", evt1.EventType)

	// Normalized event — this is the one that was silently dropped before the fix
	evt2 := recvWithTimeout(t, ch, "agent.permission")
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

	ch := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", ch)

	envelope := makeQuestionAskedEvent("que_abc", "ses_xyz")
	tracker.processEvent("ws-1", envelope)

	evt1 := recvWithTimeout(t, ch, "opencode.event (question.asked)")
	assert.Equal(t, "opencode.event", evt1.Type)
	assert.Equal(t, "question.asked", evt1.EventType)

	evt2 := recvWithTimeout(t, ch, "agent.question")
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

	ch := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", ch)

	envelope := makeResolutionEvent("permission.replied", "per_abc", "ses_xyz", "always")
	tracker.processEvent("ws-1", envelope)

	recvWithTimeout(t, ch, "opencode.event (permission.replied)") // raw
	evt2 := recvWithTimeout(t, ch, "agent.permission.resolved")
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

	ch := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", ch)

	envelope := makeResolutionEvent("question.replied", "que_abc", "ses_xyz", "")
	tracker.processEvent("ws-1", envelope)

	recvWithTimeout(t, ch, "opencode.event (question.replied)") // raw
	evt2 := recvWithTimeout(t, ch, "agent.question.resolved")
	assert.Equal(t, "agent.question.resolved", evt2.Type)
	data := evt2.Data.(map[string]string)
	assert.Equal(t, "que_abc", data["request_id"])
	assert.Equal(t, "ses_xyz", data["session_id"])
}
