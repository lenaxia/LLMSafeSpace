package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/handlers"
	"github.com/lenaxia/llmsafespace/pkg/credentials"
)

type mockCredentialService struct {
	sets     map[string]*credentials.CredentialSet
	nextID   int
	createErr error
	getErr    error
	updateErr error
	deleteErr error
}

func newMockCredSvc() *mockCredentialService {
	return &mockCredentialService{sets: make(map[string]*credentials.CredentialSet)}
}

func (m *mockCredentialService) Create(_ context.Context, req credentials.CreateCredentialSetRequest) (*credentials.CredentialSet, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	m.nextID++
	cs := &credentials.CredentialSet{
		ID:             fmt.Sprintf("cred-%d", m.nextID),
		Name:           req.Name,
		IsDefault:      req.IsDefault,
		Providers:      []string{},
		ModelAllowlist: req.ModelAllowlist,
		AssignedTo:     req.AssignedTo,
	}
	for name := range req.Providers {
		cs.Providers = append(cs.Providers, name)
	}
	m.sets[cs.ID] = cs
	return cs, nil
}

func (m *mockCredentialService) Get(_ context.Context, id string) (*credentials.CredentialSet, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	cs, ok := m.sets[id]
	if !ok {
		return nil, fmt.Errorf("credential set %q not found", id)
	}
	return cs, nil
}

func (m *mockCredentialService) List(_ context.Context) ([]*credentials.CredentialSet, error) {
	var result []*credentials.CredentialSet
	for _, cs := range m.sets {
		result = append(result, cs)
	}
	return result, nil
}

func (m *mockCredentialService) Update(_ context.Context, id string, _ credentials.UpdateCredentialSetRequest) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	if _, ok := m.sets[id]; !ok {
		return fmt.Errorf("credential set %q not found", id)
	}
	return nil
}

func (m *mockCredentialService) Delete(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.sets, id)
	return nil
}

func (m *mockCredentialService) SetDefault(_ context.Context, _ string) error {
	return nil
}

func (m *mockCredentialService) GetDefault(_ context.Context) (*credentials.CredentialSet, error) {
	return nil, nil
}

func (m *mockCredentialService) RotateEncryptionKey(_ context.Context) (*credentials.RotateKeyResult, error) {
	return &credentials.RotateKeyResult{Rotated: 2, AlreadyCurrent: 1}, nil
}

func (m *mockCredentialService) ListForUser(_ context.Context, _ string) ([]*credentials.CredentialSet, error) {
	return nil, nil
}

func setupCredRouter(svc *mockCredentialService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handlers.NewCredentialsHandler(svc)
	g := r.Group("/api/v1/admin/credentials")
	g.POST("", h.CreateCredentialSet)
	g.GET("", h.ListCredentialSets)
	g.GET("/:id", h.GetCredentialSet)
	g.PUT("/:id", h.UpdateCredentialSet)
	g.DELETE("/:id", h.DeleteCredentialSet)
	g.PUT("/:id/default", h.SetDefaultCredentialSet)
	g.POST("/rotate-key", h.RotateCredentialKey)
	return r
}

func TestCredHandler_Create_Success(t *testing.T) {
	svc := newMockCredSvc()
	r := setupCredRouter(svc)

	body, _ := json.Marshal(credentials.CreateCredentialSetRequest{
		Name:      "prod",
		Providers: credentials.ProviderConfig{"openai": {APIKey: "sk-test"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCredHandler_Create_InvalidBody(t *testing.T) {
	svc := newMockCredSvc()
	r := setupCredRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/credentials", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCredHandler_Get_Success(t *testing.T) {
	svc := newMockCredSvc()
	cs, _ := svc.Create(context.Background(), credentials.CreateCredentialSetRequest{
		Name:      "test",
		Providers: credentials.ProviderConfig{"x": {APIKey: "k"}},
	})
	r := setupCredRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/credentials/"+cs.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCredHandler_Get_NotFound(t *testing.T) {
	svc := newMockCredSvc()
	r := setupCredRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/credentials/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestCredHandler_List_Success(t *testing.T) {
	svc := newMockCredSvc()
	svc.Create(context.Background(), credentials.CreateCredentialSetRequest{
		Name: "a", Providers: credentials.ProviderConfig{"x": {APIKey: "k"}},
	})
	r := setupCredRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/credentials", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestCredHandler_Delete_Success(t *testing.T) {
	svc := newMockCredSvc()
	cs, _ := svc.Create(context.Background(), credentials.CreateCredentialSetRequest{
		Name: "del", Providers: credentials.ProviderConfig{"x": {APIKey: "k"}},
	})
	r := setupCredRouter(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/credentials/"+cs.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCredHandler_Delete_Referenced_Returns409(t *testing.T) {
	svc := newMockCredSvc()
	cs, _ := svc.Create(context.Background(), credentials.CreateCredentialSetRequest{
		Name: "ref", Providers: credentials.ProviderConfig{"x": {APIKey: "k"}},
	})
	svc.deleteErr = fmt.Errorf("credential set is referenced by 2 workspace(s)")
	r := setupCredRouter(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/credentials/"+cs.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCredHandler_Update_Success(t *testing.T) {
	svc := newMockCredSvc()
	cs, _ := svc.Create(context.Background(), credentials.CreateCredentialSetRequest{
		Name: "upd", Providers: credentials.ProviderConfig{"x": {APIKey: "k"}},
	})
	r := setupCredRouter(svc)

	body, _ := json.Marshal(map[string]string{"name": "updated"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/credentials/"+cs.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCredHandler_SetDefault_Success(t *testing.T) {
	svc := newMockCredSvc()
	cs, _ := svc.Create(context.Background(), credentials.CreateCredentialSetRequest{
		Name: "def", Providers: credentials.ProviderConfig{"x": {APIKey: "k"}},
	})
	r := setupCredRouter(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/credentials/"+cs.ID+"/default", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCredHandler_RotateKey_Success(t *testing.T) {
	svc := newMockCredSvc()
	r := setupCredRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/credentials/rotate-key", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result credentials.RotateKeyResult
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Rotated != 2 {
		t.Errorf("expected 2 rotated, got %d", result.Rotated)
	}
}
