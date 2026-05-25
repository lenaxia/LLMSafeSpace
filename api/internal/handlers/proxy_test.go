package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	k8smocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

type testLogger struct{}

func (l *testLogger) Debug(msg string, kv ...interface{})                  {}
func (l *testLogger) Info(msg string, kv ...interface{})                   {}
func (l *testLogger) Warn(msg string, kv ...interface{})                   {}
func (l *testLogger) Error(msg string, err error, kv ...interface{})       {}
func (l *testLogger) Fatal(msg string, err error, kv ...interface{})       {}
func (l *testLogger) With(kv ...interface{}) pkginterfaces.LoggerInterface { return l }
func (l *testLogger) Sync() error                                          { return nil }

type redirectTransport struct {
	server *httptest.Server
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.server.URL, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

type failFirstTransport struct {
	server   *httptest.Server
	attempts int32
	failIP   string
	newIP    string
}

func (t *failFirstTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host
	if strings.HasPrefix(host, t.failIP) {
		if atomic.AddInt32(&t.attempts, 1) == 1 {
			return nil, fmt.Errorf("dial tcp %s: connection refused", t.failIP)
		}
		req.URL.Host = t.newIP
	}
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.server.URL, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

type alwaysFailTransport struct{}

func (t *alwaysFailTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("dial tcp %s: connection refused", req.URL.Host)
}

type testEnv struct {
	handler   *ProxyHandler
	k8sMock   *k8smocks.MockKubernetesClient
	llmMock   *k8smocks.MockLLMSafespaceV1Interface
	wsMock    *k8smocks.MockWorkspaceInterface
	clientset *k8sfake.Clientset
	backend   *httptest.Server
	router    *gin.Engine
	log       *testLogger
}

func newTestEnv(t *testing.T) *testEnv {
	return newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok, "Basic Auth should be present")
		assert.Equal(t, "opencode", user)
		assert.Equal(t, "test-password", pass)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"method": r.Method,
			"path":   r.URL.Path,
			"query":  r.URL.RawQuery,
		})
	})
}

func newTestEnvWithBackend(t *testing.T, backendHandler http.HandlerFunc) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	backend := httptest.NewServer(backendHandler)
	t.Cleanup(func() { backend.Close() })

	transport := &redirectTransport{server: backend}
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()

	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	fakeClientset := k8sfake.NewSimpleClientset()
	k8sMock.On("Clientset").Return(fakeClientset)

	log := &testLogger{}
	handler, err := NewProxyHandler(k8sMock, log, "default", httpClient)
	require.NoError(t, err)

	router := gin.New()
	proxy := router.Group("/api/v1/workspaces/:id")
	{
		proxy.POST("/sessions", handler.CreateSession)
		proxy.GET("/sessions", handler.ListSessions)
		proxy.POST("/sessions/:sessionId/message", handler.SendMessage)
		proxy.POST("/sessions/:sessionId/prompt", handler.SendPromptAsync)
		proxy.GET("/sessions/:sessionId/message", handler.GetHistory)
		proxy.POST("/sessions/:sessionId/abort", handler.AbortSession)
		proxy.GET("/events", handler.StreamEvents)
	}

	return &testEnv{
		handler:   handler,
		k8sMock:   k8sMock,
		llmMock:   llmMock,
		wsMock:    wsMock,
		clientset: fakeClientset,
		backend:   backend,
		router:    router,
		log:       log,
	}
}

func (e *testEnv) setupWorkspaceMulti(workspaceID string, crds ...*v1.Workspace) {
	for _, crd := range crds {
		e.wsMock.On("Get", workspaceID, metav1.GetOptions{}).Return(crd, nil).Once()
	}
}

func (e *testEnv) setupPasswordWithT(t *testing.T, workspaceID, password string) {
	secret := makePasswordSecret(workspaceID, password)
	_, err := e.clientset.CoreV1().Secrets("default").Create(context.Background(), secret, metav1.CreateOptions{})
	require.NoError(t, err)
}

func (e *testEnv) setupWorkspaceWithT(t *testing.T, name string, maxSessions int) {
	ws := makeWorkspaceCRD(name, maxSessions)
	e.wsMock.On("Get", name, metav1.GetOptions{}).Return(ws, nil).Maybe()
}

func (e *testEnv) setupWorkspacePodWithT(t *testing.T, workspaceID, podIP, phase, _ string) {
	ws := makeWorkspaceCRDWithStatus(workspaceID, podIP, phase, "")
	e.wsMock.On("Get", workspaceID, metav1.GetOptions{}).Return(ws, nil).Maybe()
}
func (e *testEnv) doRequestWithT(t *testing.T, method, path string, body io.Reader) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	e.router.ServeHTTP(w, req)
	return w
}

