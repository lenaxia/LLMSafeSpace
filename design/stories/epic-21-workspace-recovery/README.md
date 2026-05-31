# Epic 21: Workspace Failure-Mode Robustness

**Status:** Planning
**Created:** 2026-05-30
**Priority:** Medium-High
**Depends on:** Worklog 0099 (Change A — declarative recovery from Failed via `spec.restartGeneration`)
**Blocks:** none
**Reserved IDs:** Epic 19 and Epic 20 are reserved for unrelated work and intentionally skipped.

## Problem Statement

The Workspace state machine has six paths into the `Failed` phase but only one path out — operator hand-edits the CRD. Worklog 0099 introduced **Change A**, a declarative recovery via `spec.restartGeneration`, which solved the immediate UX gap (frontend can now offer a "Retry" button instead of requiring `kubectl patch --subresource=status`). But Change A doesn't fix the upstream churn: workspaces still flip to Failed too eagerly, and the same root causes (transient agentd unresponsiveness under load, pod-Failed-during-creation when an operator deletes the pod) keep producing user-visible outages that need manual recovery.

Three follow-ups to Change A would close the remaining gaps. This epic scopes them.

| Change | Title | Reduces Failed-rate by | Risk |
|---|---|---|---|
| A | Declarative recovery via `spec.restartGeneration` (worklog 0099) | n/a (recovery, not prevention) | low — landed |
| B | Exponential backoff + reset window for transient pod loss | ~80% of today's terminal-Failed transitions | medium |
| C | Failure-cause taxonomy with per-cause retry policy | last 20%; eliminates surprising-Failed-from-Creating | medium-high |

The motivation comes from worklog 0099's incident: workspace `c98963e7-…` was set to terminal `Failed` because (a) `agentd /v1/statusz` starved under SSE load (3× timeouts in 30s) — the same flaw worklog 0096 already noted as a separate bug — and (b) the controller's `transientFailureCount >= 3 → terminal Failed` is a hard count with no decay, so a workspace that has run for hours with one transient blip earlier is treated as identically suspect to one that just stood up and failed three times in 30s.

---

## Change B — Exponential backoff for transient pod loss

### Goal

Replace the 3-strike hard count in `recoverFromTransientPodLoss` (controller.go:437-441) with exponential backoff and a reset window so:

1. **Transient flakes** (the worklog 0096+0099 case: agentd briefly unresponsive under load) self-heal without operator action and without exhausting the retry budget on the first cluster-wide noisy minute.
2. **Persistent failures** (pod CrashLoopBackOff from a corrupt config) still terminate eventually, instead of looping forever and burning K8s API quota.
3. **A long-running stable workspace** that has hit one transient failure 6 hours ago is not treated identically to a workspace that just hit 3 failures in 30 seconds.

### Proposed mechanism

Add three fields to `WorkspaceStatus`:

```go
// Worklog 0099 / Epic 21 Change B: backoff tracking.
ConsecutiveFailures int32        `json:"consecutiveFailures,omitempty"`
NextRetryAt        *metav1.Time `json:"nextRetryAt,omitempty"`
LastSuccessAt      *metav1.Time `json:"lastSuccessAt,omitempty"`
```

Replace today's `TransientFailureCount` increment with:

```go
// In recoverFromTransientPodLoss
n := workspace.Status.ConsecutiveFailures + 1
backoff := backoffSchedule(n)        // 5s, 30s, 5m, 30m, 4h, 1d (capped)
nextRetry := time.Now().Add(backoff)

workspace.Status.ConsecutiveFailures = n
workspace.Status.NextRetryAt = &metav1.Time{Time: nextRetry}
workspace.Status.LastTransientFailureAt = &metav1.Time{Time: time.Now()}
workspace.Status.Phase = v1.WorkspacePhaseCreating
```

Reset window (when the workspace ever stays Active long enough):

