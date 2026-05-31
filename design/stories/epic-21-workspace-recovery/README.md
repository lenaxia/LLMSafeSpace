# Epic 21: Workspace Failure-Mode Robustness — State Machine

**Status:** Planning
**Created:** 2026-05-30 · **Last revised:** 2026-05-31 (audit pass 2)
**Priority:** Medium-High
**Depends on:** Worklog 0100 (Change A — declarative recovery from `Failed` via `spec.restartGeneration`, shipped); Epic 23 Story 1 (DeletionTimestamp guard) MUST land before any Change B story.
**Blocks:** none
**Related epics:**
- **Epic 22** — agentd health endpoint redesign (the root cause of the rate at which workspaces enter Failed). Should land before Epic 21 to remove the first-order driver of failures.
- **Epic 23** — Controller race hardening. Story 1 (DeletionTimestamp + heavy instrumentation) ships first; Stories 2 and 3 can land alongside or after Epic 21.
- **Reserved:** Epic 19, Epic 20 are reserved for unrelated work.

This epic owns the **state-machine semantics** of workspace failure: when the controller transitions a workspace to `Failed`, what counts as transient vs. permanent, and what users / operators / the controller itself can do about it. Concurrency correctness (Epic 23) and agentd performance (Epic 22) are necessary partners but live in their own epics.

---

## Stated assumptions (each validated below)

The design rests on the following assumptions. Each is verified in the next section.

| # | Assumption | Type |
|---|---|---|
| A1 | The controller's reconcile is serialized per-workspace (no concurrent reconciles of the same Workspace) | Code-verified |
| A2 | Today's `MaxTransientFailures = 3` produces terminal Failed within ~45 seconds of sustained transient failures, given `healthCheckInterval = 15s` and `healthHTTPClient.Timeout = 5s` | Code-verified, computed |
| A3 | Each workspace pod restart cycle (delete pod → create new pod → reach `PodRunning` + `healthCheckGracePeriod`) costs at least 30 seconds of unavailability before the new pod can be probed | Code-verified |
| A4 | `WorkspaceStatus` has fields the controller writes; `LastActivityAt` is the only field the API service writes today (besides `Phase` for explicit Suspend/Resume) | Code-verified |
| A5 | `spec.restartGeneration` is read by `handleActive` (controller.go:258) and `handleFailed` (controller.go:84 — Change A); both transition to a fresh creating cycle | Code-verified |
| A6 | The five Failed-write sites have semantically distinct causes; no taxonomy field today | Code-verified |
| A7 | No production users today; CRD field additions are NOT bound by backwards-compatibility constraints. Old fields may be deleted outright; absent migration paths are acceptable | Stated by user, this session |
| A8 | The cluster runs at most one workspace controller leader (controller-runtime leader election ensures only one leader writes status at a time) | Code-verified — implicit in `ctrl.NewControllerManagedBy(mgr)` defaults |

Hypotheses considered and refuted:

- **R1**: "A `TransientFailureCount > 0` check would prevent the dying-pod misclassification." Refuted (worklog 0100): `ConsecutiveHealthFailures` was reset to 0 at controller.go:1027 *before* `handleCreating` observed the dying pod. The right discriminator is `pod.DeletionTimestamp` (Epic 23 Story 1).

---

## Verified ground truth

