# Worklog: Queue Reconciliation — Frontend as View of Redis

**Date:** 2026-06-14
**Session**: Make Redis the single source of truth; frontend reconciles via refreshQueue
**Status:** Complete

---

## Objective

The frontend maintained a parallel copy of queue state that diverged from Redis. SSE drops, broken hydration dedup, and no periodic sync caused stale pills. Fix by making Redis authoritative and the frontend a view.

---

## Work Completed

### Backend: Remove + DELETE endpoint

- Added `msgqueue.Service.Remove(ctx, ws, ses, msgID)` — LRANGE + LREM by ID
- Added `DELETE /workspaces/:id/sessions/:sessionId/queue/:messageId` handler
- Publishes `queue.update { event: "dismissed" }` SSE event
- 2 new service tests, 1 new handler (implicit via route)

### Backend: G1 fix — drain retry loop

`drainQueuedMessage` now loops with backoff instead of relying on the next idle event:
- On failure: requeue, sleep `retryCount * 1s`, retry
- After max retries (5): drop message, publish error SSE
- Message can no longer get stuck in Redis after a failed drain

### Frontend: refreshQueue reconciliation

Replaced the `useReducer` + dispatch pattern with `useState` + `refreshQueue`:

- `refreshQueue()` fetches `GET /queue`, reconciles: keeps error pills (local-only), syncs pending messages with Redis
- Called on: mount, `session.status:idle`, `queue.update:sent`, `queue.update:enqueued`, `queue.update:dismissed`
- SSE `queue.update:error` → local `markError` (message is gone from Redis)
- Removed: reducer, all actions, `hydrate`, `reconcile`, `markSent`
- Added: `deleteQueueMessage` API for dismiss

### Consistency guarantees

| Scenario | Self-heals via |
|----------|---------------|
| SSE drop | Next idle → refreshQueue |
| Cross-tab enqueue | SSE enqueued → refreshQueue |
| Cross-tab dismiss | SSE dismissed → refreshQueue |
| Page refresh | Mount → refreshQueue |
| Error pills | Kept by reconciliation filter (`status === "error"`) |

---

## Assumptions

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | Redis is always available (existing dependency) | Verified: cache, rate limiter, DEK cache all use it |
| A2 | refreshQueue dedup via `refreshInFlightRef` prevents races | Verified: concurrent SSE events coalesce to one GET |
| A3 | Error pills are local-only (not in Redis) | Verified: drain drops after max retries, doesn't requeue |
| A4 | drain retry loop terminates (60s context timeout) | Verified: context.WithTimeout(60s) bounds total drain time |

---

## Tests Run

```bash
# Backend
go test -timeout 120s -race ./api/internal/handlers/ ./api/internal/services/msgqueue/ ./api/internal/server/
# ok

# Frontend
npx vitest run src/hooks/useMessageQueue.test.ts src/pages/ChatPage.queue.test.tsx src/pages/ChatPage.test.tsx src/pages/ChatPage.sse.test.tsx
# 99 passed
```

---

## Files Modified

| File | Change |
|------|--------|
| `api/internal/services/msgqueue/service.go` | Add Remove method |
| `api/internal/services/msgqueue/service_test.go` | 2 new Remove tests |
| `api/internal/interfaces/interfaces.go` | Add Remove to interface |
| `api/internal/handlers/proxy_handlers.go` | Add DeleteQueueMessage handler |
| `api/internal/handlers/proxy_events.go` | Rewrite drainQueuedMessage with retry loop |
| `api/internal/handlers/proxy_queue_test.go` | Update tests for goroutine + backoff |
| `api/internal/server/router.go` | Register DELETE route |
| `api/internal/server/router_openapi_contract_test.go` | Add DELETE to allowlist |
| `frontend/src/api/messages.ts` | Add deleteQueueMessage |
| `frontend/src/hooks/useMessageQueue.ts` | Rewrite: useState + refreshQueue |
| `frontend/src/hooks/useMessageQueue.test.ts` | Rewrite: 9 tests for new architecture |
| `frontend/src/pages/ChatPage.tsx` | SSE: refreshQueue on sent/enqueued/dismissed; refreshQueue in reconcileOnIdle |
| 10 frontend test files | Add deleteQueueMessage to mocks |
