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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdminCredStore implements AdminCredentialStore for testing.
type fakeAdminCredStore struct {
	creds     map[string]*secrets.AdminCredentialRow
	nextErr   error
	updateErr error // returned specifically by UpdateAdminCredential
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
	if f.updateErr != nil {
		err := f.updateErr
		f.updateErr = nil
		return err
	}
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
	if _, ok := f.creds[id]; !ok {
		return pgx.ErrNoRows
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
	g.GET("/:id/models", h.ProbeModels)
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

// TestAdminProviderCredentials_Delete_NotFound verifies L-1 fix: deleting a
// non-existent credential returns 404, not 204.
func TestAdminProviderCredentials_Delete_NotFound(t *testing.T) {
	store := newFakeAdminCredStore() // empty store — nothing to delete
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return make([]byte, 32) })
	router := setupAdminCredRouter(h)

	req, _ := http.NewRequest("DELETE", "/api/v1/admin/provider-credentials/missing-id", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAdminProviderCredentials_Update_CorruptCiphertext_Returns500 verifies C-4 fix:
// attempting to rotate the API key when the existing ciphertext is unreadable returns 500
// with an actionable message, instead of silently encrypting a zeroed credential.
func TestAdminProviderCredentials_Update_CorruptCiphertext_Returns500(t *testing.T) {
	store := newFakeAdminCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i)
	}
	// Store a credential whose ciphertext was encrypted with a DIFFERENT key — simulates
	// the "unreadable ciphertext" scenario (wrong KEK, DB corruption, etc.).
	differentKEK := make([]byte, 32)
	store.creds["c1"] = &secrets.AdminCredentialRow{
		ID:         "c1",
		Name:       "test",
		Provider:   "openai",
		Ciphertext: mustEncrypt(t, differentKEK, `{"provider":"openai","apiKey":"original"}`),
	}
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return kek })
	router := setupAdminCredRouter(h)

	body := `{"apiKey":"new-rotated-key"}` // triggers re-encrypt path
	req, _ := http.NewRequest("PUT", "/api/v1/admin/provider-credentials/c1", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Must return 500 with an actionable error, NOT 200 with a zeroed credential.
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "unreadable")
}

// TestAdminProviderCredentials_Update_DuplicateProvider_Returns409 verifies M-4 fix:
// changing provider to one that already exists returns 409 Conflict, not 500.
func TestAdminProviderCredentials_Update_DuplicateProvider_Returns409(t *testing.T) {
	store := newFakeAdminCredStore()
	kek := make([]byte, 32)
	store.creds["c1"] = &secrets.AdminCredentialRow{
		ID: "c1", Name: "existing", Provider: "openai",
		Ciphertext: mustEncrypt(t, kek, `{"provider":"openai","apiKey":"key1"}`),
	}
	// updateErr is consumed ONLY by UpdateAdminCredential; GetAdminCredential won't touch it.
	store.updateErr = &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return kek })
	router := setupAdminCredRouter(h)

	body := `{"name":"renamed"}`
	req, _ := http.NewRequest("PUT", "/api/v1/admin/provider-credentials/c1", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

// TestAdminProviderCredentials_AutoApply_NilStore_Returns503 verifies M-7 fix:
// all three auto-apply handlers return 503 when the store is nil.
func TestAdminProviderCredentials_AutoApply_NilStore_Returns503(t *testing.T) {
	store := newFakeAdminCredStore()
	kek := make([]byte, 32)
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return kek })
	// autoApplyStore is nil by default — do NOT call SetAutoApplyStore
	g := gin.New()
	g.POST("/api/v1/admin/provider-credentials/:id/auto-apply", h.CreateAutoApply)
	g.GET("/api/v1/admin/provider-credentials/:id/auto-apply", h.ListAutoApply)
	g.DELETE("/api/v1/admin/provider-credentials/:id/auto-apply/:targetType/:targetId", h.DeleteAutoApply)

	for _, tc := range []struct {
		method string
		url    string
		body   string
	}{
		{"POST", "/api/v1/admin/provider-credentials/c1/auto-apply", `{"targetType":"all"}`},
		{"GET", "/api/v1/admin/provider-credentials/c1/auto-apply", ""},
		{"DELETE", "/api/v1/admin/provider-credentials/c1/auto-apply/all/_", ""},
	} {
		t.Run(tc.method, func(t *testing.T) {
			var bodyReader *bytes.Reader
			if tc.body != "" {
				bodyReader = bytes.NewReader([]byte(tc.body))
			} else {
				bodyReader = bytes.NewReader(nil)
			}
			req, _ := http.NewRequest(tc.method, tc.url, bodyReader)
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			g.ServeHTTP(w, req)
			assert.Equal(t, http.StatusServiceUnavailable, w.Code, "method %s should return 503", tc.method)
		})
	}
}

// TestAdminProviderCredentials_Create_ModelContextLimits verifies that
// modelContextLimits round-trips through create and appears in the response.
func TestAdminProviderCredentials_Create_ModelContextLimits(t *testing.T) {
	store := newFakeAdminCredStore()
	kek := make([]byte, 32)
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return kek })
	router := setupAdminCredRouter(h)

	body := `{
		"name":"limits-test","provider":"openai","apiKey":"sk-test",
		"baseURL":"https://example.com/v1",
		"modelAllowlist":["glm-5.1","gpt-4o"],
		"modelContextLimits":{"glm-5.1":200000,"gpt-4o":128000}
	}`
	req, _ := http.NewRequest("POST", "/api/v1/admin/provider-credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	var resp AdminCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, []string{"glm-5.1", "gpt-4o"}, resp.ModelAllowlist)
	require.Equal(t, 200000, resp.ModelContextLimits["glm-5.1"])
	require.Equal(t, 128000, resp.ModelContextLimits["gpt-4o"])
}

