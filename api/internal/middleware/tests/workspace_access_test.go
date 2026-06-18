// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package tests

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apierrors "github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// fakeWorkspaceAccessService is a configurable stand-in for the narrow
// workspaceAccessService interface consumed by WorkspaceAccessMiddleware.
// It records the arguments it was called with so tests can assert wiring.
type fakeWorkspaceAccessService struct {
	meta       *types.WorkspaceMetadata
	resolveErr error
	ownErr     error

	resolveCalled bool
	resolveArg    string
	checkCalled   bool
	checkUserArg  string
	checkMetaArg  *types.WorkspaceMetadata
}

func (f *fakeWorkspaceAccessService) ResolveWorkspace(_ context.Context, workspaceID string) (*types.WorkspaceMetadata, error) {
	f.resolveCalled = true
	f.resolveArg = workspaceID
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	return f.meta, nil
}

func (f *fakeWorkspaceAccessService) CheckOwnership(_ context.Context, userID string, meta *types.WorkspaceMetadata) error {
	f.checkCalled = true
	f.checkUserArg = userID
	f.checkMetaArg = meta
	return f.ownErr
}

// buildAccessRouter wires a gin engine with an auth-shim that sets userID when
// the Authorization header is present, then the WorkspaceAccessMiddleware on
// the :id group, then a downstream handler that echoes the stored meta (if any)
// for assertion purposes.
func buildAccessRouter(t *testing.T, svc *fakeWorkspaceAccessService, withParam bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "" {
			c.Set("userID", "user-1")
		}
		c.Next()
	})

	handle := func(c *gin.Context) {
		meta, ok := middleware.WorkspaceMetaFromContext(c)
		c.JSON(http.StatusOK, gin.H{"ok": true, "hasMeta": ok, "metaID": metaIDOr(meta)})
	}

	if withParam {
		g := r.Group("/api/v1/workspaces/:id", middleware.WorkspaceAccessMiddleware(svc))
		g.GET("", handle)
		g.GET("/sub", handle)
	} else {
		// Register on a path WITHOUT :id so we can exercise the missing-param
		// branch of the middleware.
		r.GET("/api/v1/workspaces", middleware.WorkspaceAccessMiddleware(svc), handle)
	}
	return r
}

func metaIDOr(m *types.WorkspaceMetadata) string {
	if m == nil {
		return ""
	}
	return m.ID
}

func TestWorkspaceAccessMiddleware_Table(t *testing.T) {
	existingMeta := &types.WorkspaceMetadata{ID: "ws-1", UserID: "user-1"}

	tests := []struct {
		name       string
		svc        func() *fakeWorkspaceAccessService
		path       string
		authHeader bool
		wantStatus int
	}{
		{
			name:       "missing_userID_returns_401",
			svc:        func() *fakeWorkspaceAccessService { return &fakeWorkspaceAccessService{} },
			path:       "/api/v1/workspaces/ws-1",
			authHeader: false,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "missing_id_returns_400",
			svc:  func() *fakeWorkspaceAccessService { return &fakeWorkspaceAccessService{meta: existingMeta} },
			// Router registered without :id param → middleware sees empty c.Param("id").
			path:       "/api/v1/workspaces",
			authHeader: true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "resolve_not_found_returns_404",
			svc: func() *fakeWorkspaceAccessService {
				return &fakeWorkspaceAccessService{resolveErr: apierrors.NewNotFoundError("workspace", "ws-1", errors.New("nf"))}
			},
			path:       "/api/v1/workspaces/ws-1",
			authHeader: true,
			wantStatus: http.StatusNotFound,
		},
		{
			name: "resolve_internal_error_returns_500",
			svc: func() *fakeWorkspaceAccessService {
				return &fakeWorkspaceAccessService{resolveErr: apierrors.NewInternalError("workspace_retrieval_failed", errors.New("db down"))}
			},
			path:       "/api/v1/workspaces/ws-1",
			authHeader: true,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "resolve_bare_error_returns_500",
			svc: func() *fakeWorkspaceAccessService {
				return &fakeWorkspaceAccessService{resolveErr: errors.New("unexpected")}
			},
			path:       "/api/v1/workspaces/ws-1",
			authHeader: true,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "check_forbidden_returns_403",
			svc: func() *fakeWorkspaceAccessService {
				return &fakeWorkspaceAccessService{
					meta:   &types.WorkspaceMetadata{ID: "ws-1", UserID: "other"},
					ownErr: apierrors.NewForbiddenError("workspace access denied", errors.New("not owner")),
				}
			},
			path:       "/api/v1/workspaces/ws-1",
			authHeader: true,
			wantStatus: http.StatusForbidden,
		},
		{
			name: "check_bare_error_returns_500",
			svc: func() *fakeWorkspaceAccessService {
				return &fakeWorkspaceAccessService{meta: existingMeta, ownErr: errors.New("infra")}
			},
			path:       "/api/v1/workspaces/ws-1",
			authHeader: true,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "happy_path_returns_200",
			svc:        func() *fakeWorkspaceAccessService { return &fakeWorkspaceAccessService{meta: existingMeta} },
			path:       "/api/v1/workspaces/ws-1",
			authHeader: true,
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := tc.svc()
			withParam := tc.path != "/api/v1/workspaces"
			router := buildAccessRouter(t, svc, withParam)

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.authHeader {
				req.Header.Set("Authorization", "Bearer token")
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			assert.Equal(t, tc.wantStatus, rec.Code, "body=%s", rec.Body.String())
		})
	}
}

