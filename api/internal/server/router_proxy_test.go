package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/lenaxia/llmsafespace/api/internal/handlers"
	apilogger "github.com/lenaxia/llmsafespace/api/internal/logger"
	imocks "github.com/lenaxia/llmsafespace/api/internal/mocks"
	k8smocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

type serverTestLogger struct{}

func (l *serverTestLogger) Debug(msg string, kv ...interface{})                  {}
func (l *serverTestLogger) Info(msg string, kv ...interface{})                   {}
func (l *serverTestLogger) Warn(msg string, kv ...interface{})                   {}
func (l *serverTestLogger) Error(msg string, err error, kv ...interface{})       {}
func (l *serverTestLogger) Fatal(msg string, err error, kv ...interface{})       {}
func (l *serverTestLogger) With(kv ...interface{}) pkginterfaces.LoggerInterface { return l }
func (l *serverTestLogger) Sync() error                                          { return nil }

type proxyRedirectTransport struct{ server *httptest.Server }

func (t *proxyRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.server.URL, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

func newProxyRouterFixture(t *testing.T, backendHandler http.HandlerFunc) (*gin.Engine, *k8smocks.MockSandboxInterface, *k8sfake.Clientset) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	backend := httptest.NewServer(backendHandler)
	t.Cleanup(func() { backend.Close() })

	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	sbMock := k8smocks.NewMockSandboxInterface()
	wsMock := k8smocks.NewMockWorkspaceInterface()

	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Sandboxes", "default").Return(sbMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	fakeClientset := k8sfake.NewSimpleClientset()
	k8sMock.On("Clientset").Return(fakeClientset)
	wsMock.On("Get", mock.Anything, metav1.GetOptions{}).Return(&v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "ws-1", Namespace: "default"},
		Spec:       v1.WorkspaceSpec{MaxActiveSessions: 5},
	}, nil).Maybe()

	httpClient := &http.Client{Transport: &proxyRedirectTransport{server: backend}}
	proxyHandler, err := handlers.NewProxyHandler(k8sMock, &serverTestLogger{}, "default", httpClient)
	require.NoError(t, err)

	auth := &imocks.MockAuthMiddlewareService{}
	met := &imocks.MockMetricsService{}

	met.On("RecordRequest", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	met.On("IncrementActiveConnections", mock.Anything, mock.Anything).Maybe()
	met.On("DecrementActiveConnections", mock.Anything, mock.Anything).Maybe()

	auth.On("AuthMiddleware").Return(gin.HandlerFunc(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		c.Set("userID", "test-user")
		c.Next()
	}))
	auth.On("GetUserID", mock.Anything).Return("test-user")

	svc := &mockServices{auth: auth, metrics: met, workspace: nil}

	log, err := apilogger.New(false, "error", "json")
	require.NoError(t, err)

	router := NewRouter(svc, log, proxyHandler, RouterConfig{Debug: false})
	return router, sbMock, fakeClientset
}

func makeSandboxForProxy(sandboxID, userID, podIP, phase string) *v1.Sandbox {
	return &v1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxID,
			Namespace: "default",
			Labels:    map[string]string{"user-id": userID},
		},
		Spec:   v1.SandboxSpec{WorkspaceRef: "ws-1"},
		Status: v1.SandboxStatus{Phase: phase, PodIP: podIP},
	}
}

func addProxyPasswordSecret(t *testing.T, clientset *k8sfake.Clientset, sandboxID, password string) {
	t.Helper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("sandbox-pw-%s", sandboxID),
			Namespace: "default",
		},
		Data: map[string][]byte{"password": []byte(password)},
	}
	_, err := clientset.CoreV1().Secrets("default").Create(context.Background(), secret, metav1.CreateOptions{})
	require.NoError(t, err)
}

var proxyRouteTable = []struct {
	method string
	path   string
}{
	{http.MethodPost, "/api/v1/sandboxes/sb-1/sessions"},
	{http.MethodGet, "/api/v1/sandboxes/sb-1/sessions"},
	{http.MethodPost, "/api/v1/sandboxes/sb-1/sessions/sess-1/message"},
	{http.MethodPost, "/api/v1/sandboxes/sb-1/sessions/sess-1/prompt"},
	{http.MethodGet, "/api/v1/sandboxes/sb-1/sessions/sess-1/message"},
	{http.MethodPost, "/api/v1/sandboxes/sb-1/sessions/sess-1/abort"},
	{http.MethodGet, "/api/v1/sandboxes/sb-1/events"},
}

func TestProxyRoutes_Exist(t *testing.T) {
	for _, rt := range proxyRouteTable {
		t.Run(rt.method+"_"+rt.path, func(t *testing.T) {
			var capturedRequest bool
			router, sbMock, fakeClientset := newProxyRouterFixture(t, func(w http.ResponseWriter, r *http.Request) {
				capturedRequest = true
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
			})

			sb := makeSandboxForProxy("sb-1", "test-user", "10.0.0.1", "Running")
			sbMock.On("Get", "sb-1", metav1.GetOptions{}).Return(sb, nil)
			addProxyPasswordSecret(t, fakeClientset, "sb-1", "pw")

			req, _ := http.NewRequest(rt.method, rt.path, nil)
			req.Header.Set("Authorization", "Bearer testtoken")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.NotEqual(t, http.StatusNotFound, w.Code,
				"proxy route %s %s should be registered (got %d)", rt.method, rt.path, w.Code)
			assert.Equal(t, http.StatusOK, w.Code,
				"proxy route %s %s should reach the backend", rt.method, rt.path)
			assert.True(t, capturedRequest,
				"proxy route %s %s should reach backend handler", rt.method, rt.path)
		})
	}
}

