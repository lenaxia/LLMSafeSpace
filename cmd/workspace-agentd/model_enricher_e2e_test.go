// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// model_enricher_e2e_test.go exercises the full chain through the REAL
// workspace-agentd materialize subprocess:
//
//	secrets.json with an llm-provider that has BaseURL but no Models
//	  → workspace-agentd materialize (real binary)
//	  → enrichProviderModels (real HTTP fetch from /models endpoint)
//	  → FormatOpenCodeConfig (renders provider+models)
//	  → agent-config.json (the file opencode reads)
//
// The existing enricher tests (model_enricher_test.go) stop at the enricher
// RETURN VALUE. No test proves the fetched models actually land in
// agent-config.json through the real materialize → enrich → flush chain.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_ModelEnricher_ModelsLandInAgentConfig runs the REAL materialize
// binary against a fake /models endpoint and asserts the fetched models
// appear in agent-config.json with the correct npm attribute.
func TestE2E_ModelEnricher_ModelsLandInAgentConfig(t *testing.T) {
	bin := buildAgentdBinary(t)
	dir := t.TempDir()

	// Fake /models endpoint returning three model IDs.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/models", r.URL.Path)
		assert.Equal(t, "Bearer sk-enrich-test", r.Header.Get("Authorization"))
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{
				{"id": "glm-5.1"},
				{"id": "glm-5.2"},
				{"id": "deepseek-v3"},
			},
		})
	}))
	defer srv.Close()

	// secrets.json with an llm-provider that has BaseURL but no Models.
	// plaintext must be a JSON STRING (double-encoded) containing the provider JSON.
	providerJSON, _ := json.Marshal(map[string]any{
		"kind":    "custom-llm",
		"slug":    "custom-llm",
		"apiKey":  "sk-enrich-test",
		"baseURL": srv.URL,
	})
	secretsBatch, err := json.Marshal([]map[string]any{{
		"type":      "llm-provider",
		"name":      "custom-llm",
		"plaintext": string(providerJSON),
	}})
	require.NoError(t, err)
	secretsPath := filepath.Join(dir, "secrets.json")
	require.NoError(t, os.WriteFile(secretsPath, secretsBatch, 0o600))

	agentCfg := filepath.Join(dir, "agent-config.json")
	exit, _, stderr := runMaterializeSubcommand(t, bin, secretsPath,
		filepath.Join(dir, "secrets"),
		filepath.Join(dir, ".ssh"),
		agentCfg,
		filepath.Join(dir, "env"),
		filepath.Join(dir, ".git-credentials"))
	require.Equal(t, 0, exit, "materialize must succeed; stderr=%s", stderr)

	raw, err := os.ReadFile(agentCfg)
	require.NoError(t, err, "agent-config.json must exist after materialize; stderr=%s", stderr)

	var cfg struct {
		Provider map[string]json.RawMessage `json:"provider"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg))
	require.Contains(t, cfg.Provider, "custom-llm",
		"the enriched provider must appear in agent-config.json")

	var entry struct {
		Options struct {
			APIKey  string `json:"apiKey"`
			BaseURL string `json:"baseURL"`
		} `json:"options"`
		NPM    string              `json:"npm"`
		Models map[string]struct{} `json:"models"`
	}
	require.NoError(t, json.Unmarshal(cfg.Provider["custom-llm"], &entry))
	assert.Equal(t, "sk-enrich-test", entry.Options.APIKey)
	assert.Equal(t, srv.URL, entry.Options.BaseURL)
	assert.Equal(t, "@ai-sdk/openai-compatible", entry.NPM,
		"custom-BaseURL provider must set npm so opencode uses the OpenAI-compatible SDK")
	assert.Len(t, entry.Models, 3,
		"all three fetched models must appear in agent-config.json")
}

// TestE2E_ModelEnricher_FetchFail_StillWritesConfig is the unhappy path: when
// the /models endpoint returns 401, the provider must STILL be written to
// agent-config.json (with no models) so opencode registers it. A regression
// that drops the provider entirely on enrich failure would silently remove
// the user's credential.
func TestE2E_ModelEnricher_FetchFail_StillWritesConfig(t *testing.T) {
	bin := buildAgentdBinary(t)
	dir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	providerJSON, _ := json.Marshal(map[string]any{
		"kind":    "failing-endpoint",
		"slug":    "failing-endpoint",
		"apiKey":  "sk-still-here",
		"baseURL": srv.URL,
	})
	secretsBatch, err := json.Marshal([]map[string]any{{
		"type":      "llm-provider",
		"name":      "failing-endpoint",
		"plaintext": string(providerJSON),
	}})
	require.NoError(t, err)
	secretsPath := filepath.Join(dir, "secrets.json")
	require.NoError(t, os.WriteFile(secretsPath, secretsBatch, 0o600))

	agentCfg := filepath.Join(dir, "agent-config.json")
	exit, _, stderr := runMaterializeSubcommand(t, bin, secretsPath,
		filepath.Join(dir, "secrets"),
		filepath.Join(dir, ".ssh"),
		agentCfg,
		filepath.Join(dir, "env"),
		filepath.Join(dir, ".git-credentials"))
	require.Equal(t, 0, exit,
		"materialize must succeed even when /models fetch fails (best-effort); stderr=%s", stderr)

	raw, err := os.ReadFile(agentCfg)
	require.NoError(t, err)
	var cfg struct {
		Provider map[string]json.RawMessage `json:"provider"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg))
	require.Contains(t, cfg.Provider, "failing-endpoint",
		"provider must STILL appear in agent-config.json even when /models fetch failed")

	var entry struct {
		Options struct {
			APIKey string `json:"apiKey"`
		} `json:"options"`
		Models map[string]json.RawMessage `json:"models"`
	}
	require.NoError(t, json.Unmarshal(cfg.Provider["failing-endpoint"], &entry))
	assert.Equal(t, "sk-still-here", entry.Options.APIKey,
		"apiKey must survive the enrichment failure")
	assert.Empty(t, entry.Models, "no models must be present when the fetch failed")
}
