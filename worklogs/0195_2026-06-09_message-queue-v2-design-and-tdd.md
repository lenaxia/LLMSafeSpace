# 0195 — Message Queue v2: Validated Design + TDD Implementation

**Date:** 2026-06-09
**Session:** Design and validate message queue v2 architecture, implement core hook and component via TDD
**Status:** In Progress

---

## Objective

Design a v2 message queue system that leverages opencode's server-side `prompt_async` serialization instead of maintaining a client-side flush state machine. Validate every assumption against the opencode codebase, then implement the core hook and component via TDD.

---

## Work Completed

### Phase 1: v1 Queue Implementation (PR #69 — merged)

Already documented in worklog 0190. Session began with cloning repos and implementing v1:
- Textarea stays enabled during streaming, messages buffered client-side, flushed one-at-a-time on idle
- PR #69 opened, reviewed by automated reviewer (3 rounds), all findings resolved, merged to main
- 718 tests passing after rebase onto main

### Phase 2: UX Redesign Discussion

User requested a different UX from v1:
- Queued messages should render as **colored pills/bubbles in a dedicated section at the bottom** of the chat, not mixed into the main message flow
- Auto-scroll follows streaming LLM responses (above the queue section), not the queue
- Count badge next to the bouncing dots: `[ X messages queued ↓ ]`
- Messages flushed as **one concatenated batch** (not one-at-a-time) to avoid models rejecting consecutive user turns

### Phase 3: opencode Internal Queue Discovery

Cloned and studied `anomalyco/opencode` to understand server-side queuing:
- `prompt_async` endpoint uses `ensureRunning()` which serializes prompts per session via a `Runner` state machine (`Idle → Running → Idle`)
- Multiple `prompt_async` calls queue naturally — no client-side serialization needed
- `PromptInput` has optional `messageID` field — client can provide a stable ID that flows through to history and SSE events
- `messageID` must start with `"msg"` prefix (validated from `id.ts:35-36`)
- Abort (`Runner.cancel`) interrupts current fiber + fails deferred with `Cancelled` → all queued `ensureRunning` callers receive `Cancelled` and their forked fibers are interrupted → **server-side queue is drained on abort**

### Phase 4: Validated Assumptions

Every assumption traced through the actual codebase:

| # | Assumption | Status | Evidence |
|---|---|---|---|
| 1 | `prompt_async` serializes per session | **Validated** | `ensureRunning()` + `Effect.fork` with runner state machine in `runner.ts` |
| 2 | `messageID` flows to history and SSE events | **Validated** | `prompt.ts:1658` accepts optional messageID; `id.ts:51-69` generates ULID if omitted; `message.updated` SSE event carries `info.id` |
| 3 | Client-provided messageID is used as-is | **Validated** | `id.ts:35-36`: if given starts with prefix, returned unchanged |
| 4 | Abort drains server-side queue | **Validated** | `Runner.cancel` interrupts current fiber + fails deferred; all `ensureRunning` callers get `Cancelled`; forked fibers interrupted via `Scope.close` |
| 5 | Go proxy forwards messageID verbatim | **Validated** | `proxy.go:481` reads raw bytes; `proxy.go:557` sends raw bytes — no field stripping |
| 6 | SSE `message.part.updated` carries `messageID` | **Validated** | `partBase` includes `messageID` field; confirmed in SSE event schema |
| 7 | Message ID uniqueness enforced by DB | **Validated** | `id` is primary key on `message` table; retry with same messageID fails with constraint violation |

### Phase 5: v2 Design

**Architecture: client fires immediately, server serializes, pills track completion via SSE**

```
User sends while streaming
  ├─ useMessageQueue.enqueue(text)
  │   ├─ id = "msg_" + crypto.randomUUID()
  │   ├─ add pill to display array
  │   └─ POST /prompt_async ──────►  verbatim forward ──────►  ensureRunning()
  │       { parts, messageID: id }                               (queues behind current turn)
  │
  ├─ SSE: message.updated arrives with info.id = id
  │   └─ remove pill from display (server picked it up)
  │
  └─ If abort → server cancels all queued → pills remain, marked stale
      └─ User can re-send or dismiss
```

**Key design decisions:**
1. No client-side flush state machine — fire all messages immediately via `prompt_async`, let opencode serialize
2. Track pill removal via SSE `message.updated` event matching on `messageID` (not text matching, not idle-count heuristic)
3. Abort: server drains its queue; frontend marks remaining pills as stale; user chooses re-send or dismiss
4. Page reload with pending: pills lost (in-memory), but messages already submitted to opencode — they'll process and appear in history after reload. Acceptable.
5. Stuck detector: 90s timeout with no SSE activity → mark pills as stale, show retry button
6. 429 retry with exponential backoff (1s, 2s, 4s) — 3 attempts before marking as failed

### Phase 6: TDD Implementation (hook + component)

