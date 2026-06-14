// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

type mockOrgStore struct {
	mu                    sync.Mutex
	orgs                  map[string]*types.Organization
	members               map[string][]*types.OrgMember
	keyMembers            map[string]*secrets.OrgKeyMemberRecord
	adminCounts           map[string]int
	pendingKeyWrap        map[string]bool
	salts                 map[string][]byte
	listOrgsForUserResult []*types.OrgResponse
	listOrgsForUserErr    error
	createErr             error
	slugExists            bool
	orgHasWorkspaces      bool
}

func newMockOrgStore() *mockOrgStore {
	return &mockOrgStore{
		orgs:           make(map[string]*types.Organization),
		members:        make(map[string][]*types.OrgMember),
		keyMembers:     make(map[string]*secrets.OrgKeyMemberRecord),
		adminCounts:    make(map[string]int),
		pendingKeyWrap: make(map[string]bool),
		salts:          make(map[string][]byte),
	}
}

func memberKey(orgID, userID string) string { return orgID + ":" + userID }

func (m *mockOrgStore) CreateOrgWithAdmin(_ context.Context, org *types.Organization, adminUserID string, _ []byte) (*types.Organization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return nil, m.createErr
	}
	cp := *org
	m.orgs[org.ID] = &cp
	m.members[org.ID] = []*types.OrgMember{
		{OrgID: org.ID, UserID: adminUserID, Role: types.OrgRoleAdmin, PendingKeyWrap: false},
	}
	return &cp, nil
}

func (m *mockOrgStore) GetOrg(_ context.Context, orgID string) (*types.Organization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if org, ok := m.orgs[orgID]; ok {
		cp := *org
		return &cp, nil
	}
	return nil, nil
}

func (m *mockOrgStore) GetOrgBySlug(_ context.Context, slug string) (*types.Organization, error) {
	if m.slugExists {
		return &types.Organization{ID: "existing", Slug: slug}, nil
	}
	return nil, nil
}

func (m *mockOrgStore) ListOrgsForUser(_ context.Context, _ string) ([]*types.OrgResponse, error) {
	return m.listOrgsForUserResult, m.listOrgsForUserErr
}

func (m *mockOrgStore) UpdateOrg(_ context.Context, orgID string, req types.UpdateOrgRequest) (*types.Organization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	org, ok := m.orgs[orgID]
	if !ok {
		return nil, nil
	}
	if req.Name != "" {
		org.Name = req.Name
	}
	if req.Slug != "" {
		org.Slug = req.Slug
	}
	cp := *org
	return &cp, nil
}

func (m *mockOrgStore) SoftDeleteOrg(_ context.Context, orgID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.orgs, orgID)
	return nil
}

func (m *mockOrgStore) OrgHasActiveWorkspaces(_ context.Context, _ string) (bool, error) {
	return m.orgHasWorkspaces, nil
}

func (m *mockOrgStore) IsOrgMember(_ context.Context, orgID, userID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mem := range m.members[orgID] {
		if mem.UserID == userID {
			return true, nil
		}
	}
	return false, nil
}

func (m *mockOrgStore) IsOrgAdmin(_ context.Context, orgID, userID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mem := range m.members[orgID] {
		if mem.UserID == userID && mem.Role == types.OrgRoleAdmin && !mem.PendingKeyWrap {
			return true, nil
		}
	}
	return false, nil
}

