// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	apilogger "github.com/lenaxia/llmsafespace/api/internal/logger"
	imocks "github.com/lenaxia/llmsafespace/api/internal/mocks"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

type authMockServices struct {
	auth     *imocks.MockAuthMiddlewareService
	metrics  *imocks.MockMetricsService
	database *imocks.MockDatabaseService
	cache    *imocks.MockCacheService
}

func (s *authMockServices) GetAuth() interfaces.AuthService               { return s.auth }
func (s *authMockServices) GetDatabase() interfaces.DatabaseService       { return s.database }
func (s *authMockServices) GetCache() interfaces.CacheService             { return s.cache }
func (s *authMockServices) GetMetrics() interfaces.MetricsService         { return s.metrics }
func (s *authMockServices) GetWorkspace() interfaces.WorkspaceService     { return nil }
func (s *authMockServices) GetRateLimiter() interfaces.RateLimiterService { return nil }
func (s *authMockServices) GetMetering() interfaces.MeteringService     { return nil }

func newAuthFixture(t *testing.T) (*gin.Engine, *authMockServices) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	log, err := apilogger.New(false, "error", "json")
	require.NoError(t, err, "logger init failed")

	auth := &imocks.MockAuthMiddlewareService{}
	met := &imocks.MockMetricsService{}
	db := &imocks.MockDatabaseService{}
	ca := &imocks.MockCacheService{}

	met.On("RecordRequest", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	auth.On("AuthMiddleware").Return(gin.HandlerFunc(func(c *gin.Context) { c.Next() })).Maybe()
	auth.On("GetUserID", mock.Anything).Return("").Maybe()

	svc := &authMockServices{auth: auth, metrics: met, database: db, cache: ca}
	router := NewRouter(svc, log, nil, RouterConfig{Debug: false})
	return router, svc
}

func newAuthenticatedFixture(t *testing.T, userID string) (*gin.Engine, *authMockServices) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	log, err := apilogger.New(false, "error", "json")
	require.NoError(t, err, "logger init failed")

	auth := &imocks.MockAuthMiddlewareService{}
	met := &imocks.MockMetricsService{}
	db := &imocks.MockDatabaseService{}
	ca := &imocks.MockCacheService{}

	met.On("RecordRequest", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	auth.On("AuthMiddleware").Return(gin.HandlerFunc(func(c *gin.Context) {
		c.Set("userID", userID)
		c.Next()
	})).Maybe()
	auth.On("GetUserID", mock.Anything).Return(userID).Maybe()

	svc := &authMockServices{auth: auth, metrics: met, database: db, cache: ca}
	router := NewRouter(svc, log, nil, RouterConfig{Debug: false})
	return router, svc
}

