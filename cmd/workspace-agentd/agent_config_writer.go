// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// agent_config_writer.go implements the SINGLE writer of agent-config.json.
//
// Before US-46.10, four independent code paths wrote agent-config.json:
//   1. FlushProviders (boot + reload) — provider credentials only
//   2. applyWorkspaceConfig (boot subcommand) — adds model key
//   3. startRelayInjector (~T+7s) — merges relay provider + disabled_providers
//   4. reloadSecretsHandler re-merge — restores relay after FlushProviders clobbers it
//
// None coordinated atomically. The design relied on strict boot ordering,
// reloadMu serialization, and opencode not hot-reloading the file. This was
// fragile — a future change that reorders the boot sequence or adds a new
// write path could reintroduce relay clobbering.
//
// The AgentConfigWriter eliminates this fragility by being the sole writer.
// It holds three sources — providers, model, relay — and Rebuild() merges
// them into a complete config written atomically via temp-file + os.Rename.
//
// Boot initialization: NewAgentConfigWriter reads the existing file (written
// by the materialize subcommand) and captures the provider map and model as
// initial sources. This lets the relay injector merge into them without
// re-deriving provider credentials.
//
// The materialize subcommand still writes the file directly (it is a separate
// process before agentd starts). But once agentd is running, ALL writes go
// through the writer. See README-LLM.md "Relay Config Subsystem" for the
// full write-sequence documentation.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/lenaxia/llmsafespaces/pkg/agentd"
)

// relaySource holds the relay URL and free model list that the relay
// injector discovered from opencode's /provider endpoint.
type relaySource struct {
	url    string
	models []relayModel
}

// AgentConfigWriter is the single writer of agent-config.json within the
// agentd process. All config changes (provider credentials, model
// selection, relay injection) go through SetProviders/SetModel/SetRelay
// followed by Rebuild.
//
// Thread-safe: all methods acquire mu. Rebuild serializes the
// read-merge-write cycle so concurrent reloads and relay injection
// cannot interleave.
type AgentConfigWriter struct {
	mu          sync.Mutex
	path        string
	providerRaw json.RawMessage // raw "provider" map JSON from FormatOpenCodeConfig; nil = no providers
	model       string          // fully-qualified "providerID/modelID" form; "" = no model
	relay       *relaySource    // nil = relay not yet injected / skipped
	adminPrompt string          // admin-configured system prompt from agentd.AdminPromptPath; "" = none
	agentsRaw   json.RawMessage // existing "agents" config from loadExisting, preserved across rebuilds
}

// newAgentConfigWriter creates the writer and initializes its sources
// from the existing agent-config.json file (written by the materialize
// subcommand at boot). If the file is absent or corrupt, sources start
// empty and the first Rebuild() creates a fresh file.
func newAgentConfigWriter(path string) *AgentConfigWriter {
	w := &AgentConfigWriter{path: path}
	w.loadExisting()
	w.loadAdminPrompt()
	return w
}

