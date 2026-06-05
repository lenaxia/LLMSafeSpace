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

// ModelStore provides all database operations needed by model endpoints.
// Satisfied by *database.Service.
type ModelStore interface {
	UpdateWorkspace(ctx context.Context, workspaceID string, updates types.WorkspaceUpdates) error
	GetDefaultModel(ctx context.Context, workspaceID string) (string, error)
	GetWorkspace(ctx context.Context, workspaceID string) (*types.WorkspaceMetadata, error)
}

// ModelSelectionRequest is the request body for PUT /workspaces/:id/model.
type ModelSelectionRequest struct {
	Model string `json:"model" binding:"required"`
}

// SetWorkspaceMetadataUpdater installs the model store for workspace metadata operations.
func (h *SecretsHandler) SetWorkspaceMetadataUpdater(u ModelStore) {
	h.wsUpdater = u
}

// ModelCache abstracts model catalog caching. Default: in-process map.
// Future: Redis-backed for multi-replica consistency (US-30.11).
type ModelCache interface {
	Get(workspaceID string) []byte
	Set(workspaceID string, data []byte)
	Evict(workspaceID string)
}

// inMemoryModelCache is the default in-process cache (5s TTL).
type inMemoryModelCache struct {
	mu    sync.Mutex
	cache map[string]*modelCacheEntry
}

type modelCacheEntry struct {
	data      []byte
	expiresAt time.Time
}

func newInMemoryModelCache() *inMemoryModelCache {
	return &inMemoryModelCache{cache: make(map[string]*modelCacheEntry)}
}

func (c *inMemoryModelCache) Get(workspaceID string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.cache[workspaceID]
	if entry == nil || time.Now().After(entry.expiresAt) {
		return nil
	}
	return entry.data
}

func (c *inMemoryModelCache) Set(workspaceID string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[workspaceID] = &modelCacheEntry{data: data, expiresAt: time.Now().Add(5 * time.Second)}
}

func (c *inMemoryModelCache) Evict(workspaceID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, workspaceID)
}

// defaultModelCache is the singleton used when no Redis is wired.
var defaultModelCache = newInMemoryModelCache()

var modelHTTPClient = &http.Client{Timeout: 5 * time.Second}

