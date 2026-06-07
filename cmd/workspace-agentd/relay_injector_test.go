// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// Tests for relay_injector.go — the Epic 26 post-boot relay config injection.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- buildRelayConfig tests ---

// TestBuildRelayConfig_WritesDisabledAndCustomProvider verifies that
// buildRelayConfig produces a valid opencode.json config with:
//   - disabled_providers: ["opencode"]
//   - provider.opencode-relay.options.baseURL = relay URL
//   - provider.opencode-relay.npm = "@ai-sdk/openai-compatible"
//   - provider.opencode-relay.models = the given free model list
func TestBuildRelayConfig_WritesDisabledAndCustomProvider(t *testing.T) {
	dir := t.TempDir()
	agentConfigPath := filepath.Join(dir, "agent-config.json")
	relayURL := "https://relay.safespaces.dev/secret123"
	models := []relayModel{
		{ID: "nemotron-3-ultra-free", Name: "Nemotron 3 Ultra Free", ContextLimit: 1000000, OutputLimit: 128000},
		{ID: "glm-5-free", Name: "GLM-5 Free", ContextLimit: 204800, OutputLimit: 131072},
	}

	cfg, err := buildRelayConfig(agentConfigPath, relayURL, models)
	require.NoError(t, err)

	var parsed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(cfg, &parsed))

	// disabled_providers must contain "opencode"
	var disabled []string
	require.NoError(t, json.Unmarshal(parsed["disabled_providers"], &disabled))
	assert.Contains(t, disabled, "opencode")

	// provider.opencode-relay must exist with correct fields
	var providers map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(parsed["provider"], &providers))

	relayProvider, ok := providers["opencode-relay"]
	require.True(t, ok, "opencode-relay provider must be present")

	var rp map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(relayProvider, &rp))

	var npm string
	require.NoError(t, json.Unmarshal(rp["npm"], &npm))
	assert.Equal(t, "@ai-sdk/openai-compatible", npm)

	var options map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rp["options"], &options))
	var baseURL string
	require.NoError(t, json.Unmarshal(options["baseURL"], &baseURL))
	assert.Equal(t, relayURL, baseURL)

	// Models must be present with context+output limits (no input)
	var modelsCfg map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rp["models"], &modelsCfg))
	assert.Len(t, modelsCfg, 2)

	var nemotron map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(modelsCfg["nemotron-3-ultra-free"], &nemotron))

	var limit map[string]int
	require.NoError(t, json.Unmarshal(nemotron["limit"], &limit))
	assert.Equal(t, 1000000, limit["context"])
	assert.Equal(t, 128000, limit["output"])
	_, hasInput := limit["input"]
	assert.False(t, hasInput, "limit.input must be absent — opencode config schema rejects it")
}

// TestBuildRelayConfig_MergesExistingProviders verifies that buildRelayConfig
// preserves existing provider entries (e.g. openai written by FlushProviders)
// rather than replacing the entire agent-config.json. This is the fix for the
// bug where the relay injector clobbered the openai provider config.
func TestBuildRelayConfig_MergesExistingProviders(t *testing.T) {
	dir := t.TempDir()
	agentConfigPath := filepath.Join(dir, "agent-config.json")

	// Write an existing config as FlushProviders would (openai provider).
	existing := `{
		"$schema": "https://opencode.ai/config.json",
		"provider": {
			"openai": {
				"options": {
					"apiKey": "sk-test-key",
					"baseURL": "https://ai.thekao.cloud/v1"
				}
			}
		}
	}`
	require.NoError(t, os.WriteFile(agentConfigPath, []byte(existing), 0o600))

	cfg, err := buildRelayConfig(agentConfigPath,
		"https://relay.safespaces.dev/secret",
		[]relayModel{{ID: "big-pickle", Name: "Big Pickle"}})
	require.NoError(t, err)

	var parsed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(cfg, &parsed))

	var providers map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(parsed["provider"], &providers))

	// openai provider from existing config must survive
	_, hasOpenAI := providers["openai"]
	assert.True(t, hasOpenAI, "openai provider must be preserved after merge")

	// opencode-relay must be added
	_, hasRelay := providers["opencode-relay"]
	assert.True(t, hasRelay, "opencode-relay provider must be added")
}

// TestBuildRelayConfig_WorksWithoutExistingConfig verifies that buildRelayConfig
// handles a missing agent-config.json gracefully (fresh pod, first boot).
func TestBuildRelayConfig_WorksWithoutExistingConfig(t *testing.T) {
	dir := t.TempDir()
	agentConfigPath := filepath.Join(dir, "agent-config.json")
	// Deliberately do NOT create the file.

	cfg, err := buildRelayConfig(agentConfigPath,
		"https://relay.example.com/s",
		[]relayModel{{ID: "m", Name: "M", ContextLimit: 1000, OutputLimit: 100}})
	require.NoError(t, err)

	var parsed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(cfg, &parsed))

	// Must still have the required relay keys.
	assert.Contains(t, parsed, "$schema")
	assert.Contains(t, parsed, "disabled_providers")
	assert.Contains(t, parsed, "provider")
}

