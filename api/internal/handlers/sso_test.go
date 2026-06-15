// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

type mockSSOConfigStore struct {
	mu      sync.Mutex
	configs map[string]*types.OrgSSOConfig
}

func newMockSSOConfigStore() *mockSSOConfigStore {
	return &mockSSOConfigStore{configs: make(map[string]*types.OrgSSOConfig)}
}

func (m *mockSSOConfigStore) GetSSOConfig(_ context.Context, orgID string) (*types.OrgSSOConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cfg, ok := m.configs[orgID]; ok {
		cp := *cfg
		return &cp, nil
	}
	return nil, nil
}

func (m *mockSSOConfigStore) UpsertSSOConfig(_ context.Context, cfg *types.OrgSSOConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *cfg
	m.configs[cfg.OrgID] = &cp
	return nil
}

func (m *mockSSOConfigStore) DeleteSSOConfig(_ context.Context, orgID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.configs, orgID)
	return nil
}

func setupSSORouter(h *SSOHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/orgs/:id/sso", h.Get)
	r.PUT("/api/v1/orgs/:id/sso", h.Put)
	r.DELETE("/api/v1/orgs/:id/sso", h.Delete)
	return r
}

func TestSSOHandler_Get_NotConfigured(t *testing.T) {
	store := newMockSSOConfigStore()
	h := NewSSOHandler(store, &mockOrgAuthService{userID: "admin-1"})

	w := doRequest(setupSSORouter(h), "GET", "/api/v1/orgs/org-1/sso", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestSSOHandler_Get_Configured(t *testing.T) {
	store := newMockSSOConfigStore()
	store.configs["org-1"] = &types.OrgSSOConfig{
		OrgID:         "org-1",
		DiscoveryURL:  "https://idp.example.com/.well-known/openid-configuration",
		ClientID:      "client-123",
		Enabled:       true,
		AutoProvision: true,
		ConfiguredAt:  time.Now(),
	}
	h := NewSSOHandler(store, &mockOrgAuthService{userID: "admin-1"})

	w := doRequest(setupSSORouter(h), "GET", "/api/v1/orgs/org-1/sso", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSSOHandler_Put_Success(t *testing.T) {
	store := newMockSSOConfigStore()
	h := NewSSOHandler(store, &mockOrgAuthService{userID: "admin-1"})

	body := `{"discoveryUrl":"https://idp.example.com/.well-known/openid-configuration","clientId":"client-123","clientSecret":"secret-456"}`
	w := doRequest(setupSSORouter(h), "PUT", "/api/v1/orgs/org-1/sso", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	store.mu.Lock()
	cfg, ok := store.configs["org-1"]
	store.mu.Unlock()
	if !ok {
		t.Fatal("config not stored")
	}
	if cfg.ClientID != "client-123" {
		t.Errorf("expected client-123, got %s", cfg.ClientID)
	}
	if cfg.Enabled != false {
		t.Error("new config should default to enabled=false")
	}
}

func TestSSOHandler_Put_InvalidURL(t *testing.T) {
	store := newMockSSOConfigStore()
	h := NewSSOHandler(store, &mockOrgAuthService{userID: "admin-1"})

	w := doRequest(setupSSORouter(h), "PUT", "/api/v1/orgs/org-1/sso", `{"discoveryUrl":"not-a-url","clientId":"c","clientSecret":"s"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid URL, got %d", w.Code)
	}
}

func TestSSOHandler_Delete(t *testing.T) {
	store := newMockSSOConfigStore()
	store.configs["org-1"] = &types.OrgSSOConfig{OrgID: "org-1"}
	h := NewSSOHandler(store, &mockOrgAuthService{userID: "admin-1"})

	w := doRequest(setupSSORouter(h), "DELETE", "/api/v1/orgs/org-1/sso", "")
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
	store.mu.Lock()
	_, ok := store.configs["org-1"]
	store.mu.Unlock()
	if ok {
		t.Error("config should be deleted")
	}
}

func TestSSOHandler_Put_DefaultGroupClaims(t *testing.T) {
	store := newMockSSOConfigStore()
	h := NewSSOHandler(store, &mockOrgAuthService{userID: "admin-1"})

	body := `{"discoveryUrl":"https://idp.example.com","clientId":"c","clientSecret":"s"}`
	w := doRequest(setupSSORouter(h), "PUT", "/api/v1/orgs/org-1/sso", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	store.mu.Lock()
	cfg := store.configs["org-1"]
	store.mu.Unlock()
	if cfg.GroupAdminClaim != "llmsafespace-admins" {
		t.Errorf("expected default admin claim, got %q", cfg.GroupAdminClaim)
	}
}