func doRequest(t *testing.T, router *gin.Engine, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		req = httptest.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// --- Register ---

func TestRegister_Success(t *testing.T) {
	router, svc := newAuthFixture(t)

	resp := &types.AuthResponse{
		Token: "jwt-token",
		User: types.User{
			ID:       "user-1",
			Username: "testuser",
			Email:    "test@example.com",
			Active:   true,
			Role:     "user",
		},
	}
	svc.auth.On("Register", mock.Anything, mock.MatchedBy(func(r types.RegisterRequest) bool {
		return r.Username == "testuser" && r.Email == "test@example.com" && len(r.Password) >= 8
	})).Return(resp, nil)

	rec := doRequest(t, router, http.MethodPost, "/api/v1/auth/register", types.RegisterRequest{
		Username: "testuser",
		Email:    "test@example.com",
		Password: "securepassword123",
	})

	assert.Equal(t, http.StatusCreated, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "jwt-token", body["token"])
}

func TestRegister_MissingFields(t *testing.T) {
	router, _ := newAuthFixture(t)

	rec := doRequest(t, router, http.MethodPost, "/api/v1/auth/register", map[string]string{
		"username": "testuser",
	})

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegister_ShortPassword(t *testing.T) {
	router, _ := newAuthFixture(t)

	rec := doRequest(t, router, http.MethodPost, "/api/v1/auth/register", types.RegisterRequest{
		Username: "testuser",
		Email:    "test@example.com",
		Password: "short",
	})

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegister_DuplicateEmail(t *testing.T) {
	router, svc := newAuthFixture(t)

	svc.auth.On("Register", mock.Anything, mock.Anything).Return(nil, errors.New("registration failed"))

	rec := doRequest(t, router, http.MethodPost, "/api/v1/auth/register", types.RegisterRequest{
		Username: "testuser",
		Email:    "taken@example.com",
		Password: "securepassword123",
	})

	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "registration failed", body["error"])
	assert.NotContains(t, body, "email", "error must not leak email existence")
}

func TestRegister_InvalidEmail(t *testing.T) {
	router, _ := newAuthFixture(t)

	rec := doRequest(t, router, http.MethodPost, "/api/v1/auth/register", types.RegisterRequest{
		Username: "testuser",
		Email:    "not-an-email",
		Password: "securepassword123",
	})

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// --- Login ---

func TestLogin_Success(t *testing.T) {
	router, svc := newAuthFixture(t)

	resp := &types.AuthResponse{
		Token: "jwt-token",
		User: types.User{
			ID:       "user-1",
			Username: "testuser",
			Email:    "test@example.com",
		},
	}
	svc.auth.On("Login", mock.Anything, mock.MatchedBy(func(r types.LoginRequest) bool {
		return r.Email == "test@example.com"
	})).Return(resp, nil)

	rec := doRequest(t, router, http.MethodPost, "/api/v1/auth/login", types.LoginRequest{
		Email:    "test@example.com",
		Password: "securepassword123",
	})

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "jwt-token", body["token"])
}

func TestLogin_InvalidCredentials(t *testing.T) {
	router, svc := newAuthFixture(t)

	svc.auth.On("Login", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	rec := doRequest(t, router, http.MethodPost, "/api/v1/auth/login", types.LoginRequest{
		Email:    "test@example.com",
		Password: "wrongpassword",
	})

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestLogin_MissingFields(t *testing.T) {
	router, _ := newAuthFixture(t)

	rec := doRequest(t, router, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": "test@example.com",
	})

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// --- API Keys (authenticated) ---

func TestCreateAPIKey_Success(t *testing.T) {
	router, svc := newAuthenticatedFixture(t, "user-1")

	apiKey := &types.APIKey{
		ID:     "key-1",
		Name:   "my-key",
		Key:    "lsp_abc123",
		Prefix: "lsp_",
		Active: true,
	}
	svc.auth.On("CreateAPIKey", mock.Anything, "user-1", mock.MatchedBy(func(r types.CreateAPIKeyRequest) bool {
		return r.Name == "my-key"
	}), mock.Anything).Return(apiKey, nil)

	rec := doRequest(t, router, http.MethodPost, "/api/v1/auth/api-keys", types.CreateAPIKeyRequest{Name: "my-key"})

	assert.Equal(t, http.StatusCreated, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "lsp_abc123", body["key"])
}

func TestCreateAPIKey_Unauthenticated(t *testing.T) {
	router, _ := newAuthFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/api-keys", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// The middleware passes through in tests (c.Next()), but GetUserID returns "",
	// so the handler returns 401.
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestListAPIKeys_Success(t *testing.T) {
	router, svc := newAuthenticatedFixture(t, "user-1")

	keys := []*types.APIKey{
		{ID: "key-1", Name: "key-one", Prefix: "lsp_", Active: true},
		{ID: "key-2", Name: "key-two", Prefix: "lsp_", Active: true},
	}
	svc.auth.On("ListAPIKeys", mock.Anything, "user-1").Return(keys, nil)

	rec := doRequest(t, router, http.MethodGet, "/api/v1/auth/api-keys", nil)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body []*types.APIKey
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Len(t, body, 2)
}

func TestListAPIKeys_Empty(t *testing.T) {
	router, svc := newAuthenticatedFixture(t, "user-1")

	svc.auth.On("ListAPIKeys", mock.Anything, "user-1").Return([]*types.APIKey{}, nil)

	rec := doRequest(t, router, http.MethodGet, "/api/v1/auth/api-keys", nil)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "[]", rec.Body.String())
}

func TestDeleteAPIKey_Success(t *testing.T) {
	router, svc := newAuthenticatedFixture(t, "user-1")

	svc.auth.On("DeleteAPIKey", mock.Anything, "user-1", "key-1").Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/api-keys/key-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestDeleteAPIKey_NotFound(t *testing.T) {
	router, svc := newAuthenticatedFixture(t, "user-1")

	svc.auth.On("DeleteAPIKey", mock.Anything, "user-1", "nonexistent").Return(assert.AnError)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/api-keys/nonexistent", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// --- Auth routes bypass auth middleware for register/login ---

func TestAuthRoutes_NotBehindAuthMiddleware(t *testing.T) {
	router, svc := newAuthFixture(t)

	svc.auth.On("Register", mock.Anything, mock.Anything).Return(&types.AuthResponse{
		Token: "t",
		User:  types.User{ID: "u", Username: "u", Email: "e@e.com"},
	}, nil)

	rec := doRequest(t, router, http.MethodPost, "/api/v1/auth/register", types.RegisterRequest{
		Username: "u",
		Email:    "e@e.com",
		Password: "password123",
	})

	assert.NotEqual(t, http.StatusUnauthorized, rec.Code, "register should not require auth")
}

// --- Auth routes registered ---

func TestAuthRoutes_Registered(t *testing.T) {
	router, _ := newAuthFixture(t)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/auth/register"},
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/auth/api-keys"},
		{http.MethodGet, "/api/v1/auth/api-keys"},
		{http.MethodDelete, "/api/v1/auth/api-keys/some-id"},
	}

	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.NotEqual(t, http.StatusNotFound, rec.Code,
			"route %s %s should be registered (got %d)", rt.method, rt.path, rec.Code)
	}
}

// --- Auth response does not leak password hash ---

func TestAuthResponse_NoPasswordLeak(t *testing.T) {
	u := types.User{
		ID:           "u1",
		Username:     "test",
		Email:        "test@test.com",
		PasswordHash: "$2a$10$supersecrethash",
		Active:       true,
		Role:         "user",
	}

	b, err := json.Marshal(u)
	require.NoError(t, err)

	assert.NotContains(t, string(b), "supersecrethash",
		"User JSON must not contain password_hash (json:\"-\" tag)")
	assert.NotContains(t, string(b), "password_hash",
		"User JSON must not contain password_hash field name")
}

// --- APIKey type serialization ---

func TestAPIKey_KeyOmittedWhenEmpty(t *testing.T) {
	k := types.APIKey{
		ID:     "k1",
		Name:   "test",
		Key:    "",
		Prefix: "lsp_",
		Active: true,
	}

	b, err := json.Marshal(k)
	require.NoError(t, err)

	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &m))

	_, hasKey := m["key"]
	assert.False(t, hasKey, "APIKey.Key should be omitted when empty (omitempty)")
}

func TestAPIKey_ExpiresAtOmittedWhenNil(t *testing.T) {
	k := types.APIKey{
		ID:     "k1",
		Name:   "test",
		Prefix: "lsp_",
		Active: true,
	}

	b, err := json.Marshal(k)
	require.NoError(t, err)

	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &m))

	_, hasExpires := m["expiresAt"]
	assert.False(t, hasExpires, "APIKey.ExpiresAt should be omitted when nil")
}

func TestAPIKey_ExpiresAtPresentWhenSet(t *testing.T) {
	now := time.Now()
	k := types.APIKey{
		ID:        "k1",
		Name:      "test",
		Prefix:    "lsp_",
		Active:    true,
		ExpiresAt: &now,
	}

	b, err := json.Marshal(k)
	require.NoError(t, err)

	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &m))

	_, hasExpires := m["expiresAt"]
	assert.True(t, hasExpires, "APIKey.ExpiresAt should be present when set")
}