func TestProxy_ProxiesGETRequest(t *testing.T) {
	env := newTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "GET", resp["method"])
	assert.Equal(t, "/session", resp["path"])
}

func TestProxy_ProxiesPOSTRequest(t *testing.T) {
	env := newTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	body := strings.NewReader(`{"message":"hello"}`)
	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions", body)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "POST", resp["method"])
	assert.Equal(t, "/session", resp["path"])
}

func TestProxy_SendsBasicAuth(t *testing.T) {
	var capturedUser, capturedPass string
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		capturedUser, capturedPass, _ = r.BasicAuth()
		w.WriteHeader(http.StatusOK)
	})
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "my-secret-pw")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "opencode", capturedUser)
	assert.Equal(t, "my-secret-pw", capturedPass)
}

func TestProxy_ForwardsQueryParameters(t *testing.T) {
	env := newTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions?limit=10&offset=0", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "limit=10&offset=0", resp["query"])
}

func TestProxy_StreamingResponse(t *testing.T) {
	// StreamEvents is now broker-based; it no longer proxies to the pod.
	// Verify: with a broker attached, the endpoint sets SSE headers and returns 200.
	env := newTestEnv(t)
	env.handler.broker = NewWorkspaceEventBroker()
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")

	cancel, body, header, code := doStreamingRequest(env.router, "/api/v1/workspaces/ws-1/events")
	defer body.Close()

	// Allow the handler to write response headers.
	time.Sleep(30 * time.Millisecond)
	cancel()

	assert.Equal(t, http.StatusOK, *code)
	assert.Equal(t, "text/event-stream", header.Get("Content-Type"))
	assert.Equal(t, "no-cache", header.Get("Cache-Control"))
}

// TestProxy_SSEStreamPassthrough previously tested transparent proxy forwarding
// to the pod's /event endpoint. StreamEvents is now broker-based and no longer
// proxies to the pod; passthrough behaviour is covered by stream_events_test.go.

func TestProxy_RetriesOnStaleIP(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer backend.Close()

	transport := &failFirstTransport{
		server: backend,
		failIP: "10.0.0.1:4096",
		newIP:  "10.0.0.2:4096",
	}
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()

	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	fakeClientset := k8sfake.NewSimpleClientset()
	k8sMock.On("Clientset").Return(fakeClientset)

	oldCRD := makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	newCRD := makeWorkspaceCRDWithStatus("ws-1", "10.0.0.2", string(v1.WorkspacePhaseActive), "ws-1")
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(oldCRD, nil).Once()
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(newCRD, nil).Once()

	secret := makePasswordSecret("ws-1", "test-password")
	_, err := fakeClientset.CoreV1().Secrets("default").Create(context.Background(), secret, metav1.CreateOptions{})
	require.NoError(t, err)

	ws := makeWorkspaceCRD("ws-1", 5)
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(ws, nil)

	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", httpClient)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/workspaces/:id/sessions", handler.ListSessions)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/workspaces/ws-1/sessions", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int32(1), atomic.LoadInt32(&transport.attempts))
}

func TestProxy_ConnectionFailureReturns503(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()

	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	fakeClientset := k8sfake.NewSimpleClientset()
	k8sMock.On("Clientset").Return(fakeClientset)

	crd := makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(crd, nil)
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(crd, nil)

	secret := makePasswordSecret("ws-1", "test-password")
	_, err := fakeClientset.CoreV1().Secrets("default").Create(context.Background(), secret, metav1.CreateOptions{})
	require.NoError(t, err)

	ws := makeWorkspaceCRD("ws-1", 5)
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(ws, nil)

	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", &http.Client{
		Transport: &alwaysFailTransport{},
		Timeout:   2 * time.Second,
	})
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/workspaces/:id/sessions", handler.ListSessions)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/workspaces/ws-1/sessions", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "10", w.Header().Get("Retry-After"))
	assert.Contains(t, w.Body.String(), "workspace connection failed")
}

func TestProxy_WorkspaceNotRunning(t *testing.T) {
	tests := []struct {
		name  string
		phase string
		podIP string
	}{
		{"Pending phase", "Pending", ""},
		{"Creating phase", "Creating", ""},
		{"Suspended phase", "Suspended", ""},
		{"Running but no PodIP", string(v1.WorkspacePhaseActive), ""},
		{"Suspending phase", "Suspending", "10.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t)
			sb := makeWorkspaceCRDWithStatus("ws-1", tt.podIP, tt.phase, "ws-1")
			env.wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(sb, nil).Once()

			w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions", nil)
			assert.Equal(t, http.StatusServiceUnavailable, w.Code)
			assert.Equal(t, "10", w.Header().Get("Retry-After"))
		})
	}
}

