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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespaces/pkg/email"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

type mockInvitationStore struct {
	mu             sync.Mutex
	invitations    map[string]*types.OrgInvitation
	tokenHashIndex map[string]string
	orgs           map[string]*types.Organization
	members        map[string][]*types.OrgMember
	countLastHour  int
	createErr      error
	acceptErr      error
	userOrgID      string
	userOrgIDErr   error
	userEmail      string
	userEmailErr   error

	// usersByEmail backs GetUserIDByEmail. Keys are lowercased emails.
	// Empty map means "no users registered" — GetUserIDByEmail returns
	// ("", nil) for misses (matches the production PgOrgStore signature
	// where a missing user is a non-error case the caller must inspect).
	usersByEmail        map[string]string
	getUserIDByEmailErr error

	// markVerifiedCalls captures every userID passed to
	// MarkUserEmailVerified so tests can assert intent (not just outcome).
	markVerifiedCalls []string
	markVerifiedErr   error

	// auditEvents captures every LogOrgEvent call. The mock reuses
	// orgs_test.go's mockAuditEvent type since both test files share
	// the package; this keeps assertion shape consistent across the
	// two surfaces (member.verify and invitation.verify_user).
	auditEvents []mockAuditEvent
	auditErr    error
}

func newMockInvitationStore() *mockInvitationStore {
	return &mockInvitationStore{
		invitations:    make(map[string]*types.OrgInvitation),
		tokenHashIndex: make(map[string]string),
		orgs:           make(map[string]*types.Organization),
		members:        make(map[string][]*types.OrgMember),
		usersByEmail:   make(map[string]string),
	}
}

func (m *mockInvitationStore) CreateInvitation(_ context.Context, inv *types.OrgInvitation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return m.createErr
	}
	cp := *inv
	m.invitations[inv.ID] = &cp
	m.tokenHashIndex[inv.TokenHash] = inv.ID
	return nil
}

