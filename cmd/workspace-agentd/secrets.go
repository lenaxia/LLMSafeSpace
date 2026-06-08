// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// secrets.go — Glue between the workspace-agentd binary and the
// pkg/agentd/secrets package. This file holds:
//
//   - materializeConfig: typed bundle of filesystem paths.
//   - loadMaterializeConfig: env-var driven path resolution with sensible
//     defaults that match the production pod layout.
//   - runMaterializeCommand: implements the `materialize` subcommand. The
//     subcommand reads /sandbox-cfg/secrets.json (or --from), applies the
//     batch via the secrets package, and exits non-zero ONLY on I/O or
//     parse failures. Per-secret validation skips do not block boot.
//   - reloadSecretsHandler: HTTP handler for /v1/reload-secrets. Same
//     semantics as the subcommand but driven by an HTTP request body and
//     with optional opencode restart on env/llm changes.
//   - buildEnvFrom: replaces the legacy buildEnv() string-mangling with a
//     proper FormatEnvLine/ParseEnvLine round-trip.
//
// Splitting this out of main.go gives the materialize logic a stable test
// surface and prevents a future change to main.go's HTTP wiring from
// silently regressing the secrets path.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/lenaxia/llmsafespace/pkg/agent/opencode"
	"github.com/lenaxia/llmsafespace/pkg/agentd"
	"github.com/lenaxia/llmsafespace/pkg/agentd/secrets"
)

// materializeConfig is the resolved set of filesystem paths used by the
// materialize subcommand and the reload handler. It maps 1:1 onto
// secrets.Paths but lives here so the binary can override defaults via
// environment variables (which the secrets package, by design, does not
// know about).
type materializeConfig struct {
	home            string
	secretsBaseDir  string
	sshDir          string
	agentConfigPath string
	secretsEnvPath  string
	gitCredsPath    string
	// enricherCacheDir is the directory used by the model enricher to cache
	// provider model lists between credential reloads. It must NOT be inside
	// secretsBaseDir because reset() deletes that directory on every Materialize
	// call, which would destroy the cache before it could be used.
	// Default: $HOME/.local/state/llmsafespace (on the sandbox-home emptyDir,
	// outside SecretsBaseDir and SSHDir, never cleaned by reset()).
	enricherCacheDir string
}

func (c materializeConfig) toPaths() secrets.Paths {
	return secrets.Paths{
		Home:            c.home,
		SecretsBaseDir:  c.secretsBaseDir,
		SSHDir:          c.sshDir,
		AgentConfigPath: c.agentConfigPath,
		SecretsEnvPath:  c.secretsEnvPath,
		GitCredsPath:    c.gitCredsPath,
	}
}

