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
	"github.com/stretchr/testify/assert"
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

	// Mock opencode /provider endpoint on port 4096 — enforces Basic auth.
	const testPassword = "test-pw-456"
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	providerResp := `{"connected":["anthropic"],"all":[{"id":"anthropic","models":{"claude-sonnet-4-5":{"id":"claude-sonnet-4-5","name":"Claude Sonnet 4.5","cost":{"input":3,"output":15}}}}]}`
	srv := httptest.NewUnstartedServer(authEnforcingHandler(testPassword, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/provider", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(providerResp))
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
		case r.Method == http.MethodGet && r.URL.Path == "/provider":
			// Return a paid anthropic model so relay remap is not triggered
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"connected":["anthropic"],"all":[{"id":"anthropic","models":{"claude-paid":{"id":"claude-paid","name":"Claude Paid","cost":{"input":3,"output":15}}}}]}`))
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

	body, _ := json.Marshal(map[string]string{"model": "claude-paid"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/model", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "claude-paid", resp["model"])
	require.Equal(t, true, resp["applied"], "applied must be true when PATCH /global/config succeeds with Basic auth")
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

// --- Availability Classification Tests ---

func TestClassifyAvailability_OpencodeZeroCost(t *testing.T) {
	avail := classifyAvailability("opencode", providerCost{Input: 0, Output: 0})
	require.Equal(t, ModelFreeTier, avail)
}

func TestClassifyAvailability_OpencodeNoCostEntries(t *testing.T) {
	// zero-value providerCost = input:0, output:0 → free tier
	avail := classifyAvailability("opencode", providerCost{})
	require.Equal(t, ModelFreeTier, avail)
}

func TestClassifyAvailability_OpencodeNilCost(t *testing.T) {
	// zero-value is indistinguishable from "no cost data" → still free tier
	avail := classifyAvailability("opencode", providerCost{})
	require.Equal(t, ModelFreeTier, avail)
}

func TestClassifyAvailability_OpencodePaidCost(t *testing.T) {
	avail := classifyAvailability("opencode", providerCost{Input: 3.0, Output: 15.0})
	require.Equal(t, ModelAvailable, avail)
}

// TestClassifyAvailability_OpencodeRelayZeroCost verifies that models with
// providerID="opencode-relay" and zero cost are classified as ModelFreeTier.
func TestClassifyAvailability_OpencodeRelayZeroCost(t *testing.T) {
	avail := classifyAvailability("opencode-relay", providerCost{Input: 0, Output: 0})
	require.Equal(t, ModelFreeTier, avail)
}

func TestClassifyAvailability_OpencodeRelayNoCostEntries(t *testing.T) {
	avail := classifyAvailability("opencode-relay", providerCost{})
	require.Equal(t, ModelFreeTier, avail)
}

func TestClassifyAvailability_OpencodeRelayPaidCost(t *testing.T) {
	// opencode-relay models with non-zero cost should be Available, not Free.
	avail := classifyAvailability("opencode-relay", providerCost{Input: 1.0, Output: 2.0})
	require.Equal(t, ModelAvailable, avail)
}

func TestClassifyAvailability_NonOpencodeLoaded(t *testing.T) {
	avail := classifyAvailability("anthropic", providerCost{Input: 0, Output: 0})
	require.Equal(t, ModelAvailable, avail)
}

func TestAnnotateModels_RelayActive_OnlyRemapsOpencode(t *testing.T) {
	// When relayActive=true and the catalog already has providerID="opencode-relay"
	// (Phase 2: relay injected), annotateModels must NOT double-remap to "opencode-relay".
	raw := `{
		"connected": ["opencode-relay","opencode"],
		"all": [
			{"id":"opencode-relay","models":{"nemotron-free":{"id":"nemotron-free","name":"Nemotron Free","cost":{"input":0,"output":0}}}},
			{"id":"opencode","models":{"glm-5.1-free":{"id":"glm-5.1-free","name":"GLM 5.1 Free","cost":{"input":0,"output":0}}}}
		]
	}`

	result, err := annotateModels([]byte(raw), true)
	require.NoError(t, err)
	require.Len(t, result, 2)

	byID := make(map[string]annotatedModel)
	for _, m := range result {
		byID[m.ID] = m
	}
	// Phase 2 model: already opencode-relay, stays opencode-relay
	require.Equal(t, "opencode-relay", byID["nemotron-free"].ProviderID)
	require.Equal(t, ModelFreeTier, byID["nemotron-free"].Availability)
	// Phase 1 model: opencode remapped to opencode-relay when relayActive
	require.Equal(t, "opencode-relay", byID["glm-5.1-free"].ProviderID)
	require.Equal(t, ModelFreeTier, byID["glm-5.1-free"].Availability)
}

func TestAnnotateModels_FullResponse(t *testing.T) {
	raw := `{
		"connected": ["opencode","anthropic"],
		"all": [
			{"id":"opencode","models":{
				"free-model": {"id":"free-model","name":"Free Model","cost":{"input":0,"output":0}},
				"paid-model": {"id":"paid-model","name":"Paid OpenCode","cost":{"input":1,"output":2}}
			}},
			{"id":"anthropic","models":{
				"claude-sonnet-4-5": {"id":"claude-sonnet-4-5","name":"Claude Sonnet 4.5","cost":{"input":3,"output":15}}
			}}
		]
	}`

	result, err := annotateModels([]byte(raw), false)
	require.NoError(t, err)
	require.Len(t, result, 3)

	byID := make(map[string]annotatedModel)
	for _, m := range result {
		byID[m.ID] = m
	}

	require.Equal(t, "free", byID["free-model"].Tier)
	require.True(t, byID["free-model"].FreeTier)
	require.True(t, byID["free-model"].ProxyRequired, "free-tier model must have proxyRequired=true")

	require.Equal(t, "paid", byID["claude-sonnet-4-5"].Tier)
	require.False(t, byID["claude-sonnet-4-5"].FreeTier)
	require.False(t, byID["claude-sonnet-4-5"].ProxyRequired, "paid model must have proxyRequired=false")

	require.Equal(t, "paid", byID["paid-model"].Tier) // opencode provider but has cost > 0
	require.False(t, byID["paid-model"].ProxyRequired, "paid model must have proxyRequired=false")
}

func TestAnnotateModels_InvalidJSON(t *testing.T) {
	_, err := annotateModels([]byte("not json"), false)
	require.Error(t, err)
}

func TestAnnotateModels_EmptyConnected(t *testing.T) {
	// connected=[] → no models accessible → empty result
	raw := `{"connected":[],"all":[{"id":"opencode","models":{"free":{"id":"free","cost":{"input":0,"output":0}}}}]}`
	result, err := annotateModels([]byte(raw), false)
	require.NoError(t, err)
	require.Len(t, result, 0)
}

func TestAnnotateModels_PreservesProviderID(t *testing.T) {
	raw := `{"connected":["anthropic"],"all":[{"id":"anthropic","models":{"claude":{"id":"claude","name":"Claude","cost":{"input":3,"output":15}}}}]}`
	result, err := annotateModels([]byte(raw), false)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "anthropic", result[0].ProviderID)
	require.Equal(t, "claude", result[0].ID)
}

func TestListModels_ResponseAnnotated(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	const testPassword = "annotated-pw"
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	models := `{"connected":["opencode"],"all":[{"id":"opencode","models":{"test":{"id":"test","name":"Test","cost":{"input":0,"output":0}}}}]}`
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
	require.Equal(t, "test", resp.Models[0].ID)
	require.Equal(t, "", resp.CurrentModel) // no updater set = empty
}

// TestAnnotateModels_RelayActive_RemapsProviderID verifies that when the relay
// is active, free-tier opencode models have ProviderID remapped to
// "opencode-relay" so clients use the relay provider for inference.
func TestAnnotateModels_RelayActive_RemapsProviderID(t *testing.T) {
	raw := `{
		"connected": ["opencode","anthropic"],
		"all": [
			{"id":"opencode","models":{
				"free-model": {"id":"free-model","name":"Free","cost":{"input":0,"output":0}},
				"paid-model": {"id":"paid-model","name":"Paid","cost":{"input":1,"output":2}}
			}},
			{"id":"anthropic","models":{
				"claude": {"id":"claude","name":"Claude","cost":{"input":3,"output":15}}
			}}
		]
	}`

	result, err := annotateModels([]byte(raw), true /* relayActive */)
	require.NoError(t, err)
	require.Len(t, result, 3)

	byID := make(map[string]annotatedModel)
	for _, m := range result {
		byID[m.ID] = m
	}

	// Free opencode model: ProviderID must be remapped to opencode-relay
	assert.Equal(t, "opencode-relay", byID["free-model"].ProviderID,
		"free-tier opencode model must use opencode-relay providerID when relay is active")
	assert.True(t, byID["free-model"].ProxyRequired)
	assert.True(t, byID["free-model"].FreeTier)

	// Paid opencode model: ProviderID stays "opencode"
	assert.Equal(t, "opencode", byID["paid-model"].ProviderID,
		"paid opencode model must keep opencode providerID")
	assert.False(t, byID["paid-model"].ProxyRequired)

	// Non-opencode model: unaffected
	assert.Equal(t, "anthropic", byID["claude"].ProviderID)
}

// TestAnnotateModels_RelayInactive_DoesNotRemap verifies that when the relay
// is not active, providerIDs are not remapped.
func TestAnnotateModels_RelayInactive_DoesNotRemap(t *testing.T) {
	raw := `{
		"connected": ["opencode"],
		"all": [{"id":"opencode","models":{"free-model":{"id":"free-model","name":"Free","cost":{"input":0,"output":0}}}}]
	}`

	result, err := annotateModels([]byte(raw), false /* relayActive */)
	require.NoError(t, err)
	require.Len(t, result, 1)

	assert.Equal(t, "opencode", result[0].ProviderID,
		"providerID must not be remapped when relay is inactive")
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
	models := `{"connected":["anthropic"],"all":[{"id":"anthropic","models":{"claude-sonnet-4-5":{"id":"claude-sonnet-4-5","name":"Claude","cost":{"input":3,"output":15}}}}]}`
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
	handler.SetWorkspaceMetadataUpdater(&mockModelReader{model: "claude-sonnet-4-5"})

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
	require.Equal(t, "claude-sonnet-4-5", resp.CurrentModel)
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
	// Mix of connected providers: opencode (free+paid) and anthropic
	// openai is NOT in connected[] — should be excluded entirely
	models := `{
		"connected": ["opencode","anthropic"],
		"all": [
			{"id":"opencode","models":{
				"free-model": {"id":"free-model","name":"Free","cost":{"input":0,"output":0}},
				"paid-model": {"id":"paid-model","name":"Paid","cost":{"input":3,"output":15}}
			}},
			{"id":"anthropic","models":{
				"claude": {"id":"claude","name":"Claude","cost":{"input":3,"output":15}}
			}},
			{"id":"openai","models":{
				"gpt-5": {"id":"gpt-5","name":"GPT-5","cost":{"input":5,"output":15}}
			}}
		]
	}`
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

	// Should contain: free opencode + paid opencode + anthropic/claude
	// Should NOT contain: openai/gpt-5 (not in connected[])
	ids := make([]string, len(resp.Models))
	for i, m := range resp.Models {
		ids[i] = m.ID
	}
	require.Contains(t, ids, "free-model")
	require.Contains(t, ids, "paid-model")
	require.Contains(t, ids, "claude")
	require.NotContains(t, ids, "gpt-5")
	require.Len(t, resp.Models, 3)
}

// TestResolveModelIDFromCatalog verifies that patchAgentModel sends providerID/modelID
// to opencode's /global/config, not the flat catalog ID.
// Regression test for the opencode 1.15.x ProviderModelNotFoundError bug.
func TestResolveModelIDFromCatalog_PrefixesProvider(t *testing.T) {
	const pw = "test-pw"
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	srv := httptest.NewUnstartedServer(authEnforcingHandler(pw, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/provider" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"connected": ["openai","anthropic"],
				"all": [
					{"id":"openai","models":{"gpt-5.5":{"id":"gpt-5.5","name":"GPT-5.5","cost":{"input":5,"output":15}}}},
					{"id":"anthropic","models":{"claude-3":{"id":"claude-3","name":"Claude 3","cost":{"input":3,"output":15}}}}
				]
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	tests := []struct {
		catalogID string
		want      string
	}{
		{"gpt-5.5", "openai/gpt-5.5"},
		{"claude-3", "anthropic/claude-3"},
		{"unknown-model", "unknown-model"}, // not in catalog → unchanged
	}
	for _, tt := range tests {
		t.Run(tt.catalogID, func(t *testing.T) {
			h := NewSecretsHandler(nil)
			got := h.resolveModelIDFromCatalog(context.Background(), "127.0.0.1", pw, tt.catalogID)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestSetModel_LivePush_ResolvesProviderPrefix verifies that SetModel sends
// "providerID/modelID" to opencode, not just the flat catalog ID.
func TestSetModel_LivePush_ResolvesProviderPrefix(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)
	const testPassword = "ws-resolve-pw"

	var receivedModel string
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	srv := httptest.NewUnstartedServer(authEnforcingHandler(testPassword, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/provider":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"connected":["openai"],"all":[{"id":"openai","models":{"gpt-5.5":{"id":"gpt-5.5","name":"GPT-5.5","cost":{"input":5,"output":15}}}}]}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/global/config":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			receivedModel = body["model"]
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

	body, _ := json.Marshal(map[string]string{"model": "gpt-5.5"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/ws-1/model", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, true, resp["applied"], "applied must be true")
	require.Equal(t, "openai/gpt-5.5", receivedModel,
		"patchAgentModel must send providerID/modelID, not flat catalog ID")
}

// TestListModels_CurrentModelProviderID verifies that the currentModelProviderID
// field is populated with the resolved providerID for the selected model.
func TestListModels_CurrentModelProviderID(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	const testPassword = "providerid-pw"
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	models := `{"connected":["thekao"],"all":[{"id":"thekao","models":{
		"glm-5.1":{"id":"glm-5.1","name":"GLM 5.1","cost":{"input":1,"output":2}},
		"gpt-5.4":{"id":"gpt-5.4","name":"GPT 5.4","cost":{"input":1,"output":2}}
	}}]}`
	srv := httptest.NewUnstartedServer(authEnforcingHandler(testPassword, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(models))
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
	handler.SetPasswordGetter(mockPasswordGetter(testPassword))
	handler.SetWorkspaceMetadataUpdater(&mockModelReader{model: "glm-5.1"})

	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("userID", "user-1"); c.Next() })
	router.GET("/api/v1/workspaces/:id/models", handler.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Models                 []annotatedModel `json:"models"`
		CurrentModel           string           `json:"currentModel"`
		CurrentModelProviderID string           `json:"currentModelProviderID"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "glm-5.1", resp.CurrentModel)
	require.Equal(t, "thekao", resp.CurrentModelProviderID,
		"currentModelProviderID must be the provider that owns the selected model")
}

