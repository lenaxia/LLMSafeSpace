// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// These tests lock in the post-merge fixes for worklog 0372 findings F3 + F4
// (US-43.19): the auth middleware must FAIL CLOSED when it cannot confirm a
// user is not suspended (F3), and suspending a user must immediately revoke
// their live tokens without depending on the DB (F4).
//
// They reuse the minimal mockDB/mockCache from auth_sessionid_test.go and layer
// error-injection + marker-tracking on top via embedding, so the full
// interfaces stay satisfied without re-declaring ~30 no-op methods.

// suspensionCache wraps mockCache, recording writes/deletes to the
// user_suspended:<userID> marker and serving it back from Get so the middleware
// can exercise the fast-path rejection. Non-marker keys delegate to the
// underlying no-op mock (cache miss).
type suspensionCache struct {
	*mockCache
	mu          sync.Mutex
	suspended   map[string]bool
	setMarkers  []string
	delMarkers  []string
	setErr      error
	delErr      error
	getMarkerOK bool // when false, Get reports a marker miss even if set
}

func newSuspensionCache() *suspensionCache {
	return &suspensionCache{mockCache: &mockCache{}, suspended: map[string]bool{}, getMarkerOK: true}
}

func (c *suspensionCache) Get(ctx context.Context, key string) (string, error) {
	if strings.HasPrefix(key, "user_suspended:") {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.getMarkerOK && c.suspended[strings.TrimPrefix(key, "user_suspended:")] {
			return "1", nil
		}
		return "", nil
	}
	return c.mockCache.Get(ctx, key)
}

func (c *suspensionCache) Set(ctx context.Context, key, value string, _ time.Duration) error {
	if c.setErr != nil {
		return c.setErr
	}
	if strings.HasPrefix(key, "user_suspended:") {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.suspended[strings.TrimPrefix(key, "user_suspended:")] = value != ""
		c.setMarkers = append(c.setMarkers, key)
		return nil
	}
	return c.mockCache.Set(ctx, key, value, 0)
}

func (c *suspensionCache) Delete(ctx context.Context, key string) error {
	if strings.HasPrefix(key, "user_suspended:") {
		if c.delErr != nil {
			return c.delErr
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.suspended, strings.TrimPrefix(key, "user_suspended:"))
		c.delMarkers = append(c.delMarkers, key)
		return nil
	}
	return c.mockCache.Delete(ctx, key)
}

// errDB wraps mockDB, forcing GetUser to return an injected error (F3 path).
type errDB struct {
	*mockDB
	getUserErr error
}

func (d *errDB) GetUser(ctx context.Context, userID string) (*types.User, error) {
	if d.getUserErr != nil {
		return nil, d.getUserErr
	}
	return d.mockDB.GetUser(ctx, userID)
}

func newAuthSvc(t *testing.T, db interfaces.DatabaseService, cache *suspensionCache) *Service {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := testConfig()
	svc, err := New(cfg, testLogger(), db, cache)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

// buildSuspendedRouter wires AuthMiddleware to a handler that records whether
// the request reached it (so tests can assert fail-closed aborted before it).
func buildSuspendedRouter(t *testing.T, svc *Service) (*gin.Engine, *bool) {
	t.Helper()
	reached := false
	router := gin.New()
	router.Use(svc.AuthMiddleware())
	router.GET("/secure", func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})
	return router, &reached
}

// --- F3: fail-closed on DB error ---

// TestAuthMiddleware_GetUserError_FailsClosed proves the auth middleware does
// NOT let a request through when GetUser fails. Pre-fix it silently fell through
// to c.Next(), so a suspended user regained access during any DB blip.
func TestAuthMiddleware_GetUserError_FailsClosed(t *testing.T) {
	cache := newSuspensionCache()
	db := &errDB{mockDB: &mockDB{}, getUserErr: errors.New("db connection lost")}
	svc := newAuthSvc(t, db, cache)

	token, err := svc.GenerateToken("user-123")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	router, reached := buildSuspendedRouter(t, svc)

	req := httptest.NewRequest("GET", "/secure", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (fail-closed) on GetUser error, got %d: %s", w.Code, w.Body.String())
	}
	if *reached {
		t.Fatal("handler must NOT be reached when account status cannot be verified (fail-closed)")
	}
}

