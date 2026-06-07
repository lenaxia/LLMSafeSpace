# 0182 — Optimistic Model Selector + Platform Credential Injection Fix

**Date:** 2026-06-07
**Session:** Two independent bugs diagnosed and fixed: model selector UX latency and missing OpenAI credential on new workspaces
**Status:** Complete

---

## Objective

1. Eliminate the perceived latency when clicking a model in the model selector.
2. Diagnose and fix why OpenAI models from the `epic30-openai-1780700420` platform credential are absent from the model selection dialog on new workspaces.

---

## Work Completed

### Investigation

**Model selector latency root cause:**

`SetModel` (`api/internal/handlers/models.go:363`) makes three synchronous pod-proxy calls before responding:

1. `modelExistsInCatalog` → `GET /provider` on the workspace pod (~200ms)
2. `patchAgentModel` → `resolveModelIDFromCatalog` → another `GET /provider` (~200ms) + `PATCH /global/config` (~50ms)
3. `resolveModelIDFromCatalog` for metering → third `GET /provider` (~200ms)

Total backend latency: ~600ms+ per model click. The frontend compounded this by holding the dropdown open and disabling the trigger button via `setModelMutation.isPending` until the server responded, making the perceived delay >600ms.

**Platform credential root cause:**

`workspace-secrets-5c1f9130` (the K8s Secret read by the pod's init container) was never created for the new workspace. Tracing the flow:

- `CreateWorkspace` correctly calls `SeedWorkspaceCredentials` → inserts rows into `workspace_credential_bindings` (DB confirmed: both `opencode-free-tier` and `epic30-openai-1780700420` bound, `apply_count=0`).
- `CreateWorkspace` does NOT call `refreshEphemeralSecrets` → the `workspace-secrets-<id>` K8s Secret is never written at creation time.
- `refreshEphemeralSecrets` is only called from `ActivateWorkspace` and `RestartWorkspace` (resume/restart paths), not `CreateWorkspace`.
- `refreshEphemeralSecrets` also guards on `sessionID != ""` — but the `CreateWorkspace` request context has no `sessionID` so it would have skipped anyway.
- Result: pod init container mounts an absent `workspace-secrets-*` (volume is `optional: true`) → no OPENAI_API_KEY injected → opencode never sees the credential → `openai` absent from `connected[]` → OpenAI models filtered out of model selector.

**Admin credentials don't need a session:**

Per `pkg/secrets/injection.go:28-41`, admin credentials (`owner_type='admin'`) use a server-side KEK derived from `LLMSAFESPACE_MASTER_SECRET` and can always be decrypted without a user `sessionID`. The `sessionID` guard in `refreshEphemeralSecrets` was added to protect user credentials (which require a per-session DEK). At `CreateWorkspace` time only admin platform credentials are bound; user credentials do not exist yet.

### Fix 1: Optimistic model selector (`frontend/src/components/chat/ModelSelector.tsx`)

- Added `optimisticModel: string | null` state alongside existing `open` and `toast`.
- New `handleSelectModel(modelId)`: sets `optimisticModel`, closes dropdown, fires mutation — all synchronously before the server responds.
- `currentModel` derived as `optimisticModel ?? serverModel`: the selector button and highlight reflect the new selection immediately.
- `onSuccess`: clears `optimisticModel`, invalidates query (background refetch updates server-confirmed value).
- `onError`: clears `optimisticModel` (reverts to `serverModel`), shows error toast.
- Removed `setModelMutation.isPending` from the trigger button's `disabled` prop: the dropdown can be opened again immediately after a click (e.g. to change selection while the first request is still in-flight).

Net UX result: click → dropdown closes immediately, button label updates immediately. Server confirmation happens in the background.

### Fix 2: Seed ephemeral secrets on workspace creation (`api/internal/services/workspace/workspace_service.go`)

- Added `seedEphemeralSecrets(ctx, userID, workspaceID)` — a `CreateWorkspace`-only variant of `refreshEphemeralSecrets`.
- Calls `PrepareSecretsForInjection` with `sessionID=""` — correct because only admin credentials (server-side KEK, no session required) are bound at creation time.
- Called in `CreateWorkspace` immediately after `SeedWorkspaceCredentials`, before returning the response. If it fails it logs Warn and proceeds (same fail-open semantics as `refreshEphemeralSecrets`).
- Does not replace `refreshEphemeralSecrets` on the resume/restart paths — those still need sessionID for user credentials and use the existing guard.

### Tests

**Frontend (ModelSelector):** 3 new tests added to `ModelSelector.test.tsx`:
- `closes dropdown immediately on selection (optimistic)` — verifies the list collapses before the mutation resolves (mutation never resolves in test).
- `shows selected model immediately on click (optimistic), reverts on error` — verifies button label changes immediately on click and reverts after error.
- Existing `shows toast when model saved with applied:false` and `shows error toast when setModel fails` updated to use `getByRole("button")` selector since the trigger button is no longer blocked open by `isPending`.

All 681 frontend tests pass.

**Backend:** `go test -race ./api/internal/services/workspace/...` and `./pkg/secrets/...` pass.

---

## Assumptions Validated

| Assumption | Validation |
|---|---|
| Admin credentials use server-side KEK, no sessionID required | Confirmed in `pkg/secrets/injection.go:28-41` — `deriveAdminKey("provider-credentials")` runs iff any binding has `OwnerType == "admin"` |
| `workspace-secrets-*` secret absent on new workspace | Confirmed: `kubectl get secret workspace-secrets-5c1f9130... → NotFound` |
| DB bindings seeded correctly | Confirmed: `workspace_credential_bindings` has both rows, `apply_count=0` in `credential_auto_apply` (counter unrelated to injection) |
| `refreshEphemeralSecrets` skips on empty sessionID | Confirmed: `workspace_service.go:1083-1087` |
| PrepareSecretsForInjection called with `sessionID=""` returns admin creds | Validated via `injection.go:66-73` — `serverKEK` is derived if any binding is `owner_type='admin'`; user bindings fail gracefully with empty sessionID |

---

## Key Decisions

- **Don't patch `refreshEphemeralSecrets`** to allow empty sessionID — its existing guard is correct for the resume/restart context. A separate `seedEphemeralSecrets` keeps the invariants clear and avoids accidentally bypassing the sessionID check on restart paths.
- **Don't move `patchAgentModel` to async** in the backend — the `applied: bool` field in the response is meaningful to callers. The frontend optimistic pattern is the right layer for UX improvement without changing backend semantics.
- **Don't validate model against catalog on SetModel** synchronously — the `modelExistsInCatalog` check adds one full `/provider` round-trip. Validation is belt-and-braces (the UI already only shows models from the catalog); removing it would be a separate change.

---

## Open Items

- `modelExistsInCatalog` + duplicate `resolveModelIDFromCatalog` in `SetModel` still make 3 `/provider` calls per click — optimization opportunity but not a bug. Tracked for a future session.
- Live credential push (reload-secrets POST to agentd) on new workspace creation — `seedEphemeralSecrets` writes the K8s Secret but does not push live to the running pod because the pod is still starting. Not a problem: the init container reads the secret at startup. If the pod is already running before `seedEphemeralSecrets` completes (unlikely given pod scheduling time), the user would need a workspace restart to pick up the credentials.

---

## Files Modified

- `frontend/src/components/chat/ModelSelector.tsx`
- `frontend/src/components/chat/ModelSelector.test.tsx`
- `api/internal/services/workspace/workspace_service.go`
- `values-cluster.yaml` (tag bump ts-1780821820 → ts-1780824572, prior session)
