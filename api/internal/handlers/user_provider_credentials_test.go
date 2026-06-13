// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeUserCredStore struct {
	creds      map[string]*secrets.UserCredentialRow
	bindings   map[string][]string // credID -> []wsID
	autoBinds  map[string]bool     // wsID -> true if auto-bound (for protection test)
	nextErr    error
	bindAllErr error
}

func newFakeUserCredStore() *fakeUserCredStore {
	return &fakeUserCredStore{
		creds:     make(map[string]*secrets.UserCredentialRow),
		bindings:  make(map[string][]string),
		autoBinds: make(map[string]bool),
	}
}

func (f *fakeUserCredStore) CreateUserCredential(_ context.Context, row *secrets.UserCredentialRow) error {
	if f.nextErr != nil {
		err := f.nextErr
		f.nextErr = nil
		return err
	}
	f.creds[row.ID] = row
	return nil
}

func (f *fakeUserCredStore) ListUserCredentials(_ context.Context, userID string) ([]*secrets.UserCredentialRow, error) {
	var out []*secrets.UserCredentialRow
	for _, c := range f.creds {
		if c.OwnerID == userID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeUserCredStore) GetUserCredential(_ context.Context, userID, id string) (*secrets.UserCredentialRow, error) {
	c, ok := f.creds[id]
	if !ok || c.OwnerID != userID {
		return nil, nil
	}
	return c, nil
}

func (f *fakeUserCredStore) DeleteUserCredential(_ context.Context, userID, id string) error {
	c, ok := f.creds[id]
	if !ok || c.OwnerID != userID {
		return nil
	}
	delete(f.creds, id)
	return nil
}

func (f *fakeUserCredStore) BindCredentialToWorkspace(_ context.Context, credID, wsID string) error {
	f.bindings[credID] = append(f.bindings[credID], wsID)
	return nil
}

func (f *fakeUserCredStore) UnbindCredentialFromWorkspace(_ context.Context, credID, wsID string) error {
	if f.autoBinds[wsID] {
		return secrets.ErrAutoBindingProtected
	}
	orig := f.bindings[credID]
	filtered := orig[:0]
	for _, id := range orig {
		if id != wsID {
			filtered = append(filtered, id)
		}
	}
	f.bindings[credID] = filtered
	return nil
}

func (f *fakeUserCredStore) GetCredentialBindings(_ context.Context, credID, _ string) ([]string, error) {
	ids := f.bindings[credID]
	if ids == nil {
		return []string{}, nil
	}
	return ids, nil
}

func (f *fakeUserCredStore) GetCredentialBindingsWithSource(_ context.Context, credID, _ string) ([]secrets.CredentialBindingInfo, error) {
	ids := f.bindings[credID]
	out := make([]secrets.CredentialBindingInfo, len(ids))
	for i, id := range ids {
		sourceType := "explicit"
		if f.autoBinds[id] {
			sourceType = "auto"
		}
		out[i] = secrets.CredentialBindingInfo{WorkspaceID: id, SourceType: sourceType}
	}
	return out, nil
}

func (f *fakeUserCredStore) BindCredentialToAllUserWorkspaces(_ context.Context, credID, _ string) error {
	_ = credID
	if f.bindAllErr != nil {
		return f.bindAllErr
	}
	return nil
}

type fakeKeyStore struct {
	version int
}

func (f *fakeKeyStore) GetUserKey(_ context.Context, _ string) (*secrets.UserKeyRecord, error) {
	return &secrets.UserKeyRecord{KeyVersion: f.version}, nil
}
func (f *fakeKeyStore) CreateUserKey(_ context.Context, _ *secrets.UserKeyRecord) error { return nil }
func (f *fakeKeyStore) UpdateWrappedDEK(_ context.Context, _ string, _ []byte, _ []byte, _ int) error {
	return nil
}
func (f *fakeKeyStore) UpdateWrappedDEKRecovery(_ context.Context, _ string, _ []byte, _ []byte) error {
	return nil
}

func setupUserCredRouter(h *UserProviderCredentialsHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Set("sessionID", "sess-1")
		c.Next()
	})
	g := r.Group("/api/v1/provider-credentials")
	g.POST("", h.Create)
	g.GET("", h.List)
	g.GET("/:id", h.Get)
	g.DELETE("/:id", h.Delete)
	g.GET("/:id/bindings", h.ListBindings)
	g.POST("/:id/bind/:workspaceId", h.Bind)
	g.DELETE("/:id/bind/:workspaceId", h.Unbind)
	return r
}