func TestProxy_WorkspaceNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.wsMock.On("Get", "sb-missing", metav1.GetOptions{}).Return(nil, fmt.Errorf("not found")).Once()

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/sb-missing/sessions", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestProxy_PasswordCachedAfterFirstRead(t *testing.T) {
	env := newTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w1 := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions", nil)
	assert.Equal(t, http.StatusOK, w1.Code)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w2 := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions", nil)
	assert.Equal(t, http.StatusOK, w2.Code)

	_, err := env.clientset.CoreV1().Secrets("default").Get(context.Background(), "workspace-pw-ws-1", metav1.GetOptions{})
	assert.NoError(t, err, "password should be read from cache on second request")
}

func TestProxy_SecretNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "failed to retrieve workspace credentials")
}

func TestProxy_EmptyPasswordKey(t *testing.T) {
	env := newTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "workspace-pw-ws-1", Namespace: "default"},
		Data:       map[string][]byte{"password": {}},
	}
	_, err := env.clientset.CoreV1().Secrets("default").Create(context.Background(), secret, metav1.CreateOptions{})
	require.NoError(t, err)

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestProxy_ActiveSessionLimit(t *testing.T) {
	env := newTestEnv(t)
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 2)

	for i := 0; i < 2; i++ {
		env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
		sid := fmt.Sprintf("session-%d", i)
		w := env.doRequestWithT(t, "POST", fmt.Sprintf("/api/v1/workspaces/ws-1/sessions/%s/message", sid), strings.NewReader(`{"msg":"hi"}`))
		assert.Equal(t, http.StatusOK, w.Code, "session %s should succeed", sid)
	}

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/session-2/message", strings.NewReader(`{"msg":"hi"}`))
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "10", w.Header().Get("Retry-After"))

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, float64(2), body["maxActiveSessions"])
	assert.Equal(t, float64(10), body["retryAfter"])
	assert.Contains(t, body["error"], "active session limit reached")
}

func TestProxy_AlreadyActiveSessionSucceeds(t *testing.T) {
	env := newTestEnv(t)
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 1)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w1 := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/s1/message", strings.NewReader(`{"msg":"hi"}`))
	assert.Equal(t, http.StatusOK, w1.Code)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w2 := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/s1/message", strings.NewReader(`{"msg":"hi2"}`))
	assert.Equal(t, http.StatusOK, w2.Code, "same session should not be double-counted")
}

func TestProxy_ReadOnlyBypassesSessionLimit(t *testing.T) {
	env := newTestEnv(t)
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 1)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w1 := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/s1/message", strings.NewReader(`{"msg":"hi"}`))
	assert.Equal(t, http.StatusOK, w1.Code)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w2 := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions/s1/message", nil)
	assert.Equal(t, http.StatusOK, w2.Code, "read-only GET history should bypass limit")
}

func TestProxy_CreateSessionBypassesLimit(t *testing.T) {
	env := newTestEnv(t)
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 0)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions", strings.NewReader(`{}`))
	assert.Equal(t, http.StatusOK, w.Code, "create session should bypass limit")
}

func TestProxy_AbortBypassesLimit(t *testing.T) {
	env := newTestEnv(t)
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 0)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/s1/abort", nil)
	assert.Equal(t, http.StatusOK, w.Code, "abort should bypass limit")
}

func TestProxy_PromptAsyncEnforcesLimit(t *testing.T) {
	env := newTestEnv(t)
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 1)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w1 := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/s1/prompt", strings.NewReader(`{"prompt":"hi"}`))
	assert.Equal(t, http.StatusOK, w1.Code)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w2 := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/s2/prompt", strings.NewReader(`{"prompt":"hi"}`))
	assert.Equal(t, http.StatusTooManyRequests, w2.Code, "prompt_async should enforce session limit")
}

func TestProxy_ConnectionCeiling(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 100)

	for i := 0; i < 10; i++ {
		env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	}
	require.True(t, env.handler.acquireConnection("ws-1"))

	for i := 0; i < 9; i++ {
		assert.True(t, env.handler.acquireConnection("ws-1"))
	}

	assert.False(t, env.handler.acquireConnection("ws-1"), "11th connection should be rejected")

	env.handler.releaseConnection("ws-1")
	assert.True(t, env.handler.acquireConnection("ws-1"), "connection after release should succeed")
}

func TestProxy_ConnectionCeiling_Returns429(t *testing.T) {
	env := newTestEnv(t)
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 100)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")

	env.handler.connMu.Lock()
	env.handler.connCount["ws-1"] = 10
	env.handler.connMu.Unlock()

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions", nil)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Body.String(), "connection limit reached")
}

