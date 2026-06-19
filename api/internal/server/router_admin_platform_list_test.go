// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/api/internal/handlers"
	apilogger "github.com/lenaxia/llmsafespaces/api/internal/logger"
	imocks "github.com/lenaxia/llmsafespaces/api/internal/mocks"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// platformListOrgStore + platformListUserStore satisfy the platform-admin
// handler's store interfaces (handlers.platformAdminOrgStore /
// platformAdminUserStore) via structural typing. They return a single fixed
// row so the admin path can assert a real 200 body.

type platformListOrgStore struct{}

func (platformListOrgStore) UpdateOrgStatus(context.Context, string, *types.OrgStatus, *types.OrgSubscriptionStatus, *types.OrgPlan) error {
	return nil
}
func (platformListOrgStore) LogAuditEvent(context.Context, string, string, string, string, *string, map[string]any) error {
	return nil
}
func (platformListOrgStore) OrgsWhereUserIsLastActiveAdmin(context.Context, string) ([]types.LastAdminOrg, error) {
	return nil, nil
}
func (platformListOrgStore) ListAllOrgs(_ context.Context, limit, offset int, _ *string) ([]types.OrgSummary, *types.PaginationMetadata, error) {
	return []types.OrgSummary{{Organization: types.Organization{ID: "org-1", Name: "Acme"}}},
		&types.PaginationMetadata{Total: 1, Limit: limit, Offset: offset}, nil
}

type platformListUserStore struct{}

func (platformListUserStore) SetUserStatus(context.Context, string, types.UserStatus) error {
	return nil
}
func (platformListUserStore) ListAllUsers(_ context.Context, limit, offset int, _ *string) ([]types.UserListEntry, *types.PaginationMetadata, error) {
	return []types.UserListEntry{{ID: "user-1", Email: "a@example.com", Role: "user", Status: types.UserStatusActive}},
		&types.PaginationMetadata{Total: 1, Limit: limit, Offset: offset}, nil
}

// newPlatformListRouter builds a real NewRouter with the PlatformAdminHandler
// wired and an auth middleware that stamps userRole=role. This proves the
// /api/v1/admin/orgs + /api/v1/admin/users routes are registered through the
// live wiring path and are protected by AdminGuard.
func newPlatformListRouter(t *testing.T, role string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log, err := apilogger.New(false, "error", "json")
	require.NoError(t, err)

	auth := &imocks.MockAuthMiddlewareService{}
	met := &imocks.MockMetricsService{}
	met.On("RecordRequest", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	met.On("IncrementActiveConnections", mock.Anything, mock.Anything).Maybe()
	met.On("DecrementActiveConnections", mock.Anything, mock.Anything).Maybe()

	auth.On("AuthMiddleware").Return(gin.HandlerFunc(func(c *gin.Context) {
		c.Set("userID", "test-user")
		c.Set("userRole", role)
		c.Next()
	}))
	auth.On("GetUserID", mock.Anything).Return("test-user")

	svc := &mockServices{auth: auth, metrics: met}
	cfg := RouterConfig{
		Debug: false,
		PlatformAdminHandler: handlers.NewPlatformAdminHandler(
			platformListOrgStore{}, platformListUserStore{}, nil, nil,
		),
	}
	return NewRouter(svc, log, nil, cfg)
}

// TestPlatformListRoutes_AdminGuard_BlocksNonAdmin is the integration
// counterpart to the unit-level AdminGuard test: it confirms the router mounts
// AdminGuard on /api/v1/admin/orgs + /api/v1/admin/users so a non-admin gets
// 404 BEFORE the handler runs.
func TestPlatformListRoutes_AdminGuard_BlocksNonAdmin(t *testing.T) {
	router := newPlatformListRouter(t, "user")

	for _, p := range []string{"/api/v1/admin/orgs", "/api/v1/admin/users"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code,
			"non-admin must get 404 on %s, body=%s", p, rec.Body.String())
	}
}

// TestPlatformListRoutes_AdminGuard_AllowsAdmin confirms an admin reaches the
// handler and gets 200 with the items.
func TestPlatformListRoutes_AdminGuard_AllowsAdmin(t *testing.T) {
	router := newPlatformListRouter(t, "admin")

	t.Run("orgs", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/orgs", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		assert.Contains(t, rec.Body.String(), "Acme", "response must contain the org item")
		assert.Contains(t, rec.Body.String(), "\"pagination\"", "response must contain pagination metadata")
	})

	t.Run("users", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		assert.Contains(t, rec.Body.String(), "a@example.com", "response must contain the user item")
	})
}
