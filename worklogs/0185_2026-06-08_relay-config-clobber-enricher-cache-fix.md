# 0185 — Fix relay config clobber on credential bind + enricher cache survival

**Date:** 2026-06-08
**Status:** Complete — PR #66 open, automated reviewer approved

---

## Objectives

1. Fix production bug: every `PUT /bindings` credential bind permanently removed the relay config (`disabled_providers` + `opencode-relay` provider block) from `agent-config.json` until the next pod restart.
2. Fix production bug: model enricher cache was deleted on every credential reload, making the advertised 24h TTL unreachable.

---

## Investigation

### Bug 1: Relay config clobbered on credential bind

Confirmed via pod logs and API logs. At 07:01:20 UTC 2026-06-08, workspace `1aa87aec` had a `PUT /bindings` call that triggered `pushSecretsToAgent` → `POST /v1/reload-secrets` → `reloadSecretsHandler` → `FlushProviders(opencode.FormatOpenCodeConfig)`. `FormatOpenCodeConfig` writes a fresh `agent-config.json` with only credential-sourced providers — no `disabled_providers`, no `opencode-relay`. The relay injector had written those at T+7s but had no mechanism to re-assert after subsequent writes.

**Root cause:** `reloadSecretsHandler` and the relay injector are two independent writers of `agent-config.json` with no coordination. `FlushProviders` is authoritative for credentials; the relay injector is authoritative for relay config. Neither knows about the other.

**Why it's hard to fix permanently:** Epic 30 US-30.4 explicitly noted this problem (design doc line 769) and specified the fix: "relay re-injection must run after every `Materialize` call." This was never implemented when US-30.4 shipped.

### Bug 2: Enricher cache deleted by reset()

`enrichProviderModels` wrote cache to `cfg.secretsBaseDir` = `/home/sandbox/.secrets`. `Materializer.reset()` calls `RemoveAll(/home/sandbox/.secrets)` at line 394 of `pkg/agentd/secrets/secrets.go` at the start of every `Materialize` call — before any secrets are materialized. The cache was always cold: every credential bind made a live HTTP call to `ai.thekao.cloud/v1/models` (~138ms, within the 5s API client timeout but unnecessary).

---

## Solution

### Bug 1 — atomic relay model storage + re-merge in reload handler

**`cmd/workspace-agentd/relay_injector.go`:**
- Added `activeRelayModels atomic.Pointer[[]relayModel]` package-level variable
- `setActiveRelayModels(models []relayModel)` — called once by the injector goroutine on success, before `KillOpenCode()` (ordering matters: value must be visible before the restart that will trigger future reloads)
- `getActiveRelayModels() []relayModel` — returns nil if injector hasn't run, was skipped, or failed

**`cmd/workspace-agentd/secrets.go`:**
- After `FlushProviders` in `reloadSecretsHandler`, added relay re-merge:
  - If `INFERENCE_RELAY_BASEURL` is set and `getActiveRelayModels()` is non-nil, calls `buildRelayConfig` with the stored model list and writes the result
  - nil correctly skips re-injection for: not-yet-run (self-heals at T+7s), skipped for personal key, failed

**Nil sentinel design:** nil means "do not inject" for all failure/skip cases. This avoids a separate boolean flag and is correct: a nil relay model list cannot produce a meaningful `opencode-relay` provider block anyway (empty `models{}` map would cause opencode to discover the full relay catalog including paid models — wrong).

### Bug 2 — move enricher cache to stable directory

**`cmd/workspace-agentd/secrets.go`:**
- Added `enricherCacheDir` field to `materializeConfig`
- Default: `$HOME/.local/state/llmsafespace` — on the `sandbox-home` emptyDir, outside `SecretsBasePath`, never touched by `reset()`
- Overridable via `LLMSAFESPACE_ENRICHER_CACHE_DIR` env var for tests
- Both `runMaterializeCommand` (boot path) and `reloadSecretsHandler` (reload path) pass `cfg.enricherCacheDir` to `enrichProviderModels`

