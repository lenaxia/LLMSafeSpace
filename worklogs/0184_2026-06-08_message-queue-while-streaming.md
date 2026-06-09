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

### Final correctness pass

1. **BUG-3 (ID collision):** `Date.now()` replaced with monotonic `++idCounterRef.current` for all generated IDs (`queued-`, `local-`, `error-`)
2. **BUG-1 (reconcileOnIdle flicker):** `reconcileOnIdle` now guards `setLocalMessages([])` with `flushInProgressRef` — prevents wiping a just-flushed user message during concurrent SSE idle reconciliation
3. **RACE-1 (cross-session localStreaming):** `useChatStream.send()` finally block now checks `currentSessionRef.current === capturedSessionId` before setting `localStreaming(false)` — prevents stale send from killing session B's streaming indicator
4. **RACE-2 (effect ordering):** Added comment documenting that `chatError` effect MUST be defined before flush effect
5. **Flush recovery:** Added `flushTick` state — bumped on SSE idle, added to flush effect deps — forces re-evaluation after `flushFailedRef` reset, enabling queue drain to resume after transient failure
6. **Test:** identical queued messages produce separate bubbles and flush separately
7. **Test:** flush recovers after failure when SSE idle resets flushFailedRef

### Tests
- `ChatPage.queue.test.tsx`: 14 tests
- `Composer.test.tsx`: 8 new streaming/queue tests
- All 707 tests pass, TypeScript compiles clean

---

## Files Touched

- `frontend/src/components/chat/Composer.tsx`
- `frontend/src/components/chat/Composer.test.tsx`
- `frontend/src/components/chat/ChatView.tsx`
- `frontend/src/pages/ChatPage.tsx`
- `frontend/src/pages/ChatPage.queue.test.tsx` (new)

- `frontend/src/hooks/useChatStream.ts`

---

## Deployment

PR #69: https://github.com/lenaxia/LLMSafeSpace/pull/69

---

## Open Items

- None — all reviewer findings addressed, final pass complete
