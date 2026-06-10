# 0184 — Platform credential injection gaps, SSE tracker reset, auto-abort stuck sessions

**Date:** 2026-06-08
**Status:** Complete

---

## Session Overview

Long debugging session covering three distinct production issues discovered after deploying `ts-1780863877`:

1. First-message-after-workspace-creation response doesn't appear (SSE tracker backoff gap)
2. glm-5.1 / ai.thekao.cloud models missing when workspace activated without JWT session
3. Session stuck on question/permission tool after opencode restart — refresh required

---

## Issue 1 — SSE tracker backoff causes first message to be lost

### Root Cause

When a browser opens a new workspace that is still `Creating`, the `/session-events` SSE handler calls `EnsureWatching`. The subscription goroutine starts but immediately fails with "no pod IP" and enters exponential backoff (2s → 4s → 8s → 16s → **30s max**).

The workspace pod becomes `Active` ~36 seconds later. If the user sends a message within the current backoff window, `session.status=idle` fires inside the pod but the tracker isn't connected — event is dropped, `reconcileOnIdle` never runs, response never appears.

**Confirmed from browser console logs** (workspace `1aa87aec`):
- `sse.connected` at `22:45:51` while Creating
- workspace Active at `22:46:27` — tracker in 30s backoff, won't retry until `22:46:57`
- user sends message at `22:46:30` (3s after Active)
- response completes at `22:46:32` — tracker not connected, `session.status=idle` dropped

### Fix — PR #62

`api/internal/handlers/proxy.go`: when the CRD watcher detects `Creating→Active` (or any non-Active→Active) transition, call `StopWatching(workspaceID)` + `EnsureWatching(workspaceID)`. This cancels the backoff-waiting goroutine and starts a fresh subscription immediately — pod now has an IP so `connectAndRead` succeeds on first attempt (<100ms).

**Also addressed (PR #61):** `useChatStream.send()` `onComplete` was a no-op. Changed to call `reconcileOnIdle()` — ensures history is refetched after the 60-second timeout fires when SSE misses the idle event entirely.

**Tests added:** `TestProxy_OnPhaseChange_CreatingToActive_ResetsSSETracker`, `TestProxy_OnPhaseChange_ActiveToActive_NoReset`.

---

## Issue 2 — Platform credentials not injected when workspace activated without JWT session

### Root Cause

`refreshEphemeralSecrets` skipped entirely when `sessionID == ""` in context (e.g. API-key auth, controller reconcile, background jobs). This meant `ActivateWorkspace`/`RestartWorkspace` called by API-key clients never wrote `workspace-secrets-<id>`, leaving the pod without platform credentials. `glm-5.1` and other `thekao` models showed as unavailable.

Admin platform credentials (`owner_type='admin'`) use the server-side KEK and don't need a session DEK — only user credentials require a per-session DEK. The skip was overly conservative.

### Fix — PR #64

`refreshEphemeralSecrets`: when `sessionID == ""`, fall back to `seedEphemeralSecrets(ctx, userID, workspaceID)` (the admin-credential-only path) instead of returning early. Updated godocs for both `refreshEphemeralSecrets` and `seedEphemeralSecrets` to document the new fallback behavior and session semantics.

**Test updated:** `TestRefreshEphemeralSecrets_NoSessionID_SkipsAndWarns` → `TestRefreshEphemeralSecrets_NoSessionID_FallsBackToAdminCredentials`.

---

## Issue 3 — Session stuck on question/permission tool after opencode restart

### Root Cause

When the relay injector fires, it kills opencode to reload config. If a `question` or `permission` tool was pending in opencode's in-memory queue at that moment, the question is lost when the new opencode process starts. SQLite persists the message/part state (`toolState: "running"`) but the live queue is in-memory only.

On page refresh:
- `/question` returns `[]` — `emitPendingInputRequests` finds nothing to replay
- The session message history shows `type=tool_use, toolState=running, text="question: GitHub auth required"`
- `session.status=idle` was never fired — session is permanently busy
- User sees old question UI but can never answer it

**Confirmed:** opencode restarted at `04:25:52` (relay injector); session `ses_15a84243fff` on workspace `1aa87aec` was stuck.

### Fix — PR #65

`ChatPage.tsx`: added `useEffect` that fires in reconnect mode (busy session on page load) after history loads. If:
1. `isReconnectMode.current === true`
2. Last assistant message has a `tool_use` part with `toolState === "running"` and `text.startsWith("question")` or `text.startsWith("permission")`
3. `pendingQuestions.length === 0 && pendingPermissions.length === 0` (opencode lost the question — not re-emitted via SSE)

→ auto-call `workspacesApi.abortSession`, set `sessionWasInterrupted = true`, call `reconcileOnIdle()`.

Yellow banner: "⚠ Session was interrupted while waiting for your input. You can continue in this session or start a new one."

Guard prevents false aborts when the question is still live (re-emitted by `emitPendingInputRequests` via SSE — answerable).

**4 new tests:** question-tool abort, permission-tool abort, no-abort-when-SSE-replayed, abort-failure-still-shows-banner.

---

## Other Changes This Session

### Default workspace storage size (DB + schema)
`workspace.defaultStorageSize` updated from `1Gi` → `5Gi` in both the live DB and `pkg/settings/schema.go`. 1Gi is too small for real projects (node_modules, venvs, git history). Updated directly in DB (`UPDATE instance_settings SET value = '"5Gi"'`) and committed to schema default.

### Pod restart (workspace `1aa87aec`)
Manually restarted via `kubectl delete pod` after all fixes deployed to pick up `ts-1780904613` (PR #65 frontend build).

---

## Deployments

| Image | Revision | PRs |
|-------|----------|-----|
| `ts-1780870869` | 173 | PR #61 (reconcileOnIdle on SSE timeout) |
| `ts-1780874174` | 174 | PR #62 (SSE tracker reset on Creating→Active) |
| `ts-1780883278` | 175 | PR #64 (admin credential fallback in refreshEphemeralSecrets) |
| `ts-1780904613` | 176 | PR #65 (auto-abort stuck question sessions) |

---

## Assumptions Validated

| Assumption | Validation |
|---|---|
| SSE tracker backoff reaches 30s during Creating phase | Confirmed: 2s→4s→8s→16s→30s from `session_tracker.go:194-218` |
| `/question` returns `[]` when opencode restarts with pending question | Confirmed: `kubectl exec ... curl http://localhost:4096/question` returned `[]` after relay injector restart |
| `toolState === "running"` correct field for stuck tool detection | Confirmed: `messages.ts:37` maps `state.status` → `toolState` |
| Admin credentials don't need sessionID for decryption | Confirmed: `pkg/secrets/injection.go:28-41` uses `deriveAdminKey` for `owner_type='admin'` bindings |
| `pendingQuestions` populated before auto-abort `useEffect` fires | Validated: SSE events go through `handleSSEEvent` → `setPendingQuestions` before `useEffect` re-runs |
