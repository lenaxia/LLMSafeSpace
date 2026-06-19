// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"sync"
	"time"

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

// --- Model catalog cache ---

// ModelCache abstracts model catalog caching. Default: in-process map.
type ModelCache interface {
	Get(workspaceID string) []byte
	Set(workspaceID string, data []byte)
	Evict(workspaceID string)
}

type inMemoryModelCache struct {
	mu    sync.Mutex
	cache map[string]*modelCacheEntry
}

type modelCacheEntry struct {
	data      []byte
	expiresAt time.Time
}

type modelCachePayload struct {
	Models        []annotatedModel `json:"models"`
	RelayInjected bool             `json:"relayInjected"`
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

var defaultModelCache = newInMemoryModelCache()

func clearModelCache() {
	defaultModelCache.mu.Lock()
	defer defaultModelCache.mu.Unlock()
	defaultModelCache.cache = make(map[string]*modelCacheEntry)
}

// --- Model annotation types and functions ---

type annotatedModel struct {
	ID            string            `json:"id"`
	ProviderID    string            `json:"providerID"`
	Name          string            `json:"name"`
	Family        string            `json:"family,omitempty"`
	Enabled       bool              `json:"enabled"`
	Status        string            `json:"status,omitempty"`
	Availability  ModelAvailability `json:"availability"`
	Tier          string            `json:"tier"`
	FreeTier      bool              `json:"freeTier"`
	ProxyRequired bool              `json:"proxyRequired"`
	Selected      bool              `json:"selected"`
	Details       json.RawMessage   `json:"details"`
}

type providerListResponse struct {
	Connected []string       `json:"connected"`
	All       []providerInfo `json:"all"`
}

type providerInfo struct {
	ID     string                   `json:"id"`
	Models map[string]providerModel `json:"models"`
}

type providerModel struct {
	ID   string       `json:"id"`
	Name string       `json:"name"`
	Cost providerCost `json:"cost"`
}

type providerCost struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

type ModelAvailability string

const (
	ModelAvailable   ModelAvailability = "available"
	ModelUnavailable ModelAvailability = "unavailable"
	ModelFreeTier    ModelAvailability = "free"
)

// annotateModels enriches the raw GET /provider response with tier,
// availability, and relay information. Only models whose providerID is
// in connected[] are returned; all others are omitted.
func annotateModels(raw []byte, relayGloballyEnabled, relayInjected bool) ([]annotatedModel, error) {
	var provResp providerListResponse
	if err := json.Unmarshal(raw, &provResp); err != nil {
		return nil, err
	}

	connectedSet := make(map[string]bool, len(provResp.Connected))
	for _, id := range provResp.Connected {
		connectedSet[id] = true
	}

	var result []annotatedModel
	for _, p := range provResp.All {
		if !connectedSet[p.ID] {
			continue
		}
		for modelKey, m := range p.Models {
			id := m.ID
			if id == "" {
				id = modelKey
			}
			avail := classifyAvailability(p.ID, m.Cost)
			providerID := p.ID

			if relayGloballyEnabled && relayInjected && avail == ModelFreeTier && p.ID == "opencode" {
				providerID = "opencode-relay"
			}
			result = append(result, annotatedModel{
				ID:            id,
				ProviderID:    providerID,
				Name:          m.Name,
				Enabled:       true,
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