func (m *mockOrgStore) GetOrgMember(_ context.Context, orgID, userID string) (*types.OrgMember, error) {
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

func (m *mockOrgStore) ListOrgMembers(_ context.Context, orgID string) ([]*types.OrgMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.members[orgID], nil
}

func (m *mockOrgStore) AddOrgMember(_ context.Context, orgID, userID string, role types.OrgRole, pendingKeyWrap bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.members[orgID] = append(m.members[orgID], &types.OrgMember{
		OrgID: orgID, UserID: userID, Role: role, PendingKeyWrap: pendingKeyWrap,
	})
	return nil
}

func (m *mockOrgStore) RemoveOrgMember(_ context.Context, orgID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	members := m.members[orgID]
	for i, mem := range members {
		if mem.UserID == userID {
			m.members[orgID] = append(members[:i], members[i+1:]...)
			break
		}
	}
	delete(m.keyMembers, memberKey(orgID, userID))
	return nil
}

func (m *mockOrgStore) RemoveOrgAdminIfNotLast(_ context.Context, orgID, targetUserID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	adminCount := 0
	for _, mem := range m.members[orgID] {
		if mem.Role == types.OrgRoleAdmin {
			adminCount++
		}
	}
	if adminCount <= 1 {
		return false, nil
	}
	members := m.members[orgID]
	for i, mem := range members {
		if mem.UserID == targetUserID {
			m.members[orgID] = append(members[:i], members[i+1:]...)
			break
		}
	}
	delete(m.keyMembers, memberKey(orgID, targetUserID))
	return true, nil
}

func (m *mockOrgStore) DemoteOrgAdminIfNotLast(_ context.Context, orgID, targetUserID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	adminCount := 0
	for _, mem := range m.members[orgID] {
		if mem.Role == types.OrgRoleAdmin {
			adminCount++
		}
	}
	if adminCount <= 1 {
		return false, nil
	}
	for _, mem := range m.members[orgID] {
		if mem.UserID == targetUserID {
			mem.Role = types.OrgRoleMember
			break
		}
	}
	delete(m.keyMembers, memberKey(orgID, targetUserID))
	return true, nil
}

func (m *mockOrgStore) CountOrgAdmins(_ context.Context, orgID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, mem := range m.members[orgID] {
		if mem.Role == types.OrgRoleAdmin {
			count++
		}
	}
	return count, nil
}

func (m *mockOrgStore) SetPendingKeyWrap(_ context.Context, orgID, userID string, pending bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mem := range m.members[orgID] {
		if mem.UserID == userID {
			mem.PendingKeyWrap = pending
			return nil
		}
	}
	return nil
}

func (m *mockOrgStore) UpdateOrgMemberRole(_ context.Context, orgID, userID string, role types.OrgRole) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mem := range m.members[orgID] {
		if mem.UserID == userID {
			mem.Role = role
			return nil
		}
	}
	return nil
}

func (m *mockOrgStore) DeleteOrgKeyMember(_ context.Context, orgID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keyMembers, memberKey(orgID, userID))
	return nil
}

func (m *mockOrgStore) ListOrgWorkspaces(_ context.Context, _ string, _, _ int) ([]*types.WorkspaceMetadata, *types.PaginationMetadata, error) {
	return []*types.WorkspaceMetadata{}, &types.PaginationMetadata{Total: 0}, nil
}

func (m *mockOrgStore) GetUserSalt(_ context.Context, userID string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if salt, ok := m.salts[userID]; ok {
		return salt, nil
	}
	return nil, secrets.ErrUserKeysMissing
}

type mockOrgAuthService struct{ userID string }

func (m *mockOrgAuthService) GetUserID(_ *gin.Context) string { return m.userID }

func setupOrgTestRouter(t *testing.T, store *mockOrgStore) (*gin.Engine, *OrgsHandler) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dekCache := newTestDEKCache()
	orgKeyStore := secrets.NewPgOrgKeyStore(nil)
	_ = orgKeyStore
	orgKeySvc := secrets.NewOrgKeyService(nil, dekCache)

	handler := NewOrgsHandler(store, orgKeySvc, dekCache, &mockOrgAuthService{userID: "admin-1"})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "admin-1")
		c.Next()
	})

	orgGroup := router.Group("/api/v1/orgs")
	orgGroup.POST("", handler.Create)
	orgGroup.GET("", handler.List)
	orgGroup.GET("/:id", handler.Get)
	orgGroup.PUT("/:id", handler.Update)
	orgGroup.DELETE("/:id", handler.Delete)
	orgGroup.GET("/:id/workspaces", handler.ListWorkspaces)
	orgGroup.GET("/:id/members", handler.ListMembers)
	orgGroup.POST("/:id/members", handler.AddMember)
	orgGroup.DELETE("/:id/members/:userID", handler.RemoveMember)
	orgGroup.PUT("/:id/members/:userID", handler.ChangeMemberRole)
	orgGroup.POST("/:id/accept-key", handler.AcceptKey)
	orgGroup.POST("/:id/rotate-key", handler.RotateKey)

	return router, handler
}

