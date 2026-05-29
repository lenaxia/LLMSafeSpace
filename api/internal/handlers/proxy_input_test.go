package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8smocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	agentoc "github.com/lenaxia/llmsafespace/pkg/agent/opencode"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

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

func TestNormalizedEvents_QuestionAsked(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.broker = NewWorkspaceEventBroker()
	handler.dialect = &agentoc.Dialect{}

	ch := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", ch)

	rawData := `{"id":"que_abc","sessionID":"ses_xyz","questions":[{"question":"Pick?","header":"H","options":[{"label":"A","description":"a"}]}]}`
	handler.onRawEvent("ws-1", "question.asked", rawData)

	// Should receive 2 events: raw opencode.event + normalized agent.question
	evt1 := <-ch
	assert.Equal(t, "opencode.event", evt1.Type)
	assert.Equal(t, "question.asked", evt1.EventType)

	evt2 := <-ch
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

	rawData := `{"id":"que_abc","sessionID":"ses_xyz","answers":[["Go"]]}`
	handler.onRawEvent("ws-1", "question.replied", rawData)

	// Raw event
	evt1 := <-ch
	assert.Equal(t, "opencode.event", evt1.Type)

	// Normalized resolved event
	evt2 := <-ch
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

	rawData := `{"id":"que_abc","sessionID":"ses_xyz"}`
	handler.onRawEvent("ws-1", "question.rejected", rawData)

	<-ch // raw
	evt2 := <-ch
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

	rawData := `{"id":"per_abc","sessionID":"ses_xyz","permission":"shell","patterns":["rm -rf /tmp"]}`
	handler.onRawEvent("ws-1", "permission.asked", rawData)

	<-ch // raw
	evt2 := <-ch
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

	rawData := `{"id":"per_abc","sessionID":"ses_xyz","reply":"always"}`
	handler.onRawEvent("ws-1", "permission.replied", rawData)

	<-ch // raw
	evt2 := <-ch
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
	handler.onRawEvent("ws-1", "session.diff", `{"some":"data"}`)

	evt := <-ch
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

	// Malformed question event — missing required fields
	handler.onRawEvent("ws-1", "question.asked", `{"invalid": true}`)

	// Raw event still published
	evt := <-ch
	assert.Equal(t, "opencode.event", evt.Type)

	// No normalized event (parse failed)
	select {
	case <-ch:
		t.Fatal("should not publish normalized event on parse error")
	default:
		// expected
	}
}

func TestNormalizedEvents_BrokerNil_NoPanic(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil, nil)
	handler.dialect = &agentoc.Dialect{}
	// broker is nil

	// Should not panic
	handler.onRawEvent("ws-1", "question.asked", `{"id":"que_abc","sessionID":"ses_xyz","questions":[]}`)
}
