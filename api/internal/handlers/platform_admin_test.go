// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// --- mock stores for the platform-admin handler ---

type mockPlatformOrgStore struct {
	mu                sync.Mutex
	updateStatusCalls []struct {
		OrgID  string
		Status *types.OrgStatus
		Sub    *types.OrgSubscriptionStatus
		Plan   *types.OrgPlan
	}
	updateStatusErr error

	auditCalls []struct {
		Domain, ActorID, Action, TargetID string
		OrgID                             *string
	}
	auditErr error

	lastAdminOrgs []types.LastAdminOrg
	lastAdminErr  error

	// US-43.18 ListOrgs surface.
	listOrgsCalls []struct {
		Limit  int
		Offset int
		Status *string
	}
	listOrgs     []types.OrgSummary
	listOrgsPage *types.PaginationMetadata
	listOrgsErr  error
}

func (m *mockPlatformOrgStore) UpdateOrgStatus(_ context.Context, orgID string, status *types.OrgStatus, sub *types.OrgSubscriptionStatus, plan *types.OrgPlan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateStatusCalls = append(m.updateStatusCalls, struct {
		OrgID  string
		Status *types.OrgStatus
		Sub    *types.OrgSubscriptionStatus
		Plan   *types.OrgPlan
	}{orgID, status, sub, plan})
	return m.updateStatusErr
}

func (m *mockPlatformOrgStore) LogAuditEvent(_ context.Context, domain, actorID, action, targetID string, orgID *string, _ map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditCalls = append(m.auditCalls, struct {
		Domain, ActorID, Action, TargetID string
		OrgID                             *string
	}{domain, actorID, action, targetID, orgID})
	return m.auditErr
}

func (m *mockPlatformOrgStore) OrgsWhereUserIsLastActiveAdmin(_ context.Context, _ string) ([]types.LastAdminOrg, error) {
	return m.lastAdminOrgs, m.lastAdminErr
}

type mockPlatformUserStore struct {
	mu             sync.Mutex
	setStatusCalls []struct {
		UserID string
		Status types.UserStatus
	}
	setStatusErr error

	// US-43.18 ListUsers surface.
	listUsersCalls []struct {
		Limit  int
		Offset int
		Status *string
	}
	listUsers     []types.UserListEntry
	listUsersPage *types.PaginationMetadata
	listUsersErr  error
}

func (m *mockPlatformUserStore) SetUserStatus(_ context.Context, userID string, status types.UserStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setStatusCalls = append(m.setStatusCalls, struct {
		UserID string
		Status types.UserStatus
	}{userID, status})
	return m.setStatusErr
}

type stubLogger struct{ msgs []string }

func (s *stubLogger) Warn(msg string, args ...any) { s.msgs = append(s.msgs, msg) }

func setupPlatformAdminRouter(t *testing.T, orgs *mockPlatformOrgStore, users *mockPlatformUserStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewPlatformAdminHandler(orgs, users, &mockOrgAuthService{userID: "admin-1"}, &stubLogger{})
	r := gin.New()
	r.POST("/api/v1/admin/orgs/:id/suspend", h.SuspendOrg)
	r.POST("/api/v1/admin/orgs/:id/unsuspend", h.UnsuspendOrg)
	r.POST("/api/v1/admin/users/:id/suspend", h.SuspendUser)
	r.POST("/api/v1/admin/users/:id/unsuspend", h.UnsuspendUser)
	return r
}

// --- Org suspend / unsuspend ---

