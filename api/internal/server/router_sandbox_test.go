package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	apierrors "github.com/lenaxia/llmsafespace/api/internal/errors"
	apilogger "github.com/lenaxia/llmsafespace/api/internal/logger"
	imocks "github.com/lenaxia/llmsafespace/api/internal/mocks"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// newSandboxRouterFixture returns a router with a configured MockSandboxService.
// Auth always passes with userID=test-user when an Authorization header is set.
func newSandboxRouterFixture(t *testing.T) (http.Handler, *imocks.MockSandboxService) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	log, err := apilogger.New(false, "error", "json")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	auth := &imocks.MockAuthMiddlewareService{}
	met := &imocks.MockMetricsService{}
	ws := &imocks.MockWorkspaceService{}
	sb := &imocks.MockSandboxService{}

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

	svc := &mockServices{auth: auth, metrics: met, workspace: ws, sandbox: sb}
	router := NewRouter(svc, log, nil, RouterConfig{Debug: false})
	return router, sb
}

// sandboxRoutes lists every sandbox CRUD route (does not include proxy routes).
var sandboxRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/api/v1/sandboxes"},
	{http.MethodPost, "/api/v1/sandboxes"},
	{http.MethodGet, "/api/v1/sandboxes/sb-1"},
	{http.MethodDelete, "/api/v1/sandboxes/sb-1"},
	{http.MethodGet, "/api/v1/sandboxes/sb-1/status"},
}

// TestSandboxRoutes_Exist verifies every sandbox CRUD route is registered.
func TestSandboxRoutes_Exist(t *testing.T) {
	for _, rt := range sandboxRoutes {
		t.Run(rt.method+"_"+rt.path, func(t *testing.T) {
			router, sb := newSandboxRouterFixture(t)

			// Provide loose mock setup so handlers don't panic; the assertion
			// only checks that the route is registered (non-404).
			sb.On("ListSandboxes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(nil, assert.AnError).Maybe()
			sb.On("CreateSandbox", mock.Anything, mock.Anything).
				Return(nil, assert.AnError).Maybe()
			sb.On("GetSandbox", mock.Anything, mock.Anything).
				Return(nil, assert.AnError).Maybe()
			sb.On("TerminateSandbox", mock.Anything, mock.Anything).
				Return(assert.AnError).Maybe()
			sb.On("GetSandboxStatus", mock.Anything, mock.Anything).
				Return(nil, assert.AnError).Maybe()

			req, _ := http.NewRequest(rt.method, rt.path, bytes.NewReader([]byte(`{"runtime":"base"}`)))
			req.Header.Set("Authorization", "Bearer testtoken")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.NotEqual(t, http.StatusNotFound, w.Code,
				"route %s %s should be registered (got %d)", rt.method, rt.path, w.Code)
		})
	}
}

// TestSandboxRoutes_RequireAuth verifies every sandbox CRUD route returns 401
// without an Authorization header.
func TestSandboxRoutes_RequireAuth(t *testing.T) {
	for _, rt := range sandboxRoutes {
		t.Run(rt.method+"_"+rt.path, func(t *testing.T) {
			router, _ := newSandboxRouterFixture(t)

			req, _ := http.NewRequest(rt.method, rt.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusUnauthorized, w.Code,
				"route %s %s should return 401 without auth token", rt.method, rt.path)
		})
	}
}

// TestSandboxRoutes_CreateSandbox_Success verifies the happy path: a POST to
// /api/v1/sandboxes returns 201 with the created sandbox JSON.
func TestSandboxRoutes_CreateSandbox_Success(t *testing.T) {
	router, sb := newSandboxRouterFixture(t)

	expected := &types.Sandbox{}
	expected.Name = "sb-abc"
	expected.Spec.Runtime = "python:3.11"

	sb.On("CreateSandbox", mock.Anything, mock.MatchedBy(func(req *types.CreateSandboxRequest) bool {
		return req.Runtime == "python:3.11" && req.UserID == "test-user"
	})).Return(expected, nil)

	body, _ := json.Marshal(map[string]string{
		"runtime": "python:3.11",
	})
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/sandboxes", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer testtoken")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var got types.Sandbox
	if err := json.Unmarshal(w.Body.Bytes(), &got); err == nil {
		assert.Equal(t, "sb-abc", got.Name)
	}
}

