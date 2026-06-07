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
	"strings"
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

		url := fmt.Sprintf("http://%s:%d/provider", podIP, agentd.AgentPort)
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
	annotated, err := annotateModels(body, h.relayActive)
	if err != nil {
		// Fallback: return raw response if annotation fails.
		c.Data(http.StatusOK, "application/json", body)
		return
	}

	// annotateModels already filters to connected-only models.
	// ModelUnavailable cannot appear in the result, but keep the guard
	// for safety in case the provider response schema changes.
	usable := make([]annotatedModel, 0, len(annotated))
	for _, m := range annotated {
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

// providerListResponse is the shape of GET /provider from opencode.
// all[] contains every provider from models.dev (139+) regardless of auth.
// connected[] is the subset with live credentials — the only models we can use.
type providerListResponse struct {
	Connected []string       `json:"connected"`
	All       []providerInfo `json:"all"`
}

// providerInfo is a single provider entry in the /provider response.
type providerInfo struct {
	ID     string                   `json:"id"`
	Models map[string]providerModel `json:"models"`
}

// providerModel is a model entry within a provider in the /provider response.
// cost is an object {input, output}, not an array.
type providerModel struct {
	ID   string       `json:"id"`
	Name string       `json:"name"`
	Cost providerCost `json:"cost"`
}

// providerCost is the cost object in /provider model entries.
type providerCost struct {
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

// annotateModels enriches the raw GET /provider response with tier, availability,
// and relay information. When relayActive is true, free-tier opencode models
// have their ProviderID remapped to "opencode-relay" so clients send inference
// requests to the relay provider rather than the (disabled) built-in opencode
// provider.
//
// Only models whose providerID is in connected[] are returned as available;
// all others (from models.dev but without credentials) are omitted.
func annotateModels(raw []byte, relayActive bool) ([]annotatedModel, error) {
	var provResp providerListResponse
	if err := json.Unmarshal(raw, &provResp); err != nil {
		return nil, err
	}

	// Build a set of connected provider IDs — the only ones we have credentials for.
	connectedSet := make(map[string]bool, len(provResp.Connected))
	for _, id := range provResp.Connected {
		connectedSet[id] = true
	}

	var result []annotatedModel
	for _, p := range provResp.All {
		if !connectedSet[p.ID] {
			continue // no credentials — skip entire provider
		}
		for modelKey, m := range p.Models {
			id := m.ID
			if id == "" {
				id = modelKey
			}
			avail := classifyAvailability(p.ID, m.Cost)
			providerID := p.ID
			if relayActive && avail == ModelFreeTier && p.ID == "opencode" {
				// Remap free-tier opencode models to opencode-relay so inference
				// requests route through the CF Worker. Only remap from "opencode"
				// to "opencode-relay" — if the catalog already has "opencode-relay"
				// (Phase 2: relay injected, opencode restarted) the model already
				// has the correct providerID and no remap is needed.
				providerID = "opencode-relay"
			}
			result = append(result, annotatedModel{
				ID:            id,
				ProviderID:    providerID,
				Name:          m.Name,
				Enabled:       true, // connected = credentials present = enabled
				Availability:  avail,
				Tier:          tierFromAvailability(avail),
				FreeTier:      avail == ModelFreeTier,
				ProxyRequired: avail == ModelFreeTier,
			})
		}
	}
	return result, nil
}

func classifyAvailability(providerID string, cost providerCost) ModelAvailability {
	if isZeroCostOpencode(providerID, cost) {
		return ModelFreeTier
	}
	return ModelAvailable
}

// isZeroCostOpencode returns true when the model is from the opencode free
// tier — either the built-in opencode provider (Phase 1, before relay
// injection) or the synthesized opencode-relay provider (Phase 2, after
// relay injection). Both represent the same free-tier relay and should be
// classified as ModelFreeTier / proxyRequired=true.
func isZeroCostOpencode(providerID string, cost providerCost) bool {
	if providerID != "opencode" && providerID != "opencode-relay" {
		return false
	}
	return cost.Input == 0 && cost.Output == 0
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

	// Metering: record the model selection for billing dashboards.
	if h.metricsRecorder != nil {
		// Determine providerID for the selected model (relay or direct).
		providerID := "opencode"
		if h.relayActive {
			// Use the resolved providerID from the catalog remap logic.
			// For relay-active free models the resolved path uses opencode-relay.
			if h.podIPResolver != nil {
				if podIP, err := h.podIPResolver.GetWorkspacePodIP(c.Request.Context(), userID, workspaceID); err == nil && podIP != "" {
					pw, _ := func() (string, error) {
						if h.passwordGetter != nil {
							return h.passwordGetter(c.Request.Context(), workspaceID)
						}
						return "", nil
					}()
					resolved := h.resolveModelIDFromCatalog(c.Request.Context(), podIP, pw, req.Model)
					if idx := strings.Index(resolved, "/"); idx >= 0 {
						providerID = resolved[:idx]
					}
				}
			}
		}
		h.metricsRecorder.RecordModelSelection(req.Model, providerID)
	}
	c.JSON(http.StatusOK, gin.H{"model": req.Model, "applied": applied})
}

// catalogEntry is a model record parsed from the /provider response, used for
// model existence checks and providerID resolution.
type catalogEntry struct {
	ID         string
	ProviderID string
	Cost       providerCost
}

// fetchCatalog retrieves the model catalog from the running agent via GET /provider.
// Returns only models whose provider is in connected[]. Returns nil on any error
// (callers should fail open when the catalog is unavailable).
func (h *SecretsHandler) fetchCatalog(ctx context.Context, podIP, workspaceID string) []catalogEntry {
	if h.passwordGetter == nil {
		return nil
	}
	password, err := h.passwordGetter(ctx, workspaceID)
	if err != nil || password == "" {
		return nil
	}
	url := fmt.Sprintf("http://%s:%d/provider", podIP, agentd.AgentPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // G107: URL to internal pod
	if err != nil {
		return nil
	}
	req.SetBasicAuth(agentd.AuthUsername, password)
	resp, err := modelHTTPClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort drain
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var provResp providerListResponse
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&provResp) != nil {
		return nil
	}
	connectedSet := make(map[string]bool, len(provResp.Connected))
	for _, id := range provResp.Connected {
		connectedSet[id] = true
	}
	var entries []catalogEntry
	for _, p := range provResp.All {
		if !connectedSet[p.ID] {
			continue
		}
		for modelKey, m := range p.Models {
			id := m.ID
			if id == "" {
				id = modelKey
			}
			entries = append(entries, catalogEntry{
				ID:         id,
				ProviderID: p.ID,
				Cost:       m.Cost,
			})
		}
	}
	return entries
}

// modelExistsInCatalog checks if a model ID exists in the running agent's catalog.
func (h *SecretsHandler) modelExistsInCatalog(ctx context.Context, podIP, workspaceID, model string) bool {
	entries := h.fetchCatalog(ctx, podIP, workspaceID)
	if entries == nil {
		return true // fail open — don't block on catalog retrieval failure
	}
	for _, e := range entries {
		if e.ID == model {
			return true
		}
	}
	return false
}

// resolveModelIDFromCatalog fetches the catalog via GET /provider and returns
// the providerID/modelID form. When h.relayActive is true and the model
// resolves to a free-tier opencode model (providerID=opencode, cost.input=0),
// the providerID is remapped to "opencode-relay" so inference routes through
// the CF Worker. Falls back to model unchanged on any error.
func (h *SecretsHandler) resolveModelIDFromCatalog(ctx context.Context, podIP, password, model string) string {
	url := fmt.Sprintf("http://%s:%d/provider", podIP, agentd.AgentPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // G107: internal pod
	if err != nil {
		return model
	}
	if password != "" {
		req.SetBasicAuth(agentd.AuthUsername, password)
	}
	resp, err := modelHTTPClient.Do(req)
	if err != nil {
		return model
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort drain
	if resp.StatusCode != http.StatusOK {
		return model
	}
	var provResp providerListResponse
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&provResp) != nil {
		return model
	}
	connectedSet := make(map[string]bool, len(provResp.Connected))
	for _, id := range provResp.Connected {
		connectedSet[id] = true
	}
	for _, p := range provResp.All {
		if !connectedSet[p.ID] {
			continue
		}
		for modelKey, m := range p.Models {
			id := m.ID
			if id == "" {
				id = modelKey
			}
			if id != model {
				continue
			}
			providerID := p.ID
			// When relay is active, remap free-tier opencode models to the relay provider.
			// Without this, PATCH /global/config sets "opencode/<model>" which fails because
			// disabled_providers:["opencode"] blocks the built-in provider in routing.
			if h.relayActive && isZeroCostOpencode(providerID, m.Cost) {
				providerID = "opencode-relay"
			}
			return providerID + "/" + id
		}
	}
	return model
}

// patchAgentModel sends the model selection to the running opencode agent.
// It resolves the catalog flat model ID to providerID/modelID format before patching.
func (h *SecretsHandler) patchAgentModel(ctx context.Context, podIP, password, model string) error {
	// Resolve to opencode's providerID/modelID format (e.g. "openai/gpt-5.5").
	resolved := h.resolveModelIDFromCatalog(ctx, podIP, password, model)
	body := []byte(fmt.Sprintf(`{"model":%q}`, resolved))
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