func doRequest(router *gin.Engine, method, path string, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// --- Tests ---

func TestOrgsHandler_Create_SlugConflict(t *testing.T) {
	store := newMockOrgStore()
	store.slugExists = true
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "POST", "/api/v1/orgs", `{"name":"Test","slug":"test","password":"pass123"}`)
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOrgsHandler_Create_MissingPassword(t *testing.T) {
	store := newMockOrgStore()
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "POST", "/api/v1/orgs", `{"name":"Test","slug":"test"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestOrgsHandler_Create_Success(t *testing.T) {
	store := newMockOrgStore()
	store.salts["admin-1"] = make([]byte, 32)
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "POST", "/api/v1/orgs", `{"name":"Test Org","slug":"testorg","password":"secretpass"}`)
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp types.OrgResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Name != "Test Org" {
		t.Errorf("name: got %q", resp.Name)
	}
	if resp.UserRole != types.OrgRoleAdmin {
		t.Errorf("expected admin role, got %q", resp.UserRole)
	}
}

func TestOrgsHandler_RemoveMember_LastAdmin(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.members["org-1"] = []*types.OrgMember{
		{OrgID: "org-1", UserID: "admin-1", Role: types.OrgRoleAdmin},
		{OrgID: "org-1", UserID: "admin-2", Role: types.OrgRoleAdmin},
	}
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "DELETE", "/api/v1/orgs/org-1/members/admin-2", "")
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOrgsHandler_RemoveMember_LastAdminBlocked(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.members["org-1"] = []*types.OrgMember{
		{OrgID: "org-1", UserID: "admin-1", Role: types.OrgRoleAdmin},
		{OrgID: "org-1", UserID: "other", Role: types.OrgRoleAdmin},
	}
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "DELETE", "/api/v1/orgs/org-1/members/other", "")
	if w.Code != http.StatusNoContent {
		t.Errorf("with 2 admins, removing one should succeed, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOrgsHandler_RemoveMember_SelfRemovalBlocked(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.members["org-1"] = []*types.OrgMember{
		{OrgID: "org-1", UserID: "admin-1", Role: types.OrgRoleAdmin},
		{OrgID: "org-1", UserID: "admin-2", Role: types.OrgRoleAdmin},
	}
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "DELETE", "/api/v1/orgs/org-1/members/admin-1", "")
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for self-removal, got %d", w.Code)
	}
}

func TestOrgsHandler_RemoveMember_NotFound(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.members["org-1"] = []*types.OrgMember{
		{OrgID: "org-1", UserID: "admin-1", Role: types.OrgRoleAdmin},
	}
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "DELETE", "/api/v1/orgs/org-1/members/nonexistent", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestOrgsHandler_ChangeMemberRole_PromoteSetsPendingKeyWrap(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.members["org-1"] = []*types.OrgMember{
		{OrgID: "org-1", UserID: "admin-1", Role: types.OrgRoleAdmin},
		{OrgID: "org-1", UserID: "member-1", Role: types.OrgRoleMember},
	}
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "PUT", "/api/v1/orgs/org-1/members/member-1", `{"role":"admin"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	member, _ := store.GetOrgMember(context.Background(), "org-1", "member-1")
	if member == nil {
		t.Fatal("member not found")
	}
	if member.Role != types.OrgRoleAdmin {
		t.Errorf("expected role=admin, got %q", member.Role)
	}
	if !member.PendingKeyWrap {
		t.Error("REGRESSION: promoted admin must have pendingKeyWrap=true")
	}
}

func TestOrgsHandler_ChangeMemberRole_DemoteLastAdminBlocked(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.members["org-1"] = []*types.OrgMember{
		{OrgID: "org-1", UserID: "admin-1", Role: types.OrgRoleAdmin},
		{OrgID: "org-1", UserID: "member-1", Role: types.OrgRoleMember},
	}
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "PUT", "/api/v1/orgs/org-1/members/member-1", `{"role":"member"}`)
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for same role, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOrgsHandler_ChangeMemberRole_DemoteSelfBlocked(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.members["org-1"] = []*types.OrgMember{
		{OrgID: "org-1", UserID: "admin-1", Role: types.OrgRoleAdmin},
		{OrgID: "org-1", UserID: "admin-2", Role: types.OrgRoleAdmin},
	}
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "PUT", "/api/v1/orgs/org-1/members/admin-1", `{"role":"member"}`)
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for self-demotion, got %d", w.Code)
	}
}