// --- shouldSkipRelay tests ---

func TestShouldSkipRelay_SkipsWhenPersonalKey(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	require.NoError(t, os.WriteFile(authPath, []byte(`{
		"opencode": {"type": "api", "key": "sk-personal-key-abc123"}
	}`), 0o600))

	skip, reason := shouldSkipRelay(authPath)
	assert.True(t, skip)
	assert.Contains(t, reason, "personal")
}

func TestShouldSkipRelay_DoesNotSkipWithPublicKey(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	require.NoError(t, os.WriteFile(authPath, []byte(`{
		"opencode": {"type": "api", "key": "public"}
	}`), 0o600))

	skip, _ := shouldSkipRelay(authPath)
	assert.False(t, skip)
}

func TestShouldSkipRelay_DoesNotSkipWithNoEntry(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	require.NoError(t, os.WriteFile(authPath, []byte(`{}`), 0o600))

	skip, _ := shouldSkipRelay(authPath)
	assert.False(t, skip)
}

func TestShouldSkipRelay_DoesNotSkipWithMissingFile(t *testing.T) {
	skip, _ := shouldSkipRelay("/nonexistent/auth.json")
	assert.False(t, skip)
}

// --- fetchFreeModels tests ---

func TestFetchFreeModels_FiltersCorrectly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/provider" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"connected": ["opencode"],
				"all": [
					{"id":"opencode","models":{
						"free-1":      {"id":"free-1","name":"Free 1","cost":{"input":0,"output":0},"limit":{"context":100000,"output":10000}},
						"paid-1":      {"id":"paid-1","name":"Paid 1","cost":{"input":3,"output":15},"limit":{"context":200000,"output":20000}},
						"free-nokey":  {"id":"free-nokey","name":"Free Nokey","cost":{"input":0,"output":0},"limit":{"context":50000,"output":5000}}
					}},
					{"id":"anthropic","models":{
						"claude":      {"id":"claude","name":"Claude","cost":{"input":0,"output":0},"limit":{"context":200000,"output":10000}}
					}}
				]
			}`))
		}
	}))
	defer srv.Close()

	models, err := fetchFreeModels(srv.URL, "testpassword")
	require.NoError(t, err)
	// opencode provider: free-1 and free-nokey pass (cost.input==0); paid-1 excluded
	// anthropic: excluded (not in connected[])
	require.Len(t, models, 2)
	ids := make(map[string]bool)
	for _, m := range models {
		ids[m.ID] = true
	}
	assert.True(t, ids["free-1"], "free-1 must be included")
	assert.True(t, ids["free-nokey"], "free-nokey must be included")
	assert.False(t, ids["paid-1"], "paid-1 must be excluded (cost.input>0)")
	assert.False(t, ids["claude"], "anthropic/claude must be excluded (not connected)")
	// Validate limits on a specific model
	for _, m := range models {
		if m.ID == "free-1" {
			assert.Equal(t, 100000, m.ContextLimit)
			assert.Equal(t, 10000, m.OutputLimit)
		}
	}
}

func TestFetchFreeModels_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"crashed"}`))
	}))
	defer srv.Close()

	_, err := fetchFreeModels(srv.URL, "pw")
	assert.Error(t, err)
}

// --- updateAuthJSONForRelay tests ---

func TestUpdateAuthJSONForRelay_AddsRelayEntry(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")

	existing := map[string]interface{}{
		"opencode":  map[string]string{"type": "api", "key": "public"},
		"anthropic": map[string]string{"type": "api", "key": "sk-ant-real-key"},
	}
	data, _ := json.Marshal(existing)
	require.NoError(t, os.WriteFile(authPath, data, 0o600))

	require.NoError(t, updateAuthJSONForRelay(authPath))

	var updated map[string]map[string]string
	raw, _ := os.ReadFile(authPath)
	require.NoError(t, json.Unmarshal(raw, &updated))

	assert.Equal(t, "public", updated["opencode-relay"]["key"])
	assert.Equal(t, "sk-ant-real-key", updated["anthropic"]["key"],
		"existing anthropic key must be preserved")
}

func TestUpdateAuthJSONForRelay_CreatesFileIfMissing(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")

	require.NoError(t, updateAuthJSONForRelay(authPath))

	var updated map[string]map[string]string
	raw, _ := os.ReadFile(authPath)
	require.NoError(t, json.Unmarshal(raw, &updated))
	assert.Equal(t, "public", updated["opencode-relay"]["key"])
}

// --- startRelayInjector integration tests ---