func TestSuspendOrg_Happy(t *testing.T) {
	orgs := &mockPlatformOrgStore{}
	users := &mockPlatformUserStore{}
	r := setupPlatformAdminRouter(t, orgs, users)

	w := doRequest(r, "POST", "/api/v1/admin/orgs/org-1/suspend", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if len(orgs.updateStatusCalls) != 1 {
		t.Fatalf("expected 1 UpdateOrgStatus call, got %d", len(orgs.updateStatusCalls))
	}
	call := orgs.updateStatusCalls[0]
	if call.OrgID != "org-1" {
		t.Errorf("expected orgID=org-1, got %q", call.OrgID)
	}
	if call.Status == nil || *call.Status != types.OrgStatusSuspended {
		t.Errorf("expected status=suspended, got %+v", call.Status)
	}
	if call.Sub != nil || call.Plan != nil {
		t.Errorf("sub/plan must be untouched, got sub=%+v plan=%+v", call.Sub, call.Plan)
	}

	if len(orgs.auditCalls) != 1 {
		t.Fatalf("expected 1 audit call, got %d", len(orgs.auditCalls))
	}
	a := orgs.auditCalls[0]
	if a.Domain != "org" || a.Action != "org.suspend" || a.ActorID != "admin-1" || a.TargetID != "org-1" {
		t.Errorf("unexpected audit: %+v", a)
	}
	if a.OrgID == nil || *a.OrgID != "org-1" {
		t.Errorf("expected org-scoped audit, got OrgID=%+v", a.OrgID)
	}
}

func TestSuspendOrg_StoreError_500(t *testing.T) {
	orgs := &mockPlatformOrgStore{updateStatusErr: errors.New("db down")}
	users := &mockPlatformUserStore{}
	r := setupPlatformAdminRouter(t, orgs, users)

	w := doRequest(r, "POST", "/api/v1/admin/orgs/org-1/suspend", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	if len(orgs.auditCalls) != 0 {
		t.Errorf("audit must not be emitted on failure, got %d calls", len(orgs.auditCalls))
	}
}

func TestUnsuspendOrg_Happy(t *testing.T) {
	orgs := &mockPlatformOrgStore{}
	users := &mockPlatformUserStore{}
	r := setupPlatformAdminRouter(t, orgs, users)

	w := doRequest(r, "POST", "/api/v1/admin/orgs/org-1/unsuspend", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(orgs.updateStatusCalls) != 1 {
		t.Fatalf("expected 1 UpdateOrgStatus call, got %d", len(orgs.updateStatusCalls))
	}
	if orgs.updateStatusCalls[0].Status == nil || *orgs.updateStatusCalls[0].Status != types.OrgStatusActive {
		t.Errorf("expected status=active, got %+v", orgs.updateStatusCalls[0].Status)
	}
	if len(orgs.auditCalls) != 1 || orgs.auditCalls[0].Action != "org.unsuspend" {
		t.Errorf("expected org.unsuspend audit, got %+v", orgs.auditCalls)
	}
}

// --- User suspend / unsuspend ---

func TestSuspendUser_Happy(t *testing.T) {
	orgs := &mockPlatformOrgStore{lastAdminOrgs: []types.LastAdminOrg{}}
	users := &mockPlatformUserStore{}
	r := setupPlatformAdminRouter(t, orgs, users)

	w := doRequest(r, "POST", "/api/v1/admin/users/user-1/suspend", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(users.setStatusCalls) != 1 {
		t.Fatalf("expected 1 SetUserStatus call, got %d", len(users.setStatusCalls))
	}
	if users.setStatusCalls[0].UserID != "user-1" || users.setStatusCalls[0].Status != types.UserStatusSuspended {
		t.Errorf("unexpected SetUserStatus: %+v", users.setStatusCalls[0])
	}
	if len(orgs.auditCalls) != 1 {
		t.Fatalf("expected 1 audit call, got %d", len(orgs.auditCalls))
	}
	a := orgs.auditCalls[0]
	if a.Domain != "admin" || a.Action != "user.suspend" || a.TargetID != "user-1" || a.OrgID != nil {
		t.Errorf("unexpected user.suspend audit: %+v", a)
	}
}

// TestSuspendUser_LastAdminBlocked verifies the D19 deadlock-prevention: the
// suspend is refused with 409 when the user is the sole active admin of an
// org, and SetUserStatus is NOT called.
func TestSuspendUser_LastAdminBlocked(t *testing.T) {
	orgs := &mockPlatformOrgStore{
		lastAdminOrgs: []types.LastAdminOrg{{OrgID: "org-9", OrgName: "Acme"}},
	}
	users := &mockPlatformUserStore{}
	r := setupPlatformAdminRouter(t, orgs, users)

	w := doRequest(r, "POST", "/api/v1/admin/users/user-1/suspend", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if len(users.setStatusCalls) != 0 {
		t.Errorf("SetUserStatus must NOT be called when last-admin check fails, got %d calls", len(users.setStatusCalls))
	}
	if !strings.Contains(w.Body.String(), "Acme") {
		t.Errorf("expected error to name the org 'Acme', got: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "last admin") {
		t.Errorf("expected 'last admin' in message, got: %s", w.Body.String())
	}
}

// TestSuspendUser_LastAdminForceOverride verifies the ?force=true escape hatch
// (D19): a platform admin can force-suspend even the last admin in a security
// emergency, leaving the org unmanageable until manually remediated.
func TestSuspendUser_LastAdminForceOverride(t *testing.T) {
	orgs := &mockPlatformOrgStore{
		lastAdminOrgs: []types.LastAdminOrg{{OrgID: "org-9", OrgName: "Acme"}},
	}
	users := &mockPlatformUserStore{}
	r := setupPlatformAdminRouter(t, orgs, users)

	w := doRequest(r, "POST", "/api/v1/admin/users/user-1/suspend?force=true", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with force=true, got %d: %s", w.Code, w.Body.String())
	}
	if len(users.setStatusCalls) != 1 {
		t.Errorf("expected SetUserStatus called under force, got %d", len(users.setStatusCalls))
	}
	if len(orgs.auditCalls) != 1 {
		t.Fatalf("expected 1 audit call, got %d", len(orgs.auditCalls))
	}
}

func TestSuspendUser_LastAdminCheckError_500(t *testing.T) {
	orgs := &mockPlatformOrgStore{lastAdminErr: errors.New("db down")}
	users := &mockPlatformUserStore{}
	r := setupPlatformAdminRouter(t, orgs, users)

	w := doRequest(r, "POST", "/api/v1/admin/users/user-1/suspend", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on last-admin check error, got %d", w.Code)
	}
	if len(users.setStatusCalls) != 0 {
		t.Errorf("SetUserStatus must not run when the precheck fails")
	}
}

func TestSuspendUser_SetStatusError_500(t *testing.T) {
	orgs := &mockPlatformOrgStore{lastAdminOrgs: []types.LastAdminOrg{}}
	users := &mockPlatformUserStore{setStatusErr: errors.New("db down")}
	r := setupPlatformAdminRouter(t, orgs, users)

	w := doRequest(r, "POST", "/api/v1/admin/users/user-1/suspend", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestUnsuspendUser_Happy(t *testing.T) {
	orgs := &mockPlatformOrgStore{}
	users := &mockPlatformUserStore{}
	r := setupPlatformAdminRouter(t, orgs, users)

	w := doRequest(r, "POST", "/api/v1/admin/users/user-1/unsuspend", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(users.setStatusCalls) != 1 || users.setStatusCalls[0].Status != types.UserStatusActive {
		t.Errorf("expected SetUserStatus active, got %+v", users.setStatusCalls)
	}
	if len(orgs.auditCalls) != 1 || orgs.auditCalls[0].Action != "user.unsuspend" {
		t.Errorf("expected user.unsuspend audit, got %+v", orgs.auditCalls)
	}
}

// TestSuspendOrg_ReturnsNewStatus verifies the response body carries the new
// status so the admin UI can update without a follow-up GET.
func TestSuspendOrg_ReturnsNewStatus(t *testing.T) {
	orgs := &mockPlatformOrgStore{}
	users := &mockPlatformUserStore{}
	r := setupPlatformAdminRouter(t, orgs, users)

	w := doRequest(r, "POST", "/api/v1/admin/orgs/org-1/suspend", "")
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Status != string(types.OrgStatusSuspended) {
		t.Errorf("expected status=suspended in body, got %q", body.Status)
	}
}
