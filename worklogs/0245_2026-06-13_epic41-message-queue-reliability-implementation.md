# Worklog: Epic 41 — Message Queue Reliability Implementation

**Date:** 2026-06-13
**Session:** Implement all four user stories from Epic 41 design document
**Status:** Complete

---

## Objective

Implement the four user stories from Epic 41 (Message Queue Reliability):
- US-41.4: Fix `onSessionIdle` dead code (workspaceConfig.workspaceID never populated)
- US-41.2: Add 409 Conflict guard in `SendPromptAsync`
- US-41.1: Fix `reconcileOnIdle` streaming state clear timing
- US-41.3: Handle 409 in `drainOne` with `mark_pending` action

---

## Work Completed

### US-41.4: Fix onSessionIdle dead code

**Root cause:** `workspaceConfig.workspaceID` was never populated, making the `cfg.workspaceID != ""` guard at `proxy.go:1060` always false. Activity tracking and session title persistence on idle were silently broken since Epic 06.

**Fix:** Removed the `wsConfig` lookup and `workspaceID` guard from `onSessionIdle`. Now calls `activityTracker.Record(workspaceID)` and `sessionIndex.RecordMessage(...)` directly using the `workspaceID` parameter — which IS the map key, making the lookup redundant.

**Tests (3 new):**
- `TestProxy_OnSessionIdle_RecordsActivityWithoutWsConfig` — verifies activity recorded without wsConfig
- `TestProxy_OnSessionIdle_RecordsSessionIndexWithoutWsConfig` — verifies RecordMessage called without wsConfig
- `TestProxy_OnSessionIdle_FetchAndPersistTitleWithoutWsConfig` — verifies fetchAndPersistTitle goroutine fires and upserts title

**Updated existing test:**
- `TestProxy_OnSessionIdle_ActivitySkippedWhenCacheEvicted` → renamed to `TestProxy_OnSessionIdle_RecordsActivityWithoutWsConfig` — now asserts activity IS recorded (was testing the bug behavior)

### US-41.2: Add 409 Conflict guard in SendPromptAsync

**Implementation:**
- Added `isSessionActive(workspaceID, sessionID string) bool` helper (checks `activeSess` map)
- Added 409 guard at top of `SendPromptAsync` before `proxyToWorkspace`
- Returns `{"error": "session is busy; retry after idle", "retryAfter": 1}` with `Retry-After: 1` header
- `SendMessage` (synchronous) is NOT guarded — only `SendPromptAsync`

**Tests (5 new):**
- `TestProxy_SendPromptAsync_Returns409WhenSessionActive`
- `TestProxy_SendPromptAsync_ProceedsWhenSessionNotActive`
- `TestProxy_IsSessionActive_ReturnsFalseForUnknownWorkspace`
- `TestProxy_IsSessionActive_ReturnsTrueForActiveSession`
- `TestProxy_SendPromptAsync_409DoesNotAffectSendMessage`

**Updated existing test:**
- `TestProxy_E2E_FullFlow` — added `removeActiveSession` call between message and prompt to simulate idle event that would happen in production via SSE

### US-41.1: Fix reconcileOnIdle streaming state clear timing

**Root cause:** `reconcileOnIdle` unconditionally cleared `sseStreamParts`, `localMessages`, and `sessionErrors` after the history refetch. When a queued message was just sent (204 returned), the streaming content from that message would appear briefly, then get cleared on the next idle.

