# Worklog: Streaming UX — State Machine Routing, Markdown, Auto-scroll

**Date:** 2026-05-27
**Session:** Fix SSE delta routing, add GFM markdown, add auto-scroll tailing
**Status:** Complete

---

## Objective

Fix three streaming UX issues and add two features:
1. SSE deltas routed to wrong buffer (thinking vs response)
2. User echo appearing in response
3. GFM markdown rendering (tables, code blocks)
4. Auto-scroll tailing with breakaway detection

---

## Work Completed

### State-machine SSE delta routing (commit `664cd4d`)
- Replaced field-based delta routing with `activePartTypeRef` state machine
- Opencode sends ALL deltas with `field:"text"` — the only signal is the preceding `message.part.updated` partType
- State transitions: `reasoning` → thinking buffer, `text` → response buffer, `user-echo`/`null` → discard
- 6 new tests, 5 updated tests, 375 total passing

### Suppress /auth/me 401 redirect (commit `fdf63cc`)
- `/auth/me` 401 is normal "not logged in" — skip redirect to /login for that path
- Added README-LLM Rule 5 amendment: no pre-existing errors acceptable

### GFM markdown + auto-scroll tailing (commit `1cf5685`)
- Added `remark-gfm` for tables, strikethrough, fenced code blocks
- Rewrote `MessageList` with MutationObserver-based tailing, scroll breakaway detection, "Resume tailing"/"Jump to bottom" button
- 9 new tests, 384 total passing

---

## Key Decisions

1. **State machine over field-based routing** — opencode's SSE protocol sends `field:"text"` for everything; only `part.updated` events signal the content type
2. **MutationObserver for scroll tailing** — React state updates don't fire fast enough for per-delta scrolling; DOM-level observation catches all mutations
3. **60px threshold for "at bottom"** — prevents jitter from sub-pixel scroll differences

---

## Blockers

None for completed work. Two new issues identified (see Next Steps).

---

## Tests Run

- `npx vitest run` — 384 tests passing
- `npx vite build` — production bundle successful
- `npx tsc --noEmit` — no errors in changed files
- Live cluster validation: SSE routing confirmed correct via console logging

---

## Next Steps

Two issues remain from user feedback:

1. **Tool calls not rendered during streaming** — `part.updated type: tool` events arrive but are discarded (route → null). Need to capture tool call data and render it as a part in the streaming bubble.

2. **Multiple thinking blocks overwrite each other** — Current design uses a single `sseThinkingText` string. When a second `reasoning` block arrives, it overwrites the first. Need to change the streaming model from `{thinkingText, responseText}` to an ordered array of parts: `[thinking1, text1, tool1, thinking2, text2, ...]` that accumulates as the stream progresses.

---

## Files Modified

- `frontend/src/pages/ChatPage.tsx` — state machine parseStreamEvent, activePartTypeRef, debug logging
- `frontend/src/pages/ChatPage.sse.test.tsx` — 6 new state-machine tests, 5 updated
- `frontend/src/api/client.ts` — skip handleUnauthorized for /auth/me
- `frontend/src/components/chat/MessagePart.tsx` — added remark-gfm
- `frontend/src/components/chat/MessagePart.test.tsx` — 4 new GFM tests
- `frontend/src/components/chat/MessageList.tsx` — rewritten with auto-scroll tailing
- `frontend/src/components/chat/MessageList.test.tsx` — 5 new scroll tests
- `README-LLM.md` — git push syntax, zero pre-existing errors rule
- `package.json` / `package-lock.json` — remark-gfm dependency
