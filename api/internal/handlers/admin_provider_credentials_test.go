// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdminCredStore implements AdminCredentialStore for testing.
type fakeAdminCredStore struct {
	creds   map[string]*secrets.AdminCredentialRow
	nextErr error
}

func newFakeAdminCredStore() *fakeAdminCredStore {
	return &fakeAdminCredStore{creds: make(map[string]*secrets.AdminCredentialRow)}
}

func (f *fakeAdminCredStore) CreateAdminCredential(_ context.Context, row *secrets.AdminCredentialRow) error {
	if f.nextErr != nil {
		err := f.nextErr
		f.nextErr = nil
		return err
	}
	f.creds[row.ID] = row
	return nil
}

func (f *fakeAdminCredStore) ListAdminCredentials(_ context.Context) ([]*secrets.AdminCredentialRow, error) {
	if f.nextErr != nil {
		err := f.nextErr
		f.nextErr = nil
		return nil, err
	}
	var out []*secrets.AdminCredentialRow
	for _, c := range f.creds {
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeAdminCredStore) GetAdminCredential(_ context.Context, id string) (*secrets.AdminCredentialRow, error) {
	if f.nextErr != nil {
		err := f.nextErr
		f.nextErr = nil
		return nil, err
	}
	c, ok := f.creds[id]
	if !ok {
		return nil, nil
	}
	return c, nil
}

func (f *fakeAdminCredStore) UpdateAdminCredential(_ context.Context, row *secrets.AdminCredentialRow) error {
	if f.nextErr != nil {
		err := f.nextErr
		f.nextErr = nil
		return err
	}
	f.creds[row.ID] = row
	return nil
}

func (f *fakeAdminCredStore) DeleteAdminCredential(_ context.Context, id string) error {
	if f.nextErr != nil {
		err := f.nextErr
		f.nextErr = nil
		return err
	}
	delete(f.creds, id)
	return nil
}

func setupAdminCredRouter(h *AdminProviderCredentialsHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/v1/admin/provider-credentials")
	g.POST("", h.Create)
	g.GET("", h.List)
	g.GET("/:id", h.Get)
	g.PUT("/:id", h.Update)
	g.DELETE("/:id", h.Delete)
	return r
}

func TestAdminProviderCredentials_Create_Success(t *testing.T) {
	store := newFakeAdminCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i)
	}
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return kek })
	router := setupAdminCredRouter(h)

	body := `{"name":"my-anthropic","provider":"anthropic","apiKey":"sk-ant-123"}`
	req, _ := http.NewRequest("POST", "/api/v1/admin/provider-credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp AdminCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "my-anthropic", resp.Name)
	assert.Equal(t, "anthropic", resp.Provider)
	assert.NotEmpty(t, resp.ID)
	assert.Empty(t, resp.BaseURL) // not set
}

func TestAdminProviderCredentials_Create_MissingAPIKey(t *testing.T) {
	store := newFakeAdminCredStore()
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return make([]byte, 32) })
	router := setupAdminCredRouter(h)

	body := `{"name":"my-anthropic","provider":"anthropic"}`
	req, _ := http.NewRequest("POST", "/api/v1/admin/provider-credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAdminProviderCredentials_Create_NilKEK(t *testing.T) {
	store := newFakeAdminCredStore()
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return nil })
	router := setupAdminCredRouter(h)

	body := `{"name":"my-anthropic","provider":"anthropic","apiKey":"sk-ant-123"}`
	req, _ := http.NewRequest("POST", "/api/v1/admin/provider-credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestAdminProviderCredentials_List(t *testing.T) {
	store := newFakeAdminCredStore()
	kek := make([]byte, 32)
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return kek })
	router := setupAdminCredRouter(h)

	// Create one first.
	store.creds["id1"] = &secrets.AdminCredentialRow{
		ID: "id1", Name: "test", Provider: "openai",
		Ciphertext: mustEncrypt(t, kek, `{"provider":"openai","apiKey":"sk-123"}`),
	}

	req, _ := http.NewRequest("GET", "/api/v1/admin/provider-credentials", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var list []AdminCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	assert.Len(t, list, 1)
	assert.Equal(t, "openai", list[0].Provider)
}

func TestAdminProviderCredentials_Get_NotFound(t *testing.T) {
	store := newFakeAdminCredStore()
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return make([]byte, 32) })
	router := setupAdminCredRouter(h)

	req, _ := http.NewRequest("GET", "/api/v1/admin/provider-credentials/nonexistent", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAdminProviderCredentials_Delete(t *testing.T) {
	store := newFakeAdminCredStore()
	store.creds["del-id"] = &secrets.AdminCredentialRow{ID: "del-id", Name: "x", Provider: "anthropic"}
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return make([]byte, 32) })
	router := setupAdminCredRouter(h)

	req, _ := http.NewRequest("DELETE", "/api/v1/admin/provider-credentials/del-id", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, store.creds)
}

func TestAdminProviderCredentials_Update_Success(t *testing.T) {
	store := newFakeAdminCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i)
	}
	store.creds["upd-id"] = &secrets.AdminCredentialRow{
		ID: "upd-id", Name: "old", Provider: "anthropic",
		Ciphertext: mustEncrypt(t, kek, `{"provider":"anthropic","apiKey":"old-key"}`),
	}
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return kek })
	router := setupAdminCredRouter(h)

	body := `{"name":"new-name","provider":"anthropic","apiKey":"new-key","baseURL":"https://custom.api"}`
	req, _ := http.NewRequest("PUT", "/api/v1/admin/provider-credentials/upd-id", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp AdminCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "new-name", resp.Name)
	// BaseURL is stored encrypted; not returned in response (verify via decrypt if needed)
}

func mustEncrypt(t *testing.T, kek []byte, plaintext string) []byte {
	t.Helper()
	ct, err := secrets.EncryptSecret(kek, []byte(plaintext))
	require.NoError(t, err)
	return ct
}
