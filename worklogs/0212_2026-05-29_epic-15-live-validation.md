# Worklog 0076: Epic 15 Live Validation & Bug Investigation

**Date:** 2026-05-29
**Epic:** 15 — Streaming State Resilience & Mid-Stream Reconnect
**Cluster:** safespace.thekao.cloud (default namespace)
**Commit deployed:** sha-cdf2ddc (revision 64)

## Summary

Partial live validation of Epic 15 streaming resilience features. EA5 assumption validated (FALSE). SSE event pipeline discovered to be non-functional for the test workspace. User-reported refresh-during-streaming bug investigated — root cause unresolved but two hypotheses eliminated.

## Test Environment

- **Workspace:** f220ae7e-9856-4e15-b7d2-7668fa8698b3 (epic15-test-ws-2, runtime: base)
- **Opencode version:** v1.15.12 (free provider: big-pickle via opencode)
- **API image:** sha-cdf2ddc
- **API log level:** info (SSETracker debug logs suppressed)

## Prerequisites

| Check | Result | Detail |
|-------|--------|--------|
| Cluster reachable | PASS | kubectl + port-forward on 18080 |
| Workspace Active | PASS | Phase=Active, AgentHealthy=True |
| Opencode v1.15.12+ | PASS | version=1.15.12, connected=[opencode] |
| LLM provider working | PASS | Free provider "big-pickle" via opencode responds |
| GET /workspaces/:id/status returns sessions | PASS | activeSessions field tracks correctly |

## EA5 Assumption Validation

**Assumption:** `GET /workspaces/:id/sessions/:sessionId/message` returns the in-progress assistant message BEFORE the session goes idle.

**Result: FALSE**

### Procedure
1. Created session `ses_18a9b578affeCm2Kh13cK9c3ZQ`
2. Sent long prompt via `prompt_async`
3. Polled history endpoint 30 times at 1s intervals during streaming
4. After session went idle, fetched final history

### Findings
- During streaming: history shows `step-start` and `reasoning` parts but assistant `text` part has `text_len=0`
- After idle: assistant `text` part populated with full response
- The history endpoint does NOT expose partial/in-progress text

### Impact on Epic 15
- **Test 2 (mid-stream history renders on reconnect):** The UX is degraded. On reconnect, the user sees prior messages + streaming dots, NOT the partial response text. This is documented as acceptable in the Epic 15 test plan.
- **Test 3 (new parts stream live after reconnect):** Boundary detection still works because parts have unique IDs. New parts (not in history) will stream live.

## Test 6: Normal Send Flow Regression

**Result: PASS (partial)**

| Step | Result | Detail |
|------|--------|--------|
| Dots appear on send | N/A (CLI test) | Status poll shows activeSessions=1 |
| Response streams in | PASS | prompt_async returns 202, opencode processes |
| Dots disappear on idle | PASS | Status poll shows activeSessions=0 |
| No duplicate messages | PASS | History shows 1 user + 1 assistant message |

### Status Poll Lifecycle
```
Pre-send:    activeSessions=0
During send: activeSessions=1  (within 3s of prompt)
Post-idle:   activeSessions=0  (within ~15s for short response)
```

## SSE Event Pipeline — CRITICAL FINDING

**The API's SSE event broker publishes ZERO events to browser clients.**

### Evidence
1. `curl -sN /workspaces/:id/events` connects (200 OK, text/event-stream) but receives no events
2. Tested with 3 different sessions, each with 15+ second streaming duration
3. API logs show no errors during SSE subscription setup
4. Opencode's `/event` endpoint works directly from within the pod (confirmed `server.connected` event)
5. SSETracker is initialized (code path verified: `proxyHandler.Start()` is called at boot)
6. SSETracker's `EnsureWatching` is called when browser subscribes (line 247 of proxy.go)

### Probable Cause
The SSETracker connects to opencode's `/event` endpoint on the pod, reads events, and publishes to the broker via `onRawEvent`. But the tracker's logs are at Debug level (`session_tracker.go:128`), and the API runs at Info level. The tracker may be:
1. Failing to connect (auth issue with `getPassword`)
2. Failing to parse events from opencode v1.15.12
3. Connecting but the broker channel isn't delivering to subscribers

