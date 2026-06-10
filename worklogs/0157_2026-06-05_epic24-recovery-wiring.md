# Worklog: Epic 24 — Wire Recovery System, Eliminate Terminal Failures

**Date:** 2026-06-04 / 2026-06-05
**Session:** Wire Epic 24's per-class recovery system into the reconcile loop, replacing the old 3-strike terminal failure path. Five AI review iterations to achieve clean approval.
**Status:** Complete (PR #24 approved, pending merge)

---

## Objective

Replace all `markFailed` and `recoverFromTransientPodLoss` calls with `enterRecovery(classifyFailure(obs))` so workspaces never reach terminal `Failed` from transient causes. Add exponential backoff and per-class failure policies.

---

## Work Completed

### Code Changes (single commit: `92261de`)

1. **phase_pending.go**: Removed `pendingTimedOut` from PVC-not-found path (was causing infinite loop — `CreationTimestamp` is immutable). PVC now always created when missing. PVC-exists-but-not-bound timeout calls `enterRecovery(Infrastructure)`.

2. **phase_creating.go**: 
   - `buildPod` failure → `enterRecovery(Configuration)` (was `markFailed`)
   - Pod in `PodFailed` → `observePod → classifyFailure → deletePod → enterRecovery` (was `markFailed`)
   - Added `NextRetryAt` enforcement (backoff respected before pod creation)

3. **phase_active.go**:
   - Pod missing → `enterRecovery(Infrastructure)` (was `recoverFromTransientPodLoss`)
   - Pod not Running → `observePod → classifyFailure → enterRecovery` (was `recoverFromTransientPodLoss`)
   - CrashLoopBackOff → `observePod → classifyFailure → enterRecovery` (was `recoverFromTransientPodLoss`)
   - `maybeResetTransientCounter` → `maybeResetConsecutiveFailures` (2-min stability window)

4. **recovery.go**: Removed dead code (`recoverFromTransientPodLoss`, `maybeResetTransientCounter`). Kept `handleFailed` for legacy workspace self-heal. Added `ConsecutiveFailures`/`NextRetryAt` clearing to all `handleFailed` recovery paths.

5. **recovery_policy.go**: Added `timeUntilNextRetry`, `maybeResetConsecutiveFailures`, `stabilityResetWindow`.

6. **classification.go**: Fixed ephemeral-storage eviction classification (moved before general `Evicted` catch).

7. **helpers.go**: Removed `markFailed` (unused after wiring).

### Review Iterations (5 total)

| # | Finding | Fix |
|---|---------|-----|
| 1 | `classifyFailure` dead-code branch (ephemeral eviction unreachable) | Moved check before general `Evicted` switch |
| 2 | Pending→Creating with empty PVCName causes buildPod failure | Stay in Pending with inline backoff |
| 3 | `handleFailed` doesn't clear `ConsecutiveFailures` | Added clearing to all 4 recovery paths |
| 4 | `CreationTimestamp = now` is dead write (Status().Update ignores ObjectMeta) | Replaced with `NextRetryAt` as backoff gate |
| 5 | `pendingTimedOut` always true after first timeout (immutable CreationTimestamp) | Removed `pendingTimedOut` from PVC-not-found path entirely |

### Design Doc Updates

- US-24.9: Marked superseded (readiness gate already shipped in `85ace63`)
- US-24.15: Marked superseded (secret self-healing already in handleCreating)
- US-24.12: Rewritten to reflect deferred cleanup status

---

## Key Decisions

1. **Removed `pendingTimedOut` from PVC-not-found path** — The timeout mechanism relied on resetting `CreationTimestamp` which is immutable via Status().Update. Simplest fix: just create the PVC when it doesn't exist. The K8s API handles transient failures via framework requeue.

2. **Kept `handleFailed` for legacy workspaces** — Any workspace already in Failed phase from before this change will still self-heal via the existing `handleFailed` logic.

3. **Single commit on PR** — After 5 review iterations, squashed to one clean commit rebased on latest main for clean merge.

---

## Blockers

None.

---

## Tests Run

```
go test -timeout 120s ./controller/... ./pkg/...  → all 22 packages pass
```

- 7 new tests: `maybeResetConsecutiveFailures` (4 cases), `timeUntilNextRetry` (3 cases)
- 1 new test: `TestClassifyFailure_Evicted_EphemeralStorage`
- 3 existing tests updated for new behavior
- 1 test renamed (`Pending_Timeout_EntersRecovery` → `Pending_NoPVC_CreatesPVC`)

---

## Next Steps

1. Merge PR #24 once CI completes (frontend TypeScript failure is pre-existing on main)
2. Deploy to cluster and validate recovery behavior with `kubectl delete pod`
3. Consider US-24.12 (CRD field cleanup) after 1 week of stable operation
4. Write worklog for the deployment validation

---

## Files Modified

- `controller/internal/workspace/phase_pending.go`
- `controller/internal/workspace/phase_creating.go`
- `controller/internal/workspace/phase_active.go`
- `controller/internal/workspace/recovery.go`
- `controller/internal/workspace/recovery_policy.go`
- `controller/internal/workspace/classification.go`
- `controller/internal/workspace/helpers.go`
- `controller/internal/workspace/controller_test.go`
- `controller/internal/workspace/deletion_timestamp_test.go`
- `controller/internal/workspace/classification_test.go`
- `controller/internal/workspace/recovery_wiring_test.go` (new)
- `design/stories/epic-24-self-healing-workspace-lifecycle/US-24.9-readiness-gate.md`
- `design/stories/epic-24-self-healing-workspace-lifecycle/US-24.15-secret-self-healing.md`
- `design/stories/epic-24-self-healing-workspace-lifecycle/US-24.12-crd-schema-update.md`
