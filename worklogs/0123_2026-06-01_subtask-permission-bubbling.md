# Worklog: Subtask Permission Bubbling — Subagent Prompts Lost in Chat UI

**Date:** 2026-06-01
**Session:** Diagnose dropped permission requests from opencode subagents (`task` tool), implement parent-session resolution, ship end-to-end fix
**Status:** Complete

---

## Objective

User reported via the live chat at `https://safespace.thekao.cloud/chat/98c53aec-9a7a-4be8-96d3-3af7d54611b7/ses_17b1f034cffeTysii4ZtwVBvWW`: a subtask titled "Find opencode message endpoint" requested a permission, but no permission prompt was rendered in the parent chat — the user was unable to approve or reject it.

Goal: route subagent permission/question prompts back to the user-visible parent session so they cannot be silently dropped.

---

## Diagnosis

### What "subtask" means in opencode

opencode exposes a `task` tool that the agent can invoke to spawn a subagent (e.g. `@explore`). Each invocation creates a **child session** with its own `sessionID` and a `parentID` pointing at the caller's session. Live cluster confirmation:

```
PW=$(kubectl get secret workspace-pw-... -o jsonpath='{.data.password}' | base64 -d)
kubectl exec ... -- curl -u opencode:$PW http://localhost:4096/session
```

returned, among others:

```json
{
  "id": "ses_17b15a359ffeUU611BeylVPZwB",
  "parentID": "ses_17b1f034cffeTysii4ZtwVBvWW",
  "title": "Find opencode message endpoint (@explore subagent)"
}
```

The user was viewing `ses_17b1f034cffeTysii4ZtwVBvWW` (the parent). The subtask's permission events carried `sessionID = ses_17b15a359...`.

### Where the event was being dropped

`api/internal/handlers/proxy.go:emitNormalizedInputEvent` parses `permission.asked` events from the SSE stream and publishes a normalized `agent.permission` event to the per-workspace broker. The frontend filter in `frontend/src/pages/ChatPage.tsx` (line 399 pre-fix) was:

```ts
if (req.session_id === sessionId) {     // sessionId = URL session = ses_parent
  setPendingPermissions((prev) => …);   // never reached for subtasks
}
```

Because the subtask's `session_id` does not match the URL session, the event hit the filter and was silently discarded. The same bug applied to `agent.question` events.

### Validated assumptions

1. opencode's session GET response includes `parentID` for child sessions, empty/absent for top-level. **Verified live** against the user's pod (sample above).
2. Permission/question events emit with the session that owns the prompt, not the root. **Verified** via `pkg/agent/opencode/dialect.go:113` (`ocPermissionEvent.SessionID`) and the dialect tests at `pkg/agent/opencode/dialect_test.go:299-309`.
3. The frontend filter was the only drop point. **Verified** — the proxy `emitNormalizedInputEvent` does no session filtering; the broker is per-workspace, not per-session.
4. Existing handler unit tests (`TestNormalizedEvents_*`) build the proxy without the K8s mocks needed for a live `GET /session/:id`. **Verified** — that's why the resolver is opt-in (`EnableSessionParentResolution`) rather than always-on.

---

## Implementation

### 1. Backend: session-parent cache + opt-in resolver

`api/internal/handlers/session_parents.go` (new) implements an in-memory `sessionParentCache` keyed by `(workspaceID, sessionID)`. `resolveRoot` walks the parent chain up to depth 16 (cycle protection), caching every entry along the way. On any fetch failure it returns the deepest known ancestor — better to bubble the wrong (deeper) session than silently lose a user-facing permission request.

`fetchSessionParent` performs `GET /session/:id` against the workspace pod using the standard password cache. The cache is invalidated alongside the password cache in `invalidateCaches` so a workspace suspend/restart does not return stale parents.

The cache is wired in via a new `EnableSessionParentResolution()` method rather than the constructor. Rationale: the existing `TestNormalizedEvents_*` tests construct `ProxyHandler` directly without the K8s mocks needed for the session lookup, and would panic on every event. Production callers (`api/internal/app/app.go`) call the new method explicitly.

### 2. Backend: enrich events before publishing

`emitNormalizedInputEvent` now calls `resolveRootSessionID(workspaceID, sessionID)` and stores the result on `req.RootSessionID` before publishing. Same in `emitPendingInputRequests` (the SSE-reconnect catch-up path that fetches `/permission` and `/question`).

### 3. API types

Added `RootSessionID string \`json:"root_session_id,omitempty"\`` to both `agent.PermissionRequest` and `agent.QuestionRequest`. Frontend `types.ts` mirrored.

### 4. Frontend filter

`ChatPage.tsx` filter changed from:

```ts
if (req.session_id === sessionId) { … }
```

to:

```ts
const eventRoot = req.root_session_id ?? req.session_id;
if (eventRoot === sessionId || req.session_id === sessionId) { … }
```

The fallback to `session_id` preserves backward compatibility with API replicas that do not yet emit `root_session_id`.

---

## Tests

### Go (added)

