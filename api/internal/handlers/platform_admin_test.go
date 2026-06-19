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

	// US-43.18 ListOrgs surface.
	listOrgsCalls []struct {
		Limit  int
		Offset int
		Status *string
	}
	listOrgs     []types.OrgSummary
	listOrgsPage *types.PaginationMetadata
	listOrgsErr  error

	// F7: atomic guarded-suspend surface (replaces the separate last-admin read
	// + SetUserStatus write). suspendGuardedConflict != nil ⇒ refuse with 409.
	suspendGuardedCalls []struct {
		UserID string
		Force  bool
	}
	suspendGuardedConflict *types.LastAdminOrg
	suspendGuardedErr      error
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

// SuspendUserGuardedByLastAdmin records the call and returns the configured
// conflict (F7). The real PgOrgStore performs the status update inside the same
// transaction; the mock intentionally does not, so tests can assert the
// handler's response shape independently of DB plumbing.
func (m *mockPlatformOrgStore) SuspendUserGuardedByLastAdmin(_ context.Context, userID string, force bool) (*types.LastAdminOrg, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.suspendGuardedCalls = append(m.suspendGuardedCalls, struct {
		UserID string
		Force  bool
	}{userID, force})
	cp := m.suspendGuardedConflict
	return cp, m.suspendGuardedErr
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

// mockRevoker records F4 token-revocation calls. markErr lets a test simulate a
// Redis blip (best-effort path must not fail the admin action).
type mockRevoker struct {
	mu       sync.Mutex
	marked   []string
	cleared  []string
	markErr  error
	clearErr error
}

func (m *mockRevoker) MarkUserSuspended(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.marked = append(m.marked, userID)
	return m.markErr
}

func (m *mockRevoker) ClearUserSuspended(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleared = append(m.cleared, userID)
	return m.clearErr
}

func setupPlatformAdminRouter(t *testing.T, orgs *mockPlatformOrgStore, users *mockPlatformUserStore) *gin.Engine {
	return setupPlatformAdminRouterWithRevoker(t, orgs, users, &mockRevoker{})
}

func setupPlatformAdminRouterWithRevoker(t *testing.T, orgs *mockPlatformOrgStore, users *mockPlatformUserStore, revoker *mockRevoker) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewPlatformAdminHandler(orgs, users, &mockOrgAuthService{userID: "admin-1"}, revoker, &stubLogger{})
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
	orgs := &mockPlatformOrgStore{}
	users := &mockPlatformUserStore{}
	revoker := &mockRevoker{}
	r := setupPlatformAdminRouterWithRevoker(t, orgs, users, revoker)

	w := doRequest(r, "POST", "/api/v1/admin/users/user-1/suspend", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// F7: the suspend is now atomic via SuspendUserGuardedByLastAdmin (which in
	// production performs the status UPDATE inside the same tx as the check).
	// SetUserStatus is no longer called by the suspend path.
	if len(orgs.suspendGuardedCalls) != 1 {
		t.Fatalf("expected 1 SuspendUserGuardedByLastAdmin call, got %d", len(orgs.suspendGuardedCalls))
	}
	if orgs.suspendGuardedCalls[0].UserID != "user-1" || orgs.suspendGuardedCalls[0].Force {
		t.Errorf("unexpected guarded-suspend call: %+v", orgs.suspendGuardedCalls[0])
	}
	if len(users.setStatusCalls) != 0 {
		t.Errorf("SetUserStatus must NOT be called separately on suspend (atomic path owns the update), got %d", len(users.setStatusCalls))
	}
	// F4: the user's tokens are revoked immediately.
	if len(revoker.marked) != 1 || revoker.marked[0] != "user-1" {
		t.Errorf("expected MarkUserSuspended(user-1), got %v", revoker.marked)
	}
	if len(revoker.cleared) != 0 {
		t.Errorf("suspend must not clear the revocation marker, got %v", revoker.cleared)
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
// atomic guarded suspend refuses with 409 when the user is the sole active
// admin of an org, and NO token revocation occurs (the user is not suspended).
func TestSuspendUser_LastAdminBlocked(t *testing.T) {
	orgs := &mockPlatformOrgStore{
		suspendGuardedConflict: &types.LastAdminOrg{OrgID: "org-9", OrgName: "Acme"},
	}
	users := &mockPlatformUserStore{}
	revoker := &mockRevoker{}
	r := setupPlatformAdminRouterWithRevoker(t, orgs, users, revoker)

	w := doRequest(r, "POST", "/api/v1/admin/users/user-1/suspend", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if len(orgs.suspendGuardedCalls) != 1 {
		t.Fatalf("expected the guarded-suspend check to run, got %d calls", len(orgs.suspendGuardedCalls))
	}
	if len(revoker.marked) != 0 {
		t.Errorf("must NOT revoke tokens when the suspend is refused, got %v", revoker.marked)
	}
	if !strings.Contains(w.Body.String(), "Acme") {
		t.Errorf("expected error to name the org 'Acme', got: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "last admin") {
		t.Errorf("expected 'last admin' in message, got: %s", w.Body.String())
	}
}

// TestSuspendUser_LastAdminForceOverride verifies the ?force=true escape hatch
// (D19): the guarded suspend proceeds even for the last admin.
func TestSuspendUser_LastAdminForceOverride(t *testing.T) {
	orgs := &mockPlatformOrgStore{}
	users := &mockPlatformUserStore{}
	revoker := &mockRevoker{}
	r := setupPlatformAdminRouterWithRevoker(t, orgs, users, revoker)

	w := doRequest(r, "POST", "/api/v1/admin/users/user-1/suspend?force=true", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with force=true, got %d: %s", w.Code, w.Body.String())
	}
	if len(orgs.suspendGuardedCalls) != 1 || !orgs.suspendGuardedCalls[0].Force {
		t.Errorf("expected guarded-suspend with force=true, got %+v", orgs.suspendGuardedCalls)
	}
	if len(revoker.marked) != 1 {
		t.Errorf("force-suspend must still revoke tokens, got %v", revoker.marked)
	}
	if len(orgs.auditCalls) != 1 {
		t.Fatalf("expected 1 audit call, got %d", len(orgs.auditCalls))
	}
}

func TestSuspendUser_GuardedStoreError_500(t *testing.T) {
	orgs := &mockPlatformOrgStore{suspendGuardedErr: errors.New("db down")}
	users := &mockPlatformUserStore{}
	revoker := &mockRevoker{}
	r := setupPlatformAdminRouterWithRevoker(t, orgs, users, revoker)

	w := doRequest(r, "POST", "/api/v1/admin/users/user-1/suspend", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on guarded-suspend store error, got %d", w.Code)
	}
	if len(revoker.marked) != 0 {
		t.Errorf("must not revoke tokens when the suspend store call fails")
	}
}

func TestUnsuspendUser_Happy(t *testing.T) {
	orgs := &mockPlatformOrgStore{}
	users := &mockPlatformUserStore{}
	revoker := &mockRevoker{}
	r := setupPlatformAdminRouterWithRevoker(t, orgs, users, revoker)

	w := doRequest(r, "POST", "/api/v1/admin/users/user-1/unsuspend", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(users.setStatusCalls) != 1 || users.setStatusCalls[0].Status != types.UserStatusActive {
		t.Errorf("expected SetUserStatus active, got %+v", users.setStatusCalls)
	}
	// F4: unsuspend clears the revocation marker so existing tokens work again.
	if len(revoker.cleared) != 1 || revoker.cleared[0] != "user-1" {
		t.Errorf("expected ClearUserSuspended(user-1), got %v", revoker.cleared)
	}
	if len(orgs.auditCalls) != 1 || orgs.auditCalls[0].Action != "user.unsuspend" {
		t.Errorf("expected user.unsuspend audit, got %+v", orgs.auditCalls)
	}
}

// TestSuspendUser_RevokerBestEffort verifies the F4 marker write is best-effort:
// a Redis blip surfaces a warning but does NOT fail the admin action (the user
// is already suspended in the DB; the per-request GetUser gate still enforces).
func TestSuspendUser_RevokerBestEffort(t *testing.T) {
	orgs := &mockPlatformOrgStore{}
	users := &mockPlatformUserStore{}
	logger := &stubLogger{}
	gin.SetMode(gin.TestMode)
	h := NewPlatformAdminHandler(orgs, users, &mockOrgAuthService{userID: "admin-1"}, &mockRevoker{markErr: errors.New("redis down")}, logger)
	r := gin.New()
	r.POST("/api/v1/admin/users/:id/suspend", h.SuspendUser)

	w := doRequest(r, "POST", "/api/v1/admin/users/user-1/suspend", "")
	if w.Code != http.StatusOK {
		t.Fatalf("suspend must succeed (best-effort revocation) even when Redis is down, got %d: %s", w.Code, w.Body.String())
	}
	if len(logger.msgs) == 0 {
		t.Errorf("expected a warning log when the revocation marker write fails")
	}
}

// TestSuspendUser_NilRevoker verifies the handler tolerates a nil revoker (used
// by minimal test setups): no panic, status flips via the atomic path.
func TestSuspendUser_NilRevoker(t *testing.T) {
	orgs := &mockPlatformOrgStore{}
	users := &mockPlatformUserStore{}
	gin.SetMode(gin.TestMode)
	h := NewPlatformAdminHandler(orgs, users, &mockOrgAuthService{userID: "admin-1"}, nil, &stubLogger{})
	r := gin.New()
	r.POST("/api/v1/admin/users/:id/suspend", h.SuspendUser)

	w := doRequest(r, "POST", "/api/v1/admin/users/user-1/suspend", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with nil revoker, got %d: %s", w.Code, w.Body.String())
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
