// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apierrors "github.com/lenaxia/llmsafespaces/api/internal/errors"
	"github.com/lenaxia/llmsafespaces/api/internal/handlers"
	apilogger "github.com/lenaxia/llmsafespaces/api/internal/logger"
	imocks "github.com/lenaxia/llmsafespaces/api/internal/mocks"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// newWorkspaceAccessRouter builds a router identical to newRouterFixture but
// with explicit, assertion-friendly control over ResolveWorkspace /
// CheckOwnership. The middleware always runs because the routes are wired on
// idGroup inside NewRouter — this fixture just sets the mock's behavior.
func newWorkspaceAccessRouter(t *testing.T, resolveErr error, ownErr error) (*gin.Engine, *imocks.MockWorkspaceService) {
	return newWorkspaceAccessRouterWithConfig(t, resolveErr, ownErr, false)
}

// newWorkspaceAccessRouterWithSecrets is newWorkspaceAccessRouter with a
// SecretsHandler wired so the bindings/env/reload-secrets routes are actually
// registered on idGroup. The handler is constructed with a nil service — the
// middleware is expected to short-circuit before any service call.
func newWorkspaceAccessRouterWithSecrets(t *testing.T, resolveErr error, ownErr error) (*gin.Engine, *imocks.MockWorkspaceService) {
	return newWorkspaceAccessRouterWithConfig(t, resolveErr, ownErr, true)
}

func newWorkspaceAccessRouterWithConfig(t *testing.T, resolveErr error, ownErr error, withSecrets bool) (*gin.Engine, *imocks.MockWorkspaceService) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	log, err := apilogger.New(false, "error", "json")
	require.NoError(t, err)

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

	if resolveErr != nil {
		ws.On("ResolveWorkspace", mock.Anything, mock.Anything).Return((*types.WorkspaceMetadata)(nil), resolveErr)
	} else {
		ws.On("ResolveWorkspace", mock.Anything, mock.Anything).
			Return(&types.WorkspaceMetadata{ID: "ws-1", UserID: "test-user"}, nil)
	}
	ws.On("CheckOwnership", mock.Anything, mock.Anything, mock.Anything).Return(ownErr)
	// Default-allow the List path's service mock so we can assert the route is
	// reachable independently of the middleware.
	ws.On("ListWorkspaces", mock.Anything, mock.Anything, mock.Anything).
		Return(&types.WorkspaceListResult{Items: []types.WorkspaceListItem{}}, nil).Maybe()

	svc := &mockServices{auth: auth, metrics: met, workspace: ws}
	cfg := RouterConfig{Debug: false}
	if withSecrets {
		// Bare handlers — the middleware is expected to short-circuit before
		// any handler method runs, so the nil services are never dereferenced.
		cfg.SecretsHandler = &handlers.SecretsHandler{}
		cfg.WorkspaceEnvHandler = &handlers.WorkspaceEnvHandler{}
	}
	router := NewRouter(svc, log, nil, cfg)
	return router, ws
}

// TestWorkspaceAccessMiddleware_WiredOnIdGroup_Forbidden is the integration
// counterpart to the unit-level middleware test: it confirms the router
// actually mounts WorkspaceAccessMiddleware on idGroup so a Forbidden
// CheckOwnership short-circuits to 403 BEFORE the handler runs.
func TestWorkspaceAccessMiddleware_WiredOnIdGroup_Forbidden(t *testing.T) {
	router, ws := newWorkspaceAccessRouter(t,
		nil,
		apierrors.NewForbiddenError("workspace access denied", nil))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
	ws.AssertCalled(t, "ResolveWorkspace", mock.Anything, "ws-1")
	ws.AssertCalled(t, "CheckOwnership", mock.Anything, "test-user", mock.Anything)
	// Handler must NOT have been reached — GetWorkspace on the service mock has
	// no expectation and would panic if called.
}