```go
// In maybeResetTransientCounter, called on every successful Active reconcile
const successResetWindow = 5 * time.Minute
if workspace.Status.LastSuccessAt != nil &&
   time.Since(workspace.Status.LastSuccessAt.Time) >= successResetWindow {
    workspace.Status.ConsecutiveFailures = 0
    workspace.Status.NextRetryAt = nil
}
```

Terminal cap: after `MaxConsecutiveFailures = 8` (i.e. ~12 hours of escalating retries), if there's still no success, transition to `Failed` with `failureReason: TooManyFailures` (see Change C).

`handleCreating` and `handleActive` honor `NextRetryAt`:

```go
if workspace.Status.NextRetryAt != nil && time.Now().Before(workspace.Status.NextRetryAt.Time) {
    return ctrl.Result{RequeueAfter: time.Until(workspace.Status.NextRetryAt.Time)}, nil
}
```

### Backoff schedule (proposed)

| Attempt | Backoff | Cumulative time |
|---|---|---|
| 1 | 5s | 5s |
| 2 | 30s | 35s |
| 3 | 2m | ~3m |
| 4 | 5m | ~8m |
| 5 | 15m | ~23m |
| 6 | 1h | ~1h 23m |
| 7 | 4h | ~5h 23m |
| 8 | 12h | ~17h |
| 9+ | terminal Failed | n/a |

Tunable via existing `Spec.MaxRetries` (rename to `MaxConsecutiveFailures` for clarity) and a new `Spec.BackoffMultiplier` if needed.

### Acceptance criteria

- [ ] **Unit:** `recoverFromTransientPodLoss` increments `ConsecutiveFailures`, sets `NextRetryAt` per the schedule, leaves `Phase=Creating`.
- [ ] **Unit:** Reconcile honors `NextRetryAt`: returns a requeue equal to `time.Until(NextRetryAt)`, does not delete or recreate the pod.
- [ ] **Unit:** Reaching `MaxConsecutiveFailures` transitions to `Failed` with `failureReason: TooManyFailures` (Change C dependency).
- [ ] **Unit:** Active reconcile resets `ConsecutiveFailures` after `LastSuccessAt` is at least `successResetWindow` old.
- [ ] **Integration (envtest or real-pg):** A workspace that fails twice, then runs Active for 6 minutes, then fails 3 more times reaches max-attempt 5 (not 5+2=7) — proving the reset works end-to-end.
- [ ] **Integration:** A workspace that fails 9 times in tight succession transitions to terminal `Failed` (not infinite loop).
- [ ] **Metrics:** Gauge `llmsafespace_workspace_consecutive_failures{workspace=…}` exported so operators can alert on workspaces approaching their cap.
- [ ] **No regression** to existing `Active`/`Creating`/`Suspended` happy paths.

### Edge cases requiring explicit handling

- Clock skew between controller and pod-creation timestamps. `NextRetryAt` is controller-local; safe.
- Controller restart mid-backoff: `NextRetryAt` is in CRD, so the new controller picks up the schedule correctly.
- Operator bumps `RestartGeneration` while in backoff: should immediately end the backoff (clear `NextRetryAt`, reset `ConsecutiveFailures`, transition per Change A). Test required.
- `RestartGeneration` bump from terminal Failed already handled in Change A; just ensure the recovery path zeros the new fields too.

### Files to modify

- `pkg/apis/llmsafespace/v1/workspace_types.go` — add 3 status fields
- `controller/internal/workspace/controller.go` — `recoverFromTransientPodLoss`, `maybeResetTransientCounter`, `handleActive`, `handleCreating`
- `controller/internal/workspace/constants.go` — backoff schedule constants
- `controller/internal/workspace/controller_test.go` — 6 unit tests, 2 integration
- `pkg/apis/llmsafespace/v1/zz_generated_deepcopy.go` — regenerate via `make deepcopy`

### Risk assessment

