# Worklog: Refresh-Aborts-LLM Bug — Root Cause + Fix + Race Hardening

**Date:** 2026-05-29
**Session:** Bug-report follow-up: "page refresh kills in-flight LLM request"
**Status:** Complete

---

## Objective

Investigate the user-reported bug "page refresh kills in-flight LLM request"
on safespace.thekao.cloud. The prior triage report claimed the bug's
root cause was "UNRESOLVED" with three speculative directions. Validate
those claims against the live cluster, identify the actual cause, fix it
with TDD, and harden adjacent race conditions.

---

## Assumptions Stated and Validated (Rule 7)

| # | Assumption | Validated against | Result |
|---|------------|-------------------|--------|
| A1 | User refreshed during streaming | API replica logs for session `ses_18a7e5ef5ffeZ12jBuBfgflebq` | ✅ TRUE — SSE stream `ajjC1JAB` ended at 20:51:48.024 (17.9s after open); new stream `pNx7fUOa` opened 443ms later |
| A2 | Workspace runs opencode v1.15.12 (not v1.2.27) | `kubectl get workspace de04f989... -o yaml` `Status.Conditions` | ✅ TRUE — `connected=[opencode] sessions=0 version=1.15.12` |
| A3 | Frontend issued POST /abort on refresh | API logs | ✅ TRUE — `oXIxu07P POST /abort` at 20:51:48.012 |
| A4 | sendBeacon abort 401'd because no Authorization header (prior bug report claim) | API log status code for `oXIxu07P` | ❌ **REFUTED** — returned **200** with body `"true"`. The `lsp_session` HttpOnly cookie set by `router.go:266` is read by `auth.go:579`'s `extractToken`. sendBeacon sends same-origin cookies. Prior bug report's analysis of the auth path was wrong. |
| A5 | prompt_async returned 204 quickly | API logs duration field | ✅ TRUE — `EEwe5wIR` 33ms, status 204 |
| A6 | Browser SSE stream killed by tab unload | Stream end timestamp matches refresh moment | ✅ TRUE — `ajjC1JAB` ended at 20:51:48.024 |
| A8 | LLM ran to completion server-side, only display lost | `kubectl exec -- curl localhost:4096/session/.../message` | ❌ **REFUTED** — opencode session history shows assistant message with `"error": {"name": "MessageAbortedError", "data": {"message": "Aborted"}}` and `"completed": 1780087908037` (20:51:48.037). The agent was killed at the agent level, not just the display lost. |

The validation collapsed assumption A4 and A8 simultaneously: the abort
endpoint is not dead, it is the cause.

---

## Work Completed

### 1. Forensic timeline reconstruction

Pulled API logs from both replicas (`/tmp/api-9xr5j.log`, `/tmp/api-s4jhm.log`),
sorted by timestamp, matched `request_id` across "Request received" and
"Request completed" entries:

```
20:51:30.133  GET /events  reqid=ajjC1JAB        (SSE stream opens — page 1)
20:51:45.880  POST /sessions/.../prompt          (prompt_async)
20:51:45.913  → 204 in 33ms                       ✅ sendAsync returns
20:51:48.012  POST /sessions/.../abort  reqid=oXIxu07P
20:51:48.024  /events ajjC1JAB ENDS (200, dur=17.9s)   ← tab unloaded
20:51:48.037  opencode marks message MessageAbortedError ← THE KILL
20:51:48.049  POST /abort returns 200, body "true"
20:51:48.418  GET /sessions  (page reload begins)
20:51:48.467  GET /events    reqid=pNx7fUOa     (new SSE — page 2)
```

Direct opencode session history fetch via `kubectl exec`:
```json
{"role":"assistant","time":{"completed":1780087908037},
 "error":{"name":"MessageAbortedError","data":{"message":"Aborted"}},
 "parts":[
   {"type":"step-start"},
   {"type":"reasoning","text":"The user is asking me to tell a long story..."},
   {"type":"text","text":"Once upon a time, in a land far"}
 ]}
```

The truncation was not in the display layer; the agent was actually killed.

### 2. Root cause

