// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"encoding/json"
	"fmt"
)

// ProviderModelCost holds the per-token cost for a model. Zero on both
// fields identifies a free-tier model (e.g. opencode's built-in models).
type ProviderModelCost struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// CatalogModel is a single model entry in a parsed agent catalog.
type CatalogModel struct {
	ID   string            `json:"id"`
	Name string            `json:"name"`
	Cost ProviderModelCost `json:"cost"`
}

// CatalogProvider groups models under a provider. Models is keyed by the
// provider-local model key (which may differ from the model's own ID).
type CatalogProvider struct {
	ID     string                  `json:"id"`
	Models map[string]CatalogModel `json:"models"`
}

// Catalog is the normalized, agent-agnostic shape of a model listing.
// Connected holds provider IDs the agent reports as active; Providers
// holds the full provider→model tree. A ModelCatalogParser converts an
// agent-specific wire format into this shape.
type Catalog struct {
	Connected []string          `json:"connected"`
	Providers []CatalogProvider `json:"all"`
}

// ModelCatalogParser decodes an agent's model catalog response into the
// normalized Catalog shape. The concrete implementation is selected at
// construction time based on the agent type/version — callers never
// branch on the wire format.
//
// Worklog 0377 H1-a′: the interface exists so that opencode schema drift
// across versions (and, if a future runtime runs a different agent, a
// different wire format) is handled by adding a parser variant rather
// than branching in every caller. One implementation exists today
// (opencodeProviderParser); the interface earns its keep because the
// opencode /provider schema is documented to vary by version.
type ModelCatalogParser interface {
	Parse(raw []byte) (*Catalog, error)
}

// opencodeProviderParser parses opencode's GET /provider response.
// The wire shape is {"connected":["id",...],"all":[{"id","models"}]}.
type opencodeProviderParser struct{}

// NewOpencodeProviderParser returns a parser for opencode's /provider format.
func NewOpencodeProviderParser() ModelCatalogParser {
	return opencodeProviderParser{}
}

func (opencodeProviderParser) Parse(raw []byte) (*Catalog, error) {
	if len(raw) == 0 {
		return &Catalog{}, nil
	}
	var provResp struct {
		Connected []string `json:"connected"`
		All       []struct {
			ID     string `json:"id"`
			Models map[string]struct {
				ID   string            `json:"id"`
				Name string            `json:"name"`
				Cost ProviderModelCost `json:"cost"`
			} `json:"models"`
		} `json:"all"`
	}
	if err := json.Unmarshal(raw, &provResp); err != nil {
		return nil, fmt.Errorf("parse opencode /provider response: %w", err)
	}
	catalog := &Catalog{Connected: provResp.Connected}
	for _, p := range provResp.All {
		cp := CatalogProvider{ID: p.ID, Models: make(map[string]CatalogModel, len(p.Models))}
		for k, m := range p.Models {
			cp.Models[k] = CatalogModel{ID: m.ID, Name: m.Name, Cost: m.Cost}
		}
		catalog.Providers = append(catalog.Providers, cp)
	}
	return catalog, nil
}

// modelExists returns true if modelID appears in any connected provider's
// model list. Replaces the former modelExistsInRawCatalog (which took []byte).
func (c *Catalog) modelExists(modelID string) bool {
	connectedSet := c.connectedSet()
	for _, p := range c.Providers {
		if !connectedSet[p.ID] {
			continue
		}
		for modelKey, m := range p.Models {
			id := m.ID
			if id == "" {
				id = modelKey
			}
			if id == modelID {
				return true
			}
		}
	}
	return false
}

// resolveModel finds the provider-scoped model ID ("providerID/modelID") for
// the given model, applying the relay remap when relay is active and injected.
// Replaces the former resolveModelFromCatalog (7 params, side-effecting).
//
// relayInjected is pre-resolved by the caller (M3-a) — this function is pure.
func (c *Catalog) resolveModel(modelID string, relayGloballyEnabled, relayInjected bool) string {
	connectedSet := c.connectedSet()
	for _, p := range c.Providers {
		if !connectedSet[p.ID] {
			continue
		}
		for modelKey, m := range p.Models {
			id := m.ID
			if id == "" {
				id = modelKey
			}
			if id != modelID {
				continue
			}
			providerID := p.ID
			if relayGloballyEnabled && relayInjected && isZeroCostOpencode(providerID, m.Cost) {
				providerID = "opencode-relay"
			}
			return providerID + "/" + id
		}
	}
	return modelID
}

func (c *Catalog) connectedSet() map[string]bool {
	set := make(map[string]bool, len(c.Connected))
	for _, id := range c.Connected {
		set[id] = true
	}
	return set
}