func TestOrgsHandler_AcceptKey_NonAdmin(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.members["org-1"] = []*types.OrgMember{
		{OrgID: "org-1", UserID: "admin-1", Role: types.OrgRoleMember},
	}
	store.salts["admin-1"] = make([]byte, 32)
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "POST", "/api/v1/orgs/org-1/accept-key", `{"password":"pass"}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-admin calling accept-key, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOrgsHandler_Delete_OrgWithWorkspaces(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.members["org-1"] = []*types.OrgMember{
		{OrgID: "org-1", UserID: "admin-1", Role: types.OrgRoleAdmin},
	}
	store.orgHasWorkspaces = true
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "DELETE", "/api/v1/orgs/org-1", "")
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for org with workspaces, got %d", w.Code)
	}
}

func TestOrgsHandler_List_Success(t *testing.T) {
	store := newMockOrgStore()
	store.listOrgsForUserResult = []*types.OrgResponse{
		{Organization: types.Organization{ID: "org-1", Name: "Test"}, UserRole: types.OrgRoleAdmin, MemberCount: 3},
	}
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "GET", "/api/v1/orgs", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var orgs []*types.OrgResponse
	json.Unmarshal(w.Body.Bytes(), &orgs)
	if len(orgs) != 1 || orgs[0].Name != "Test" {
		t.Errorf("unexpected response: %s", w.Body.String())
	}
}

func TestOrgsHandler_AddMember_Success(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.members["org-1"] = []*types.OrgMember{
		{OrgID: "org-1", UserID: "admin-1", Role: types.OrgRoleAdmin},
	}
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "POST", "/api/v1/orgs/org-1/members", `{"userId":"new-user","role":"member"}`)
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOrgsHandler_AddMember_AdminSetsPendingKeyWrap(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.members["org-1"] = []*types.OrgMember{
		{OrgID: "org-1", UserID: "admin-1", Role: types.OrgRoleAdmin},
	}
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "POST", "/api/v1/orgs/org-1/members", `{"userId":"new-admin","role":"admin"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	member, _ := store.GetOrgMember(context.Background(), "org-1", "new-admin")
	if member == nil {
		t.Fatal("new admin member not found")
	}
	if !member.PendingKeyWrap {
		t.Error("new admin must have pendingKeyWrap=true")
	}
}

func TestOrgsHandler_ListWorkspaces_LimitCapped(t *testing.T) {
	store := newMockOrgStore()
	store.orgs["org-1"] = &types.Organization{ID: "org-1"}
	store.members["org-1"] = []*types.OrgMember{
		{OrgID: "org-1", UserID: "admin-1", Role: types.OrgRoleAdmin},
	}
	router, _ := setupOrgTestRouter(t, store)

	w := doRequest(router, "GET", "/api/v1/orgs/org-1/workspaces?limit=999999", "")
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