// loadExisting reads the current agent-config.json and captures the
// provider map and model as sources. Called once at construction.
// Silent on error — a missing or corrupt file means the writer starts
// empty, which is correct for zero-credential users.
func (w *AgentConfigWriter) loadExisting() {
	data, err := os.ReadFile(w.path)
	if err != nil || len(data) == 0 {
		return
	}
	var cfg struct {
		Provider json.RawMessage `json:"provider"`
		Model    string          `json:"model,omitempty"`
		Agents   json.RawMessage `json:"agents,omitempty"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return
	}
	w.providerRaw = cfg.Provider
	w.model = cfg.Model
	w.agentsRaw = cfg.Agents

	// 2026-06-23 cold-start optimization (item #1a, Phase D): detect
	// a pre-boot-injected relay block and set the writer's relay
	// source so hasRelay() returns true. Without this, the legacy
	// in-pod startRelayInjector goroutine would think no relay is
	// configured (writer.relay == nil) and run its full
	// fetch+kill+restart cycle redundantly, defeating the entire
	// point of Phase C.
	//
	// We extract just enough info to satisfy hasRelay() — the actual
	// relay config is already on disk, so we don't need to round-trip
	// the full URL or model list back into the writer's source. A
	// sentinel non-nil relaySource is sufficient, but we populate
	// fields where we can so any future caller that introspects the
	// writer sees consistent state.
	if len(cfg.Provider) > 0 {
		var providers map[string]json.RawMessage
		if err := json.Unmarshal(cfg.Provider, &providers); err == nil {
			if relayRaw, ok := providers["opencode-relay"]; ok {
				w.relay = parseRelayFromExisting(relayRaw)
			}
		}
	}
}

// loadAdminPrompt reads the admin-configured system prompt written by the
// bootstrap subcommand to agentd.AdminPromptPath. Loaded once at init;
// persists across all rebuilds. Changes take effect on next pod boot
// (design decision: no hot-reload).
func (w *AgentConfigWriter) loadAdminPrompt() {
	data, err := os.ReadFile(agentd.AdminPromptPath)
	if err != nil || len(data) == 0 {
		return
	}
	w.adminPrompt = string(data)
}

// parseRelayFromExisting extracts URL + models from a pre-injected
// opencode-relay provider block. Used by loadExisting to make the
// writer aware of a relay block written by the materialize subcommand
// (Phase C) before agentd started.
//
// Returns a populated *relaySource on success, or a sentinel
// non-nil source with empty fields if extraction fails — the
// non-nil-ness is what matters for hasRelay().
func parseRelayFromExisting(relayRaw json.RawMessage) *relaySource {
	var entry struct {
		Options struct {
			BaseURL string `json:"baseURL"`
		} `json:"options"`
		Models map[string]struct {
			Name  string `json:"name"`
			Limit struct {
				Context int `json:"context"`
				Output  int `json:"output"`
			} `json:"limit"`
		} `json:"models"`
	}
	src := &relaySource{}
	if err := json.Unmarshal(relayRaw, &entry); err != nil {
		// Block exists but isn't parseable — still set non-nil
		// sentinel so hasRelay() reports true. Rebuild() will
		// regenerate the block from defaults if anyone calls it.
		return src
	}
	src.url = entry.Options.BaseURL
	for id, m := range entry.Models {
		src.models = append(src.models, relayModel{
			ID:           id,
			Name:         m.Name,
			ContextLimit: m.Limit.Context,
			OutputLimit:  m.Limit.Output,
		})
	}
	return src
}

// setProviders updates the provider source from a FormatOpenCodeConfig
// result. The formatted bytes contain the complete opencode config shape
// ({ $schema, provider: {...} }); this method extracts just the provider
// map. The model from the formatter is NOT captured — the model source
// is owned by applyWorkspaceConfig (set at boot via loadExisting) and
// must survive credential reloads.
func (w *AgentConfigWriter) setProviders(formattedConfig []byte) error {
	var cfg struct {
		Provider json.RawMessage `json:"provider"`
	}
	if err := json.Unmarshal(formattedConfig, &cfg); err != nil {
		return fmt.Errorf("parse formatted providers: %w", err)
	}
	w.mu.Lock()
	w.providerRaw = cfg.Provider
	w.mu.Unlock()
	return nil
}

// setModel updates the model source. Called by applyWorkspaceConfig
// at boot (via the materialize subcommand) to set the default model
// from workspace-config.json.
func (w *AgentConfigWriter) setModel(model string) {
	w.mu.Lock()
	w.model = model
	w.mu.Unlock()
}

// setRelay updates the relay source after the relay injector successfully
// discovers the free model list. The writer stores the URL and models;
// Rebuild() merges them into the provider map.
func (w *AgentConfigWriter) setRelay(url string, models []relayModel) {
	w.mu.Lock()
	w.relay = &relaySource{url: url, models: models}
	w.mu.Unlock()
}

// hasRelay returns true if the relay injector has successfully injected
// relay config. Used by the readyz handler for the RelayInjected signal
// (replaces the old getActiveRelayModels() != nil check).
func (w *AgentConfigWriter) hasRelay() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.relay != nil
}

// rebuild merges all sources (providers, model, relay) and writes the
// complete agent-config.json atomically via temp-file + os.Rename.
//
// Merge semantics:
//   - $schema is always set to "https://opencode.ai/config.json"
//   - provider map = existing providers (from setProviders or loadExisting)
//   - opencode-relay (if relay is set). No existing provider is removed.
//   - model = the model source (from setModel or loadExisting)
//   - disabled_providers = ["opencode"] (only if relay is set)
//
// The temp-file + rename pattern ensures readers never see a partially
// written file. os.Rename is atomic on POSIX filesystems (same mount).
func (w *AgentConfigWriter) rebuild() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	cfg := make(map[string]json.RawMessage)

	schema, _ := json.Marshal("https://opencode.ai/config.json")
	cfg["$schema"] = schema

	// Build provider map from the provider source.
	providers := make(map[string]json.RawMessage)
	if len(w.providerRaw) > 0 {
		if err := json.Unmarshal(w.providerRaw, &providers); err != nil {
			return fmt.Errorf("agent-config writer: parse provider source: %w", err)
		}
	}

	// Merge relay provider if relay is set.
	if w.relay != nil {
		relayEntry, err := buildRelayProviderEntry(w.relay.url, w.relay.models)
		if err != nil {
			return fmt.Errorf("agent-config writer: build relay provider: %w", err)
		}
		providers["opencode-relay"] = relayEntry

		disabled, _ := json.Marshal([]string{"opencode"})
		cfg["disabled_providers"] = disabled
	}

	if len(providers) > 0 {
		providerJSON, err := json.Marshal(providers)
		if err != nil {
			return fmt.Errorf("agent-config writer: marshal provider map: %w", err)
		}
		cfg["provider"] = providerJSON
	}

	if w.model != "" {
		modelJSON, _ := json.Marshal(w.model)
		cfg["model"] = modelJSON
	}

	// Merge admin prompt into agents config. Sets agents.build.system so the
	// prompt is prepended to the system prompt by opencode's LLM runner
	// (agent.info.system is placed before system.baseline). Existing agents
	// config from loadExisting is preserved; admin prompt overrides build.system.
	if w.adminPrompt != "" || len(w.agentsRaw) > 0 {
		agents := make(map[string]json.RawMessage)
		if len(w.agentsRaw) > 0 {
			_ = json.Unmarshal(w.agentsRaw, &agents)
		}
		if w.adminPrompt != "" {
			// Deep-merge into any existing build agent config so we only
			// override "system" and preserve sibling fields (tools, model,
			// mode, etc.) rather than wholesale-replacing the build agent.
			var existingBuild map[string]json.RawMessage
			if raw, ok := agents["build"]; ok {
				_ = json.Unmarshal(raw, &existingBuild)
			}
			if existingBuild == nil {
				existingBuild = map[string]json.RawMessage{}
			}
			systemJSON, _ := json.Marshal(w.adminPrompt)
			existingBuild["system"] = systemJSON
			buildJSON, err := json.Marshal(existingBuild)
			if err != nil {
				return fmt.Errorf("agent-config writer: marshal build agent: %w", err)
			}
			agents["build"] = buildJSON
		}
		agentsJSON, err := json.Marshal(agents)
		if err != nil {
			return fmt.Errorf("agent-config writer: marshal agents: %w", err)
		}
		cfg["agents"] = agentsJSON
	}

	output, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("agent-config writer: marshal config: %w", err)
	}

	return atomicRenameWrite(w.path, output, 0o600)
}

// atomicRenameWrite writes data to a temp file in the same directory as
// path, then atomically renames it to path. This ensures readers never
// observe a partially-written file (os.Rename is atomic on POSIX).
//
// The temp file is created in the same directory as the target so the
// rename is guaranteed to be on the same filesystem (rename across
// filesystems fails with EXDEV).
func atomicRenameWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".agent-config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp to target: %w", err)
	}
	return nil
}

// buildRelayProviderEntry builds the JSON for the opencode-relay provider
// entry that gets merged into the provider map. This is the same logic
// that buildRelayConfig used inline — extracted so the writer can call it
// during Rebuild without reading the existing file.
//
// The relay entry shape:
//
//	{
//	  "name": "OpenCode Zen (Free)",
//	  "npm": "@ai-sdk/openai-compatible",
//	  "options": {"baseURL": "<relayURL>", "apiKey": "public"},
//	  "models": {"<id>": {"name": "...", "limit": {"context": ..., "output": ...}}}
//	}
func buildRelayProviderEntry(relayURL string, models []relayModel) (json.RawMessage, error) {
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

	entry := provider{
		Name: "OpenCode Zen (Free)",
		NPM:  "@ai-sdk/openai-compatible",
		Options: options{
			BaseURL: relayURL,
			APIKey:  "public",
		},
		Models: modelMap,
	}
	return json.Marshal(entry)
}
