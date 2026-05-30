package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lenaxia/llmsafespace/api/internal/handlers"
	"github.com/lenaxia/llmsafespace/api/internal/server"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/lenaxia/llmsafespace/pkg/types"

	"github.com/gin-gonic/gin"
)

// TestSecretsWiring_E2E tests that the secrets handler is properly wired
// into the router and processes requests end-to-end.
func TestSecretsWiring_E2E(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create real services using in-memory adapters (same as app.go wiring)
	keyStore := &dbKeyStoreAdapter{}
	dekCache := &memDEKCache{store: make(map[string][]byte)}
	keyService := secrets.NewKeyService(keyStore, dekCache)
	secretStore := &dbSecretStoreAdapter{}
	secretService := secrets.NewSecretService(keyService, secretStore)
	secretsHandler := handlers.NewSecretsHandler(secretService)

	// Initialize user keys (simulates registration)
	_, err := keyService.InitializeUserKeys(context.Background(), "test-user", []byte("password"))
	if err != nil {
		t.Fatalf("InitializeUserKeys: %v", err)
	}

	// Unlock DEK (simulates login)
	err = keyService.UnlockDEK(context.Background(), "test-user", []byte("password"), "test-jti", 0)
	if err != nil {
		t.Fatalf("UnlockDEK: %v", err)
	}

	// Create a minimal router with auth simulation + secrets routes
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "test-user")
		c.Set("sessionID", "test-jti")
		c.Next()
	})

	// Wire exactly as the real router does
	secretsGroup := router.Group("/api/v1/secrets")
	secretsGroup.POST("", secretsHandler.CreateSecret)
	secretsGroup.GET("", secretsHandler.ListSecrets)
	secretsGroup.GET("/audit", secretsHandler.GetAuditLog)
	secretsGroup.GET("/:id", secretsHandler.GetSecret)
	secretsGroup.PUT("/:id", secretsHandler.UpdateSecret)
	secretsGroup.DELETE("/:id", secretsHandler.DeleteSecret)

	wsGroup := router.Group("/api/v1/workspaces")
	wsGroup.PUT("/:id/bindings", secretsHandler.SetBindings)
	wsGroup.GET("/:id/bindings", secretsHandler.GetBindings)

	// === Test: Create secret ===
	body := `{"name":"wiring-test","type":"api-key","value":"sk-wired-123","metadata":{"provider":"openai"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/secrets", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Create: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created secrets.SecretResponse
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == "" {
		t.Fatal("Created secret should have ID")
	}

	// === Test: List secrets ===
	req = httptest.NewRequest(http.MethodGet, "/api/v1/secrets", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("List: expected 200, got %d", w.Code)
	}

	// === Test: Get secret ===
	req = httptest.NewRequest(http.MethodGet, "/api/v1/secrets/"+created.ID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Get: expected 200, got %d", w.Code)
	}

	// === Test: Bind to workspace ===
	bindBody := `{"secretIds":["` + created.ID + `"]}`
	req = httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-wiring/bindings", bytes.NewBufferString(bindBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("Bind: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// === Test: Get bindings ===
	req = httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-wiring/bindings", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GetBindings: expected 200, got %d", w.Code)
	}
	var bindResp secrets.BindingsResponse
	json.Unmarshal(w.Body.Bytes(), &bindResp)
	if len(bindResp.Bindings) != 1 {
		t.Errorf("Expected 1 binding, got %d", len(bindResp.Bindings))
	}

	// === Test: Audit log ===
	req = httptest.NewRequest(http.MethodGet, "/api/v1/secrets/audit", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Audit: expected 200, got %d", w.Code)
	}

	// === Test: Delete ===
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/secrets/"+created.ID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("Delete: expected 204, got %d", w.Code)
	}

	t.Log("Secrets wiring E2E: all operations successful")
}

// TestRouterConfig_SecretsHandler verifies the router accepts SecretsHandler in config
func TestRouterConfig_SecretsHandler(t *testing.T) {
	cfg := server.DefaultRouterConfig()
	// Should not panic with nil SecretsHandler
	if cfg.SecretsHandler != nil {
		t.Error("Default config should have nil SecretsHandler")
	}
}

// TestSecretsHandler_PodIPResolverWired is the regression test for Bug 1
// in worklog 0085: app.New must call SetPodIPResolver on the secrets
// handler. Without this the reload-secrets endpoint and the SetBindings
// auto-push both silently no-op (returning 503 / swallowing the error).
//
// We test the wiring helper directly rather than constructing the full
// App because app.New requires PostgreSQL/Redis; the helper is the unit
// of behaviour we actually care about.
func TestSecretsHandler_PodIPResolverWired(t *testing.T) {
	keyStore := &dbKeyStoreAdapter{}
	dekCache := &memDEKCache{store: make(map[string][]byte)}
	keyService := secrets.NewKeyService(keyStore, dekCache)
	secretStore := &dbSecretStoreAdapter{}
	secretService := secrets.NewSecretService(keyService, secretStore)

	h := handlers.NewSecretsHandler(secretService)
	if h.HasPodIPResolver() {
		t.Fatalf("freshly-constructed SecretsHandler must not have a resolver")
	}

	// Same call app.New makes; if this stops being valid the wiring is
	// either changed deliberately (update the test) or it has regressed.
	h.SetPodIPResolver(newSecretsPodIPResolver(
		&fakeAppCRDGetter{},
		&fakeAppDBLookup{},
		nil, // logger optional in this smoke test
	))

	if !h.HasPodIPResolver() {
		t.Fatalf("SetPodIPResolver must populate the handler's resolver")
	}
}

// fakeAppCRDGetter / fakeAppDBLookup are placeholders used only to
// confirm the resolver constructor accepts compatible adapter types.
// Behavioural tests live in secrets_podip_resolver_test.go.
type fakeAppCRDGetter struct{}

func (f *fakeAppCRDGetter) GetWorkspace(string) (*v1.Workspace, error) { return nil, nil }

type fakeAppDBLookup struct{}

func (f *fakeAppDBLookup) GetWorkspace(context.Context, string) (*types.WorkspaceMetadata, error) {
	return nil, nil
}

type memDEKCache struct {
	store map[string][]byte
}

func (m *memDEKCache) CacheDEK(_ context.Context, sessionID string, dek []byte, _ time.Duration) error {
	cp := make([]byte, len(dek))
	copy(cp, dek)
	m.store[sessionID] = cp
	return nil
}

func (m *memDEKCache) GetDEK(_ context.Context, sessionID string) ([]byte, error) {
	dek, ok := m.store[sessionID]
	if !ok {
		return nil, nil
	}
	return dek, nil
}

func (m *memDEKCache) EvictDEK(_ context.Context, sessionID string) error {
	delete(m.store, sessionID)
	return nil
}