func TestProxy_EndpointMapping(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		path           string
		expectedTarget string
	}{
		{"create session", "POST", "/api/v1/workspaces/ws-1/sessions", "/session"},
		{"list sessions", "GET", "/api/v1/workspaces/ws-1/sessions", "/session"},
		{"send message", "POST", "/api/v1/workspaces/ws-1/sessions/s1/message", "/session/s1/message"},
		{"prompt async", "POST", "/api/v1/workspaces/ws-1/sessions/s1/prompt", "/session/s1/prompt_async"},
		{"get history", "GET", "/api/v1/workspaces/ws-1/sessions/s1/message", "/session/s1/message"},
		{"abort", "POST", "/api/v1/workspaces/ws-1/sessions/s1/abort", "/session/s1/abort"},
		// NOTE: "events" is intentionally omitted — StreamEvents is broker-based
		// and does not proxy to the pod; it is covered by stream_events_test.go.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedPath string
			env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"path": r.URL.Path})
			})
			env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
			env.setupPasswordWithT(t, "ws-1", "test-password")
			env.setupWorkspaceWithT(t, "ws-1", 5)

			w := env.doRequestWithT(t, tt.method, tt.path, nil)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tt.expectedTarget, capturedPath, "proxy should map to correct target path")
		})
	}
}

func TestProxy_E2E_FullFlow(t *testing.T) {
	var requests []string
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/session":
			if r.Method == "POST" {
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]string{"id": "sess-1"})
			} else {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{"sessions": []string{"sess-1"}})
			}
		case "/session/sess-1/message":
			if r.Method == "POST" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"status": "streaming"})
			} else {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{"messages": []string{"msg1"}})
			}
		case "/session/sess-1/prompt_async":
			w.WriteHeader(http.StatusNoContent)
		case "/session/sess-1/abort":
			w.WriteHeader(http.StatusAccepted)
			// NOTE: /event is intentionally omitted — StreamEvents no longer proxies to the pod.
		}
	})
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions", strings.NewReader(`{"runtime":"python"}`))
	assert.Equal(t, http.StatusCreated, w.Code)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w = env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w = env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/sess-1/message", strings.NewReader(`{"content":"hello"}`))
	assert.Equal(t, http.StatusOK, w.Code)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w = env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/sess-1/prompt", strings.NewReader(`{"prompt":"do something"}`))
	assert.Equal(t, http.StatusNoContent, w.Code)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w = env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions/sess-1/message", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w = env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/sess-1/abort", nil)
	assert.Equal(t, http.StatusAccepted, w.Code)

	expected := []string{
		"POST /session",
		"GET /session",
		"POST /session/sess-1/message",
		"POST /session/sess-1/prompt_async",
		"GET /session/sess-1/message",
		"POST /session/sess-1/abort",
	}
	assert.Equal(t, expected, requests)
}

func TestProxy_E2E_MultipleWorkspaceIsolation(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	env.setupPasswordWithT(t, "ws-1", "pw-1")
	env.setupPasswordWithT(t, "sb-2", "pw-2")
	env.setupWorkspaceWithT(t, "ws-1", 1)
	env.setupWorkspaceWithT(t, "ws-1", 1)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/s1/message", strings.NewReader(`{}`))
	assert.Equal(t, http.StatusOK, w.Code)

	env.setupWorkspacePodWithT(t, "sb-2", "10.0.0.2", string(v1.WorkspacePhaseActive), "ws-1")
	w = env.doRequestWithT(t, "POST", "/api/v1/workspaces/sb-2/sessions/s2/message", strings.NewReader(`{}`))
	assert.Equal(t, http.StatusOK, w.Code, "different workspace should have independent session tracking")

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w = env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/s3/message", strings.NewReader(`{}`))
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "ws-1 should be at limit")
}

func TestProxy_WorkspaceNotFound_UsesDefaults(t *testing.T) {
	env := newTestEnv(t)
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-missing")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	for i := 0; i < 6; i++ {
		env.wsMock.On("Get", "ws-missing", metav1.GetOptions{}).Return(nil, fmt.Errorf("not found")).Once()
	}

	for i := 0; i < 5; i++ {
		env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-missing")
		sid := fmt.Sprintf("s%d", i)
		w := env.doRequestWithT(t, "POST", fmt.Sprintf("/api/v1/workspaces/ws-1/sessions/%s/message", sid), strings.NewReader(`{}`))
		assert.Equal(t, http.StatusOK, w.Code, "session %s with default limit 5 should succeed", sid)
	}

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-missing")
	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/s6/message", strings.NewReader(`{}`))
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "6th session with default limit 5 should be rejected")
}

func TestProxy_BackendErrorPassthrough(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "internal opencode error"})
	})
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "internal opencode error")
}

