// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// relay_injector.go implements the two-phase relay config injection for Epic 26.
//
// After opencode boots with its default config (Phase 1), this module:
//   1. Checks whether the user has a personal opencode API key — if yes, skips
//      the relay entirely and lets opencode call opencode.ai/zen/v1 directly.
//   2. Calls GET /api/model on the running opencode server to get the live
//      free model list (enabled, cost.input == 0, providerID == "opencode").
//   3. Writes a new agent-config.json with:
//        - disabled_providers: ["opencode"] — removes the built-in provider
//        - provider.opencode-relay — custom OpenAI-compatible provider pointing
//          at the CF Worker relay with the free model list
//   4. Writes the opencode-relay auth entry to auth.json (preserving existing
//      paid provider entries from llm-provider secrets).
//   5. Kills the opencode process — the agentd supervisor restarts it and
//      opencode reads the new config on boot.
//
// The injection is gated by a one-shot flag so it runs exactly once per pod
// lifetime. On subsequent opencode restarts (crash recovery), agentd does NOT
// overwrite the config.
//
// Bypass condition:
//   If auth.json contains an "opencode" entry with key != "public", the user
//   has a personal opencode Zen API key. In that case the relay is bypassed
//   and opencode routes to opencode.ai/zen/v1 using the personal key directly.
//   This is the correct behavior for paying Zen subscribers.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"
)

// relayModel is the minimal model info needed to build the custom provider config.
type relayModel struct {
	ID           string
	Name         string
	ContextLimit int
	OutputLimit  int
}

// buildRelayConfig returns the JSON bytes for an opencode.json config that:
//   - disables the built-in opencode provider
//   - adds a custom opencode-relay provider using @ai-sdk/openai-compatible
//     pointed at relayURL with the given free model list
//
// The returned JSON is suitable for writing to OPENCODE_CONFIG (agent-config.json).
// Callers are responsible for merging with any existing non-relay provider config.
func buildRelayConfig(relayURL string, models []relayModel) ([]byte, error) {
	// Build model map — only context and output limits are valid in the
	// opencode config schema. The limit.input field returned by /api/model
	// is not accepted and causes ConfigInvalidError.
	type modelLimit struct {
		Context int `json:"context,omitempty"`
		Output  int `json:"output,omitempty"`
	}
	type modelEntry struct {
		Name  string     `json:"name"`
		Limit modelLimit `json:"limit,omitempty"`
	}
	modelMap := make(map[string]modelEntry, len(models))
	for _, m := range models {
		modelMap[m.ID] = modelEntry{
			Name:  m.Name,
			Limit: modelLimit{Context: m.ContextLimit, Output: m.OutputLimit},
		}
	}

	type options struct {
		BaseURL string `json:"baseURL"`
		APIKey  string `json:"apiKey"`
	}
	type provider struct {
		Name    string                `json:"name"`
		NPM     string                `json:"npm"`
		Options options               `json:"options"`
		Models  map[string]modelEntry `json:"models"`
	}
	type config struct {
		Schema            string              `json:"$schema"`
		DisabledProviders []string            `json:"disabled_providers"`
		Provider          map[string]provider `json:"provider"`
	}

	cfg := config{
		Schema:            "https://opencode.ai/config.json",
		DisabledProviders: []string{"opencode"},
		Provider: map[string]provider{
			"opencode-relay": {
				Name: "OpenCode Zen (Free)",
				NPM:  "@ai-sdk/openai-compatible",
				Options: options{
					BaseURL: relayURL,
					APIKey:  "public",
				},
				Models: modelMap,
			},
		},
	}

	return json.MarshalIndent(cfg, "", "  ")
}

// shouldSkipRelay reads auth.json at authPath and returns (true, reason) if
// relay injection should be skipped because the user has a personal opencode
// API key. Returns (false, "") if relay should proceed.
//
// The check: auth.json["opencode"]["key"] exists and is not "public".
// "public" is the default anonymous key used for free-tier access. Any other
// value indicates a personal paid key — in that case opencode routes directly.
func shouldSkipRelay(authJSONPath string) (bool, string) {
	data, err := os.ReadFile(authJSONPath)
	if err != nil {
		return false, "" // absent = fresh pod, proceed with relay
	}

	var auth map[string]json.RawMessage
	if err := json.Unmarshal(data, &auth); err != nil {
		return false, ""
	}

	ocRaw, ok := auth["opencode"]
	if !ok {
		return false, ""
	}

	var entry struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(ocRaw, &entry); err != nil {
		return false, ""
	}

	if entry.Key != "" && entry.Key != "public" {
		return true, "personal opencode API key configured — relay bypassed, using key directly"
	}
	return false, ""
}