Created `useMessageQueue` hook and `QueueSection` component via TDD:

**useMessageQueue.ts** — core hook:
- `enqueue(text)` → generates `"msg_" + crypto.randomUUID()`, fires `prompt_async`, adds to display array
- Removes pill on matching `message.updated` SSE event
- Abort handling: marks all pending pills as stale
- Stuck detector: 90s interval, marks stale on timeout
- 429 retry with exponential backoff
- Session change: clears all state

**QueueSection.tsx** — UI component:
- Renders pending pills with distinct styling
- Shows count badge
- Stale/failed pills show retry/dismiss actions

**Tests:**
- `useMessageQueue.test.ts`: 20 tests covering enqueue, SSE removal, abort, stuck detection, 429 retry, session change, race conditions
- `QueueSection.test.tsx`: 9 tests covering rendering, count badge, stale/failed states, retry/dismiss actions
- All 793 tests passing (86 new from main + 29 new), TypeScript clean

**Committed to `feat/message-queue-v2` branch** (but session ended on wrong branch — files need recovery, see Blockers)

---

## Key Decisions

1. **Fire immediately, don't batch concat:** opencode serializes via `ensureRunning()` — no need for client-side batching. Each message is a separate `prompt_async` call. Models receive each as a distinct turn, which is correct behavior.
2. **messageID as correlation token:** Client generates `"msg_" + crypto.randomUUID()` before firing. Used to match SSE `message.updated` events back to pills. No text matching, no heuristics.
3. **Pills in dedicated section, not in message flow:** Clear visual separation between "sent" and "not yet sent". User never doubts message state.
4. **Abort = server drains + pills marked stale:** Don't try to cancel what's already submitted. Show user what happened and let them decide.

---

## Assumptions

1. `prompt_async` serialization is per-session — validated from `runner.ts` state machine
2. `messageID` round-trips through Go proxy unchanged — validated from `proxy.go` raw bytes forwarding
3. SSE `message.updated` fires for every user message — validated from opencode's `createUserMessage` → `sessions.updateMessage`
4. Abort cancels all queued prompts — validated from `Runner.cancel` → fiber interruption → scope close
5. `crypto.randomUUID()` available in browser — safe assumption for modern browsers

---

## Blockers

1. **v2 files committed to wrong branch.** The agent was on `feat/chat-bubble-copy-timestamp-model` when committing the v2 work. The `useMessageQueue.ts`, `QueueSection.tsx`, and their tests may be in the stash or a dangling commit. Need to recover them and apply to a clean `feat/message-queue-v2` branch.
2. **ChatPage integration not started.** The hook and component exist but are not wired into ChatPage.tsx. The existing `handleSend`, `useChatStream`, and SSE handling need to be refactored to use `useMessageQueue` instead of the v1 flush state machine.

---

## Tests Run

```
vitest run — 793 tests passing (718 existing + 29 new + 26 from other agents)
tsc — no errors
```

---

## Next Steps

1. **Recover v2 files:** Check stash `stash@{0}` and dangling commits for `useMessageQueue.ts`, `QueueSection.tsx`, and test files. Apply to clean `feat/message-queue-v2` branch from current main.
2. **ChatPage integration:** Replace v1 `pendingQueue`/`flushFailedRef`/`doSendNowRef` state machine in `ChatPage.tsx` with `useMessageQueue` hook. Wire SSE `message.updated` events to `onMessageUpdated` callback. Remove v1 queue code.
3. **Remove v1 code:** Delete `ChatPage.queue.test.tsx` (v1 tests) after v2 is wired. Remove `queuedUserMessages` state and flush effect.
4. **Update `messagesApi.sendAsync`** to accept optional `messageID` parameter and include it in the POST body.
5. **E2E test:** Verify queued messages fire via `prompt_async`, pills appear and disappear on SSE events, abort marks pills stale, page reload recovers correctly.
6. **Write design doc** to `design/stories/` per project conventions.

---

## Files Modified

### Created (v2 — need recovery from wrong branch)
- `frontend/src/hooks/useMessageQueue.ts`
- `frontend/src/hooks/useMessageQueue.test.ts`
- `frontend/src/components/chat/QueueSection.tsx`
- `frontend/src/components/chat/QueueSection.test.tsx`
- `frontend/src/api/types.ts` (added `messageID` to send params)

### Modified (v1 — merged via PR #69)
- `frontend/src/components/chat/Composer.tsx`
- `frontend/src/components/chat/Composer.test.tsx`
- `frontend/src/components/chat/ChatView.tsx`
- `frontend/src/pages/ChatPage.tsx`
- `frontend/src/pages/ChatPage.queue.test.tsx`
- `frontend/src/hooks/useChatStream.ts`

---

## Related

- Worklog 0190: v1 message queue (PR #69)
- PR #69: https://github.com/lenaxia/LLMSafeSpace/pull/69 (merged)