- **State-machine correctness** — needs the integration tests above. Without them it's easy to introduce a stuck-in-backoff bug where `NextRetryAt` is never cleared on success.
- **Behavioral change for existing workspaces** — pre-existing CRDs don't have the new status fields. `omitempty` makes this backwards-compatible at the JSON layer, but the controller must treat missing fields as zero-values. Add a unit test for "old CRD with no backoff fields runs through new controller without panic."

---

## Change C — Failure-cause taxonomy

### Goal

Today, all six paths to `Failed` produce a free-form `Status.Message`. Operators cannot programmatically distinguish "PVC stuck pending — retrying won't help, your storage class is broken" from "agentd timed out — almost certainly transient." The frontend cannot offer different recovery affordances (retry vs. delete-and-recreate vs. show-storage-class-error).

### Proposed mechanism

Add a typed `FailureReason` enum to `WorkspaceStatus`:

```go
// FailureReason categorizes WHY a workspace entered Failed phase.
// The reason determines the recovery affordances offered to the user
// and the controller's retry policy.
type FailureReason string

const (
    // TransientPodLoss: pod crashed or agentd became unreachable.
    // Almost always recoverable via Change A retry; Change B handles
    // most cases automatically.
    FailureReasonTransientPodLoss FailureReason = "TransientPodLoss"

    // PodFailedDuringCreation: pod entered K8s phase=Failed during
    // initial creation. May be transient (image pull retry, node
    // draining) or permanent (bad image, OOM). Change A retry is
    // safe; Change B should retry automatically up to N times.
    FailureReasonPodFailedDuringCreation FailureReason = "PodFailedDuringCreation"

    // PodBuildFailed: buildPod() returned an error (invalid runtime,
    // missing secret, malformed CRD). Permanent until the spec is
    // corrected. Frontend should surface the error directly.
    FailureReasonPodBuildFailed FailureReason = "PodBuildFailed"

    // PVCBindTimeout: PVC could not bind to a PV within
    // pendingPhaseTimeout. Usually a misconfigured StorageClass —
    // permanent until storage admin acts. Change A retry only helps
    // if the StorageClass was just fixed.
    FailureReasonPVCBindTimeout FailureReason = "PVCBindTimeout"

    // PendingTimeout: workspace stuck in Pending past
    // pendingPhaseTimeout for non-PVC reasons (e.g. K8s API outage).
    // Recoverable via retry once the upstream issue is fixed.
    FailureReasonPendingTimeout FailureReason = "PendingTimeout"

    // TooManyFailures: Change B's exponential backoff exhausted
    // MaxConsecutiveFailures. Always retriable — the cap is just a
    // budget cap, not a "this is permanently broken" signal.
    FailureReasonTooManyFailures FailureReason = "TooManyFailures"
)
```

Replace the 6 free-form `Status.Message =` assignments with structured:

```go
workspace.Status.Phase = v1.WorkspacePhaseFailed
workspace.Status.FailureReason = v1.FailureReasonPVCBindTimeout
workspace.Status.Message = "PVC %q has not bound for %s; check StorageClass %q"
```

The `Message` becomes a human-readable explanation; `FailureReason` is the machine-readable contract.

### Per-cause retry policy

| FailureReason | Auto-retry (Change B)? | User-driven retry (Change A)? | Notes |
|---|---|---|---|
| TransientPodLoss | yes (default schedule) | yes | the worklog 0099 case |
| PodFailedDuringCreation | yes (3 attempts max) | yes | image-pull-retry territory |
| PodBuildFailed | **no** | yes (only after spec fix) | spec is wrong; auto-retry would loop |
| PVCBindTimeout | **no** | yes (only after SC fix) | infrastructure problem |
| PendingTimeout | yes (longer backoff) | yes | usually external outage |
| TooManyFailures | **no** | yes | already retried automatically |

### Frontend implications

The chat UI's workspace status badge can now show:

