// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespaces/pkg/agent/opencode"
	pkginterfaces "github.com/lenaxia/llmsafespaces/pkg/interfaces"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// RelayStateChecker returns whether the relay injector has completed
// for the given workspace. Production implementation resolves podIP +
// password and calls /v1/readyz on the agentd admin port (4098).
// This is separate from AgentClient (which targets opencode port 4096
// with Basic auth) because the relay check uses the admin port with
// Bearer auth.
type RelayStateChecker func(ctx context.Context, userID, workspaceID string) bool

// ModelsHandler handles GET /workspaces/:id/models and
// PUT /workspaces/:id/model (US-29.5). Extracted from SecretsHandler
// to enforce single responsibility. Consumes AgentClient (US-29.1)
// for all opencode HTTP communication — the handler never resolves
// podIP or password directly for opencode calls.
type ModelsHandler struct {
	agentClient     opencode.AgentClient
	wsUpdater       ModelStore
	policyChecker   OrgPolicyChecker
	metricsRecorder ModelSelectionRecorder
	manifestWriter  SecretsManifestWriter
	relayActive     bool
	relayChecker    RelayStateChecker
	logger          pkginterfaces.LoggerInterface
}

// NewModelsHandler creates a ModelsHandler with the required AgentClient.
// Optional dependencies are set via the Set methods.
func NewModelsHandler(agentClient opencode.AgentClient) *ModelsHandler {
	return &ModelsHandler{agentClient: agentClient}
}

func (h *ModelsHandler) SetAgentClient(ac opencode.AgentClient)      { h.agentClient = ac }
func (h *ModelsHandler) SetModelStore(s ModelStore)                  { h.wsUpdater = s }
func (h *ModelsHandler) SetPolicyChecker(p OrgPolicyChecker)         { h.policyChecker = p }
func (h *ModelsHandler) SetMetricsRecorder(r ModelSelectionRecorder) { h.metricsRecorder = r }
func (h *ModelsHandler) SetManifestWriter(w SecretsManifestWriter)   { h.manifestWriter = w }
func (h *ModelsHandler) SetRelayActive(active bool)                  { h.relayActive = active }
func (h *ModelsHandler) SetRelayChecker(rc RelayStateChecker)        { h.relayChecker = rc }
func (h *ModelsHandler) SetLogger(l pkginterfaces.LoggerInterface)   { h.logger = l }

func (h *ModelsHandler) warn(msg string, fields ...interface{}) {
	if h.logger != nil {
		h.logger.Warn(msg, fields...)
	}
}

// ListModels handles GET /api/v1/workspaces/:id/models.
func (h *ModelsHandler) ListModels(c *gin.Context) {
	workspaceID := c.Param("id")
	userID, _ := extractAuth(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	if h.agentClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "model discovery unavailable"})
		return
	}

	// Check cache first (5s TTL).
	var annotated []annotatedModel
	var relayInjected bool
	if cached := defaultModelCache.Get(workspaceID); cached != nil {
		var payload modelCachePayload
		if err := json.Unmarshal(cached, &payload); err == nil {
			annotated = payload.Models
			relayInjected = payload.RelayInjected
		}
	}

	if annotated == nil {
		// Fetch model catalog via AgentClient (resolves podIP + password internally).
		body, err := h.agentClient.ListModels(c.Request.Context(), userID, workspaceID)
		if err != nil {
			if strings.Contains(err.Error(), "no running pod") {
				c.JSON(http.StatusNotFound, gin.H{"error": "workspace pod not running"})
				return
			}
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to reach agent"})
			return
		}

		// Check relay injection state via the admin-port relay checker.
		if h.relayActive && h.relayChecker != nil {
			relayInjected = h.relayChecker(c.Request.Context(), userID, workspaceID)
		}

		annotated, err = annotateModels(body, h.relayActive, relayInjected)
		if err != nil {
			annotated = []annotatedModel{}
		}
		if serialized, serErr := json.Marshal(modelCachePayload{Models: annotated, RelayInjected: relayInjected}); serErr == nil {
			defaultModelCache.Set(workspaceID, serialized)
		}
	}

	// Filter unavailable models.
	usable := make([]annotatedModel, 0, len(annotated))
	for _, m := range annotated {
		if m.Availability != ModelUnavailable {
			usable = append(usable, m)
		}
	}
	annotated = usable

	// US-43.8: Filter by org policy.
	if h.policyChecker != nil && h.wsUpdater != nil {
		annotated = h.filterByOrgPolicy(c.Request.Context(), workspaceID, annotated)
	}

	// Include current model selection + resolve providerID.
	currentModel := ""
	if h.wsUpdater != nil {
		currentModel, _ = h.wsUpdater.GetDefaultModel(c.Request.Context(), workspaceID)
	}
	currentModelProviderID := resolveProviderID(annotated, currentModel)
	markSelected(annotated, currentModel)

	c.JSON(http.StatusOK, gin.H{
		"models":                 annotated,
		"currentModel":           currentModel,
		"currentModelProviderID": currentModelProviderID,
	})
}

