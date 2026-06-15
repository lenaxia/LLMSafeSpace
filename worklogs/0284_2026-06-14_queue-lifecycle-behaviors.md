# Worklog: Queue Lifecycle Behaviors

**Date:** 2026-06-14
**Session**: Fix queue behavior on workspace suspend/terminate, dispose, and abort
**Status:** Complete

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
- `onPhaseChange` now calls `publishDismissedForWorkspace` before `ClearWorkspace` for all suspend/terminate phases (previously only called on Terminating/Terminated).

### BUG 2: Dispose — queue not cleared after agent reload

**Root cause**: `AgentReloadHandler` had no access to the queue service or SSE broker.

**Fix**:
- Added `QueueClearer` and `BrokerPublisher` interfaces to `agent_reload.go`.
- Added `queueSvc` and `broker` fields to both `AgentReloadHandler` and `BulkReloadHandler`.
- Added `SetQueueClearer` and `SetBrokerPublisher` setters on both handlers.
- Added `clearQueueOnDispose` method on both handlers: peeks workspace queue, publishes dismissed SSE for each message, clears the queue.
- Wired queue service and broker in `app.go` alongside the existing SSETracker wiring.
- `Reload` and `reloadOne` both call `clearQueueOnDispose` after a successful dispose.

### BUG 3: Abort — queued messages stuck

**Root cause**: `AbortSession` was a pure proxy to opencode. No queue interaction.

**Behavior implemented**:
1. On abort: peek all queued messages for the session, dequeue them from Redis immediately, publish dismissed SSE for each pill.
2. Proxy the abort to opencode (kills the running turn).
3. In background (`flushAndAbortAfterIdle`): wait for the session to become idle via SSE drain subscription (abort causes idle event), then send all flushed messages to opencode via `prompt_async` (so they appear in the session transcript), then issue a second abort to stop them from being processed further.

Added `getPodIPAndPassword` helper to avoid duplicating pod-IP/password resolution across the two abort call sites.

---

## Tests

5 new tests in `proxy_queue_test.go`:

| Test | Covers |
|------|--------|
| `TestOnPhaseChange_SuspendPublishesDismissedAndClears` | Suspend phase: dismissed SSE published + queue cleared |
| `TestAbortSession_FlushesQueueThenAborts` | Abort: dismissed SSE + queue cleared + abort proxied |
| `TestAbortSession_EmptyQueue_JustAborts` | Abort with empty queue: abort still proxies cleanly |
| `TestClearQueueOnDispose_PublishesDismissedAndClears` | Dispose: dismissed SSE published + queue cleared |

All 5 pass. Full handler suite (25s) passes.

---

## Files Modified

| File | Change |
|------|--------|
| `api/internal/services/msgqueue/service.go` | Added `PeekAllWorkspace` |
| `api/internal/interfaces/interfaces.go` | Added `PeekAllWorkspace` to `MessageQueueService` |
| `api/internal/handlers/proxy_events.go` | `publishDismissedForWorkspace` helper; `onPhaseChange` clears on suspend too; `onPhaseChange` now publishes dismissed before clearing |
| `api/internal/handlers/proxy_handlers.go` | `AbortSession` rewritten; `flushAndAbortAfterIdle` goroutine; `getPodIPAndPassword` helper; `time` import added |
| `api/internal/handlers/proxy_lifecycle.go` | `GetMessageQueueService` and `GetBroker` getters added |
| `api/internal/handlers/agent_reload.go` | `QueueClearer`/`BrokerPublisher` interfaces; fields + setters on both handlers; `clearQueueOnDispose` on both handlers; called after dispose |
| `api/internal/app/app.go` | Wire `SetQueueClearer` and `SetBrokerPublisher` on reload handlers |
| `api/internal/handlers/proxy_queue_test.go` | 4 new test cases |
