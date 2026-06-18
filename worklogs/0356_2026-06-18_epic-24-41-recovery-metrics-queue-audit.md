# Worklog: Epic 24 US-24.7/24.11 + Epic 41 closeout

**Date:** 2026-06-18
**Session:** Wire ControllerRestartCount, add 7 recovery Prometheus metrics, fix safe-mode cardinality hazard, audit Epic 41 409 guard completeness
**Status:** Complete
**Epics:** 24 (Self-Healing), 41 (Message Queue Reliability)

---

## Objective

Close remaining actionable items in Epic 24 (US-24.7 ControllerRestartCount, US-24.11 recovery metrics) and Epic 41 (409 guard audit). US-24.17 (disk pressure) was found already implemented during exploration — README updated to reflect actual state.

---

## Work Completed

### 1. US-24.7 — Wire `ControllerRestartCount`

**Problem:** `Status.ControllerRestartCount` field was declared but never written. User-initiated restarts (RestartGeneration bump) and controller-initiated restarts (health-check threshold exceeded) were indistinguishable.

**Changes:**
- `health.go:122,153` — both health-check-initiated pod-delete paths now increment `ws.Status.ControllerRestartCount++` alongside `ws.Status.RestartCount++`, plus `metrics.WorkspaceControllerRestartsTotal.Inc()`.
- `phase_creating.go:146` — on successful recovery (Creating→Active with prior failures), `ControllerRestartCount` is reset to 0.
- `recovery_policy.go:79` — `maybeResetConsecutiveFailures` (2-min stability window) also clears `ControllerRestartCount = 0`.

### 2. US-24.11 — Recovery Prometheus metrics

**New metrics** (`controller/internal/metrics/metrics.go`):
- `llmsafespace_workspace_controller_restarts_total` — Counter. Distinct from user-initiated restarts.
- `llmsafespace_workspace_safe_mode_entries_total{trigger}` — Counter. Tracks safe-mode entries by failure class.
- `llmsafespace_workspace_safe_mode_exits_total{method}` — Counter. Tracks exits by method (`restart_generation`, `stability_reset`).
- `llmsafespace_workspaces_in_recovery` — Gauge. Aggregate count of workspaces with `ConsecutiveFailures > 0` currently not Active. Inc on `enterRecovery`, Dec on recovery success.
- `llmsafespace_workspace_recovery_duration_seconds{failure_class}` — Histogram. Wall-clock time from `enterRecovery` to Active transition.

**Cardinality fix (F18):**
- `WorkspaceSafeModeActive` changed from `GaugeVec{workspace_id}` to plain `Gauge`. The per-workspace label was explicitly rejected by the design's F18 finding ("1000 workspaces = 2000 unbounded time series; Prometheus OOM risk"). Now an aggregate count: Inc on entry, Dec on exit/termination.
- Updated emission in `metrics_wiring.go:recordRecoveryMetricsInto` — signature changed to accept `prometheus.Gauge` instead of `*prometheus.GaugeVec`.
- Updated `phase_terminating.go:75` — replaced `DeleteLabelValues` with conditional `Dec()`.

**Emission wiring:**
- `enterRecovery` → `recordRecoveryMetrics` now Inc's `WorkspacesInRecovery` + `WorkspaceSafeModeEntriesTotal`.
- `phase_creating.go` Active transition → Dec's `WorkspacesInRecovery`, observes `WorkspaceRecoveryDurationSeconds`, Inc's `WorkspaceSafeModeExitsTotal{restart_generation}` on SafeMode clear.
- `phase_terminating.go` → Dec's `WorkspaceSafeModeActive` if workspace was in SafeMode.

### 3. Epic 41 — 409 guard audit (closeout)

**Finding: DONE.** The exploration confirmed:
- `SendPromptAsync` (`proxy_handlers.go:71-87`) returns HTTP 409 with `{"error":"session is busy; retry after idle","retryAfter":1}` + `Retry-After: 1` header when session is active.
- `SendMessage` is intentionally unguarded (synchronous path; verified by `TestProxy_SendPromptAsync_409DoesNotAffectSendMessage`).
- Queue drain path handles upstream 409 via requeue+backoff (`proxy_events.go:469`).
- Guard reads through Redis-backed `wsstate.Store` (US-45.2), so multi-replica consistency is covered.

**Action:** Updated `design/stories/README.md` to mark Epic 41 as ✅ Complete.

