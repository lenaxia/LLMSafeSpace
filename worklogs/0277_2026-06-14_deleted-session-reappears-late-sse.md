# Worklog: Deleted Session Reappears — Late SSE Event Re-insertion

**Date:** 2026-06-14
**Session:** Fix deleted sessions reappearing in sidebar after a brief disappearance
**Status:** Complete

---

## Objective

When deleting a session via the sidebar kebab menu, the session would disappear for a second then reappear. Root cause analysis and fix.

---

## Work Completed

### Root Cause

After `DeleteSession` removes the session from opencode (via proxied DELETE) and `session_index` (PostgreSQL), late SSE events from opencode would **re-insert** the session into `session_index`:

- `onSessionIdle` → `RecordMessage` (INSERT...ON CONFLICT upsert)
- `onRawEvent("session.updated")` → `persistTitleFromEvent` → `UpsertTitle` / `UpsertParent`
- `onRawEvent("session.next.step.ended")` → `persistContextFromEvent` → `UpsertContextUsed`

The frontend refetches sessions after the delete (cache invalidation), sees the session gone, then refetches again (triggered by SSE events or navigation) and sees it reappear because the late event re-inserted it.

### Fix

Added a `deletedSessions` set (`map[string]struct{}`) to `ProxyHandler`:

- `DeleteSession` marks the session as deleted after opencode + session_index cleanup.
- `onSessionIdle` — skips `RecordMessage` + `fetchAndPersistTitle` + `drainQueuedMessage` for deleted sessions.
- `persistTitleFromEvent` — skips `UpsertTitle` + `UpsertParent` for deleted sessions.
- `persistContextFromEvent` — skips `UpsertContextUsed` for deleted sessions.
- `invalidateCaches` (workspace phase changes) clears the deleted set for that workspace.
- Bounded at 500 entries (evicts oldest half if exceeded — effectively never triggers in practice).

### Tests

- `TestProxy_DeleteSession_SuppressesLateSSEUpserts` — verifies `onSessionIdle` does NOT call `RecordMessage` for a deleted session.
- `TestProxy_DeleteSession_AllowsNonDeletedSessionUpserts` — verifies `onSessionIdle` DOES call `RecordMessage` for a non-deleted session.

---

## Files Modified

- `api/internal/handlers/proxy.go` — added `deletedSessions` field + initialization
- `api/internal/handlers/proxy_handlers.go` — `markSessionDeleted`, `isSessionDeleted`, `clearDeletedSessions` helpers + call in `DeleteSession`
- `api/internal/handlers/proxy_events.go` — guards in `onSessionIdle`, `persistTitleFromEvent`, `persistContextFromEvent`
- `api/internal/handlers/proxy_connections.go` — `clearDeletedSessions` call in `invalidateCaches`
- `api/internal/handlers/proxy_test.go` — 2 new regression tests