| # | Validates | Fact | How verified |
|---|---|---|---|
| F1 | A1, A8 | `MaxConcurrentReconciles` defaults to 1 in controller-runtime; `SetupWithManager` sets no override | `controller.go:902-906` — no `WithOptions` clause; default applies |
| F2 | A2 | `healthCheckInterval = 15 * time.Second`, `healthCheckFailureThreshold = int32(3)`, `healthHTTPClient.Timeout = 5 * time.Second` | `controller.go:952, 954, 959` |
| F3 | A2 | Three consecutive failures take ≥ 45 seconds (3 × 15s rate-limit between probes) plus up to 5s for the third probe to time out | Computed from F2 |
| F4 | A3 | `healthCheckGracePeriod = 30 * time.Second` — no health checks fire for the first 30s after pod start | `controller.go:955, 962-963` |
| F5 | A3 | Pod-restart cycle: `r.deletePodByName` (immediate K8s API call) → kubelet termination grace period (default 30s) → `r.Create` new pod → kubelet pulls image / starts container → `PodRunning` → 30s grace before health checks resume. Empirically observed at 30-60s from worklog 0100 incident timeline | Pod events (`kubectl get events`) + code paths in `handleCreating`, `checkAgentHealth` |
| F6 | A4 | Status writers: controller writes 23 fields; `api/internal/handlers/activity.go:121` writes `Status.LastActivityAt`; `api/internal/services/workspace/workspace_service.go:400, 446` write `Status.Phase` for Suspend/Resume | `grep -rn "Status\." --include="*.go"` in controller and api packages |
| F7 | A5 | Change A handler at `controller.go:84-99` reads `spec.RestartGeneration > status.ObservedRestartGeneration`; transitions to `Pending` and clears stale fields | Worklog 0100 + current source |
| F8 | A6 | Five Failed-write sites with distinct messages: controller.go:138, 169, 203, 244, 467 | `grep "Status.Phase = v1.WorkspacePhaseFailed" controller.go` |

---

## Problem Statement

### What Change A solved (worklog 0100)

A workspace stuck in `Failed` is now recoverable by bumping `spec.restartGeneration`. The controller's `handleFailed` honors the bump and walks the workspace back through `Pending`. The frontend can ship a "Retry" button. Hand-edits of `status.phase` are no longer required.

### What Change A did NOT solve

Workspaces still enter `Failed` too eagerly. The state machine has five distinct paths into `Failed` and they're treated identically — same handler, same recovery affordance, same UX. They have very different semantics:

| # | Site (controller.go:line) | Trigger | Recoverable how? |
|---|---|---|---|
| 1 | 138 | PVC could not be created within `pendingPhaseTimeout` (5 min) | Retry once K8s API is healthy. No human intervention typically needed. |
| 2 | 169 | PVC stuck non-bound for 5 min (immediate-binding storage class) | Storage admin must fix the StorageClass. Auto-retry is wasted budget. |
| 3 | 203 | `buildPod()` returned an error | Operator must fix the spec (bad runtime image, missing secret). Auto-retry will loop. |
| 4 | 244 | Pod observed in K8s phase=Failed during `handleCreating` | **Often a controller bug** (Epic 23 Story 1: dying pod misclassified). Otherwise transient — image pull retry, node draining. |
| 5 | 467 | `recoverFromTransientPodLoss` exhausted `MaxTransientFailures = 3` | Almost always transient — the underlying flake recovered N seconds later. The 3-strike count, combined with a 15s rate-limit, gives a workspace ≥ 45s to demonstrate it's broken before being killed. |

The verified incident from worklogs 0099/0100 hit path #4 via path #5's underlying mechanism: agentd `/v1/statusz` timed out 3× under SSE load → controller deleted the pod → the dying pod showed K8s phase=Failed for a moment → `handleCreating:244` interpreted it as a permanent failure. **Two distinct bugs compounded.** Epic 23 Story 1 fixes path #4's misclassification. Epic 21 (this one) fixes path #5's overly-eager give-up.

### Why this hurts

Today the only signal a user sees is "Failed (manual intervention required)" in the UI. They can't tell:

