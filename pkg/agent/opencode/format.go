// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"encoding/json"
	"fmt"

	"github.com/lenaxia/llmsafespace/pkg/secrets"
)

// FormatOpenCodeConfig renders a slice of validated LLMProviderData into
// the JSON shape opencode 1.15.12 accepts.
//
// **Schema** (evidence-driven; established by live cluster probe in
// worklog 0128. Do NOT change without re-validating against a running
// opencode):
//
//	{
//	  "$schema": "https://opencode.ai/config.json",
//	  "provider": {                          <-- SINGULAR (not "providers")
//	    "<id>": {
//	      "options": {                       <-- direct, NO aisdk wrapper
//	        "apiKey":  "...",                <-- the credential
//	        "baseURL": "..."                 <-- in options, NOT in a
//	      },                                     separate `endpoint` object
//	      "models": { "<id>": { "name": "..." } }
//	    }
//	  },
//	  "model": "<id>/<modelID>"
//	}
//
// What pre-fix code generated, and why opencode rejected it:
//   - top-level key was `providers` (plural) → ConfigInvalidError
//   - apiKey lived at options.aisdk.provider.apiKey → ConfigInvalidError
//   - baseURL lived at endpoint.url → silently ignored (chat requests
//     went to api.openai.com instead of the operator's endpoint)
//
// The function is pure — no side effects, no filesystem access.
//
// Returns an error if providers is empty (callers MUST check for this
// — opencode treats an empty config differently and a "no-op write of
// an empty config" is a bug).
func FormatOpenCodeConfig(providers []secrets.LLMProviderData) ([]byte, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("FormatOpenCodeConfig: no providers to render")
	}

	cfg := opencodeConfig{
		Schema:   "https://opencode.ai/config.json",
		Provider: make(map[string]*opencodeProvider, len(providers)),
	}

	for _, p := range providers {
		op := &opencodeProvider{
			Options: opencodeOptions{
				APIKey:  p.APIKey,
				BaseURL: p.BaseURL, // empty string is omitted via omitempty
			},
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

		cfg.Provider[p.Provider] = op

		// First provider with a Default wins.
		if cfg.Model == "" && p.Default != "" {
			cfg.Model = p.Default
		}
	}

	// json.Marshal is deterministic for maps (sorted keys) since Go 1.12.
	return json.MarshalIndent(cfg, "", "  ")
}

// --- internal types (not exported) ---
//
// JSON tag ordering matters for the snapshot test. Go's json package
// emits fields in struct-declaration order (NOT alphabetical), so the
// field order below is the wire-format order.

type opencodeConfig struct {
	Schema   string                       `json:"$schema"`
	Provider map[string]*opencodeProvider `json:"provider"`
	Model    string                       `json:"model,omitempty"`
}

type opencodeProvider struct {
	Options opencodeOptions           `json:"options"`
	Models  map[string]*opencodeModel `json:"models,omitempty"`
}

type opencodeOptions struct {
	APIKey  string `json:"apiKey,omitempty"`
	BaseURL string `json:"baseURL,omitempty"`
}

type opencodeModel struct {
	Name string `json:"name,omitempty"`
}
