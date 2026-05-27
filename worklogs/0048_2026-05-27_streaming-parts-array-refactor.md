# Worklog: Streaming Parts Array Refactor + Delta Type Safety

**Date:** 2026-05-27
**Session:** Refactor streaming model from scalar strings to ordered parts array; fix delta type-mismatch bug
**Status:** Complete

---

## Objective

Fix two streaming UX bugs:
1. Multiple thinking blocks overwrite each other (only last visible)
2. Tool calls not rendered during streaming

---

## Work Completed

### Streaming parts array refactor (commit `d988065`)

Replaced `sseThinkingText: string` + `sseStreamText: string` with `sseStreamParts: Array<{type, text}>`.

**State machine behavior:**
- `part.updated(reasoning, "")` → push new `{thinking, ""}` entry
- `part.updated(text, "")` → push new `{text, ""}` entry
- `part.updated(tool)` → push `{tool, ""}` (deduplicated: consecutive tools = one entry)
- `part.updated(reasoning/text, "snapshot")` → update last matching part's text
- Deltas → append to last part in array

**Props change:** `ChatView` now receives `streamParts[]` instead of two strings. Maps directly to `MessagePart[]` for the streaming bubble.

### Delta type-safety fix (commit `d32b5ba`)

Found during manual trace of real event sequence: after a reasoning snapshot sets `activePartTypeRef = "reasoning"`, if the last array entry is a `{tool,""}`, deltas would incorrectly append to the tool part.

**Fix:** Added type check — deltas only append if `last.type === expectedType` (reasoning→thinking, text→text). Otherwise discarded.

---

## Assumptions (Stated and Validated)

| # | Assumption | Validated? | How |
|---|-----------|-----------|-----|
| 1 | Each `part.updated(reasoning, "")` signals a NEW thinking block | **TRUE** | Console log shows empty-text reasoning at start of each new block |
| 2 | `part.updated(tool)` fires multiple times per tool batch | **TRUE** | Console shows ~14 consecutive tool events |
| 3 | Consecutive tool events should produce ONE tool entry | **TRUE** | Design decision validated by dedup logic + test |
| 4 | Deltas always follow the most recent reasoning/text part.updated | **TRUE** | Console log confirms no deltas after tool events |
| 5 | Reasoning snapshot with text updates existing thinking part (not push new) | **TRUE** | Console shows snapshot text matching the streamed content |
| 6 | `findLastIndex` correctly targets the right thinking/text part | **TRUE** | Manual trace through 3-step event sequence confirmed |
| 7 | Deltas should NOT append to parts of wrong type | **TRUE** | Bug found and fixed during validation |

---

## Key Decisions

1. **Ordered array over separate buffers** — preserves interleaved structure (thinking→tool→thinking→text)
2. **Tool deduplication** — consecutive tool events produce one entry (avoids visual noise)
3. **Snapshot updates vs push** — non-empty text in part.updated updates the last matching part (not creates new), because opencode sends final snapshots at end of each step
4. **Type-checked delta append** — prevents corruption when activePartType doesn't match last array entry type

---

## Blockers

None.

---

## Tests Run

- `npx vitest run` — 387 tests passing (39 SSE, 11 ChatView, rest unchanged)
- `npx vite build` — production bundle successful
- Manual trace of real console log event sequence against implementation — all routing correct

---

## Next Steps

UI polish items identified by user:
1. Increase session title font size in nav panel
2. Selected session highlight should cover full row including "..." menu
3. Remove "Sessions" subheading, move "+" to workspace name row
4. Session titles from opencode not showing (regression)
5. Minimal scrollbars, no horizontal scroll, truncate long titles, resizable nav panel
6. Workspace names: shorten GUID, prefix with "workspace-"
7. New workspace creation: no confirmation dialog
8. Workspace loading: larger centered indicator instead of small toast

---

## Files Modified

- `frontend/src/pages/ChatPage.tsx` — sseStreamParts state, parseStreamEvent rewrite, delta type-check
- `frontend/src/pages/ChatPage.sse.test.tsx` — 39 tests (complete rewrite for parts array model)
- `frontend/src/components/chat/ChatView.tsx` — accepts streamParts prop
- `frontend/src/components/chat/ChatView.test.tsx` — updated for new props
