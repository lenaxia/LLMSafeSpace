# Worklog 0186 — GLM model routing silent failure, error persistence, collision determinism

**Date:** 2026-06-08
**PR:** #67 — fix/glm-model-routing-and-error-display

## Problem

User reported that sending a message with `glm-5.1` selected caused a silent failure:
an error flashed briefly then disappeared with no trace. Confirmed on cluster
(`a3a1c914` workspace, session `ses_1572a1a21ffeUzs3xeB8t0HDf0`): all 7 prior
messages had the same pattern — 403 from `opencode-relay/big-pickle` even though
`glm-5.1` was selected.

## Root cause investigation

Investigated live on the cluster via `kubectl exec` → `curl localhost:4096`:

- `GET /global/config` → `{"model":"thekao/glm-5.1"}` (correct)
- `GET /session/.../message` → all 7 messages: `providerID:"opencode-relay"`, `modelID:"big-pickle"`, `error:"APIError"` (403 Forbidden from relay)
- Sending a prompt with explicit `{model:{providerID:"thekao",modelID:"glm-5.1"}}` in the body → worked correctly

This confirmed the frontend was not injecting the model into the prompt body.

## Three bugs found and fixed

### Bug 1 (primary) — `ChatPage.tsx:handleSend` bad model ID parsing

`currentModel` is stored as a flat catalog ID (`"glm-5.1"`, never `"provider/model"`).
Old code: `id.indexOf("/") === -1 → return undefined` — so no model was ever sent
for any thekao model. opencode fell back to the session-level default
(`opencode-relay/big-pickle`), which 403'd from the relay.

Fix: use `currentModelProviderID` from the `listModels` response (new API field).
Fallback to `find()` on the models array for older API responses or collision.

### Bug 2 — `session.error` silently cleared by `reconcileOnIdle`

Sequence: `session.error` SSE → error added to `localMessages` → user sees it briefly →
`session.status=idle` SSE → `reconcileOnIdle()` → `setLocalMessages([])` → error gone.
`transformHistory` also excludes opencode error messages (empty `parts[]`) so errors
never appear in history either. Complete silent failure.

Fix: move session.error messages into a separate `sessionErrors` state that
`reconcileOnIdle` never touches. Cleared only on `sessionId` change.

### Bug 3 — `applyWorkspaceConfig` writing flat model ID at pod boot

`applyWorkspaceConfig` wrote `"glm-5.1"` (flat) to `agent-config.json`. opencode
requires `"thekao/glm-5.1"`. New sessions after pod restart used wrong default model.
Also: `ListModels` returned `currentModel` without resolved `providerID`, forcing
a `find()` that was non-deterministic when two providers shared a model ID.

Fixes:
- `resolveModelWithProvider` scans the provider map (already written by `FlushProviders`)
  to qualify flat IDs to `"providerID/modelID"` form.
- `ListModels` now returns `currentModelProviderID` alongside `currentModel`. Emits
  `""` on collision to signal ambiguity to the frontend.

## Files changed

- `frontend/src/pages/ChatPage.tsx` — bugs 1 and 2
- `frontend/src/api/workspaces.ts` — `ListModelsResponse.currentModelProviderID`
- `api/internal/handlers/models.go` — emit `currentModelProviderID` in `ListModels`
- `api/internal/handlers/models_test.go` — 2 new tests
- `cmd/workspace-agentd/secrets.go` — `resolveModelWithProvider`, fix `applyWorkspaceConfig`
- `cmd/workspace-agentd/secrets_test.go` — 3 new tests
- `frontend/src/pages/ChatPage.test.tsx` — 3 new regression tests
- `frontend/src/pages/ChatPage.sse.test.tsx` — 2 new regression tests

## Known limitations (documented, not fixed here)

- `resolveModelWithProvider` is non-deterministic when two providers in `agent-config.json`
  share a model ID (Go map iteration). Impact is low: the per-prompt frontend override
  routes correctly regardless of boot default, and `ListModels` emits `""` on collision.
- Secrets are intentionally deleted after pod creation for security. A pod restart
  with no credentials still loses all provider config. This is a separate issue.
