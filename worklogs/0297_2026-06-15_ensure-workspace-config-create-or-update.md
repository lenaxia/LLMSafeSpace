# Worklog: EnsureWorkspaceConfig silent no-op — root cause, tests, fix

**Date:** 2026-06-15
**Session:** Diagnosed and fixed model selection not persisting for users with zero LLM credentials
**Status:** Complete

---

## Objective

Workspace `8154ae86` (safespace.thekao.cloud session `ses_13385abe2ffe8mWBk2B0ITYnOB`) was showing the wrong model in the UI — the active session showed `thekao cloud/bedrock-claude-sonnet-4.6` (an unauthenticated provider), while `ListModels` returned only `opencode-relay` models. Find the root cause and fix it.

---

## Work Completed

### Investigation

Confirmed on the live pod via `kubectl exec`:

1. `OPENCODE_CONFIG=/tmp/agent-config.json` is set in the opencode process environment — correct.
2. `agent-config.json` contains the relay config and `opencode-relay` provider but **no `model` key**.
3. `/home/sandbox/.config/opencode/opencode.jsonc` (on the PVC, persistent across restarts) contains `"model": "thekao cloud/bedrock-claude-sonnet-4.6"`.
4. `ls /sandbox-cfg/` → only `password` and `secrets.json`. **`workspace-config.json` is absent.**
5. `kubectl get secret workspace-secrets-8154ae86... -n default` → `Error from server (NotFound)`.

### Root cause chain (five steps)

**Step 1 — No workspace-secrets Secret exists.**
The user has zero LLM credentials configured. `seedEphemeralSecrets` (called at workspace create time via `refreshEphemeralSecrets`) calls `PrepareSecretsForInjection`, gets back `[]` (2 bytes — no bindings), hits the `len(secretsJSON) <= 2` guard, and returns without writing the `workspace-secrets-<id>` Secret. This guard is correct: writing an empty `secrets.json` would be worse than writing nothing.

**Step 2 — SetModel silently no-ops.**
When the user selects a model via the frontend, `SetModel` → `EnsureWorkspaceConfig` → `secretClient.Get(ctx, secretName)` → `NotFound`. The original code returned `nil` with the comment "it'll be created on next bind." This was wrong: for users with zero credentials, there is no "next bind." The model selection was discarded permanently.

**Step 3 — Init container copies nothing.**
The `credential-setup` init container script copies `secrets.json` from the (optional-mounted) `workspace-secrets-<id>` Secret to `/sandbox-cfg/`. Since the secret never existed, `/sandbox-cfg/workspace-config.json` is also absent.

**Step 4 — applyWorkspaceConfig returns early.**
At pod boot, `applyWorkspaceConfig(agentConfigPath, secretsPath)` computes `configPath = /sandbox-cfg/workspace-config.json`. `os.ReadFile` returns `ENOENT` → function returns early → no `model` key is written to `agent-config.json`.

**Step 5 — Stale PVC config wins.**
opencode's config merge order: `~/.config/opencode/opencode.jsonc` (loaded first) → project config (none) → `OPENCODE_CONFIG` path (last, wins). `~/.config/opencode/opencode.jsonc` is on the PVC subPath `/home/sandbox` — it persists across pod restarts. It contains `"model": "thekao cloud/bedrock-claude-sonnet-4.6"` (written by a previous session). Since `agent-config.json` has no `model` key, the PVC-resident stale value wins. The `"thekao cloud"` provider is not in `connected[]` (auth.json only has `opencode-relay`). Model selection is silently broken.

### Why the assumption in the original comment was wrong

The comment "it'll be created on next bind" assumed that every workspace eventually gets credentials bound. That is false for users who rely exclusively on the free relay tier (the primary use case for this deployment). They never bind credentials, so the Secret is never created by the secrets path, and `EnsureWorkspaceConfig` was in a permanent no-op state.

### Fix

`EnsureWorkspaceConfig` now follows the same create-or-update pattern as `EnsureSecretsManifest`:

- If the secret **exists**: merge `workspace-config.json`, preserve all other keys (including `secrets.json`), update labels.
- If the secret is **absent**: create it with `workspace-config.json` only. `secrets.json` is intentionally omitted — the init container mounts the secret as `optional: true` and guards with `if [ -f ... ]`, so a missing `secrets.json` is safe and will be written on the next credential bind.
- `k8serrors.IsAlreadyExists` on the create path is converted to a conflict error so `retry.RetryOnConflict` retries via the get-then-update path (same pattern as `EnsureSecretsManifest`).
- Same standard labels (`app`, `llmsafespace.dev/workspace`, `llmsafespace.dev/ephemeral`) set on the create path so the secret is discoverable by operators and the controller lifecycle.

The abstraction level is correct: this is fixed in `EnsureWorkspaceConfig` at the service layer. The `seedEphemeralSecrets` empty-payload guard is correct and must not be changed — the two concerns are independent (`workspace-config.json` is non-sensitive metadata, `secrets.json` is sensitive credentials).

### Tests written

Nine new tests in `workspace_service_test.go` (all TDD — written before the fix, confirmed failing, then fixed):