// SetModel handles PUT /api/v1/workspaces/:id/model.
func (h *ModelsHandler) SetModel(c *gin.Context) {
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

	if h.agentClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "model selection unavailable (no agent client)"})
		return
	}

	// Validate model exists in the live catalog (if pod is running).
	catalog, catErr := h.agentClient.ListModels(c.Request.Context(), userID, workspaceID)
	if catErr == nil && len(catalog) > 0 {
		if !modelExistsInRawCatalog(catalog, req.Model) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "model not found in workspace catalog"})
			return
		}
	}
	// If pod not running, skip validation (store optimistically).

	// Persist to workspace metadata.
	if err := h.wsUpdater.UpdateWorkspace(c.Request.Context(), workspaceID, types.WorkspaceUpdates{
		DefaultModel: &req.Model,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update workspace"})
		return
	}

	defaultModelCache.Evict(workspaceID)

	// Persist to K8s Secret for pod-boot durability.
	if h.manifestWriter != nil {
		if cfgErr := h.manifestWriter.EnsureWorkspaceConfig(c.Request.Context(), workspaceID, types.WorkspaceConfig{
			DefaultModel: req.Model,
		}); cfgErr != nil {
			h.warn("SetModel: EnsureWorkspaceConfig failed — model will not persist across pod restart until next successful write",
				"workspaceID", workspaceID, "error", cfgErr.Error())
		}
	}

	// Push model selection to running agent via AgentClient.
	applied := false
	var resolvedModel string
	resolved := resolveModelFromCatalog(catalog, req.Model, h.relayActive, h.relayChecker, c.Request.Context(), userID, workspaceID)
	if resolved != "" {
		config := map[string]any{"model": resolved}
		if patchErr := h.agentClient.PatchConfig(c.Request.Context(), userID, workspaceID, config); patchErr != nil {
			h.warn("PATCH model to agent failed", "error", patchErr.Error())
		} else {
			applied = true
			resolvedModel = resolved
		}
	}

	// Metering.
	if h.metricsRecorder != nil {
		providerID := "unknown"
		if idx := strings.Index(resolvedModel, "/"); idx >= 0 {
			providerID = resolvedModel[:idx]
		}
		h.metricsRecorder.RecordModelSelection(req.Model, providerID)
	}
	c.JSON(http.StatusOK, gin.H{"model": req.Model, "applied": applied})
}

// filterByOrgPolicy applies org allowed_models / allowed_providers.
func (h *ModelsHandler) filterByOrgPolicy(ctx context.Context, workspaceID string, models []annotatedModel) []annotatedModel {
	meta, err := h.wsUpdater.GetWorkspace(ctx, workspaceID)
	if err != nil || meta == nil || meta.OrgID == nil || *meta.OrgID == "" {
		return models
	}
	pol, polErr := h.policyChecker.GetEffectivePolicy(ctx, *meta.OrgID)
	if polErr != nil || pol == nil {
		return models
	}
	filtered := make([]annotatedModel, 0, len(models))
	for _, m := range models {
		if pol.IsModelAllowed(m.ID) && pol.IsProviderAllowed(m.ProviderID) {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

// resolveProviderID finds the providerID for the selected model.
// Returns "" if ambiguous (multiple providers have the same model ID).
func resolveProviderID(models []annotatedModel, modelID string) string {
	if modelID == "" {
		return ""
	}
	var providerID string
	for _, m := range models {
		if m.ID == modelID {
			if providerID == "" {
				providerID = m.ProviderID
			} else if providerID != m.ProviderID {
				return "" // collision
			}
		}
	}
	return providerID
}

func markSelected(models []annotatedModel, modelID string) {
	if modelID == "" {
		return
	}
	for i := range models {
		if models[i].ID == modelID {
			models[i].Selected = true
		}
	}
}

// modelExistsInRawCatalog checks if a model ID exists in the raw /provider JSON.
func modelExistsInRawCatalog(rawCatalog []byte, model string) bool {
	var provResp providerListResponse
	if json.Unmarshal(rawCatalog, &provResp) != nil {
		return true // fail open on parse error
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
			if id == model {
				return true
			}
		}
	}
	return false
}

// resolveModelFromCatalog resolves a flat model ID to providerID/modelID form.
// Applies relay remapping when relay is active and injected.
func resolveModelFromCatalog(
	rawCatalog []byte,
	model string,
	relayActive bool,
	relayChecker RelayStateChecker,
	ctx context.Context,
	userID, workspaceID string,
) string {
	var provResp providerListResponse
	if json.Unmarshal(rawCatalog, &provResp) != nil {
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
			if relayActive && isZeroCostOpencode(providerID, m.Cost) {
				if relayChecker != nil && relayChecker(ctx, userID, workspaceID) {
					providerID = "opencode-relay"
				}
			}
			return providerID + "/" + id
		}
	}
	return model
}
