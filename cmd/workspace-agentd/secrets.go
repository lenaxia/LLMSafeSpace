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
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/lenaxia/llmsafespace/pkg/agent/opencode"
	"github.com/lenaxia/llmsafespace/pkg/agentd"
	"github.com/lenaxia/llmsafespace/pkg/agentd/secrets"
)

// reloadMu serializes concurrent calls to reloadSecretsHandler. Two
// simultaneous reloads (from two API replicas or from parallel credential
// binds) race through Materializer.reset() which calls RemoveAll on
// SecretsBaseDir and RemoveAll on SSHDir, and both then appendFile to
// SecretsEnvPath — producing duplicate env var entries. Holding this mutex
// for the materialize → enrich → flush → re-merge sequence ensures exactly
// one reload runs at a time per pod. The restart at the end is excluded from
// the lock to avoid holding it during the ~5s SIGTERM window.
var reloadMu sync.Mutex

// restartableProcess is the subset of *managedProcess needed by the
// session-aware restart logic. Extracting it as an interface lets tests
// pass a mock without constructing a real managedProcess supervisor.
type restartableProcess interface {
	restart()
}

// restartIdleCheckInterval is how often the deferred-restart goroutine
// polls the sessionStatusTracker for busy→idle transitions. 5s per
// the US-44.2 design.
const restartIdleCheckInterval = 5 * time.Second

// defaultMaxDefer bounds how long a deferred restart waits for busy sessions
// to idle before force-restarting (worklog 371 H1). Without it, a stuck
// session (infinite loop, hung MCP, deadlocked tool) defers the restart
// forever and the credential change never applies — silent non-application.
// 2h is long enough that legitimate agentic turns (which can run for tens
// of minutes) are not interrupted, while bounded enough that stuck sessions
// eventually get the credential applied. The force-restart at expiry logs
// a warning so the operator can correlate the interruption.
const defaultMaxDefer = 2 * time.Hour

// sessionListerProbeTimeout bounds the cost of probing opencode's /session
// endpoint from the restart decision path. If opencode is unreachable the
// probe fails fast; if it is slow, we don't block the reload handler.
const sessionListerProbeTimeout = 3 * time.Second

// sessionLister returns the current live session IDs from opencode, or nil
// if opencode is unreachable. Used for two purposes in the session-aware
// restart logic:
//
//   - Pruning stale busy entries from the tracker (C2a): when opencode dies
//     mid-busy and the supervisor respawns it, the tracker retains a stale
//     "busy" entry for a session that no longer exists. Calling prune() with
//     the live session list removes it.
//   - Cold-start probing (C2b): when the tracker is empty (agentd restarted,
//     SSE not yet reconnected), the lister tells us whether opencode is
//     alive. An alive opencode with an empty tracker means sessions might be
//     busy but invisible — we defer. An unreachable opencode means nothing
//     is running — we restart immediately.
//
// Returns a non-nil slice (possibly empty) when opencode is reachable, nil
// when opencode is unreachable. "Empty non-nil" means "opencode is alive
// with zero sessions".
type sessionLister func(ctx context.Context) []string

// pruneFromLister prunes the tracker using the live session list from
// opencode. No-op if tracker is nil, lister is nil, the tracker is empty
// (nothing to prune — and the caller trackerHasBusyOrUnknown will probe
// opencode itself), or the probe fails (opencode unreachable — cannot
// verify, leave tracker as-is).
func pruneFromLister(ctx context.Context, tracker *sessionStatusTracker, lister sessionLister) {
	if tracker == nil || lister == nil || !tracker.hasAnyData() {
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, sessionListerProbeTimeout)
	defer cancel()
	if ids := lister(probeCtx); ids != nil {
		tracker.prune(ids)
	}
}

// trackerHasBusyOrUnknown reports whether the restart should be deferred.
// Returns true (defer) when:
//   - the tracker has data AND any session is busy, OR
//   - the tracker is empty BUT opencode is reachable with at least one
//     session (cold-start: sessions might be busy but invisible — C2b).
//
// Returns false (proceed with restart) when:
//   - the tracker has data AND no session is busy, OR
//   - the tracker is empty AND opencode is unreachable (nothing to lose), OR
//   - the tracker is empty AND opencode has zero sessions (nothing to lose).
func trackerHasBusyOrUnknown(ctx context.Context, tracker *sessionStatusTracker, lister sessionLister) bool {
	if tracker != nil && tracker.hasAnyData() {
		return tracker.hasAnyBusy()
	}
	// Tracker is empty — probe opencode to decide (C2b).
	if lister == nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, sessionListerProbeTimeout)
	defer cancel()
	ids := lister(probeCtx)
	// nil = unreachable → restart (nothing to lose).
	// non-nil + len>0 = opencode alive with sessions → defer (might be busy).
	// non-nil + len==0 = opencode alive, no sessions → restart (nothing to lose).
	return len(ids) > 0
}