**`cmd/workspace-agentd/model_enricher.go`:**
- Added `os.MkdirAll(cacheDir, 0o700)` at the top of `fetchOrCacheModels` — directory may not exist on first boot or after a node recycle. If creation fails, falls through to a direct fetch (non-fatal).

---

## Tests

**7 new tests + 1 updated test:**

| Test | What it covers |
|---|---|
| `TestActiveRelayModels_NilBeforeInjection` | Zero value is nil — relay-not-yet-run case handled correctly |
| `TestActiveRelayModels_SetAndGet` | Atomic store/load round-trips model list correctly |
| `TestStartRelayInjector_SetsActiveRelayModels` | Successful injection stores model list |
| `TestStartRelayInjector_DoesNotSetModelsWhenSkipped` | Personal key skip leaves var nil |
| `TestReloadSecretsHandler_RemergesRelayAfterFlush` | **Regression test for Bug 1** — verifies `disabled_providers` + `opencode-relay` survive a credential bind |
| `TestReloadSecretsHandler_SkipsRelayMergeWhenModelsNil` | Pre-injection/personal-key: relay not injected |
| `TestEnrichProviderModels_CacheWrittenToEnricherCacheDir` | **Regression test for Bug 2** — cache lands in `enricherCacheDir`, not `secretsBaseDir` |
| `TestReloadSecretsHandler_EnvOnly_NoConfigReload` | Updated to use `enricherCacheDir` field |

All tests pass: `go test -timeout 120s -race -count=1 ./cmd/workspace-agentd/`

---

## Automated Reviewer Findings (PR #66)

Reviewer: APPROVE

**Addressed in follow-up commit:**
- Doc inconsistency: README-LLM.md said "T+7s", code comment said "T+16s". Fixed comment to "~T+7s (opencode health check passes at T+5s, model fetch + config write adds ~2s)".
- Missing boot-path enricher cache test: Added `TestEnrichProviderModels_CacheWrittenToEnricherCacheDir`.

**Noted but not blocking:**
- `os.WriteFile` vs `atomicWrite` in re-merge: consistent with relay injector's existing behavior. Low risk — on crash/restart the file is rewritten. Future hardening pass.
- `activeRelayModels` is package-level global state: pragmatic for a single-process pod binary. Acceptable trade-off vs. threading through function signatures.
- Gap 5 (concurrent reload mutex) not addressed: out of scope, documented in README-LLM.md.
- Pre-existing tests missing `enricherCacheDir`: latent fragility but not a current bug (no tests have providers with `baseURL`). These will start failing only if a future test adds a provider with `baseURL` but no `enricherCacheDir`, which is an obvious setup error.

---

## Files Modified

```
cmd/workspace-agentd/relay_injector.go      — atomic model store + setter/getter
cmd/workspace-agentd/relay_injector_test.go — 4 new tests
cmd/workspace-agentd/secrets.go             — enricherCacheDir field + relay re-merge
cmd/workspace-agentd/secrets_test.go        — 3 new tests + 1 updated
cmd/workspace-agentd/model_enricher.go      — MkdirAll in fetchOrCacheModels
cmd/workspace-agentd/model_enricher_test.go — 1 new test
README-LLM.md                               — Relay Config Subsystem section (v1.11)
```

---

## Next Steps

- Merge PR #66 and deploy to cluster (requires workspace-agentd runtime image rebuild)
- After deployment: verify with workspace `1aa87aec` — bind a credential and confirm relay config survives (`connected: ["opencode-relay", "thekao"]` not `["opencode", "thekao"]`)
- Gap 5 (concurrent reload `sync.Mutex`) — low priority, file a story if concurrent bind behavior becomes an issue
- Epic 30 cleanup: when US-30.4's relay-as-admin-credential work makes the relay injector obsolete, delete `activeRelayModels`, `setActiveRelayModels`, `getActiveRelayModels`, and the re-merge block in `reloadSecretsHandler`
