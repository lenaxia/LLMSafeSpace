# 0194 — Chat bubble copy button, timestamps, and model name

**Date:** 2026-06-09
**Session:** Add copy button, message timestamps, and model name to chat bubble UI
**Status:** Complete

---

## Objective

Three UX improvements to the chat screen message bubbles:

1. **Copy button** — each bubble has a copy icon that copies the entire message text to clipboard, with a Copy → Check icon swap on success (2s revert)
2. **Timestamps** — each bubble shows when the message was created (relative for recent, clock time for same-day, date+time for older)
3. **Model name** — assistant messages show the model that produced them, next to the timestamp

---

## Work Completed

### Investigation

The opencode V1 API (`GET /session/:id/message`) returns `Array<{ info: { id, role, time: { created }, modelID?, providerID? }, parts }>`. Timestamps at `info.time.created` (epoch millis) are always present on both user and assistant messages. `modelID` is present on assistant messages only. Both fields were being dropped by `transformHistory` — the `OpenCodeMessage.info` type had neither.

The workspace models list is already fetched in `ChatPage` via `useQuery(["models", workspaceId])`. Model name resolution uses a `Map<id, name>` built with `useMemo` in `MessageList`.

### Data layer (`frontend/src/api/`)

- `types.ts`: Added `modelID?: string` to `Message`
- `messages.ts`: Extended `OpenCodeMessage.info` with `time`, `modelID`, `providerID`. Exported `transformHistory`. Extract `createdAt` (as ISO string) and `modelID` in the map step.

### UI (`frontend/src/components/chat/`)

- `MessageBubble.tsx`: Rewritten to add footer row with timestamp, model name, and copy button. Exported `extractMessageText` (pure function for testability). Copy button uses `group-hover:opacity-100 focus:opacity-100` for hover and keyboard visibility. `clearTimeout` before setting new timeout prevents premature icon revert on rapid double-click.
- `MessageList.tsx`: Added `models?: ModelInfo[]` prop. Builds `modelMap` via `useMemo`. Resolves `modelName` string per message and passes to `MemoizedBubble`. String prop preserves React.memo effectiveness.
- `ChatView.tsx`: Added `models?: ModelInfo[]` prop passthrough to `MessageList`.

### ChatPage (`frontend/src/pages/`)

- Passes `modelsData?.models` to `ChatView`
- Added `createdAt: new Date().toISOString()` to local and queued user messages at creation time

### Adversarial review

Three real findings identified and fixed before commit:

| Finding | Fix |
|---|---|
| Timer leak on rapid double-click | `clearTimeout(timerRef.current)` before setting new timeout |
| Copy button invisible on keyboard focus | Added `focus:opacity-100` |
| Footer spacing added to all bubbles | Conditional margin — only when timestamp or model name present |

### Tests

44 new tests, TDD (tests written first, verified failing, then implementation):

| File | Tests | Coverage |
|---|---|---|
| `messages.test.ts` | 7 | `transformHistory` createdAt + modelID extraction |
| `MessageBubble.test.tsx` | 25 | Copy success/failure/timeout, `extractMessageText` (6), timestamp (3), model name (3) |
| `MessageList.test.tsx` | 3 | Model name resolution from models prop |
| `ChatView.test.tsx` | 1 | Models prop threading end-to-end |

All 759 tests pass, TypeScript clean, coverage +0.1%.

### CI / PR review

- PR #75, all checks green: CI (15 jobs), PR Review (AI reviewer, 0 inline comments), Security scan, Secrets Integration
- AI reviewer flagged `messageID` on `SendMessageRequest` as unrelated dead code — identified as pre-existing change from another agent, not touched
- Merged via squash into main as `fcb1df6c6c89`

---

## Blockers

None.

---

## Next Steps

None — feature is complete. Potential future improvements (not required):
- Auto-refresh relative timestamps (e.g. every 60s) so "3m ago" stays accurate without re-render
- Per-message token count or cost display alongside model name (data is available in `info.tokens` / `info.cost` from the API)