func TestProxy_Backend404Passthrough(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "session not found"})
	})
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions/missing/message", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestProxy_CacheInvalidation(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil)

	handler.pwCacheMu.Lock()
	handler.pwCache["ws-1"] = "old-password"
	handler.pwCacheMu.Unlock()

	handler.wsConfigMu.Lock()
	handler.wsConfig["ws-1"] = workspaceConfig{workspaceID: "ws-1", maxActiveSessions: 5}
	handler.wsConfigMu.Unlock()

	handler.activeMu.Lock()
	handler.activeSess["ws-1"] = map[string]bool{"s1": true}
	handler.activeMu.Unlock()

	handler.invalidateCaches("ws-1")

	handler.pwCacheMu.RLock()
	_, pwOk := handler.pwCache["ws-1"]
	handler.pwCacheMu.RUnlock()
	assert.False(t, pwOk, "password cache should be cleared")

	handler.wsConfigMu.RLock()
	_, wsOk := handler.wsConfig["ws-1"]
	handler.wsConfigMu.RUnlock()
	assert.False(t, wsOk, "workspace config cache should be cleared")

	handler.activeMu.Lock()
	_, sessOk := handler.activeSess["ws-1"]
	handler.activeMu.Unlock()
	assert.False(t, sessOk, "active sessions should be cleared")
}

func TestProxy_PhaseChangeCallback(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil)

	handler.pwCacheMu.Lock()
	handler.pwCache["ws-1"] = "password"
	handler.pwCacheMu.Unlock()

	handler.activeMu.Lock()
	handler.activeSess["ws-1"] = map[string]bool{"s1": true}
	handler.activeMu.Unlock()

	phases := []string{phaseSuspending, phaseSuspended, phaseTerminating, phaseTerminated}
	for _, phase := range phases {
		sb := makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", phase, "ws-1")
		handler.onPhaseChange(sb)
	}

	handler.pwCacheMu.RLock()
	_, pwOk := handler.pwCache["ws-1"]
	handler.pwCacheMu.RUnlock()
	assert.False(t, pwOk, "phase change to %s should invalidate password cache")
}

func TestProxy_PhaseChange_RunningNoInvalidation(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil)

	handler.pwCacheMu.Lock()
	handler.pwCache["ws-1"] = "password"
	handler.pwCacheMu.Unlock()

	sb := makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(phaseActive), "ws-1")
	handler.onPhaseChange(sb)

	handler.pwCacheMu.RLock()
	_, pwOk := handler.pwCache["ws-1"]
	handler.pwCacheMu.RUnlock()
	assert.True(t, pwOk, "phase change to Running should NOT invalidate cache")
}

func TestProxy_ConcurrentRequests(t *testing.T) {
	env := newTestEnvWithBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	env.setupPasswordWithT(t, "ws-1", "test-password")
	for i := 0; i < 5; i++ {
		env.setupWorkspaceWithT(t, "ws-1", 100)
		env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	}

	results := make(chan int, 5)
	for i := 0; i < 5; i++ {
		go func() {
			w := env.doRequestWithT(t, "GET", "/api/v1/workspaces/ws-1/sessions", nil)
			results <- w.Code
		}()
	}

	for i := 0; i < 5; i++ {
		select {
		case code := <-results:
			assert.Equal(t, http.StatusOK, code)
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent request timed out")
		}
	}
}

func TestProxy_E2E_MaxActiveSessionsCustom(t *testing.T) {
	env := newTestEnv(t)
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 3)

	for i := 0; i < 3; i++ {
		env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
		sid := fmt.Sprintf("s%d", i)
		w := env.doRequestWithT(t, "POST", fmt.Sprintf("/api/v1/workspaces/ws-1/sessions/%s/message", sid), strings.NewReader(`{}`))
		assert.Equal(t, http.StatusOK, w.Code)
	}

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/s3/message", strings.NewReader(`{}`))
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestProxy_RemoveActiveSession(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil)

	handler.activeMu.Lock()
	handler.activeSess["ws-1"] = map[string]bool{"s1": true, "s2": true}
	handler.activeMu.Unlock()

	handler.removeActiveSession("ws-1", "s1")
	assert.Equal(t, 1, handler.activeSessionCount("ws-1"))

	handler.removeActiveSession("ws-1", "s2")
	assert.Equal(t, 0, handler.activeSessionCount("ws-1"))

	handler.activeMu.Lock()
	_, exists := handler.activeSess["ws-1"]
	handler.activeMu.Unlock()
	assert.False(t, exists, "empty session set should be cleaned up")
}

func TestProxy_RemoveNonexistentSession(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil)

	handler.removeActiveSession("sb-missing", "s1")
	assert.Equal(t, 0, handler.activeSessionCount("sb-missing"))
}