`registerTabCloseAbort()` in `frontend/src/api/events.ts` (introduced in
799973e on 2026-05-24, "Harden for production confidence") installs a
`beforeunload` handler that calls `navigator.sendBeacon('/abort')`. On every
F5 the browser:
1. Fires `beforeunload` → handler runs
2. sendBeacon POSTs to `/abort` carrying the `lsp_session` HttpOnly cookie
   automatically (same-origin, third-party-cookie rules don't apply)
3. API auth middleware accepts the cookie → 200
4. Proxy forwards to `http://pod:4096/session/.../abort` → opencode aborts

This actively defeats Epic 15 (commit `2566747`, 2026-05-29), whose entire
purpose is mid-stream reconnect after refresh. The two features were
contradictory and Epic 15 lost.

### 3. The fix (TDD)

**Failing tests written first** (`frontend/src/hooks/useChatStream.test.ts`):

```ts
it("does NOT install a beforeunload handler — refresh must not abort the in-flight LLM response", ...)
it("registerTabCloseAbort is not exported from api/events (removed in fix for refresh-abort bug)", ...)
```

Verified both failed against current code (with `addEventListener` spy
showing `[['beforeunload', fn]]` and `registerTabCloseAbort` defined).

**Fix applied:**
- `frontend/src/api/events.ts` — removed `registerTabCloseAbort` export entirely (10 lines)
- `frontend/src/hooks/useChatStream.ts` — removed import, the `cleanupBeaconRef` ref, the unmount cleanup `useEffect`, the call site at send-start, and the cleanup-on-finally call. Removed unused `useEffect` import.
- Updated `useChatStream.test.ts` mock setup — replaced the `vi.mock("../api/events")` with a plain `import * as eventsApi` for the regression assertion.

Both new tests pass. The 13 pre-existing tests still pass.

### 4. Race condition coverage (in response to user prompt)

Enumerated and tested race conditions in the redesigned `send()` flow:

| # | Race | Test result | Outcome |
|---|------|-------------|---------|
| R1 | Idle SSE arrives DURING `await sendAsync()` (before 204) — pre-existing bug | ❌ Failing | Real bug. Fixed: pre-arm idle observer with `idleAlreadyFired` flag before sendAsync; check flag before installing the long-wait resolver. |
| R2 | New session starts mid-flight — old session's idle must still resolve old wait via `capturedSessionId` | ✅ Passing | Existing capture logic correct. |
| R3 | `setTimeout` fires + late idle arrives after — must not double-resolve | ✅ Passing | Resolver nulled on timeout fire. |
| R4 | sendAsync rejects, pre-armed resolver stranded | ✅ Passing (after fix) | finally block clears `idleResolverRef.current = null` even on reject. |
| R5 | Stale idle from prior send leaks into next send | ✅ Passing | finally block cleanup proves it. |

R1 was a pre-existing latent bug introduced when the SSE-driven idle wait
was added. The window is short (33ms-ish HTTP roundtrip in the production
log) but real for fast/cached prompts. The fix is in scope because:
1. The user explicitly asked to cover races
2. Rule 5 (zero technical debt) applies to errors encountered in changed files
3. The fix is small (~15 lines) and entirely contained in the same function

**Net test count:** 16 → 21 in `useChatStream.test.ts` (+5; -1 deleted). Total
frontend tests: 494 → 499.

### 5. Pre-existing lint debt cleared in touched file

`useChatStream.ts:39` had `let timeoutId: ReturnType<typeof setTimeout>;`
which triggered `prefer-const`. Restructured to declare-and-init in one
statement (setTimeout fires before the resolver assignment but cannot run
before the next macrotask, so ordering is safe). Lint count for repo: 42 → 41.

### 6. Validation

- Frontend tests: `npx vitest run` — **499/499 passing** (67 files)
- Frontend build: `npm run build` — clean (152 KB gz JS, 7.81 KB gz CSS)
- Frontend lint: my files clean; pre-existing 41 errors in unrelated files (down from 42)
- Backend tests: `go test ./api/internal/handlers/ -run "Abort|Proxy"` — passing (no backend changes; sanity check that abort endpoint itself still functions for the explicit Stop button)
- AbortSessionButton tests: still pass — explicit user abort via Stop button is unaffected

---

## Key Decisions

1. **Remove `registerTabCloseAbort` entirely rather than gate by intent.**
   Distinguishing F5 from intentional tab close is not reliably possible
   from `beforeunload` alone. Epic 15's reconnect machinery is the correct
   architecture for refresh; aborting on every unload defeats it. The
   original justification (release maxActiveSessions slot quickly) is
   redundant — the proxy already releases on `session.status idle` SSE
   from the agent, and slots are per-session not per-tab.

2. **Pre-arm the idle observer before sendAsync.** A flag-based
   pre-observer captures any early idle event; the long-wait promise
   then checks the flag before installing the full resolver. This is
   strictly additive to the existing 60s timeout fallback.

3. **Did not touch backend abort handler.** The `/abort` endpoint itself
   is correct — it must work for the explicit user-initiated Stop button
   (`AbortSessionButton` → `useChatStream.abort()` → `abortController.abort()`).
   The bug was only the automatic `beforeunload` invocation on the frontend.

4. **Did not modify `lsp_session` cookie semantics.** The cookie is
   correctly designed for human-driven flows. Removing the broken
   `beforeunload` handler is sufficient and minimal.

---

## Blockers

None.

---

## Tests Run

| Command | Result |
|---------|--------|
| `cd frontend && npx vitest run src/hooks/useChatStream.test.ts` | 21/21 pass |
| `cd frontend && npx vitest run` | 499/499 pass (67 files) |
| `cd frontend && npm run build` | OK |
| `cd frontend && npm run lint` | 41 pre-existing errors in unrelated files; my files clean |
| `go test ./api/internal/handlers/ -run "Abort\|Proxy"` | OK |

---

## Next Steps

1. **Deploy and verify** — push to main, let CI build a new frontend image, redeploy, repeat the same F5 test from the original report. Expected behavior: refreshing during a long story prompt no longer truncates the response; Epic 15 reconnect renders accumulated history and resumes live streaming for new parts.
2. **Live cluster post-deploy validation** — repeat the test with browser DevTools Network tab open to confirm zero `POST /abort` requests on F5.

---

## Files Modified

| File | Change |
|------|--------|
| `frontend/src/api/events.ts` | Removed `registerTabCloseAbort` export (10 lines) |
| `frontend/src/hooks/useChatStream.ts` | Removed import, ref, unmount effect, call site, cleanup invocation. Pre-armed idle observer before sendAsync to fix R1. Restructured timeoutId declaration to fix `prefer-const`. Removed unused `useEffect` import. |
| `frontend/src/hooks/useChatStream.test.ts` | Replaced `registerTabCloseAbort` mock + test with regression test that asserts no `beforeunload` handler is installed and the export does not exist. Added 5 race-condition tests (R1, R2, R3, R4, R5). |
| `worklogs/0078_2026-05-29_refresh-aborts-llm-bug-fix.md` | This worklog. |

Untouched (verified intact):
- `api/internal/handlers/proxy.go` — abort endpoint behavior unchanged
- `frontend/src/components/chat/AbortSessionButton.tsx` — explicit Stop button unchanged
- `frontend/src/pages/ChatPage.tsx` — Epic 15 reconnect machinery is correct and now actually has a chance to work