// makeSessionAwareRestartDecision decides whether to restart opencode now or
// defer until sessions are idle. Returns true if the restart was initiated
// (immediately or via a deferred goroutine that has since fired), false if
// the restart was deferred to a background goroutine.
//
// Behavior:
//
//   - If proc is nil, returns true without doing anything (test/no-op path).
//   - If the tracker shows all sessions idle (or opencode is unreachable with
//     no tracker data), restarts immediately.
//   - If sessions are busy (or the tracker is empty but opencode is alive
//     with sessions — cold-start, C2b), defers the restart.
//
// The deferred goroutine:
//
//   - Polls every pollInterval, pruning stale entries via lister (C2a) and
//     re-checking busy state.
//   - Selects on ctx.Done() so it is canceled at agentd shutdown (H1a).
//   - Force-restarts after maxDefer (H1b) so credentials eventually apply
//     even if sessions stay busy forever (stuck tool, infinite loop).
//   - Is tracked by bgWg (H1c) so shutdown waits for it before proc.stop().
//
// maxDefer <= 0 falls back to defaultMaxDefer. pollInterval <= 0 falls back
// to restartIdleCheckInterval.
func makeSessionAwareRestartDecision(
	ctx context.Context,
	proc restartableProcess,
	tracker *sessionStatusTracker,
	pollInterval time.Duration,
	maxDefer time.Duration,
	lister sessionLister,
	bgWg *sync.WaitGroup,
) bool {
	if proc == nil {
		return true
	}
	if maxDefer <= 0 {
		maxDefer = defaultMaxDefer
	}
	if pollInterval <= 0 {
		pollInterval = restartIdleCheckInterval
	}

	// Prune stale entries before deciding (C2a).
	pruneFromLister(ctx, tracker, lister)

	if !trackerHasBusyOrUnknown(ctx, tracker, lister) {
		proc.restart()
		return true
	}

	// Sessions are busy or status is unknown (cold-start) — defer.
	var busy []string
	if tracker != nil {
		busy = tracker.listBusy()
	}
	if len(busy) > 0 {
		log.Info("session-aware restart: deferring restart, sessions are busy",
			zap.Strings("busySessions", busy),
			zap.Duration("maxDefer", maxDefer))
	} else {
		log.Warn("session-aware restart: deferring restart, session status unknown (tracker empty, opencode alive — SSE disconnected?)",
			zap.Duration("maxDefer", maxDefer))
	}

	runDeferred := func() {
		deadline := time.NewTimer(maxDefer)
		defer deadline.Stop()
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info("session-aware restart: deferred restart canceled by shutdown")
				return
			case <-deadline.C:
				log.Warn("session-aware restart: max-defer elapsed, force-restarting to apply credential change",
					zap.Duration("maxDefer", maxDefer),
					zap.Strings("busySessions", func() []string {
						if tracker != nil {
							return tracker.listBusy()
						}
						return nil
					}()))
				proc.restart()
				return
			case <-ticker.C:
				pruneFromLister(ctx, tracker, lister)
				if !trackerHasBusyOrUnknown(ctx, tracker, lister) {
					log.Info("session-aware restart: all sessions now idle, applying deferred restart")
					proc.restart()
					return
				}
			}
		}
	}

	if bgWg != nil {
		bgWg.Add(1)
		go func() {
			defer bgWg.Done()
			runDeferred()
		}()
	} else {
		go runDeferred()
	}

	return false
}

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
	// Default: $HOME/.local/state/llmsafespace (on the workspace PVC subPath:home,
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
			// secrets.json is absent (zero-credential user or pre-first-bind).
			// Still apply workspace-config.json so the default model is written
			// to agent-config.json even when no LLM credentials are configured.
			applyWorkspaceConfig(cfg.toPaths().AgentConfigPath, *from)
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

// reloadSecretsDeps bundles the runtime dependencies that
// reloadSecretsHandler needs beyond the materialize config. Grouping them in
// a struct keeps the handler signature stable as dependencies are added and
// makes call sites self-documenting.
type reloadSecretsDeps struct {
	// Proc is the supervised opencode process. May be nil in tests; in
	// production it is a *managedProcess so the handler can restart
	// opencode after env/llm secret changes.
	Proc restartableProcess

	// OpencodePassword is the Basic-auth password every request to opencode
	// (PUT /auth/:providerID, POST /instance/dispose) must carry. Production
	// reads /sandbox-cfg/password at startup; tests pass "" since they
	// either skip the credential push (no llm-provider in the batch) or
	// stub the URL to a server that does not enforce auth. An empty
	// password produces 401 against real opencode and was the proximate
	// cause of Bug 1 (worklog 0125).
	OpencodePassword string

	// Tracker is the SSE session-status tracker. May be nil.
	Tracker *sessionStatusTracker

	// BgCtx is the agentd background-goroutine context. The deferred-restart
	// goroutine selects on it so it is canceled at shutdown (H1a). When
	// nil, context.Background() is used (goroutine lives until restart fires
	// or maxDefer elapses — tests only).
	BgCtx context.Context

	// BgWg tracks background goroutines for clean shutdown. The deferred-
	// restart goroutine registers here so main's shutdown waits for it
	// before proc.stop() (H1c). May be nil (tests only).
	BgWg *sync.WaitGroup

	// Lister probes opencode's /session endpoint for the live session list.
	// Used to prune stale busy entries (C2a) and to decide cold-start
	// behavior when the tracker is empty (C2b). May be nil (the restart
	// logic falls back to immediate-restart-on-empty-tracker).
	Lister sessionLister

	// RestartReasonMarkerPath overrides where the restart-reason marker is
	// written. Empty falls back to the package const RestartReasonMarkerPath
	// (production). Tests inject a path under t.TempDir() (or a sabotaged
	// path) to assert marker-write behavior without polluting /workspace.
	RestartReasonMarkerPath string
}