func TestIsConnectionError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		isConn bool
	}{
		{"connection refused", fmt.Errorf("dial tcp 10.0.0.1:4096: connection refused"), true},
		{"no such host", fmt.Errorf("dial tcp: lookup ws-1.default.svc: no such host"), true},
		{"connection reset", fmt.Errorf("read tcp: connection reset by peer"), true},
		{"i/o timeout", fmt.Errorf("dial tcp 10.0.0.1:4096: i/o timeout"), true},
		{"EOF", fmt.Errorf("unexpected EOF"), true},
		{"network unreachable", fmt.Errorf("dial tcp: network is unreachable"), true},
		{"nil error", nil, false},
		{"other error", fmt.Errorf("something else"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.isConn, isConnectionError(tt.err))
		})
	}
}

func TestProxy_NewProxyHandler_Validation(t *testing.T) {
	tests := []struct {
		name      string
		k8sClient pkginterfaces.KubernetesClient
		logger    pkginterfaces.LoggerInterface
		expectErr string
	}{
		{"nil k8s client", nil, &testLogger{}, "kubernetes client cannot be nil"},
		{"nil logger", k8smocks.NewMockKubernetesClient(), nil, "logger cannot be nil"},
		{"both valid", k8smocks.NewMockKubernetesClient(), &testLogger{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewProxyHandler(tt.k8sClient, tt.logger, "default", nil)
			if tt.expectErr != "" {
				assert.EqualError(t, err, tt.expectErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestProxy_DefaultNamespace(t *testing.T) {
	h, err := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "default", h.namespace)
}

func TestProxy_CustomHTTPClient(t *testing.T) {
	custom := &http.Client{Timeout: 10 * time.Second}
	h, err := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "ns", custom)
	require.NoError(t, err)
	assert.Equal(t, custom, h.httpClient)
}

func TestProxy_ConnectionCountTracking(t *testing.T) {
	h, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil)

	assert.Equal(t, 0, h.connectionCount("ws-1"))
	h.acquireConnection("ws-1")
	assert.Equal(t, 1, h.connectionCount("ws-1"))
	h.acquireConnection("ws-1")
	assert.Equal(t, 2, h.connectionCount("ws-1"))
	h.releaseConnection("ws-1")
	assert.Equal(t, 1, h.connectionCount("ws-1"))
	h.releaseConnection("ws-1")
	assert.Equal(t, 0, h.connectionCount("ws-1"))
}

func TestProxy_E2E_SSEDrivenSessionLifecycle(t *testing.T) {
	idleSignal := make(chan struct{}, 1)

	sseBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/event" {
			flusher := w.(http.Flusher)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			select {
			case <-idleSignal:
				evt := sseEvent{Type: "session.status", SessionID: "s1", Status: "idle"}
				data, _ := json.Marshal(evt)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			case <-r.Context().Done():
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer sseBackend.Close()

	transport := &redirectTransport{server: sseBackend}
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()

	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	fakeClientset := k8sfake.NewSimpleClientset()
	k8sMock.On("Clientset").Return(fakeClientset)

	secret := makePasswordSecret("ws-1", "test-password")
	_, err := fakeClientset.CoreV1().Secrets("default").Create(context.Background(), secret, metav1.CreateOptions{})
	require.NoError(t, err)

	ws := makeWorkspaceCRD("ws-1", 1)
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(ws, nil)

	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", httpClient)
	require.NoError(t, err)

	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(
		makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1"), nil,
	)

	handler.sseTracker = NewSSETracker(httpClient, &testLogger{}, handler.onSessionIdle)
	handler.sseTracker.SetPasswordGetter(handler.getPassword)
	handler.sseTracker.SetPodIPResolver(handler.getPodIPForSSE)
	handler.sseTracker.SetOnSessionActive(handler.onSessionActive)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	proxy := router.Group("/api/v1/workspaces/:id")
	proxy.POST("/sessions/:sessionId/message", handler.SendMessage)

	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(
		makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1"), nil,
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/workspaces/ws-1/sessions/s1/message", strings.NewReader(`{"msg":"hi"}`))
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, handler.activeSessionCount("ws-1"), "session s1 should be active")

	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(
		makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1"), nil,
	)

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/v1/workspaces/ws-1/sessions/s2/message", strings.NewReader(`{"msg":"hi"}`))
	router.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusTooManyRequests, w2.Code, "limit of 1 should reject second session")

	idleSignal <- struct{}{}

	require.Eventually(t, func() bool {
		return handler.activeSessionCount("ws-1") == 0
	}, 3*time.Second, 50*time.Millisecond, "SSE idle event should clear session s1")

	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(
		makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1"), nil,
	)

	handler.sseTracker.StopWatching("ws-1")

	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("POST", "/api/v1/workspaces/ws-1/sessions/s2/message", strings.NewReader(`{"msg":"hi"}`))
	router.ServeHTTP(w3, req3)
	assert.Equal(t, http.StatusOK, w3.Code, "after SSE idle, new session should succeed")
}

func TestProxy_E2E_SSEBusyEventAddsActiveSession(t *testing.T) {
	handler, err := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil)
	require.NoError(t, err)

	handler.wsConfigMu.Lock()
	handler.wsConfig["ws-1"] = workspaceConfig{workspaceID: "ws-1", maxActiveSessions: 5}
	handler.wsConfigMu.Unlock()

	handler.onSessionActive("ws-1", "s1")
	assert.Equal(t, 1, handler.activeSessionCount("ws-1"), "busy event should add session s1")

	handler.onSessionActive("ws-1", "s1")
	assert.Equal(t, 1, handler.activeSessionCount("ws-1"), "duplicate busy should not double-count")

	handler.onSessionActive("ws-1", "s2")
	assert.Equal(t, 2, handler.activeSessionCount("ws-1"), "second busy session should be counted")
}

func TestProxy_SessionLeak_NotOnConnectionCeilingReject(t *testing.T) {
	env := newTestEnv(t)
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.setupWorkspaceWithT(t, "ws-1", 5)

	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")

	env.handler.connMu.Lock()
	env.handler.connCount["ws-1"] = 10
	env.handler.connMu.Unlock()

	w := env.doRequestWithT(t, "POST", "/api/v1/workspaces/ws-1/sessions/s1/message", strings.NewReader(`{}`))
	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	assert.Equal(t, 0, env.handler.activeSessionCount("ws-1"),
		"session should not leak into active set when connection ceiling rejects")
}

func TestProxy_SessionLeak_CleanedUpOn503(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()

	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	fakeClientset := k8sfake.NewSimpleClientset()
	k8sMock.On("Clientset").Return(fakeClientset)

	secret := makePasswordSecret("ws-1", "test-password")
	_, err := fakeClientset.CoreV1().Secrets("default").Create(context.Background(), secret, metav1.CreateOptions{})
	require.NoError(t, err)

	ws := makeWorkspaceCRD("ws-1", 5)
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(ws, nil)

	crd := makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(crd, nil)

	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", &http.Client{
		Transport: &alwaysFailTransport{},
		Timeout:   2 * time.Second,
	})
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/workspaces/:id/sessions/:sessionId/message", handler.SendMessage)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/workspaces/ws-1/sessions/s1/message", strings.NewReader(`{}`))
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, 0, handler.activeSessionCount("ws-1"),
		"active session should be cleaned up when proxy fails with 503")
}

