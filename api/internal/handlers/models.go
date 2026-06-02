// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lenaxia/llmsafespace/pkg/agentd"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// WorkspaceMetadataUpdater updates non-sensitive workspace fields.
type WorkspaceMetadataUpdater interface {
	UpdateWorkspace(ctx context.Context, workspaceID string, updates types.WorkspaceUpdates) error
}

// WorkspaceDefaultModelReader reads the workspace's current default model.
// Optional — if the updater also implements this, ListModels includes the selection.
type WorkspaceDefaultModelReader interface {
	GetDefaultModel(ctx context.Context, workspaceID string) (string, error)
}

// ModelSelectionRequest is the request body for PUT /workspaces/:id/model.
type ModelSelectionRequest struct {
	Model string `json:"model" binding:"required"`
}

// SetWorkspaceMetadataUpdater installs the updater for workspace metadata.
func (h *SecretsHandler) SetWorkspaceMetadataUpdater(u WorkspaceMetadataUpdater) {
	h.wsUpdater = u
}

var modelHTTPClient = &http.Client{Timeout: 5 * time.Second}

// modelCache is a brief per-workspace cache for model catalog responses
// to avoid re-fetching on rapid page loads.
type modelCacheEntry struct {
	data      []byte
	expiresAt time.Time
}

var (
	modelCacheMu sync.Mutex
	modelCacheMap = make(map[string]*modelCacheEntry)
)

func getCachedModels(workspaceID string) []byte {
	modelCacheMu.Lock()
	defer modelCacheMu.Unlock()
	entry := modelCacheMap[workspaceID]
	if entry == nil || time.Now().After(entry.expiresAt) {
		return nil
	}
	return entry.data
}

func setCachedModels(workspaceID string, data []byte) {
	modelCacheMu.Lock()
	defer modelCacheMu.Unlock()
	modelCacheMap[workspaceID] = &modelCacheEntry{
		data:      data,
		expiresAt: time.Now().Add(5 * time.Second),
	}
}

// clearModelCache removes all cached entries. Exported for testing.
func clearModelCache() {
	modelCacheMu.Lock()
	defer modelCacheMu.Unlock()
	modelCacheMap = make(map[string]*modelCacheEntry)
}

// ListModels handles GET /api/v1/workspaces/:id/models.
// Proxies to the running opencode instance's model catalog.
func (h *SecretsHandler) ListModels(c *gin.Context) {
	workspaceID := c.Param("id")
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	if h.podIPResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "model discovery unavailable"})
		return
	}

	podIP, err := h.podIPResolver.GetWorkspacePodIP(c.Request.Context(), userID, workspaceID)
	if err != nil || podIP == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace pod not running"})
		return
	}

	// Check cache first (5s TTL, avoids repeated catalog fetches on page loads).
	body := getCachedModels(workspaceID)
	if body == nil {
		url := fmt.Sprintf("http://%s:%d/api/model", podIP, agentd.AgentPort)
		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, url, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
			return
		}

		resp, err := modelHTTPClient.Do(req)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to reach agent"})
			return
		}
		defer resp.Body.Close()

		body, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB max
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read agent response"})
			return
		}

		if resp.StatusCode != http.StatusOK {
			c.JSON(resp.StatusCode, gin.H{"error": "agent returned error"})
			return
		}

		setCachedModels(workspaceID, body)
	}

	// Annotate models with tier information.
	annotated, err := annotateModels(body)
	if err != nil {
		// Fallback: return raw response if annotation fails.
		c.Data(http.StatusOK, "application/json", body)
		return
	}

	// Include current workspace model selection for convenience.
	var currentModel string
	if h.wsUpdater != nil {
		// wsUpdater also implements a reader if available — check via type assertion.
		if reader, ok := h.wsUpdater.(WorkspaceDefaultModelReader); ok {
			currentModel, _ = reader.GetDefaultModel(c.Request.Context(), workspaceID)
		}
	}

	// Mark the selected model in the array.
	if currentModel != "" {
		for i := range annotated {
			if annotated[i].ID == currentModel {
				annotated[i].Selected = true
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"models":       annotated,
		"currentModel": currentModel,
	})
}

// annotatedModel is the enriched model response returned to callers.
type annotatedModel struct {
	ID         string          `json:"id"`
	ProviderID string          `json:"providerID"`
	Name       string          `json:"name"`
	Family     string          `json:"family,omitempty"`
	Enabled    bool            `json:"enabled"`
	Status     string          `json:"status,omitempty"`
	Tier       string          `json:"tier"`     // "free" or "paid"
	FreeTier   bool            `json:"freeTier"` // convenience boolean
	Selected   bool            `json:"selected"` // true if this is the workspace's current default
	Details    json.RawMessage `json:"details"`  // full opencode model object (unstable schema)
}