**Fix:** Guard `setSseStreamParts([])`, `setLocalMessages([])`, `setSessionErrors([])` on `msgs.length > 0`. If the refetched history is empty (opencode hasn't committed yet), preserve existing state. Moved `freshHistory` fetch before the guard check. Ref resets (`isReconnectMode`, `knownLivePartIds`, etc.) always run regardless.

**Tests (3 new in ChatPage.queue.test.tsx):**
- Empty history does not clear streaming state
- Non-empty history clears streaming state
- Queued message sent on idle with response appearing after second idle

### US-41.3: Handle 409 in drainOne with mark_pending action

**Implementation:**
- Added `_retryCount?: number` to `QueuedMessage` type
- Added `mark_pending` reducer action: transitions `sending` → `pending`, clears `_sentAt`, increments `_retryCount`
- Added `MAX_RETRIES = 5` constant
- In `drainOne .catch()`: 409 → `mark_pending` (unless `_retryCount >= MAX_RETRIES`, then `mark_error` with "Session busy — retry manually")
- 409 handling runs before the existing 429/error handling

**Tests (4 new in useMessageQueue.test.ts):**
- 409 transitions message to pending (not error)
- Message in pending after 409 is retried on next notifyIdle
- After MAX_RETRIES 409s, message transitions to error
- Queue item is removed on successful send after prior 409 retries

---

## Key Decisions

1. **Removed wsConfig lookup entirely (US-41.4)** rather than populating `workspaceID` in the config. The `workspaceID` parameter to `onSessionIdle` IS the map key — the lookup was redundant. Simpler and correct.

2. **409 only on SendPromptAsync, not SendMessage (US-41.2)**. The synchronous path blocks until completion; the async path is where the race with opencode's `ensureRunning` happens.

3. **MAX_RETRIES = 5 for 409 handling (US-41.3)**. At ~1s per retry cycle (opencode processing time), 5 retries = ~5s before escalating to error. This matches the design's FM3 mitigation.

4. **Guard on msgs.length > 0 (US-41.1)** rather than suppressing reconcileOnIdle entirely. Reconcile serves important functions (clearing stale state, queue reconciliation). The guard is minimal and preserves the safety function.

---

## Assumptions Stated and Validated

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | `workspaceConfig.workspaceID` is never populated (V18 from design) | Verified in proxy.go — only `autoApprovePermissions` and `maxActiveSessions` are set |
| A2 | `activeSess` is per-replica in-memory (limitation of 409 guard) | Acknowledged in design; guard is defense-in-depth |
| A3 | `SendMessage` (synchronous) does not need 409 guard | Verified: synchronous path blocks; no racing with `ensureRunning` |
| A4 | `reconcileOnIdle` refetch returns data including the just-completed response | Verified from V5-V6: runner sets Idle before SSE event; response committed before `finishRun` |

---

## Blockers

None.

---

## Tests Run

```bash
# Backend (all pass)
GOPROXY=direct GONOSUMCHECK=* GONOSUMDB=* go test -timeout 120s -race ./api/internal/handlers/ -count=1
# ok  18.362s

# Frontend (all pass)
cd frontend && npx vitest run src/hooks/useMessageQueue.test.ts src/pages/ChatPage.queue.test.tsx
# 46 passed (33 + 13)

# Vet (clean)
GOPROXY=direct GONOSUMCHECK=* GONOSUMDB=* go vet ./api/internal/handlers/
```

---

## Next Steps

1. Open PR against `fix/message-queue-deep-dive` branch
2. Verify CI passes
3. Consider FM2 follow-up: gate `doSendNow` on `!drainingRef.current` from the queue

---

## Files Modified

| File | Change |
|------|--------|
| `api/internal/handlers/proxy.go` | US-41.4: Removed wsConfig lookup from `onSessionIdle`; US-41.2: Added `isSessionActive` helper and 409 guard in `SendPromptAsync` |
| `api/internal/handlers/proxy_test.go` | US-41.4: Updated `TestProxy_OnSessionIdle_*` tests; US-41.2: Added 409 guard tests; Added `recordingActivitySessionIndex` mock; Updated `TestProxy_E2E_FullFlow` |
| `frontend/src/pages/ChatPage.tsx` | US-41.1: Guarded streaming state clear on `msgs.length > 0` in `reconcileOnIdle` |
| `frontend/src/pages/ChatPage.queue.test.tsx` | US-41.1: Added 3 tests for reconcileOnIdle guard behavior |
| `frontend/src/hooks/useMessageQueue.ts` | US-41.3: Added `_retryCount`, `mark_pending` action, `MAX_RETRIES`, 409 handling in `drainOne` |
| `frontend/src/hooks/useMessageQueue.test.ts` | US-41.3: Added 4 tests for 409 handling |