// TestWorkspaceAccessMiddleware_NotOnListRoute confirms the inverse: the List
// route has no :id and therefore MUST NOT trigger CheckOwnership. This is the
// regression guard against an accidental future restructure that puts List on
// idGroup.
func TestWorkspaceAccessMiddleware_NotOnListRoute(t *testing.T) {
	router, ws := newWorkspaceAccessRouter(t, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	ws.AssertNotCalled(t, "ResolveWorkspace", mock.Anything, mock.Anything)
	ws.AssertNotCalled(t, "CheckOwnership", mock.Anything, mock.Anything, mock.Anything)
}

// TestWorkspaceAccessMiddleware_NotFoundResolvesTo404 confirms the
// ResolveWorkspace NotFound path surfaces as 404 on a real router (not 403).
func TestWorkspaceAccessMiddleware_NotFoundResolvesTo404(t *testing.T) {
	router, _ := newWorkspaceAccessRouter(t,
		apierrors.NewNotFoundError("workspace", "ws-missing", nil),
		nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-missing", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())
}

// TestWorkspaceAccessMiddleware_AuthorizedReachesHandler confirms that when
// both ResolveWorkspace and CheckOwnership succeed, the actual route handler
// runs and the downstream service method is reached.
func TestWorkspaceAccessMiddleware_AuthorizedReachesHandler(t *testing.T) {
	router, ws := newWorkspaceAccessRouter(t, nil, nil)
	ws.On("GetWorkspace", mock.Anything, "test-user", "ws-1").
		Return(&types.Workspace{ID: "ws-1", Name: "ws", UserID: "test-user"}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	ws.AssertCalled(t, "GetWorkspace", mock.Anything, "test-user", "ws-1")
}

// TestWorkspaceAccessMiddleware_DeleteEnvRoute_ForbiddenForNonOwner is the
// headline security regression for design 0041: DELETE /:id/env/:name was
// previously UNGUARDED (no verifyOwner, no handler-level check) — an
// authenticated user with a leaked workspaceID could delete another user's
// env vars. Story 1 moved the route onto idGroup; this test proves the
// middleware now blocks a non-owner BEFORE the secrets handler runs. The
// handler is never reached, so a bare SecretsHandler is sufficient.
func TestWorkspaceAccessMiddleware_DeleteEnvRoute_ForbiddenForNonOwner(t *testing.T) {
	router, _ := newWorkspaceAccessRouterWithSecrets(t,
		nil,
		apierrors.NewForbiddenError("workspace access denied", nil))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/ws-1/env/MY_VAR", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
}

// TestWorkspaceAccessMiddleware_EnvRoutes_ForbiddenForNonOwner confirms the
// same middleware gate covers PUT and GET on /:id/env — those used to reach
// verifyWorkspaceOwner via AddBindings/GetBindings, but the new contract is
// that the middleware alone decides. Tabular so adding future env sub-routes
// is a one-line change.
func TestWorkspaceAccessMiddleware_EnvRoutes_ForbiddenForNonOwner(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"put_env", http.MethodPut, "/api/v1/workspaces/ws-1/env"},
		{"get_env", http.MethodGet, "/api/v1/workspaces/ws-1/env"},
		{"delete_env_name", http.MethodDelete, "/api/v1/workspaces/ws-1/env/MY_VAR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, _ := newWorkspaceAccessRouterWithSecrets(t,
				nil,
				apierrors.NewForbiddenError("workspace access denied", nil))

			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer token")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
		})
	}
}

// TestWorkspaceAccessMiddleware_BindingsAndReloadSecrets_ForbiddenForNonOwner
// fills the Story 2 validator gap on the remaining previously-UNGUARDED secret
// routes. PUT/GET /:id/bindings used to reach ownership only through
// SecretService.verifyWorkspaceOwner (no D5); POST /:id/reload-secrets likewise.
// Story 1 moved them onto idGroup; this proves the production router (real
// NewRouter with a SecretsHandler wired) now blocks a non-owner before the bare
// SecretsHandler runs. Tabular to mirror the env-route regression above.
func TestWorkspaceAccessMiddleware_BindingsAndReloadSecrets_ForbiddenForNonOwner(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"put_bindings", http.MethodPut, "/api/v1/workspaces/ws-1/bindings"},
		{"get_bindings", http.MethodGet, "/api/v1/workspaces/ws-1/bindings"},
		{"reload_secrets", http.MethodPost, "/api/v1/workspaces/ws-1/reload-secrets"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, _ := newWorkspaceAccessRouterWithSecrets(t,
				nil,
				apierrors.NewForbiddenError("workspace access denied", nil))

			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer token")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
		})
	}
}

// TestWorkspaceAccessMiddleware_D5Composition_BindingsRoute proves the
// composition on a previously-unguarded gap route through the PRODUCTION
// NewRouter: when the workspace service reports an offboarded creator (D5) via
// CheckOwnership returning Forbidden, the middleware blocks PUT /:id/bindings.
// The real router uses a mocked WorkspaceService, so D5 is simulated here by
// the mock's CheckOwnership return value. The D5 creator-membership LOGIC
// itself (IsOrgMember gate on creators) is proven at the unit level in
// verify_owner_d5_membership_test.go and end-to-end through the REAL
// workspace.Service in TestWorkspaceIntegration_AccessMatrix; this test pins
// the composition (middleware → CheckOwnership → 403) on the gap route.
func TestWorkspaceAccessMiddleware_D5Composition_BindingsRoute(t *testing.T) {
	router, ws := newWorkspaceAccessRouterWithSecrets(t,
		nil,
		apierrors.NewForbiddenError("workspace access denied",
			errors.New("user creator is no longer a member of org org-1")))

	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/bindings", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
	ws.AssertCalled(t, "CheckOwnership", mock.Anything, "test-user", mock.Anything)
}

// TestWorkspaceAccessMiddleware_CreateRouteBypassesOwnership confirms the
// inverse of the idGroup gate: POST /api/v1/workspaces (Create) has no :id and
// MUST NOT trigger ResolveWorkspace/CheckOwnership. Together with
// TestWorkspaceAccessMiddleware_NotOnListRoute (GET /api/v1/workspaces) this
// pins the design 0041 invariant that List/Create intentionally bypass the
// ownership check (there is no target workspace to own yet).
func TestWorkspaceAccessMiddleware_CreateRouteBypassesOwnership(t *testing.T) {
	router, ws := newWorkspaceAccessRouterWithConfig(t, nil, nil, true)
	ws.On("CreateWorkspace", mock.Anything, mock.Anything, mock.Anything).
		Return(&types.Workspace{ID: "ws-new", UserID: "test-user"}, nil).Maybe()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.NotEqual(t, http.StatusNotFound, rec.Code, "Create route must be registered")
	ws.AssertNotCalled(t, "ResolveWorkspace", mock.Anything, mock.Anything)
	ws.AssertNotCalled(t, "CheckOwnership", mock.Anything, mock.Anything, mock.Anything)
}