// clearModelCache resets the cache. Exported for testing.
func clearModelCache() {
	defaultModelCache.mu.Lock()
	defer defaultModelCache.mu.Unlock()
	defaultModelCache.cache = make(map[string]*modelCacheEntry)
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

	// Explicit ownership check before any pod communication.
	if h.wsUpdater != nil {
		meta, err := h.wsUpdater.GetWorkspace(c.Request.Context(), workspaceID)
		if err != nil || meta == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
			return
		}
		if meta.UserID != userID {
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
			return
		}
	}

	podIP, err := h.podIPResolver.GetWorkspacePodIP(c.Request.Context(), userID, workspaceID)
	if err != nil || podIP == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace pod not running"})
		return
	}

	// Check cache first (5s TTL, avoids repeated catalog fetches on page loads).
	body := defaultModelCache.Get(workspaceID)
	if body == nil {
		// Retrieve workspace password for opencode Basic auth.
		if h.passwordGetter == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "model discovery unavailable (no password getter)"})
			return
		}
		password, err := h.passwordGetter(c.Request.Context(), workspaceID)
		if err != nil || password == "" {
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to retrieve workspace credentials"})
			return
		}

		url := fmt.Sprintf("http://%s:%d/api/model", podIP, agentd.AgentPort)
		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, url, nil) //nolint:gosec // G107: URL constructed from trusted podIP (internal cluster network)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
			return
		}
		req.SetBasicAuth(agentd.AuthUsername, password)

		resp, err := modelHTTPClient.Do(req)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to reach agent"})
			return
		}
		defer resp.Body.Close() //nolint:errcheck // best-effort drain

		body, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB max
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read agent response"})
			return
		}

		if resp.StatusCode != http.StatusOK {
			c.JSON(resp.StatusCode, gin.H{"error": "agent returned error"})
			return
		}

		defaultModelCache.Set(workspaceID, body)
	}

	// Annotate models with tier information.
	annotated, err := annotateModels(body)
	if err != nil {
		// Fallback: return raw response if annotation fails.
		c.Data(http.StatusOK, "application/json", body)
		return
	}

	// Filter to only usable models (available or free-tier).
	usable := make([]annotatedModel, 0, len(annotated))
	for _, m := range annotated {
		if !m.Enabled {
			continue
		}
		if m.Availability == ModelUnavailable {
			continue
		}
		usable = append(usable, m)
	}
	annotated = usable

	// Include current workspace model selection for convenience.
	var currentModel string
	if h.wsUpdater != nil {
		currentModel, _ = h.wsUpdater.GetDefaultModel(c.Request.Context(), workspaceID)
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
	ID            string            `json:"id"`
	ProviderID    string            `json:"providerID"`
	Name          string            `json:"name"`
	Family        string            `json:"family,omitempty"`
	Enabled       bool              `json:"enabled"`
	Status        string            `json:"status,omitempty"`
	Availability  ModelAvailability `json:"availability"`
	Tier          string            `json:"tier"`          // "free" or "paid" (deprecated, use Availability)
	FreeTier      bool              `json:"freeTier"`      // convenience boolean (deprecated)
	ProxyRequired bool              `json:"proxyRequired"` // true if model requires client-side relay (Epic 26)
	Selected      bool              `json:"selected"`      // true if this is the workspace's current default
	Details       json.RawMessage   `json:"details"`       // full opencode model object (unstable schema)
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

// ModelAvailability classifies model accessibility.
type ModelAvailability string

const (
	ModelAvailable   ModelAvailability = "available"
	ModelUnavailable ModelAvailability = "unavailable"
	ModelFreeTier    ModelAvailability = "free"
)

func annotateModels(raw []byte) ([]annotatedModel, error) {
	var models []opencodeModel
	if err := json.Unmarshal(raw, &models); err != nil {
		return nil, err
	}

	var rawModels []json.RawMessage
	if err := json.Unmarshal(raw, &rawModels); err != nil {
		return nil, err
	}

	// Derive loadedProviders from catalog — which providers have enabled models.
	loadedProviders := make(map[string]bool)
	for _, m := range models {
		if m.Enabled {
			loadedProviders[m.ProviderID] = true
		}
	}

	result := make([]annotatedModel, len(models))
	for i, m := range models {
		avail := classifyAvailability(m.ProviderID, m.Cost, loadedProviders)
		var details json.RawMessage
		if i < len(rawModels) {
			details = rawModels[i]
		}
		result[i] = annotatedModel{
			ID:            m.ID,
			ProviderID:    m.ProviderID,
			Name:          m.Name,
			Family:        m.Family,
			Enabled:       m.Enabled,
			Status:        m.Status,
			Availability:  avail,
			Tier:          tierFromAvailability(avail),
			FreeTier:      avail == ModelFreeTier,
			ProxyRequired: avail == ModelFreeTier,
			Details:       details,
		}
	}
	return result, nil
}

func classifyAvailability(providerID string, cost []opencodeCost, loadedProviders map[string]bool) ModelAvailability {
	if !loadedProviders[providerID] {
		return ModelUnavailable
	}
	if isZeroCostOpencode(providerID, cost) {
		return ModelFreeTier
	}
	return ModelAvailable
}

func isZeroCostOpencode(providerID string, cost []opencodeCost) bool {
	if providerID != "opencode" {
		return false
	}
	if len(cost) == 0 {
		return true // no cost data for opencode model = assume free
	}
	for _, c := range cost {
		if c.Input > 0 || c.Output > 0 {
			return false
		}
	}
	return true
}

func tierFromAvailability(a ModelAvailability) string {
	if a == ModelFreeTier {
		return "free"
	}
	return "paid"
}

// SetModel handles PUT /api/v1/workspaces/:id/model.
// Stores the selected model as workspace metadata. The model takes effect
// on next agent reload or pod restart (not pushed live to avoid stream
// disruption — see Epic 27a principles). Returns applied:false always.
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

	// Ownership check: verify the user owns this workspace before mutating.
	meta, err := h.wsUpdater.GetWorkspace(c.Request.Context(), workspaceID)
	if err != nil || meta == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}
	if meta.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	// Validate model exists in the live catalog (if pod is running).
	if h.podIPResolver != nil {
		podIP, err := h.podIPResolver.GetWorkspacePodIP(c.Request.Context(), userID, workspaceID)
		if err == nil && podIP != "" {
			if !h.modelExistsInCatalog(c.Request.Context(), podIP, workspaceID, req.Model) {
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

	// Clear this workspace's model cache so next ListModels reflects the new selection.
	defaultModelCache.Evict(workspaceID)

	// Also persist to K8s Secret so the next pod boot picks it up.
	if h.manifestWriter != nil {
		_ = h.manifestWriter.EnsureWorkspaceConfig(c.Request.Context(), workspaceID, types.WorkspaceConfig{
			DefaultModel: req.Model,
		})
	}

	// Push model selection to running agent (if pod available).
	applied := false
	if h.podIPResolver != nil {
		podIP, err := h.podIPResolver.GetWorkspacePodIP(c.Request.Context(), userID, workspaceID)
		if err == nil && podIP != "" {
			// Fetch workspace password once; used for all opencode Basic-auth calls below.
			password := ""
			if h.passwordGetter != nil {
				password, _ = h.passwordGetter(c.Request.Context(), workspaceID)
			}

			if patchErr := h.patchAgentModel(c.Request.Context(), podIP, password, req.Model); patchErr != nil {
				h.warn("PATCH model to agent failed", "error", patchErr.Error())
			} else {
				applied = true
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"model": req.Model, "applied": applied})
}

// modelExistsInCatalog checks if a model ID exists in the running agent's catalog.
func (h *SecretsHandler) modelExistsInCatalog(ctx context.Context, podIP, workspaceID, model string) bool {
	if h.passwordGetter == nil {
		return true // fail open — don't block on missing password getter
	}
	password, err := h.passwordGetter(ctx, workspaceID)
	if err != nil || password == "" {
		return true // fail open — don't block on password retrieval failure
	}
	url := fmt.Sprintf("http://%s:%d/api/model", podIP, agentd.AgentPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // G107: URL to internal pod
	if err != nil {
		return true // fail open — don't block on validation infrastructure failure
	}
	req.SetBasicAuth(agentd.AuthUsername, password)
	resp, err := modelHTTPClient.Do(req)
	if err != nil {
		return true // fail open
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort drain
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

// patchAgentModel sends the model selection to the running opencode agent.
func (h *SecretsHandler) patchAgentModel(ctx context.Context, podIP, password, model string) error {
	body := []byte(fmt.Sprintf(`{"model":%q}`, model))
	url := fmt.Sprintf("http://%s:%d/global/config", podIP, agentd.AgentPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body)) //nolint:gosec // G107: internal pod
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if password != "" {
		req.SetBasicAuth(agentd.AuthUsername, password)
	}
	resp, err := modelHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PATCH /global/config returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
