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

// fakeOrgCredStore implements orgCredentialStore for testing. It stores
// ciphertext verbatim and tracks call counts so tests can assert side effects.
type fakeOrgCredStore struct {
	creds          map[string]*secrets.OrgCredentialRow
	nextID         int
	createErr      error
	updateErr      error
	getErr         error
	bindErr        error
	createCalls    int
	updateCalls    int
	bindCalls      int
	lastCreateCT   []byte
	lastUpdateCT   []byte
	lastUpdateKV   int
	lastUpdateName *string
}

func newFakeOrgCredStore() *fakeOrgCredStore {
	return &fakeOrgCredStore{creds: make(map[string]*secrets.OrgCredentialRow)}
}

func (f *fakeOrgCredStore) CreateOrgCredential(_ context.Context, orgID, name, provider string, ciphertext []byte, modelAllowlist []string, modelContextLimits map[string]int) (string, error) {
	f.createCalls++
	if f.createErr != nil {
		return "", f.createErr
	}
	f.nextID++
	id := "cred-" + itoa(f.nextID)
	f.lastCreateCT = ciphertext
	f.creds[id] = &secrets.OrgCredentialRow{
		OrgCredentialMetadata: secrets.OrgCredentialMetadata{
			ID: id, OrgID: orgID, Name: name, Provider: provider,
			ModelAllowlist: modelAllowlist, ModelContextLimits: modelContextLimits,
		},
		Ciphertext: ciphertext,
		KeyVersion: 1,
	}
	return id, nil
}

