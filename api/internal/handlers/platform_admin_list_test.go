// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

// --- interface-completing additions to the existing mock stores ---
//
// platform_admin_test.go defines *mockPlatformOrgStore and
// *mockPlatformUserStore for the suspension endpoints. US-43.18 extends both
// handler-side interfaces with a list method, so the same mock types must grow
// the list method or the existing tests stop compiling. The methods live here,
// next to the new list-handler tests, so the suspension test file stays focused.

func (m *mockPlatformOrgStore) ListAllOrgs(_ context.Context, limit, offset int, statusFilter *string) ([]types.OrgSummary, *types.PaginationMetadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listOrgsCalls = append(m.listOrgsCalls, struct {
		Limit  int
		Offset int
		Status *string
	}{limit, offset, statusFilter})
	if m.listOrgsErr != nil {
		return nil, nil, m.listOrgsErr
	}
	return m.listOrgs, m.listOrgsPage, nil
}

func (m *mockPlatformUserStore) ListAllUsers(_ context.Context, limit, offset int, statusFilter *string) ([]types.UserListEntry, *types.PaginationMetadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listUsersCalls = append(m.listUsersCalls, struct {
		Limit  int
		Offset int
		Status *string
	}{limit, offset, statusFilter})
	if m.listUsersErr != nil {
		return nil, nil, m.listUsersErr
	}
	return m.listUsers, m.listUsersPage, nil
}

func setupPlatformListRouter(t *testing.T, orgs *mockPlatformOrgStore, users *mockPlatformUserStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewPlatformAdminHandler(orgs, users, &mockOrgAuthService{userID: "admin-1"}, &stubLogger{})
	r := gin.New()
	r.GET("/api/v1/admin/orgs", h.ListOrgs)
	r.GET("/api/v1/admin/users", h.ListUsers)
	return r
}

// --- ListOrgs handler ---

