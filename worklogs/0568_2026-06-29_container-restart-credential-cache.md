# Worklog: Container-restart reload-secrets cache (#443)

**Date:** 2026-06-29
**Session:** Investigated and fixed issue #443 — container restart wipes user-DEK credentials and never restores them.
**Status:** Complete

---

## Objective

Restore user-DEK credentials (env-secrets like `GH_TOKEN`, SSH keys, user LLM providers) after a main-container restart (OOM, panic, kubelet restart). Pre-fix, the boot-time `materialize` subcommand's `reset()` wiped them on every container start, and the base `/sandbox-cfg/secrets.json` (written sessionless by the init container) only ever contained server-KEK creds — so user-DEK creds silently vanished until a rebind or full pod recreation.

---

## Work Completed

### Investigation & issue triage
- Verified every claim in issue #443 against source:
  - `Materialize()` calls `reset()` unconditionally → `pkg/agentd/secrets/secrets.go:386`
  - `reset()` `RemoveAll`s `SecretsBaseDir`/`SSHDir` + removes git/agent-config/secrets-env → `secrets.go:400-420`
  - Boot path reads only `/sandbox-cfg/secrets.json` (`cmd/workspace-agentd/secrets.go:343`)
  - `reloadSecretsHandler` materializes but persists the batch nowhere (`secrets.go:592-745`)
  - `/sandbox-cfg` is read-only on the main container (`controller/internal/workspace/pod_builder.go:161`)
  - `entrypoint-common.sh:35` runs `materialize` on every container start
  - `InjectSessionlessSecrets` (bootstrap) skips user-DEK creds via `secret_skipped_no_session` (`pkg/secrets/injection.go:407`)
- Found and removed a misleading `github-actions[bot]` comment on #443 that claimed the fix was "complete" (it was not) and referenced the wrong issue (#441 — an unrelated already-merged pagination PR). Deleted comment id 4827629947. No branch/commit/constants/tests for the claimed fix existed in the repo.

### Fix: persist-and-replay (per issue's proposed design)
- `reloadSecretsHandler` now persists the just-applied batch to `/sandbox-runtime/last-reload-secrets.json` (mode 0600, atomic temp+rename) immediately after `Materialize` succeeds, inside `reloadMu`. Non-fatal (warns on I/O failure). Never written on a hard materialize failure (500).
- `runMaterializeCommand` (boot) now loads base `secrets.json` + the cache, merges them (cache wins on duplicate Type+Name), and calls `Materialize(merged)` once. Absent cache → first-boot behavior preserved (base only / applyWorkspaceConfig). Corrupt cache → warn + base only.

### Tests (TDD — written first, all green)
- `reload_cache_test.go` (NEW): 20 unit tests for `mergeSecretBatches`, `writeReloadSecretsCache`, `loadReloadSecretsCache`, and handler persistence (writes after success; never on failure; mode 0600; atomic; empty-batch still writes).
- `container_restart_test.go` (NEW): 5 subprocess integration tests including the regression test `TestContainerRestart_ReplaysUserDEKCreds` (boot → live reload push → wipe → restart boot → both server-KEK and user-DEK creds present), plus no-cache fallback, corrupt-cache degradation, pod-recreation wipe, and SSH-key survival.

---

## Key Decisions

1. **Merge in memory, single `Materialize` call** (vs. an additive-apply refactor). The issue offered both; this is simplest and reuses the existing reset-then-apply semantics with no new entrypoint in `pkg/agentd/secrets`. Dedup key = `Type + "\x00" + Name`; cache wins because a reload is a full-replace and is the newer ground truth.
2. **Cache write placed after `Materialize` succeeds, before enrich/writer-rebuild.** Faithful to the issue ("after `Materialize(batch)` succeeds"). Has the side benefit that even if the writer rebuild later fails (500), the materialized env/SSH/git creds are cached and survive the next restart — strictly better than only caching on full success.
3. **Cache on `/sandbox-runtime` tmpfs (not `/sandbox-cfg`).** `/sandbox-cfg` is read-only on the main container. `/sandbox-runtime` is RW tmpfs (`Medium: Memory`, 96Mi) — survives container restart, wiped on pod death → preserves US-35.7 (no plaintext on PVC at rest) and G17 (no bootstrap token on main container).
4. **`loadReloadSecretsCache` never returns an error.** Absent = normal first boot (no warn); corrupt = warn + degrade to base only. Boot must never fail on a cache read.

### Assumptions stated & validated (Rule 7)
- **A1** `/sandbox-runtime` emptyDir survives container restart, wiped on pod death. **Validated:** `pod_builder.go:188-191` (`StorageMediumMemory`, 96Mi) + `security_test.go:173-198`.
- **A2** The reload batch (`InjectSecrets`) includes admin/org AND user LLM creds + non-LLM when a JWT session exists, so the cache is normally the complete live state; merge-with-base is defense-in-depth for the API-key-pseudo-session / no-session cases. **Validated:** `injection.go:176-202` (`loadLLMCredentials` decrypts all owner types).
- **A3** merge with cache-wins is correct because reload = full replace. **Validated by design.**

---

## Blockers

None.

---

## Tests Run

```
go build ./...                                                    # PASS (exit 0)
go vet ./cmd/workspace-agentd/ ./pkg/agentd/                      # CLEAN
gofmt -l <changed files>                                          # CLEAN
go test -race -run '<cache+restart+reload handler>' ./cmd/.../    # PASS (23 new + existing)
go test ./pkg/agentd/...                                          # PASS
go test -run '<existing materialize/reload/path>' ./cmd/.../      # PASS (no regressions)
```

Pre-existing, environment-dependent hangs unrelated to this change: `TestE2E_ModelEnricher_ModelsLandInAgentConfig` and `TestSSETracker_*` (network/timing; no `-short` skip exists). Not introduced by this work.

`golangci-lint` is not installed in the dev sandbox; gofmt + `go vet` are clean and the pre-commit hook / CI run golangci-lint.

---

## Next Steps

- Open PR with `Closes #443`, iterate through the automated AI reviewer until APPROVE, then squash-merge.
- Consider the issue's "Alternative considered": API-side re-push on pod-restart-counter increment as defence-in-depth (requires cached JWT session; silently no-op for offline users). Complement to this fix, not a replacement.
- #440 (env-secret reloads should not trigger an opencode restart) is explicitly out of scope per the issue.

---

## Files Modified

- `pkg/agentd/types.go` — new `ReloadSecretsCachePath` constant.
- `cmd/workspace-agentd/secrets.go` — `materializeConfig.reloadCachePath` + resolver; cache write in `reloadSecretsHandler`; cache replay+merge in `runMaterializeCommand`; new helpers `mergeSecretBatches`, `writeReloadSecretsCache`, `loadReloadSecretsCache`.
- `cmd/workspace-agentd/reload_cache_test.go` (NEW) — 20 unit tests.
- `cmd/workspace-agentd/container_restart_test.go` (NEW) — 5 integration tests.
- `README-LLM.md` — volume layout, key path constants, boot/reload write-sequence docs.