// loadMaterializeConfig resolves filesystem paths. It honors the same
// LLMSAFESPACE_* env-var overrides used by the test suite; in production
// no overrides are set and defaults match the runtime pod layout.
func loadMaterializeConfig() materializeConfig {
	home := envOrDefault("HOME", "/home/sandbox")
	return materializeConfig{
		home:             home,
		secretsBaseDir:   envOrDefault("LLMSAFESPACE_SECRETS_BASE_DIR", agentd.SecretsBasePath),
		sshDir:           envOrDefault("LLMSAFESPACE_SSH_DIR", home+"/.ssh"),
		agentConfigPath:  envOrDefault("LLMSAFESPACE_AGENT_CONFIG_PATH", agentd.AgentConfigPath),
		secretsEnvPath:   envOrDefault("LLMSAFESPACE_SECRETS_ENV_PATH", agentd.SecretsEnvPath),
		gitCredsPath:     envOrDefault("LLMSAFESPACE_GIT_CREDS_PATH", home+"/.git-credentials"),
		enricherCacheDir: envOrDefault("LLMSAFESPACE_ENRICHER_CACHE_DIR", home+"/.local/state/llmsafespace"),
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// runMaterializeCommand implements the `materialize` subcommand.
//
// Exit codes:
//
//	0 — secrets file applied successfully (every secret either Materialized
//	    or Skipped). Skipped is not a failure: it means the input was
//	    structurally rejected, which is the security policy.
//	0 — secrets file is absent. Pods without user-supplied credentials
//	    boot normally.
//	2 — input file is unreadable or unparseable.
//	3 — at least one secret failed to apply due to an I/O error.
//
// The reason for distinguishing 2 from 3 is operability: 2 means the
// controller wrote a malformed secrets.json (bug in the API server); 3
// means the node filesystem is misbehaving (e.g. tmpfs full).
func runMaterializeCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("materialize", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "/sandbox-cfg/secrets.json", "path to secrets.json")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg := loadMaterializeConfig()

	secretsList, err := secrets.LoadSecretsFile(*from)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no such file") {
			// Missing file is a no-op, not a failure.
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "materialize: %v\n", err)
		return 2
	}

	m := &secrets.Materializer{FS: secrets.RealFS(), Paths: cfg.toPaths()}
	result, err := m.Materialize(secretsList)
	reportResult(stderr, result)

	if err != nil && !errors.Is(err, secrets.ErrPartialFailure) {
		_, _ = fmt.Fprintf(stderr, "materialize: %v\n", err)
		return 3
	}

	// Enrich staged providers that have a custom BaseURL but no model list.
	// This fetches the live model list from the provider's /models endpoint
	// (e.g. ai.thekao.cloud/v1/models) so opencode uses the correct model IDs
	// instead of its internal hardcoded list. Results are cached to cacheDir
	// for providerModelCacheTTL so pod restarts don't re-fetch unnecessarily.
	httpClient := &http.Client{Timeout: 15 * time.Second}
	m.EnrichProviders(enrichProviderModels(context.Background(), cfg.enricherCacheDir, httpClient))

	// Flush staged llm-provider secrets to AgentConfigPath so opencode
	// reads them at startup. Without this, the config file is empty and
	// opencode boots with no provider credentials.
	if flushErr := m.FlushProviders(opencode.FormatOpenCodeConfig); flushErr != nil {
		_, _ = fmt.Fprintf(stderr, "materialize: flush providers: %v\n", flushErr)
		return 3
	}

	// Apply workspace-level default model if present. This file is
	// written by the API server alongside secrets.json.
	applyWorkspaceConfig(cfg.toPaths().AgentConfigPath, *from)

	if result != nil && result.HasFailures() {
		// Some I/O failure already logged via reportResult; exit 3 so the
		// runtime entrypoint can surface this to kubelet (CrashLoopBackOff
		// rather than silent partial-credential boot).
		return 3
	}
	// Skips are intentional; do not fail the boot.
	return 0
}

// reportResult writes a human-readable per-secret summary to stderr so
// `kubectl logs <pod>` operators see materialization outcomes.
func reportResult(w io.Writer, r *secrets.MaterializeResult) {
	if r == nil {
		return
	}
	mat, skip, fail := r.Counts()
	_, _ = fmt.Fprintf(w, "materialize: %d materialized, %d skipped, %d failed\n", mat, skip, fail)
	for _, sr := range r.Results {
		if sr.Outcome == secrets.OutcomeMaterialized {
			continue
		}
		_, _ = fmt.Fprintf(w, "  - %s/%s: %s — %s\n", sr.Type, sr.Name, sr.Outcome, sr.Reason)
	}
}

// applyWorkspaceConfig reads workspace-config.json (sibling to secrets.json)
// and merges the default model into the agent config file. This ensures the
// workspace's model selection survives pod restarts.
//
// DefaultModel is stored as a flat catalog ID (e.g. "glm-5.1"). opencode
// requires the fully-qualified "providerID/modelID" form in agent-config.json.
// We resolve the providerID by scanning the provider map already written to
// agent-config.json by FlushProviders (which runs before this function).
// If no provider claims the model, the flat ID is written as a best-effort
// fallback (opencode will reject it at startup, but the per-prompt model
// override in the frontend still routes correctly for interactive sessions).
func applyWorkspaceConfig(agentConfigPath, secretsPath string) {
	// workspace-config.json lives alongside secrets.json in /sandbox-cfg/
	dir := filepath.Dir(secretsPath)
	configPath := filepath.Join(dir, "workspace-config.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return // absent = no workspace config to apply
	}

	var wsCfg struct {
		DefaultModel string `json:"defaultModel"`
	}
	if json.Unmarshal(data, &wsCfg) != nil || wsCfg.DefaultModel == "" {
		return
	}

	// Read existing agent config (written by FlushProviders above).
	var cfg map[string]json.RawMessage
	existing, err := os.ReadFile(agentConfigPath)
	if err == nil && len(existing) > 0 {
		_ = json.Unmarshal(existing, &cfg)
	}
	if cfg == nil {
		cfg = map[string]json.RawMessage{}
	}

	// Resolve providerID from the provider map so opencode gets the fully-
	// qualified "providerID/modelID" form it requires. The provider map is
	// written by FlushProviders (called just before this function), so all
	// user-configured providers are already present.
	model := resolveModelWithProvider(cfg, wsCfg.DefaultModel)
	modelJSON, _ := json.Marshal(model)
	cfg["model"] = modelJSON

	if _, ok := cfg["$schema"]; !ok {
		schemaJSON, _ := json.Marshal("https://opencode.ai/config.json")
		cfg["$schema"] = schemaJSON
	}

	merged, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.WriteFile(agentConfigPath, merged, 0o600)
}

