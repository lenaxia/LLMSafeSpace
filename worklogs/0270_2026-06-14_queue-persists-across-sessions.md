# Worklog: Queue Persists Across Sessions

**Date:** 2026-06-14
**Session:** Fix queued messages lost on session navigation
**Status:** Complete

---

## Objective

When a user queues a message (typed while busy) and navigates to a different session in the same workspace, the queued message was silently destroyed.

---

## Work Completed

### Root cause analysis

Two bugs:
1. `filter_session` effect in `useMessageQueue` deleted all messages when `sessionId` changed
2. `ChatPage.tsx` only called `queue.notifyIdle()` for the current session — idle events for other sessions were ignored despite arriving on the same workspace-level SSE stream

### Fix

- Removed `filter_session` effect — messages persist across session changes; display filter (`sessionQueue`) still scopes to current session
- Changed `drainingRef` (boolean) to `drainingSessionsRef` (Set<string>) for per-session drain tracking
- `notifyIdle(targetSessionId?)` accepts optional target session
- `drainOne` filters by target session and sends with that session ID
- `ChatPage` calls `queue.notifyIdle(event.session_id)` for all session idle events
- Removed dead `filter_session` action type and reducer case (per Rule 5)

### Tests

3 new + 1 updated in `useMessageQueue.test.ts`:
- Queued message persists when navigating away and is sent on idle for its session
- notifyIdle with targetSessionId does not affect current session queue
- Independent draining for different sessions
- Updated: "changing sessionId" test verifies messages are hidden but preserved

---

## Key Decisions

1. **Per-session Set over single boolean** — allows concurrent independent drains for different sessions in the same workspace.

2. **Display filter over state filter** — messages stay in reducer state; `sessionQueue` filters for display. Users only see pills for the session they're viewing.

3. **Workspace-scoped fix only** — cross-workspace navigation (ChatPage unmount) still loses queue state. This is a React fundamental requiring a context provider lift — not justified for this bug.

---

## Blockers

None.

---

## Tests Run

```bash
cd frontend && npx vitest run src/hooks/useMessageQueue.test.ts src/pages/ChatPage.queue.test.tsx
# 49 passed (36 + 13)
```

---

## Files Modified

| File | Change |
|------|--------|
| `frontend/src/hooks/useMessageQueue.ts` | Remove filter_session, per-session draining, notifyIdle(targetSessionId?) |
| `frontend/src/hooks/useMessageQueue.test.ts` | 3 new tests, 1 updated test |
| `frontend/src/pages/ChatPage.tsx` | Drain queue for any session idle event |