// reloadSecretsHandler returns the HTTP handler for /v1/reload-secrets.
func reloadSecretsHandler(cfg materializeConfig, deps reloadSecretsDeps) http.HandlerFunc {
	proc := deps.Proc
	opencodePassword := deps.OpencodePassword
	tracker := deps.Tracker
	bgCtx := deps.BgCtx
	if bgCtx == nil {
		bgCtx = context.Background()
	}
	lister := deps.Lister
	markerPath := deps.RestartReasonMarkerPath
	if markerPath == "" {
		markerPath = RestartReasonMarkerPath
	}

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
		// Capture the request context once and propagate it explicitly to the
		// downstream calls that need it. Threading a local ctx (rather than
		// repeated r.Context() calls) keeps the context lineage obvious to
		// readers and to the contextcheck linter.
		reqCtx := r.Context()

		// Serialize the materialize → enrich → flush → re-merge sequence.
		// Concurrent reloads (from two API replicas or parallel credential binds)
		// race through Materializer.reset() which RemoveAlls SecretsBaseDir and
		// SSHDir and appendFiles to SecretsEnvPath — producing duplicate env var
		// entries and interleaved agent-config.json writes. The restart at the
		// end is excluded from the lock to avoid holding it during the ~5s SIGTERM
		// window.
		reloadMu.Lock()

		m := &secrets.Materializer{FS: secrets.RealFS(), Paths: cfg.toPaths()}
		result, mErr := m.Materialize(batch)

		if mErr != nil && !errors.Is(mErr, secrets.ErrPartialFailure) {
			reloadMu.Unlock()
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
		m.EnrichProviders(enrichProviderModels(reqCtx, cfg.enricherCacheDir, reloadHTTPClient))

		// Flush staged llm-provider secrets to AgentConfigPath.
		// This MUST succeed before we notify the agent of config changes.
		if err := m.FlushProviders(opencode.FormatOpenCodeConfig); err != nil {
			reloadMu.Unlock()
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

		reloadMu.Unlock()

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
				oc := opencode.NewClient(fmt.Sprintf("http://localhost:%d", agentd.AgentPort), opencodePassword, log)
				if err := oc.StageCredentials(reqCtx, staged); err != nil {
					log.Warn("reload-secrets: opencode stage failed; credentials remain in "+
						"auth.json on disk but in-memory provider state will not pick them up "+
						"until the next explicit reload or pod restart",
						zap.Error(err))
				}
			}
		}

		restarted := false
		if proc != nil && shouldRestart(batch) {
			if reason, names := classifySecretRestartReason(batch); reason != "" {
				if err := writeRestartReasonMarker(markerPath, reason, names); err != nil {
					log.Error("failed to write restart-reason marker", zap.Error(err))
				} else {
					logRestartReasonAtWrite(reason, names, log.Core())
				}
				// H2: record the restart in the Prometheus counter so ops
				// dashboards surface credential-change restarts. Recorded
				// UNCONDITIONALLY (after the marker/log block), not gated on
				// marker-write success — a full/read-only PVC must not suppress
				// the metric. This matches the crash path (main.go) and the OOM
				// path (oom_detection.go), which also record the metric
				// regardless of marker outcome. The reason label is the short
				// metric form (env_secrets / api_key) matching the help text and
				// the crash/oom reasons.
				pkgOpsMetrics.RecordRestart(workspaceIDFromEnv(), metricRestartReason(reason))
			}
			restarted = makeSessionAwareRestartDecision(bgCtx, proc, tracker, restartIdleCheckInterval, defaultMaxDefer, lister, deps.BgWg)
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

// metricRestartReason maps a marker reason (from classifySecretRestartReason,
// used in the on-disk restart-reason marker) to the short Prometheus label
// used by opsMetrics.RecordRestart. The metric help text enumerates:
// env_secrets, api_key, crash, oom, user_requested. Unknown reasons pass
// through unchanged so the metric remains useful if new reasons are added.
func metricRestartReason(markerReason string) string {
	switch markerReason {
	case "env_secrets_changed":
		return "env_secrets"
	case "api_key_changed":
		return "api_key"
	default:
		return markerReason
	}
}

func shouldRestart(batch []secrets.Secret) bool {
	for _, s := range batch {
		if s.Type == "env-secret" || s.Type == "api-key" {
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