// TestAuthMiddleware_MissingUserRow_FailsClosed covers the (userID valid but no
// DB row) case: an inconsistent state that must not silently pass.
func TestAuthMiddleware_MissingUserRow_FailsClosed(t *testing.T) {
	cache := newSuspensionCache()
	db := &errDB{mockDB: &mockDB{users: map[string]*mockUser{}}} // no row for user-123
	svc := newAuthSvc(t, db, cache)

	token, err := svc.GenerateToken("user-123")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	router, reached := buildSuspendedRouter(t, svc)

	req := httptest.NewRequest("GET", "/secure", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (fail-closed) for missing user row, got %d", w.Code)
	}
	if *reached {
		t.Fatal("handler must NOT be reached when the user row is missing")
	}
}

// TestAuthMiddleware_ActiveUserStillAllowed is the regression guard: a normal
// active user still reaches the handler after the fail-closed change.
func TestAuthMiddleware_ActiveUserStillAllowed(t *testing.T) {
	cache := newSuspensionCache()
	db := &errDB{mockDB: &mockDB{users: map[string]*mockUser{
		"user-123": {ID: "user-123", Role: "user", Active: true, Status: types.UserStatusActive},
	}}}
	svc := newAuthSvc(t, db, cache)

	token, err := svc.GenerateToken("user-123")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	router, reached := buildSuspendedRouter(t, svc)

	req := httptest.NewRequest("GET", "/secure", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for active user, got %d: %s", w.Code, w.Body.String())
	}
	if !*reached {
		t.Fatal("active user must reach the handler")
	}
}

// --- F4: revocation marker ---

// TestSuspensionMarkerTTL_CoversRememberMe locks the F4 TTL fix: the marker
// must outlive the longest-lived token (remember-me, 720h default), not just
// the standard token duration (24h). A regression that used tokenDuration
// alone would let a suspended user's remember-me session resume 24h after
// suspension if the DB were also down — the marker would be gone while the
// token was still valid.
func TestSuspensionMarkerTTL_CoversRememberMe(t *testing.T) {
	cases := []struct {
		name            string
		token, remember time.Duration
		want            time.Duration
	}{
		{"remember_me_longer", 24 * time.Hour, 720 * time.Hour, 720 * time.Hour},
		{"token_longer", 48 * time.Hour, 24 * time.Hour, 48 * time.Hour},
		{"remember_me_unset", 24 * time.Hour, 0, 24 * time.Hour},
		{"equal", 24 * time.Hour, 24 * time.Hour, 24 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := suspensionMarkerTTL(tc.token, tc.remember)
			if got != tc.want {
				t.Errorf("suspensionMarkerTTL(%v, %v) = %v, want %v", tc.token, tc.remember, got, tc.want)
			}
		})
	}
}

// TestMarkUserSuspended_WritesMarker proves the revocation primitive writes the
// per-user marker the middleware fast-path checks.
func TestMarkUserSuspended_WritesMarker(t *testing.T) {
	cache := newSuspensionCache()
	svc := newAuthSvc(t, &errDB{mockDB: &mockDB{}}, cache)

	if err := svc.MarkUserSuspended(context.Background(), "user-9"); err != nil {
		t.Fatalf("MarkUserSuspended: %v", err)
	}
	if len(cache.setMarkers) != 1 || !strings.Contains(cache.setMarkers[0], "user-9") {
		t.Fatalf("expected one marker Set for user-9, got %v", cache.setMarkers)
	}
	// The marker must be visible to the same cache so the middleware rejects.
	if !svc.isUserSuspendedCached(context.Background(), "user-9") {
		t.Fatal("marker must be readable via isUserSuspendedCached after MarkUserSuspended")
	}
}

// TestMarkUserSuspended_CacheError_ReturnsError exercises the error path: a
// Redis failure during marker write must surface (not be swallowed), so the
// caller (SuspendUser) can log it. Best-effort at the handler layer, but the
// primitive itself must report the failure. Locks the previously-scaffolded
// setErr field into a real test (Rule 5: no dead scaffolding).
func TestMarkUserSuspended_CacheError_ReturnsError(t *testing.T) {
	cache := newSuspensionCache()
	cache.setErr = errors.New("redis connection refused")
	svc := newAuthSvc(t, &errDB{mockDB: &mockDB{}}, cache)

	err := svc.MarkUserSuspended(context.Background(), "user-9")
	if err == nil {
		t.Fatal("MarkUserSuspended must return the cache error, not swallow it")
	}
	if !strings.Contains(err.Error(), "mark user suspended") {
		t.Errorf("expected wrapped 'mark user suspended' error, got %v", err)
	}
}

