# 0183 — Relay merge fix + platform provider model enricher

**Date:** 2026-06-07
**Status:** Complete

---

## Objectives

1. Fix `ai.thekao.cloud` models not appearing in the model selector — the platform credential's openai-compatible endpoint serves 16 custom models but opencode was using its hardcoded openai.com list.
2. Fix relay injector clobbering the openai provider config written by the init container.
3. Diagnose and document the free-tier relay 429 rate-limit situation.

---

## Investigation

### Relay injector overwrites agent-config.json (root cause of missing openai models)

`buildRelayConfig` in `cmd/workspace-agentd/relay_injector.go` called `os.WriteFile(cfg.AgentConfigPath, cfgBytes, 0o600)` unconditionally, replacing the entire file. The init container runs `FlushProviders(opencode.FormatOpenCodeConfig)` which writes the openai provider block (baseURL `https://ai.thekao.cloud/v1`, apiKey from platform credential) to `agent-config.json`. The relay injector then fired ~6s later and clobbered it. Result: only `opencode-relay` in the config, `openai` lost.

### Platform credential model list empty (root cause of wrong model IDs)

`epic30-openai-1780700420` has `model_allowlist={}`. `PrepareSecretsForInjection` produced `LLMProviderData{Provider:"openai", BaseURL:"https://ai.thekao.cloud/v1", APIKey:"...", Models:nil}`. `FormatOpenCodeConfig` wrote the provider block with no `models` map. opencode saw an `openai` provider with no explicit model list and fell back to its internal hardcoded model IDs (openai.com names) — all wrong for the custom endpoint.

`GET https://ai.thekao.cloud/v1/models` returns 16 models: `gpt-5.4`, `gpt-5.4-mini`, `deepseek-v3-chat`, `deepseek-r1-reasoning`, `deepseek-v4-flash`, `deepseek-v4-pro`, `glm-4.7`, `glm-4.6`, `glm-5.1`, `o3-mini-reasoning`, `gpt-5.5`, `bedrock-claude-sonnet-4.6`, `kokoro`, `ha-assistant`, `classifier`, `default`.

### Free-tier relay 429 (opencode.ai rate limiting)

> **CORRECTION (2026-06-13):** The diagnosis below that "This is a per-key rate limit, not an IP ban" is **wrong**. Verified by the project owner: the same `public` API key works without issue from a residential IP. Zen is throttling **Cloudflare's egress IP ranges**, not the `public` key. This confirms the premise of Epic 42 (Multi-Cloud Inference Relay) — the fix is moving traffic off Cloudflare IPs, not rotating keys.

The CF Worker relay infrastructure is working correctly. `opencode.ai/zen` is rate-limiting the `public` anonymous key (`FreeUsageLimitError`, `retry-after: ~22876s`). This is a per-key rate limit on the anonymous free tier, not an IP ban. The relay was verified working once (worklog 0170, 1 call) and then hit the quota. Nothing in our code caused this. The relay architecture (IP distribution via CF edge POPs) remains valid for future multi-key rotation.

### workspace-secrets lifecycle (confirmed not a bug)

The controller deletes `workspace-secrets-<id>` as soon as init containers complete (`phase_creating.go:111`, `phase_active.go:113`) to minimize etcd exposure of plaintext API keys. The init container reads it, materializes credentials into the `sandbox-cfg` emptyDir, then the K8s secret is cleaned up. `workspace-secrets` being absent on a running pod is correct designed behavior.

The `392cf65f` workspace appeared to not get `workspace-secrets` written — traced to the rolling update: the workspace was created during the 25s window between the new API pods starting and the old pods terminating. The old `ts-1780821820` pods (without `seedEphemeralSecrets`) handled the request. Not a bug in our code.

---

## Changes

### PR #58 — Relay injector merge fix (`cmd/workspace-agentd/relay_injector.go`)

`buildRelayConfig` now takes `agentConfigPath string` as its first argument. It reads the existing file, unmarshals into `map[string]json.RawMessage`, merges the `opencode-relay` provider into the existing `provider` map (preserving `openai` and any other providers), sets `disabled_providers`, and marshals the result. Returns error on corrupt existing config rather than silently clobbering it.

4 new tests: `MergesExistingProviders`, `WorksWithoutExistingConfig`, `CorruptExistingConfig_ReturnsError`, updated `WritesDisabledAndCustomProvider`.

### PR #59 — Model enricher (`cmd/workspace-agentd/model_enricher.go`, new file)

Before `FlushProviders`, enrich staged LLM providers that have a custom `BaseURL` but empty `Models` list by calling `GET {baseURL}/models` (OpenAI-compatible). Results cached to `{secretsBaseDir}/provider-models-cache-{provider}.json` (TTL 24h). Applied in both:
- Init-container `materialize` path (`runMaterializeCommand`)
- Live-reload path (`reloadSecretsHandler` / `/v1/reload-secrets`)

Provider name sanitized before use in cache filename: alphanumerics and hyphens only; dots excluded to prevent `..` path traversal.

`pkg/agentd/secrets/secrets.go`: Added `Materializer.EnrichProviders(fn)` — applies a transform to the staged provider slice before `FlushProviders` without exposing the internal slice.

14 new tests: `fetchModels` (parse IDs, skip empty, non-200 error, trailing slash), `fetchOrCacheModels` (writes cache, fresh hit, expired refetch, corrupt refetch, path-traversal sanitization), `enrichProviderModels` (fetch from endpoint, skip no-baseURL, skip existing models, fetch-fail best-effort, multiple providers).

---

## Deployment

- PR #58 deployed as `ts-1780854067` (revision 167)
- PR #59: pending deploy after merge — requires new runtime image build (workspace-agentd is in the runtime base image, not the API image)

---

## Verification

Workspace `392cf65f` after PR #58 deploy:
- `agent-config.json providers: ['openai', 'opencode', 'opencode-relay']` ✓
- `connected: ['opencode-relay', 'openai']` ✓
- Relay merge working correctly

Model enricher verified locally against `https://ai.thekao.cloud/v1/models` with `$OPENAI_API_KEY` — returns 16 models, all IDs sensible.

---

## Open Items

- Free-tier relay rate limit: `retry-after ~6h` from `opencode.ai/zen`. ~~Will reset automatically. Tracked for future: per-workspace key rotation or alternative free-tier sourcing.~~ **CORRECTED (2026-06-13):** The throttle is per-IP (Cloudflare egress ranges), not per-key — see correction above. Key rotation would not help. The correct solution is moving relay traffic off Cloudflare IPs (Epic 42).
- `relay.enabled` admin toggle deferred — requires live-pod update path (discussed in session, deferred to future worklog). Helm `inferenceRelayURL` flag remains the operational control.