// fetchFreeModels calls GET /api/model on the opencode server at baseURL,
// authenticating with the given password, and returns models that are:
//   - providerID == "opencode"
//   - enabled == true
//   - cost[0].input == 0  (free tier)
func fetchFreeModels(baseURL, password string) ([]relayModel, error) {
	url := baseURL + "/api/model"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil) //nolint:gosec // G107: internal pod URL
	if err != nil {
		return nil, fmt.Errorf("build GET /api/model request: %w", err)
	}
	req.SetBasicAuth("opencode", password)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET /api/model: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GET /api/model returned %d: %s", resp.StatusCode, body)
	}

	var raw []struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerID"`
		Name       string `json:"name"`
		Enabled    bool   `json:"enabled"`
		Cost       []struct {
			Input float64 `json:"input"`
		} `json:"cost"`
		Limit struct {
			Context int `json:"context"`
			Output  int `json:"output"`
		} `json:"limit"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode /api/model: %w", err)
	}

	var free []relayModel
	for _, m := range raw {
		if m.ProviderID != "opencode" {
			continue
		}
		if !m.Enabled {
			continue
		}
		if len(m.Cost) == 0 || m.Cost[0].Input != 0 {
			continue
		}
		free = append(free, relayModel{
			ID:           m.ID,
			Name:         m.Name,
			ContextLimit: m.Limit.Context,
			OutputLimit:  m.Limit.Output,
		})
	}
	return free, nil
}

// updateAuthJSONForRelay reads auth.json at authPath, adds an "opencode-relay"
// entry with key="public", and writes it back. Existing entries (including paid
// provider keys) are preserved. If the file doesn't exist, it is created.
func updateAuthJSONForRelay(authJSONPath string) error {
	var auth map[string]json.RawMessage

	data, err := os.ReadFile(authJSONPath)
	if err == nil && len(data) > 0 {
		if jsonErr := json.Unmarshal(data, &auth); jsonErr != nil {
			auth = nil
		}
	}
	if auth == nil {
		auth = make(map[string]json.RawMessage)
	}

	entry, _ := json.Marshal(map[string]string{"type": "api", "key": "public"})
	auth["opencode-relay"] = entry

	updated, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal auth.json: %w", err)
	}
	return os.WriteFile(authJSONPath, updated, 0o600)
}

// relayInjectorConfig holds the parameters for startRelayInjector.
type relayInjectorConfig struct {
	// RelayURL is the full CF Worker URL including secret path segment,
	// e.g. https://relay.safespaces.dev/<secret>. Empty → no-op.
	RelayURL string
	// OpenCodeBaseURL is the http://localhost:PORT base for opencode API calls.
	OpenCodeBaseURL string
	// OpenCodePassword is the Basic auth password for opencode.
	OpenCodePassword string
	// AgentConfigPath is the path to write agent-config.json.
	AgentConfigPath string
	// AuthJSONPath is the path to opencode's auth.json.
	AuthJSONPath string
	// KillOpenCode is called to trigger opencode process restart after config
	// is written. The supervisor restarts opencode, which reads the new config.
	KillOpenCode func()
	// HealthCheck returns true when opencode is healthy and ready to serve API calls.
	HealthCheck func() bool
}

// startRelayInjector starts a background goroutine that waits for opencode to
// be healthy, then applies the relay config (Phase 2 injection). It runs at
// most once per pod lifetime.
//
// If INFERENCE_RELAY_BASEURL is not set or the user has a personal opencode
// API key, the goroutine exits without making any changes.
func startRelayInjector(cfg relayInjectorConfig) {
	if cfg.RelayURL == "" {
		return
	}
	go func() {
		// Wait up to 5 minutes for opencode to be healthy.
		deadline := time.Now().Add(5 * time.Minute)
		for time.Now().Before(deadline) {
			if cfg.HealthCheck() {
				break
			}
			time.Sleep(2 * time.Second)
		}
		if !cfg.HealthCheck() {
			log.Warn("relay injector: opencode did not become healthy in time, skipping relay config")
			return
		}

		// Check whether to skip relay.
		if skip, reason := shouldSkipRelay(cfg.AuthJSONPath); skip {
			log.Info("relay injector: skipping relay injection", zap.String("reason", reason))
			return
		}

		// Fetch the live free model list from the running opencode.
		models, err := fetchFreeModels(cfg.OpenCodeBaseURL, cfg.OpenCodePassword)
		if err != nil {
			log.Warn("relay injector: failed to fetch free models, skipping", zap.Error(err))
			return
		}
		if len(models) == 0 {
			log.Warn("relay injector: no free opencode models found, skipping relay config")
			return
		}
		log.Info("relay injector: fetched free models", zap.Int("count", len(models)))

		// Build and write the relay config.
		cfgBytes, err := buildRelayConfig(cfg.RelayURL, models)
		if err != nil {
			log.Warn("relay injector: failed to build relay config", zap.Error(err))
			return
		}
		if err := os.WriteFile(cfg.AgentConfigPath, cfgBytes, 0o600); err != nil {
			log.Warn("relay injector: failed to write agent config", zap.Error(err))
			return
		}
		log.Info("relay injector: wrote relay config",
			zap.String("path", cfg.AgentConfigPath),
			zap.Int("models", len(models)),
			zap.String("relayURL", cfg.RelayURL[:min(len(cfg.RelayURL), 50)]))

		// Update auth.json with the opencode-relay entry.
		if err := updateAuthJSONForRelay(cfg.AuthJSONPath); err != nil {
			log.Warn("relay injector: failed to update auth.json", zap.Error(err))
			return
		}
		log.Info("relay injector: updated auth.json with opencode-relay entry")

		// Kill opencode — the supervisor restarts it and reads the new config.
		cfg.KillOpenCode()
		log.Info("relay injector: triggered opencode restart to apply relay config")
	}()
}