func TestProxyRoutes_RequireAuth(t *testing.T) {
	for _, rt := range proxyRouteTable {
		t.Run(rt.method+"_"+rt.path, func(t *testing.T) {
			router, _, _ := newProxyRouterFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			req, _ := http.NewRequest(rt.method, rt.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusUnauthorized, w.Code,
				"proxy route %s %s should return 401 without auth token", rt.method, rt.path)
		})
	}
}

func TestProxyRoutes_OwnershipCheck_WrongUser_Returns403(t *testing.T) {
	router, sbMock, _ := newProxyRouterFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	sb := makeSandboxForProxy("sb-1", "other-user", "10.0.0.1", "Running")
	sbMock.On("Get", "sb-1", metav1.GetOptions{}).Return(sb, nil)

	req, _ := http.NewRequest("GET", "/api/v1/sandboxes/sb-1/sessions", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code, "wrong owner should receive 403")
}

func TestProxyRoutes_OwnershipCheck_SandboxNotFound_Returns404(t *testing.T) {
	router, sbMock, _ := newProxyRouterFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	sbMock.On("Get", "sb-1", metav1.GetOptions{}).Return(nil, fmt.Errorf("not found"))

	req, _ := http.NewRequest("GET", "/api/v1/sandboxes/sb-1/sessions", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code, "missing sandbox should return 404")
}

func TestProxyRoutes_NoProxyHandler_RoutesNotRegistered(t *testing.T) {
	router, _ := newRouterFixture(t)

	req, _ := http.NewRequest("GET", "/api/v1/sandboxes/sb-1/sessions", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code, "proxy routes should not exist without proxy handler")
}

func TestProxyRoutes_E2E_ProxiesRequest(t *testing.T) {
	var capturedPath string
	router, sbMock, fakeClientset := newProxyRouterFixture(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	sb := makeSandboxForProxy("sb-1", "test-user", "10.0.0.1", "Running")
	sbMock.On("Get", "sb-1", metav1.GetOptions{}).Return(sb, nil)
	addProxyPasswordSecret(t, fakeClientset, "sb-1", "test-pw")

	req, _ := http.NewRequest("GET", "/api/v1/sandboxes/sb-1/sessions", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "/session", capturedPath, "request should be proxied to opencode /session")
}

func TestProxyRoutes_E2E_EndpointMapping(t *testing.T) {
	tests := []struct {
		method         string
		path           string
		expectedTarget string
	}{
		{"POST", "/api/v1/sandboxes/sb-1/sessions", "/session"},
		{"GET", "/api/v1/sandboxes/sb-1/sessions", "/session"},
		{"POST", "/api/v1/sandboxes/sb-1/sessions/s1/message", "/session/s1/message"},
		{"POST", "/api/v1/sandboxes/sb-1/sessions/s1/prompt", "/session/s1/prompt_async"},
		{"GET", "/api/v1/sandboxes/sb-1/sessions/s1/message", "/session/s1/message"},
		{"POST", "/api/v1/sandboxes/sb-1/sessions/s1/abort", "/session/s1/abort"},
		{"GET", "/api/v1/sandboxes/sb-1/events", "/event"},
	}

	for _, tt := range tests {
		t.Run(tt.method+"_"+tt.path, func(t *testing.T) {
			var capturedPath string
			router, sbMock, fakeClientset := newProxyRouterFixture(t, func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			})

			sb := makeSandboxForProxy("sb-1", "test-user", "10.0.0.1", "Running")
			sbMock.On("Get", "sb-1", metav1.GetOptions{}).Return(sb, nil)
			addProxyPasswordSecret(t, fakeClientset, "sb-1", "test-pw")

			req, _ := http.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", "Bearer testtoken")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tt.expectedTarget, capturedPath)
		})
	}
}

func TestProxyRoutes_ExistingWorkspaceRoutesUnaffected(t *testing.T) {
	router, sbMock, _ := newProxyRouterFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	sbMock.On("Get", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("not found")).Maybe()

	req, _ := http.NewRequest("GET", "/api/v1/workspaces", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.NotEqual(t, http.StatusNotFound, w.Code,
		"workspace routes must still be registered when proxy handler is present")
}

func TestProxyRoutes_OwnershipCheck_MissingUserIDLabel_Returns403(t *testing.T) {
	router, sbMock, _ := newProxyRouterFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	sb := &v1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-1",
			Namespace: "default",
			Labels:    map[string]string{},
		},
		Spec:   v1.SandboxSpec{WorkspaceRef: "ws-1"},
		Status: v1.SandboxStatus{Phase: "Running", PodIP: "10.0.0.1"},
	}
	sbMock.On("Get", "sb-1", metav1.GetOptions{}).Return(sb, nil)

	req, _ := http.NewRequest("GET", "/api/v1/sandboxes/sb-1/sessions", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code, "sandbox with no user-id label should return 403")
}

func TestProxyRoutes_OwnershipCheck_NilLabels_Returns403(t *testing.T) {
	router, sbMock, _ := newProxyRouterFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	sb := &v1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-1",
			Namespace: "default",
			Labels:    nil,
		},
		Spec:   v1.SandboxSpec{WorkspaceRef: "ws-1"},
		Status: v1.SandboxStatus{Phase: "Running", PodIP: "10.0.0.1"},
	}
	sbMock.On("Get", "sb-1", metav1.GetOptions{}).Return(sb, nil)

	req, _ := http.NewRequest("GET", "/api/v1/sandboxes/sb-1/sessions", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code, "sandbox with nil labels should return 403")
}