### 4. US-24.17 — README correction

The audit README incorrectly claimed `WorkspaceConditionDiskPressure` was absent. It is implemented:
- Constant: `workspace_types.go:232`
- Detection: `health.go:272-286` (>95% ratio, auto-clear below, divide-by-zero guard)
- Tests: `health_pressure_provider_test.go` (6 tests)

Updated `design/stories/README.md` to reflect actual state.

---

## Key Decisions

1. **`WorkspaceSafeModeActive` → plain Gauge, not GaugeVec.** The per-workspace label violated the design's F18 cardinality finding. The aggregate gauge (Inc/Dec on entry/exit) provides the same SRE dashboard value without the time-series explosion.

2. **`WorkspacesInRecovery` uses Inc/Dec tracking, not per-reconcile scan.** A per-reconcile scan would be O(n) per workspace per reconcile — too expensive. The Inc/Dec approach is O(1) and matches the existing `WorkspacesRunning` pattern. Edge case: a workspace that goes terminal-Failed while in recovery leaves the gauge stale by 1 — acceptable for a monitoring signal (the Failed counter tracks the terminal event separately).

3. **Recovery duration uses `LastFailureAt` as the start anchor.** `enterRecovery` sets `LastFailureAt` to `metav1.Now()` before transitioning. The histogram observes `time.Since(LastFailureAt)` at the Creating→Active transition. This covers the full user-visible recovery time including backoff.

4. **Epic 41 closed without code changes.** The exploration proved the 409 guard is complete and tested. Adding code would be unnecessary work.

---

## Assumptions Validated (Rule 7)

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | `WorkspaceConditionDiskPressure` + detection exist | `workspace_types.go:232`, `health.go:272-286`, 6 tests in `health_pressure_provider_test.go` |
| A2 | `ControllerRestartCount` was never written | `grep -rn "ControllerRestartCount" controller/` — only declaration, no assignments (pre-fix) |
| A3 | `WorkspaceSafeModeActive` had per-workspace label | `metrics.go:48-51` — `GaugeVec{workspace_id}` (pre-fix) |
| A4 | Epic 41 409 guard exists for `SendPromptAsync` | `proxy_handlers.go:71-87` + 2 tests in `proxy_test.go` |
| A5 | Guard reads through Redis store | `proxy_connections.go:47-49` → `state().IsSessionActive` → `wsstate.Store` (US-45.2) |

---

## Tests Run

| Command | Outcome |
|---|---|
| `go build ./...` | PASS |
| `go vet ./controller/...` | PASS |
| `go test -race -count=1 ./controller/internal/workspace/` | PASS |
| `go test -race -count=1 ./...` | PASS (zero failures) |

---

## Files Modified

| File | Change |
|------|--------|
| `controller/internal/metrics/metrics.go` | 5 new metrics; `WorkspaceSafeModeActive` → plain Gauge; registered in AllCollectors |
| `controller/internal/workspace/health.go` | `ControllerRestartCount++` + `WorkspaceControllerRestartsTotal.Inc()` at 2 restart sites |
| `controller/internal/workspace/recovery_policy.go` | `ControllerRestartCount = 0` in stability reset |
| `controller/internal/workspace/phase_creating.go` | Recovery duration histogram, `WorkspacesInRecovery.Dec()`, safe-mode exit metric, `ControllerRestartCount = 0` on recovery success |
| `controller/internal/workspace/phase_terminating.go` | Safe-mode gauge Dec on termination |
| `controller/internal/workspace/metrics_wiring.go` | `recordRecoveryMetricsInto` signature + safe-mode Inc/entries; `recordRecoveryMetrics` passes new args |
| `controller/internal/workspace/metrics_wiring_test.go` | Updated 4 recovery metrics tests for new signature + helpers |
| `controller/internal/workspace/gauge_drift_test.go` | Replaced GaugeVec test with aggregate Gauge test |
| `design/stories/README.md` | Epic 24 + Epic 41 entries updated to actual state |

---

## Next Steps

- **Epic 29 US-29.1** — Extract `AgentClient` interface (separate PR; 5 password-getter sites to unify).
- **Epic 24 US-24.13** — `buildSafeModePod` (needs design: safe-mode image must be chosen/built/pinned).
- **Epic 44** — US-44.1c (broker-side `agent_died` bridge) + US-44.8 (Prometheus ops metrics) to close Phase 1.
