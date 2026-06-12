# 0232 — Message Queue v3: Bug Fixes, Per-Session Isolation, Drawer UI

**Date:** 2026-06-11
**Session:** Resume PR #93 (serialize-on-idle v2), fix 4 reported bugs, address automated review findings, redesign queue UI
**Status:** Complete

---

## Objective

Pick up PR #93 (`fix/message-queue-serialize-on-idle-v2`) which implements TUI-matching serialized message queue behavior. The user reported 4 bugs and requested a UI redesign. Additionally, the automated PR reviewer flagged 2 blocking findings.

A follow-up session performed a thorough adversarial review which found a critical deadlock bug and a structural issue with side effects in React state updaters. The hook was refactored to `useReducer` to address both correctly.

---

## Assumptions Stated & Validated

1. **opencode TUI serializes on idle** — Validated by reading `packages/tui/src/component/prompt/index.tsx`: `sdk.client.session.prompt()` is called once per submit; no client-side queue exists in the TUI. The TUI simply calls prompt and the server serializes. Our frontend serializes client-side because we proxy through the API server.
2. **`drainingRef` prevents duplicate sends** — Validated via new test `double notifyIdle in same tick does not cause duplicate send`. The ref guards against React batched updates calling `drainOne` twice with the same state snapshot.
3. **`sessionId` effect cleanup is sufficient for per-session isolation** — Validated via test `changing sessionId clears messages from previous session`. The `useEffect` on `sessionId` runs a `setQueuedMessages(filter)` which permanently removes messages from other sessions.
4. **60s timeout on `sending` items is sufficient** — The previous 15s/90s timeout was removed in PR #93. A hung `sendAsync` (TCP accepted but no response) would leave a pill in `sending` forever. 60s is conservative; the API has a 5s client timeout, so a truly hung connection would be caught by the fetch API itself. The timeout is a safety net for pathological cases.
5. **Drawer open state defaults** — Desktop (`!isMobile`) defaults to open, mobile defaults to closed. Validated by reading `useMediaQuery.ts`: `isMobile = !matchMedia("(min-width: 768px)")`, matching the project's existing responsive breakpoint.

---

## Work Completed

### PR #93 Review Findings (2 blocking — both fixed)

1. **`notifyIdle` updater-with-side-effects** (`useMessageQueue.ts:101-106`): The updater function called `drainOne(prev)` which fires `sendAsync`. Under React batched updates, two `notifyIdle()` calls could both receive the same `prev` and both fire `sendAsync` for the same message. **Fix:** Added `drainingRef = useRef(false)` guard in `drainOne`. Set to `true` before firing `sendAsync`, reset to `false` in `.then()` and `.catch()`. Second `notifyIdle` hits the guard and returns early. Tested with `double notifyIdle in same tick does not cause duplicate send`.

2. **Missing worklog**: This worklog entry.

Additional review findings addressed:
- **Inconsistent `useCallback`**: All exposed functions (`remove`, `clear`, `reconcile`, `retry`, `dismiss`, `onPhaseChange`) now wrapped in `useCallback`.
- **`retry` closure capture**: `retry` previously read `queuedMessages` from render closure. Rewritten to use functional state updates exclusively — no closure capture of state.
- **No timeout for stuck `sending` items**: Re-added a targeted 60s timeout via 10s interval that only checks `sending` items (not `pending`). Uses `_sentAt` timestamp stored when transitioning to `sending`.

### Bug 1: Queued messages appear as LLM response pills

**Root cause:** The pill-style rendering (`rounded-full`, amber/red bg) looked like system/LLM status indicators, not user messages. No visual association with the user's input.

**Fix:** Redesigned `QueueSection` to render queued messages as right-aligned chat bubbles matching `MessageBubble` styling (`bg-primary text-primary-foreground`, `rounded-lg`, `max-w-[90%]`). Pending messages show as solid user bubbles; error messages show with line-through text, error detail, and Retry/Dismiss buttons.

### Bug 2: Pills stay after message appears, eventually timeout to red

**Root cause (two parts):**
1. `reconcile` was labeled "kept for backward compatibility" and was not called consistently. The comment was misleading — reconcile is defense-in-depth cleanup. Now called in `reconcileOnIdle` in ChatPage after history refetch.
2. No timeout on `sending` items after the old 15s interval was removed in PR #93.

**Fix:**
- Re-added 60s timeout via interval that marks stuck `sending` items as `error`.
- `reconcile` is still called by ChatPage's `reconcileOnIdle` (unchanged — was already working, the pills staying was caused by the `sending` timeout being removed, not reconcile).

### Bug 3: Queue pills stay on screen when switching sessions/workspaces

**Root cause:** `queuedMessages` was a flat array with no session association. Switching sessions showed all queued messages from all sessions.

