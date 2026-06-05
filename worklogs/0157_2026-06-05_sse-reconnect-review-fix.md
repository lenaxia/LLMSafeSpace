# Worklog: SSE Reconnect — Review Fix (AbortController + Tests)

**Date:** 2026-06-05
**Session:** Address PR #25 review findings
**Status:** Complete

---

## Objective

Fix incomplete Bug 2 implementation and add missing tests as identified in PR #25 automated review.

---

## Review Findings Addressed

### Finding 1 — Bug 2 fix incomplete (useUserEventStream.ts)

**Problem:** `abortCtrl` was declared `const` and never replaced/aborted in `scheduleReconnect()`. When the 35s read timeout fires, the hanging `reader.read()` is never cancelled, causing resource leaks (multiple concurrent fetch streams).

**Fix:** Changed `const abortCtrl` → `let abortCtrl`. In `scheduleReconnect()`, added `abortCtrl.abort()` followed by `abortCtrl = new AbortController()` before calling `connect()` — matching the pattern already correct in `useEventStream.ts` (Bug 1).

### Finding 2 — No new tests

**Problem:** No tests covered the 35s read timeout or abort-on-reconnect for either hook.

**Fix:** Added tests to both `useEventStream.test.ts` and `useUserEventStream.test.tsx`:
- "reconnects after READ_TIMEOUT_MS of silence" — verifies the timeout triggers reconnect
- "aborts old controller on reconnect so hanging read is released" — verifies the stale AbortController is aborted

---

## Files Changed

| File | Change |
|------|--------|
| `frontend/src/hooks/useUserEventStream.ts` | `const` → `let` abortCtrl; abort + replace in `scheduleReconnect()` |
| `frontend/src/hooks/useEventStream.test.ts` | Added 2 timeout/abort tests |
| `frontend/src/hooks/useUserEventStream.test.tsx` | Added 2 timeout/abort tests |