func TestProxy_GetPodIPForSSE_RunningReturnsIP(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()

	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	crd := makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(crd, nil).Once()

	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil)
	require.NoError(t, err)

	ip := handler.getPodIPForSSE("ws-1")
	assert.Equal(t, "10.0.0.1", ip)
}

func TestProxy_GetPodIPForSSE_SuspendedReturnsEmpty(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()

	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	crd := makeWorkspaceCRDWithStatus("ws-1", "", "Suspended", "ws-1")
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(crd, nil).Once()

	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil)
	require.NoError(t, err)

	ip := handler.getPodIPForSSE("ws-1")
	assert.Equal(t, "", ip)
}

func TestProxy_GetPodIPForSSE_NotFoundReturnsEmpty(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()

	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	wsMock.On("Get", "sb-missing", metav1.GetOptions{}).Return(nil, fmt.Errorf("not found")).Once()

	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil)
	require.NoError(t, err)

	ip := handler.getPodIPForSSE("sb-missing")
	assert.Equal(t, "", ip)
}

func TestProxy_OnPhaseChange_SuspendingStopsSSE(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil)

	handler.sseTracker = NewSSETracker(
		&http.Client{},
		&testLogger{},
		func(workspaceID, sessionID string) {},
	)
	handler.sseTracker.SetPasswordGetter(func(ctx context.Context, workspaceID string) (string, error) {
		return "pw", nil
	})
	handler.sseTracker.SetPodIPResolver(func(workspaceID string) string { return "10.0.0.1" })

	handler.sseTracker.EnsureWatching("ws-1")
	assert.Equal(t, 1, handler.sseTracker.SubscriptionCount())

	phases := []string{phaseSuspending, phaseSuspended, phaseTerminating, phaseTerminated}
	for _, phase := range phases {
		handler.sseTracker.EnsureWatching("ws-1")
		sb := makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", phase, "ws-1")
		handler.onPhaseChange(sb)
		assert.Equal(t, 0, handler.sseTracker.SubscriptionCount(),
			"SSE subscription should be stopped on phase %s", phase)
	}
}