func (m *mockInvitationStore) ListPendingInvitations(_ context.Context, orgID string) ([]*types.OrgInvitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*types.OrgInvitation
	for _, inv := range m.invitations {
		if inv.OrgID == orgID && inv.AcceptedAt == nil && inv.DeclinedAt == nil {
			cp := *inv
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *mockInvitationStore) GetInvitationByTokenHash(_ context.Context, hash string) (*types.OrgInvitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.tokenHashIndex[hash]
	if !ok {
		return nil, nil
	}
	cp := *m.invitations[id]
	return &cp, nil
}

func (m *mockInvitationStore) GetInvitationByID(_ context.Context, invID string) (*types.OrgInvitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invitations[invID]
	if !ok {
		return nil, nil
	}
	cp := *inv
	return &cp, nil
}

func (m *mockInvitationStore) AcceptInvitationTx(_ context.Context, invID, userID string, role types.OrgRole) (*types.OrgMember, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.acceptErr != nil {
		return nil, false, m.acceptErr
	}
	inv, ok := m.invitations[invID]
	if !ok {
		return nil, false, nil
	}
	if inv.AcceptedAt != nil || inv.DeclinedAt != nil {
		return nil, true, nil
	}
	now := time.Now()
	inv.AcceptedAt = &now
	member := &types.OrgMember{
		OrgID:  inv.OrgID,
		UserID: userID,
		Role:   role,
	}
	m.members[inv.OrgID] = append(m.members[inv.OrgID], member)
	return member, false, nil
}

func (m *mockInvitationStore) DeclineInvitation(_ context.Context, invID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inv, ok := m.invitations[invID]; ok && inv.AcceptedAt == nil && inv.DeclinedAt == nil {
		now := time.Now()
		inv.DeclinedAt = &now
	}
	return nil
}

func (m *mockInvitationStore) DeleteInvitation(_ context.Context, invID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inv, ok := m.invitations[invID]; ok {
		if inv.AcceptedAt == nil && inv.DeclinedAt == nil {
			delete(m.tokenHashIndex, inv.TokenHash)
			delete(m.invitations, invID)
		}
	}
	return nil
}

func (m *mockInvitationStore) CountInvitationsLastHour(_ context.Context, _ string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.countLastHour, nil
}

func (m *mockInvitationStore) GetOrg(_ context.Context, orgID string) (*types.Organization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if org, ok := m.orgs[orgID]; ok {
		cp := *org
		return &cp, nil
	}
	return nil, nil
}

func (m *mockInvitationStore) GetOrgMember(_ context.Context, orgID, userID string) (*types.OrgMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mem := range m.members[orgID] {
		if mem.UserID == userID {
			cp := *mem
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *mockInvitationStore) GetUserOrgID(_ context.Context, userID string) (string, error) {
	if m.userOrgIDErr != nil {
		return "", m.userOrgIDErr
	}
	return m.userOrgID, nil
}

func (m *mockInvitationStore) GetUserEmail(_ context.Context, _ string) (string, error) {
	if m.userEmailErr != nil {
		return "", m.userEmailErr
	}
	return m.userEmail, nil
}

// GetUserIDByEmail mirrors PgOrgStore.GetUserIDByEmail: returns ("", nil)
// when no user is registered with that email (a non-error miss).
// Tests register accounts via store.usersByEmail.
func (m *mockInvitationStore) GetUserIDByEmail(_ context.Context, email string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getUserIDByEmailErr != nil {
		return "", m.getUserIDByEmailErr
	}
	return m.usersByEmail[strings.ToLower(strings.TrimSpace(email))], nil
}

func (m *mockInvitationStore) MarkUserEmailVerified(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.markVerifiedErr != nil {
		return m.markVerifiedErr
	}
	m.markVerifiedCalls = append(m.markVerifiedCalls, userID)
	return nil
}

func (m *mockInvitationStore) LogOrgEvent(_ context.Context, orgID, actorID, action, targetID string, metadata map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.auditErr != nil {
		return m.auditErr
	}
	// Copy metadata to avoid the caller mutating it post-call (defense
	// against test-introduced flakes).
	mcopy := make(map[string]any, len(metadata))
	for k, v := range metadata {
		mcopy[k] = v
	}
	m.auditEvents = append(m.auditEvents, mockAuditEvent{
		OrgID:    orgID,
		ActorID:  actorID,
		Action:   action,
		TargetID: targetID,
		Metadata: mcopy,
	})
	return nil
}

// mockCredBinder records calls to BindAllOrgCredentialsToOrgWorkspaces for
// verifying F7 credential seeding after invitation acceptance.
type mockCredBinder struct {
	mu        sync.Mutex
	bindCalls []string // orgIDs passed
	bindErr   error
}

func (m *mockCredBinder) BindAllOrgCredentialsToOrgWorkspaces(_ context.Context, orgID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bindCalls = append(m.bindCalls, orgID)
	return m.bindErr
}

func (m *mockCredBinder) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.bindCalls)
}

func setupInvitationRouter(t *testing.T, store *mockInvitationStore, mailer email.EmailProvider) *gin.Engine {
	return setupInvitationRouterWithBinder(t, store, mailer, nil)
}

func setupInvitationRouterWithBinder(t *testing.T, store *mockInvitationStore, mailer email.EmailProvider, binder orgCredentialBinder) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewInvitationsHandler(store, mailer, &mockOrgAuthService{userID: "user-1"}, "https://app.test", nil)
	if binder != nil {
		h.SetCredentialBinder(binder)
	}
	r := gin.New()
	r.POST("/api/v1/orgs/:id/invitations", h.Create)
	r.GET("/api/v1/orgs/:id/invitations", h.List)
	r.DELETE("/api/v1/orgs/:id/invitations/:invID", h.Delete)
	r.POST("/api/v1/orgs/:id/invitations/:invID/resend", h.Resend)
	r.GET("/api/v1/invitations/:token", h.GetByToken)
	r.POST("/api/v1/invitations/:token/accept", h.Accept)
	r.POST("/api/v1/invitations/:token/decline", h.Decline)
	return r
}

func TestInvitations_Create_Success(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Name: "Acme", Slug: "acme"}
	store.members["org-1"] = []*types.OrgMember{{OrgID: "org-1", UserID: "user-1", Username: "admin", Role: types.OrgRoleAdmin}}
	router := setupInvitationRouter(t, store, &email.NoopProvider{})

	body := `{"emails":["alice@test.com","bob@test.com"],"role":"member"}`
	w := doRequest(router, "POST", "/api/v1/orgs/org-1/invitations", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created []*types.OrgInvitation
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 invitations, got %d", len(created))
	}
}

