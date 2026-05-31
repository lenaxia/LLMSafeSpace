// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/handlers"
	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	"github.com/lenaxia/llmsafespace/pkg/credentials"
)

// TestIntegration_CredentialCRUDLifecycle exercises the full flow:
// create → get → list → update → set-default → rotate-key → delete
func TestIntegration_CredentialCRUDLifecycle(t *testing.T) {
	svc := newMockCredSvc()
	r := setupCredRouterWithAdminGuard(svc)

	// 1. Create
	body, _ := json.Marshal(credentials.CreateCredentialSetRequest{
		Name:           "production",
		Providers:      credentials.ProviderConfig{"openai": {APIKey: "sk-live-123"}},
		ModelAllowlist: []string{"gpt-4", "gpt-4o"},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created credentials.CredentialSet
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == "" {
		t.Fatal("create: expected non-empty ID")
	}
	if created.Name != "production" {
		t.Errorf("create: expected name 'production', got %q", created.Name)
	}

	// 2. Get by ID
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/v1/admin/credentials/"+created.ID, nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", w.Code)
	}

	// 3. List
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/v1/admin/credentials", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	var list []credentials.CredentialSet
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Errorf("list: expected 1, got %d", len(list))
	}

	// 4. Update
	updateBody, _ := json.Marshal(map[string]string{"name": "production-v2"})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("PUT", "/api/v1/admin/credentials/"+created.ID, bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 5. Set default
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("PUT", "/api/v1/admin/credentials/"+created.ID+"/default", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set-default: expected 200, got %d", w.Code)
	}

	// 6. Rotate key
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/v1/admin/credentials/rotate-key", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("rotate-key: expected 200, got %d", w.Code)
	}

	// 7. Delete
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("DELETE", "/api/v1/admin/credentials/"+created.ID, nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// 8. Get after delete — should 404
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/v1/admin/credentials/"+created.ID, nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("get-after-delete: expected 404, got %d", w.Code)
	}
}

// TestIntegration_NonAdminCannotAccessCredentials verifies AdminGuard blocks non-admin users.
func TestIntegration_NonAdminCannotAccessCredentials(t *testing.T) {
	svc := newMockCredSvc()
	r := setupCredRouterWithRole(svc, "user")

	endpoints := []struct {
		method string
		path   string
	}{
		{"POST", "/api/v1/admin/credentials"},
		{"GET", "/api/v1/admin/credentials"},
		{"GET", "/api/v1/admin/credentials/some-id"},
		{"PUT", "/api/v1/admin/credentials/some-id"},
		{"DELETE", "/api/v1/admin/credentials/some-id"},
		{"PUT", "/api/v1/admin/credentials/some-id/default"},
		{"POST", "/api/v1/admin/credentials/rotate-key"},
	}

	for _, ep := range endpoints {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(ep.method, ep.path, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s: expected 404 for non-admin, got %d", ep.method, ep.path, w.Code)
		}
	}
}

func setupCredRouterWithAdminGuard(svc *mockCredentialService) *gin.Engine {
	return setupCredRouterWithRole(svc, "admin")
}

func setupCredRouterWithRole(svc *mockCredentialService, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "test-admin")
		c.Set("userRole", role)
		c.Next()
	})
	h := handlers.NewCredentialsHandler(svc)
	g := r.Group("/api/v1/admin/credentials")
	g.Use(middleware.AdminGuard())
	g.POST("", h.CreateCredentialSet)
	g.GET("", h.ListCredentialSets)
	g.GET("/:id", h.GetCredentialSet)
	g.PUT("/:id", h.UpdateCredentialSet)
	g.DELETE("/:id", h.DeleteCredentialSet)
	g.PUT("/:id/default", h.SetDefaultCredentialSet)
	g.POST("/rotate-key", h.RotateCredentialKey)
	return r
}
