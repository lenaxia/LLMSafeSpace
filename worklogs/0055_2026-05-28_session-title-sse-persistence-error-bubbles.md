# Worklog: Session Title Persistence via SSE, Nav Fix, Auto-Rename Fix, Error Bubbles

**Date:** 2026-05-28
**Session:** Fix nav bar session titles, workspace auto-rename, add SSE-driven title persistence, surface session.error as chat bubbles
**Status:** Complete

---

## Objective

Fix two reported bugs:
1. Nav bar shows "New chat" for all sessions despite the chat window showing correct titles
2. Workspace name not updating to reflect the first session's title

Additionally: surface `session.error` SSE events to the user.

---

## Work Completed

### Bug 1: Nav bar showing "New chat"

**Root cause:** Race condition. `useSessionTitle` fetched the title from opencode (via proxy) and invalidated the `["sessions", workspaceId]` query. That query refetches from PostgreSQL, but `fetchAndPersistTitle` (a fire-and-forget goroutine) hadn't written the title to the DB yet.

**Fix (frontend):** `useSessionTitle` now uses `queryClient.setQueryData` to directly update the sidebar's cached session list with the title from opencode — no DB round-trip needed for immediate display.

**Fix (backend):** Added `persistTitleFromEvent` in `proxy.go`. The `onRawEvent` handler now intercepts `session.updated` SSE events from opencode and writes the title to PostgreSQL immediately. This eliminates the race — the DB is updated the moment opencode generates the title, not after a separate HTTP fetch.

**Fix (frontend SSE):** `handleSSEEvent` in ChatPage now handles `opencode.event` with `event_type === "session.updated"` and updates the sidebar cache in real-time via `setQueryData`.

### Bug 2: Workspace not getting updated name

**Root cause:** Two issues:
1. Auto-rename regex only matched `adjective-noun-number` pattern. If workspace was previously renamed to a temporary opencode title like "New session - 2026-05-27T23:03:56.256Z", the regex didn't match.
2. Auto-rename fired on temporary titles before the LLM generated the real one.

**Fix:** 
- Expanded regex to also match `"New session - <timestamp>"` pattern
- Added guard to skip renaming when `sessionTitle` itself looks like a temporary opencode default

### session.error as chat bubbles

**What it is:** SSE event emitted by opencode when the LLM provider fails mid-stream (rate limit, context window exceeded, invalid key, etc.). The HTTP POST already returned 204, so the existing error path never fires.

**Fix:** When `session.error` arrives for the active session, inject an error message into `localMessages` with `type: "error"`. `MessagePart.tsx` renders it as a red-bordered bubble in the chat flow — persistent, contextual, scrolls with history.

---

## Key Decisions

- **setQueryData over invalidateQueries** — eliminates the race condition entirely; sidebar updates instantly from the same source the chat header uses.
- **Backend SSE-driven persistence** — tapping into `session.updated` events means the DB is always up to date without requiring the frontend to make a separate persist call. Works even if the user closes the tab mid-stream.
- **Error as message bubble, not banner** — banners are dismissible and easy to miss. A message bubble is persistent, contextual, and visible in scroll history.
- **No `renameSession` call from frontend** — the backend handles persistence via SSE events, so the frontend only needs to update its local cache.

---

## Blockers

None.

---

## Tests Run

```
cd frontend && npx vitest run
# 61 test files, 432 tests passing (38 new tests added)
# New test files:
#   - src/hooks/useSessionTitle.test.tsx (15 tests)
#   - src/pages/ChatPage.autorename.test.tsx (10 tests)
#   - src/components/layout/Sidebar.sessions.test.tsx (5 tests)
#   - src/lib/names.test.ts (8 tests)

cd . && go build ./api/...
# Compiles clean
```

---

## Next Steps

- Verify `session.updated` event payload shape against a live opencode instance (flat vs nested format)
- Consider handling `session.error` in the sidebar too (e.g., red dot indicator on errored sessions)
- The `fetchAndPersistTitle` goroutine in `SendMessage` is now redundant (SSE handles it) — could be removed in a cleanup pass

---

## Files Modified

- `api/internal/handlers/proxy.go` — added `persistTitleFromEvent`, hooked into `onRawEvent` for `session.updated`
- `frontend/src/hooks/useSessionTitle.ts` — `setQueryData` instead of `invalidateQueries`
- `frontend/src/pages/ChatPage.tsx` — `session.updated` cache update, `session.error` message injection, auto-rename regex fix
- `frontend/src/components/chat/MessagePart.tsx` — added `"error"` part type renderer
- `frontend/src/hooks/useSessionTitle.test.tsx` — new (15 tests)
- `frontend/src/pages/ChatPage.autorename.test.tsx` — new (10 tests)
- `frontend/src/components/layout/Sidebar.sessions.test.tsx` — new (5 tests)
- `frontend/src/lib/names.test.ts` — new (8 tests)
