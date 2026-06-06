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
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeUserCredStore struct {
	creds    map[string]*secrets.UserCredentialRow
	bindings map[string][]string // credID -> []wsID
	nextErr  error
}

func newFakeUserCredStore() *fakeUserCredStore {
	return &fakeUserCredStore{creds: make(map[string]*secrets.UserCredentialRow), bindings: make(map[string][]string)}
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

func (f *fakeUserCredStore) BindCredentialToAllUserWorkspaces(_ context.Context, credID, _ string) error {
	// No-op in tests — workspace auto-bind is covered by dedicated integration tests.
	_ = credID
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
	store.nextErr = errors.New("ERROR: duplicate key value violates unique constraint (SQLSTATE 23505)")
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