// TestClearUserSuspended_CacheError_ReturnsError mirrors the above for the
// clear path: a Redis failure during unsuspend must surface so the handler can
// log it (the user is already active in the DB; the marker self-heals on next
// request via the middleware, but the operator should see the warning).
func TestClearUserSuspended_CacheError_ReturnsError(t *testing.T) {
	cache := newSuspensionCache()
	// suspensionCache only injects setErr; for the Delete error path, wrap a
	// failing cache. Reuse a minimal inline type via the same suspensionCache
	// shape by giving Delete an error path.
	cache.delErr = errors.New("redis connection refused")
	svc := newAuthSvc(t, &errDB{mockDB: &mockDB{}}, cache)

	err := svc.ClearUserSuspended(context.Background(), "user-9")
	if err == nil {
		t.Fatal("ClearUserSuspended must return the cache error, not swallow it")
	}
	if !strings.Contains(err.Error(), "clear user suspended") {
		t.Errorf("expected wrapped 'clear user suspended' error, got %v", err)
	}
}

// TestClearUserSuspended_RemovesMarker proves unsuspend clears the marker so the
// user's tokens work again immediately.
func TestClearUserSuspended_RemovesMarker(t *testing.T) {
	cache := newSuspensionCache()
	svc := newAuthSvc(t, &errDB{mockDB: &mockDB{}}, cache)

	if err := svc.MarkUserSuspended(context.Background(), "user-9"); err != nil {
		t.Fatalf("MarkUserSuspended: %v", err)
	}
	if err := svc.ClearUserSuspended(context.Background(), "user-9"); err != nil {
		t.Fatalf("ClearUserSuspended: %v", err)
	}
	if len(cache.delMarkers) != 1 {
		t.Fatalf("expected one marker Delete, got %v", cache.delMarkers)
	}
	if svc.isUserSuspendedCached(context.Background(), "user-9") {
		t.Fatal("marker must be gone after ClearUserSuspended")
	}
}

// TestAuthMiddleware_SuspendedMarker_RejectsImmediately proves the marker
// rejects the request WITHOUT a working DB — the DB would have returned an
// error here, yet the marker lets us report the precise 401 (suspended) rather
// than a generic 503. This is the DB-outage-resilience property of F4.
func TestAuthMiddleware_SuspendedMarker_RejectsImmediately(t *testing.T) {
	cache := newSuspensionCache()
	// DB errors on every GetUser; without the marker the request would 503.
	// With the marker it must 401 (suspended) — a precise denial, not a
	// service-failure label.
	db := &errDB{mockDB: &mockDB{}, getUserErr: errors.New("db down")}
	svc := newAuthSvc(t, db, cache)

	if err := svc.MarkUserSuspended(context.Background(), "user-123"); err != nil {
		t.Fatalf("MarkUserSuspended: %v", err)
	}
	token, err := svc.GenerateToken("user-123")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	router, reached := buildSuspendedRouter(t, svc)

	req := httptest.NewRequest("GET", "/secure", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (suspended via marker) even when DB is down, got %d", w.Code)
	}
	if *reached {
		t.Fatal("handler must NOT be reached for a marker-suspended user")
	}
	if !strings.Contains(w.Body.String(), "suspended") {
		t.Errorf("expected 'account suspended' message, got %s", w.Body.String())
	}
}

// TestAuthMiddleware_StaleMarker_Healed is the regression test for the
// adversarial finding against the original F4 design: if ClearUserSuspended
// failed during an unsuspend (Redis blip), a stale marker would block an active
// user for up to the marker TTL. The fix re-validates via GetUser on the
// active-user path and HEALS the stale marker, so an unsuspended user is let in
// and the marker is cleared.
func TestAuthMiddleware_StaleMarker_Healed(t *testing.T) {
	cache := newSuspensionCache()
	// The DB says ACTIVE (user was unsuspended), but a stale marker lingers.
	db := &errDB{mockDB: &mockDB{users: map[string]*mockUser{
		"user-123": {ID: "user-123", Role: "user", Active: true, Status: types.UserStatusActive},
	}}}
	svc := newAuthSvc(t, db, cache)

	if err := svc.MarkUserSuspended(context.Background(), "user-123"); err != nil {
		t.Fatalf("seed stale marker: %v", err)
	}
	token, err := svc.GenerateToken("user-123")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	router, reached := buildSuspendedRouter(t, svc)

	req := httptest.NewRequest("GET", "/secure", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("an active user with a stale marker must be ALLOWED (healed), got %d: %s", w.Code, w.Body.String())
	}
	if !*reached {
		t.Fatal("active user with a stale marker must reach the handler")
	}
	// The stale marker must have been cleared so subsequent requests are fast.
	if svc.isUserSuspendedCached(context.Background(), "user-123") {
		t.Error("stale marker must be cleared after healing an active user")
	}
}