| Test | What it proves |
|---|---|
| `TestEnsureWorkspaceConfig_CreatesSecretWhenAbsent` | Primary regression: zero-credential user, secret absent → must create |
| `TestEnsureWorkspaceConfig_UpdatesExistingSecret` | Happy path: secret exists → updates config, preserves secrets.json |
| `TestEnsureWorkspaceConfig_OverwritesExistingConfig` | Second call replaces first — not an append |
| `TestEnsureWorkspaceConfig_EmptyWorkspaceID` | Input validation guard |
| `TestEnsureWorkspaceConfig_PreservesLabels` | Created secret carries standard labels |
| `TestEnsureWorkspaceConfig_NonNotFoundGetError` | Non-NotFound errors propagate instead of silently succeeding |
| `TestEnsureWorkspaceConfig_ZeroCredentialUserSelectsModel` | End-to-end regression: zero-credential path calls EnsureWorkspaceConfig, Secret must exist after |
| `TestEnsureSecretsManifest_PreservesWorkspaceConfig` | Credential bind does not clobber workspace-config.json |
| `TestEnsureSecretsManifest_PreservesWorkspaceConfig_BindFirst` | workspace-config.json written after bind is also preserved |

---

## Key Decisions

**Do not touch `seedEphemeralSecrets`'s empty-payload guard.**
That guard is correct: writing an empty `secrets.json` to a workspace that has no credentials would clobber any pre-existing valid content on a restart path. The guard was the right call for the `secrets.json` concern. The bug was that `EnsureWorkspaceConfig` was written with an incorrect assumption that piggy-backed on the credentials path when it should have been independent.

**Create with `workspace-config.json` only, not `workspace-config.json` + empty `secrets.json`.**
The init container script already handles a missing `secrets.json` gracefully via `if [ -f /mnt/secrets/user-secrets/secrets.json ]`. Adding an empty `secrets.json` would introduce a new data hazard: if a credential bind later calls `EnsureSecretsManifest`, and `EnsureWorkspaceConfig`'s created secret has a non-nil `Data["secrets.json"]`, the bind path (which replaces `Data["secrets.json"]` wholesale) would work correctly. But empty `secrets.json` in the init container would be parsed by agentd as an empty array — safe in practice but misleading. Omitting it entirely is cleaner.

**Fix at service layer, not at agentd.**
One alternative was to make `applyWorkspaceConfig` fall back to reading `DefaultModel` from some other source (e.g. environment variable, or a different API call). That would be the wrong abstraction: the Secret is the correct durable channel for all workspace boot-time configuration. Fixing the Secret writer is the right place.

---

## Blockers

None.

---

## Tests Run

```
go test -timeout 30s -run "TestEnsureWorkspaceConfig|TestEnsureSecretsManifest" ./api/internal/services/workspace/ -v
# → 9 PASS

go test -timeout 60s -race ./api/internal/services/workspace/
# → PASS (all tests, race detector clean)

go test -timeout 60s -race ./api/internal/handlers/ ./api/internal/services/workspace/
# → PASS both packages
```

The `cmd/workspace-agentd` timeout failure under `-race -timeout 60s` is pre-existing (the binary compile step in `TestMaterializeSubcommand` takes >60s with race enabled). Confirmed passing at 120s with `-short`. Not related to this change.

---

## Next Steps

1. **Operationally fix the live workspace.** The workspace `8154ae86` still has no `workspace-secrets` Secret. After this fix is deployed, the user needs to re-select their model once via the UI for `EnsureWorkspaceConfig` to create the secret. Alternatively, create the secret manually. Consider a one-time migration job for all workspaces that have a `defaultModel` in the DB but no `workspace-config.json` in their secret.

2. **Consider a defensive check in `applyWorkspaceConfig`.** The current fallback (flat ID in `agent-config.json`) is documented as "opencode will reject it at startup." Now that `EnsureWorkspaceConfig` always writes the secret, this fallback should be hit less often. Still worth a comment noting the resolution of this bug.

3. **Consider writing `opencode.jsonc` from agentd boot.** The stale PVC config at `/home/sandbox/.config/opencode/opencode.jsonc` is fragile — it was written by some previous path and never cleaned up. `agent-config.json` correctly overrides it for keys it sets (via `OPENCODE_CONFIG`), but if `agent-config.json` ever lacks a key, the PVC value wins silently. A defensive fix would be to have agentd overwrite `opencode.jsonc` at boot with only the `$schema` key — ensuring the PVC copy never contains stale model/provider values. Not urgent now that the Secret path is fixed.

---

## Files Modified

- `api/internal/services/workspace/workspace_service.go` — `EnsureWorkspaceConfig`: create-or-update pattern instead of update-only no-op on NotFound; `EnsureSecretsManifest`: merge `secrets.json` key instead of replacing entire Data map (preserves `workspace-config.json` on credential bind)
- `api/internal/services/workspace/workspace_service_test.go` — 9 new TDD tests: 7 for `EnsureWorkspaceConfig` (including non-NotFound error path and zero-credential regression) + 2 for `EnsureSecretsManifest` clobber regression
- `controller/internal/workspace/pod_builder.go` — `credScript`: add conditional `cp` for `workspace-config.json` from mounted Secret to `/sandbox-cfg/`
- `controller/internal/workspace/health_test.go` — add `workspace-config.json` copy assertion to existing init container test; add `TestInitContainerScript_CopiesWorkspaceConfig` regression test
- `api/internal/handlers/models.go` — `SetModel`: surface `EnsureWorkspaceConfig` error as Warn log instead of silently discarding with `_ =`
- `cmd/workspace-agentd/secrets.go` — `applyWorkspaceConfig`: called on zero-credential boot path where `workspace-config.json` is absent; function already handles missing file gracefully via `os.ReadFile` early return
- `cmd/workspace-agentd/secrets_test.go` — `TestMaterializeSubcommand_MissingSecretsFile_AppliesWorkspaceConfig`: regression test for zero-credential boot path
- `worklogs/0297_2026-06-15_ensure-workspace-config-create-or-update.md` — this worklog