func TestListOrgs_Happy(t *testing.T) {
	orgs := &mockPlatformOrgStore{
		listOrgs: []types.OrgSummary{
			{Organization: types.Organization{ID: "org-1", Name: "Acme", Slug: "acme", Status: types.OrgStatusActive, PlanID: types.PlanEnterprise}, MemberCount: 3, WorkspaceCount: 5},
			{Organization: types.Organization{ID: "org-2", Name: "Globex", Slug: "globex", Status: types.OrgStatusSuspended, PlanID: types.PlanTeam}, MemberCount: 1, WorkspaceCount: 2},
		},
		listOrgsPage: &types.PaginationMetadata{Total: 2, Start: 0, End: 2, Limit: 50, Offset: 0},
	}
	users := &mockPlatformUserStore{}
	r := setupPlatformListRouter(t, orgs, users)

	w := doRequest(r, "GET", "/api/v1/admin/orgs", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items      []types.OrgSummary        `json:"items"`
		Pagination *types.PaginationMetadata `json:"pagination"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}
	if resp.Items[0].ID != "org-1" || resp.Items[0].MemberCount != 3 || resp.Items[0].WorkspaceCount != 5 {
		t.Errorf("unexpected first item: %+v", resp.Items[0])
	}
	if resp.Pagination == nil || resp.Pagination.Total != 2 {
		t.Errorf("expected pagination total 2, got %+v", resp.Pagination)
	}
}

func TestListOrgs_StatusFilterAndPagingPassedThrough(t *testing.T) {
	orgs := &mockPlatformOrgStore{
		listOrgs:     []types.OrgSummary{},
		listOrgsPage: &types.PaginationMetadata{Total: 0, Limit: 10, Offset: 20},
	}
	users := &mockPlatformUserStore{}
	r := setupPlatformListRouter(t, orgs, users)

	w := doRequest(r, "GET", "/api/v1/admin/orgs?status=suspended&limit=10&offset=20", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(orgs.listOrgsCalls) != 1 {
		t.Fatalf("expected 1 ListAllOrgs call, got %d", len(orgs.listOrgsCalls))
	}
	call := orgs.listOrgsCalls[0]
	if call.Limit != 10 || call.Offset != 20 {
		t.Errorf("limit/offset not forwarded: got limit=%d offset=%d", call.Limit, call.Offset)
	}
	if call.Status == nil || *call.Status != "suspended" {
		t.Errorf("status filter not forwarded: got %v", call.Status)
	}
}

func TestListOrgs_LimitClampedToMax(t *testing.T) {
	orgs := &mockPlatformOrgStore{
		listOrgs:     []types.OrgSummary{},
		listOrgsPage: &types.PaginationMetadata{Total: 0, Limit: 200, Offset: 0},
	}
	users := &mockPlatformUserStore{}
	r := setupPlatformListRouter(t, orgs, users)

	w := doRequest(r, "GET", "/api/v1/admin/orgs?limit=9999", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(orgs.listOrgsCalls) != 1 || orgs.listOrgsCalls[0].Limit != 200 {
		t.Errorf("expected limit clamped to 200, got %+v", orgs.listOrgsCalls)
	}
}

func TestListOrgs_StoreError_500(t *testing.T) {
	orgs := &mockPlatformOrgStore{listOrgsErr: errors.New("db down")}
	users := &mockPlatformUserStore{}
	r := setupPlatformListRouter(t, orgs, users)

	w := doRequest(r, "GET", "/api/v1/admin/orgs", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on store error, got %d", w.Code)
	}
}

// --- ListUsers handler ---

func TestListUsers_Happy(t *testing.T) {
	now := time.Now()
	orgs := &mockPlatformOrgStore{}
	users := &mockPlatformUserStore{
		listUsers: []types.UserListEntry{
			{ID: "user-1", Email: "a@example.com", Role: "admin", Status: types.UserStatusActive, CreatedAt: now, OrgCount: 1, OrgID: "org-1", OrgName: "Acme"},
			{ID: "user-2", Email: "b@example.com", Role: "user", Status: types.UserStatusSuspended, CreatedAt: now, OrgCount: 0},
		},
		listUsersPage: &types.PaginationMetadata{Total: 2, Start: 0, End: 2, Limit: 50, Offset: 0},
	}
	r := setupPlatformListRouter(t, orgs, users)

	w := doRequest(r, "GET", "/api/v1/admin/users", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items      []types.UserListEntry     `json:"items"`
		Pagination *types.PaginationMetadata `json:"pagination"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}
	if resp.Items[0].Email != "a@example.com" || resp.Items[0].OrgName != "Acme" {
		t.Errorf("unexpected first user: %+v", resp.Items[0])
	}
	if resp.Items[1].OrgCount != 0 || resp.Items[1].OrgID != "" {
		t.Errorf("expected no-org user to have empty org fields, got %+v", resp.Items[1])
	}
	// Password hashes are never part of UserListEntry — enforced statically by
	// the DTO having no such field; no runtime assertion needed.
}

func TestListUsers_StatusFilterAndPagingPassedThrough(t *testing.T) {
	orgs := &mockPlatformOrgStore{}
	users := &mockPlatformUserStore{
		listUsers:     []types.UserListEntry{},
		listUsersPage: &types.PaginationMetadata{Total: 0, Limit: 5, Offset: 15},
	}
	r := setupPlatformListRouter(t, orgs, users)

	w := doRequest(r, "GET", "/api/v1/admin/users?status=suspended&limit=5&offset=15", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(users.listUsersCalls) != 1 {
		t.Fatalf("expected 1 ListAllUsers call, got %d", len(users.listUsersCalls))
	}
	call := users.listUsersCalls[0]
	if call.Limit != 5 || call.Offset != 15 {
		t.Errorf("limit/offset not forwarded: got limit=%d offset=%d", call.Limit, call.Offset)
	}
	if call.Status == nil || *call.Status != "suspended" {
		t.Errorf("status filter not forwarded: got %v", call.Status)
	}
}

func TestListUsers_StoreError_500(t *testing.T) {
	orgs := &mockPlatformOrgStore{}
	users := &mockPlatformUserStore{listUsersErr: errors.New("db down")}
	r := setupPlatformListRouter(t, orgs, users)

	w := doRequest(r, "GET", "/api/v1/admin/users", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on store error, got %d", w.Code)
	}
}
