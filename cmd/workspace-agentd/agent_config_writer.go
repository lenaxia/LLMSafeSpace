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
// reloadMu serialisation, and opencode not hot-reloading the file. This was
// fragile — a future change that reorders the boot sequence or adds a new
// write path could reintroduce relay clobbering.
//
// The AgentConfigWriter eliminates this fragility by being the sole writer.
// It holds three sources — providers, model, relay — and Rebuild() merges
// them into a complete config written atomically via temp-file + os.Rename.
//
// Boot initialisation: NewAgentConfigWriter reads the existing file (written
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
// Thread-safe: all methods acquire mu. Rebuild serialises the
// read-merge-write cycle so concurrent reloads and relay injection
// cannot interleave.
type AgentConfigWriter struct {
	mu          sync.Mutex
	path        string
	providerRaw json.RawMessage // raw "provider" map JSON from FormatOpenCodeConfig; nil = no providers
	model       string          // fully-qualified "providerID/modelID" form; "" = no model
	relay       *relaySource    // nil = relay not yet injected / skipped
}

// newAgentConfigWriter creates the writer and initialises its sources
// from the existing agent-config.json file (written by the materialize
// subcommand at boot). If the file is absent or corrupt, sources start
// empty and the first Rebuild() creates a fresh file.
func newAgentConfigWriter(path string) *AgentConfigWriter {
	w := &AgentConfigWriter{path: path}
	w.loadExisting()
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
	}
	if json.Unmarshal(data, &cfg) != nil {
		return
	}
	w.providerRaw = cfg.Provider
	w.model = cfg.Model
}

// setProviders updates the provider source from a FormatOpenCodeConfig
// result. The formatted bytes contain the complete opencode config shape
// ({ $schema, provider: {...} }); this method extracts just the provider
// map. The model from the formatter is NOT captured — the model source
// is owned by applyWorkspaceConfig (set at boot via loadExisting) and
// must survive credential reloads.
func (w *AgentConfigWriter) setProviders(formattedConfig []byte) {
	var cfg struct {
		Provider json.RawMessage `json:"provider"`
	}
	_ = json.Unmarshal(formattedConfig, &cfg)
	w.mu.Lock()
	w.providerRaw = cfg.Provider
	w.mu.Unlock()
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

// getRelayModels returns the injected relay model list, or nil if the
// relay injector has not completed. Used by code that needs to know
// which models are relay-routed (replaces getActiveRelayModels).
func (w *AgentConfigWriter) getRelayModels() []relayModel {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.relay == nil {
		return nil
	}
	return w.relay.models
}

// rebuild merges all sources (providers, model, relay) and writes the
// complete agent-config.json atomically via temp-file + os.Rename.
//
// Merge semantics:
//   - $schema is always set to "https://opencode.ai/config.json"
//   - provider map = existing providers (from setProviders or loadExisting)
//     + opencode-relay (if relay is set). No existing provider is removed.
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
