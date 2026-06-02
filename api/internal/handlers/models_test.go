// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
}

func (m *mockWSUpdater) UpdateWorkspace(_ context.Context, _ string, updates types.WorkspaceUpdates) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, updates)
	return m.failErr
}

// --- ListModels Tests ---

func TestListModels_HappyPath(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	// Mock opencode model endpoint on port 4096.
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	models := `[{"id":"anthropic/claude-sonnet-4-5","providerID":"anthropic","name":"Claude Sonnet 4.5","enabled":true}]`
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/model", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(models))
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})

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

// --- SetModel Tests ---

func TestSetModel_HappyPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Mock opencode PATCH /global/config on port 4096.
	var (
		mu        sync.Mutex
		patchBody []byte
	)
	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/config" && r.Method == http.MethodPatch {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			patchBody = body
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"model":"anthropic/claude-sonnet-4-5"}`))
			return
		}
		http.Error(w, "not found", 404)
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	updater := &mockWSUpdater{}
	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
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
	require.Equal(t, true, resp["applied"])

	// Verify workspace metadata was updated.
	updater.mu.Lock()
	require.Len(t, updater.calls, 1)
	require.Equal(t, "anthropic/claude-sonnet-4-5", *updater.calls[0].DefaultModel)
	updater.mu.Unlock()

	// Verify PATCH was sent to opencode.
	mu.Lock()
	require.Contains(t, string(patchBody), "anthropic/claude-sonnet-4-5")
	mu.Unlock()
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
	require.Equal(t, "opencode/free-model", result[0].ID)

	require.Equal(t, "paid", result[1].Tier)
	require.False(t, result[1].FreeTier)

	require.Equal(t, "paid", result[2].Tier) // opencode provider but has cost > 0
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

	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	models := `[{"id":"opencode/test","providerID":"opencode","name":"Test","enabled":true,"cost":[{"input":0,"output":0,"cache":{"read":0,"write":0}}],"status":"active"}]`
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(models))
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})

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

func TestListModels_IncludesCurrentModel(t *testing.T) {
	clearModelCache()
	gin.SetMode(gin.TestMode)

	listener, err := net.Listen("tcp", "127.0.0.1:4096")
	if err != nil {
		t.Skip("port 4096 not available")
	}
	models := `[{"id":"anthropic/claude-sonnet-4-5","providerID":"anthropic","name":"Claude","enabled":true,"cost":[{"input":3,"output":15,"cache":{"read":0,"write":0}}]}]`
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(models))
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	handler := NewSecretsHandler(nil)
	handler.SetPodIPResolver(&staticPodIPResolver{addr: "127.0.0.1"})
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
