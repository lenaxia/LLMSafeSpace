# Worklog: ChatPage Assistant Message Duplication Race Fix

**Date:** 2026-05-29
**Session:** Follow-up to 0080 — duplicate messages still appearing after deploy
**Status:** Complete

---

## Objective

After deploying 0080, user reported duplicates persisted with a different
shape: ONLY the assistant response was duplicated (not the user message).
Reproduce, validate, fix with TDD.

---

## Assumptions Stated and Validated (Rule 7)

| # | Assumption | Validation method | Result |
|---|------------|-------------------|--------|
| A1 | Browser is running the new bundle (sha-19779ae from 0080) | DevTools network panel shows `index-DKNpa850.js` (matches sha-19779ae build) | ✅ TRUE — new bundle confirmed |
| A2 | TWO `GET .../message` requests fire after the prompt completes | DevTools Network panel | ✅ TRUE — two separate `message` requests (807 B + 765 B) visible after `prompt 204` |
| A3 | The two fetches come from useChatStream.send (line 70) and reconcileOnIdle (line 123) | Code inspection | ✅ TRUE — confirmed both call `messagesApi.getHistory` |
| A4 | The race causes assistant duplication: if reconcileOnIdle's fetch resolves FIRST, it clears localMessages and populates history; THEN useChatStream.send's onComplete re-adds assistant to localMessages | Constructed deterministic test that defers useChatStream.send's history fetch until AFTER reconcileOnIdle completes | ✅ TRUE — test reproduced the bug (got 2 assistant messages, expected 1) |
| A5 | The user message is NOT duplicated by this race because handleSend only adds the user message once (before send), not in onComplete | Code inspection | ✅ TRUE — explains why only assistant duplicates |

---

## Root Cause

`handleSend` in `ChatPage.tsx:419-437` (pre-fix) added the assistant
response to `localMessages` in the `send()` callback. This callback fires
when `useChatStream.send` resolves, which happens AFTER:
1. The `prompt_async` POST returns 204
2. The `session.status idle` SSE event arrives → resolves the await
3. `messagesApi.getHistory()` is called → resolves
4. `onComplete(lastAssistant)` is invoked → ChatPage adds finalMsg to `localMessages`

Meanwhile, the SAME `session.status idle` SSE event triggers
`reconcileOnIdle()` in ChatPage.tsx:337, which:
1. Calls `queryClient.refetchQueries({queryKey:["messages",...]})` →
   triggers a SECOND `messagesApi.getHistory()`
2. On success: `setLocalMessages([])` (the 0080 fix) and resets stream state

**The race:**
- If reconcileOnIdle's fetch resolves first → clears localMessages, populates history
- Then useChatStream.send's onComplete fires → re-adds assistant to localMessages
- Render: `[...history, ...localMessages]` → assistant appears in BOTH

Production confirmed via DevTools Network panel: two `GET /message`
requests fire concurrently. The order of resolution determines whether
the bug manifests. In our user's case, the bug consistently manifested,
confirming reconcileOnIdle's fetch lands first in production timing.

The user message was not affected because it is added only ONCE in
handleSend (before `send()` is called, not in the callback).

---

## Fix (TDD)

**Failing test written first** (`ChatPage.sse.test.tsx`):

```ts
it("REGRESSION: assistant response is not duplicated when reconcileOnIdle's history fetch resolves BEFORE useChatStream's onComplete", async () => {
  // Defer useChatStream.send's getHistory resolution until after
  // reconcileOnIdle completes, exposing the race.
  // Drive idle SSE → reconcileOnIdle clears + refetches → resolve send's
  // getHistory → assert assistant renders exactly once.
});
```

Verified test FAILED against current code: `expected length 1 but got 2`.

**Fix applied** (`frontend/src/pages/ChatPage.tsx`):

Removed `setLocalMessages` for the assistant response in handleSend's
onComplete callback. The assistant message is now delivered via two
mechanisms:
1. **During streaming**: live `streamingBubble` rendered from `sseStreamParts`
2. **After idle**: history (refetched by `reconcileOnIdle`) is authoritative

The user message stays in `localMessages` for optimistic UX during the
in-flight period, then is cleared by `reconcileOnIdle` when history catches up.

Also removed the now-unused `sseStreamPartsRef` and its sync useEffect,
since the streaming-parts-to-final-message conversion is no longer needed.

**Test result after fix:** 2/2 regression tests passing (0080's test +
this new race test). Full suite: 501/501.

---

## Why this approach

The previous logic in onComplete tried to "preserve thinking/tool structure
from streamed parts" by synthesizing finalMsg from `sseStreamPartsRef`.
This was conceptually duplicating what history already contains — opencode
returns the full part structure (step-start, reasoning, text, tool, step-finish)
in the history endpoint. Trusting history eliminates the duplication risk
entirely AND simplifies state management.

The "preserve streaming parts" concern is moot because:
- During streaming: streamingBubble renders sseStreamParts → user sees structure live
- After streaming: history has the same parts → user sees structure persisted
- The brief moment between (a) streaming ends and (b) history catches up:
  the streaming bubble disappears (streaming=false) but history is
  already updating in the background. Worst case: ~100ms gap with no
  bubble shown for that turn — acceptable.

---

## Validation

| Command | Result |
|---------|--------|
| Race regression test | passing in 257ms |
| Full ChatPage tests (5 files) | 84/84 passing |
| Full frontend suite | 501/501 passing (was 500, +1 race test) |
| `npm run build` | clean |
| `npm run lint` for ChatPage.tsx | 8 pre-existing errors (was 9; -1 from removing sseStreamPartsRef effect) |

---

## Files Modified

| File | Change |
|------|--------|
| `frontend/src/pages/ChatPage.tsx` | handleSend.onComplete no longer adds assistant message to localMessages; removed unused sseStreamPartsRef + its sync useEffect |
| `frontend/src/pages/ChatPage.sse.test.tsx` | Added race regression test that defers useChatStream's getHistory resolution to expose the production race |
| `worklogs/0081_2026-05-29_chatpage-assistant-duplication-race-fix.md` | This worklog |

---

## Related

- 0078: refresh-abort fix — original bug investigation
- 0080: localMessages clear on reconcile — partial fix; eliminated user-message duplication, but exposed this race for assistant-message duplication
- Epic 15 design intent: history is authoritative once idle. This fix completes the alignment with that intent.
