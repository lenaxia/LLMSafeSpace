# 0184 — Message queue while streaming

**Date:** 2026-06-08
**Session:** Implement message queuing so the chat textarea stays enabled during LLM streaming
**Status:** Complete

---

## Objective

Enhance chat UX: when the LLM is streaming a response, the textarea stays enabled. Messages typed and sent during streaming are buffered in a queue with optimistic bubbles, then flushed one at a time in order when the session returns to idle.

---

## Work Completed

### Composer.tsx
- Textarea no longer disabled during streaming
- Send button always visible; Stop button shown alongside during streaming
- Queue count indicator ("N messages queued") above textarea

### ChatView.tsx
- Passes `queuedCount` prop to Composer

### ChatPage.tsx
- `handleSend` checks `streaming` — queues if true, calls `doSendNow` if false
- `pendingQueue` typed as `{id, text}[]` — flush filter uses unique ID to avoid text-content dedup collisions
- `queuedUserMessages` state renders optimistic user bubbles in chat
- Flush effect dequeues next message when `streaming` becomes false and queue has items
- `flushFailedRef` stops auto-drain on send failure; reset on SSE idle and session change
- `doSendNowRef` synced via `useEffect` (not render-time write)
- Queue cleared on session change

### Tests
- `ChatPage.queue.test.tsx`: 12 tests (queue, flush, sequential flush, flush halted on failure, abort during flush, session change clear, textarea enabled, stop button)
- `Composer.test.tsx`: 8 new streaming/queue tests
- All 705 tests pass, TypeScript compiles clean

### Adversarial review (post-PR)
AI reviewer (PR #69) requested changes:
1. Missing test: flush halted on send failure — **added**
2. Missing test: sequential flush of multiple queued messages — **added**
3. Missing test: abort during flush preserves remaining queue — **added**
4. Missing worklog entry — **this entry**
5. Double blank line at ChatPage.tsx:152 — **fixed**

---

## Files Touched

- `frontend/src/components/chat/Composer.tsx`
- `frontend/src/components/chat/Composer.test.tsx`
- `frontend/src/components/chat/ChatView.tsx`
- `frontend/src/pages/ChatPage.tsx`
- `frontend/src/pages/ChatPage.queue.test.tsx` (new)

---

## Deployment

PR #69: https://github.com/lenaxia/LLMSafeSpace/pull/69

---

## Open Items

- None — all reviewer findings addressed