func (f *fakeOrgCredStore) ListOrgCredentials(_ context.Context, _ string) ([]*secrets.OrgCredentialRow, error) {
	out := make([]*secrets.OrgCredentialRow, 0, len(f.creds))
	for _, c := range f.creds {
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeOrgCredStore) GetOrgCredential(_ context.Context, _, credID string) (*secrets.OrgCredentialRow, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	c, ok := f.creds[credID]
	if !ok {
		return nil, nil
	}
	return c, nil
}

func (f *fakeOrgCredStore) UpdateOrgCredential(_ context.Context, _, credID string, name *string, ciphertext []byte, modelAllowlist []string, modelContextLimits map[string]int, keyVersion int) error {
	f.updateCalls++
	if f.updateErr != nil {
		return f.updateErr
	}
	c, ok := f.creds[credID]
	if !ok {
		return nil
	}
	if name != nil {
		c.Name = *name
		f.lastUpdateName = name
	}
	if ciphertext != nil {
		c.Ciphertext = ciphertext
		c.KeyVersion = keyVersion
		f.lastUpdateCT = ciphertext
		f.lastUpdateKV = keyVersion
	}
	if modelAllowlist != nil {
		c.ModelAllowlist = modelAllowlist
	}
	if modelContextLimits != nil {
		c.ModelContextLimits = modelContextLimits
	}
	return nil
}

func (f *fakeOrgCredStore) DeleteOrgCredential(_ context.Context, _, credID string) error {
	delete(f.creds, credID)
	return nil
}

func (f *fakeOrgCredStore) BindCredentialToAllOrgWorkspaces(_ context.Context, _, _ string) error {
	f.bindCalls++
	return f.bindErr
}

func (f *fakeOrgCredStore) CreateOrgAutoApply(_ context.Context, _, _ string, _ int) error {
	return nil
}
func (f *fakeOrgCredStore) ListOrgAutoApply(_ context.Context, _ string) ([]*secrets.AutoApplyRule, error) {
	return nil, nil
}
func (f *fakeOrgCredStore) DeleteOrgAutoApply(_ context.Context, _, _ string) error { return nil }

// itoa avoids pulling in strconv purely for fake ID generation.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

func setupOrgCredRouter(h *OrgCredentialsHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/v1/orgs/:id/credentials")
	g.POST("", h.Create)
	g.GET("", h.List)
	g.PUT("/:credID", h.Update)
	g.DELETE("/:credID", h.Delete)
	g.GET("/:credID/models", h.ProbeModels)
	return r
}

// TestOrgCredentials_Create_Success verifies the happy path: a request with a
// valid apiKey is encrypted with the org KEK (derived from "org-credentials"),
// stored, bound to org workspaces, and returns 201.
func TestOrgCredentials_Create_Success(t *testing.T) {
	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 1)
	}
	deriver := func(label string) []byte {
		assert.Equal(t, "org-credentials", label, "Create must derive the org-credentials label")
		return kek
	}
	h := NewOrgCredentialsHandler(store, deriver, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	body := `{"name":"team-anthropic","provider":"anthropic","apiKey":"sk-ant-123"}`
	req, _ := http.NewRequest("POST", "/api/v1/orgs/org-1/credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var resp OrgCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "team-anthropic", resp.Name)
	assert.Equal(t, "anthropic", resp.Provider)
	assert.Equal(t, "org-1", resp.OrgID)
	assert.NotEmpty(t, resp.ID)

	require.Equal(t, 1, store.createCalls, "credential must be stored")
	require.Equal(t, 1, store.bindCalls, "credential must be bound to org workspaces")
	require.NotEmpty(t, store.lastCreateCT, "stored ciphertext must be non-empty")

	// The stored ciphertext must decrypt back to the original provider data.
	pd, err := secrets.DecryptSecret(kek, store.lastCreateCT)
	require.NoError(t, err)
	var decoded secrets.LLMProviderData
	require.NoError(t, json.Unmarshal(pd, &decoded))
	assert.Equal(t, "anthropic", decoded.Provider)
	assert.Equal(t, "sk-ant-123", decoded.APIKey)
}

// TestOrgCredentials_Create_NilKEK_503 verifies that when the server KEK is
// unavailable (nil deriver), Create returns 503 and does NOT store anything.
// This anchors the fail-closed contract: never store plaintext or encrypt with
// a nil key.
func TestOrgCredentials_Create_NilKEK_503(t *testing.T) {
	store := newFakeOrgCredStore()
	deriver := func(string) []byte { return nil }
	h := NewOrgCredentialsHandler(store, deriver, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	body := `{"name":"x","provider":"openai","apiKey":"sk-1"}`
	req, _ := http.NewRequest("POST", "/api/v1/orgs/org-1/credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, 0, store.createCalls, "nothing must be stored when KEK is nil")
}

func TestOrgCredentials_Create_MissingAPIKey_400(t *testing.T) {
	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	h := NewOrgCredentialsHandler(store, func(string) []byte { return kek }, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	body := `{"name":"x","provider":"openai"}`
	req, _ := http.NewRequest("POST", "/api/v1/orgs/org-1/credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, store.createCalls)
}

// TestOrgCredentials_Create_BindFails_Returns201WithWarning verifies that a
// bind failure (e.g. no workspaces yet) does not fail the whole create — the
// credential is still stored, and the response carries a bindWarning. This is
// the contract in org_credentials.go:106-112.
func TestOrgCredentials_Create_BindFails_Returns201WithWarning(t *testing.T) {
	store := newFakeOrgCredStore()
	store.bindErr = context.DeadlineExceeded
	kek := make([]byte, 32)
	h := NewOrgCredentialsHandler(store, func(string) []byte { return kek }, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	body := `{"name":"x","provider":"openai","apiKey":"sk-1"}`
	req, _ := http.NewRequest("POST", "/api/v1/orgs/org-1/credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code, "credential must still be created on bind failure")
	var resp OrgCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.BindWarning, "bind failure must surface a warning")
}

// TestOrgCredentials_Update_APIKeyRotation_Success verifies the re-encryption
// path: an existing credential (encrypted with org KEK) is decrypted, its
// apiKey is replaced, and the result is re-encrypted and stored with an
// incremented key version.
func TestOrgCredentials_Update_APIKeyRotation_Success(t *testing.T) {
	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 1)
	}
	// Seed an existing credential encrypted with the org KEK.
	existingPlaintext, _ := json.Marshal(secrets.LLMProviderData{Provider: "anthropic", APIKey: "old-key"}) //nolint:gosec
	existingCT, err := secrets.EncryptSecret(kek, existingPlaintext)
	require.NoError(t, err)
	store.creds["cred-1"] = &secrets.OrgCredentialRow{
		OrgCredentialMetadata: secrets.OrgCredentialMetadata{
			ID: "cred-1", OrgID: "org-1", Name: "old-name", Provider: "anthropic",
		},
		Ciphertext: existingCT,
		KeyVersion: 1,
	}

	deriver := func(label string) []byte {
		assert.Equal(t, "org-credentials", label, "Update must derive the org-credentials label")
		return kek
	}
	h := NewOrgCredentialsHandler(store, deriver, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	body := `{"apiKey":"rotated-key"}`
	req, _ := http.NewRequest("PUT", "/api/v1/orgs/org-1/credentials/cred-1", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.Equal(t, 1, store.updateCalls, "credential must be updated")
	require.Equal(t, 2, store.lastUpdateKV, "key version must increment from 1 to 2")
	require.NotEmpty(t, store.lastUpdateCT, "re-encrypted ciphertext must be stored")
	require.NotEqual(t, existingCT, store.lastUpdateCT, "ciphertext must change after rotation")

	// Decrypt the stored ciphertext and confirm the API key rotated while
	// other fields survived the round trip.
	pd, err := secrets.DecryptSecret(kek, store.lastUpdateCT)
	require.NoError(t, err)
	var decoded secrets.LLMProviderData
	require.NoError(t, json.Unmarshal(pd, &decoded))
	assert.Equal(t, "rotated-key", decoded.APIKey)
	assert.Equal(t, "anthropic", decoded.Provider, "provider must survive rotation")
}

// TestOrgCredentials_Update_NilKEK_503 verifies that rotating the API key when
// the server KEK is unavailable returns 503 and does NOT corrupt the stored
// credential. This is the fail-closed contract for the re-encrypt path.
func TestOrgCredentials_Update_NilKEK_503(t *testing.T) {
	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	existingPlaintext, _ := json.Marshal(secrets.LLMProviderData{Provider: "openai", APIKey: "old"}) //nolint:gosec
	existingCT, err := secrets.EncryptSecret(kek, existingPlaintext)
	require.NoError(t, err)
	store.creds["cred-1"] = &secrets.OrgCredentialRow{
		OrgCredentialMetadata: secrets.OrgCredentialMetadata{ID: "cred-1", OrgID: "org-1", Provider: "openai"},
		Ciphertext:            existingCT,
		KeyVersion:            1,
	}

	deriver := func(string) []byte { return nil }
	h := NewOrgCredentialsHandler(store, deriver, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	body := `{"apiKey":"new"}`
	req, _ := http.NewRequest("PUT", "/api/v1/orgs/org-1/credentials/cred-1", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, 0, store.updateCalls, "nothing must be written when KEK is nil")
	// Existing credential must be untouched.
	require.Equal(t, existingCT, store.creds["cred-1"].Ciphertext, "stored ciphertext must not be corrupted")
}

func TestOrgCredentials_Update_NotFound_404(t *testing.T) {
	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	h := NewOrgCredentialsHandler(store, func(string) []byte { return kek }, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	body := `{"name":"new"}`
	req, _ := http.NewRequest("PUT", "/api/v1/orgs/org-1/credentials/missing", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, 0, store.updateCalls)
}

// TestOrgCredentials_Update_NameOnly_NoReEncrypt verifies that updating only
// metadata (name) without an apiKey does NOT re-encrypt the ciphertext or bump
// the key version. The handler may still derive the KEK for read-only baseURL
// display decryption (which never writes ciphertext). This anchors the
// conditional-re-encryption contract in org_credentials.go.
func TestOrgCredentials_Update_NameOnly_NoReEncrypt(t *testing.T) {
	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	existingPlaintext, _ := json.Marshal(secrets.LLMProviderData{Provider: "openai", APIKey: "kept"}) //nolint:gosec
	existingCT, err := secrets.EncryptSecret(kek, existingPlaintext)
	require.NoError(t, err)
	store.creds["cred-1"] = &secrets.OrgCredentialRow{
		OrgCredentialMetadata: secrets.OrgCredentialMetadata{ID: "cred-1", OrgID: "org-1", Name: "old", Provider: "openai"},
		Ciphertext:            existingCT,
		KeyVersion:            3,
	}

	deriver := func(string) []byte { return kek }
	h := NewOrgCredentialsHandler(store, deriver, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	body := `{"name":"renamed"}`
	req, _ := http.NewRequest("PUT", "/api/v1/orgs/org-1/credentials/cred-1", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, store.updateCalls)
	require.Nil(t, store.lastUpdateCT, "no re-encryption (ciphertext write) when apiKey absent")
	require.Equal(t, "renamed", store.creds["cred-1"].Name)
	assert.Equal(t, 3, store.creds["cred-1"].KeyVersion, "key version must not change without re-encryption")
}

// TestOrgCredentials_Update_CorruptCiphertext_500 verifies that rotating the
// API key against a credential whose ciphertext is unreadable returns 500 (not
// 200 with a zeroed credential). Mirrors the admin-credential C-4 fix.
func TestOrgCredentials_Update_CorruptCiphertext_500(t *testing.T) {
	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 1)
	}
	// Ciphertext was encrypted with a DIFFERENT key — simulates DB corruption
	// or a KEK rotation that lost the old key.
	differentKEK := make([]byte, 32)
	corruptCT, err := secrets.EncryptSecret(differentKEK,
		[]byte(`{"provider":"openai","apiKey":"original"}`))
	require.NoError(t, err)
	store.creds["cred-1"] = &secrets.OrgCredentialRow{
		OrgCredentialMetadata: secrets.OrgCredentialMetadata{ID: "cred-1", OrgID: "org-1", Provider: "openai"},
		Ciphertext:            corruptCT,
		KeyVersion:            1,
	}

	h := NewOrgCredentialsHandler(store, func(string) []byte { return kek }, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	body := `{"apiKey":"rotated"}`
	req, _ := http.NewRequest("PUT", "/api/v1/orgs/org-1/credentials/cred-1", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, 0, store.updateCalls, "corrupt ciphertext must not be written back")
}

// --- ProbeModels (B-1) ---

// TestOrgCredentials_ProbeModels_NotFound verifies 404 for an unknown credID.
func TestOrgCredentials_ProbeModels_NotFound(t *testing.T) {
	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	h := NewOrgCredentialsHandler(store, func(string) []byte { return kek }, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	req, _ := http.NewRequest("GET", "/api/v1/orgs/org-1/credentials/missing/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestOrgCredentials_ProbeModels_NilKEK_503 verifies that probing when the
// server KEK is unavailable returns 503 (fail-closed — cannot decrypt).
func TestOrgCredentials_ProbeModels_NilKEK_503(t *testing.T) {
	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 1)
	}
	existingPlaintext, _ := json.Marshal(secrets.LLMProviderData{Provider: "openai", APIKey: "sk-x", BaseURL: "http://localhost:19998/v1"})
	existingCT, err := secrets.EncryptSecret(kek, existingPlaintext)
	require.NoError(t, err)
	store.creds["cred-1"] = &secrets.OrgCredentialRow{
		OrgCredentialMetadata: secrets.OrgCredentialMetadata{ID: "cred-1", OrgID: "org-1", Provider: "openai"},
		Ciphertext:            existingCT,
		KeyVersion:            1,
	}

	h := NewOrgCredentialsHandler(store, func(string) []byte { return nil }, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	req, _ := http.NewRequest("GET", "/api/v1/orgs/org-1/credentials/cred-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestOrgCredentials_ProbeModels_NoBaseURL verifies a graceful warning (200)
// when the credential has no baseURL (native provider).
func TestOrgCredentials_ProbeModels_NoBaseURL(t *testing.T) {
	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 2)
	}
	h := NewOrgCredentialsHandler(store, func(label string) []byte {
		assert.Equal(t, "org-credentials", label)
		return kek
	}, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	createBody := `{"name":"native","provider":"anthropic","apiKey":"sk-ant-123"}`
	req, _ := http.NewRequest("POST", "/api/v1/orgs/org-1/credentials", bytes.NewBufferString(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var created OrgCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	req, _ = http.NewRequest("GET", "/api/v1/orgs/org-1/credentials/"+created.ID+"/models", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var probe ProbeModelsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &probe))
	assert.NotEmpty(t, probe.Warning, "no-baseURL credential must return a warning")
	assert.Empty(t, probe.Models)
}

// TestOrgCredentials_ProbeModels_Success verifies that with a reachable fake
// provider, the probe returns the model list with saved context limits merged.
func TestOrgCredentials_ProbeModels_Success(t *testing.T) {
	fakeProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		assert.Equal(t, "Bearer sk-probe-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-5.1"},{"id":"glm-5.2"},{"id":"classifier"}]}`))
	}))
	defer fakeProvider.Close()

	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 4)
	}
	h := NewOrgCredentialsHandler(store, func(string) []byte { return kek }, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	createBody, _ := json.Marshal(map[string]interface{}{
		"name":               "thekao",
		"provider":           "thekao cloud",
		"apiKey":             "sk-probe-key",
		"baseURL":            fakeProvider.URL + "/v1",
		"modelAllowlist":     []string{"glm-5.1", "glm-5.2"},
		"modelContextLimits": map[string]int{"glm-5.1": 200000, "glm-5.2": 1000000},
	})
	req, _ := http.NewRequest("POST", "/api/v1/orgs/org-1/credentials", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var created OrgCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	req, _ = http.NewRequest("GET", "/api/v1/orgs/org-1/credentials/"+created.ID+"/models", nil)
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
	assert.Equal(t, 0, byID["classifier"].ContextLimit, "unsaved model has no limit")
}

// --- List (B-2): camelCase keys + baseURL extraction ---

// TestOrgCredentials_List_CamelCaseAndBaseURL verifies that the List response
// uses camelCase JSON keys (fixing the latent PascalCase serialization bug) and
// that baseURL is extracted from each credential's ciphertext via decryption.
func TestOrgCredentials_List_CamelCaseAndBaseURL(t *testing.T) {
	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 5)
	}

	// Seed two credentials: one with a baseURL, one without.
	for _, tc := range []struct {
		id, baseURL string
		limits      map[string]int
	}{
		{"cred-a", "https://api.example.com/v1", map[string]int{"glm-5.1": 200000}},
		{"cred-b", "", nil},
	} {
		pd := secrets.LLMProviderData{Provider: "custom", APIKey: "sk-" + tc.id, BaseURL: tc.baseURL}
		plain, _ := json.Marshal(pd)
		ct, err := secrets.EncryptSecret(kek, plain)
		require.NoError(t, err)
		store.creds[tc.id] = &secrets.OrgCredentialRow{
			OrgCredentialMetadata: secrets.OrgCredentialMetadata{
				ID: tc.id, OrgID: "org-1", Name: tc.id, Provider: "custom",
				ModelAllowlist: []string{}, ModelContextLimits: tc.limits,
			},
			Ciphertext: ct,
			KeyVersion: 1,
		}
	}

	h := NewOrgCredentialsHandler(store, func(label string) []byte {
		assert.Equal(t, "org-credentials", label)
		return kek
	}, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	req, _ := http.NewRequest("GET", "/api/v1/orgs/org-1/credentials", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// Unmarshal into a generic map to assert the raw JSON keys are camelCase
	// (not Go struct field names like "ModelAllowlist").
	var raw []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	require.Len(t, raw, 2)

	// Every entry must expose camelCase keys.
	keys := map[string]bool{}
	for _, entry := range raw {
		for k := range entry {
			keys[k] = true
		}
	}
	for _, want := range []string{"id", "orgId", "name", "provider", "baseURL", "modelAllowlist", "modelContextLimits", "createdAt", "updatedAt"} {
		assert.True(t, keys[want], "List JSON must include camelCase key %q (got %v)", want, keys)
	}
	for _, forbidden := range []string{"ID", "OrgID", "ModelAllowlist", "ModelContextLimits", "CreatedAt"} {
		assert.False(t, keys[forbidden], "List JSON must NOT include PascalCase key %q", forbidden)
	}

	// Typed decode to verify baseURL extraction per credential.
	var typed []OrgCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &typed))
	byID := map[string]OrgCredentialResponse{}
	for _, c := range typed {
		byID[c.ID] = c
	}
	assert.Equal(t, "https://api.example.com/v1", byID["cred-a"].BaseURL, "baseURL must be decrypted for cred-a")
	assert.Equal(t, "", byID["cred-b"].BaseURL, "cred-b has no baseURL")
	assert.Equal(t, 200000, byID["cred-a"].ModelContextLimits["glm-5.1"])
}

// TestOrgCredentials_List_Empty verifies the empty-list contract returns [] not null.
func TestOrgCredentials_List_Empty(t *testing.T) {
	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	h := NewOrgCredentialsHandler(store, func(string) []byte { return kek }, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	req, _ := http.NewRequest("GET", "/api/v1/orgs/org-1/credentials", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "[]", w.Body.String(), "empty list must serialize as []")
}

// --- Create / Update (B-3): full responses ---

// TestOrgCredentials_Create_FullResponse verifies that Create returns the full
// OrgCredentialResponse (not the old sparse {id,orgId,name,provider}), including
// modelAllowlist, modelContextLimits, baseURL, and timestamps.
func TestOrgCredentials_Create_FullResponse(t *testing.T) {
	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 6)
	}
	h := NewOrgCredentialsHandler(store, func(string) []byte { return kek }, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	createBody, _ := json.Marshal(map[string]interface{}{
		"name":               "thekao",
		"provider":           "thekao cloud",
		"apiKey":             "sk-x",
		"baseURL":            "https://api.example.com/v1",
		"modelAllowlist":     []string{"glm-5.1"},
		"modelContextLimits": map[string]int{"glm-5.1": 200000},
	})
	req, _ := http.NewRequest("POST", "/api/v1/orgs/org-1/credentials", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var resp OrgCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.ID)
	assert.Equal(t, "org-1", resp.OrgID)
	assert.Equal(t, "thekao", resp.Name)
	assert.Equal(t, "thekao cloud", resp.Provider)
	assert.Equal(t, "https://api.example.com/v1", resp.BaseURL, "Create response must echo baseURL")
	assert.Equal(t, []string{"glm-5.1"}, resp.ModelAllowlist)
	assert.Equal(t, 200000, resp.ModelContextLimits["glm-5.1"])
	assert.NotEmpty(t, resp.CreatedAt, "Create response must include createdAt")
	assert.NotEmpty(t, resp.UpdatedAt, "Create response must include updatedAt")
}

// TestOrgCredentials_Update_FullResponse verifies that Update returns the full
// OrgCredentialResponse (not the old sparse {id,message}) after a metadata-only
// update (no re-encryption).
func TestOrgCredentials_Update_FullResponse(t *testing.T) {
	store := newFakeOrgCredStore()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 7)
	}
	existingPlaintext, _ := json.Marshal(secrets.LLMProviderData{Provider: "openai", APIKey: "kept", BaseURL: "https://api.openai.com/v1"})
	existingCT, err := secrets.EncryptSecret(kek, existingPlaintext)
	require.NoError(t, err)
	store.creds["cred-1"] = &secrets.OrgCredentialRow{
		OrgCredentialMetadata: secrets.OrgCredentialMetadata{
			ID: "cred-1", OrgID: "org-1", Name: "old", Provider: "openai",
			ModelAllowlist: []string{}, ModelContextLimits: map[string]int{},
		},
		Ciphertext: existingCT,
		KeyVersion: 1,
	}

	h := NewOrgCredentialsHandler(store, func(string) []byte { return kek }, &mockOrgAuthService{userID: "admin-1"})
	router := setupOrgCredRouter(h)

	body := `{"name":"renamed","modelAllowlist":["gpt-4o"],"modelContextLimits":{"gpt-4o":128000}}`
	req, _ := http.NewRequest("PUT", "/api/v1/orgs/org-1/credentials/cred-1", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp OrgCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "cred-1", resp.ID)
	assert.Equal(t, "renamed", resp.Name)
	assert.Equal(t, "openai", resp.Provider)
	assert.Equal(t, "https://api.openai.com/v1", resp.BaseURL, "Update response must decrypt baseURL")
	assert.Equal(t, []string{"gpt-4o"}, resp.ModelAllowlist)
	assert.Equal(t, 128000, resp.ModelContextLimits["gpt-4o"])
}
