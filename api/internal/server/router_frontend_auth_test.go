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

	apilogger "github.com/lenaxia/llmsafespaces/api/internal/logger"
	imocks "github.com/lenaxia/llmsafespaces/api/internal/mocks"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// --- GET /api/v1/auth/config ---

func TestAuthConfig_ReturnsFeatureFlags(t *testing.T) {
	router, _ := newAuthFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var cfg types.AuthConfig
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &cfg))
	assert.True(t, cfg.RegistrationEnabled)
	assert.False(t, cfg.OIDCEnabled)
}

func TestAuthConfig_NoAuthRequired(t *testing.T) {
	router, _ := newAuthFixture(t)

	// No Authorization header — should still work
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

// --- POST /api/v1/auth/login (cookie setting) ---

func TestLogin_SetsCookie(t *testing.T) {
	router, svc := newAuthFixture(t)

	// Explicit TokenTTL=24h so we test the configured-TTL path, not the zero-guard fallback.
	svc.auth.On("Login", mock.Anything, mock.Anything).Return(&types.AuthResponse{
		Token:    "jwt-token-123",
		User:     types.User{ID: "u1", Username: "alice", Email: "alice@test.com", Role: "user", Active: true},
		TokenTTL: 24 * time.Hour,
	}, nil)

	body, _ := json.Marshal(types.LoginRequest{Email: "alice@test.com", Password: "pass123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify cookie is set
	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "lsp_session" {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "lsp_session cookie should be set")
	assert.Equal(t, "jwt-token-123", sessionCookie.Value)
	assert.True(t, sessionCookie.HttpOnly)
	assert.True(t, sessionCookie.Secure)
	assert.Equal(t, "/", sessionCookie.Path)
	assert.Equal(t, 86400, sessionCookie.MaxAge) // 24h = 86400s from explicit TokenTTL
}

func TestLogin_Failure_NoCookie(t *testing.T) {
	router, svc := newAuthFixture(t)

	svc.auth.On("Login", mock.Anything, mock.Anything).Return(nil, errors.New("invalid credentials"))

	body, _ := json.Marshal(types.LoginRequest{Email: "alice@test.com", Password: "wrong"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	// No cookie should be set on failure
	cookies := rec.Result().Cookies()
	for _, c := range cookies {
		assert.NotEqual(t, "lsp_session", c.Name)
	}
}

// --- POST /api/v1/auth/register (cookie setting) ---

func TestRegister_SetsCookie(t *testing.T) {
	router, svc := newAuthFixture(t)

	// Explicit TokenTTL=24h so we test the configured-TTL path, not the zero-guard fallback.
	svc.auth.On("Register", mock.Anything, mock.Anything).Return(&types.AuthResponse{
		Token:    "new-jwt-456",
		User:     types.User{ID: "u2", Username: "bob", Email: "bob@test.com", Role: "user", Active: true},
		TokenTTL: 24 * time.Hour,
	}, nil)

	body, _ := json.Marshal(types.RegisterRequest{Username: "bob", Email: "bob@test.com", Password: "pass1234"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "lsp_session" {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "lsp_session cookie should be set on register")
	assert.Equal(t, "new-jwt-456", sessionCookie.Value)
	assert.True(t, sessionCookie.HttpOnly)
}

func TestRegister_Failure_NoCookie(t *testing.T) {
	router, svc := newAuthFixture(t)

	svc.auth.On("Register", mock.Anything, mock.Anything).Return(nil, errors.New("email taken"))

	body, _ := json.Marshal(types.RegisterRequest{Username: "bob", Email: "bob@test.com", Password: "pass1234"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	cookies := rec.Result().Cookies()
	for _, c := range cookies {
		assert.NotEqual(t, "lsp_session", c.Name)
	}
}

// --- POST /api/v1/auth/logout ---

func TestLogout_ClearsCookie(t *testing.T) {
	router, _ := newAuthFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)

	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "lsp_session" {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "lsp_session cookie should be cleared")
	assert.Equal(t, "", sessionCookie.Value)
	assert.True(t, sessionCookie.MaxAge < 0, "MaxAge should be negative to delete cookie")
}

func TestLogout_NoAuthRequired(t *testing.T) {
	router, _ := newAuthFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

// --- GET /api/v1/auth/me ---

func TestMe_ReturnsUser(t *testing.T) {
	router, svc := newAuthenticatedFixture(t, "user-1")

	svc.database.On("GetUser", mock.Anything, "user-1").Return(&types.User{
		ID: "user-1", Username: "alice", Email: "alice@test.com", Role: "user", Active: true,
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var user types.User
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &user))
	assert.Equal(t, "alice", user.Username)
	assert.Equal(t, "user-1", user.ID)
}

func TestMe_Unauthenticated_Returns401(t *testing.T) {
	router, _ := newAuthFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestMe_DBError_Returns500(t *testing.T) {
	router, svc := newAuthenticatedFixture(t, "user-1")

	svc.database.On("GetUser", mock.Anything, "user-1").Return(nil, errors.New("db down"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// =============================================================================
// G18 — /auth/logout must call RevokeToken (Epic 17 Phase 4 RT-4.13)
// =============================================================================
//
// Pre-fix: the logout handler only cleared the lsp_session cookie;
// the JWT remained valid and could be replayed via Authorization header
// or via re-supplying the cookie value (an attacker who captured it
// before logout). This contradicted the threat model's promise that
// /auth/logout invalidates the active session.
//
// Fix contract:
//   * On logout, if a JWT token is present (via cookie OR Authorization
//     header), the handler calls authSvc.RevokeToken(token).
//   * If the token looks like an API key (lsp_ prefix), RevokeToken is
//     NOT called — API keys are managed via /api-keys/:id DELETE, not
//     via session logout.
//   * RevokeToken errors do NOT prevent the cookie from being cleared
//     and 204 from being returned. Logout must always succeed from the
//     user's perspective; the revocation is best-effort defense-in-depth.

func TestG18Logout_RevokesCookieToken(t *testing.T) {
	router, svc := newAuthFixture(t)
	svc.auth.On("RevokeToken", "captured-jwt-token").Return(nil).Once()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "lsp_session", Value: "captured-jwt-token"})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	svc.auth.AssertCalled(t, "RevokeToken", "captured-jwt-token")
}

func TestG18Logout_RevokesBearerToken(t *testing.T) {
	router, svc := newAuthFixture(t)
	svc.auth.On("RevokeToken", "header-jwt-token").Return(nil).Once()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer header-jwt-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	svc.auth.AssertCalled(t, "RevokeToken", "header-jwt-token")
}

func TestG18Logout_NoTokenSkipsRevoke(t *testing.T) {
	router, svc := newAuthFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	svc.auth.AssertNotCalled(t, "RevokeToken", mock.Anything)
}

func TestG18Logout_APIKeySkipsRevoke(t *testing.T) {
	// API keys are managed via /api-keys/:id DELETE. Calling RevokeToken
	// on them would parse-fail (they aren't JWTs); skip cleanly.
	router, svc := newAuthFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer lsp_apikey_aaaaaaaaaaaaaaaa")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	svc.auth.AssertNotCalled(t, "RevokeToken", mock.Anything)
}

func TestG18Logout_RevokeFailureStillClearsCookie(t *testing.T) {
	// Revocation can fail (cache outage, token already expired, etc.).
	// The user has clicked Logout; we MUST still clear the cookie and
	// return 204 so the UI flips to logged-out state. Surfacing the
	// revoke failure as a 5xx would force the user to retry logout
	// indefinitely — a worse failure mode than best-effort revoke.
	router, svc := newAuthFixture(t)
	svc.auth.On("RevokeToken", "tok").Return(errors.New("cache unavailable")).Once()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "lsp_session", Value: "tok"})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)

	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "lsp_session" {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie)
	assert.True(t, sessionCookie.MaxAge < 0, "cookie must be cleared even when revoke fails")
}

// ---- Epic 34 US-34.1: new cookie and remember-me tests ----

func TestLogin_RememberMe_CookieMaxAge30Days(t *testing.T) {
	router, svc := newAuthFixture(t)
	svc.auth.On("Login", mock.Anything, mock.Anything).Return(&types.AuthResponse{
		Token:    "jwt-30d",
		User:     types.User{ID: "u1", Username: "alice", Email: "alice@test.com", Role: "user", Active: true},
		TokenTTL: 720 * time.Hour, // 30 days
	}, nil)

	body, _ := json.Marshal(types.LoginRequest{Email: "alice@test.com", Password: "pass", RememberMe: true})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "lsp_session" {
			cookie = c
			break
		}
	}
	require.NotNil(t, cookie, "lsp_session cookie should be set")
	assert.Equal(t, 2592000, cookie.MaxAge, "Max-Age should be 30 days (720h = 2592000s)")
}

func TestLogin_ZeroTokenTTL_FallbackMaxAge(t *testing.T) {
	// When mock returns TokenTTL=0, the zero-guard should produce MaxAge=86400.
	// This documents the fallback path explicitly.
	router, svc := newAuthFixture(t)
	svc.auth.On("Login", mock.Anything, mock.Anything).Return(&types.AuthResponse{
		Token: "jwt-zero",
		User:  types.User{ID: "u1", Username: "alice", Email: "alice@test.com", Role: "user", Active: true},
		// TokenTTL intentionally omitted (zero value) — tests the fallback guard
	}, nil)

	body, _ := json.Marshal(types.LoginRequest{Email: "alice@test.com", Password: "pass"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "lsp_session" {
			cookie = c
			break
		}
	}
	require.NotNil(t, cookie, "lsp_session cookie should be set even with zero TokenTTL")
	assert.Equal(t, 86400, cookie.MaxAge, "zero-guard should produce Max-Age=86400")
}

func TestLogin_TokenTTLNotInResponseBody(t *testing.T) {
	router, svc := newAuthFixture(t)
	svc.auth.On("Login", mock.Anything, mock.Anything).Return(&types.AuthResponse{
		Token:    "jwt-tok",
		User:     types.User{ID: "u1", Username: "alice", Email: "alice@test.com", Role: "user", Active: true},
		TokenTTL: 24 * time.Hour,
	}, nil)

	body, _ := json.Marshal(types.LoginRequest{Email: "alice@test.com", Password: "pass"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
	if _, ok := m["tokenTTL"]; ok {
		t.Error("tokenTTL must not appear in the JSON response body (json:\"-\" tag)")
	}
	if _, ok := m["token_ttl"]; ok {
		t.Error("token_ttl must not appear in the JSON response body")
	}
}

func TestCookieName_FromRouterConfig(t *testing.T) {
	// Router with custom CookieName should use that name for the session cookie.
	gin.SetMode(gin.TestMode)
	log, _ := apilogger.New(false, "error", "json")

	auth := &imocks.MockAuthMiddlewareService{}
	met := &imocks.MockMetricsService{}
	db := &imocks.MockDatabaseService{}
	ca := &imocks.MockCacheService{}

	met.On("RecordRequest", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	auth.On("AuthMiddleware").Return(gin.HandlerFunc(func(c *gin.Context) { c.Next() })).Maybe()
	auth.On("GetUserID", mock.Anything).Return("").Maybe()
	auth.On("Login", mock.Anything, mock.Anything).Return(&types.AuthResponse{
		Token:    "jwt-custom",
		User:     types.User{ID: "u1", Username: "alice", Email: "alice@test.com", Role: "user", Active: true},
		TokenTTL: 24 * time.Hour,
	}, nil)

	svc := &authMockServices{auth: auth, metrics: met, database: db, cache: ca}
	router := NewRouter(svc, log, nil, RouterConfig{Debug: false, CookieName: "my_session"})

	body, _ := json.Marshal(types.LoginRequest{Email: "alice@test.com", Password: "pass"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var found *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "my_session" {
			found = c
			break
		}
	}
	require.NotNil(t, found, "custom cookie name 'my_session' should be used")
	assert.Equal(t, "jwt-custom", found.Value)
}

func TestCookieName_DefaultsToLspSession(t *testing.T) {
	// Router with empty CookieName falls back to "lsp_session".
	router, svc := newAuthFixture(t) // newAuthFixture uses RouterConfig{Debug: false} → CookieName=""
	svc.auth.On("Login", mock.Anything, mock.Anything).Return(&types.AuthResponse{
		Token:    "jwt-default",
		User:     types.User{ID: "u1", Username: "alice", Email: "alice@test.com", Role: "user", Active: true},
		TokenTTL: 24 * time.Hour,
	}, nil)

	body, _ := json.Marshal(types.LoginRequest{Email: "alice@test.com", Password: "pass"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var found *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "lsp_session" {
			found = c
			break
		}
	}
	require.NotNil(t, found, "default cookie name 'lsp_session' should be used when CookieName is empty")
}

func TestLogout_ClearsCorrectCookie(t *testing.T) {
	// When a custom cookie name is configured, logout should clear that name.
	gin.SetMode(gin.TestMode)
	log, _ := apilogger.New(false, "error", "json")

	auth := &imocks.MockAuthMiddlewareService{}
	met := &imocks.MockMetricsService{}
	db := &imocks.MockDatabaseService{}
	ca := &imocks.MockCacheService{}

	met.On("RecordRequest", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	auth.On("AuthMiddleware").Return(gin.HandlerFunc(func(c *gin.Context) { c.Next() })).Maybe()
	auth.On("GetUserID", mock.Anything).Return("").Maybe()
	auth.On("RevokeToken", mock.Anything).Return(nil).Maybe()

	svc := &authMockServices{auth: auth, metrics: met, database: db, cache: ca}
	router := NewRouter(svc, log, nil, RouterConfig{Debug: false, CookieName: "my_session"})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	var cleared *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "my_session" {
			cleared = c
			break
		}
	}
	require.NotNil(t, cleared, "custom cookie 'my_session' should be cleared on logout")
	assert.True(t, cleared.MaxAge < 0, "MaxAge should be negative to delete cookie")
}
