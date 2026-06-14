# Worklog: Fix Context Usage Display — SSE Watcher Startup Race

**Date:** 2026-06-13
**Session:** Investigate and fix context usage bar showing "0/Unknown" for all sessions; validate all assumptions with live cluster evidence; implement and test fix
**Status:** Complete

---

## Objective

The frontend context usage bar was showing "0/Unknown" for all sessions regardless of how much history
they had. Investigate the root cause with live cluster evidence (no assumptions), fix it, and deliver a PR.

---

## Work Completed

### Investigation — Systematic Assumption Validation

Followed the stated protocol: enumerate every assumption, then validate each with cluster evidence
before drawing any conclusions.

**Assumptions enumerated:**

| # | Assumption | Validation method | Result |
|---|---|---|---|
| A1 | `kubectl get ws` works in current namespace | `kubectl config current-context` | **DISPROVED** — current namespace was `databases`, not `default` |
| A2 | Active workspaces exist | `kubectl get ws -n default` | Confirmed — 6 Active workspaces |
| A3 | `session_index.context_used` is being written | Direct PG query | **DISPROVED** — 599 rows, 0 with `context_used` set |
| A4 | `contextTotal=0` means `ModelContextLimit()` returns 0 | Agentd `/v1/statusz` with auth | Confirmed — `context.total_tokens=0` |
| A5 | `ModelContextLimit()` fails because models lack limits | `/config/providers` with Basic auth | Confirmed — `thekao cloud` provider has `ctx=0` for all models |
| A6 | Sidebar "0/Unknown" caused by `context_used=0` in session_index | Read Sidebar.tsx source | Confirmed — Sidebar reads from `/sessions` → session_index |
| A7 | SSE `session.next.step.ended` emits expected tokens shape | Live SSE monitor on port 4096 with auth | Confirmed — format is `{id, type, properties:{sessionID, tokens}}` |
| A8 | session_index.context_used read correctly in API | Traced code + PG query | Confirmed — query correct, column exists, insert SQL works — the write just never happens |

### Root Cause Identified

**Primary bug:** `api/internal/services/workspace/watcher.go:seedResourceVersion()` was called
asynchronously inside `go w.runWatchLoop()`. The `proxy_lifecycle.go:Start()` method calls
`watcher.Start()` (which spawns the goroutine and returns immediately), then immediately iterates
`watcher.GetAllKnownPhases()` — which is **empty** because `seedResourceVersion` hasn't run yet.

The `EnsureWatching` loop in `proxy_lifecycle.Start()`:
```go
for wsName, phase := range watcher.GetAllKnownPhases() {  // always empty
    if phase == string(phaseActive) {
        h.sseTracker.EnsureWatching(wsName)  // NEVER REACHED
    }
}
```

Result: The SSE tracker never connects to any workspace pod. All `session.next.step.ended` events
are missed. `persistContextFromEvent` is never called. `session_index.context_used` is never written.
The Sidebar shows `0/Unknown` for all sessions.

**Confirmed with:**
- `kubectl exec` on workspace pod: `cat /proc/net/tcp6` — no established connections to port 4096 from API pods
- Captured live SSE stream: `message.part.delta`, `session.status` events arrive; session tracker in agentd has correct `contextUsed` values (non-zero in CRD status); but API proxy never receives them
- PostgreSQL: `session_index.context_used` = NULL for all 599 rows, including sessions with `last_message_at` 6+ hours after API pod restart

**Secondary issue (not a code bug):** `contextTotal=0` because `thekao cloud` provider models have
no `limit.context` configured in `agent-config.json`. This is a LiteLLM configuration gap, not a
code defect. The "Unknown" badge in DiskUsageBar for `contextTotal=0` is correct behavior.

### Fix

Two-part change:

**Part 1 — `watcher.go:seedResourceVersion()`** (`api/internal/services/workspace/watcher.go`):
After populating `knownPhases` during the initial List, collect all Active workspaces and call
`w.onPhaseChange(ws)` for each of them (after releasing locks, consistent with the `onVersionSync`
pattern already in use). This ensures the SSE tracker starts watching all already-Active workspaces
when the API restarts, even though no phase transition event is emitted for them.

**Part 2 — `proxy_events.go:onPhaseChange()`** (`api/internal/handlers/proxy_events.go`):
Changed the `phaseActive` branch condition from:
```go
if prior != "" && prior != string(phaseActive) {  // never true for seed calls (prior="")
```
to:
```go
if prior == "" || prior != string(phaseActive) {  // true for seed calls AND real transitions
```

