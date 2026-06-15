# Worklog: Queue drain miss when session goes idle during SSE reconnect

**Date:** 2026-06-15
**Session:** Diagnosed and fixed stranded queued messages when SSE connection drops
**Status:** Complete

---

## Objective

Message "make passes for internal consistency, and ensure we solve the right problem at the right level at the right level of abstraction" was enqueued in workspace `a847faa5` session `ses_1361f1c44ffedDI7pqWvXkNGJt` but never sent to opencode. The session went idle but the queued message sat in Redis for 4+ minutes with no drain triggered.

---

## Work Completed

### Investigation

Reproduced the failure from API logs:

1. `POST /queue` at 20:18:22 — message enqueued, confirmed in Redis immediately.
2. At 20:18:22, workspace status showed `activeSessions=1`, session `busy` — correct, frontend queued instead of sending directly.
3. At 20:18:37, workspace status transitioned to `activeSessions=0`, session `idle`. `onSessionIdle` should have fired and launched `drainQueuedMessage`.
4. `GET /queue` at 20:22:58 (4+ minutes later) still returned the message — drain never ran.
5. No `queue.update` SSE event published. No `prompt_async` call in logs.

### Root cause

`drainQueuedMessage` is only triggered by `onSessionIdle`, which is only called from `dispatchProperties` inside the SSE tracker's `connectAndRead` loop. The SSE connection to the workspace pod is a long-poll. When it drops (network blip, idle timeout, pod restart), the tracker sleeps a backoff interval (starting at 2s, doubling to 30s max) then reconnects.

At 20:18:26, the SSE long-poll on one API replica closed (33.9s duration, no idle event in it). The session went idle at 20:18:37. During the 2s backoff window between the old connection closing and the new one opening, opencode emitted `session.status=idle` — to nobody. SSE has no replay: the reconnecting API server never received the event. `onSessionIdle` was never called. `drainQueuedMessage` never ran.

### Fix

Add an `onReconnect` callback to the SSE `Tracker`, called at the start of each `connectAndRead` attempt — after the pod IP and password are resolved, before the SSE stream opens. `ProxyHandler.Start()` wires `reconcileStrandedQueues` as the callback.

`reconcileStrandedQueues`:
1. Calls `GET /v1/statusz` on the agentd admin port (4098, `Authorization: Bearer <password>`).
2. Decodes the response into `agentd.StatuszResponse`.
3. For each session with `Status == "idle"` that has a non-empty Redis queue, calls `onSessionIdle(workspaceID, sessionID)`.

`onSessionIdle` is idempotent: it calls `drainQueuedMessage`, which dequeues one message at a time and returns immediately when the queue is empty. If the session is actually busy (race between the statusz call and the drain), `sendQueuedToOpencode` gets a 409, requeues with retry backoff, and the next idle event will drain it normally.

The reconcile fires before the SSE stream opens so if the stream immediately delivers a `session.status=busy` event, there is no double-drain.

### Tests written

Three scenario tests and five unit tests in `proxy_queue_drain_miss_test.go`:

| Test | What it proves |
|---|---|
| `TestDrainMiss_SSEDownWhenSessionGoesIdle` | Documents broken behavior: message stays in Redis when idle fires during SSE disconnect. Flips to fail if behavior changes. |
| `TestDrainMiss_SSEIdleTimeoutCausesReconnect` | Documents that `onSessionIdle` is not called when idle fires during reconnect backoff window. |
| `TestDrainMiss_QueueNotDrainedAfterReconnectWithNoNewIdleEvent` | **Regression gate**: pod serves only heartbeats on second SSE connection; `/v1/statusz` reports session idle; asserts `prompt_async` is called with the queued message. FAILS before this fix, PASSES after. |
| `TestReconcileStrandedQueues_Non200Statusz` | Non-200 from statusz → no drain, no panic, queue intact. |
| `TestReconcileStrandedQueues_MalformedJSON` | Malformed statusz body → no drain, no panic, queue intact. |
| `TestReconcileStrandedQueues_NoIdleSessions` | All sessions busy → no drain triggered. |
| `TestReconcileStrandedQueues_IdleButEmptyQueue` | Session idle but queue empty → no `prompt_async`. |
| `TestReconcileStrandedQueues_StatuszUnavailable` | Network error to statusz → no panic, queue intact. |

---

## Key Decisions

**Reconcile on every reconnect attempt, not just successful ones.**
The bug manifests after any connection drop. Firing the reconcile unconditionally at the start of each `connectAndRead` (before the stream opens) means even a series of failed reconnects will check the statusz and drain if needed. The cost is one extra HTTP call per reconnect attempt — negligible.

**Use `/v1/statusz` not `/v1/readyz`.**
`ReadyzResponse` does not include session status. `StatuszResponse` (on the same admin port 4098) includes `[]SessionInfo` each with `Status: "idle" | "busy"`. `StatuszResponse` is more expensive than `ReadyzResponse` (calls `IsHealthy` live, reads disk/memory/CPU), but this is called only on reconnect — an infrequent event — not on every request. Acceptable.

**Call `onSessionIdle` directly, not `drainQueuedMessage`.**
`onSessionIdle` also removes the session from `activeSessionsMap`, publishes the `session.status=idle` SSE event to frontend subscribers, records activity, and fetches/persists the session title. Calling `drainQueuedMessage` directly would bypass these side-effects. The correct path is `onSessionIdle`.

**Do not add a `Len > 0` check to the general `onSessionIdle` path.**
The reconcile checks `Len` before calling `onSessionIdle` to avoid calling it for sessions that have nothing stranded. But the existing `onSessionIdle` → `drainQueuedMessage` path is correct: `drainQueuedMessage` already returns immediately on an empty dequeue. No change needed to the existing idle handler.

---

## Blockers

None.

---

## Tests Run

```
go test -timeout 30s -run "TestDrainMiss|TestReconcileStrandedQueues" ./api/internal/handlers/ -v
# → 8 PASS

go test -timeout 120s ./api/internal/handlers/ ./api/internal/services/sse/
# → PASS both packages
```

---

## Files Modified

- `api/internal/services/sse/tracker.go` — add `ReconnectCallback` type, `onReconnect` field, `SetOnReconnect` setter; call `onReconnect` in `connectAndRead` after pod IP and password resolved
- `api/internal/handlers/proxy_events.go` — add `reconcileStrandedQueues`: calls `/v1/statusz`, finds idle sessions with non-empty queue, calls `onSessionIdle`; import `pkg/agentd` for `StatuszResponse` and `AgentdAdminPort`
- `api/internal/handlers/proxy_lifecycle.go` — wire `handler.reconcileStrandedQueues` as `sseTracker.SetOnReconnect` callback in `Start()`
- `api/internal/handlers/proxy_queue_drain_miss_test.go` — 8 new tests (scenario + unit) covering the drain miss bug and `reconcileStrandedQueues` unhappy paths
- `worklogs/0298_2026-06-15_queue-drain-on-sse-reconnect.md` — this worklog
