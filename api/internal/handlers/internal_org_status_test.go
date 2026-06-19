// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// fakeInternalOrgStatusStore is a minimal GetOrg-only store for the internal
// status endpoint tests.
type fakeInternalOrgStatusStore struct {
	org *types.Organization
	err error
}

func (f *fakeInternalOrgStatusStore) GetOrg(_ context.Context, _ string) (*types.Organization, error) {
	return f.org, f.err
}

func setupInternalStatusRouter(t *testing.T, store *fakeInternalOrgStatusStore, setToken bool) (*gin.Engine, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if setToken {
		t.Setenv("LLMSAFESPACES_INTERNAL_TOKEN", "sekret")
	}
	h := NewInternalOrgStatusHandler(store)
	r := gin.New()
	r.GET("/api/v1/internal/orgs/:orgID/status", h.GetOrgStatus)
	return r, httptest.NewRecorder()
}

func TestInternalOrgStatus_SuspendedOrg(t *testing.T) {
	store := &fakeInternalOrgStatusStore{org: &types.Organization{ID: "o1", Status: types.OrgStatusSuspended}}
	r, _ := setupInternalStatusRouter(t, store, true)

	req := httptest.NewRequest("GET", "/api/v1/internal/orgs/o1/status", nil)
	req.Header.Set("X-Internal-Token", "sekret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"suspended"`) {
		t.Errorf("expected status suspended, got %s", w.Body.String())
	}
}

func TestInternalOrgStatus_ActiveOrg(t *testing.T) {
	store := &fakeInternalOrgStatusStore{org: &types.Organization{ID: "o1", Status: types.OrgStatusActive}}
	r, _ := setupInternalStatusRouter(t, store, true)

	req := httptest.NewRequest("GET", "/api/v1/internal/orgs/o1/status", nil)
	req.Header.Set("X-Internal-Token", "sekret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"active"`) {
		t.Errorf("expected status active, got %s", w.Body.String())
	}
}

// TestInternalOrgStatus_NotFoundFailsOpen verifies that when the org row is
// absent (hard-deleted or never existed) the endpoint returns 'active' so the
// controller does NOT suspend the workspace — the fail-safe direction per D20
// (an unwarranted suspension is more disruptive than leaving a pod running).
func TestInternalOrgStatus_NotFoundFailsOpen(t *testing.T) {
	store := &fakeInternalOrgStatusStore{org: nil}
	r, _ := setupInternalStatusRouter(t, store, true)

	req := httptest.NewRequest("GET", "/api/v1/internal/orgs/missing/status", nil)
	req.Header.Set("X-Internal-Token", "sekret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"active"`) {
		t.Errorf("expected fail-open active for missing org, got %s", w.Body.String())
	}
}

func TestInternalOrgStatus_TokenRequiredWhenSet(t *testing.T) {
	store := &fakeInternalOrgStatusStore{org: &types.Organization{ID: "o1", Status: types.OrgStatusSuspended}}
	r, _ := setupInternalStatusRouter(t, store, true)

	// No header → 401.
	req := httptest.NewRequest("GET", "/api/v1/internal/orgs/o1/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", w.Code)
	}

	// Wrong header → 401.
	req = httptest.NewRequest("GET", "/api/v1/internal/orgs/o1/status", nil)
	req.Header.Set("X-Internal-Token", "wrong")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", w.Code)
	}

	// Correct header → 200.
	req = httptest.NewRequest("GET", "/api/v1/internal/orgs/o1/status", nil)
	req.Header.Set("X-Internal-Token", "sekret")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct token, got %d", w.Code)
	}
}

// TestInternalOrgStatus_TokenUnsetFailsClosed verifies that when
// LLMSAFESPACES_INTERNAL_TOKEN is unset, the endpoint FAILS CLOSED with 403
// (F5). Pre-fix the endpoint was fully unauthenticated in this state, letting
// any routable pod enumerate which orgs are suspended. The chart sets the
// token on both the API and the controller so this is a deployment
// misconfiguration signal, not a legitimate caller being blocked.
func TestInternalOrgStatus_TokenUnsetFailsClosed(t *testing.T) {
	os.Unsetenv("LLMSAFESPACES_INTERNAL_TOKEN")
	store := &fakeInternalOrgStatusStore{org: &types.Organization{ID: "o1", Status: types.OrgStatusSuspended}}
	r, _ := setupInternalStatusRouter(t, store, false)

	req := httptest.NewRequest("GET", "/api/v1/internal/orgs/o1/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (fail-closed) with token unset, got %d: %s", w.Code, w.Body.String())
	}
}