- Did the cluster have a 60-second blip? (transient, will probably recover on retry)
- Did the user mistype an image name? (permanent, retry will fail again until spec is fixed)
- Is storage broken cluster-wide? (operator concern, retry is pointless)
- Is the workspace actually fine and just having a bad health-check minute? (will self-heal — please don't burn its retry budget)

The controller can answer all of these but currently throws away the information. Operators can't write reason-based alerts. Frontend can't show meaningful copy.

---

## Scope

**This epic owns:**

- **Change B** — Replace the 3-strike hard-count for transient pod loss with exponential backoff and a stability-based reset window. Reduces eager terminal-Failed transitions while still catching genuinely-broken workspaces.
- **Change C** — Introduce a typed `FailureReason` enum on `WorkspaceStatus`. Wire it into all 5 Failed-write sites with per-cause retry policy.

**This epic does NOT own (but depends on):**

- **Race hardening** (Epic 23). Without Epic 23 Story 1 (`pod.DeletionTimestamp` check), Epic 21's Change B reduces but does not eliminate the worklog-0100 failure pattern. **Story 1 of Epic 23 ships before any Epic 21 story begins.**
- **agentd health-endpoint redesign** (Epic 22). Without Epic 22, agentd health endpoints keep starving under SSE load and Change B's backoff merely buffers the symptom. Epic 22 should land before Epic 21 because it removes the first-order driver of failures.

---

## Robustness criteria — what "done" looks like

Epic 21 is done when **all** of the following hold. These are the invariants the design must enforce; the test plan exists to prove them.

1. **A 60-second cluster-wide agentd flake does not transition any workspace to terminal `Failed`.** With `healthCheckInterval = 15s` and `healthHTTPClient.Timeout = 5s`, 60s of slowness produces 4 consecutive failures. Change B's backoff schedule must absorb these without exceeding the cap.
2. **A workspace that runs `Active` continuously for ≥ 5 minutes resets `ConsecutiveFailures` to 0.** Subsequent independent failures don't compound with old ones from hours earlier. The 5-minute window matches the existing `TransientFailureResetWindow = 5 * 60` constant.
3. **A genuinely-broken workspace (e.g. invalid image name) terminates without infinite retry.** The cap exists; reaching it transitions to terminal `Failed` with `FailureReason = TooManyFailures`.
4. **Every Failed transition carries a `FailureReason` enum value.** Operators and frontend can switch on it without parsing free-form messages.
5. **`PodBuildFailed` and `PVCBindTimeout` do not auto-retry.** Both are spec/infra problems; auto-retry is wasted budget. Manual `RestartGeneration` bump still works for both (Change A semantics preserved).
6. **`TransientPodLoss`, `PodFailedDuringCreation`, and `PendingTimeout` auto-retry per their backoff schedules.** All three are likely-transient.
7. **No regression to the Active / Suspended / Resuming / Terminating happy paths.** The existing 38-test controller suite must remain green.
8. **`spec.restartGeneration` bump from any non-terminal phase still works, including from a workspace mid-backoff.** Change A semantics preserved; backoff state is cleared on bump.
9. **Metrics expose the failure budget state.** Operators can alert on workspaces approaching their cap before they hit it. Schema defined in Epic 23 Story 1; consumed by Epic 21.

---

## Change B — Exponential backoff with stability-reset

### What's wrong today (re-stated precisely)

`recoverFromTransientPodLoss` (controller.go:456-477) does:

```
TransientFailureCount++
if TransientFailureCount >= MaxTransientFailures (3):
    Phase = Failed
else:
    Phase = Creating
```

A 60-second agentd flake → 4 consecutive timeouts → `TransientFailureCount` goes 1, 2, 3 → terminal Failed. Each transition between Creating and Active during that window also costs ~30-60s of pod-restart latency (F5), so the total wall-clock from "first slowness" to "terminal Failed" is ~90 seconds.

The design problem is binary: 3 failures = terminal. There's no notion of "wait longer between retries" or "the workspace was healthy 4 hours ago, this single new failure shouldn't count toward the same budget."

### Proposed mechanism

**New status fields:**

```go
// WorkspaceStatus additions for Epic 21 Change B.
ConsecutiveFailures int32        `json:"consecutiveFailures,omitempty"`
NextRetryAt        *metav1.Time `json:"nextRetryAt,omitempty"`
LastSuccessAt      *metav1.Time `json:"lastSuccessAt,omitempty"`
```

`TransientFailureCount` and `LastTransientFailureAt` are **removed outright** in US-21.1 (per A7, no BC required). All consumers — `recoverFromTransientPodLoss`, `maybeResetTransientCounter`, the Change A handler at controller.go:84-99 (handleFailed branch), and the Active-branch RestartGeneration handler at controller.go:258-266 — switch to the new fields in the same commit cycle.

**Backoff schedule (constants in `constants.go`):**

```go
var BackoffSchedule = []time.Duration{
    5 * time.Second,    // attempt 1 → wait 5s before retry
    30 * time.Second,   // attempt 2 → wait 30s
    2 * time.Minute,    // attempt 3
    5 * time.Minute,    // attempt 4
    15 * time.Minute,   // attempt 5
    1 * time.Hour,      // attempt 6
    4 * time.Hour,      // attempt 7
    12 * time.Hour,     // attempt 8
}
const MaxConsecutiveFailures = 8  // 9th failure → terminal
```

| Attempt | Wait | Cumulative wait | + ~30-60s pod-restart per attempt | Total wall-clock to attempt N |
|---|---|---|---|---|
| 1 | 5s | 5s | + 30s | ≥ 35s |
| 2 | 30s | 35s | + 30s | ≥ 95s |
| 3 | 2m | 155s | + 30s | ≥ 3m 5s |
| 4 | 5m | ~8m | + 30s | ≥ 8m 35s |
| 5 | 15m | ~23m | + 30s | ≥ 23m 35s |
| 6 | 1h | ~83m | + 30s | ≥ 1h 23m |
| 7 | 4h | ~5h 23m | + 30s | ≥ 5h 24m |
| 8 | 12h | ~17h | + 30s | ≥ 17h |
| 9 | terminal `Failed` (`FailureReason = TooManyFailures`) | n/a | n/a | n/a |

**Justification for the schedule:**

- A 60-second agentd flake (the verified incident class) produces ~4 health-check failures. Under the new schedule, attempts 1+2 absorb 35s of backoff; the workspace is restarted twice. Attempt 3 starts after 95 seconds total. The 60s flake has long since ended; the workspace recovers. Today's design would have terminal-Failed it after ~45 seconds of probe-time plus ~45 seconds of restart latency.
- A 10-minute outage (e.g. cluster-wide kubelet issue) is comfortably absorbed: by t=10min, the workspace is in attempt 4's backoff window (cumulative wait through attempt 4 is ~8min; attempt 5's wait runs from ~8min to ~23min). When opencode recovers, the next attempt succeeds without escalating to terminal. Total pod restarts: 4 (one per failed attempt before recovery), ~2-3 minutes of cumulative restart latency. At most one user-visible Failed-phase blip if any (only if Story 1 doesn't deduplicate the dying-pod cases first).
- A 24-hour broken workspace (e.g. invalid image) terminates after 17 hours of escalating retries. The cumulative cost is 8 pod restarts spread across the day, capped.

**Reset rule:** the new field `LastSuccessAt` is set to `metav1.Now()` every time the controller observes the workspace in `PodRunning` state with a non-empty `PodIP` (i.e. each successful `handleActive` reconcile that passes the early-return guards). The reset condition is:

```go
if workspace.Status.LastSuccessAt != nil &&
   time.Since(workspace.Status.LastSuccessAt.Time) >= TransientFailureResetWindow {
    workspace.Status.ConsecutiveFailures = 0
    workspace.Status.NextRetryAt = nil
}
```

This reuses the existing `TransientFailureResetWindow = 5 * 60` constant (controller.go:486-488 today). No new constant. Implementation lives in `maybeResetTransientCounter` (existing function, repurposed to also reset Change B's fields).

**Reconcile honors `NextRetryAt`:** in `handleCreating` and `handleActive`, before any pod-state-checking work:

```go
if workspace.Status.NextRetryAt != nil {
    if time.Now().Before(workspace.Status.NextRetryAt.Time) {
        return ctrl.Result{RequeueAfter: time.Until(workspace.Status.NextRetryAt.Time)}, nil
    }
    // Backoff window elapsed; clear NextRetryAt and proceed.
    workspace.Status.NextRetryAt = nil
    if err := r.Status().Update(ctx, workspace); err != nil {
        return ctrl.Result{}, err
    }
}
```

The clear-on-elapse means a single reconcile both expires the backoff and proceeds with the next attempt.

**Initial value semantics:** a freshly-created workspace has `ConsecutiveFailures = 0`, `NextRetryAt = nil`, `LastSuccessAt = nil`. All three fields are `omitempty` in JSON. Zero values mean "no failures yet." The controller never explicitly initializes them at creation time.

**Interaction with Change A (`spec.restartGeneration`):** if the operator bumps the gen field while the workspace is mid-backoff (Phase=Creating, NextRetryAt set), the bump must immediately end the backoff. The Change A handler at `controller.go:84-99` (Failed branch) already clears stale fields; extend it (and the Active branch at `controller.go:258-266`) to also clear `ConsecutiveFailures = 0`, `NextRetryAt = nil`, `LastSuccessAt = nil`. Test US-21.5 covers this.

**Interaction with Change C:** when the cap is reached, `recoverFromTransientPodLoss` at line 467 (refactored to call Change C's `markFailed` helper) sets `FailureReason = TooManyFailures`. Today's free-form `"pod lost N times; marking failed"` becomes a structured field plus a human message.

### Files modified

- `pkg/apis/llmsafespace/v1/workspace_types.go` — 3 new status fields (Change B); 1 new field (Change C: `FailureReason`)
- `pkg/apis/llmsafespace/v1/zz_generated.deepcopy.go` — regenerated via `make deepcopy`
- `controller/internal/workspace/constants.go` — new `BackoffSchedule []time.Duration` and `MaxConsecutiveFailures = 8`
- `controller/internal/workspace/controller.go` — `recoverFromTransientPodLoss`, `maybeResetTransientCounter`, `handleCreating`, `handleActive`, plus the Change A handler for backoff-clearing
- `controller/internal/workspace/controller_test.go` — table-driven backoff tests, reset tests, RestartGeneration-during-backoff tests

### Edge cases

- **Controller restart mid-backoff.** `NextRetryAt` is in the CRD. Replacement controller picks up the schedule deterministically. Test: kill controller pod, observe workspace, restart controller, assert NextRetryAt honored. (envtest scenario.)
- **Clock skew between controller pods.** Per A8, only the leader writes; controller-runtime ensures single-writer via leader election. `metav1.Now()` is the leader's local clock; clock drift across leader transitions could cause one extra requeue but no correctness issue.
- **Backoff exceeds `spec.timeout`.** `spec.timeout` is enforced in `handleActive` at controller.go:311-318 only when the workspace is Active and `StartTime` is set. During backoff the workspace is in `Creating` with no live `StartTime` (cleared on transition). So `spec.timeout` does NOT terminate a backing-off workspace. **Documented behavior:** Change B may keep a workspace alive past its `spec.timeout` boundary if it's stuck retrying. This is intentional — `spec.timeout` is a per-Active-session deadline, not a workspace-lifetime deadline.
- **Pre-existing CRD with no Change B fields (per A7+#8):** zero values cause the controller to treat the workspace as "no failures yet, no backoff active." First failure goes through the new path normally.

### Story breakdown — Change B

| Story | Title | Depends on | Acceptance criteria summary |
|---|---|---|---|
| US-21.1 | Add `ConsecutiveFailures`, `NextRetryAt`, `LastSuccessAt` to `WorkspaceStatus`; remove `TransientFailureCount` and `LastTransientFailureAt`; regenerate deepcopy | none | New fields present, old fields gone, JSON serialization round-trips, `make deepcopy` regen passes, all consumers compile and pass tests |
| US-21.2 | Implement backoff in `recoverFromTransientPodLoss` | US-21.1 | Schedule from constants; ConsecutiveFailures increments; NextRetryAt set per attempt |
| US-21.3 | Honor `NextRetryAt` in `handleCreating` / `handleActive` | US-21.1 | Reconcile during backoff returns `RequeueAfter`, calls neither `r.Get(pod)` nor `r.Create(pod)`; clears NextRetryAt when elapsed |
| US-21.4 | Stability-reset in `maybeResetTransientCounter` | US-21.1, US-21.3 | After 5min Active, ConsecutiveFailures resets to 0; envtest scenario |
| US-21.5 | RestartGeneration during backoff clears state | US-21.1, US-21.3, Change A handler at controller.go:84 | Bump from Creating-with-NextRetryAt → all 3 fields cleared, transitions to Pending |
| US-21.6 | Cap at attempt 9 → terminal Failed with `FailureReason = TooManyFailures` | US-21.1, US-21.2, US-21.7, US-21.8 | Attempt 9 invocation calls `markFailed(ws, FailureReasonTooManyFailures, "...")`; FailureReason set correctly; transitions to Failed |

---

## Change C — Failure-cause taxonomy

### Mechanism

Add a typed `FailureReason` enum to `WorkspaceStatus`:

```go
type FailureReason string

const (
    FailureReasonNone                    FailureReason = ""                       // not Failed
    FailureReasonTransientPodLoss        FailureReason = "TransientPodLoss"
    FailureReasonPodFailedDuringCreation FailureReason = "PodFailedDuringCreation"
    FailureReasonPodBuildFailed          FailureReason = "PodBuildFailed"
    FailureReasonPVCBindTimeout          FailureReason = "PVCBindTimeout"
    FailureReasonPendingTimeout          FailureReason = "PendingTimeout"
    FailureReasonTooManyFailures         FailureReason = "TooManyFailures"
)
```

**Status field:**

```go
// WorkspaceStatus additions for Epic 21 Change C.
FailureReason FailureReason `json:"failureReason,omitempty"`
```

**Per-cause retry policy:**

| Reason | Auto-retry (Change B)? | User-driven retry (Change A)? | Notes |
|---|---|---|---|
| TransientPodLoss | yes — full schedule (8 attempts) | yes | The 07:43:00-class incident |
| PodFailedDuringCreation | yes — capped at 3 attempts | yes | After Epic 23 Story 1 lands, the dying-pod misclassification is gone; remaining triggers are real (image pull retry, OOM during init). 3-attempt cap is tighter than transient loss because real causes that survive 3 retries are rarely transient. |
| PodBuildFailed | **no** — spec is wrong, auto-retry loops | yes (only after spec fix) | Frontend should surface the buildErr message verbatim |
| PVCBindTimeout | **no** — infrastructure problem | yes (only after StorageClass fix) | Frontend should hint at admin contact |
| PendingTimeout | yes — longer initial backoff: 30s, 5m, 30m, 2h, 12h (5 attempts; then terminal) | yes | Usually upstream K8s API outage; faster-ramp schedule wastes work if the API is genuinely down |
| TooManyFailures | **no** — already retried automatically | yes | Means the auto-retry budget exhausted; manual restart starts from a clean slate |

The three distinct backoff schedules (8-attempt for `TransientPodLoss`, 3-attempt for `PodFailedDuringCreation`, 5-attempt for `PendingTimeout`) are encoded in a `BackoffPolicyForReason(FailureReason)` helper. `PodBuildFailed`, `PVCBindTimeout`, `TooManyFailures` return a sentinel "no auto-retry" policy.

### Wire format

The 5 Failed-write sites are refactored to call a single helper:

```go
func (r *WorkspaceReconciler) markFailed(ws *v1.Workspace, reason v1.FailureReason, format string, args ...any) {
    ws.Status.Phase = v1.WorkspacePhaseFailed
    ws.Status.FailureReason = reason
    ws.Status.Message = fmt.Sprintf(format, args...)
}
```

Each site provides the appropriate reason. Free-form `Message` text becomes a human-readable explanation; programmatic consumers use `FailureReason`.

| Today's site | Reason after Change C |
|---|---|
| controller.go:138 ("workspace timed out in Pending phase") | `FailureReasonPendingTimeout` |
| controller.go:169 ("PVC not bound after timeout") | `FailureReasonPVCBindTimeout` |
| controller.go:203 ("pod build failed: ...") | `FailureReasonPodBuildFailed` |
| controller.go:244 ("pod entered Failed phase during creation") | `FailureReasonPodFailedDuringCreation` (after Epic 23 Story 1 has eliminated the dying-pod misclassification) |
| controller.go:467 ("pod lost N times; marking failed") | `FailureReasonTooManyFailures` |

### API surface

`GET /api/v1/workspaces/:id/status` (existing endpoint at `workspace_service.go:455-…`) extends `WorkspaceStatusResult` with `FailureReason` field. Frontend receives both `phase` and `failureReason`.

### Frontend implications (separate epic, not Epic 21)

| `phase=Failed`, `failureReason=…` | Badge | Affordance |
|---|---|---|
| `TransientPodLoss` | 🟡 "Recovering…" | hide retry button (auto-retry in progress, show countdown to NextRetryAt) |
| `PodBuildFailed` | 🔴 "Container image is invalid" | spec-edit link + retry button |
| `PVCBindTimeout` | 🔴 "Storage problem" | "Contact admin" hint + retry button |
| `TooManyFailures` | 🔴 "Repeatedly failing" | retry button starts from a clean slate |
| (none), `phase=Active` | 🟢 normal | n/a |
| (none), `phase=Pending` with `NextRetryAt` set | 🟡 "Waiting…" with mm:ss countdown | none (controller is retrying) |

### Story breakdown — Change C

| Story | Title | Depends on | Acceptance criteria summary |
|---|---|---|---|
| US-21.7 | Add `FailureReason` enum + status field; deepcopy regen | none | Field serializes; controller-test fixtures populate it |
| US-21.8 | `markFailed` helper + wire all 5 Failed-write sites | US-21.7 | Each Failed message in tests asserts the matching enum value |
| US-21.9 | `BackoffPolicyForReason` helper + per-cause retry policy in Change B | US-21.2, US-21.7 | Table-driven: PodBuildFailed must NOT auto-retry; TransientPodLoss MUST; each policy tested |
| US-21.10 | Surface `FailureReason` in API status endpoint + types | US-21.7 | API contract test: `GET /workspaces/:id/status` returns the field |

(Frontend stories US-21.11+ are deferred to a separate frontend epic per scope decision.)

---

## Test plan

### Unit tests (controller_test.go, fake-client)

The existing 38 controller tests stay green. New tests:

- **Change B backoff:** table-driven over the schedule; assert each attempt N sets `NextRetryAt = Now + BackoffSchedule[N-1]`. Includes "9th attempt → terminal Failed" case (US-21.6).
- **Stability reset:** workspace fails twice → reaches Active → simulate `LastSuccessAt = Now() - 6 minutes` → fails again. Assert `ConsecutiveFailures = 1` (not 3) at the second failure. Uses `metav1.Now` overridden via test fixture.
- **NextRetryAt honored:** reconcile during backoff returns `RequeueAfter > 0`; mock-call assertions verify `r.Get(pod)` and `r.Create(pod)` are NOT called.
- **NextRetryAt elapsed → cleared:** reconcile after backoff window has expired clears `NextRetryAt` and proceeds with normal flow.
- **RestartGeneration during backoff:** workspace mid-backoff, operator bumps gen → all 3 backoff fields cleared, phase transitions per Change A.
- **Change C reasons:** for each of the 5 Failed-write sites, assert the matching `FailureReason` enum is set.
- **Per-cause policy:** PodBuildFailed must NOT auto-retry (`ConsecutiveFailures` does not increment, `NextRetryAt` not set). PVCBindTimeout same. TransientPodLoss must auto-retry per the 8-attempt schedule. PodFailedDuringCreation must auto-retry per the 3-attempt schedule.
- **API contract:** `GET /workspaces/:id/status` returns `failureReason`.
- **Pre-existing CRD compat:** synthesize a Workspace JSON without Change B/C fields; reconcile; assert no panic; first failure path increments correctly.

### Integration tests (envtest, real kube-apiserver+etcd)

This is new infrastructure for the project. Setup ~half day; pays back across this epic and Epic 23.

- **Real K8s pod transitions:** create a pod via test, manipulate its `Status.Phase` directly via the API, observe controller behavior. Catches the bugs the fake-client can't.
- **Cache lag:** force the informer cache to be stale by writing directly to etcd while the controller's local cache holds an older view. Assert no "object has been modified" panics; controller retries (Epic 23 Story 2 territory; envtest infrastructure shared).
- **Concurrent writer:** simulate the API service's `Status.LastActivityAt` write between the controller's Get and Update. Assert controller resolves the conflict.
- **Backoff under stress:** kill the pod 5 times in tight succession; assert backoff schedule observed; assert no terminal Failed before the cap.
- **Stability reset end-to-end:** Failed → recover → Active 6 minutes → Failed again. Assert second failure starts from `ConsecutiveFailures = 1`.
- **Controller restart mid-backoff:** kill controller pod with workspace in backoff; bring up replacement; assert `NextRetryAt` is honored on resume.

### Chaos / failure-injection tests

- **agentd flaps:** a fake agentd that returns 500 on every 3rd `/v1/statusz` request. Workspace must NOT transition to terminal Failed. (Layered defense with Epic 22.)
- **Pod eviction during backoff:** kubelet evicts the pod while controller is mid-backoff. Reconcile correctly recovers (treats as transient pod loss, increments counter, schedules next retry).
- **Controller crash mid-update:** controller pod killed between in-memory mutation and `Status().Update`. Replacement controller observes the pre-update state and re-runs; no double-count of failures.

### Observability tests

- Metrics emitted at the right transitions (these are Epic 23 Story 1's metrics; Epic 21 just consumes the same schema).
- Test asserts `WorkspaceConsecutiveFailures` gauge tracks the new field.
- Test asserts `WorkspaceTerminalFailuresTotal{reason="TooManyFailures"}` increments on cap.

---

## Sequencing

Strict dependency order:

1. **Epic 23 Story 1 (D-narrow + heavy instrumentation)** — must land before Epic 21 starts. The DeletionTimestamp check eliminates the dying-pod misclassification that today contaminates the failure-counter signal.
2. **Epic 22 Stories 1-4** (agentd health endpoint redesign) — strongly recommended to land before Epic 21. Removes the first-order driver of the failures Change B is meant to absorb.
3. **Epic 21 US-21.1** (status fields + deepcopy) — independent.
4. **Epic 21 US-21.7** (FailureReason enum + status field) — independent.
5. **Epic 21 US-21.2, US-21.3, US-21.4** (Change B core) — depend on US-21.1.
6. **Epic 21 US-21.8** (markFailed helper + wire 5 sites) — depends on US-21.7.
7. **Epic 21 US-21.5** (RestartGeneration during backoff) — depends on US-21.3.
8. **Epic 21 US-21.9** (per-cause retry policy) — depends on US-21.2 + US-21.7.
9. **Epic 21 US-21.6** (cap → TooManyFailures) — depends on US-21.2 + US-21.7 + US-21.8.
10. **Epic 21 US-21.10** (API surface) — depends on US-21.7.
11. **Epic 23 Stories 2 + 3** — independent of Epic 21. Land alongside or after.

---

## Out of scope (called out for clarity)

- agentd `/v1/healthz`, `/v1/readyz`, `/v1/statusz` redesign → **Epic 22**.
- DeletionTimestamp check, retry-on-conflict, single-writer audit → **Epic 23**.
- Workspace migration / live-recovery to a different node.
- Cross-workspace blast-radius caps.
- Frontend per-reason UX rendering (depends on Change C landing; tracked in a separate frontend epic).

---

## Open questions for review

1. **Backoff schedule.** Is 5s/30s/2m/5m/15m/1h/4h/12h the right shape? Justification given but not validated by production data. Epic 23 Story 1's metrics will provide that data once deployed.
2. **`PendingTimeout`'s separate schedule.** 30s/5m/30m/2h/12h proposed (5 attempts). Right number of attempts? Right ramp?
3. **`spec.maxRetries`.** Today's per-workspace tunable is conflated with the 3-strike scheme. **Recommendation: remove for Change B; reintroduce per-workspace tunability later only if a user requests it.** Simpler, fewer test combinations.
