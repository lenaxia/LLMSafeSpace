# Worklog 0194 — Chat bubble copy button, timestamps, and model name

**Date:** 2026-06-09
**PR:** #75
**Branch:** feat/chat-bubble-copy-timestamp-model

## Context

Three UX improvements to the chat screen message bubbles:
1. Copy button on each bubble to copy entire message text to clipboard
2. Timestamp display showing when the message was sent/received
3. Model name shown next to timestamp for assistant messages

## Investigation

### API response shape
The Go proxy calls `/session/:id/message` (V1 opencode endpoint). Response is `Array<{ info: { id, role, time: { created, completed? }, modelID?, providerID? }, parts }>`. Timestamps are at `info.time.created` (epoch millis, always present). `modelID` is present on assistant messages only.

The frontend's `OpenCodeMessage` type and `transformHistory` function previously dropped both `time` and `modelID` — these were extracted from the response but never passed through.

### Model name resolution
The workspace models list (`workspacesApi.listModels`) is already fetched in `ChatPage`. Resolved via `Map<modelID, modelName>` built in `MessageList` with `useMemo`.

## Design decisions

- **Copy feedback**: Icon-only (Copy → Check swap on clipboard success, 2s revert). No toast.
- **Model name**: Threaded via props (ChatPage → ChatView → MessageList → MemoizedBubble). `modelName` is a string prop preserving React.memo effectiveness.
- **Timestamps**: `formatTimestamp` helper — relative for recent ("just now", "3m ago"), clock time for same-day, date+time for older.
- **Keyboard accessibility**: Copy button has `focus:opacity-100` in addition to `group-hover:opacity-100`.
- **Timer safety**: `clearTimeout` before setting new timeout prevents premature revert on rapid double-click.
- **Footer spacing**: Conditional margin — only added when timestamp or model name is present.

## Files changed

| File | Change |
|---|---|
| `frontend/src/api/types.ts` | Added `modelID?: string` to `Message` |
| `frontend/src/api/messages.ts` | Extended `OpenCodeMessage.info` with `time`/`modelID`/`providerID`. Exported `transformHistory`. Extract `createdAt`/`modelID`. |
| `frontend/src/components/chat/MessageBubble.tsx` | Copy button + timestamp + model name in footer. Exported `extractMessageText`. |
| `frontend/src/components/chat/MessageList.tsx` | Added `models` prop, `modelMap` via `useMemo`, passes `modelName` to `MemoizedBubble`. |
| `frontend/src/components/chat/ChatView.tsx` | Added `models` prop, passes to `MessageList`. |
| `frontend/src/pages/ChatPage.tsx` | Passes `modelsData?.models` to `ChatView`. Added `createdAt` to local and queued user messages. |

## Tests

44 new tests across 4 test files:
- `messages.test.ts`: 7 tests for `transformHistory` (createdAt, modelID extraction)
- `MessageBubble.test.tsx`: 25 tests (copy button success/failure/timeout, `extractMessageText` unit tests, timestamp, model name)
- `MessageList.test.tsx`: 3 tests (model name resolution from models prop)
- `ChatView.test.tsx`: 1 test (models prop threading)

All 759 tests pass, TypeScript clean.

## Adversarial review findings

| # | Finding | Severity | Resolution |
|---|---|---|---|
| 1 | Timer leak on rapid double-click | Medium | Fixed: `clearTimeout` before new timeout |
| 2 | Copy button invisible on keyboard focus | Medium | Fixed: `focus:opacity-100` |
| 3 | Footer spacing on all bubbles | Low | Fixed: conditional margin |

## PR Review feedback

AI reviewer approved with no inline comments. Noted `messageID` on `SendMessageRequest` — identified as pre-existing change from another agent, not part of this PR. Left untouched.
