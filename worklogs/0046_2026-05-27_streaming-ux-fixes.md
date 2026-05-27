# Worklog: Streaming UX Fixes — User Echo, Thinking Blocks, Bubble Overflow

**Date:** 2026-05-27
**Session:** Fix three streaming UX bugs: user message appearing in response, thinking block disappearing, streaming bubble overflow
**Status:** In Progress (blocked on live SSE event structure diagnosis)

---

## Objective

Fix three reported streaming UX issues:

1. User request text appears as part of the assistant response bubble during streaming
2. Thinking block disappears after streaming completes — should stay rendered as a blockquote-style sub-block
3. Streaming response bubble overflows on top of other chat bubbles instead of pushing them down

---

## Work Completed

### Round 1: Initial streaming UX fixes (commit `54cb589`)

- **Bug 3 fix — Bubble overflow:** Replaced `@tanstack/react-virtual` in `MessageList.tsx` with a simple flex column layout. The virtualizer used absolute positioning which caused the streaming bubble (rendered outside the virtualizer container) to overflow on top of other messages. Now all messages including the streaming bubble are in normal document flow inside the scroll container.
- **Bug 2 fix — Thinking disappears:** `transformHistory` in `messages.ts` was filtering parts to `type === "text"` only. Added `thinking` and `reasoning` part types. `parseStreamEvent` in `ChatPage.tsx` now tracks `sseThinkingText` separately from `sseStreamText`. `MessagePart.tsx` renders thinking as a collapsible `<details>` with `border-l-2` blockquote content when completed, and with pulsing brain icon when streaming.
- **Bug 1 attempt — User echo:** Added `messageID` lock-on filter and `role` field filter to skip user echo events. Added nested SSE format unwrapping. Fixed E2E test SSE data format from nested to flat.
- **Test updates:** `ChatView` mock updated with `streamedThinkingText` data attribute. 10 new SSE tests added.
- 369 tests passing, deployed to cluster.

### Round 2: Fix dead user-echo filter (commit `46dd2ac`)

- **Assumption validated false:** Inspected all backend test data (`proxy_filter_test.go`, `session_tracker_test.go`, `stream_events_test.go`) and confirmed that `messageID` and `role` fields **do not exist** in SSE `message.part.updated` event `properties`. They only exist in HTTP response body `info` objects. The messageID lock-on and role filters were dead code — they never matched anything.
- **Real fix:** Replaced dead filters with `sentTextRef` — stores the user's sent message text on `handleSend`, then strips exact matches and prefixes from both `message.part.updated` snapshots and accumulated `message.part.delta` text.
- **Unified thinking style:** Made streaming and completed thinking use the same visual treatment — rounded border container, brain icon, `border-l-2` blockquote content. Streaming shows expanded with pulsing icon; completed wraps in `<details>`.
- Tests updated to replace dead-code tests with sent-text echo stripping tests. 369 tests passing.

### Round 3: Attempted thinking-as-separate-block (reverted)

- Moved thinking outside `MessageBubble` as a standalone `MessagePart` component.
- User clarified this was wrong — thinking should be a sub-block **inside** the response bubble, not outside.
- Reverted (code already matched `46dd2ac` state).

### Round 4: Diagnosing actual SSE event structure (commit `c30d6e9`, deployed)

- **Core problem identified:** Despite thinking being rendered as a separate typed `MessagePart` inside the bubble, user still sees thinking and text as an unformatted blob during streaming. This strongly suggests opencode is NOT sending thinking as a separate `part.type === "thinking"` event — it may be mixing thinking text into the regular `text` part, or sending all content as a single `text` part.
- **Assumption not yet validated:** We do not know what SSE events opencode actually emits during a thinking+response cycle. All test data uses `type: "text"` parts only. No test data shows `type: "thinking"` in SSE events.
- **Debug build deployed:** Added `console.log("[SSE DEBUG]", ...)` to `parseStreamEvent` at `ChatPage.tsx:82` to capture event type, part type, field, and text content for every `message.part.delta` and `message.part.updated` event. Deployed as `sha-c30d6e9`.
- **Awaiting:** User to open browser DevTools console during a thinking+response exchange and paste the `[SSE DEBUG]` output.

