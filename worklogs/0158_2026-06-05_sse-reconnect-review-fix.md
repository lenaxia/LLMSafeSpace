# Worklog: SSE Reconnect — Review Fix + Hardening

**Date:** 2026-06-05
**Session:** Address PR #25 review findings + architecture hardening
**Status:** Complete

---

## Objective

1. Fix incomplete Bug 2 (missing AbortController abort in useUserEventStream)
2. Eliminate DRY violation that caused Bug 2
3. Add reader.cancel() for immediate stream lock release on timeout
4. Add jitter to reconnect backoff (prevent thundering herd)
5. Add backoff to leader re-election in events.ts (prevent tight loops)
6. Add test coverage for all new behaviors

---

## Changes

### New: `src/lib/sseConnection.ts` — shared SSE fetch utility

Extracted the common fetch→read→timeout→reconnect logic into a single reusable
module. Both hooks now delegate to this. Features:
- Read timeout via Promise.race (configurable, default 35s > backend 25s heartbeat)
- reader.cancel() on timeout for immediate stream lock release
- AbortController abort+replace on reconnect
- Exponential backoff with jitter: delay × [0.5, 1.5]
- onConnect/onDisconnect/onEvent callbacks
- destroy() for clean teardown

### Refactored: `useEventStream.ts` and `useUserEventStream.ts`

Both hooks now delegate to `createSSEConnection`. Hook-specific logic (query
invalidation, Last-Event-ID, onReconnect callback) stays in the hook. Shared
reconnect/timeout/abort logic is in the utility.

### Hardened: `events.ts` — leader re-election backoff

Added `resignationCount` that increments on each resignation and adds
`min(resignationCount × 2s, 30s)` backoff before re-election. Resets on
successful message receipt. Prevents tight resign→elect→error→resign loops
on single-tab with persistent network failure.

---

## Gotchas & Known Limitations

1. **Last-Event-ID on reconnect**: `useUserEventStream` passes headers at
   construction time. If the connection drops and reconnects internally, the
   `Last-Event-ID` header used for the reconnect fetch will be whatever was
   current when `createSSEConnection` was called — NOT the latest event ID.
   This is acceptable because the reconnect invalidates all caches anyway (FM9).

2. **Jitter non-determinism in tests**: Tests use `advanceTimersByTimeAsync`
   with the maximum possible jittered delay (base × 1.5) to avoid flakiness.
   This means tests don't precisely verify minimum delay.

3. **reader.cancel() is async**: The `await reader.cancel()` call adds one
   microtask of latency before the `break` takes effect. In practice this is
   negligible.

---

## Files Changed

| File | Change |
|------|--------|
| `frontend/src/lib/sseConnection.ts` | NEW — shared SSE connection utility |
| `frontend/src/lib/sseConnection.test.ts` | NEW — 10 tests |
| `frontend/src/hooks/useEventStream.ts` | Refactored to use sseConnection |
| `frontend/src/hooks/useEventStream.test.ts` | Updated mocks for reader.cancel + jitter |
| `frontend/src/hooks/useUserEventStream.ts` | Refactored to use sseConnection |
| `frontend/src/hooks/useUserEventStream.test.tsx` | Updated mocks for reader.cancel + jitter |
| `frontend/src/api/events.ts` | Added resignationCount backoff |
| `frontend/src/api/events.test.ts` | NEW — 5 leader resignation tests |

## Test Results

All 639 tests pass (77 test files).