// TestListModels_CurrentModelProviderID_Collision verifies that when two
// connected providers expose the same model ID, currentModelProviderID is ""
// (signals ambiguity; client falls back to find()).
func TestListModels_CurrentModelProviderID_Collision(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	const testPassword = "collision-pw"
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	// Two connected providers both expose model ID "shared-model".
	models := `{"connected":["provider-a","provider-b"],"all":[
		{"id":"provider-a","models":{"shared-model":{"id":"shared-model","name":"Shared A","cost":{"input":1,"output":2}}}},
		{"id":"provider-b","models":{"shared-model":{"id":"shared-model","name":"Shared B","cost":{"input":1,"output":2}}}}
	]}`
	srv := httptest.NewUnstartedServer(authEnforcingHandler(testPassword, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(models))
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
	handler.SetPasswordGetter(mockPasswordGetter(testPassword))
	handler.SetWorkspaceMetadataUpdater(&mockModelReader{model: "shared-model"})

	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("userID", "user-1"); c.Next() })
	router.GET("/api/v1/workspaces/:id/models", handler.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Models                 []annotatedModel `json:"models"`
		CurrentModel           string           `json:"currentModel"`
		CurrentModelProviderID string           `json:"currentModelProviderID"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "shared-model", resp.CurrentModel)
	require.Equal(t, "", resp.CurrentModelProviderID,
		"collision must produce empty currentModelProviderID")
}

// TestListModels_CurrentModelProviderID_RelayActive verifies that when
// relayActive=true, a free-tier opencode model that has been remapped to
// "opencode-relay" by annotateModels is correctly reflected in
// currentModelProviderID. This catches regressions if the remap logic or
// the annotated-model iteration order changes.
func TestListModels_CurrentModelProviderID_RelayActive(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	const testPassword = "relay-providerid-pw"
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	// Free-tier opencode model: cost.input==0, providerID=="opencode" →
	// annotateModels remaps providerID to "opencode-relay" when relayActive=true.
	models := `{"connected":["opencode"],"all":[{"id":"opencode","models":{
		"glm-5.1-free":{"id":"glm-5.1-free","name":"GLM 5.1 Free","cost":{"input":0,"output":0}}
	}}]}`
	srv := httptest.NewUnstartedServer(authEnforcingHandler(testPassword, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(models))
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
	handler.SetPasswordGetter(mockPasswordGetter(testPassword))
	handler.SetWorkspaceMetadataUpdater(&mockModelReader{model: "glm-5.1-free"})
	handler.SetRelayActive(true)

	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("userID", "user-1"); c.Next() })
	router.GET("/api/v1/workspaces/:id/models", handler.ListModels)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Models                 []annotatedModel `json:"models"`
		CurrentModel           string           `json:"currentModel"`
		CurrentModelProviderID string           `json:"currentModelProviderID"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "glm-5.1-free", resp.CurrentModel)
	require.Equal(t, "opencode-relay", resp.CurrentModelProviderID,
		"relay remap must be reflected in currentModelProviderID")
}