`prior == ""` means the workspace had no prior known phase in the handler — which happens only
for seed calls (API restart). In this case, `EnsureWatching` must be called to start the SSE
subscription. For `prior == phaseActive` (Active→Active reconcile), the `else` branch still runs,
preserving the existing behavior of not resetting an already-healthy subscription.

**Side effects addressed:**
- Metering guard: `RecordLifecycleEvent` is only called when `prior != ""` to prevent phantom
  billing records on API restart (seed calls with `prior=""` are not real phase transitions).
- Existing test `TestProxy_OnPhaseChange_RecordsLifecycleEvent` updated to set `prior="Creating"`
  to simulate a real transition.
- `TestProxy_PhaseChange_RunningNoInvalidation` updated to set `prior="Active"` to correctly
  represent the Active→Active reconcile case (the test was inadvertently testing the seed case).

### Tests Added

**`api/internal/services/workspace/watcher_test.go`:**
- `TestWorkspaceWatcher_SeedResourceVersion_CallsOnPhaseChangeForActiveWorkspaces` — verifies
  `onPhaseChange` is called for each Active workspace during seeding, and not for non-Active ones
- `TestWorkspaceWatcher_SeedResourceVersion_NonActiveNoCallback` — verifies Suspended/Creating/Failed
  workspaces do not trigger `onPhaseChange` during seeding

**`api/internal/handlers/proxy_test.go`:**
- `TestProxy_OnPhaseChange_SeedCallActive_StartsSSETracker` — verifies that calling `onPhaseChange`
  with `prior=""` (seed call) starts a new SSE subscription (the regression test for this bug)
- `TestProxy_OnPhaseChange_SeedCall_NoLifecycleEvent` — verifies no metering event is recorded
  for seed calls
- `TestProxy_OnPhaseChange_RecordsLifecycleEvent` — updated to use `prior="Creating"` for a real
  Creating→Active transition

---

## Key Decisions

1. **Call `onPhaseChange` from seed rather than `EnsureWatching` directly.** `onPhaseChange` is
   the correct hook — it also handles `invalidateCaches`, `userBroker.RecordWorkspaceOwner`, and
   `activityTracker`. Calling `EnsureWatching` directly would duplicate logic.

2. **`prior==""` semantics.** The watcher never calls `onPhaseChange` when `existed==false`
   (first observation of a workspace). So `prior==""` in the handler is exclusive to seed calls.
   This is a valid discriminator; no additional sentinel value is needed.

3. **Broadcast on seed call is acceptable.** On API restart, connected clients receive a
   `workspace.phase` event with `phase=Active`. This is a harmless redundant notification —
   preferable to the complexity of an additional guard that breaks the existing test for terminal
   phases (where `prior` is `""` after the handler deletes it on `Terminating`).

4. **`contextTotal=0` is not fixed.** The `thekao cloud` provider lacks `limit.context` in
   `agent-config.json`. This is a configuration gap in LiteLLM, not a code bug. The "Unknown"
   badge is correct. Fix is: add model context window sizes to `agent-config.json`.

---

## Blockers

None.

---

## Tests Run

```
go test -timeout 120s -race ./api/internal/handlers/... ./api/internal/services/workspace/...
# All tests pass except two pre-existing failures unrelated to this change:
#   TestHandler_E2E_LLMProvider_BindTriggersReloadWithFormattedConfig (pre-existing)
#   TestHandler_E2E_LLMProvider_MultipleProviders_Bind (pre-existing)
# Both confirmed pre-existing by running same tests on main branch with git stash.

go build ./...  # clean
```

---

## Next Steps

1. Deploy fix to cluster — restart API pods to verify SSE tracker starts watching all Active workspaces.
2. After one LLM step in any workspace, verify `session_index.context_used` is now set.
3. Address `contextTotal=0`: configure model context window sizes in LiteLLM/`agent-config.json`.

---

## Files Modified

- `api/internal/services/workspace/watcher.go` — `seedResourceVersion()`: call `onPhaseChange` for Active workspaces
- `api/internal/handlers/proxy_events.go` — `onPhaseChange()`: fix `phaseActive` condition; add metering guard
- `api/internal/services/workspace/watcher_test.go` — add two seed-call tests
- `api/internal/handlers/proxy_test.go` — add two new tests; fix three existing tests that assumed wrong prior-phase semantics
- `worklogs/0260_2026-06-13_fix-context-usage-sse-watcher-race.md` — this file