- 🟡 "Retrying… (attempt 3 of 8, next try in 5m)" — `Phase=Creating, ConsecutiveFailures=3`
- 🔴 "Container image is invalid" + spec-edit link — `FailureReason=PodBuildFailed`
- 🔴 "Storage class problem — contact admin" — `FailureReason=PVCBindTimeout`
- 🔵 "Retry" button — any non-Terminating phase

This is much better UX than today's binary "Failed (manual intervention required)."

### Acceptance criteria

- [ ] **Unit:** Each of the 6 sites that flip to Failed sets the matching `FailureReason`.
- [ ] **Unit:** `Status.Message` formats vary by reason; all are non-empty.
- [ ] **Unit:** Change B's auto-retry honors the policy table — `PodBuildFailed` does NOT auto-retry, `TransientPodLoss` does.
- [ ] **API:** `GET /api/v1/workspaces/:id/status` returns `failureReason` alongside `phase` and `message`.
- [ ] **Frontend:** badge displays per-reason copy + correct affordance (retry button visible/hidden, spec-edit link for `PodBuildFailed`).
- [ ] **No regression** in existing tests; the `Message` text may change but `Phase=Failed` semantics preserved.

### Files to modify

- `pkg/apis/llmsafespace/v1/workspace_types.go` — `FailureReason` type, status field
- `controller/internal/workspace/controller.go` — 6 sites
- `api/internal/services/workspace/workspace_service.go` — surface the reason in `GetWorkspaceStatus`
- `pkg/types/workspace.go` (api types) — add `FailureReason` to `WorkspaceStatusResult`
- `frontend/src/api/types.ts` — extend the type
- `frontend/src/components/workspace/WorkspaceStatusBadge.tsx` (or equivalent) — per-reason rendering
- `controller/internal/workspace/controller_test.go` — 6 cause-specific tests
- `frontend/test/…` — UI rendering tests for each reason

### Risk assessment

- **Surface area** — touches API types, frontend, all six failure sites. Largest change in this epic.
- **Backwards compat** — pre-existing `FailureReason=""` rows from before this epic must be tolerated by the API and rendered as a neutral "failed" badge by the frontend (test required).
- **Frontend coupling** — deciding the exact UI affordances is a product question; the engineering work should pre-bake hooks but not lock in copy/visuals.

---

## Out of scope for Epic 21

- agentd performance under SSE load (the underlying reason `/v1/statusz` starves). Fixing that would reduce Failed-rate even before Changes B + C take effect, but it's an agentd-internal concern (probably needs goroutine isolation for the health endpoint) and warrants its own epic.
- Workspace migration / hot-recovery to a different node. Would require more invasive PVC handling.
- Cross-workspace blast-radius caps (e.g. "if 50% of workspaces are flapping, slow down all retries").

---

## Sequencing recommendation

Do **Change B first** (it's the bigger UX win — most failures auto-recover without operator awareness), then **Change C** (which mostly improves observability and frontend copy on top of B). Each is its own user story; both depend on Change A which is already shipped.

| Story | Title | Order |
|---|---|---|
| US-21.1 | Add `ConsecutiveFailures` / `NextRetryAt` / `LastSuccessAt` to status, deepcopy regen | 1 |
| US-21.2 | Implement backoff in `recoverFromTransientPodLoss` + reset in `maybeResetTransientCounter` | 2 |
| US-21.3 | Honor `NextRetryAt` in `handleCreating`/`handleActive`; integration tests | 3 |
| US-21.4 | `MaxConsecutiveFailures` cap → Failed transition; metric for backoff state | 4 |
| US-21.5 | Add `FailureReason` enum + status field; deepcopy regen | 5 |
| US-21.6 | Wire `FailureReason` into all 6 Failed-transition sites | 6 |
| US-21.7 | Surface `FailureReason` in API status endpoint | 7 |
| US-21.8 | Frontend badge: per-reason copy + retry button visibility | 8 |
| US-21.9 | E2E: kill pod → backoff → success → reset → repeat fail → terminal cap | 9 |