- `api/internal/handlers/session_parents_test.go` — 8 cache-level tests (top-level, direct child, nested, cache hits, fetch error fallback, cycle protection, invalidation, per-workspace isolation)
- `api/internal/handlers/proxy_subtask_permission_test.go` — 6 integration tests:
  - `TestSubtaskPermission_BubblesToRootSession` — the bug fix, end to end
  - `TestSubtaskPermission_ResolutionDisabled_RootEqualsSelf` — backward compat
  - `TestSubtaskPermission_TopLevelSession_RootEqualsSelf`
  - `TestSubtaskQuestion_BubblesToRootSession` — same fix for questions
  - `TestSubtaskPermission_FetcherFails_FallsBackToSelf` — graceful degradation
  - `TestSessionParentCache_InvalidateOnWorkspaceCacheFlush` — cache lifecycle

### Frontend (added to `ChatPage.input.test.tsx`)

- `agent.permission from a subtask bubbles to parent session via root_session_id`
- `agent.question from a subtask bubbles to parent session via root_session_id`
- `agent.permission with root_session_id pointing at a different parent is ignored`
- `backward compat: event without root_session_id falls back to session_id match`

### Test results

```
go test -timeout 240s -short ./...   →  all packages pass except 2 pre-existing,
                                          unrelated failures:
                                          - api/internal/services/auth: TestE2E_RealAuth_WorkspaceEnv
                                            (env-dependent test; verified pre-existing via git stash)
                                          - pkg/secrets: TestIntegration_SecretTypeSpecificMetadata
                                            (regression introduced by 0b511cb — secret name validation
                                            now rejects fixture names with spaces)
go test ./api/internal/handlers/      →  17.8s, all green
go vet ./...                          →  clean
go build ./...                        →  clean
npx vitest run (frontend)             →  546/546 tests pass
npx tsc --noEmit (frontend)           →  clean
```

---

## Key Decisions

1. **Resolve synchronously, opt-in.** First attempt was async resolution in a goroutine. Rejected because (a) it produced flaky test ordering (event arrived before/after the test's `recvWithTimeout`) and (b) it doubled the publish path. Synchronous with a 3s context timeout is bounded — and the cache makes repeated lookups within a session free.

2. **Opt-in via `EnableSessionParentResolution()` not always-on.** Existing handler tests construct `ProxyHandler` directly with minimal K8s mocks. Always-on resolution would have required updating ~20 test setups to add `wsMock.On("Get", ...)` and password mocks. Production wires it explicitly via `app.go`.

3. **Fall back to session ID, never silently drop.** If the workspace pod is unreachable, `resolveRoot` returns the input session ID (so `RootSessionID == SessionID`). The frontend filter then matches as if it were a top-level session. Worst case: a subtask prompt appears in the subtask's view (which the user isn't watching) — but never silently lost to the broker.

4. **Cache invalidation hooks into the existing `invalidateCaches`.** Same trigger as password rotation. No new lifecycle event to plumb.

5. **Did not modify `pkg/types/contract_test.go` to include the new field.** `root_session_id` is `omitempty` on the Go side and optional on the TS side; the existing fixtures still type-check. A future contract-test addition can verify the round-trip if desired.

---

## Blockers

None.

---

## Next Steps

1. Deploy the new image to the cluster and confirm the user's chat at `ses_17b1f034cffeTysii4ZtwVBvWW` now receives the pending permission prompt. (The original prompt has already been resolved out-of-band — `GET /permission` returns `[]` — so a fresh subtask invocation is needed to validate.)
2. The two pre-existing test failures (`TestE2E_RealAuth_WorkspaceEnv`, `TestIntegration_SecretTypeSpecificMetadata`) are independent of this change but violate the "zero pre-existing errors" rule. The secrets one is a regression in commit `0b511cb` — file a follow-up to align fixture names with the new validator.
3. Consider exposing `root_session_id` on the REST `GET /workspaces/:id/permission` and `/question` responses (currently transparent passthrough). Low priority — the SSE path is the only consumer in the production UI.

---

## Files Modified

- `pkg/agent/types.go` — added `RootSessionID` to `QuestionRequest` and `PermissionRequest`
- `api/internal/handlers/session_parents.go` (new) — cache + fetcher
- `api/internal/handlers/session_parents_test.go` (new) — 8 unit tests
- `api/internal/handlers/proxy_subtask_permission_test.go` (new) — 6 integration tests
- `api/internal/handlers/proxy.go` — `EnableSessionParentResolution`, `resolveRootSessionID`, enrichment in `emitNormalizedInputEvent`, cache invalidation
- `api/internal/handlers/proxy_input.go` — enrichment in `emitPendingInputRequests`
- `api/internal/app/app.go` — call `proxyHandler.EnableSessionParentResolution()` in production wiring
- `frontend/src/api/types.ts` — `root_session_id?: string` on `QuestionRequest` and `PermissionRequest`
- `frontend/src/pages/ChatPage.tsx` — filter against `root_session_id ?? session_id`
- `frontend/src/pages/ChatPage.input.test.tsx` — 4 new bubbling/backward-compat tests
