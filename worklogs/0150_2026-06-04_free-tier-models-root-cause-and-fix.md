# 0150 — Free-Tier Models Root Cause Analysis & Fix

**Date:** 2026-06-04
**Session type:** Investigation + bug fix + deploy
**Status:** Complete ✅

---

## Summary

Investigated why `GET /api/v1/workspaces/:id/models` returned `{"models":[],"currentModel":""}` for a workspace with no credentials bound. Traced the root cause through the opencode plugin pipeline, identified the fix, verified the commit, and deployed to cluster.

---

## Investigation

### Initial symptoms

`GET /workspaces/72ae4451-0a89-473f-989d-88c7736e9f76/models` returned `{"models":[],"currentModel":""}`.

### Ruled out

- **Network connectivity to models.dev** — confirmed reachable from workspace pod via `kubectl exec ... -- curl https://models.dev/api.json` (returned 2.2 MB payload successfully).
- **Missing workspace pod** — pod `72ae4451-0a89-473f-989d-88c7736e9f76-bbbac2f8` was running.
- **Auth issues** — querying `GET /api/model` directly on the pod at port 4096 returned `UnauthorizedError`, confirming opencode was running but the API needed Basic auth.

### Root cause

Traced through the opencode codebase (`~/personal/opencode`):

1. **Every provider starts `enabled: false`** — `ProviderV2.Info.empty()` in `packages/core/src/provider.ts` initialises `enabled: false` for all providers.

2. **`catalog.model.available()` filters on `provider.enabled !== false`** — so any provider that was never enabled has all its models excluded from the result, regardless of what the models themselves say.

3. **`OpencodePlugin`** (`packages/core/src/plugin/provider/opencode.ts`) does two things when no credential is found (`hasKey = false`):
   - Sets `provider.options.aisdk.provider.apiKey = "public"` ✓
   - Disables all paid (non-zero cost) models ✓
   - **Does NOT set `provider.enabled`** ✗ — this is the gap

4. **`EnvPlugin` and `AccountPlugin`** only run when env vars or `account.json` credentials exist. With an empty workspace (no `user_secret_bindings`), neither sets anything, so `provider.enabled` stays `false` for every provider including `opencode`.

5. **Result**: `catalog.model.available()` returns `[]` because every provider fails the `enabled !== false` check, even though opencode's free-tier models would work with `apiKey = "public"`.

### Why this is an opencode bug (but not ours to fix)

`OpencodePlugin` clearly intends to support anonymous/public usage — it injects `"public"` as the API key and disables paid models. The missing step is setting `provider.enabled`. This is arguably a bug in opencode, but since we don't control the opencode source, the fix must live in LLMSafeSpace.

---

## Fix

**Commit `0112a05`** (`fix(controller): Inject OPENCODE_AUTH_CONTENT to enable free-tier models`):

```diff
// controller/internal/workspace/pod_builder.go
+{Name: "OPENCODE_AUTH_CONTENT", Value: `{"opencode":{"type":"api","key":"public"}}`},
```

**How it works:**

- `OPENCODE_AUTH_CONTENT` is an env var opencode reads in `packages/core/src/auth.ts:150`
- The value is a legacy v1 format: `{ serviceID: Credential }` — opencode's `migrate()` function converts it to v2 (`{ version: 2, accounts: {...}, active: {...} }`) automatically
- `AccountPlugin` reads the migrated accounts, finds the `"opencode"` service entry, and sets `provider.enabled = { via: "account", service: "opencode" }` with `apiKey = "public"`
- `OpencodePlugin` then sees `hasKey = true` (via the `item.provider.enabled.via === "account"` branch), skips disabling paid models

**Side effect**: with `hasKey = true`, opencode no longer auto-disables paid models. All opencode provider models (free and paid) appear in `catalog.model.available()`. Paid models will fail at inference time without real credentials.

**Mitigation**: `classifyTier()` in `api/internal/handlers/models.go` already correctly identifies paid vs free models by cost data, and sets `ProxyRequired: true` only for free-tier. The frontend should use `tier`/`freeTier` fields to indicate paid models require credentials — tracked as a follow-up UX item.

---

## Deployment

- **Build:** CI run `26976522983` on `main` @ `dc7f34c` — all components built successfully
- **Image tag:** `ts-1780603662`
- **Deploy command:**
  ```
  helm upgrade llmsafespace charts/llmsafespace -n default \
    --reuse-values \
    --set api.image.tag=ts-1780603662 \
    --set controller.image.tag=ts-1780603662 \
    --set frontend.image.tag=ts-1780603662 \
    --set runtimeEnvironments.base.image.tag=ts-1780603662 \
    --wait --timeout=3m
  ```
- **Result:** Helm revision 127, all pods rolled to new tag

| Component | Old tag | New tag |
|---|---|---|
| API | `sha-7ecabfd` | `ts-1780603662` |
| Controller | `sha-eb1a7c4` | `ts-1780603662` |
| Frontend | `sha-eb1a7c4` | `ts-1780603662` |
| Runtime base | `sha-eb1a7c4` | `ts-1780603662` |

---

## Follow-up items

- **Existing workspace pods** need a restart to pick up `OPENCODE_AUTH_CONTENT` (only injected at pod creation by the controller). Restart workspace `72ae4451-0a89-473f-989d-88c7736e9f76` to verify.
- **Frontend UX**: paid opencode models now appear in the model selector. Frontend should grey them out or show a "requires credentials" badge for models where `tier === "paid"` and the workspace has no credential bindings. Currently the `tier` field is returned by the API but the frontend may not be using it.
- **Upstream**: consider filing a PR to opencode's `OpencodePlugin` to set `provider.enabled` when falling back to `"public"` key — this would be the clean fix and would benefit all opencode users.