func TestProxy_OnPhaseChange_RunningKeepsSSE(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil)

	handler.sseTracker = NewSSETracker(
		&http.Client{},
		&testLogger{},
		func(workspaceID, sessionID string) {},
	)
	handler.sseTracker.SetPasswordGetter(func(ctx context.Context, workspaceID string) (string, error) {
		return "pw", nil
	})
	handler.sseTracker.SetPodIPResolver(func(workspaceID string) string { return "10.0.0.1" })

	handler.sseTracker.EnsureWatching("ws-1")
	assert.Equal(t, 1, handler.sseTracker.SubscriptionCount())

	sb := makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(phaseActive), "ws-1")
	handler.onPhaseChange(sb)

	assert.Equal(t, 1, handler.sseTracker.SubscriptionCount(),
		"SSE subscription should NOT be stopped on Running phase change")
}

func TestProxy_ActivityNotRecordedOnProxyFailure(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()

	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	fakeClientset := k8sfake.NewSimpleClientset()
	k8sMock.On("Clientset").Return(fakeClientset)

	secret := makePasswordSecret("ws-1", "test-password")
	_, err := fakeClientset.CoreV1().Secrets("default").Create(context.Background(), secret, metav1.CreateOptions{})
	require.NoError(t, err)

	ws := makeWorkspaceCRD("ws-1", 5)
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(ws, nil)

	crd := makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(crd, nil)

	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", &http.Client{
		Transport: &alwaysFailTransport{},
		Timeout:   2 * time.Second,
	})
	require.NoError(t, err)

	tracker := NewActivityTracker(k8sMock, &testLogger{}, "default")
	handler.activityTracker = tracker

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/workspaces/:id/sessions", handler.ListSessions)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/workspaces/ws-1/sessions", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, 0, tracker.PendingCount(),
		"activity should NOT be recorded when proxy call fails")
}

func TestProxy_ActivityRecordedOnSuccess(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer backend.Close()

	transport := &redirectTransport{server: backend}
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()

	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	fakeClientset := k8sfake.NewSimpleClientset()
	k8sMock.On("Clientset").Return(fakeClientset)

	secret := makePasswordSecret("ws-1", "test-pw")
	_, err := fakeClientset.CoreV1().Secrets("default").Create(context.Background(), secret, metav1.CreateOptions{})
	require.NoError(t, err)

	ws := makeWorkspaceCRD("ws-1", 5)
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(ws, nil)

	crd := makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	wsMock.On("Get", "ws-1", metav1.GetOptions{}).Return(crd, nil)

	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", httpClient)
	require.NoError(t, err)

	tracker := NewActivityTracker(k8sMock, &testLogger{}, "default")
	handler.activityTracker = tracker

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/workspaces/:id/sessions", handler.ListSessions)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/workspaces/ws-1/sessions", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, tracker.PendingCount(),
		"activity should be recorded (pending for flush) when proxy succeeds")
}

func TestProxy_OnSessionIdle_ActivitySkippedWhenCacheEvicted(t *testing.T) {
	handler, _ := NewProxyHandler(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default", nil)

	tracker := NewActivityTracker(k8smocks.NewMockKubernetesClient(), &testLogger{}, "default")
	handler.activityTracker = tracker

	handler.activeMu.Lock()
	handler.activeSess["ws-1"] = map[string]bool{"s1": true}
	handler.activeMu.Unlock()

	handler.onSessionIdle("ws-1", "s1")

	assert.Equal(t, 0, tracker.PendingCount(),
		"activity should not be recorded when wsConfig cache is absent (workspace evicted)")
	assert.Equal(t, 0, handler.activeSessionCount("ws-1"),
		"session should still be removed from active set even when cache is absent")
}

func makePasswordSecret(workspaceID, password string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("workspace-pw-%s", workspaceID),
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte(password),
		},
	}
}

func makeWorkspaceCRD(name string, maxActiveSessions int) *v1.Workspace {
	return &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: v1.WorkspaceSpec{
			Owner:             v1.WorkspaceOwner{UserID: "user-1"},
			MaxActiveSessions: int32(maxActiveSessions),
		},
		Status: v1.WorkspaceStatus{
			Phase: v1.WorkspacePhaseActive,
			PodIP: "10.0.0.1",
		},
	}
}

func makeWorkspaceCRDWithStatus(name, podIP, phase, _ string) *v1.Workspace {
	return &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: v1.WorkspaceSpec{
			Owner:   v1.WorkspaceOwner{UserID: "user-1"},
			Runtime: "python:3.11",
		},
		Status: v1.WorkspaceStatus{
			Phase: v1.WorkspacePhase(phase),
			PodIP: podIP,
		},
	}
}
