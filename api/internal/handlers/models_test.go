// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/pkg/types"
)

// --- Mocks ---

type mockWSUpdater struct {
	mu      sync.Mutex
	calls   []types.WorkspaceUpdates
	failErr error
	// WorkspaceOwnerChecker support
	ownerUserID string // if set, GetWorkspace returns a workspace owned by this user
}

func (m *mockWSUpdater) UpdateWorkspace(_ context.Context, _ string, updates types.WorkspaceUpdates) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, updates)
	return m.failErr
}

func (m *mockWSUpdater) GetWorkspace(_ context.Context, workspaceID string) (*types.WorkspaceMetadata, error) {
	if m.ownerUserID == "" {
		// Default: workspace owned by "user-1" (matches test auth middleware)
		return &types.WorkspaceMetadata{ID: workspaceID, UserID: "user-1"}, nil
	}
	return &types.WorkspaceMetadata{ID: workspaceID, UserID: m.ownerUserID}, nil
}

func (m *mockWSUpdater) GetDefaultModel(_ context.Context, _ string) (string, error) {
	return "", nil
}

// mockPasswordGetter returns a fixed password for any workspace.
func mockPasswordGetter(password string) func(context.Context, string) (string, error) {
	return func(_ context.Context, _ string) (string, error) {
		return password, nil
	}
}

// authEnforcingHandler returns an HTTP handler that rejects requests without valid Basic auth.
// This simulates real opencode behavior (Epic 27a A6).
func authEnforcingHandler(expectedPassword string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != expectedPassword {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		handler(w, r)
	}
}

// --- ListModels Tests ---

func TestListModels_HappyPath(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	// Mock opencode model endpoint on port 4096 — enforces Basic auth.
	const testPassword = "test-pw-456"
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	models := `[{"id":"anthropic/claude-sonnet-4-5","providerID":"anthropic","name":"Claude Sonnet 4.5","enabled":true}]`
	srv := httptest.NewUnstartedServer(authEnforcingHandler(testPassword, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/model", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(models))
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
	handler.SetPasswordGetter(mockPasswordGetter(testPassword))

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.GET("/api/v1/workspaces/:id/models", handler.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "claude-sonnet-4-5")
	require.Contains(t, w.Body.String(), `"tier"`)
	require.Contains(t, w.Body.String(), `"models"`)
}

func TestListModels_NoPodRunning(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: ""})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.GET("/api/v1/workspaces/:id/models", handler.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "pod not running")
}

