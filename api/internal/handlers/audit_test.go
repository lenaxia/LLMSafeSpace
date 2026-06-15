// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

type mockAuditStore struct {
	mu      sync.Mutex
	entries []*types.AuditEntry
	err     error
}

func (m *mockAuditStore) ListOrgAudit(_ context.Context, _ string, limit, offset int) ([]*types.AuditEntry, *types.PaginationMetadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, nil, m.err
	}
	total := len(m.entries)
	end := offset + limit
	if end > total {
		end = total
	}
	var out []*types.AuditEntry
	if offset < total {
		out = m.entries[offset:end]
	}
	if out == nil {
		out = []*types.AuditEntry{}
	}
	return out, &types.PaginationMetadata{Total: total, Start: offset, End: end, Limit: limit, Offset: offset}, nil
}

func setupAuditRouter(h *AuditHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/orgs/:id/audit", h.List)
	return r
}

func TestAuditHandler_List_Success(t *testing.T) {
	store := &mockAuditStore{
		entries: []*types.AuditEntry{
			{ID: 1, ActorID: "user-1", Domain: "org", Action: "policy.set", TargetID: "allowed_models", CreatedAt: time.Now()},
			{ID: 2, ActorID: "user-1", Domain: "org", Action: "policy.delete", TargetID: "max_workspaces_per_member", CreatedAt: time.Now()},
		},
	}
	h := NewAuditHandler(store)

	w := doRequest(setupAuditRouter(h), "GET", "/api/v1/orgs/org-1/audit", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items      []*types.AuditEntry       `json:"items"`
		Pagination *types.PaginationMetadata `json:"pagination"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}
	if resp.Items[0].Action != "policy.set" {
		t.Errorf("first entry action: got %q", resp.Items[0].Action)
	}
	if resp.Pagination == nil || resp.Pagination.Total != 2 {
		t.Errorf("expected pagination total 2, got %+v", resp.Pagination)
	}
}

func TestAuditHandler_List_Empty(t *testing.T) {
	store := &mockAuditStore{entries: []*types.AuditEntry{}}
	h := NewAuditHandler(store)

	w := doRequest(setupAuditRouter(h), "GET", "/api/v1/orgs/org-1/audit", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAuditHandler_List_LimitCapped(t *testing.T) {
	store := &mockAuditStore{entries: make([]*types.AuditEntry, 10)}
	for i := range store.entries {
		store.entries[i] = &types.AuditEntry{ID: int64(i + 1), Action: "test"}
	}
	h := NewAuditHandler(store)

	w := doRequest(setupAuditRouter(h), "GET", "/api/v1/orgs/org-1/audit?limit=500", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAuditHandler_List_Pagination(t *testing.T) {
	store := &mockAuditStore{entries: make([]*types.AuditEntry, 100)}
	for i := range store.entries {
		store.entries[i] = &types.AuditEntry{ID: int64(i + 1), Action: "test"}
	}
	h := NewAuditHandler(store)

	w := doRequest(setupAuditRouter(h), "GET", "/api/v1/orgs/org-1/audit?limit=10&offset=20", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAuditHandler_List_DBError(t *testing.T) {
	store := &mockAuditStore{err: context.DeadlineExceeded}
	h := NewAuditHandler(store)

	w := doRequest(setupAuditRouter(h), "GET", "/api/v1/orgs/org-1/audit", "")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on DB error, got %d", w.Code)
	}
}
