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

var modelHTTPClient = &http.Client{Timeout: 5 * time.Second}

// modelCache is a brief per-workspace cache for model catalog responses
// to avoid re-fetching on rapid page loads.
type modelCacheEntry struct {
	data      []byte
	expiresAt time.Time
}

var (
	modelCacheMu  sync.Mutex
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

// evictModelCache removes a single workspace's cached entry.
func evictModelCache(workspaceID string) {
	modelCacheMu.Lock()
	defer modelCacheMu.Unlock()
	delete(modelCacheMap, workspaceID)
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
	body := getCachedModels(workspaceID)
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

		setCachedModels(workspaceID, body)
	}

	// Annotate models with tier information.
	annotated, err := annotateModels(body)
	if err != nil {
		// Fallback: return raw response if annotation fails.
		c.Data(http.StatusOK, "application/json", body)
		return
	}

	// Filter to only usable models:
	// - Free-tier models always work (public key)
	// - Models from non-opencode providers work (they only appear if the
	//   provider was enabled via real credentials injected by AccountPlugin)
	// - Paid opencode models do NOT work with the public key — exclude them
	usable := make([]annotatedModel, 0, len(annotated))
	for _, m := range annotated {
		if !m.Enabled {
			continue
		}
		if m.ProviderID == "opencode" && !m.FreeTier {
			continue // paid opencode model — won't work with public key
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
	ID            string          `json:"id"`
	ProviderID    string          `json:"providerID"`
	Name          string          `json:"name"`
	Family        string          `json:"family,omitempty"`
	Enabled       bool            `json:"enabled"`
	Status        string          `json:"status,omitempty"`
	Tier          string          `json:"tier"`          // "free" or "paid"
	FreeTier      bool            `json:"freeTier"`      // convenience boolean
	ProxyRequired bool            `json:"proxyRequired"` // true if model requires client-side relay (Epic 26)
	Selected      bool            `json:"selected"`      // true if this is the workspace's current default
	Details       json.RawMessage `json:"details"`       // full opencode model object (unstable schema)
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
			ID:            m.ID,
			ProviderID:    m.ProviderID,
			Name:          m.Name,
			Family:        m.Family,
			Enabled:       m.Enabled,
			Status:        m.Status,
			Tier:          tier,
			FreeTier:      tier == "free",
			ProxyRequired: tier == "free",
			Details:       details,
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
	evictModelCache(workspaceID)

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

			// Epic 26: If the selected model is free-tier, configure the
			// opencode provider's baseURL to route through the relay proxy.
			// If switching to a paid model, reset to direct (no relay).
			if h.isFreeTierModel(c.Request.Context(), podIP, password, req.Model) {
				relayBaseURL := fmt.Sprintf("http://localhost:%d/relay/inference", agentd.AgentdPort)
				if pushErr := h.pushRelayBaseURL(c.Request.Context(), podIP, password, relayBaseURL); pushErr != nil {
					h.warn("push relay baseURL failed", "error", pushErr.Error())
				}
			} else {
				if pushErr := h.clearRelayBaseURL(c.Request.Context(), podIP, password); pushErr != nil {
					h.warn("clear relay baseURL failed", "error", pushErr.Error())
				}
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

// isFreeTierModel checks if the given model ID is a free-tier model in the live catalog.
func (h *SecretsHandler) isFreeTierModel(ctx context.Context, podIP, password, modelID string) bool {
	url := fmt.Sprintf("http://%s:%d/api/model", podIP, agentd.AgentPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // G107: internal pod
	if err != nil {
		return false
	}
	if password != "" {
		req.SetBasicAuth(agentd.AuthUsername, password)
	}
	resp, err := modelHTTPClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false
	}
	var models []opencodeModel
	if json.Unmarshal(body, &models) != nil {
		return false
	}
	for _, m := range models {
		if m.ID == modelID {
			return classifyTier(m.ProviderID, m.Cost) == "free"
		}
	}
	return false
}

// pushRelayBaseURL configures the opencode provider's baseURL to route
// through the in-pod relay proxy (Epic 26). After updating auth.json via
// PUT /auth/opencode, it calls POST /instance/dispose to invalidate the
// cached provider state so opencode picks up the new baseURL on the next
// request. Without the dispose, the cached endpoint.url is used until the
// opencode process restarts.
func (h *SecretsHandler) pushRelayBaseURL(ctx context.Context, podIP, password, relayBaseURL string) error {
	type payload struct {
		Type     string            `json:"type"`
		Key      string            `json:"key"`
		Metadata map[string]string `json:"metadata"`
	}
	body, err := json.Marshal(payload{Type: "api", Key: "public", Metadata: map[string]string{"baseURL": relayBaseURL}})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s:%d/auth/opencode", podIP, agentd.AgentPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body)) //nolint:gosec // G107: internal pod
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
		return fmt.Errorf("PUT /auth/opencode returned %d", resp.StatusCode)
	}

	// Dispose the opencode instance so it reloads provider config (including
	// the new baseURL) on the next request. Without this, opencode uses the
	// cached provider state and never routes through the relay.
	disposeURL := fmt.Sprintf("http://%s:%d/instance/dispose", podIP, agentd.AgentPort)
	dispReq, err := http.NewRequestWithContext(ctx, http.MethodPost, disposeURL, http.NoBody) //nolint:gosec // G107: internal pod
	if err != nil {
		return fmt.Errorf("create dispose request: %w", err)
	}
	dispReq.Header.Set("Content-Type", "application/json")
	if password != "" {
		dispReq.SetBasicAuth(agentd.AuthUsername, password)
	}
	dispResp, err := modelHTTPClient.Do(dispReq)
	if err != nil {
		return fmt.Errorf("POST /instance/dispose: %w", err)
	}
	defer func() { _ = dispResp.Body.Close() }()
	if dispResp.StatusCode >= 400 {
		return fmt.Errorf("POST /instance/dispose returned %d", dispResp.StatusCode)
	}
	return nil
}

// clearRelayBaseURL resets the opencode provider to its default base URL.
func (h *SecretsHandler) clearRelayBaseURL(ctx context.Context, podIP, password string) error {
	return h.pushRelayBaseURL(ctx, podIP, password, "")
}
