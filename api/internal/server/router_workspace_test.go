package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	apilogger "github.com/lenaxia/llmsafespace/api/internal/logger"
	imocks "github.com/lenaxia/llmsafespace/api/internal/mocks"
)

// mockServices is a minimal implementation of interfaces.Services for router tests.
type mockServices struct {
	auth      *imocks.MockAuthMiddlewareService
	metrics   *imocks.MockMetricsService
	workspace *imocks.MockWorkspaceService
}

func (s *mockServices) GetAuth() interfaces.AuthService         { return s.auth }
func (s *mockServices) GetDatabase() interfaces.DatabaseService { return nil }
func (s *mockServices) GetCache() interfaces.CacheService       { return nil }
func (s *mockServices) GetMetrics() interfaces.MetricsService   { return s.metrics }
func (s *mockServices) GetSandbox() interfaces.SandboxService   { return nil }
func (s *mockServices) GetWorkspace() interfaces.WorkspaceService {
	return s.workspace
}

// newRouterFixture builds a Gin engine wired with mock services.
// The auth middleware rejects requests without an Authorization header (401)
// and passes all other requests through with userID = "test-user".
func newRouterFixture(t *testing.T) (*gin.Engine, *mockServices) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	log, err := apilogger.New(false, "error", "json")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	auth := &imocks.MockAuthMiddlewareService{}
	met := &imocks.MockMetricsService{}
	ws := &imocks.MockWorkspaceService{}

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

	svc := &mockServices{auth: auth, metrics: met, workspace: ws}
	router := NewRouter(svc, log, RouterConfig{Debug: false})
	return router, svc
}

// workspaceRoutes lists every registered workspace route as [method, path] pairs.
var workspaceRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/api/v1/workspaces"},
	{http.MethodPost, "/api/v1/workspaces"},
	{http.MethodGet, "/api/v1/workspaces/ws-1"},
	{http.MethodDelete, "/api/v1/workspaces/ws-1"},
	{http.MethodPost, "/api/v1/workspaces/ws-1/suspend"},
	{http.MethodPost, "/api/v1/workspaces/ws-1/resume"},
	{http.MethodGet, "/api/v1/workspaces/ws-1/status"},
	{http.MethodPut, "/api/v1/workspaces/ws-1/credentials"},
	{http.MethodDelete, "/api/v1/workspaces/ws-1/credentials"},
}

// TestWorkspaceRoutes_Exist verifies that every workspace route returns a
// non-404 response (i.e. the route is registered), even when auth is satisfied.
func TestWorkspaceRoutes_Exist(t *testing.T) {
	for _, rt := range workspaceRoutes {
		t.Run(rt.method+"_"+rt.path, func(t *testing.T) {
			router, svc := newRouterFixture(t)

			// Provide workspace mock setup so handlers don't panic; we only
			// care that the route is registered (non-404).
			svc.workspace.On("ListWorkspaces", mock.Anything, mock.Anything, mock.Anything).
				Return(nil, assert.AnError).Maybe()
			svc.workspace.On("CreateWorkspace", mock.Anything, mock.Anything, mock.Anything).
				Return(nil, assert.AnError).Maybe()
			svc.workspace.On("GetWorkspace", mock.Anything, mock.Anything, mock.Anything).
				Return(nil, assert.AnError).Maybe()
			svc.workspace.On("DeleteWorkspace", mock.Anything, mock.Anything, mock.Anything).
				Return(assert.AnError).Maybe()
			svc.workspace.On("SuspendWorkspace", mock.Anything, mock.Anything, mock.Anything).
				Return(assert.AnError).Maybe()
			svc.workspace.On("ResumeWorkspace", mock.Anything, mock.Anything, mock.Anything).
				Return(assert.AnError).Maybe()
			svc.workspace.On("GetWorkspaceStatus", mock.Anything, mock.Anything, mock.Anything).
				Return(nil, assert.AnError).Maybe()
			svc.workspace.On("SetCredentials", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(assert.AnError).Maybe()
			svc.workspace.On("DeleteCredentials", mock.Anything, mock.Anything, mock.Anything).
				Return(assert.AnError).Maybe()

			req, _ := http.NewRequest(rt.method, rt.path, nil)
			req.Header.Set("Authorization", "Bearer testtoken")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.NotEqual(t, http.StatusNotFound, w.Code,
				"route %s %s should be registered (got %d)", rt.method, rt.path, w.Code)
		})
	}
}

// TestWorkspaceRoutes_RequireAuth verifies that every workspace route returns
// 401 when no Authorization header is provided.
func TestWorkspaceRoutes_RequireAuth(t *testing.T) {
	for _, rt := range workspaceRoutes {
		t.Run(rt.method+"_"+rt.path, func(t *testing.T) {
			router, _ := newRouterFixture(t)

			req, _ := http.NewRequest(rt.method, rt.path, nil)
			// No Authorization header — should be rejected by auth middleware.
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusUnauthorized, w.Code,
				"route %s %s should return 401 without auth token", rt.method, rt.path)
		})
	}
}
