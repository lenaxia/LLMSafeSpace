// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

// FormatOpenCodeConfig renders a slice of validated LLMProviderData into
// opencode's config JSON format. The output is deterministic: provider
// keys are sorted alphabetically.
//
// The function is pure — no side effects, no filesystem access.
func FormatOpenCodeConfig(providers []secrets.LLMProviderData) ([]byte, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("FormatOpenCodeConfig: no providers to render")
	}

	cfg := opencodeConfig{
		Schema:    "https://opencode.ai/config.json",
		Providers: make(map[string]*opencodeProvider, len(providers)),
	}

	for _, p := range providers {
		op := &opencodeProvider{
			Options: &opencodeOptions{
				AISDK: &opencodeAISDK{
					Provider: map[string]string{"apiKey": p.APIKey},
				},
			},
		}

		if p.BaseURL != "" {
			op.Endpoint = endpointForProvider(p.Provider, p.BaseURL)
		}

		if len(p.Models) > 0 {
			op.Models = make(map[string]*opencodeModel, len(p.Models))
			for _, m := range p.Models {
				om := &opencodeModel{}
				if m.Label != "" {
					om.Name = m.Label
				}
				op.Models[m.ID] = om
			}
		}

		cfg.Providers[p.Provider] = op

		// First provider with a Default wins.
		if cfg.Model == "" && p.Default != "" {
			cfg.Model = p.Default
		}
	}

	return marshalDeterministic(cfg)
}

// endpointForProvider returns the correct endpoint object based on provider name.
func endpointForProvider(provider, baseURL string) map[string]string {
	switch provider {
	case "anthropic":
		return map[string]string{"type": "anthropic/messages", "url": baseURL}
	case "openai":
		return map[string]string{"type": "openai/responses", "url": baseURL}
	default:
		return map[string]string{"type": "aisdk", "package": "@ai-sdk/openai-compatible", "url": baseURL}
	}
}

// marshalDeterministic produces JSON with sorted map keys for reproducible output.
func marshalDeterministic(cfg opencodeConfig) ([]byte, error) {
	// Build ordered output manually to guarantee key order in providers map.
	type orderedOutput struct {
		Schema    string                      `json:"$schema"`
		Providers map[string]*opencodeProvider `json:"providers"`
		Model     string                      `json:"model,omitempty"`
	}

	out := orderedOutput{
		Schema:    cfg.Schema,
		Providers: cfg.Providers,
		Model:     cfg.Model,
	}

	// json.Marshal uses sorted keys for maps by default in Go.
	return json.MarshalIndent(out, "", "  ")
}

// --- internal types (not exported) ---

type opencodeConfig struct {
	Schema    string
	Providers map[string]*opencodeProvider
	Model     string
}

type opencodeProvider struct {
	Endpoint map[string]string       `json:"endpoint,omitempty"`
	Options  *opencodeOptions        `json:"options"`
	Models   map[string]*opencodeModel `json:"models,omitempty"`
}

type opencodeOptions struct {
	AISDK *opencodeAISDK `json:"aisdk"`
}

type opencodeAISDK struct {
	Provider map[string]string `json:"provider"`
}

type opencodeModel struct {
	Name string `json:"name,omitempty"`
}

// Ensure stable iteration order for deterministic output in tests.
var _ = sort.Strings
