# Worklog: Fix sseConnection Flaky Test + Dead Code Cleanup + Weak Point Coverage

**Date:** 2026-06-06
**Agent:** agent-audit-0606
**Session:** Pre-existing CI failure remediation
**Status:** Complete

---

## Objective

Fix the pre-existing flaky test `sseConnection.test.ts > applies exponential backoff with jitter on repeated failures` that was intermittently failing CI, then validate the fix is correct and complete, clean up dead code, and add coverage for two untested production code paths identified during re-validation.

---

## Root Cause Analysis

### Why the test was flaky

`jitteredDelay(base)` in `sseConnection.ts:58` returns `base * (0.5 + Math.random())`. Vitest's fake timer implementation (`vi.useFakeTimers()`) replaces `setTimeout`/`clearTimeout` but does **not** stub `Math.random()`. As a result:

- First retry delay: `1000 * [0.5, 1.5]` — anywhere in `[500, 1500]`ms  
- Second retry delay: `2000 * [0.5, 1.5]` — anywhere in `[1000, 3000]`ms

The test advanced by the **upper bounds** (`1500ms` then `3000ms`). Two failure modes:

1. **`expected 2 to be 3`**: `Math.random()` near 1.0 → first-retry timer fires at e.g. `1450ms`, which is inside the `1500ms` advance; second-retry fires at e.g. `3100ms`, which is past the `3000ms` advance. Attempt 3 never fires. `connectCount = 2`.

2. **`expected 4 to be 3`**: Two retries fire in the same advance window when random values are low — advance of `3000ms` covers both `jitteredDelay(2000)` AND a partial `jitteredDelay(4000)`, resulting in an extra connect. `connectCount = 4`.

### Why `retryDelay` doubles before the connect, not after

Critical detail: `scheduleReconnect()` doubles `retryDelay` **inside** the `setTimeout` callback (line 68), before calling `connect()`. So:

```
attempt 1 (initial)     : retryDelay = 1000
scheduleReconnect()     : timer fires after jitteredDelay(1000)
  → timer body: retryDelay = min(1000*2, max) = 2000; connect()
attempt 2               : retryDelay = 2000
scheduleReconnect()     : timer fires after jitteredDelay(2000)
  → timer body: retryDelay = min(2000*2, max) = 4000; connect()
attempt 3               : retryDelay = 4000
```

This means the original test's advance values (1500, 3000) were correct in intent but wrong in resilience — they assumed `random()` would stay below the upper bound.

---

## Fix

**PR #46** — merged as `dc38ad8`.

`vi.spyOn(Math, 'random').mockReturnValue(0)` pins jitter to `base * 0.5`:

- First retry delay: `1000 * 0.5 = 500ms` → advance exactly `500ms`
- Second retry delay: `2000 * 0.5 = 1000ms` → advance exactly `1000ms`

Cleanup: `vi.restoreAllMocks()` in the existing `afterEach` restores `Math.random` — no contamination.

---

## Re-validation: E2E Trace

Traced all production paths that the fix touches:

| Path | Correct? | Tested? |
|------|----------|---------|
| 503 response → `scheduleReconnect()` → timer → double `retryDelay` → `connect()` | ✓ | ✓ (backoff test) |
| `jitteredDelay` returns `base*(0.5+random)` | ✓ | ✓ (deterministic with spy) |
| `retryDelay` capped at `maxReconnectMs` in timer | ✓ | ✓ (new caps test) |
| `reader.cancel()` in timeout path (line 117) | ✓ | ✓ (timeout test) |
| `reader.cancel()` in `finally` (line 147) after already cancelled | ✓ (`.catch(()=>{})` suppresses) | ✓ (new double-cancel test) |
| `destroy()` → no reconnect after timeout | ✓ (`cancelled` flag checked in timer) | ✓ (destroy test) |
| `Math.random` spy cleaned up by `afterEach` | ✓ | implied by test isolation |

---

## Dead Code Removed

`connectTimes: number[]` — populated via `Date.now()` in each mock call but never asserted on. Was originally scaffolding for timing-gap assertions that were replaced by the count-based approach. Removed in this follow-up.

---

## Weak Points Identified

### 1. `double-cancel` in `finally` — NOW TESTED
Production code calls `await reader.cancel()` in the timeout path then `reader.cancel().catch(()=>{})` in `finally`. If `cancel()` on an already-cancelled reader throws (e.g. Chrome's `TypeError: Cannot cancel a stream that already has a reader`), the `catch` must suppress it silently. Previously untested. Test added: verifies `cancelSpy` is called twice, second rejection is swallowed, and `onDisconnect` still fires.

### 2. `maxReconnectMs` cap — NOW TESTED
`retryDelay = Math.min(retryDelay * 2, maxReconnectMs)` was executed but the cap behavior was untested — a regression could silently remove the `Math.min` and cause unbounded backoff. Test added: uses `maxReconnectMs: 2_000`, verifies retry 3 fires at the same interval as retry 2.

### 3. Remaining weak point: `AbortError` on fetch when `destroy()` races `connect()`
If `destroy()` is called while `fetch()` is pending, `abortCtrl.abort()` causes the fetch Promise to reject with `AbortError`. The catch at line 151 checks `(err as Error)?.name === "AbortError"` and returns silently. This relies on the browser/jsdom throwing an `AbortError` with exactly `name === "AbortError"`. The existing test `destroy() cancels everything and does not reconnect` covers the outcome (no reconnect) but does not verify the `AbortError` name check specifically. Low risk — browsers uniformly use this name, and the test confirms the observable outcome.

### 4. Remaining weak point: `fetch` rejects with network error (not AbortError)
If `fetch` rejects with a generic network error (`TypeError: Failed to fetch`), it falls through to the catch at line 149, logs via `wsLog`, then calls `scheduleReconnect()` at line 158. This path is untested. The behavior is correct (reconnect on network failure) but a regression that dropped the `scheduleReconnect()` call in the `catch` branch would go undetected.

---

## Tests Added

| Test | What It Proves |
|------|----------------|
| `reader.cancel() in finally block does not throw when reader already cancelled by timeout` | Double-cancel safety via `.catch(()=>{})` |
| `caps retryDelay at maxReconnectMs` | `Math.min` cap is enforced; backoff doesn't grow past max |

**Total tests after this session:** 12 in `sseConnection.test.ts` (was 10).  
**Full suite:** 80 files, 678 tests, all pass.

---

## Files Changed

- `frontend/src/lib/sseConnection.test.ts` — remove `connectTimes` dead array, add 2 new tests
- `COORDINATE.md` — claim/release cycle for agent-audit-0606