// TestWorkspaceAccessMiddleware_StoresMetaInContext asserts the resolved meta
// is propagated to the downstream handler via WorkspaceMetaFromContext AND is
// retrievable from c.Request.Context() via types.WorkspaceMetaFromCtx (the
// neutral accessor used by service-layer code that cannot import the
// middleware package).
func TestWorkspaceAccessMiddleware_StoresMetaInContext(t *testing.T) {
	meta := &types.WorkspaceMetadata{ID: "ws-1", UserID: "user-1", Name: "My Workspace"}
	svc := &fakeWorkspaceAccessService{meta: meta}
	router := buildAccessRouter(t, svc, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/sub", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.True(t, svc.resolveCalled)
	require.True(t, svc.checkCalled)
	assert.Equal(t, "ws-1", svc.resolveArg)
	assert.Equal(t, "user-1", svc.checkUserArg)
	require.NotNil(t, svc.checkMetaArg)
	assert.Equal(t, "ws-1", svc.checkMetaArg.ID)

	var body struct {
		Ok      bool   `json:"ok"`
		HasMeta bool   `json:"hasMeta"`
		MetaID  string `json:"metaID"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.True(t, body.Ok)
	assert.True(t, body.HasMeta, "downstream handler must observe stored meta")
	assert.Equal(t, "ws-1", body.MetaID)
}

// TestWorkspaceAccessMiddleware_PropagatesMetaToRequestContext is the
// design-0041 Story 2 contract: WorkspaceAccessMiddleware must store the
// resolved meta in c.Request.Context() under types.ContextKeyWorkspaceMeta so
// service-layer methods that read context.Context (not the gin context) can
// short-circuit their own ownership check. Without this propagation the
// middleware and the service layer would each have to fetch the metadata
// independently, doubling the DB hit on every /:id request.
func TestWorkspaceAccessMiddleware_PropagatesMetaToRequestContext(t *testing.T) {
	meta := &types.WorkspaceMetadata{ID: "ws-1", UserID: "user-1"}
	svc := &fakeWorkspaceAccessService{meta: meta}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	var sawFromCtx *types.WorkspaceMetadata
	var okFromCtx bool
	r.GET("/api/v1/workspaces/:id", middleware.WorkspaceAccessMiddleware(svc), func(c *gin.Context) {
		sawFromCtx, okFromCtx = types.WorkspaceMetaFromCtx(c.Request.Context())
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.True(t, okFromCtx, "meta must be retrievable from c.Request.Context() via types.WorkspaceMetaFromCtx")
	require.NotNil(t, sawFromCtx)
	assert.Equal(t, "ws-1", sawFromCtx.ID)
	assert.Same(t, meta, sawFromCtx, "must be the exact meta the middleware resolved")
}

// TestWorkspaceMetaFromContext_NotSet covers the (nil, false) return when no
// middleware has run (e.g. handler invoked directly in unit tests).
func TestWorkspaceMetaFromContext_NotSet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	meta, ok := middleware.WorkspaceMetaFromContext(c)
	assert.False(t, ok)
	assert.Nil(t, meta)
}

// TestWorkspaceAccessMiddleware_NilMetaAtCheck_Returns403 covers the
// fail-closed contract: if a caller wired the middleware without Resolve
// returning a meta (impossible today, but defensive), CheckOwnership gets nil
// and the middleware must surface 403 — never 200.
func TestWorkspaceAccessMiddleware_NilMetaAtCheck_Returns403(t *testing.T) {
	svc := &fakeWorkspaceAccessService{
		meta:   nil,
		ownErr: apierrors.NewForbiddenError("workspace access denied", errors.New("nil metadata")),
	}
	router := buildAccessRouter(t, svc, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
}