// mockCredStateWriter captures MarkCredentialChanged calls for testing.
type mockCredStateWriter struct {
	fn func(ctx context.Context, wsID string) error
}

func (m *mockCredStateWriter) MarkCredentialChanged(ctx context.Context, wsID string) error {
	if m.fn != nil {
		return m.fn(ctx, wsID)
	}
	return nil
}

func TestUserProviderCredentials_Create_Success(t *testing.T) {
	store := newFakeUserCredStore()
	dek := make([]byte, 32)
	keys := secrets.NewKeyService(nil, nil) // won't be used directly
	h := &UserProviderCredentialsHandler{
		store:    store,
		keys:     keys,
		keyStore: &fakeKeyStore{version: 1},
	}
	// Override keys to use our fake DEK getter — inject via a patched KeyService isn't easy,
	// so we test through the handler by mocking at the store level.
	// Actually, the handler calls h.keys.GetDEK which needs a real KeyService.
	// Let's test the full handler with a working KeyService + DEK cache.
	dekCache := &testDEKCacheForHandler{}
	dekCache.cache = map[string][]byte{"sess-1": dek}
	keyService := secrets.NewKeyService(&fakeKeyStore{version: 1}, dekCache)
	h.keys = keyService
	h.keyStore = &fakeKeyStore{version: 1}

	router := setupUserCredRouter(h)

	body := `{"name":"my-anthropic","provider":"anthropic","apiKey":"sk-ant-123"}`
	req, _ := http.NewRequest("POST", "/api/v1/provider-credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp AdminCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "my-anthropic", resp.Name)
	assert.Equal(t, "anthropic", resp.Provider)
}

func TestUserProviderCredentials_Create_MissingAPIKey(t *testing.T) {
	store := newFakeUserCredStore()
	h := &UserProviderCredentialsHandler{store: store}
	router := setupUserCredRouter(h)

	body := `{"name":"my-anthropic","provider":"anthropic"}`
	req, _ := http.NewRequest("POST", "/api/v1/provider-credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUserProviderCredentials_Create_EmptyProvider(t *testing.T) {
	store := newFakeUserCredStore()
	dek := make([]byte, 32)
	dekCache := &testDEKCacheForHandler{cache: map[string][]byte{"sess-1": dek}}
	h := &UserProviderCredentialsHandler{
		store:    store,
		keys:     secrets.NewKeyService(&fakeKeyStore{version: 1}, dekCache),
		keyStore: &fakeKeyStore{version: 1},
	}
	router := setupUserCredRouter(h)

	body := `{"name":"test","provider":"  ","apiKey":"key"}`
	req, _ := http.NewRequest("POST", "/api/v1/provider-credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUserProviderCredentials_Create_Duplicate(t *testing.T) {
	store := newFakeUserCredStore()
	store.nextErr = &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
	dek := make([]byte, 32)
	dekCache := &testDEKCacheForHandler{cache: map[string][]byte{"sess-1": dek}}
	h := &UserProviderCredentialsHandler{
		store:    store,
		keys:     secrets.NewKeyService(&fakeKeyStore{version: 1}, dekCache),
		keyStore: &fakeKeyStore{version: 1},
	}
	router := setupUserCredRouter(h)

	body := `{"name":"test","provider":"anthropic","apiKey":"key"}`
	req, _ := http.NewRequest("POST", "/api/v1/provider-credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestUserProviderCredentials_List(t *testing.T) {
	store := newFakeUserCredStore()
	store.creds["c1"] = &secrets.UserCredentialRow{ID: "c1", OwnerID: "user-1", Name: "test", Provider: "openai", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	h := &UserProviderCredentialsHandler{store: store}
	router := setupUserCredRouter(h)

	req, _ := http.NewRequest("GET", "/api/v1/provider-credentials", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var list []AdminCredentialResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	assert.Len(t, list, 1)
}

func TestUserProviderCredentials_Get_NotFound(t *testing.T) {
	store := newFakeUserCredStore()
	h := &UserProviderCredentialsHandler{store: store}
	router := setupUserCredRouter(h)

	req, _ := http.NewRequest("GET", "/api/v1/provider-credentials/nonexistent", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUserProviderCredentials_Get_WrongOwner(t *testing.T) {
	store := newFakeUserCredStore()
	store.creds["c1"] = &secrets.UserCredentialRow{ID: "c1", OwnerID: "other-user", Name: "test", Provider: "openai"}
	h := &UserProviderCredentialsHandler{store: store}
	router := setupUserCredRouter(h)

	req, _ := http.NewRequest("GET", "/api/v1/provider-credentials/c1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUserProviderCredentials_Bind_OwnershipCheck(t *testing.T) {
	store := newFakeUserCredStore()
	store.creds["c1"] = &secrets.UserCredentialRow{ID: "c1", OwnerID: "user-1", Name: "test", Provider: "openai"}
	h := &UserProviderCredentialsHandler{
		store: store,
		wsOwnerCheck: func(_ context.Context, _, _ string) error {
			return errors.New("not owned")
		},
	}
	router := setupUserCredRouter(h)

	req, _ := http.NewRequest("POST", "/api/v1/provider-credentials/c1/bind/ws-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUserProviderCredentials_Bind_CredentialNotOwned(t *testing.T) {
	store := newFakeUserCredStore()
	store.creds["c1"] = &secrets.UserCredentialRow{ID: "c1", OwnerID: "other-user", Name: "test", Provider: "openai"}
	h := &UserProviderCredentialsHandler{store: store}
	router := setupUserCredRouter(h)

	req, _ := http.NewRequest("POST", "/api/v1/provider-credentials/c1/bind/ws-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUserProviderCredentials_Bind_Success(t *testing.T) {
	store := newFakeUserCredStore()
	store.creds["c1"] = &secrets.UserCredentialRow{ID: "c1", OwnerID: "user-1", Name: "test", Provider: "openai"}
	h := &UserProviderCredentialsHandler{
		store: store,
		wsOwnerCheck: func(_ context.Context, _, _ string) error {
			return nil // owned
		},
	}
	router := setupUserCredRouter(h)

	req, _ := http.NewRequest("POST", "/api/v1/provider-credentials/c1/bind/ws-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, store.bindings["c1"], "ws-1")
}

func TestUserProviderCredentials_Delete(t *testing.T) {
	store := newFakeUserCredStore()
	store.creds["c1"] = &secrets.UserCredentialRow{ID: "c1", OwnerID: "user-1", Name: "test", Provider: "openai"}
	h := &UserProviderCredentialsHandler{store: store}
	router := setupUserCredRouter(h)

	req, _ := http.NewRequest("DELETE", "/api/v1/provider-credentials/c1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, store.creds)
}

func TestUserProviderCredentials_ListBindings_ReturnsWorkspaceIDs(t *testing.T) {
	store := newFakeUserCredStore()
	store.creds["c1"] = &secrets.UserCredentialRow{ID: "c1", OwnerID: "user-1", Name: "test", Provider: "openai"}
	store.bindings["c1"] = []string{"ws-1", "ws-2"}
	h := &UserProviderCredentialsHandler{store: store}
	router := setupUserCredRouter(h)

	req, _ := http.NewRequest("GET", "/api/v1/provider-credentials/c1/bindings", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		WorkspaceIds []string `json:"workspaceIds"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.ElementsMatch(t, []string{"ws-1", "ws-2"}, resp.WorkspaceIds)
}

// TestUserProviderCredentials_ListBindings_JSONShape_CamelCase is a regression
// test for the CredentialBindingInfo PascalCase serialization bug.
// CredentialBindingInfo had no json struct tags, causing encoding/json to emit
// WorkspaceID/SourceType (PascalCase) instead of workspaceId/sourceType.
// The frontend TypeScript type expects camelCase; PascalCase caused the binding
// panel to show every workspace as "Bind" regardless of actual binding state.
func TestUserProviderCredentials_ListBindings_JSONShape_CamelCase(t *testing.T) {
	store := newFakeUserCredStore()
	store.creds["c1"] = &secrets.UserCredentialRow{ID: "c1", OwnerID: "user-1", Name: "test", Provider: "openai"}
	store.bindings["c1"] = []string{"ws-explicit"}
	// ws-auto is in autoBinds so GetCredentialBindingsWithSource returns sourceType="auto"
	store.autoBinds["ws-auto"] = true
	store.bindings["c1"] = append(store.bindings["c1"], "ws-auto")
	h := &UserProviderCredentialsHandler{store: store}
	router := setupUserCredRouter(h)

	req, _ := http.NewRequest("GET", "/api/v1/provider-credentials/c1/bindings", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Parse as raw map to verify exact JSON key names.
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))

	// Flat array key must be camelCase "workspaceIds", not "WorkspaceIds".
	assert.Contains(t, raw, "workspaceIds", "flat array key must be camelCase workspaceIds")
	assert.NotContains(t, raw, "WorkspaceIds", "PascalCase WorkspaceIds must not appear in response")

	// Bindings array key must be "bindings".
	assert.Contains(t, raw, "bindings", "bindings key must be present")

	// Each binding object must use camelCase keys.
	var bindings []json.RawMessage
	require.NoError(t, json.Unmarshal(raw["bindings"], &bindings))
	require.NotEmpty(t, bindings)

	for _, b := range bindings {
		var bindingMap map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(b, &bindingMap))
		assert.Contains(t, bindingMap, "workspaceId", "binding key must be camelCase workspaceId")
		assert.Contains(t, bindingMap, "sourceType", "binding key must be camelCase sourceType")
		assert.NotContains(t, bindingMap, "WorkspaceID", "PascalCase WorkspaceID must not appear")
		assert.NotContains(t, bindingMap, "SourceType", "PascalCase SourceType must not appear")
	}

	// Verify sourceType values are correct.
	bindingByWs := map[string]string{}
	for _, b := range bindings {
		var bindingObj struct {
			WorkspaceId string `json:"workspaceId"`
			SourceType  string `json:"sourceType"`
		}
		require.NoError(t, json.Unmarshal(b, &bindingObj))
		bindingByWs[bindingObj.WorkspaceId] = bindingObj.SourceType
	}
	assert.Equal(t, "explicit", bindingByWs["ws-explicit"])
	assert.Equal(t, "auto", bindingByWs["ws-auto"])
}

func TestUserProviderCredentials_ListBindings_EmptyWhenNoneBound(t *testing.T) {
	store := newFakeUserCredStore()
	store.creds["c1"] = &secrets.UserCredentialRow{ID: "c1", OwnerID: "user-1", Name: "test", Provider: "openai"}
	h := &UserProviderCredentialsHandler{store: store}
	router := setupUserCredRouter(h)

	req, _ := http.NewRequest("GET", "/api/v1/provider-credentials/c1/bindings", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		WorkspaceIds []string `json:"workspaceIds"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.WorkspaceIds)
}

func TestUserProviderCredentials_ListBindings_NotFoundForWrongOwner(t *testing.T) {
	store := newFakeUserCredStore()
	store.creds["c1"] = &secrets.UserCredentialRow{ID: "c1", OwnerID: "other-user", Name: "test", Provider: "openai"}
	h := &UserProviderCredentialsHandler{store: store}
	router := setupUserCredRouter(h)

	req, _ := http.NewRequest("GET", "/api/v1/provider-credentials/c1/bindings", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUserProviderCredentials_Unbind_RemovesBinding(t *testing.T) {
	store := newFakeUserCredStore()
	store.creds["c1"] = &secrets.UserCredentialRow{ID: "c1", OwnerID: "user-1", Name: "test", Provider: "openai"}
	store.bindings["c1"] = []string{"ws-1", "ws-2"}
	h := &UserProviderCredentialsHandler{
		store:        store,
		wsOwnerCheck: func(_ context.Context, _, _ string) error { return nil },
	}
	router := setupUserCredRouter(h)

	req, _ := http.NewRequest("DELETE", "/api/v1/provider-credentials/c1/bind/ws-1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.NotContains(t, store.bindings["c1"], "ws-1")
	assert.Contains(t, store.bindings["c1"], "ws-2")
}

// TestUserProviderCredentials_Unbind_RejectsAutoBinding verifies H-1 fix:
// auto-bindings (seeded by SeedWorkspaceCredentials) return 409, not 204.
func TestUserProviderCredentials_Unbind_RejectsAutoBinding(t *testing.T) {
	store := newFakeUserCredStore()
	store.creds["c1"] = &secrets.UserCredentialRow{ID: "c1", OwnerID: "user-1", Name: "test", Provider: "openai"}
	store.bindings["c1"] = []string{"ws-auto"}
	store.autoBinds["ws-auto"] = true // simulate auto-bound
	h := &UserProviderCredentialsHandler{
		store:        store,
		wsOwnerCheck: func(_ context.Context, _, _ string) error { return nil },
	}
	router := setupUserCredRouter(h)

	req, _ := http.NewRequest("DELETE", "/api/v1/provider-credentials/c1/bind/ws-auto", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
}

// TestUserProviderCredentials_Delete_NotifiesBoundWorkspaces verifies C-3 fix:
// deleting a credential marks all previously-bound workspaces as credential-changed.
func TestUserProviderCredentials_Delete_NotifiesBoundWorkspaces(t *testing.T) {
	store := newFakeUserCredStore()
	store.creds["c1"] = &secrets.UserCredentialRow{ID: "c1", OwnerID: "user-1", Name: "test", Provider: "openai"}
	store.bindings["c1"] = []string{"ws-1", "ws-2"}

	notified := make(map[string]bool)
	h := &UserProviderCredentialsHandler{
		store: store,
		credStateWriter: &mockCredStateWriter{fn: func(ctx context.Context, wsID string) error {
			notified[wsID] = true
			return nil
		}},
	}
	router := setupUserCredRouter(h)

	req, _ := http.NewRequest("DELETE", "/api/v1/provider-credentials/c1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.True(t, notified["ws-1"], "ws-1 should be notified")
	assert.True(t, notified["ws-2"], "ws-2 should be notified")
}

// TestUserProviderCredentials_Create_Returns207OnBindFailure verifies C-2 fix:
// if BindCredentialToAllUserWorkspaces fails, Create returns 207 not 201.
func TestUserProviderCredentials_Create_Returns207OnBindFailure(t *testing.T) {
	store := newFakeUserCredStore()
	store.bindAllErr = errors.New("db timeout")
	dek := make([]byte, 32)
	dekCache := &testDEKCacheForHandler{cache: map[string][]byte{"sess-1": dek}}
	h := &UserProviderCredentialsHandler{
		store:    store,
		keys:     secrets.NewKeyService(&fakeKeyStore{version: 1}, dekCache),
		keyStore: &fakeKeyStore{version: 1},
	}
	router := setupUserCredRouter(h)

	body := `{"name":"my-openai","provider":"openai","apiKey":"sk-test"}`
	req, _ := http.NewRequest("POST", "/api/v1/provider-credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusMultiStatus, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "bindWarning")
	assert.Contains(t, resp, "credential")
}

// testDEKCacheForHandler is a minimal DEKCache for handler tests.
type testDEKCacheForHandler struct {
	cache map[string][]byte
}

func (c *testDEKCacheForHandler) CacheDEK(_ context.Context, sessionID string, dek []byte, _ time.Duration) error {
	if c.cache == nil {
		c.cache = make(map[string][]byte)
	}
	c.cache[sessionID] = dek
	return nil
}

func (c *testDEKCacheForHandler) GetDEK(_ context.Context, sessionID string) ([]byte, error) {
	dek, ok := c.cache[sessionID]
	if !ok {
		return nil, secrets.ErrDEKUnavailable
	}
	return dek, nil
}

func (c *testDEKCacheForHandler) EvictDEK(_ context.Context, _ string) error { return nil }
