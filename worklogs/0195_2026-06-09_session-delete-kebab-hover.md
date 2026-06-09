# Worklog 0195 — Fix: session delete from kebab menu + kebab destructive hover visibility

**Date:** 2026-06-09
**PR:** #74
**Area:** full-stack (backend proxy, database, frontend)

---

## Summary

Fixed two bugs: (1) session delete from any kebab menu did nothing — the
sidebar called `renameSession` with empty title, ChatPage just invalidated
cache, and the backend had no DELETE route; (2) KebabMenu destructive items
had invisible hover in dark mode.

---

## Problem

### Bug 1: Session delete does nothing

All three kebab delete handlers were broken:

- **Sidebar session kebab** (`Sidebar.tsx:193`): called
  `workspacesApi.renameSession(ws.id, sessionId, "")` — renames to empty
  string instead of deleting.
- **ChatPage kebab** (`ChatPage.tsx:662`): just called
  `queryClient.invalidateQueries` + `navigate` without any delete API call.
- **Backend**: no `DELETE /sessions/:sessionId` route existed anywhere in
  the stack — no proxy route, no service method, no database method.

Opencode already exposes `DELETE /session/:sessionID` (recursive, handles
children).

### Bug 2: Destructive kebab hover invisible in dark mode

`KebabMenu.tsx` destructive items used `hover:bg-destructive/10`. In dark
mode, `--color-destructive` is `hsl(0 62.8% 30.6%)` — a very dark red. At
10% opacity on a dark background, the hover state is invisible.

---

## Root cause analysis

### Session delete

The full stack was never implemented. The `DatabaseService` had
`DeleteSessionIndex` (bulk, workspace-scoped) but nothing per-session.
`SessionIndexService` had no `DeleteSession`. `WorkspaceService` had no
`DeleteSession`. The router had no DELETE route. The frontend had no
`deleteSession` API method.

The existing `onDeleteSession` handler in Sidebar.tsx appears to have been a
placeholder that was never wired up correctly — it renames the session to an
empty string (which is effectively hiding it) rather than deleting it.

### Hover visibility

Tailwind's `destructive` semantic color maps to a very dark red in dark mode.
Using it at 10% opacity produces a background that is visually
indistinguishable from the dark card background.

---

## Solution

### Full-stack implementation (TDD — tests written first at each layer)

**Database layer** — `DeleteSessionTree`: recursive SQL CTE (`WITH RECURSIVE`)
that deletes a session and all its descendants from `session_index` in a
single query. The `workspace_id` predicate in both the anchor and recursive
join prevents cross-workspace traversal. 3 tests (success, not-found,
db-error).

**Session index service** — `DeleteSession`: delegates to
`DeleteSessionTree`. 2 tests (delegates, error propagation).

**Proxy handler** — `DeleteSession`: validates session ID, proxies
`DELETE /session/:id` to opencode (`isWriteOp=false`), synchronously
deletes from session_index on success, async removes active session +
invalidates parent cache + publishes SSE `session.status "deleted"` for
multi-tab consistency. 6 tests (happy path, endpoint mapping, invalid ID,
workspace not active, bypasses limit, opencode 404).

**Router** — `DELETE /:id/sessions/:sessionId` proxy route. Added to route
existence table and OpenAPI contract allowlist.

**Frontend API** — `workspacesApi.deleteSession(workspaceId, sessionId)`.

**Frontend callers** — both Sidebar.tsx and ChatPage.tsx fixed to call
`deleteSession` with 404-as-success + error handling + cache invalidation.

**KebabMenu hover** — changed from `hover:bg-destructive/10` to
`hover:bg-red-500/10 dark:hover:bg-red-500/20` which is always visible.

**Go SDK** — `SessionsService.Delete` method.

### Reconciliation layers

| Layer | Mechanism | Catches |
|-------|-----------|---------|
| Immediate | handler sync index delete + SSE event | Normal delete via UI |
| Multi-tab | SSE `session.status "deleted"` → existing `invalidateQueries` | Other tabs |
| Tab switch | `refetchOnWindowFocus` (30s stale) | Passive |

---

## Key decisions

- **Synchronous index delete, async cache cleanup**: The session_index row
  must be removed before the response returns to prevent concurrent
  `ListWorkspaceSessions` returning stale data. Cache/broker cleanup can be
  deferred.

- **No SSE `session.deleted` event type**: Reused existing `session.status`
  with `Status: "deleted"`. The frontend already invalidates
  `["sessions", workspaceId]` on any `session.status` event — zero frontend
  changes needed for multi-tab consistency.

- **Recursive CTE over per-child deletion**: One SQL query handles arbitrary
  tree depth efficiently. PostgreSQL materializes the CTE before DELETE,
  avoiding read-write conflicts on the same table.

- **404-as-success in frontend**: Handles double-delete race (sidebar + chat
  kebab, or two tabs).

---

## Deferred to follow-up

- Session tracker `onSessionDeleted` callback for opencode-initiated
  deletions (CLI deletes)
- `BackfillSessionParents` prune pass for stale session_index
  reconciliation

---

## Files changed

17 files, +243/-8 lines:
- `api/internal/services/database/database.go` + test
- `api/internal/services/sessionindex/service.go` + test
- `api/internal/interfaces/interfaces.go`
- `api/internal/mocks/database.go`
- `api/internal/handlers/proxy.go` + test
- `api/internal/server/router.go` + contract test
- 3 inline test mock updates
- `frontend/src/api/workspaces.ts`
- `frontend/src/components/layout/Sidebar.tsx`
- `frontend/src/pages/ChatPage.tsx`
- `frontend/src/components/ui/KebabMenu.tsx`
- `sdks/go/services.go`
