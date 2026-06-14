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

`prior == ""` means the workspace had no prior known phase in the handler. This happens in two cases:
(a) seed calls — API restarts with workspace already Active; (b) real Creating→Active transitions
where the API missed the Creating event during restart. Both require `EnsureWatching`.
For `prior == phaseActive` (Active→Active reconcile), the `else` branch runs, preserving the
existing behaviour of not resetting an already-healthy subscription.

**Metering (seed calls with `prior=""`):** `RecordLifecycleEvent` is called unconditionally —
including on seed calls — producing a phantom lifecycle record with `from_phase=""`.
This was a deliberate tradeoff: the alternative (guarding with `prior!=""`) silently drops
Creating→Active events for workspaces that transition while the API is restarting, which corrupts
billing data worse than a phantom record. The metering service handles `from_phase=""` as a
restart-artifact marker. See `proxy_events.go:35-51` comment.

### Tests Added

**`api/internal/services/workspace/watcher_test.go`:**
- `TestWorkspaceWatcher_SeedResourceVersion_CallsOnPhaseChangeForActiveWorkspaces` — verifies
  `onPhaseChange` is called for each Active workspace during seeding, and not for non-Active ones
- `TestWorkspaceWatcher_SeedResourceVersion_NonActiveNoCallback` — verifies Suspended/Creating/Failed
  workspaces do not trigger `onPhaseChange` during seeding

**`api/internal/handlers/proxy_test.go`:**
- `TestProxy_OnPhaseChange_SeedCallActive_StartsSSETracker` — verifies that calling `onPhaseChange`
  with `prior=""` (seed call) starts a new SSE subscription (the regression test for this bug)
- `TestProxy_OnPhaseChange_CreatingToActive_AfterRestart_RecordsLifecycleEvent` — verifies that a
  Creating→Active transition with `prior=""` (first observation after restart) DOES record a
  lifecycle event with `from_phase=""`, confirming the unconditional metering decision
- `TestProxy_OnPhaseChange_RecordsLifecycleEvent` — updated to set `prior="Creating"` for a
  standard Creating→Active transition
- `TestProxy_PhaseChange_RunningNoInvalidation` — updated to set `prior="Active"` to correctly
  represent the Active→Active reconcile case (the test was inadvertently testing the seed case)

---

## Key Decisions

1. **Call `onPhaseChange` from seed rather than `EnsureWatching` directly.** `onPhaseChange` is
   the correct hook — it also handles `invalidateCaches`, `userBroker.RecordWorkspaceOwner`, and
   `activityTracker`. Calling `EnsureWatching` directly would duplicate logic.

2. **`prior==""` semantics.** The code comment at `proxy_events.go:76-79` documents that `prior=""`
   covers two cases: seed calls AND real first-observations after restart. The `prior==""` path is
   therefore not exclusively "seed calls" — it is "first handler invocation for this workspace",
   which may be either. Both cases correctly require `EnsureWatching`.

3. **Broadcast on seed call is acceptable.** On API restart, connected clients receive a
   `workspace.phase` event with `phase=Active`. This is a harmless redundant notification —
   preferable to the complexity of an additional guard that breaks the existing test for terminal
   phases (where `prior` is `""` after the handler deletes it on `Terminating`).

4. **Metering on seed call is intentionally unconditional.** See "Metering" section above.
   The `prior!=""` guard was introduced in an early iteration of this fix and then intentionally
   removed in commit `782d3bd2` after reviewer feedback. The guard was incorrect because it would
   drop real Creating→Active billing events for workspaces that transitioned during an API restart.

5. **`contextTotal=0` is addressed in a follow-on PR (#162).** Added `ContextLimit` to
   `LLMModelConfig` and `FormatOpenCodeConfig` — see worklog 0263.

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

## Files Modified

- `api/internal/services/workspace/watcher.go` — `seedResourceVersion()`: call `onPhaseChange` for Active workspaces
- `api/internal/handlers/proxy_events.go` — `onPhaseChange()`: fix `phaseActive` condition; unconditional metering with explanatory comment
- `api/internal/services/workspace/watcher_test.go` — add two seed-call tests
- `api/internal/handlers/proxy_test.go` — add two new tests; fix three existing tests that assumed wrong prior-phase semantics
- `worklogs/0271_2026-06-13_fix-context-usage-sse-watcher-race.md` — this file