// resolveModelWithProvider scans the "provider" map in the agent config and
// returns "providerID/modelID" when the flat modelID is found in any provider's
// models map. Returns the flat modelID unchanged if no provider claims it
// (e.g. when the provider list hasn't been written yet, or the model was
// removed from the catalog since it was last selected).
func resolveModelWithProvider(cfg map[string]json.RawMessage, flatModelID string) string {
	if flatModelID == "" {
		return ""
	}
	// Already qualified — nothing to do.
	if strings.Contains(flatModelID, "/") {
		return flatModelID
	}

	providerRaw, ok := cfg["provider"]
	if !ok {
		return flatModelID
	}

	// provider map shape: {"providerID": {"models": {"modelID": {...}, ...}, ...}, ...}
	var providers map[string]struct {
		Models map[string]json.RawMessage `json:"models"`
	}
	if json.Unmarshal(providerRaw, &providers) != nil {
		return flatModelID
	}

	for providerID, p := range providers {
		if _, found := p.Models[flatModelID]; found {
			return providerID + "/" + flatModelID
		}
	}
	return flatModelID
}

// reloadSecretsHandler returns the HTTP handler for /v1/reload-secrets.
// proc may be nil (tests); in production it is a *managedProcess so the
// handler can restart opencode after env/llm secret changes.
//
// opencodePassword is the Basic-auth password every request to opencode
// (PUT /auth/:providerID, POST /instance/dispose) must carry. Production
// reads /sandbox-cfg/password at startup; tests pass "" since they
// either skip the credential push (no llm-provider in the batch) or
// stub the URL to a server that does not enforce auth. An empty
// password produces 401 against real opencode and was the proximate
// cause of Bug 1 (worklog 0125).
func reloadSecretsHandler(cfg materializeConfig, proc *managedProcess, opencodePassword string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var batch []secrets.Secret
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json: " + err.Error()})
			return
		}

		m := &secrets.Materializer{FS: secrets.RealFS(), Paths: cfg.toPaths()}
		result, mErr := m.Materialize(batch)

		if mErr != nil && !errors.Is(mErr, secrets.ErrPartialFailure) {
			log.Error("reload-secrets: materialize failed", zap.Error(mErr))
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": mErr.Error()})
			return
		}
		if result == nil {
			result = &secrets.MaterializeResult{}
		}

		// Enrich custom-endpoint providers with their live model list (same as
		// the boot-time materialize path). On reload, any cached model list is
		// reused so this is typically instant.
		reloadHTTPClient := &http.Client{Timeout: 15 * time.Second}
		m.EnrichProviders(enrichProviderModels(r.Context(), cfg.enricherCacheDir, reloadHTTPClient))

		// Flush staged llm-provider secrets to AgentConfigPath.
		// This MUST succeed before we notify the agent of config changes.
		if err := m.FlushProviders(opencode.FormatOpenCodeConfig); err != nil {
			log.Error("reload-secrets: flush providers failed", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "flush providers: " + err.Error()})
			return
		}

		// Re-merge relay config into the freshly-written agent-config.json.
		// FlushProviders writes only credential-sourced providers and has no
		// knowledge of the relay injector's disabled_providers + opencode-relay
		// block. Without this step, every credential bind permanently removes
		// the relay config until the next pod restart.
		//
		// nil models means: relay injector has not yet run, was skipped (personal
		// key), or failed. In all three cases we correctly skip re-injection:
		//   - Not yet run: relay injector fires at ~T+7s (opencode health check
		//     passes at T+5s, model fetch + config write adds ~2s) and writes its
		//     own config. Any reload in the T+0..T+7s window leaves the config
		//     without relay temporarily; the injector corrects it on completion.
		//   - Skipped: user routes directly; injecting relay would break them.
		//   - Failed: nothing to inject.
		if relayURL := os.Getenv("INFERENCE_RELAY_BASEURL"); relayURL != "" {
			if models := getActiveRelayModels(); models != nil {
				if cfgBytes, buildErr := buildRelayConfig(cfg.agentConfigPath, relayURL, models); buildErr != nil {
					log.Warn("reload-secrets: failed to re-merge relay config after flush",
						zap.Error(buildErr))
				} else if writeErr := os.WriteFile(cfg.agentConfigPath, cfgBytes, 0o600); writeErr != nil {
					log.Warn("reload-secrets: failed to write re-merged relay config",
						zap.Error(writeErr))
				} else {
					log.Info("reload-secrets: re-merged relay config after credential flush")
				}
			}
		}

		mat, skip, fail := result.Counts()
		log.Info("secrets reloaded",
			zap.Int("materialized", mat),
			zap.Int("skipped", skip),
			zap.Int("failed", fail),
		)

		// Stage llm-provider credentials. StageCredentials writes to opencode's
		// auth.json but does NOT dispose the instance. The user triggers reload
		// explicitly via POST /api/v1/workspaces/:id/agent/reload (Epic 27a).
		if hasLLMProviders(batch) {
			staged := m.StagedProviders()
			if len(staged) > 0 {
				oc := opencode.NewClient(fmt.Sprintf("http://localhost:%d", agentd.AgentPort), opencodePassword)
				if err := oc.StageCredentials(r.Context(), staged); err != nil {
					log.Warn("reload-secrets: opencode stage failed; credentials remain in "+
						"auth.json on disk but in-memory provider state will not pick them up "+
						"until the next explicit reload or pod restart",
						zap.Error(err))
				}
			}
		}

		// Restart for env-secret changes (agent reads env at boot only).
		restarted := false
		if proc != nil && shouldRestart(batch) {
			log.Info("env secrets changed, restarting opencode")
			//nolint:contextcheck // restart() spawns its own health-check goroutine with a fresh context
			proc.restart()
			restarted = true
		}

		status := http.StatusOK
		if result.HasFailures() {
			status = http.StatusInternalServerError
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"reloaded":  mat,
			"skipped":   skip,
			"failed":    fail,
			"results":   result.Results,
			"restarted": restarted,
		})
	}
}