// TestSandboxRoutes_CreateSandbox_BadJSON returns 400 on invalid JSON body.
func TestSandboxRoutes_CreateSandbox_BadJSON(t *testing.T) {
	router, _ := newSandboxRouterFixture(t)

	req, _ := http.NewRequest(http.MethodPost, "/api/v1/sandboxes", bytes.NewReader([]byte("not json")))
	req.Header.Set("Authorization", "Bearer testtoken")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestSandboxRoutes_ListSandboxes_Success returns the list result JSON.
func TestSandboxRoutes_ListSandboxes_Success(t *testing.T) {
	router, sb := newSandboxRouterFixture(t)

	expected := &types.SandboxListResult{
		Items: []types.SandboxListItem{{ID: "sb-1", Runtime: "python:3.11"}},
	}
	sb.On("ListSandboxes", mock.Anything, "test-user", 20, 0).Return(expected, nil)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/sandboxes", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var got types.SandboxListResult
	if err := json.Unmarshal(w.Body.Bytes(), &got); err == nil {
		assert.Len(t, got.Items, 1)
		assert.Equal(t, "sb-1", got.Items[0].ID)
	}
}

// TestSandboxRoutes_ListSandboxes_Pagination respects limit and offset query.
func TestSandboxRoutes_ListSandboxes_Pagination(t *testing.T) {
	router, sb := newSandboxRouterFixture(t)
	sb.On("ListSandboxes", mock.Anything, "test-user", 5, 10).
		Return(&types.SandboxListResult{}, nil)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/sandboxes?limit=5&offset=10", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	sb.AssertCalled(t, "ListSandboxes", mock.Anything, "test-user", 5, 10)
}

// TestSandboxRoutes_GetSandbox_Success returns the sandbox JSON.
func TestSandboxRoutes_GetSandbox_Success(t *testing.T) {
	router, sb := newSandboxRouterFixture(t)
	expected := &types.Sandbox{}
	expected.Name = "sb-1"
	sb.On("GetSandbox", mock.Anything, "sb-1").Return(expected, nil)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/sandboxes/sb-1", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// TestSandboxRoutes_GetSandbox_NotFound returns 404 when service reports missing.
func TestSandboxRoutes_GetSandbox_NotFound(t *testing.T) {
	router, sb := newSandboxRouterFixture(t)
	sb.On("GetSandbox", mock.Anything, "missing").
		Return(nil, &types.SandboxNotFoundError{ID: "missing"})

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/sandboxes/missing", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestSandboxRoutes_DeleteSandbox_Success returns 204 on successful termination.
func TestSandboxRoutes_DeleteSandbox_Success(t *testing.T) {
	router, sb := newSandboxRouterFixture(t)
	sb.On("TerminateSandbox", mock.Anything, "sb-1").Return(nil)

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/sandboxes/sb-1", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

// TestSandboxRoutes_DeleteSandbox_Forbidden returns 403 from service forbidden error.
func TestSandboxRoutes_DeleteSandbox_Forbidden(t *testing.T) {
	router, sb := newSandboxRouterFixture(t)
	sb.On("TerminateSandbox", mock.Anything, "sb-1").
		Return(apierrors.NewForbiddenError("not yours", assert.AnError))

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/sandboxes/sb-1", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestSandboxRoutes_GetStatus_Success returns the SandboxStatus JSON.
func TestSandboxRoutes_GetStatus_Success(t *testing.T) {
	router, sb := newSandboxRouterFixture(t)
	expected := &types.SandboxStatus{Phase: "Running"}
	sb.On("GetSandboxStatus", mock.Anything, "sb-1").Return(expected, nil)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/sandboxes/sb-1/status", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var got types.SandboxStatus
	if err := json.Unmarshal(w.Body.Bytes(), &got); err == nil {
		assert.Equal(t, "Running", got.Phase)
	}
}