func TestListModels_NoPodIPResolver(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewSecretsHandler(nil)
	// No resolver set

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.GET("/api/v1/workspaces/:id/models", handler.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestListModels_AgentUnreachable(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	handler := NewSecretsHandler(nil)
	// Resolver returns IP but nothing is listening on 4096
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.99"})
	handler.SetPasswordGetter(mockPasswordGetter("some-pw"))

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.GET("/api/v1/workspaces/:id/models", handler.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadGateway, w.Code)
}

func TestListModels_Unauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewSecretsHandler(nil)
	router := gin.New()
	// No userID in context
	router.GET("/api/v1/workspaces/:id/models", handler.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListModels_OwnershipDenied(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	// wsUpdater returns workspace owned by "other-user"
	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
	handler.SetWorkspaceMetadataUpdater(&mockWSUpdater{ownerUserID: "other-user"})
	handler.SetPasswordGetter(mockPasswordGetter("pw"))

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.GET("/api/v1/workspaces/:id/models", handler.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "access denied")
}

// --- SetModel Tests ---

func TestSetModel_HappyPath(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	updater := &mockWSUpdater{}
	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: ""}) // no pod needed — SetModel persists only
	handler.SetWorkspaceMetadataUpdater(updater)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.PUT("/api/v1/workspaces/:id/model", handler.SetModel)

	body, _ := json.Marshal(map[string]string{"model": "anthropic/claude-sonnet-4-5"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/model", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	require.Equal(t, "anthropic/claude-sonnet-4-5", resp["model"])
	// applied is always false — no live push (PATCH /global/config disposed all
	// instances, which aborts streams; removed per Epic 27a principles)
	require.Equal(t, false, resp["applied"])

	// Verify workspace metadata was updated.
	updater.mu.Lock()
	require.Len(t, updater.calls, 1)
	require.Equal(t, "anthropic/claude-sonnet-4-5", *updater.calls[0].DefaultModel)
	updater.mu.Unlock()
}

func TestSetModel_MissingModelField(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewSecretsHandler(nil)
	handler.SetWorkspaceMetadataUpdater(&mockWSUpdater{})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.PUT("/api/v1/workspaces/:id/model", handler.SetModel)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/model", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetModel_NoUpdater(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewSecretsHandler(nil)
	// No updater set

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.PUT("/api/v1/workspaces/:id/model", handler.SetModel)

	body, _ := json.Marshal(map[string]string{"model": "test/model"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/model", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestSetModel_NoPod_AppliedFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	updater := &mockWSUpdater{}
	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: ""}) // no pod
	handler.SetWorkspaceMetadataUpdater(updater)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.PUT("/api/v1/workspaces/:id/model", handler.SetModel)

	body, _ := json.Marshal(map[string]string{"model": "openai/gpt-4o"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/model", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	require.Equal(t, "openai/gpt-4o", resp["model"])
	require.Equal(t, false, resp["applied"]) // saved but not applied to live pod

	// Workspace metadata still updated.
	updater.mu.Lock()
	require.Len(t, updater.calls, 1)
	updater.mu.Unlock()
}

// TestSetModel_LivePush_SendsBasicAuth verifies that when a pod is running,
// SetModel sends the workspace password as Basic auth to PATCH /global/config.
// Without Basic auth opencode returns 401 and applied stays false.
func TestSetModel_LivePush_SendsBasicAuth(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)
	const testPassword = "ws-live-push-pw"

	// Mock opencode: only accepts authenticated PATCH /global/config (paid model).
	// For the model list (GET /api/model) return a paid model so relay baseURL is
	// not pushed (keeps the test focused on patchAgentModel auth).
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	srv := httptest.NewUnstartedServer(authEnforcingHandler(testPassword, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/global/config":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/api/model":
			// Return a paid model so relay baseURL logic is not triggered
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id":"anthropic/claude-paid","providerID":"anthropic","enabled":true,"cost":[{"input":3,"output":15}]}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	updater := &mockWSUpdater{ownerUserID: "user-1"}
	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
	handler.SetPasswordGetter(mockPasswordGetter(testPassword))
	handler.SetWorkspaceMetadataUpdater(updater)

	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("userID", "user-1"); c.Next() })
	router.PUT("/api/v1/workspaces/:id/model", handler.SetModel)

	body, _ := json.Marshal(map[string]string{"model": "anthropic/claude-paid"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/model", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "anthropic/claude-paid", resp["model"])
	require.Equal(t, true, resp["applied"], "applied must be true when PATCH /global/config succeeds with Basic auth")
}

// TestSetModel_FreeTierModel_PushesRelayBaseURL verifies that selecting a free-tier
// model causes pushRelayBaseURL to be called (PUT /auth/opencode with Basic auth).
// isFreeTierModel also requires Basic auth — without it, it gets 401 and returns
// false, causing clearRelayBaseURL instead.
func TestSetModel_FreeTierModel_PushesRelayBaseURL(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)
	const testPassword = "relay-push-pw"

	var relayBaseURLPushed string
	var instanceDisposed bool
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	srv := httptest.NewUnstartedServer(authEnforcingHandler(testPassword, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/global/config":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/api/model":
			// Return a free opencode model
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id":"opencode/free-model","providerID":"opencode","enabled":true,"cost":[{"input":0,"output":0}]}]`))
		case r.Method == http.MethodPut && r.URL.Path == "/auth/opencode":
			// Capture what baseURL was pushed
			var body struct {
				Metadata map[string]string `json:"metadata"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				relayBaseURLPushed = body.Metadata["baseURL"]
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/instance/dispose":
			// Must be called after PUT /auth/opencode for new baseURL to take effect
			instanceDisposed = true
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	updater := &mockWSUpdater{ownerUserID: "user-1"}
	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
	handler.SetPasswordGetter(mockPasswordGetter(testPassword))
	handler.SetWorkspaceMetadataUpdater(updater)

	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("userID", "user-1"); c.Next() })
	router.PUT("/api/v1/workspaces/:id/model", handler.SetModel)

	body, _ := json.Marshal(map[string]string{"model": "opencode/free-model"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/model", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, true, resp["applied"], "applied must be true when PATCH /global/config succeeds")
	require.NotEmpty(t, relayBaseURLPushed, "relay baseURL must be pushed to opencode for free-tier model")
	require.True(t, instanceDisposed, "POST /instance/dispose must be called after PUT /auth/opencode to activate new baseURL")
}

func TestSetModel_Unauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewSecretsHandler(nil)
	handler.SetWorkspaceMetadataUpdater(&mockWSUpdater{})

	router := gin.New()
	// No userID
	router.PUT("/api/v1/workspaces/:id/model", handler.SetModel)

	body, _ := json.Marshal(map[string]string{"model": "test/m"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/model", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestSetModel_OwnershipDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// wsUpdater returns a workspace owned by "other-user" — not "user-1"
	updater := &mockWSUpdater{ownerUserID: "other-user"}
	handler := NewSecretsHandler(nil)
	handler.SetWorkspaceMetadataUpdater(updater)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
	handler.SetPasswordGetter(mockPasswordGetter("pw"))

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.PUT("/api/v1/workspaces/:id/model", handler.SetModel)

	body, _ := json.Marshal(map[string]string{"model": "test/model"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/model", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "access denied")
}

func TestListModels_NoPasswordGetter_Returns503(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
	// No SetPasswordGetter — should fail gracefully

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.GET("/api/v1/workspaces/:id/models", handler.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "password getter")
}

func TestListModels_WrongPassword_Returns502(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	const correctPassword = "real-pw"
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	// Mock opencode that enforces auth
	srv := httptest.NewUnstartedServer(authEnforcingHandler(correctPassword, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
	handler.SetPasswordGetter(mockPasswordGetter("wrong-pw")) // wrong password

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.GET("/api/v1/workspaces/:id/models", handler.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// opencode returns 401 → handler passes through as error
	require.NotEqual(t, http.StatusOK, w.Code)
}

// Ensure unused import doesn't break compilation.

// --- Tier Annotation Tests ---

func TestClassifyTier_OpencodeProviderZeroCost(t *testing.T) {
	tier := classifyTier("opencode", []opencodeCost{{Input: 0, Output: 0}})
	require.Equal(t, "free", tier)
}

func TestClassifyTier_OpencodeProviderNoCostEntries(t *testing.T) {
	tier := classifyTier("opencode", []opencodeCost{})
	require.Equal(t, "free", tier)
}

func TestClassifyTier_OpencodeProviderNilCost(t *testing.T) {
	tier := classifyTier("opencode", nil)
	require.Equal(t, "free", tier)
}

func TestClassifyTier_OpencodeProviderPaidCost(t *testing.T) {
	tier := classifyTier("opencode", []opencodeCost{{Input: 3.0, Output: 15.0}})
	require.Equal(t, "paid", tier)
}

func TestClassifyTier_OpencodeProviderMixedCost(t *testing.T) {
	// Multiple cost tiers — one is free, one is paid. Should be paid.
	tier := classifyTier("opencode", []opencodeCost{{Input: 0, Output: 0}, {Input: 5.0, Output: 10.0}})
	require.Equal(t, "paid", tier)
}

func TestClassifyTier_NonOpencodeProvider(t *testing.T) {
	tier := classifyTier("anthropic", []opencodeCost{{Input: 0, Output: 0}})
	require.Equal(t, "paid", tier)
}

func TestClassifyTier_NonOpencodeProviderNoCost(t *testing.T) {
	tier := classifyTier("openai", nil)
	require.Equal(t, "paid", tier)
}

func TestAnnotateModels_FullResponse(t *testing.T) {
	raw := `[
		{"id":"opencode/free-model","providerID":"opencode","name":"Free Model","enabled":true,"cost":[{"input":0,"output":0,"cache":{"read":0,"write":0}}],"status":"active"},
		{"id":"anthropic/claude-sonnet-4-5","providerID":"anthropic","name":"Claude Sonnet 4.5","enabled":true,"cost":[{"input":3,"output":15,"cache":{"read":0.3,"write":3.75}}],"status":"active"},
		{"id":"opencode/paid-model","providerID":"opencode","name":"Paid OpenCode","enabled":true,"cost":[{"input":1,"output":2,"cache":{"read":0,"write":0}}],"status":"active"}
	]`

	result, err := annotateModels([]byte(raw))
	require.NoError(t, err)
	require.Len(t, result, 3)

	require.Equal(t, "free", result[0].Tier)
	require.True(t, result[0].FreeTier)
	require.True(t, result[0].ProxyRequired, "free-tier model must have proxyRequired=true")
	require.Equal(t, "opencode/free-model", result[0].ID)

	require.Equal(t, "paid", result[1].Tier)
	require.False(t, result[1].FreeTier)
	require.False(t, result[1].ProxyRequired, "paid model must have proxyRequired=false")

	require.Equal(t, "paid", result[2].Tier) // opencode provider but has cost > 0
	require.False(t, result[2].ProxyRequired, "paid model must have proxyRequired=false")
}

func TestAnnotateModels_InvalidJSON(t *testing.T) {
	_, err := annotateModels([]byte("not json"))
	require.Error(t, err)
}

func TestAnnotateModels_EmptyArray(t *testing.T) {
	result, err := annotateModels([]byte("[]"))
	require.NoError(t, err)
	require.Len(t, result, 0)
}

func TestAnnotateModels_PreservesDetails(t *testing.T) {
	raw := `[{"id":"test/model","providerID":"anthropic","name":"Test","enabled":true,"cost":[{"input":3,"output":15,"cache":{"read":0.3,"write":3.75}}],"status":"active","capabilities":{"tools":true,"input":["text","image"],"output":["text"]},"limit":{"context":200000,"output":8192}}]`

	result, err := annotateModels([]byte(raw))
	require.NoError(t, err)
	require.Len(t, result, 1)

	// Details should contain the full original object (including capabilities, limit, etc.)
	require.NotNil(t, result[0].Details)
	var details map[string]interface{}
	require.NoError(t, json.Unmarshal(result[0].Details, &details))
	require.Contains(t, details, "capabilities")
	require.Contains(t, details, "limit")

	caps := details["capabilities"].(map[string]interface{})
	require.Equal(t, true, caps["tools"])
}

func TestListModels_ResponseAnnotated(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	const testPassword = "annotated-pw"
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	models := `[{"id":"opencode/test","providerID":"opencode","name":"Test","enabled":true,"cost":[{"input":0,"output":0,"cache":{"read":0,"write":0}}],"status":"active"}]`
	srv := httptest.NewUnstartedServer(authEnforcingHandler(testPassword, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(models))
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
	handler.SetPasswordGetter(mockPasswordGetter(testPassword))

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.GET("/api/v1/workspaces/:id/models", handler.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Models       []annotatedModel `json:"models"`
		CurrentModel string           `json:"currentModel"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Models, 1)
	require.Equal(t, "free", resp.Models[0].Tier)
	require.True(t, resp.Models[0].FreeTier)
	require.True(t, resp.Models[0].ProxyRequired, "free-tier models must have proxyRequired=true (Epic 26)")
	require.Equal(t, "opencode/test", resp.Models[0].ID)
	require.Equal(t, "", resp.CurrentModel) // no updater set = empty
}

// mockModelReader implements WorkspaceDefaultModelReader for testing.
type mockModelReader struct {
	model string
}

func (m *mockModelReader) UpdateWorkspace(_ context.Context, _ string, _ types.WorkspaceUpdates) error {
	return nil
}

func (m *mockModelReader) GetDefaultModel(_ context.Context, _ string) (string, error) {
	return m.model, nil
}

func (m *mockModelReader) GetWorkspace(_ context.Context, workspaceID string) (*types.WorkspaceMetadata, error) {
	return &types.WorkspaceMetadata{ID: workspaceID, UserID: "user-1"}, nil
}

func TestListModels_IncludesCurrentModel(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	const testPassword = "currentmodel-pw"
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	models := `[{"id":"anthropic/claude-sonnet-4-5","providerID":"anthropic","name":"Claude","enabled":true,"cost":[{"input":3,"output":15,"cache":{"read":0,"write":0}}]}]`
	srv := httptest.NewUnstartedServer(authEnforcingHandler(testPassword, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(models))
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
	handler.SetPasswordGetter(mockPasswordGetter(testPassword))
	handler.SetWorkspaceMetadataUpdater(&mockModelReader{model: "anthropic/claude-sonnet-4-5"})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.GET("/api/v1/workspaces/:id/models", handler.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Models       []annotatedModel `json:"models"`
		CurrentModel string           `json:"currentModel"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "anthropic/claude-sonnet-4-5", resp.CurrentModel)
	require.Len(t, resp.Models, 1)
	require.Equal(t, "paid", resp.Models[0].Tier)
}

func TestListModels_FiltersPaidOpencodeModels(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	const testPassword = "filter-pw"
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	// Mix of models: free opencode, paid opencode, non-opencode enabled
	models := `[
		{"id":"opencode/free-model","providerID":"opencode","name":"Free","enabled":true,"cost":[{"input":0,"output":0}]},
		{"id":"opencode/paid-model","providerID":"opencode","name":"Paid","enabled":true,"cost":[{"input":3,"output":15}]},
		{"id":"anthropic/claude","providerID":"anthropic","name":"Claude","enabled":true,"cost":[{"input":3,"output":15}]},
		{"id":"openai/disabled","providerID":"openai","name":"Disabled","enabled":false,"cost":[{"input":1,"output":2}]}
	]`
	srv := httptest.NewUnstartedServer(authEnforcingHandler(testPassword, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(models))
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
	handler.SetPasswordGetter(mockPasswordGetter(testPassword))
	handler.SetWorkspaceMetadataUpdater(&mockWSUpdater{})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", "user-1")
		c.Next()
	})
	router.GET("/api/v1/workspaces/:id/models", handler.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Models []struct {
			ID       string `json:"id"`
			FreeTier bool   `json:"freeTier"`
		} `json:"models"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Should contain: free opencode + anthropic (enabled, non-opencode)
	// Should NOT contain: paid opencode + disabled openai
	ids := make([]string, len(resp.Models))
	for i, m := range resp.Models {
		ids[i] = m.ID
	}
	require.Contains(t, ids, "opencode/free-model")
	require.Contains(t, ids, "anthropic/claude")
	require.NotContains(t, ids, "opencode/paid-model")
	require.NotContains(t, ids, "openai/disabled")
	require.Len(t, resp.Models, 2)
}
