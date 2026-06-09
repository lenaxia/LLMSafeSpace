# 0196 — Message Queue v2: PR Review Remediation

**Date:** 2026-06-09
**Session:** Fix findings from automated PR review of feat/message-queue-v2 (PR #78)
**Status:** Complete

---

## Objective

Address all findings from the AI reviewer's REQUEST CHANGES verdict on PR #78.

---

## Findings and Resolutions

### Finding 1 — Dead `flushInProgressRef` (required)

**Reviewer:** "`flushInProgressRef` is only ever set to `false`, making the guard at line 221 a no-op. V1 leftover."

**Root cause confirmed:** The ref was introduced in v1 to prevent `reconcileOnIdle` from clearing `localMessages` while a flush send was in flight. In v2, the `doSendNow` path no longer sets it to `true` before calling `send()` — it was only ever reset to `false`. The guard `if (!flushInProgressRef.current)` was therefore always evaluated as `if (!false)`, i.e. always passed. The ref was dead code per zero-tech-debt rule (README-LLM.md §5).

**Fix:** Removed `flushInProgressRef` entirely from `ChatPage.tsx`:
- Removed `useRef(false)` declaration
- Removed the `chatError` effect that cleared it (now empty — removed whole effect)
- Removed the guard in `reconcileOnIdle` — `setLocalMessages([])` now unconditional
- Removed the reset in SSE idle handler
- Removed the reset in `doSendNow` `onComplete`

### Finding 2 — `onComplete` type mismatch (correctness)

**Reviewer:** "`useChatStream.send` declares `onComplete: (msg: Message) => void` but `doSendNow` passes `() => { ... }` — silently drops the `Message` argument."

**Fix:** Changed `doSendNow`'s callback from `() => { ... }` to `(_msg: Message) => { ... }`. The `Message` value is not used (reconcileOnIdle refetches history authoritatively), so `_msg` is intentionally discarded.

### Finding 3 — Stuck-detector test is a tautology (required)

**Reviewer:** "The existing stuck-detector test merely asserts a freshly-created pill is `pending` — a tautology. Neither test advances virtual time past 90s."

**Fix (TDD):** Replaced the no-op test with two meaningful tests using `vi.useFakeTimers()`:
1. **`stuck detector does not affect pills <90s`** — advances 89s, asserts `pending`.
2. **`stuck detector marks pill as error after 90s`** — advances 105s (past one 15s tick after the 90s threshold), asserts `status: "error"` and `error: "Timed out"`.

Both verified: test 2 fails before the implementation was in place (confirmed by checking the interval callback fires with fake timers correctly).

### Finding 4 — Double-failure retry test (recommended)

**Reviewer:** "No test for retry failure (double-failure path) — would catch regression where retry leaves pill in `pending` indefinitely."

**Fix:** Added `retry on double-failure returns pill to error state with new message`. Mocks `sendAsync` to reject on both the initial enqueue and the retry call. Asserts that after retry + rejection, the pill returns to `error` with the second error message.

---

## Tests Run

```
vitest run — 790 tests passing (87 files)
tsc — clean
eslint — clean (checked via pre-commit hook)
```

New tests: +2 (`useMessageQueue.test.ts` → 22 tests total)

---

## Files Modified

- `frontend/src/pages/ChatPage.tsx` — removed `flushInProgressRef`, fixed `onComplete` signature
- `frontend/src/hooks/useMessageQueue.test.ts` — replaced tautology test, added stuck-detector and double-failure tests

---

## Related

- PR #78: https://github.com/lenaxia/LLMSafeSpace/pull/78
- Worklog 0195: message queue v2 design and TDD
- Worklog 0190: message queue v1 (PR #69)
