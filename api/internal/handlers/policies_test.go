// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

type mockPolicyStore struct {
	mu         sync.Mutex
	policies   map[string]map[types.OrgPolicyKey]json.RawMessage
	setErr     error
	auditCalls []auditCall
}

type auditCall struct {
	orgID    string
	actorID  string
	action   string
	targetID string
}

func newMockPolicyStore() *mockPolicyStore {
	return &mockPolicyStore{policies: make(map[string]map[types.OrgPolicyKey]json.RawMessage)}
}

func (m *mockPolicyStore) GetOrgPolicies(_ context.Context, orgID string) ([]*types.OrgPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	orgMap, ok := m.policies[orgID]
	if !ok {
		return []*types.OrgPolicy{}, nil
	}
	var out []*types.OrgPolicy
	for k, v := range orgMap {
		out = append(out, &types.OrgPolicy{OrgID: orgID, Key: k, Value: v})
	}
	return out, nil
}

func (m *mockPolicyStore) SetOrgPolicy(_ context.Context, orgID string, key types.OrgPolicyKey, value json.RawMessage, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setErr != nil {
		return m.setErr
	}
	if m.policies[orgID] == nil {
		m.policies[orgID] = make(map[types.OrgPolicyKey]json.RawMessage)
	}
	m.policies[orgID][key] = value
	return nil
}

func (m *mockPolicyStore) DeleteOrgPolicy(_ context.Context, orgID string, key types.OrgPolicyKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.policies[orgID] != nil {
		delete(m.policies[orgID], key)
	}
	return nil
}

func (m *mockPolicyStore) LogOrgEvent(_ context.Context, orgID, actorID, action, targetID string, _ map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditCalls = append(m.auditCalls, auditCall{orgID: orgID, actorID: actorID, action: action, targetID: targetID})
	return nil
}

func setupPolicyRouter(t *testing.T, store *mockPolicyStore) *PolicyHandler {
	t.Helper()
	return NewPolicyHandler(store, nil, &mockOrgAuthService{userID: "admin-1"}, nil)
}

func TestPolicyHandler_Get_Empty(t *testing.T) {
	store := newMockPolicyStore()
	h := setupPolicyRouter(t, store)

	w := doRequest(setupPolicyTestRouter(h), "GET", "/api/v1/orgs/org-1/policies", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var list []*types.OrgPolicy
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 0 {
		t.Errorf("expected 0 policies, got %d", len(list))
	}
}

func TestPolicyHandler_Put_AllowedModels(t *testing.T) {
	store := newMockPolicyStore()
	h := setupPolicyRouter(t, store)

	w := doRequest(setupPolicyTestRouter(h), "PUT", "/api/v1/orgs/org-1/policies/allowed_models", `["gpt-4o"]`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	store.mu.Lock()
	val, ok := store.policies["org-1"][types.PolicyAllowedModels]
	store.mu.Unlock()
	if !ok {
		t.Fatal("policy not stored")
	}
	var models []string
	_ = json.Unmarshal(val, &models)
	if len(models) != 1 || models[0] != "gpt-4o" {
		t.Errorf("unexpected stored value: %v", models)
	}
}

func TestPolicyHandler_Put_MaxWorkspaces(t *testing.T) {
	store := newMockPolicyStore()
	h := setupPolicyRouter(t, store)

	w := doRequest(setupPolicyTestRouter(h), "PUT", "/api/v1/orgs/org-1/policies/max_workspaces_per_member", `5`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPolicyHandler_Put_InvalidKey(t *testing.T) {
	store := newMockPolicyStore()
	h := setupPolicyRouter(t, store)

	w := doRequest(setupPolicyTestRouter(h), "PUT", "/api/v1/orgs/org-1/policies/invalid_key", `["x"]`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid key, got %d", w.Code)
	}
}

func TestPolicyHandler_Put_InvalidValue_NegativeQuota(t *testing.T) {
	store := newMockPolicyStore()
	h := setupPolicyRouter(t, store)

	w := doRequest(setupPolicyTestRouter(h), "PUT", "/api/v1/orgs/org-1/policies/max_workspaces_per_member", `-1`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for negative quota, got %d", w.Code)
	}
}

func TestPolicyHandler_Put_InvalidValue_WrongType(t *testing.T) {
	store := newMockPolicyStore()
	h := setupPolicyRouter(t, store)

	w := doRequest(setupPolicyTestRouter(h), "PUT", "/api/v1/orgs/org-1/policies/allowed_models", `123`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-array allowed_models, got %d", w.Code)
	}
}

func TestPolicyHandler_Delete(t *testing.T) {
	store := newMockPolicyStore()
	store.policies["org-1"] = map[types.OrgPolicyKey]json.RawMessage{
		types.PolicyAllowedModels: json.RawMessage(`["gpt-4o"]`),
	}
	h := setupPolicyRouter(t, store)

	w := doRequest(setupPolicyTestRouter(h), "DELETE", "/api/v1/orgs/org-1/policies/allowed_models", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	store.mu.Lock()
	_, ok := store.policies["org-1"][types.PolicyAllowedModels]
	store.mu.Unlock()
	if ok {
		t.Error("policy should be deleted")
	}
}

func TestPolicyHandler_Put_EmitsAuditEvent(t *testing.T) {
	store := newMockPolicyStore()
	h := setupPolicyRouter(t, store)

	w := doRequest(setupPolicyTestRouter(h), "PUT", "/api/v1/orgs/org-1/policies/allowed_models", `["gpt-4o"]`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.auditCalls) != 1 {
		t.Fatalf("expected 1 audit call, got %d", len(store.auditCalls))
	}
	call := store.auditCalls[0]
	if call.action != "policy.set" {
		t.Errorf("expected action policy.set, got %q", call.action)
	}
	if call.targetID != "allowed_models" {
		t.Errorf("expected target allowed_models, got %q", call.targetID)
	}
}

func TestPolicyHandler_Delete_EmitsAuditEvent(t *testing.T) {
	store := newMockPolicyStore()
	store.policies["org-1"] = map[types.OrgPolicyKey]json.RawMessage{
		types.PolicyAllowedModels: json.RawMessage(`["gpt-4o"]`),
	}
	h := setupPolicyRouter(t, store)

	w := doRequest(setupPolicyTestRouter(h), "DELETE", "/api/v1/orgs/org-1/policies/allowed_models", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.auditCalls) != 1 {
		t.Fatalf("expected 1 audit call, got %d", len(store.auditCalls))
	}
	if store.auditCalls[0].action != "policy.delete" {
		t.Errorf("expected action policy.delete, got %q", store.auditCalls[0].action)
	}
}

func setupPolicyTestRouter(h *PolicyHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/orgs/:id/policies", h.Get)
	r.PUT("/api/v1/orgs/:id/policies/:key", h.Put)
	r.DELETE("/api/v1/orgs/:id/policies/:key", h.Delete)
	return r
}
