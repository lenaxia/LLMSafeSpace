package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/pkg/types"
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
	assert.False(t, cfg.RegistrationEnabled)
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

	svc.auth.On("Login", mock.Anything, mock.Anything).Return(&types.AuthResponse{
		Token: "jwt-token-123",
		User:  types.User{ID: "u1", Username: "alice", Email: "alice@test.com", Role: "user", Active: true},
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
	assert.Equal(t, 86400, sessionCookie.MaxAge)
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

	svc.auth.On("Register", mock.Anything, mock.Anything).Return(&types.AuthResponse{
		Token: "new-jwt-456",
		User:  types.User{ID: "u2", Username: "bob", Email: "bob@test.com", Role: "user", Active: true},
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