func TestInvitations_Create_InvalidEmail_Rejected(t *testing.T) {
	store := newMockInvitationStore()
	router := setupInvitationRouter(t, store, &email.NoopProvider{})

	w := doRequest(router, "POST", "/api/v1/orgs/org-1/invitations", `{"emails":["not-an-email"],"role":"member"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid email, got %d", w.Code)
	}
}

func TestInvitations_Create_RateLimited(t *testing.T) {
	store := newMockInvitationStore()
	store.countLastHour = 49
	router := setupInvitationRouter(t, store, &email.NoopProvider{})

	w := doRequest(router, "POST", "/api/v1/orgs/org-1/invitations", `{"emails":["alice@test.com","bob@test.com"],"role":"member"}`)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 rate limited, got %d", w.Code)
	}
}

func TestInvitations_GetByToken_Public_Success(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Name: "Acme", Slug: "acme"}
	store.members["org-1"] = []*types.OrgMember{{OrgID: "org-1", UserID: "inviter-1", Username: "Alice", Role: types.OrgRoleAdmin}}

	token := "valid-token-123"
	hash := hashToken(token)
	store.invitations["inv-1"] = &types.OrgInvitation{
		ID: "inv-1", OrgID: "org-1", Email: "new@test.com", Role: types.OrgRoleMember,
		InvitedBy: "inviter-1", TokenHash: hash, ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	store.tokenHashIndex[hash] = "inv-1"

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "GET", "/api/v1/invitations/"+token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var detail types.InvitationDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if detail.OrgName != "Acme" {
		t.Errorf("expected org name Acme, got %q", detail.OrgName)
	}
	if detail.InviterName != "Alice" {
		t.Errorf("expected inviter Alice, got %q", detail.InviterName)
	}
}

func TestInvitations_GetByToken_NotFound(t *testing.T) {
	store := newMockInvitationStore()
	router := setupInvitationRouter(t, store, nil)

	w := doRequest(router, "GET", "/api/v1/invitations/nonexistent", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// F7: after accepting an invitation, the handler must bind all org credentials
// to the org's workspaces (including the newly-migrated personal workspaces).
// Best-effort: even if the binder errors, the accept still succeeds.
func TestInvitations_Accept_BindsOrgCredentials(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Name: "Acme", Slug: "acme"}

	token := "bind-token"
	hash := hashToken(token)
	store.invitations["inv-bind"] = &types.OrgInvitation{
		ID: "inv-bind", OrgID: "org-1", Email: "new@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: hash, ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	store.tokenHashIndex[hash] = "inv-bind"
	store.userEmail = "new@test.com"

	binder := &mockCredBinder{}
	router := setupInvitationRouterWithBinder(t, store, nil, binder)

	w := doRequest(router, "POST", "/api/v1/invitations/"+token+"/accept", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// Credential binding is fire-and-forget — poll for the goroutine to complete.
	require.Eventually(t, func() bool {
		return binder.callCount() == 1
	}, time.Second, 10*time.Millisecond, "expected BindAllOrgCredentialsToOrgWorkspaces called once")
	if binder.bindCalls[0] != "org-1" {
		t.Errorf("expected org-1, got %q", binder.bindCalls[0])
	}
}

func TestInvitations_Accept_BindError_StillSucceeds(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Name: "Acme", Slug: "acme"}

	token := "bind-err-token"
	hash := hashToken(token)
	store.invitations["inv-bind-err"] = &types.OrgInvitation{
		ID: "inv-bind-err", OrgID: "org-1", Email: "new@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: hash, ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	store.tokenHashIndex[hash] = "inv-bind-err"
	store.userEmail = "new@test.com"

	binder := &mockCredBinder{bindErr: errors.New("binding failed")}
	router := setupInvitationRouterWithBinder(t, store, nil, binder)

	w := doRequest(router, "POST", "/api/v1/invitations/"+token+"/accept", "")
	if w.Code != http.StatusOK {
		t.Errorf("accept must succeed even if credential binding fails, got %d: %s", w.Code, w.Body.String())
	}
	require.Eventually(t, func() bool {
		return binder.callCount() == 1
	}, time.Second, 10*time.Millisecond, "binder should still be called once")
}

func TestInvitations_Accept_Success_Member(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Name: "Acme", Slug: "acme"}

	token := "accept-token"
	hash := hashToken(token)
	store.invitations["inv-1"] = &types.OrgInvitation{
		ID: "inv-1", OrgID: "org-1", Email: "new@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: hash, ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	store.tokenHashIndex[hash] = "inv-1"
	store.userEmail = "new@test.com"

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "POST", "/api/v1/invitations/"+token+"/accept", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	member, ok := resp["membership"].(map[string]any)
	if !ok {
		t.Fatal("expected membership in response")
	}
	if member["role"] != "member" {
		t.Errorf("expected role=member, got %v", member["role"])
	}
}

func TestInvitations_Accept_Success_Admin(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Name: "Acme", Slug: "acme"}

	token := "admin-token"
	hash := hashToken(token)
	store.invitations["inv-2"] = &types.OrgInvitation{
		ID: "inv-2", OrgID: "org-1", Email: "admin@test.com", Role: types.OrgRoleAdmin,
		InvitedBy: "user-1", TokenHash: hash, ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	store.tokenHashIndex[hash] = "inv-2"
	store.userEmail = "admin@test.com"

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "POST", "/api/v1/invitations/"+token+"/accept", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	member, ok := resp["membership"].(map[string]any)
	if !ok {
		t.Fatal("expected membership in response")
	}
	if member["role"] != "admin" {
		t.Errorf("expected role=admin, got %v", member["role"])
	}
}

func TestInvitations_Accept_Expired_Gone(t *testing.T) {
	store := newMockInvitationStore()
	token := "expired-token"
	hash := hashToken(token)
	store.invitations["inv-3"] = &types.OrgInvitation{
		ID: "inv-3", OrgID: "org-1", Email: "e@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: hash, ExpiresAt: time.Now().Add(-1 * time.Hour),
	}
	store.tokenHashIndex[hash] = "inv-3"

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "POST", "/api/v1/invitations/"+token+"/accept", "")
	if w.Code != http.StatusGone {
		t.Errorf("expected 410 Gone for expired invitation, got %d", w.Code)
	}
}

func TestInvitations_Accept_AlreadyAccepted_Conflict(t *testing.T) {
	store := newMockInvitationStore()
	token := "taken-token"
	hash := hashToken(token)
	now := time.Now()
	store.invitations["inv-4"] = &types.OrgInvitation{
		ID: "inv-4", OrgID: "org-1", Email: "e@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: hash, ExpiresAt: time.Now().Add(24 * time.Hour),
		AcceptedAt: &now,
	}
	store.tokenHashIndex[hash] = "inv-4"

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "POST", "/api/v1/invitations/"+token+"/accept", "")
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for already accepted, got %d", w.Code)
	}
}

func TestInvitations_Accept_AlreadyMember_Conflict(t *testing.T) {
	store := newMockInvitationStore()
	store.members["org-1"] = []*types.OrgMember{{OrgID: "org-1", UserID: "user-1"}}

	token := "member-token"
	hash := hashToken(token)
	store.invitations["inv-5"] = &types.OrgInvitation{
		ID: "inv-5", OrgID: "org-1", Email: "e@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: hash, ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	store.tokenHashIndex[hash] = "inv-5"

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "POST", "/api/v1/invitations/"+token+"/accept", "")
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for already member, got %d: %s", w.Code, w.Body.String())
	}
}

// Cross-org enforcement (S3 in 0034, D8 in 0031): a user already in org A
// accepting an invitation to org B must get a clear 409, not a raw DB
// constraint-violation 500. The Accept handler must check the user's existing
// org membership before attempting the insert.
func TestInvitations_Accept_AlreadyInAnotherOrg_Conflict(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-2"] = &types.Organization{ID: "org-2", Name: "Beta", Slug: "beta"}
	store.userOrgID = "org-2" // user-1 is already a member of a DIFFERENT org

	token := "cross-org-token"
	hash := hashToken(token)
	store.invitations["inv-6"] = &types.OrgInvitation{
		ID: "inv-6", OrgID: "org-1", Email: "e@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: hash, ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	store.tokenHashIndex[hash] = "inv-6"

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "POST", "/api/v1/invitations/"+token+"/accept", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for cross-org membership, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "another organization") {
		t.Errorf("expected a clear 'another organization' message, got: %s", w.Body.String())
	}
	// The invitation must NOT be marked accepted.
	store.mu.Lock()
	inv := store.invitations["inv-6"]
	store.mu.Unlock()
	if inv.AcceptedAt != nil {
		t.Errorf("invitation must not be marked accepted when cross-org check fails")
	}
}

func TestInvitations_Accept_GetUserOrgIDError_500(t *testing.T) {
	store := newMockInvitationStore()
	store.userOrgIDErr = errors.New("db down")

	token := "lookup-err-token"
	hash := hashToken(token)
	store.invitations["inv-7"] = &types.OrgInvitation{
		ID: "inv-7", OrgID: "org-1", Email: "e@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: hash, ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	store.tokenHashIndex[hash] = "inv-7"

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "POST", "/api/v1/invitations/"+token+"/accept", "")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on lookup error, got %d: %s", w.Code, w.Body.String())
	}
}

func TestInvitations_Accept_EmailMismatch_Returns403(t *testing.T) {
	store := newMockInvitationStore()

	token := "email-mismatch-token"
	hash := hashToken(token)
	store.invitations["inv-email"] = &types.OrgInvitation{
		ID: "inv-email", OrgID: "org-1", Email: "invited@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: hash, ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	store.tokenHashIndex[hash] = "inv-email"
	store.userEmail = "attacker@test.com" // does NOT match invited@test.com

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "POST", "/api/v1/invitations/"+token+"/accept", "")
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for email mismatch, got %d: %s", w.Code, w.Body.String())
	}
}

func TestInvitations_Accept_GetUserEmailError_500(t *testing.T) {
	store := newMockInvitationStore()
	store.userEmailErr = errors.New("db down")

	token := "email-err-token"
	hash := hashToken(token)
	store.invitations["inv-email-err"] = &types.OrgInvitation{
		ID: "inv-email-err", OrgID: "org-1", Email: "e@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: hash, ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	store.tokenHashIndex[hash] = "inv-email-err"

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "POST", "/api/v1/invitations/"+token+"/accept", "")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on GetUserEmail error, got %d: %s", w.Code, w.Body.String())
	}
}

func TestInvitations_Resend_GeneratesNewToken(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Name: "Acme", Slug: "acme"}
	store.members["org-1"] = []*types.OrgMember{{OrgID: "org-1", UserID: "user-1", Username: "admin"}}

	oldHash := hashToken("old-token")
	store.invitations["inv-old"] = &types.OrgInvitation{
		ID: "inv-old", OrgID: "org-1", Email: "resend@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: oldHash, ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	store.tokenHashIndex[oldHash] = "inv-old"

	router := setupInvitationRouter(t, store, &email.NoopProvider{})
	w := doRequest(router, "POST", "/api/v1/orgs/org-1/invitations/inv-old/resend", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var newInv types.OrgInvitation
	if err := json.Unmarshal(w.Body.Bytes(), &newInv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if newInv.ID == "inv-old" {
		t.Error("resend should create a new invitation with a new ID")
	}
	if newInv.TokenHash == oldHash {
		t.Error("resend should generate a new token hash")
	}
	if _, exists := store.tokenHashIndex[oldHash]; exists {
		t.Error("old token hash should be invalidated after resend")
	}
}

func TestInvitations_Resend_BouncedEmail_Rejected(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Name: "Acme", Slug: "acme"}
	store.invitations["inv-bounced"] = &types.OrgInvitation{
		ID: "inv-bounced", OrgID: "org-1", Email: "bad@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: "hash", ExpiresAt: time.Now().Add(24 * time.Hour),
		BounceType: "permanent",
	}

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "POST", "/api/v1/orgs/org-1/invitations/inv-bounced/resend", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bounced email resend, got %d", w.Code)
	}
}

func TestInvitations_Decline_Success(t *testing.T) {
	store := newMockInvitationStore()
	token := "decline-token"
	hash := hashToken(token)
	store.invitations["inv-6"] = &types.OrgInvitation{
		ID: "inv-6", OrgID: "org-1", Email: "e@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: hash, ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	store.tokenHashIndex[hash] = "inv-6"

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "POST", "/api/v1/invitations/"+token+"/decline", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestInvitations_Delete_Revokes(t *testing.T) {
	store := newMockInvitationStore()
	store.invitations["inv-del"] = &types.OrgInvitation{
		ID: "inv-del", OrgID: "org-1", Email: "e@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: "h", ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	store.tokenHashIndex["h"] = "inv-del"

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "DELETE", "/api/v1/orgs/org-1/invitations/inv-del", "")
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
	if _, exists := store.invitations["inv-del"]; exists {
		t.Error("invitation should be deleted")
	}
}

func TestInvitations_TokenSecurity_HashNotReversible(t *testing.T) {
	token := "my-secret-token"
	hash := hashToken(token)
	if token == hash {
		t.Fatal("token and hash should be different")
	}
	if !strings.Contains(hash, "=") {
		t.Error("hash should be base64-encoded SHA-256")
	}
	if len(hash) < 40 {
		t.Error("hash should be at least 40 chars (SHA-256 base64)")
	}
}

func TestInvitations_Delete_CrossOrg_NotFound(t *testing.T) {
	store := newMockInvitationStore()
	store.invitations["inv-x"] = &types.OrgInvitation{
		ID: "inv-x", OrgID: "other-org", Email: "e@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: "h", ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "DELETE", "/api/v1/orgs/my-org/invitations/inv-x", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("cross-org delete must return 404, got %d", w.Code)
	}
}

func TestInvitations_Resend_CrossOrg_NotFound(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["my-org"] = &types.Organization{ID: "my-org", Name: "My", Slug: "my"}
	store.invitations["inv-x"] = &types.OrgInvitation{
		ID: "inv-x", OrgID: "other-org", Email: "e@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: "h", ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "POST", "/api/v1/orgs/my-org/invitations/inv-x/resend", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("cross-org resend must return 404, got %d", w.Code)
	}
}

func TestInvitations_Resend_AlreadyAccepted_Conflict(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Name: "Acme", Slug: "acme"}
	now := time.Now()
	store.invitations["inv-acc"] = &types.OrgInvitation{
		ID: "inv-acc", OrgID: "org-1", Email: "e@test.com", Role: types.OrgRoleMember,
		InvitedBy: "user-1", TokenHash: "h", ExpiresAt: time.Now().Add(24 * time.Hour),
		AcceptedAt: &now,
	}

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "POST", "/api/v1/orgs/org-1/invitations/inv-acc/resend", "")
	if w.Code != http.StatusConflict {
		t.Errorf("resend of accepted invitation must return 409, got %d", w.Code)
	}
}

func TestInvitations_List_ReturnsPending(t *testing.T) {
	store := newMockInvitationStore()
	store.invitations["inv-1"] = &types.OrgInvitation{ID: "inv-1", OrgID: "org-1", Email: "a@test.com", Role: types.OrgRoleMember}
	store.invitations["inv-2"] = &types.OrgInvitation{ID: "inv-2", OrgID: "org-1", Email: "b@test.com", Role: types.OrgRoleMember}
	store.invitations["inv-3"] = &types.OrgInvitation{ID: "inv-3", OrgID: "org-2", Email: "c@test.com", Role: types.OrgRoleMember}

	router := setupInvitationRouter(t, store, nil)
	w := doRequest(router, "GET", "/api/v1/orgs/org-1/invitations", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var list []*types.OrgInvitation
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 invitations for org-1, got %d", len(list))
	}
}

// TestInvitations_GetByToken_DoesNotConsume is the scanner-safety invariant
// regression test (Epic 49 US-49.9). Email-link scanners (Proofpoint,
// Mimecast, Office ATP) follow links in emails to scan them. If GET
// /invitations/:token consumed the invitation, the scanner would invalidate
// it and the real user's later Accept would 404/409.
//
// This test asserts:
//  1. Two consecutive GETs return 200 with the same details (no consumption).
//  2. The invitation's AcceptedAt/DeclinedAt remain nil after GETs.
//
// A future "simplification" that makes GetByToken consume would turn this red.
func TestInvitations_GetByToken_DoesNotConsume(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1", Name: "Acme", Slug: "acme"}
	store.members["org-1"] = []*types.OrgMember{{OrgID: "org-1", UserID: "inviter-1", Username: "Alice", Role: types.OrgRoleAdmin}}

	token := "scanner-safe-token"
	hash := hashToken(token)
	store.invitations["inv-scanner"] = &types.OrgInvitation{
		ID: "inv-scanner", OrgID: "org-1", Email: "new@test.com", Role: types.OrgRoleMember,
		InvitedBy: "inviter-1", TokenHash: hash, ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	store.tokenHashIndex[hash] = "inv-scanner"

	router := setupInvitationRouter(t, store, nil)

	// First GET — a scanner following the email link.
	w1 := doRequest(router, "GET", "/api/v1/invitations/"+token, "")
	if w1.Code != http.StatusOK {
		t.Fatalf("first GET: expected 200, got %d: %s", w1.Code, w1.Body.String())
	}

	// Second GET — the real user clicks the same link. Must still work.
	w2 := doRequest(router, "GET", "/api/v1/invitations/"+token, "")
	if w2.Code != http.StatusOK {
		t.Fatalf("second GET after scanner: expected 200 (GET must not consume), got %d: %s", w2.Code, w2.Body.String())
	}

	// Both responses must carry identical details (no mutation between calls).
	if w1.Body.String() != w2.Body.String() {
		t.Errorf("GET responses differ — GetByToken must be read-only:\nfirst:  %s\nsecond: %s", w1.Body.String(), w2.Body.String())
	}

	// The invitation row must be untouched (no AcceptedAt, no DeclinedAt).
	inv := store.invitations["inv-scanner"]
	if inv.AcceptedAt != nil {
		t.Errorf("GET must not set AcceptedAt; got %v", *inv.AcceptedAt)
	}
	if inv.DeclinedAt != nil {
		t.Errorf("GET must not set DeclinedAt; got %v", *inv.DeclinedAt)
	}
}

// ---------------------------------------------------------------------------
// VerifyUserForInvitation: force-verify the user account associated with a
// pending invitation (epic-43 follow-up — the original PR #343 only handled
// already-accepted members; this surface handles invitees whose email is
// pending verification on their existing account).
// ---------------------------------------------------------------------------

// helper: registers an existing user account in the mock so
// GetUserIDByEmail returns its userID.
func (m *mockInvitationStore) registerUser(email, userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usersByEmail[strings.ToLower(strings.TrimSpace(email))] = userID
}

// setupVerifyInviteRouter wires the route under test. The handler is
// registered on a path the router uses in production via orgAdminGroup.
func setupVerifyInviteRouter(t *testing.T, store *mockInvitationStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewInvitationsHandler(store, nil, &mockOrgAuthService{userID: "admin-1"}, "https://app.test", nil)
	r := gin.New()
	r.POST("/api/v1/orgs/:id/invitations/:invID/verify-user", h.VerifyUserForInvitation)
	return r
}

func seedPendingInvitation(store *mockInvitationStore, orgID, invID, email string) {
	store.invitations[invID] = &types.OrgInvitation{
		ID:        invID,
		OrgID:     orgID,
		Email:     email,
		Role:      types.OrgRoleMember,
		ExpiresAt: time.Now().Add(time.Hour),
	}
}

// TestInvitations_VerifyUser_Success_ExistingUser pins the headline
// scenario: invitation pending, the invitee already has a users row,
// admin clicks Verify. Result: MarkUserEmailVerified called with the
// resolved userID, audit event recorded, 200 OK.
func TestInvitations_VerifyUser_Success_ExistingUser(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.registerUser("invitee@example.com", "user-42")
	seedPendingInvitation(store, "org-1", "inv-1", "invitee@example.com")
	r := setupVerifyInviteRouter(t, store)

	w := doRequest(r, "POST", "/api/v1/orgs/org-1/invitations/inv-1/verify-user", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(store.markVerifiedCalls) != 1 || store.markVerifiedCalls[0] != "user-42" {
		t.Errorf("MarkUserEmailVerified must be called once with the resolved userID; got %v", store.markVerifiedCalls)
	}
	if len(store.auditEvents) != 1 {
		t.Fatalf("expected one audit event, got %d", len(store.auditEvents))
	}
	ev := store.auditEvents[0]
	if ev.OrgID != "org-1" || ev.ActorID != "admin-1" {
		t.Errorf("audit org/actor mismatch: %+v", ev)
	}
	if ev.Action != "invitation.verify_user" {
		t.Errorf("audit action must be 'invitation.verify_user' to distinguish from member.verify; got %q", ev.Action)
	}
	if ev.TargetID != "user-42" {
		t.Errorf("audit target must be the resolved user, not the invitation ID; got %q", ev.TargetID)
	}
	if ev.Metadata["email"] != "invitee@example.com" {
		t.Errorf("audit metadata.email = %v, want invitee@example.com", ev.Metadata["email"])
	}
	if ev.Metadata["invitationID"] != "inv-1" {
		t.Errorf("audit metadata must include invitationID for traceability; got %v", ev.Metadata["invitationID"])
	}
}

// TestInvitations_VerifyUser_NoUserAccount returns 422 (not 404, not
// 200) so the frontend can show a clear "the invitee must sign up
// first" message rather than treating it as success.
func TestInvitations_VerifyUser_NoUserAccount(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	// No registerUser call — the email has no users row.
	seedPendingInvitation(store, "org-1", "inv-1", "ghost@example.com")
	r := setupVerifyInviteRouter(t, store)

	w := doRequest(r, "POST", "/api/v1/orgs/org-1/invitations/inv-1/verify-user", "")

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 when invitee has no users row; got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "no_account_for_email" {
		t.Errorf("error code must be machine-parseable 'no_account_for_email' for the frontend's switch on it; got %q", body["error"])
	}
	if len(store.markVerifiedCalls) != 0 {
		t.Errorf("MarkUserEmailVerified must NOT be called when no user exists; got %v", store.markVerifiedCalls)
	}
	if len(store.auditEvents) != 0 {
		t.Errorf("no audit event for a no-op; got %d events", len(store.auditEvents))
	}
}

// TestInvitations_VerifyUser_EmailNormalization the invitee's
// invitation row may have any casing; the lookup must be case-folded.
func TestInvitations_VerifyUser_EmailNormalization(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.registerUser("invitee@example.com", "user-42")
	// Invitation stored with non-canonical casing — production
	// invitations.go does not normalize on Create.
	seedPendingInvitation(store, "org-1", "inv-1", "INVITEE@Example.COM")
	r := setupVerifyInviteRouter(t, store)

	w := doRequest(r, "POST", "/api/v1/orgs/org-1/invitations/inv-1/verify-user", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with case-folded email lookup; got %d: %s", w.Code, w.Body.String())
	}
	if len(store.markVerifiedCalls) != 1 || store.markVerifiedCalls[0] != "user-42" {
		t.Errorf("expected MarkUserEmailVerified('user-42'), got %v", store.markVerifiedCalls)
	}
}

// TestInvitations_VerifyUser_Idempotent the SQL UPDATE is naturally
// idempotent; verifying an already-verified user is harmless. We still
// emit the audit event because the admin's intent matters even if
// the row is unchanged.
func TestInvitations_VerifyUser_Idempotent(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.registerUser("invitee@example.com", "user-42")
	seedPendingInvitation(store, "org-1", "inv-1", "invitee@example.com")
	r := setupVerifyInviteRouter(t, store)

	// Two consecutive calls should both succeed.
	w1 := doRequest(r, "POST", "/api/v1/orgs/org-1/invitations/inv-1/verify-user", "")
	w2 := doRequest(r, "POST", "/api/v1/orgs/org-1/invitations/inv-1/verify-user", "")

	if w1.Code != http.StatusOK || w2.Code != http.StatusOK {
		t.Fatalf("idempotent call must return 200 both times; got %d / %d", w1.Code, w2.Code)
	}
	if len(store.markVerifiedCalls) != 2 {
		t.Errorf("each call must reach MarkUserEmailVerified (idempotency is at DB level); got %d", len(store.markVerifiedCalls))
	}
	if len(store.auditEvents) != 2 {
		t.Errorf("each call must emit an audit event for traceability; got %d", len(store.auditEvents))
	}
}

// TestInvitations_VerifyUser_InvitationNotFound 404 with no DB writes.
func TestInvitations_VerifyUser_InvitationNotFound(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	r := setupVerifyInviteRouter(t, store)

	w := doRequest(r, "POST", "/api/v1/orgs/org-1/invitations/missing/verify-user", "")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if len(store.markVerifiedCalls) != 0 {
		t.Error("must not write when the invitation does not exist")
	}
	if len(store.auditEvents) != 0 {
		t.Error("must not audit when the invitation does not exist")
	}
}

// TestInvitations_VerifyUser_CrossOrgIsolation returns 404 (NOT 403,
// not 422) when the invitation exists but belongs to a different org.
// This avoids leaking invitation existence across orgs — an admin of
// org A asking about an invitation belonging to org B receives the
// same response as if the invitation didn't exist.
func TestInvitations_VerifyUser_CrossOrgIsolation(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.orgs["org-2"] = &types.Organization{ID: "org-2"}
	store.registerUser("invitee@example.com", "user-42")
	seedPendingInvitation(store, "org-2", "inv-1", "invitee@example.com")
	r := setupVerifyInviteRouter(t, store)

	// Admin of org-1 hits the route with their own org-1 in the path
	// but inv-1 belongs to org-2.
	w := doRequest(r, "POST", "/api/v1/orgs/org-1/invitations/inv-1/verify-user", "")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 to avoid leaking cross-org invitation existence; got %d", w.Code)
	}
	if len(store.markVerifiedCalls) != 0 {
		t.Error("must not act on a cross-org invitation")
	}
	if len(store.auditEvents) != 0 {
		t.Error("must not audit a cross-org invitation lookup")
	}
}

// TestInvitations_VerifyUser_AlreadyAccepted 409: the invitation is
// no longer pending; the regular member.verify surface should be used.
func TestInvitations_VerifyUser_AlreadyAccepted(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.registerUser("invitee@example.com", "user-42")
	now := time.Now()
	store.invitations["inv-1"] = &types.OrgInvitation{
		ID:         "inv-1",
		OrgID:      "org-1",
		Email:      "invitee@example.com",
		Role:       types.OrgRoleMember,
		ExpiresAt:  time.Now().Add(time.Hour),
		AcceptedAt: &now,
	}
	r := setupVerifyInviteRouter(t, store)

	w := doRequest(r, "POST", "/api/v1/orgs/org-1/invitations/inv-1/verify-user", "")

	if w.Code != http.StatusConflict {
		t.Fatalf("already-accepted invitation must return 409 (use member.verify instead); got %d", w.Code)
	}
	if len(store.markVerifiedCalls) != 0 {
		t.Error("must not act on accepted invitations")
	}
}

// TestInvitations_VerifyUser_AlreadyDeclined 409 — same rationale as
// already-accepted. The admin should re-invite if they want the user.
func TestInvitations_VerifyUser_AlreadyDeclined(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.registerUser("invitee@example.com", "user-42")
	now := time.Now()
	store.invitations["inv-1"] = &types.OrgInvitation{
		ID:         "inv-1",
		OrgID:      "org-1",
		Email:      "invitee@example.com",
		Role:       types.OrgRoleMember,
		ExpiresAt:  time.Now().Add(time.Hour),
		DeclinedAt: &now,
	}
	r := setupVerifyInviteRouter(t, store)

	w := doRequest(r, "POST", "/api/v1/orgs/org-1/invitations/inv-1/verify-user", "")

	if w.Code != http.StatusConflict {
		t.Fatalf("declined invitation must return 409; got %d", w.Code)
	}
}

// TestInvitations_VerifyUser_Expired 410 to match Accept's behavior.
func TestInvitations_VerifyUser_Expired(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.registerUser("invitee@example.com", "user-42")
	store.invitations["inv-1"] = &types.OrgInvitation{
		ID:        "inv-1",
		OrgID:     "org-1",
		Email:     "invitee@example.com",
		Role:      types.OrgRoleMember,
		ExpiresAt: time.Now().Add(-time.Hour), // already expired
	}
	r := setupVerifyInviteRouter(t, store)

	w := doRequest(r, "POST", "/api/v1/orgs/org-1/invitations/inv-1/verify-user", "")

	if w.Code != http.StatusGone {
		t.Fatalf("expired invitation must return 410 (matches Accept); got %d", w.Code)
	}
	if len(store.markVerifiedCalls) != 0 {
		t.Error("must not act on expired invitations")
	}
}

// TestInvitations_VerifyUser_GetUserIDError 500 on DB error during
// the email lookup — distinguishable from "user does not exist" (422).
func TestInvitations_VerifyUser_GetUserIDError(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.getUserIDByEmailErr = errors.New("DB unreachable")
	seedPendingInvitation(store, "org-1", "inv-1", "invitee@example.com")
	r := setupVerifyInviteRouter(t, store)

	w := doRequest(r, "POST", "/api/v1/orgs/org-1/invitations/inv-1/verify-user", "")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("DB error during user lookup must surface as 500; got %d", w.Code)
	}
	if len(store.markVerifiedCalls) != 0 {
		t.Error("must not write when the lookup itself failed")
	}
}

// TestInvitations_VerifyUser_MarkVerifiedError 500 on DB error during
// the actual UPDATE; no audit event because the verification did not
// succeed. (The PR #343 audit-failure-non-fatal pattern is for the
// audit emission path; this test exercises the verification-failed path.)
func TestInvitations_VerifyUser_MarkVerifiedError(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.registerUser("invitee@example.com", "user-42")
	store.markVerifiedErr = errors.New("update failed")
	seedPendingInvitation(store, "org-1", "inv-1", "invitee@example.com")
	r := setupVerifyInviteRouter(t, store)

	w := doRequest(r, "POST", "/api/v1/orgs/org-1/invitations/inv-1/verify-user", "")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("UPDATE failure must surface as 500; got %d", w.Code)
	}
	if len(store.auditEvents) != 0 {
		t.Error("must not audit when the verification itself failed")
	}
}

// TestInvitations_VerifyUser_AuditFailureNonFatal proves the
// verification is not undone when only the audit emission fails. The
// admin's intent was already persisted on the user row; rolling back
// would leave the user unverified with no recourse and no record.
// (Mirrors TestOrgsHandler_VerifyMember_AuditFailureNonFatal.)
func TestInvitations_VerifyUser_AuditFailureNonFatal(t *testing.T) {
	store := newMockInvitationStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.registerUser("invitee@example.com", "user-42")
	store.auditErr = errors.New("audit table outage")
	seedPendingInvitation(store, "org-1", "inv-1", "invitee@example.com")

	cap := &invLogCapture{}
	gin.SetMode(gin.TestMode)
	h := NewInvitationsHandler(store, nil, &mockOrgAuthService{userID: "admin-1"}, "https://app.test", cap)
	r := gin.New()
	r.POST("/api/v1/orgs/:id/invitations/:invID/verify-user", h.VerifyUserForInvitation)

	w := doRequest(r, "POST", "/api/v1/orgs/org-1/invitations/inv-1/verify-user", "")

	if w.Code != http.StatusOK {
		t.Fatalf("audit failure must NOT undo a successful verification; got %d", w.Code)
	}
	if len(store.markVerifiedCalls) != 1 {
		t.Errorf("verification must have been performed; got %d MarkUserEmailVerified calls", len(store.markVerifiedCalls))
	}
	if cap.warnCount == 0 {
		t.Errorf("audit emission failure must surface as a Warn log entry (Rule 3 — no swallowed errors)")
	}
}

// invLogCapture satisfies invitationLogger for tests that need to
// observe that a Warn was emitted. The existing warnCaptureLogger in
// orgs_test.go satisfies a smaller interface (Warn-only) which doesn't
// include Error — invitationLogger requires both.
type invLogCapture struct {
	warnCount  int
	errorCount int
}

func (c *invLogCapture) Warn(_ string, _ ...any)           { c.warnCount++ }
func (c *invLogCapture) Error(_ string, _ error, _ ...any) { c.errorCount++ }