// opencodeModel is the minimal subset of opencode's ModelV2.Info we parse for classification.
type opencodeModel struct {
	ID         string         `json:"id"`
	ProviderID string         `json:"providerID"`
	Name       string         `json:"name"`
	Family     string         `json:"family,omitempty"`
	Enabled    bool           `json:"enabled"`
	Status     string         `json:"status,omitempty"`
	Cost       []opencodeCost `json:"cost"`
}

type opencodeCost struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

func annotateModels(raw []byte) ([]annotatedModel, error) {
	// Parse the minimal fields we need for classification.
	var models []opencodeModel
	if err := json.Unmarshal(raw, &models); err != nil {
		return nil, err
	}

	// Also parse as raw JSON array to preserve full objects.
	var rawModels []json.RawMessage
	if err := json.Unmarshal(raw, &rawModels); err != nil {
		return nil, err
	}

	result := make([]annotatedModel, len(models))
	for i, m := range models {
		tier := classifyTier(m.ProviderID, m.Cost)
		var details json.RawMessage
		if i < len(rawModels) {
			details = rawModels[i]
		}
		result[i] = annotatedModel{
			ID:         m.ID,
			ProviderID: m.ProviderID,
			Name:       m.Name,
			Family:     m.Family,
			Enabled:    m.Enabled,
			Status:     m.Status,
			Tier:       tier,
			FreeTier:   tier == "free",
			Details:    details,
		}
	}
	return result, nil
}

// classifyTier determines if a model is free or paid.
// Free: opencode provider models where all cost entries have input=0 and output=0.
// Everything else is paid.
func classifyTier(providerID string, cost []opencodeCost) string {
	if providerID != "opencode" {
		return "paid"
	}
	if len(cost) == 0 {
		// No cost data for an opencode model = assume free (opencode's free tier
		// models may not have cost entries populated).
		return "free"
	}
	for _, c := range cost {
		if c.Input > 0 || c.Output > 0 {
			return "paid"
		}
	}
	return "free"
}

// SetModel handles PUT /api/v1/workspaces/:id/model.
// Stores the selected model as workspace metadata (no encryption needed)
// and pushes the model selection directly to the running agent via PATCH /global/config.
func (h *SecretsHandler) SetModel(c *gin.Context) {
	workspaceID := c.Param("id")
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	var req ModelSelectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model field is required"})
		return
	}

	if h.wsUpdater == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "model selection unavailable"})
		return
	}

	// Validate model exists in the live catalog (if pod is running).
	if h.podIPResolver != nil {
		podIP, err := h.podIPResolver.GetWorkspacePodIP(c.Request.Context(), userID, workspaceID)
		if err == nil && podIP != "" {
			if !h.modelExistsInCatalog(c.Request.Context(), podIP, req.Model) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "model not found in workspace catalog"})
				return
			}
		}
		// If pod not running, skip validation (store optimistically).
	}

	// Persist to workspace metadata (survives pod restarts).
	if err := h.wsUpdater.UpdateWorkspace(c.Request.Context(), workspaceID, types.WorkspaceUpdates{
		DefaultModel: &req.Model,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update workspace"})
		return
	}

	// Also persist to K8s Secret so the next pod boot picks it up.
	if h.manifestWriter != nil {
		_ = h.manifestWriter.EnsureWorkspaceConfig(c.Request.Context(), workspaceID, WorkspaceConfig{
			DefaultModel: req.Model,
		})
	}

	// Push model selection to running agent via PATCH /global/config.
	// This merges {"model": "..."} into opencode's config without touching providers.
	applied := false
	if h.podIPResolver != nil {
		podIP, err := h.podIPResolver.GetWorkspacePodIP(c.Request.Context(), userID, workspaceID)
		if err == nil && podIP != "" {
			if patchErr := h.patchAgentModel(c.Request.Context(), podIP, req.Model); patchErr != nil {
				h.warn("PATCH model to agent failed", "error", patchErr.Error())
			} else {
				applied = true
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"model": req.Model, "applied": applied})
}

// patchAgentModel sends {"model": "<id>"} to opencode's PATCH /global/config.
func (h *SecretsHandler) patchAgentModel(ctx context.Context, podIP, model string) error {
	body := []byte(fmt.Sprintf(`{"model":%q}`, model))
	url := fmt.Sprintf("http://%s:%d/global/config", podIP, agentd.AgentPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := modelHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PATCH /global/config returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// modelExistsInCatalog checks if a model ID exists in the running agent's catalog.
func (h *SecretsHandler) modelExistsInCatalog(ctx context.Context, podIP, model string) bool {
	url := fmt.Sprintf("http://%s:%d/api/model", podIP, agentd.AgentPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return true // fail open — don't block on validation infrastructure failure
	}
	resp, err := modelHTTPClient.Do(req)
	if err != nil {
		return true // fail open
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		return true // fail open
	}

	var models []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &models) != nil {
		return true // fail open
	}
	for _, m := range models {
		if m.ID == model {
			return true
		}
	}
	return false
}