// TestAdminProviderCredentials_Update_ModelContextLimits verifies that
// modelContextLimits can be updated independently via PUT.
func TestAdminProviderCredentials_Update_ModelContextLimits(t *testing.T) {
	store := newFakeAdminCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 1)
	}
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return kek })
	router := setupAdminCredRouter(h)

	// Create first.
	createBody := `{"name":"c1","provider":"openai","apiKey":"sk-orig","baseURL":"https://x.com/v1"}`
	req, _ := http.NewRequest("POST", "/api/v1/admin/provider-credentials", bytes.NewBufferString(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var created AdminCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	// Update context limits.
	updateBody := `{"modelAllowlist":["glm-5.2"],"modelContextLimits":{"glm-5.2":1000000}}`
	req, _ = http.NewRequest("PUT", "/api/v1/admin/provider-credentials/"+created.ID,
		bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var updated AdminCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	assert.Equal(t, []string{"glm-5.2"}, updated.ModelAllowlist)
	assert.Equal(t, 1000000, updated.ModelContextLimits["glm-5.2"])
}

// TestAdminProviderCredentials_ProbeModels_NoBaseURL verifies that the probe
// endpoint returns a graceful warning when the credential has no baseURL rather
// than attempting to probe a nil URL.
func TestAdminProviderCredentials_ProbeModels_NoBaseURL(t *testing.T) {
	store := newFakeAdminCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 2)
	}
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return kek })
	router := setupAdminCredRouter(h)

	// Create a credential without baseURL (native provider).
	createBody := `{"name":"native","provider":"anthropic","apiKey":"sk-ant-123"}`
	req, _ := http.NewRequest("POST", "/api/v1/admin/provider-credentials", bytes.NewBufferString(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var created AdminCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	// Probe models — must return 200 with a warning, not 500.
	req, _ = http.NewRequest("GET", "/api/v1/admin/provider-credentials/"+created.ID+"/models", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var probe ProbeModelsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &probe))
	assert.NotEmpty(t, probe.Warning, "no-baseURL credential must return a warning")
	assert.Empty(t, probe.Models, "no-baseURL credential must return empty model list")
}

// TestAdminProviderCredentials_ProbeModels_NotFound verifies 404 for unknown ID.
func TestAdminProviderCredentials_ProbeModels_NotFound(t *testing.T) {
	store := newFakeAdminCredStore()
	kek := make([]byte, 32)
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return kek })
	router := setupAdminCredRouter(h)

	req, _ := http.NewRequest("GET", "/api/v1/admin/provider-credentials/does-not-exist/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAdminProviderCredentials_ProbeModels_WithBaseURL_CallsProvider verifies
// that when a credential has a baseURL, the probe endpoint attempts to contact
// the provider and returns a warning (not 500) when it fails.
func TestAdminProviderCredentials_ProbeModels_WithBaseURL_CallsProvider(t *testing.T) {
	store := newFakeAdminCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 3)
	}
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return kek })
	router := setupAdminCredRouter(h)

	// Create with a baseURL that won't be reachable in tests.
	createBody := `{"name":"custom","provider":"custom","apiKey":"sk-test","baseURL":"http://localhost:19999/v1"}`
	req, _ := http.NewRequest("POST", "/api/v1/admin/provider-credentials", bytes.NewBufferString(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var created AdminCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	// Probe — provider unreachable, must return 200 with warning.
	req, _ = http.NewRequest("GET", "/api/v1/admin/provider-credentials/"+created.ID+"/models", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var probe ProbeModelsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &probe))
	assert.NotEmpty(t, probe.Warning, "unreachable provider must return a warning")
}

// TestAdminProviderCredentials_ProbeModels_WithBaseURL_Success verifies that
// when a provider's /models endpoint is reachable and returns a valid list,
// the probe response includes those models with saved context limits merged in.
func TestAdminProviderCredentials_ProbeModels_WithBaseURL_Success(t *testing.T) {
	// Spin up a fake /models server.
	fakeProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		assert.Equal(t, "Bearer sk-probe-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-5.1"},{"id":"glm-5.2"},{"id":"classifier"}]}`))
	}))
	defer fakeProvider.Close()

	store := newFakeAdminCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 4)
	}
	h := NewAdminProviderCredentialsHandler(store, func(string) []byte { return kek })
	router := setupAdminCredRouter(h)

	// Create with saved context limits for two of the three models.
	createBody, _ := json.Marshal(map[string]interface{}{
		"name":               "thekao",
		"provider":           "thekao cloud",
		"apiKey":             "sk-probe-key",
		"baseURL":            fakeProvider.URL + "/v1",
		"modelAllowlist":     []string{"glm-5.1", "glm-5.2"},
		"modelContextLimits": map[string]int{"glm-5.1": 200000, "glm-5.2": 1000000},
	})
	req, _ := http.NewRequest("POST", "/api/v1/admin/provider-credentials", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var created AdminCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	// Probe — should return all 3 models from the fake provider,
	// with saved context limits pre-populated for glm-5.1 and glm-5.2.
	req, _ = http.NewRequest("GET", "/api/v1/admin/provider-credentials/"+created.ID+"/models", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var probe ProbeModelsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &probe))

	assert.Empty(t, probe.Warning)
	require.Len(t, probe.Models, 3)
	byID := map[string]ProbeModelEntry{}
	for _, m := range probe.Models {
		byID[m.ID] = m
	}
	assert.Equal(t, 200000, byID["glm-5.1"].ContextLimit)
	assert.Equal(t, 1000000, byID["glm-5.2"].ContextLimit)
	assert.Equal(t, 0, byID["classifier"].ContextLimit, "classifier has no saved limit")
}
