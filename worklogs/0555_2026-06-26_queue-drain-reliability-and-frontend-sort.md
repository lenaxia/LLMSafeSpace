# Worklog: 0555_2026-06-26_queue-drain-reliability-and-frontend-sort.md

**Date:** 2026-06-26
**Session:** Fix two production bugs reported as GitHub issues #387 and #388 — a frontend message rendering regression and a stranded-queue reliability gap.
**Status:** PR #418 open, CI passing (govulncheck failure is pre-existing pgxpool vuln, unrelated)

---

## Objective

1. **#387 (P1):** Queued messages (`msg_q_*`) rendered after all native messages (`msg_e*`) on page reload, regardless of timestamp. Root cause: `selectChronological` sorted by `id.localeCompare` instead of `createdAt`.

2. **#388 (P2):** Queued messages sat in Redis indefinitely when the `session.status=idle` SSE event was lost — the drain trigger depended entirely on receiving that event.

---

## Changes

### P1 — Frontend sort (`useMessageHistory.ts`)
- Replaced `id.localeCompare` with `createdAt` timestamp comparison + `id` tiebreaker.

### P2 — Queue drain reliability
Three complementary mechanisms:

1. **Drain on enqueue** (`proxy_handlers.go`): If session is idle at enqueue time, drain immediately. Catches Mode C (agent already finished, no idle event coming).

2. **Periodic statusz sweep** (`proxy_events.go`): 30s goroutine scans Redis via `PeekAllGlobal`, calls `reconcileSessionState` (queries `/v1/statusz`) for each workspace with non-empty queues. Catches Mode A (idle event lost, session stuck in `activeSess`).

3. **409 safety** (`proxy_events.go`): `drainQueuedMessage` now treats 409 Conflict as a non-retryable signal — requeues once and returns instead of burning the retry budget. Prevents message loss when drain-on-enqueue fires on a false-idle read.

### Supporting changes
- `PeekAllGlobal` added to `MessageQueueService` interface + `msgqueue.Service` (with `peekByPattern` helper to eliminate duplication with `PeekAllWorkspace`).
- `stopCh` field on `ProxyHandler` for clean sweep goroutine shutdown.
- `errSessionBusy` sentinel error for 409 handling.

---

## Failure modes covered

| Mode | Scenario | Catch mechanism |
|------|----------|----------------|
| A | SSE idle event lost | Periodic sweep → statusz → drain |
| B | Drain goroutine crashed | Drain on next enqueue; periodic sweep |
| C | Enqueue while already idle | Drain on enqueue (immediate) |
| D | Normal busy→idle | SSE event path (unchanged) |
| E | False-idle drain-on-enqueue | 409 → requeue+return (no message loss) |