---

## Key Decisions

1. **Removed virtualizer** — `@tanstack/react-virtual` caused layout issues with the streaming bubble. Chat sessions rarely exceed hundreds of messages, so virtualization is unnecessary complexity. Simple flex column is correct for this use case.
2. **sentTextRef over messageID/role** — The SSE events from opencode don't carry `messageID` or `role` in `properties`. The only reliable signal is the user's own text, which we know at send time.
3. **Debug logging in production** — Added temporary `console.log` to diagnose the actual SSE event structure. This must be removed once we understand the data shape.

---

## Assumptions (Stated and Validated)

| # | Assumption | Validated? | How |
|---|-----------|-----------|-----|
| 1 | `messageID` exists in SSE `message.part.updated` properties | **FALSE** | Inspected all backend test data in `proxy_filter_test.go`, `session_tracker_test.go`, `stream_events_test.go` — `messageID` only in HTTP response body parts, never in SSE properties |
| 2 | `role` exists in SSE `message.part.updated` properties | **FALSE** | Same as above — `role` only in `info` object of HTTP responses |
| 3 | `message.part.delta` events exist | **UNVERIFIED** | No backend test data for delta events. Only `message.part.updated` and `message.updated` appear in tests |
| 4 | Opencode sends thinking as `part.type === "thinking"` | **UNVERIFIED** | All test data only shows `type: "text"`. Debug logging deployed to validate |
| 5 | Nested SSE format is possible from opencode | **TRUE** | Backend `processEvent` handles both flat and nested formats (`session_tracker.go:211-236`) |

---

## Blockers

- **Cannot fix thinking/text separation without knowing the actual SSE event structure.** Debug logging deployed (`sha-c30d6e9`). Need user to provide browser console output from `[SSE DEBUG]` during a thinking+response exchange.

---

## Tests Run

- `npx tsc --noEmit` — clean (all rounds)
- `npx vitest run` — 369 tests passing (all rounds)
- `npx vite build` — bundle produced successfully
- Cluster validation: all pods 1/1 Running, frontend serving 200 health checks, ingress responding 200

---

## Next Steps

1. **Get SSE debug output** — User opens `safespace.thekao.cloud` with DevTools, sends a message that triggers thinking, pastes `[SSE DEBUG]` lines from console
2. **Based on output, determine:** Does opencode send thinking as a separate `part.type`? Or is everything mixed into one `text` part? Does it use `message.part.delta` or only `message.part.updated`?
3. **If thinking is mixed into text:** Need client-side heuristic to split thinking from response text (e.g., detect `<think`/`</think` tags or a known delimiter in the text content)
4. **If thinking is a separate part type:** The current code should already work — investigate why it's not rendering separately during streaming
5. **Remove debug logging** once diagnosis is complete

---

## Files Modified

- `frontend/src/pages/ChatPage.tsx` — SSE parsing, state management, sentTextRef, debug logging
- `frontend/src/pages/ChatPage.sse.test.tsx` — ChatView mock, new tests for thinking/delta/nested/user-echo
- `frontend/src/api/messages.ts` — `transformHistory` preserves thinking/reasoning parts
- `frontend/src/components/chat/ChatView.tsx` — Streaming bubble rendering with thinking/text parts
- `frontend/src/components/chat/MessageBubble.tsx` — Added `isStreaming` prop
- `frontend/src/components/chat/MessageList.tsx` — Replaced virtualizer with flex column, added `streamingBubble` prop
- `frontend/src/components/chat/MessagePart.tsx` — Thinking rendering: expanded during streaming, collapsible when done
- `frontend/tests/e2e/streaming.spec.ts` — Fixed SSE event data format