**This blocks validation of Tests 1-5.** The frontend's SSE-driven features (streaming dots, idle detection, reconnect) depend on this pipeline. The status poll fallback works (activeSessions tracks correctly), but the SSE events (message.part.delta, session.status) do not flow.

### Recommended Fix
1. Temporarily elevate SSETracker log level to Info to capture connection errors
2. Or add a `/debug/sse-status` endpoint that reports `SubscriptionCount()` and last error
3. Verify the tracker can authenticate to opencode pods with the password from `workspace-pw-*` secrets

## Bug Investigation: Page Refresh Kills In-Flight Request

### User Report
> "If I had a request in flight and I navigated away from the page, the request would continue. While I wouldn't see streaming anymore, if I continuously refreshed the page I would see new responses. This no longer happens."

### Validated Findings

| Hypothesis | Result | Evidence |
|------------|--------|----------|
| `registerTabCloseAbort` introduced by Epic 15 | **FALSE** | `git log -S registerTabCloseAbort` shows commit `799973e` (2026-05-24), Epic 15 is `2566747` (2026-05-29). Pre-dates by 5 days. |
| `sendBeacon` on refresh aborts opencode session | **FALSE** | `sendBeacon` sends POST without Authorization header. The `/abort` endpoint requires auth middleware. Verified: unauthenticated POST returns `{"error":"Authorization token required"}`. The beacon is a no-op. |
| `doProxy` context propagation kills upstream | **POSSIBLE** | `proxy.go:410` uses `http.NewRequestWithContext(c.Request.Context(), ...)`. For the `prompt_async` endpoint, the response (202) returns immediately, so context cancellation after that has no effect. But if the client disconnects BEFORE the 202 arrives, `Do(req)` could fail. |
| API logs show abort calls for affected session | **FALSE** | Zero `POST /abort` in 500+ log lines for `ses_18aca5e09ffeuiQHGz2wc31Bi7` |

### Remaining Hypotheses
1. **Frontend display issue** — Epic 15's `reconcileOnIdle` or boundary detection may prevent new responses from rendering after reconnect
2. **Opencode v1.2.27 behavior** — the affected workspace runs v1.2.27, not v1.15.12. Different async handling
3. **`doProxy` context cancellation on prompt_async** — if client TCP RST arrives before 202, the prompt never reaches opencode

### Recommended Next Steps
1. Reproduce with browser DevTools Network tab open — check if `sendBeacon` returns 401
2. Reproduce on workspace running opencode v1.15.12 to rule out version difference
3. Add `fmt.Fprintf(c.Writer, "data: ...")` logging to `StreamEvents` to confirm zero broker events
4. Fix `registerTabCloseAbort` to include auth token (or remove it since it's currently a no-op)

## Tests Not Completed

| Test | Reason |
|------|--------|
| Test 1: Streaming indicator survives refresh | SSE pipeline broken — cannot validate server-driven indicator |
| Test 2: Mid-stream history renders on reconnect | EA5 FALSE means degraded UX; SSE pipeline broken prevents full validation |
| Test 3: New parts stream live after reconnect | SSE pipeline broken — no part.delta events flow to browser |
| Test 4: Idle reconciliation | SSE pipeline broken — session.status events don't reach frontend |
| Test 5: SSE disconnect recovery | SSE pipeline broken — cannot test reconnect if no events flow |

## Deployment Notes

- Deployed revision 64 (sha-cdf2ddc) to default namespace via `helm upgrade`
- All pods Running and Ready: API (2), Controller (1), Frontend (1)
- Created test user: epic15-test@example.com
- Created test workspace: epic15-test-ws-2 (f220ae7e)
- Workspace pod credentials configured from env OPENAI_API_KEY (not needed — opencode has free providers)
- Bug discovered in controller: workspace label generation from runtime image ref produces invalid label (`ghcr.io/...` contains `/` which is invalid in Kubernetes labels)

## Files Changed
- None (validation only, no code changes)