func shouldRestart(batch []secrets.Secret) bool {
	for _, s := range batch {
		if s.Type == "env-secret" {
			return true
		}
	}
	return false
}

// hasLLMProviders returns true if the batch contains any llm-provider secrets.
func hasLLMProviders(batch []secrets.Secret) bool {
	for _, s := range batch {
		if s.Type == "llm-provider" {
			return true
		}
	}
	return false
}

// buildEnvFrom returns the process environment with secrets-env entries
// merged in.
//
// Implementation: we delegate to bash itself rather than re-implement
// shell parsing in Go. Bash is the source of truth for what `source FILE`
// does, including handling values that contain newlines, single quotes
// (escaped via 'a'\”b'), and other shell-meaningful bytes. A pure Go
// parser would have to mirror bash's quoting rules exactly, which is the
// class of bug that produced G2 in the first place.
//
// We invoke `bash -c 'set -a; source FILE; env -0'` and parse the
// NUL-delimited output. Each record is KEY=VALUE; we filter to keys that
// were not already set in our parent environment so we only forward the
// secrets-introduced variables.
//
// If bash is unavailable or the file is missing/unreadable, we return the
// parent environment unchanged. The agent will run without user-injected
// env-secrets, which is a safe degradation.
func buildEnvFrom(path string) []string {
	parent := os.Environ()
	if _, err := os.Stat(path); err != nil {
		return parent
	}

	// Capture parent env as a set so we can identify which entries the
	// sourced file added.
	parentSet := make(map[string]struct{}, len(parent))
	for _, e := range parent {
		if i := strings.IndexByte(e, '='); i > 0 {
			parentSet[e[:i]] = struct{}{}
		}
	}

	// `set -a` causes every assignment in the sourced file to be exported,
	// even if the file omits the `export` keyword. `env -0` writes
	// NUL-delimited records so values containing newlines survive.
	// G204: bash + script body are constant; only `path` varies and it
	// is bound to $1 (positional argument), so even a path containing
	// shell metachars cannot escape the script body. noctx: this runs
	// at boot before context.Context is meaningful.
	//nolint:gosec,noctx // G204/noctx: positional bind, boot-time call
	out, err := exec.Command("bash", "-c",
		`set -a; source "$1"; env -0`,
		"_", path,
	).Output()
	if err != nil {
		log.Warn("buildEnvFrom: bash source failed; secrets env not loaded",
			zap.String("path", path), zap.Error(err))
		return parent
	}

	added := make([]string, 0)
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		i := strings.IndexByte(rec, '=')
		if i <= 0 {
			continue
		}
		key, val := rec[:i], rec[i+1:]
		if _, inParent := parentSet[key]; inParent {
			// Skip pre-existing env vars; we only want secrets-introduced ones.
			// (Bash's `env` will print all of them after `set -a; source`.)
			continue
		}
		added = append(added, key+"="+val)
	}
	return append(parent, added...)
}
