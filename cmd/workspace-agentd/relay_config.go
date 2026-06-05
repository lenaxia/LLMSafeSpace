// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"os"
)

// injectRelayConfig writes the relay baseURL into the opencode config file
// at cfgPath under provider.opencode.options.baseURL. If the file already
// exists, existing keys (e.g. model, $schema) are preserved. If it does not
// exist, a minimal config containing only the relay baseURL is created.
//
// This is called at agentd startup when LLMSAFESPACE_RELAY_URL is set, before
// opencode is launched. It ensures opencode routes its LLM requests through
// the relay inference endpoint (localhost:agentdPort/relay/inference) which
// the relay proxy intercepts and forwards to the connected client.
//
// The config schema is opencode 1.15.12:
//
//	{
//	  "$schema": "https://opencode.ai/config.json",
//	  "provider": {
//	    "opencode": {
//	      "options": { "baseURL": "<relayBaseURL>" }
//	    }
//	  }
//	}
func injectRelayConfig(cfgPath, relayBaseURL string) error {
	cfg := make(map[string]json.RawMessage)

	// Load existing config if present; ignore errors (absent = start fresh).
	if existing, err := os.ReadFile(cfgPath); err == nil && len(existing) > 0 {
		_ = json.Unmarshal(existing, &cfg)
	}

	if _, ok := cfg["$schema"]; !ok {
		schema, _ := json.Marshal("https://opencode.ai/config.json")
		cfg["$schema"] = schema
	}

	// Build the provider block, merging with any existing provider config.
	var providers map[string]json.RawMessage
	if raw, ok := cfg["provider"]; ok {
		_ = json.Unmarshal(raw, &providers)
	}
	if providers == nil {
		providers = make(map[string]json.RawMessage)
	}

	// Merge relay baseURL into opencode provider options.
	var opencodeProvider map[string]json.RawMessage
	if raw, ok := providers["opencode"]; ok {
		_ = json.Unmarshal(raw, &opencodeProvider)
	}
	if opencodeProvider == nil {
		opencodeProvider = make(map[string]json.RawMessage)
	}

	var options map[string]string
	if raw, ok := opencodeProvider["options"]; ok {
		_ = json.Unmarshal(raw, &options)
	}
	if options == nil {
		options = make(map[string]string)
	}
	options["baseURL"] = relayBaseURL

	optionsJSON, err := json.Marshal(options)
	if err != nil {
		return err
	}
	opencodeProvider["options"] = optionsJSON

	providerJSON, err := json.Marshal(opencodeProvider)
	if err != nil {
		return err
	}
	providers["opencode"] = providerJSON

	providersJSON, err := json.Marshal(providers)
	if err != nil {
		return err
	}
	cfg["provider"] = providersJSON

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfgPath, out, 0o600)
}