func TestStartRelayInjector_SkipsWhenNoRelayURL(t *testing.T) {
	killed := false
	startRelayInjector(relayInjectorConfig{
		RelayURL:     "",
		KillOpenCode: func() { killed = true },
		HealthCheck:  func() bool { return true },
	})
	time.Sleep(50 * time.Millisecond)
	assert.False(t, killed, "KillOpenCode must not be called when RelayURL is empty")
}

func TestStartRelayInjector_SkipsWhenPersonalKey(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	require.NoError(t, os.WriteFile(authPath,
		[]byte(`{"opencode":{"type":"api","key":"sk-personal-abc123"}}`), 0o600))

	killed := false
	startRelayInjector(relayInjectorConfig{
		RelayURL:     "https://relay.safespaces.dev/secret",
		AuthJSONPath: authPath,
		HealthCheck:  func() bool { return true },
		KillOpenCode: func() { killed = true },
	})
	time.Sleep(100 * time.Millisecond)
	assert.False(t, killed, "KillOpenCode must not be called when user has personal key")
}

func TestStartRelayInjector_WritesConfigAndKills(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent-config.json")
	authPath := filepath.Join(dir, "auth.json")
	require.NoError(t, os.WriteFile(authPath,
		[]byte(`{"opencode":{"type":"api","key":"public"}}`), 0o600))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"connected": ["opencode"],
			"all": [
				{"id":"opencode","models":{
					"free-model": {"id":"free-model","name":"Free Model","cost":{"input":0,"output":0},"limit":{"context":100000,"output":10000}}
				}}
			]
		}`))
	}))
	defer srv.Close()

	killed := make(chan struct{}, 1)
	startRelayInjector(relayInjectorConfig{
		RelayURL:         "https://relay.safespaces.dev/mysecret",
		OpenCodeBaseURL:  srv.URL,
		OpenCodePassword: "testpw",
		AgentConfigPath:  cfgPath,
		AuthJSONPath:     authPath,
		HealthCheck:      func() bool { return true },
		KillOpenCode:     func() { close(killed) },
	})

	select {
	case <-killed:
	case <-time.After(2 * time.Second):
		t.Fatal("KillOpenCode was not called within 2s")
	}

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	var cfg map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &cfg))

	var disabled []string
	require.NoError(t, json.Unmarshal(cfg["disabled_providers"], &disabled))
	assert.Contains(t, disabled, "opencode")

	authData, _ := os.ReadFile(authPath)
	var auth map[string]map[string]string
	require.NoError(t, json.Unmarshal(authData, &auth))
	assert.Equal(t, "public", auth["opencode-relay"]["key"])
}

// TestStartRelayInjector_RetriesWhenZeroModels verifies the race-condition fix:
// when the first /provider call returns opencode connected but no free models
// (catalog not yet fully initialized), the relay injector retries rather than
// permanently skipping.
func TestStartRelayInjector_RetriesWhenZeroModels(t *testing.T) {
	// First request: opencode connected but no models yet.
	// Second request: real free model present.
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health":
			_ = json.NewEncoder(w).Encode(map[string]bool{"healthy": true})
		case "/provider":
			callCount++
			w.Header().Set("Content-Type", "application/json")
			if callCount == 1 {
				// First call: opencode connected but empty model map
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"connected": []string{"opencode"},
					"all": []map[string]interface{}{
						{"id": "opencode", "models": map[string]interface{}{}},
					},
				})
			} else {
				// Second call: real free model present
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"connected": []string{"opencode"},
					"all": []map[string]interface{}{
						{"id": "opencode", "models": map[string]interface{}{
							"glm-5.1-free": map[string]interface{}{
								"id": "glm-5.1-free", "name": "GLM 5.1 Free",
								"cost":  map[string]float64{"input": 0, "output": 0},
								"limit": map[string]int{"context": 8192, "output": 2048},
							},
						}},
					},
				})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	agentConfigPath := filepath.Join(dir, "agent-config.json")
	authPath := filepath.Join(dir, "auth.json")
	killed := make(chan struct{})

	cfg := relayInjectorConfig{
		RelayURL:         "https://relay.test/secret",
		OpenCodeBaseURL:  srv.URL,
		OpenCodePassword: "pw",
		AgentConfigPath:  agentConfigPath,
		AuthJSONPath:     authPath,
		HealthCheck:      func() bool { return true },
		KillOpenCode:     func() { close(killed) },
	}

	startRelayInjector(cfg)

	select {
	case <-killed:
		// Success — relay injector retried and found models on second attempt
	case <-time.After(30 * time.Second):
		t.Fatal("relay injector did not retry after 0-model response within 30s")
	}

	// Config must be written
	_, err := os.ReadFile(agentConfigPath)
	require.NoError(t, err)

	// Both calls were made (retry happened)
	assert.Equal(t, 2, callCount, "expected exactly 2 /provider calls (initial + retry)")
}
