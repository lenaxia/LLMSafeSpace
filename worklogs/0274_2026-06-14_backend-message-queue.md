# Worklog: Backend Message Queue

**Date:** 2026-06-14
**Session:** Move message queue from frontend local state to Redis-backed backend service
**Status:** Complete

---

## Objective

Queued messages (typed while busy) were stored in React component state. Navigating away from a workspace destroyed them. Moving to a Redis-backed backend queue ensures messages survive any navigation, page reload, or tab close.

---

## Work Completed

### Backend: Redis-backed queue service (`api/internal/services/msgqueue/`)

New package with:
- `Enqueue` — RPUSH + TTL refresh
- `Dequeue` — LPOP (FIFO order)
- `Requeue` — LPUSH (push to front for retries)
- `PeekAll` — LRANGE for listing
- `Len`, `Clear`, `ClearWorkspace`

Key format: `llmsafespace:msgqueue:{workspaceID}:{sessionID}`, 24h TTL.

11 unit tests with miniredis covering FIFO, isolation, requeue, TTL expiry.

### Backend: API endpoints

- `POST /api/v1/workspaces/:id/sessions/:sessionId/queue` — enqueue
- `GET /api/v1/workspaces/:id/sessions/:sessionId/queue` — list

### Backend: Drain on idle

`onSessionIdle` starts a goroutine that:
1. Dequeues the next message
2. Sends to opencode's `prompt_async` via direct HTTP
3. Publishes `queue.update` SSE event (sent/error)
4. On failure: re-queues with incremented retry count (max 5)

The drain is triggered by SSE idle events, creating a natural event-driven loop: idle → drain → busy → ... → idle → drain → (empty) → done.

### Backend: Cleanup on terminate

`onPhaseChange` for Terminated/Terminating calls `ClearWorkspace` to remove all queue keys for the workspace.

### Backend: Wiring

- `MessageQueueService` interface in `interfaces.go`
- Queue service created from cache service's Redis client in `app.go`
- Injected into proxy handler via `SetMessageQueueService`

### Frontend: Simplified `useMessageQueue`

Removed all drain logic (`drainOne`, `notifyIdle`, `drainingSessionsRef`, `sendAsync` calls). The hook now:
- `enqueue(text)` → POST to queue API → add to display state
- `markSent(id)` / `markError(id, msg)` — called from SSE handler
- Hydrates from `GET /queue` on mount (catches messages queued from other tabs)
- Display filters by session, state preserves across session changes

### Frontend: SSE event handling

`ChatPage.tsx` handles `queue.update` events:
- `event: "sent"` → `queue.markSent(messageID)` — removes pill
- `event: "error"` → `queue.markError(messageID, error)` — shows error pill

---

## Key Decisions

1. **Redis LIST per session** — simple FIFO with LPUSH/RPOP. No Lua scripts needed. TTL prevents unbounded growth.

2. **Event-driven drain** — triggered by `onSessionIdle` (SSE tracker). No polling, no timers. Each successful drain triggers a busy period, and the next idle triggers the next drain.

3. **Goroutine in onSessionIdle** — non-blocking. The SSE tracker continues reading events while the drain HTTP call is in flight.

4. **Frontend display state stays local** — the backend stores the authoritative queue, the frontend keeps a local copy for display (pills). SSE events sync the two.

5. **Retry on failure** — failed sends are re-queued with incremented retryCount. After 5 retries, the message is dropped with an error SSE event.

---

## Assumptions

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | Redis is available (already a dependency) | Verified: cache service, rate limiter, DEK cache all use it |
| A2 | `onSessionIdle` fires once per idle transition | Verified: SSE tracker calls it on `session.status=idle` events |
| A3 | One drain goroutine per idle event is safe | Verified: each drain triggers a busy period; next drain only fires after next idle |

---

## Blockers

None.

---

## Tests Run

```bash
# Backend
go test -timeout 120s -race ./api/internal/services/msgqueue/ -v
# 11 passed

go test -timeout 120s -race ./api/internal/handlers/ -count=1
# ok  34.603s

# Frontend
npx vitest run src/hooks/useMessageQueue.test.ts src/pages/ChatPage.queue.test.tsx
# 18 passed
```

---

## Files Modified

| File | Change |
|------|--------|
| `api/internal/services/msgqueue/service.go` | New — Redis-backed queue |
| `api/internal/services/msgqueue/service_test.go` | New — 11 tests |
| `api/internal/interfaces/interfaces.go` | Add `MessageQueueService` interface |
| `api/internal/handlers/proxy_handlers.go` | Add `EnqueueMessage`, `ListQueue` |
| `api/internal/handlers/proxy_events.go` | Drain on idle, SSE events, cleanup on terminate |
| `api/internal/handlers/proxy_lifecycle.go` | `SetMessageQueueService` setter |
| `api/internal/handlers/proxy.go` | Add `queueSvc` field |
| `api/internal/app/app.go` | Wire queue service from cache Redis client |
| `api/internal/server/router.go` | Register queue routes |
| `frontend/src/api/messages.ts` | Add `queueMessage`, `getQueue` |
| `frontend/src/hooks/useMessageQueue.ts` | Rewrite — API-backed, no local drain |
| `frontend/src/hooks/useMessageQueue.test.ts` | Rewrite — 10 tests for new architecture |
| `frontend/src/pages/ChatPage.tsx` | Handle `queue.update` SSE events |
| `frontend/src/pages/ChatPage.queue.test.tsx` | Update — 8 tests for new flow |