**Fix:**
- Added `sessionId` field to `QueuedMessage` type.
- `enqueue` stamps each message with the current `sessionId`.
- `useEffect` on `sessionId` clears messages from other sessions via `setQueuedMessages(prev => prev.filter(m => m.sessionId === sessionId))`.
- The returned `queuedMessages` is additionally filtered to only the current session (`sessionQueue` computed in the return statement).
- Tested with `changing sessionId clears messages from previous session`.

### Bug 4: Drawer UI — collapsible, mobile/desktop, auto-close

**Fix:** Complete `QueueSection` rewrite:
- **Toggle button**: Shows "N messages queued" (or "No queued messages" when empty). Clickable to open/close. Animated arrow (`▸` with `rotate-90`).
- **Drawer**: `max-h-96` when open, `max-h-0` when closed. CSS transition for smooth animation.
- **Desktop** (`isMobile=false`): defaults to open.
- **Mobile** (`isMobile=true`): defaults to closed.
- **Auto-close**: `useEffect` on `count` calls `setOpen(false)` when count drops to 0.
- **Empty state**: When drawer is opened with no messages, shows "No queued messages".
- Removed `queuedCount` from `Composer` (was the old "N messages queued" text above the input). Moved count to QueueSection toggle button.
- `ChatView` passes `isMobile` (from `useIsMobile` hook) to `QueueSection`.

### Follow-up: useReducer refactor + deadlock fix

**Adversarial review findings (commit `6a4fd5d9`):**

1. **Critical: `drainingRef` not reset on timeout → permanent queue deadlock.** When the 10s interval marked a stuck `sending` message as `error`, `drainingRef.current` remained `true`. Any subsequent `notifyIdle` call hit the guard and returned early — the queue was permanently stuck. **Fix:** The interval now resets `drainingRef.current = false` when it detects a timeout. Regression test added: `after sending timeout, subsequent notifyIdle can send again (no deadlock)`.

2. **Structural: Side effects inside React state updater.** `notifyIdle` wrapped `drainOne` in `setQueuedMessages(prev => { drainOne(prev); return prev; })`. `drainOne` fires async API calls, calls `dispatch`, and mutates `drainingRef` — all inside an updater function. React docs specify updaters should be pure. While the `drainingRef` guard prevented double execution in practice, the pattern was fragile. **Fix:** Refactored from `useState` to `useReducer`. All state transitions are now pure functions in the reducer. `notifyIdle` reads current state from `stateRef.current` (synced inline during render) and calls `drainOne` directly — no updater wrapping needed.

3. **Stale promise resolution after timeout.** After timeout marked a message as `error`, the original hung promise's `.then()` handler still ran on resolution, dispatching `remove` and silently deleting the error pill. **Fix:** Added `send_success` action that only removes the message if it's still in `sending` status. If the message has already been transitioned to `error` (by timeout or phase change), the stale resolution is a no-op.

4. **Dead code: `remove` and `dismiss` were identical.** Removed `remove` from the return value. All consumers use `dismiss`.

**Reducer design:** 11 action types covering all state transitions. Each action is a pure transformation — no side effects, no API calls, no ref mutations. Side effects (API calls, `drainingRef` updates) live exclusively in `drainOne` and `onPhaseChange` callbacks.

---

## Tests Run

```
npx vitest run   # 94 test files, 916 tests passing
npx tsc --noEmit  # 0 errors
```

New test cases added (follow-up):
- `useMessageQueue.test.ts`: 1 new test (deadlock regression after timeout). Removed 1 dead test (remove function). Total: 29 tests.

---

## Files Modified

| File | Change |
|------|--------|
| `frontend/src/hooks/useMessageQueue.ts` | Refactored to useReducer (11 action types), fix drainingRef deadlock, add send_success guard, remove dead `remove` export |
| `frontend/src/hooks/useMessageQueue.test.ts` | Add deadlock regression test, remove dead `remove` test, fix act warning in onPhaseChange test |
| `frontend/src/components/chat/QueueSection.tsx` | Complete rewrite: drawer UI, chat bubbles, toggle button, mobile/desktop, auto-close |
| `frontend/src/components/chat/QueueSection.test.tsx` | Rewritten for new component API (10 tests) |
| `frontend/src/components/chat/ChatView.tsx` | Pass isMobile to QueueSection, remove queuedCount prop |
| `frontend/src/components/chat/Composer.tsx` | Remove queuedCount prop |
| `frontend/src/components/chat/Composer.test.tsx` | Remove queued-count tests |
| `frontend/src/pages/ChatPage.tsx` | Remove queuedCount pass-through to ChatView |

---

## Next Steps

- Push this branch and update PR #93
- Verify on live cluster that queue drawer renders correctly on mobile and desktop viewports
