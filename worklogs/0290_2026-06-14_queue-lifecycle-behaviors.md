# Worklog: Queue Lifecycle Behaviors

**Date:** 2026-06-14
**Session**: Fix queue behavior on workspace suspend/terminate, dispose, and abort
**Status:** Complete (iterated through review)

---

## Objective

Three queue lifecycle behaviors were missing:

1. **Suspend/Terminate**: Queue was cleared from Redis but no dismissed SSE was published, leaving UI pills stuck.
2. **Dispose (agent reload)**: Queue was not cleared at all after `agentd` reloaded opencode. Messages would be drained to a freshly-reloaded opencode with no session context.
3. **Abort**: Abort only proxied to opencode. Queued messages were neither sent nor dismissed, leaving them indefinitely stuck.

---

## Work Completed

### BUG 1: Suspend/Terminate — no dismissed SSE before clear

**Root cause**: `onPhaseChange` called `queueSvc.ClearWorkspace` but never published dismissed SSE events, so UIs had no signal to remove pending pills.

**Fix**:
- Added `msgqueue.Service.PeekAllWorkspace` — scans all session queue keys for a workspace and returns all messages.
- Added `interfaces.MessageQueueService.PeekAllWorkspace` to the interface.
- Added `ProxyHandler.publishDismissedForWorkspace` — peeks workspace queue, publishes `queue.update dismissed` SSE for each message.
- `onPhaseChange` now calls `publishDismissedForWorkspace` before `ClearWorkspace` for all four suspend/terminate phases (previously only cleared on Terminating/Terminated, and never published dismissed).
- Removed redundant inner phase check (always true inside outer guard).

### BUG 2: Dispose — queue not cleared after agent reload

**Root cause**: `AgentReloadHandler` had no access to the queue service or SSE broker.

**Fix**:
- Added `QueueClearer` and `BrokerPublisher` interfaces to `agent_reload.go`.
- Added `queueSvc` and `broker` fields to both `AgentReloadHandler` and `BulkReloadHandler`.
- Added `SetQueueClearer` and `SetBrokerPublisher` setters on both handlers.
- Extracted `clearQueueForWorkspace` as a package-level helper (DRY): peeks workspace queue, publishes dismissed SSE for each message, clears the queue.
- Both handlers delegate `clearQueueOnDispose` to `clearQueueForWorkspace`.
- Both handlers use `context.Background()` for `clearQueueOnDispose` so a client disconnect after dispose doesn't skip queue cleanup.
- Wired queue service and broker in `app.go` alongside the existing SSETracker wiring.
- `Reload` and `reloadOne` both call `clearQueueOnDispose` after a successful dispose.

### BUG 3: Abort — queued messages stuck

**Root cause**: `AbortSession` was a pure proxy to opencode. No queue interaction.

**Behavior implemented** (ordering corrected from initial draft via review):
1. Proxy the abort to opencode first (kills the running turn).
2. Only on abort success: peek queued messages, clear from Redis, publish dismissed SSE for each pill.
3. In background (`flushAndAbortAfterIdle`): wait for idle via SSE drain subscription, then send each flushed message to opencode one at a time (idle-wait between each to avoid 409 session-busy), then issue a second abort so they appear in the transcript but are not processed further.

Note: PeekAll and Clear are separate Redis commands — a message enqueued between them will be cleared without a dismissed SSE event (acceptable: the message is still discarded).

Added:
- `getPodIPAndPassword` helper to centralize pod-IP/password resolution.
- `sendQueuedToOpencode` refactored to use `getPodIPAndPassword` (DRY).

---

## Tests

11 new tests across two files:

| Test | Covers |
|------|--------|
| `TestOnPhaseChange_SuspendPublishesDismissedAndClears` | Suspend phase: dismissed SSE + queue cleared |
| `TestAbortSession_FlushesQueueThenAborts` | Abort: dismissed SSE + queue cleared + abort proxied |
| `TestAbortSession_EmptyQueue_JustAborts` | Abort with empty queue: abort still proxies |
| `TestAbortSession_FailurePreservesQueue` | Abort 500: queue untouched, no dismissed SSE |
| `TestClearQueueOnDispose_PublishesDismissedAndClears` | AgentReloadHandler dispose: dismissed + cleared |
| `TestBulkReloadHandler_ClearQueueOnDispose` | BulkReloadHandler dispose: dismissed + cleared |
| `TestFlushAndAbortAfterIdle_SingleMessage` | Single-message flush with real SSE tracker |
| `TestFlushAndAbortAfterIdle_MultipleMessages` | Multi-message flush: one-at-a-time, idle-wait between |
| `TestPeekAllWorkspace_MultiSession` | Multi-session workspace peek (msgqueue pkg) |
| `TestPeekAllWorkspace_Empty` | Empty workspace peek (msgqueue pkg) |

All pass. Full handler suite + race detector passes.

---

## Files Modified

| File | Change |
|------|--------|
| `api/internal/services/msgqueue/service.go` | Added `PeekAllWorkspace` |
| `api/internal/services/msgqueue/service_test.go` | 2 new tests for `PeekAllWorkspace` |
| `api/internal/interfaces/interfaces.go` | Added `PeekAllWorkspace` to `MessageQueueService` |
| `api/internal/handlers/proxy_events.go` | `publishDismissedForWorkspace`; `onPhaseChange` dismiss+clear on all phases; `sendQueuedToOpencode` refactored to use `getPodIPAndPassword` |
| `api/internal/handlers/proxy_handlers.go` | `AbortSession` rewritten (abort first, then peek/clear); `flushAndAbortAfterIdle` goroutine with per-message idle-wait; `getPodIPAndPassword` helper |
| `api/internal/handlers/proxy_lifecycle.go` | `GetMessageQueueService` and `GetBroker` getters; `GetBroker` nil-safe |
| `api/internal/handlers/agent_reload.go` | `QueueClearer`/`BrokerPublisher` interfaces; `clearQueueForWorkspace` shared helper; fields + setters on both handlers; `context.Background()` for both dispose paths |
| `api/internal/app/app.go` | Wire `SetQueueClearer` and `SetBrokerPublisher` on reload handlers |
| `api/internal/handlers/proxy_queue_test.go` | 9 new test cases |
