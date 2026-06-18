# Epic 23: Controller & API Race Hardening

**Status:** Stories 1, 3, 4 shipped. Story 2 helper shipped; 21-site migration deferred pending metric data.
**Note:** Stories 1 and 4 shipped in worklog 0105 (2026-05-31). Story 3 (single-writer migration: LastActivityAt → annotation, Suspend/Resume → `Spec.Suspend *bool` tri-state) shipped in worklog 0342 (2026-06-18). Story 3 uses `*bool` (not the `bool` originally specified) so that `nil` (unspecified/acknowledged) is distinguishable from `&false` (explicit resume request); the controller clears the pointer to nil after consuming a request. Story 2 helper (`updateStatusWithRetry`) shipped in worklog 0342; the 21-site migration to use it is deferred until `WorkspaceStatusUpdateConflictsTotal` shows residual conflict rate warrants it.
**Created:** 2026-05-31 · **Last revised:** 2026-05-31 (audit pass 3 — three-writer LastActivityAt reality, Story 3 before Story 2 sequencing, Option B annotation migration)
**Priority:** High (Story 1 is a hot-patch for the recurring incident; Story 4 is a hot-patch for browser-visible auth leak)
**Depends on:** none (Story 1 ships first; Stories 2-4 sequenced below)
**Related epics:**
- **Epic 21** — Workspace state machine. Epic 23 Story 1 must land BEFORE Epic 21's Change B; otherwise the dying-pod misclassification keeps poisoning Change B's failure budget.
- **Epic 22** — agentd `/v1/statusz` redesign. Independent of this epic.

This epic owns **concurrency correctness** in the controller's reconcile loop and the API's proxy-cache layer. Four classes of bugs:

- **Story 1 — DeletionTimestamp misclassification.** A pod the controller itself deleted is briefly observable in K8s phase=Failed during termination. `handleCreating` mistakes it for a genuine "pod failed during creation" and writes terminal `Failed`. Verified mechanism for the worklog 0100 incident; reproduced live on three workspaces (`c98963e7-…`, `6d36952e-…`, `8e80afc4-…`).
- **Story 3 — Single-writer migration.** Each `WorkspaceStatus` field has exactly one owner. `LastActivityAt` has THREE writers today (API ActivityTracker, API Resume, controller handleResuming). `Phase` has cross-writer behavior. Story 3 migrates `LastActivityAt` to a `metadata.annotations` patch (separate optimistic-concurrency lane from `Status().Update`) with the API service as sole writer; migrates Suspend/Resume to `Spec.Suspend bool` so controller is the sole writer of `Phase`.
- **Story 2 — `Status().Update` conflict retry.** After Story 3 eliminates cross-writer scenarios for the dangerous fields, the remaining conflicts come from controller-runtime informer-cache lag. Today the controller has no retry-on-conflict; the API service already has the pattern via `retry.RetryOnConflict` (`api/internal/handlers/activity.go:117`, `api/internal/services/workspace/workspace_service.go:1007`). This story propagates the existing pattern. **Sequenced AFTER Story 3** so the closure-rerun pattern can't resurrect lost-update state from a now-impossible cross-writer race.
- **Story 4 — API proxy auth-cache invalidation + response-header sanitization.** The API's `pwCache` is not invalidated when a workspace transitions to `Active` from a non-Active phase, so when `ensurePasswordSecret` regenerates the password (e.g., after Failed→recovery), the API forwards stale credentials to opencode and gets back a 401 with `WWW-Authenticate: Basic`. The proxy then forwards that header to the browser, prompting a basic-auth dialog. Reproduced live for user `mike@kao.family` while iterating on this design.

---

## Stated assumptions (each validated below)

| # | Assumption | Type |
|---|---|---|
| A1 | The pod that triggered the worklog 0100 terminal-Failed had `pod.DeletionTimestamp` set (the controller had just deleted it via `r.deletePodByName`) | Code-verified, K8s-spec-verified |
| A2 | controller-runtime's `MaxConcurrentReconciles` defaults to 1 per controller; concurrent reconciles of the same Workspace are impossible | Code-verified |
| A3 | At least three writers exist for `WorkspaceStatus`: the controller (Phase, 22 other fields, AND `LastActivityAt` via handleResuming), and the API service (`LastActivityAt` via ActivityTracker AND Resume; `Phase` via Suspend/Resume) | Code-verified |
| A4 | `WorkspaceStatus` has exactly 24 fields today; the controller writes 23 of them; `LastActivityAt` has THREE writers (controller handleResuming + API ActivityTracker + API Resume); `Phase` has cross-writer behavior (controller for most transitions, API for Suspend/Resume) | Code-verified |
| A5 | An existing `retry.RetryOnConflict` pattern is in use in this repo at `api/internal/handlers/activity.go:117` and `api/internal/services/workspace/workspace_service.go:1007`. Both wrap a Get-then-Update closure | Code-verified |
| A6 | The controller's `cleanupFailedWorkspaceSecrets` deletes `workspace-pw-<id>` Secret when a workspace enters `Failed` (Bug 12 fix from worklog 0085) | Code-verified |
| A7 | `ensurePasswordSecret` regenerates a fresh random password whenever the Secret is missing — i.e., on every Failed→Pending transition | Code-verified |
| A8 | The API proxy caches the password in-memory (`pwCache`) and only invalidates on `Suspending`/`Suspended`/`Terminating`/`Terminated` (NOT on `Failed`, NOT on `Active`-from-non-Active) | Code-verified |
| A9 | The API proxy's `doProxy` copies all upstream response headers verbatim to the client, including `WWW-Authenticate` | Code-verified |
| A10 | Browsers honor `WWW-Authenticate: Basic` headers from XHR/fetch responses by displaying a credential prompt unless `credentials: 'omit'` was set on the request | Web-spec-verified |
| A11 | An existing metrics package at `controller/internal/metrics/metrics.go` uses `prometheus.CounterVec` and `prometheus.HistogramVec` with `llmsafespace_*` naming and `reason` labels | Code-verified |
| A12 | `metadata.annotations` patches go through a separate optimistic-concurrency lane from `Status().Update`; an annotation Patch and a Status Update on the same object don't generate cross-conflict errors. This is the structural enabler for Story 3's "annotation as single-writer field" pattern | K8s-spec-verified — `Patch` on metadata uses the main resource subresource; `Status().Update` uses the `/status` subresource; resourceVersion is shared but conflicts are evaluated independently per write |

Hypotheses considered and refuted:

- **R1**: "Today the controller has NO retry-on-conflict pattern, so Story 2 must introduce a new utility." Refuted by A5: the existing `retry.RetryOnConflict` pattern in the API service is the prior art. Story 2 propagates it to the controller; it does not invent a new helper.
- **R2**: "`TransientFailureCount > 0` is the right discriminator for `handleCreating:249`." Refuted by worklog 0100: `ConsecutiveHealthFailures` was reset to 0 at controller.go:1030 *before* `handleCreating` observed the dying pod. The right discriminator is `pod.DeletionTimestamp != nil`.
- **R3**: "Story 3 needs to migrate ~16 controller-owned fields." Refuted by A4: only 2 fields need migration — `LastActivityAt` (move out of Status to annotation, single API writer) and `Phase` (suspend/resume migrate to spec). The other 22 fields are already controller-only and just need owner annotations in code.
- **R4**: "Story 2 (retry-on-conflict) is safe to ship before Story 3 (single-writer)." Refuted by audit pass 3: a retry-on-conflict closure that re-reads and re-applies the controller's mutation can resurrect a Phase value the API just wrote. Example: controller mid-reconcile is rewriting Phase=Active when API writes Phase=Failed; on conflict, controller's closure re-reads (Failed), re-applies its mutation (Phase=Active), and writes Active — losing the API's Failed. After Story 3, the API doesn't write Phase, so this race is structurally impossible. **Sequence Story 3 before Story 2.**

---

## Verified ground truth

| # | Validates | Fact | How verified |
|---|---|---|---|
| F1 | A1 | `handleCreating:249` writes terminal `Failed` when it observes a pod in K8s phase=Failed, with NO check for DeletionTimestamp | `controller.go:246-249`; `grep DeletionTimestamp controller/internal/workspace/` returns 0 hits in pod handling logic |
| F2 | A1 | The pod from `6d36952e-…`, `c98963e7-…`, and `8e80afc4-…` had a `Killing` event at the same moment as `checkAgentHealth`'s `Agent unreachable beyond threshold; restarting pod` log | `kubectl get events --field-selector involvedObject.kind=Pod` for each affected pod |
| F3 | A1 | Verified mechanism: `kubectl get workspace … -o jsonpath='{.status.message}'` returns the exact unique string `"pod entered Failed phase during creation"` for every reproduction | Live verification 3 times this session |
| F4 | A2 | `MaxConcurrentReconciles` defaults to 1; `SetupWithManager` sets no override | controller.go SetupWithManager — no `WithOptions` clause |
| F5 | A3, A4 | Three writers exist for `Status.LastActivityAt`: controller `controller.go:403`, API `activity.go:123`, API `workspace_service.go:448`. Two writers exist for `Status.Phase` from outside the controller's main reconcile: API `workspace_service.go:402` (Suspend), API `workspace_service.go:442` (Resume) | `grep -rn "Status\.LastActivityAt\s*=\|Status\.Phase\s*=" --include="*.go"` |
| F6 | A4 | Exact `WorkspaceStatus` fields (24 total): Phase, PVCName, ActiveSessions, LastActivityAt, SuspendedAt, Conditions, Message, ObservedGeneration, PodName, PodNamespace, PodIP, ImageTag, Endpoint, StartTime, RestartCount, TransientFailureCount, LastTransientFailureAt, ObservedRestartGeneration, CredentialSecretHash, LastHealthCheckAt, ConsecutiveHealthFailures, Sessions, DiskUsedBytes, DiskTotalBytes | `pkg/apis/llmsafespace/v1/workspace_types.go` |
| F7 | A5 | `retry.RetryOnConflict(retry.DefaultBackoff, ...)` is used in `activity.go:117` (writes LastActivityAt); `retry.RetryOnConflict(retry.DefaultRetry, ...)` is used in `workspace_service.go:1007` (rename) | `grep -rn "retry.RetryOnConflict" --include="*.go"` |
| F8 | A6 | `cleanupFailedWorkspaceSecrets` is called from the Failed branch of the main reconcile switch at `controller.go:74`; it deletes `workspace-pw-`, `workspace-creds-`, `workspace-secrets-` | `controller.go:68-105` |
| F9 | A7 | `ensurePasswordSecret` returns nil if the Secret already exists; otherwise generates a random password via `common.GenerateRandomString(32)` | controller.go (ensurePasswordSecret helper) |
| F10 | A8 | `pwCache` invalidation in `onPhaseChange`: `controller.go:708-712` covers Suspending/Suspended/Terminating/Terminated; the Active branch at line 715-718 ONLY invalidates `wsConfig`, not `pwCache`; `Failed` is not handled at all | `proxy.go:697-720` |
| F11 | A9 | `doProxy` copies upstream headers via `for k, vs := range resp.Header { c.Writer.Header().Add(k, v) }` at lines 454-458 (filtered path) and 465-469 (streamed path); no allow/deny list | `proxy.go:454-469` |
| F12 | A11 | Existing controller metrics use `prometheus.CounterVec` and `prometheus.HistogramVec` with `llmsafespace_*` naming and `reason` labels | `controller/internal/metrics/metrics.go` |

---

## Problem Statement

### Story 1's incident (verified)

At 2026-05-31 07:43:00, workspace `6d36952e-…` was Active. The controller's `checkAgentHealth` recorded its third consecutive `/v1/statusz` timeout, deleted the pod via `r.deletePodByName` (controller.go:1017), and set `Phase=Creating` in-memory. The deletion succeeded; the pod entered a terminating state with `DeletionTimestamp` set.

Within ~1 second the next reconcile fired. It read the workspace at `Phase=Creating` and routed to `handleCreating`. Inside `handleCreating`, the pod was still observable in the cluster — terminating but not yet GC'd. Its `Status.Phase` was `Failed` (containers terminated non-zero from SIGKILL after grace period). The check at controller.go:243-246 fired, writing terminal `Failed`.

**Reproduced two more times** during this session (workspaces `c98963e7-…`, `8e80afc4-…`) — same exact mechanism every time, verified by `status.message == "pod entered Failed phase during creation"`.

Codebase has zero handling for `pod.DeletionTimestamp` in this branch — `grep DeletionTimestamp controller/` returns 0 hits in pod state-checking logic.

### Story 2's incident pattern

The same 07:43:00 reconcile cycle also produced a `Reconciler error: object has been modified; please apply your changes to the latest version and try again` error (reconcileID `1b2e63bb`). This is K8s's optimistic-concurrency conflict — the controller's `r.Status().Update` sent a stale `resourceVersion`.

Verified causes (worklog 0100, hypothesis H5):

- The API service writes `Status.LastActivityAt` from `api/internal/handlers/activity.go:121` whenever a user is active. It runs from API pods, writes through its own informer, and conflicts with controller writes.
- Controller-runtime informer cache lag: a reconcile can read the pre-write resourceVersion from cache while etcd already has the post-write one.

Today the controller has NO retry-on-conflict pattern. Every `r.Status().Update` is a one-shot. **However, the existing repo prior art (F7) already implements this pattern.** Story 2 propagates the existing helper, not a new design.

### Story 3's structural problem

`WorkspaceStatus` has 24 fields. 23 are controller-only-written. `LastActivityAt` has THREE writers — controller (`handleResuming`), API ActivityTracker (periodic flush), and API Resume flow. `Phase` has cross-writer behavior — controller writes most transitions, API writes `Suspending`/`Resuming` directly via `UpdateStatus`. Multiple writers per field is exactly the conflict-multiplier the verified incidents demonstrated.

The "belt-and-suspenders" comment at `workspace_service.go:443-448` and the matching comment at `controller.go:398-403` are the original authors anticipating the race and writing both sides defensively. That's tech debt — Story 3 eliminates it.

Story 3's structural fix is **scoped narrowly** (refuting R3): two field migrations, not a sweeping refactor.

- **`LastActivityAt`** → migrate from `Status.LastActivityAt` to `metadata.annotations["llmsafespace.dev/last-activity-at"]`. Single writer: API service (both ActivityTracker and Resume code paths use the same `Patch` call). Controller stops writing entirely (its `handleResuming` redundant write is removed). Both readers (controller idle-timeout in `handleActive`, API status-enrichment) read from annotation. **Long-term correct because:** activity is a user-observation; the API service is the layer observing user requests; annotations are K8s-idiomatic for non-spec, non-status metadata; annotation patches don't conflict with `Status().Update` per A12.
- **`Phase` (write paths from API service)** → migrate Suspend/Resume to a spec-driven pattern (new `Spec.Suspend bool`, controller observes and transitions). The API service stops calling `UpdateStatus` for these flows. Controller becomes sole writer of `Phase`.

After Story 3, every `WorkspaceStatus` field has exactly one writer. Cross-owner conflicts are eliminated. Story 2's retry-on-conflict can then ship safely (refuting R4).

### Story 4's incident (verified, this session)

Sequence reproduced at ~08:25 UTC for user `mike@kao.family` on workspace `8e80afc4-…`:

1. Workspace flapped to `Failed` (Story 1 bug class).
2. `handleFailed` ran `cleanupFailedWorkspaceSecrets` (controller.go:71) → deleted `workspace-pw-8e80afc4-…`.
3. User bumped `restartGeneration` via Change A → workspace went `Pending` → `handlePending` called `ensurePasswordSecret` → **regenerated password (new 32-char random string, different from the cached one)** → workspace went Creating → new pod started with new password env → workspace went Active.
4. User refreshed `/chat/8e80afc4-…/ses_…` page in browser.
5. Frontend made API calls (`GET /api/v1/workspaces/.../sessions/...`).
6. API's proxy looked up password — **read from `pwCache` (stale: still has the OLD password)** because the Active-from-non-Active path at `proxy.go:712` does NOT invalidate `pwCache`.
7. API forwarded request to opencode with `Basic opencode:<old-password>`.
8. opencode responded **401 + `WWW-Authenticate: Basic realm="..."`**.
9. API's `doProxy` (line 462-466) copied **all** upstream headers — including `WWW-Authenticate` — to the browser.
10. Browser saw 401 with WWW-Authenticate → showed "Sign in to access this site" dialog.

Two distinct bugs compounded: cache invalidation gap + response-header passthrough. Each is fixable independently; both must land.

Verified at log level:

```
GET /api/v1/workspaces/8e80afc4-…/sessions/ses_188afb9ceffe3d4peYCe13niaR
→ 401, request_id=2fonyPtm  (08:25:16)

GET /api/v1/workspaces/8e80afc4-…/sessions/ses_182e14481ffe8u3F55AnVWY8Cy/message
→ 401, request_id=9eczuZML  (08:25:20)
```

Restarting the API pods (clearing the in-memory `pwCache`) fixes the symptom temporarily — confirmed during this session.

---

## Scope

**This epic owns** all four stories above.

**This epic does NOT own:**
- The state-machine semantics (Epic 21).
- agentd performance (Epic 22).
- Frontend UX changes from Story 3's enriched status visibility (separate frontend epic).

---

## Robustness criteria

Epic 23 is done when **all** of the following hold:

1. **A pod with `DeletionTimestamp != nil` is never interpreted as a "failure" of any kind.** `handleCreating` and `handleActive` both treat a terminating pod as "wait for it to finish terminating, then create a new one." The 07:43:00 incident pattern cannot recur.
2. **Every controller `r.Status().Update` retries on conflict.** A conflict on first attempt does NOT abort the reconcile; the controller re-reads, re-applies the intended status mutation, and retries via `retry.RetryOnConflict` up to `retry.DefaultBackoff`'s 5-attempt limit.
3. **Each `WorkspaceStatus` field has exactly one writer.** `LastActivityAt` is moved out of Status; `Phase` writes from the API service are migrated to spec-driven Suspend/Resume.
4. **`pwCache` is invalidated when a workspace transitions to `Active` from any non-Active phase OR to `Failed`.** The cached password is always either fresh (matches the live Secret) or absent.
5. **The API proxy never forwards `WWW-Authenticate` from opencode to the browser.** Proxy responses are auth-sanitized: if upstream returns 401 with WWW-Authenticate, the API converts to a structured 502 (`"upstream auth failed"`) without the browser-prompting header.
6. **Heavy instrumentation exposes failure mode rates.** Operators can answer "how often does each Failed reason fire today?" / "how often does Story 2's retry actually retry?" / "how many workspaces are mid-backoff right now?" / "how often does the pwCache get invalidated?" without reading logs.
7. **No regression to Active / Suspended / Resuming / Terminating happy paths.** Existing test suites stay green.

---

## Story 1 — DeletionTimestamp guard + heavy instrumentation

### Mechanism

Add an `isPodTerminating(pod)` helper (resolves ambiguity: should the check be inlined or a helper? Helper, for reuse + readability):

```go
// isPodTerminating reports whether the K8s pod is in the process of being
// deleted. A non-nil DeletionTimestamp means kubelet is running termination
// grace; the pod's Status.Phase during this window is unreliable for
// failure-classification purposes (e.g., a SIGKILLed container makes the
// pod briefly observable as Failed). Callers should treat such pods as
// "wait for reaping" rather than as genuine failures.
func isPodTerminating(pod *corev1.Pod) bool {
    return pod != nil && pod.DeletionTimestamp != nil
}
```

In `handleCreating` at controller.go:243-246, before the existing `corev1.PodFailed` branch:

```go
if isPodTerminating(existingPod) {
    metrics.WorkspacePodTerminatingObservedTotal.WithLabelValues("handleCreating").Inc()
    return ctrl.Result{RequeueAfter: requeueCreating}, nil  // (resolves ambiguity F)
}

if existingPod.Status.Phase == corev1.PodFailed {
    // Existing terminal-Failed logic (refactored to use markFailed once
    // Epic 21 Change C lands).
}
```

Apply the same check in `handleActive` at controller.go:280:

```go
if isPodTerminating(pod) {
    metrics.WorkspacePodTerminatingObservedTotal.WithLabelValues("handleActive").Inc()
    workspace.Status.Phase = v1.WorkspacePhaseCreating
    workspace.Status.PodIP = ""
    workspace.Status.Endpoint = ""
    return ctrl.Result{RequeueAfter: requeueCreating}, r.Status().Update(ctx, workspace)
}
if pod.Status.Phase != corev1.PodRunning {
    return r.recoverFromTransientPodLoss(ctx, workspace)
}
```

Behavioral effect: the controller stops counting its own pod deletions as failures.

### Heavy instrumentation (US-23.1's second half)

New metrics in `controller/internal/metrics/metrics.go`. Naming follows the existing `llmsafespace_*` convention (F12).

```go
// Phase transitions (counter, every transition).
WorkspacePhaseTransitionsTotal = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "llmsafespace_workspace_phase_transitions_total",
        Help: "Total workspace phase transitions, labeled by from / to / reason.",
    },
    []string{"from", "to", "reason"},  // reason: "" pre-Epic-21, FailureReason post-Epic-21 (resolves Issue #7 in audit)
)

// Terminal failures (counter, every terminal Failed).
WorkspaceTerminalFailuresTotal = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "llmsafespace_workspace_terminal_failures_total",
        Help: "Total terminal-Failed transitions, labeled by reason.",
    },
    []string{"reason"},  // pre-Epic-21 buckets: "pod_failed_during_creation", "pvc_bind_timeout", "pod_build_failed", "pending_timeout", "too_many_failures"; post-Epic-21: FailureReason enum
)

// Health-check outcomes (counter).
WorkspaceHealthCheckTotal = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "llmsafespace_workspace_health_check_total",
        Help: "Total health-check probes, labeled by outcome.",
    },
    []string{"outcome"},  // "success", "timeout", "5xx", "decode_error", "agent_unhealthy"
)

// Status update conflicts (counter; each conflict is one increment).
WorkspaceStatusUpdateConflictsTotal = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "llmsafespace_workspace_status_update_conflicts_total",
        Help: "Total optimistic-concurrency conflicts on Status updates.",
    },
    []string{"site"},  // controller code path that hit the conflict
)

// Backoff state (gauge per workspace; populated by Epic 21 Change B fields).
// Cardinality note: at 1k workspaces this is 1k time series, acceptable for Prometheus.
WorkspaceConsecutiveFailures = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
        Name: "llmsafespace_workspace_consecutive_failures",
        Help: "Current ConsecutiveFailures count per workspace.",
    },
    []string{"workspace", "namespace"},
)

WorkspaceSecondsSinceLastSuccess = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
        Name: "llmsafespace_workspace_seconds_since_last_success",
        Help: "Seconds since the workspace was last in Active state.",
    },
    []string{"workspace", "namespace"},
)

// DeletionTimestamp encounters (counter; the bug US-23.1 fixes).
WorkspacePodTerminatingObservedTotal = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "llmsafespace_workspace_pod_terminating_observed_total",
        Help: "Total times the controller observed a pod with DeletionTimestamp set; labeled by handler.",
    },
    []string{"handler"},  // "handleCreating", "handleActive"
)

// API proxy auth-cache state (Story 4).
APIProxyPasswordCacheInvalidationsTotal = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "llmsafespace_api_proxy_password_cache_invalidations_total",
        Help: "Total pwCache invalidations, labeled by trigger phase.",
    },
    []string{"trigger"},  // "active_from_non_active", "failed", "suspended", "terminating"
)

APIProxyUpstreamAuthFailuresTotal = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "llmsafespace_api_proxy_upstream_auth_failures_total",
        Help: "Total 401 responses received from upstream agentd/opencode.",
    },
    []string{"workspace_phase"},  // "active", "creating", etc., for diagnostic context
)
```

### Story 1 acceptance criteria

- `handleCreating` returns `ctrl.Result{RequeueAfter: requeueCreating}` (5s, resolves ambiguity F) when pod has DeletionTimestamp set; does NOT write terminal Failed.
- `handleActive` transitions to `Phase=Creating` (clearing PodIP, Endpoint) when pod has DeletionTimestamp set; does NOT increment `TransientFailureCount`.
- `WorkspacePodTerminatingObservedTotal` counter increments at both sites.
- All new metrics are registered with the controller manager and reachable via `/metrics`.
- Unit tests assert: pod with DeletionTimestamp + Phase=Failed does NOT trigger terminal Failed.
- Unit tests assert: pod with DeletionTimestamp + Phase=Running (mid-termination weird state) also does NOT trigger any failure path.
- envtest reproduces the 07:43:00 mechanism: trigger checkAgentHealth pod-delete, observe handleCreating, assert phase stays Creating.
- Live regression: workspace `6d36952e-…` (kept Failed by design as live test case) is recovered via Change A; the same flake pattern is induced (artificially); workspace stays Active.

### Story 1 files modified

- `controller/internal/workspace/controller.go` — DeletionTimestamp checks at lines 243 and 280.
- `controller/internal/workspace/helpers.go` (new file) — `isPodTerminating(pod)` helper.
- `controller/internal/metrics/metrics.go` — 9 new metric registrations.
- `controller/internal/workspace/controller_test.go` — 4 new unit tests.
- `controller/internal/workspace/envtest_test.go` — NEW file with envtest fixture (~half day setup).

---

## Story 2 — Retry-on-conflict for `Status().Update`

### Mechanism

Reuse the existing `retry.RetryOnConflict` pattern (refuting R1, validated by F7). The controller's helper:

```go
import "k8s.io/client-go/util/retry"

// updateStatusWithRetry wraps Status().Update in retry.RetryOnConflict.
// On conflict, re-fetches the latest Workspace and re-applies the supplied
// mutation closure. Mirrors the pattern used in api/internal/handlers/activity.go:114.
func (r *WorkspaceReconciler) updateStatusWithRetry(
    ctx context.Context,
    name types.NamespacedName,
    mutate func(*v1.Workspace),
) error {
    return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
        var fresh v1.Workspace
        if err := r.Get(ctx, name, &fresh); err != nil {
            return err
        }
        mutate(&fresh)
        if err := r.Status().Update(ctx, &fresh); err != nil {
            metrics.WorkspaceStatusUpdateConflictsTotal.WithLabelValues("controller").Inc()
            return err  // retry.RetryOnConflict examines the error
        }
        return nil
    })
}
```

Replace each of the 22 `r.Status().Update(ctx, workspace)` sites with a call to `updateStatusWithRetry` passing the field mutations as a closure. The closure captures the *intended* state changes; the helper handles re-read + retry. **Mutation closure semantics (resolves ambiguity G):** the closure operates on the freshly-fetched workspace and applies the same logical update as the original code path. For deterministic mutations (e.g., "set Phase=Active, set PodIP=X"), this works directly. For non-deterministic mutations (e.g., "increment ConsecutiveFailures"), the closure must derive the new value from the freshly-fetched current value — i.e., the closure reads `fresh.Status.ConsecutiveFailures + 1`, not a captured local.

### Story 2 acceptance criteria

- Every `r.Status().Update` site uses `updateStatusWithRetry` (or a close variant for `Update` vs `UpdateStatus`).
- A conflict-injecting test: simulate the API service's concurrent `LastActivityAt` write between Get and Update; the controller's mutation succeeds eventually with proper resourceVersion.
- `WorkspaceStatusUpdateConflictsTotal` counter increments correctly on each conflict (non-zero in the chaos test).
- No unbounded retry: `retry.DefaultBackoff` caps at 5 attempts (~1s total) per the existing convention.
- Race-free interaction with Story 1's DeletionTimestamp checks (no double-handling of a single deletion event).

### Story 2 files modified

- `controller/internal/workspace/controller.go` — every `r.Status().Update` site.
- `controller/internal/workspace/controller_test.go` — conflict-injection tests.
- envtest extension: simulate concurrent writer.

---

## Story 3 — Single-writer audit (scoped: 2 fields)

### Mechanism

**Annotate ownership in code.** Each `WorkspaceStatus` field gets a doc comment specifying its owner:

```go
type WorkspaceStatus struct {
    // Owned by the controller (only the controller may write):
    Phase WorkspacePhase `json:"phase,omitempty"`
    PVCName string `json:"pvcName,omitempty"`
    ActiveSessions int32 `json:"activeSessions,omitempty"`
    // ... (22 fields total)

    // Owned by the API service (only the API may write):
    LastActivityAt *metav1.Time `json:"lastActivityAt,omitempty"`  // DEPRECATED: moving to annotation in Story 3
}
```

**Migration #1: `LastActivityAt`.** Decision (resolves ambiguity H — annotation is chosen over alternatives):

- **Chosen:** annotation `llmsafespace.dev/last-activity-at` on the Workspace object.
- **Rejected: separate CR.** Too heavy for a single timestamp; ergonomically awful for consumers.
- **Rejected: Spec field.** Spec is desired state; an activity timestamp is observation, not desire.
- **Rejected: keep in Status.** Defeats the single-writer goal.

The annotation pattern works because:
- Annotations don't go through the status subresource and don't conflict with `Status().Update`.
- `metav1.ObjectMeta.Annotations` is a `map[string]string`; the API service uses `map[string]string` Update which serializes the timestamp as RFC3339.
- Consumers (the API service for idle-suspend logic, the controller for `maybeResetTransientCounter`) read the annotation directly; both paths are read-only post-migration.

Audit pre-migration: who reads/writes `LastActivityAt`?

**Writers (2 sites):**
- `api/internal/handlers/activity.go:120` — primary writer when user is active.
- `api/internal/services/workspace/workspace_service.go:445` — Resume flow resets the timestamp.

**Readers (2 sites):**
- `api/internal/services/workspace/workspace_service.go:554-556` — surfaced in `GET /workspaces/:id/status` response.
- `controller/internal/workspace/controller.go:333-339` — idle-timeout check in `handleActive` (drives auto-suspend).

All 4 sites must migrate to the annotation in the same commit cycle as Story 3. Annotation key: `llmsafespace.dev/last-activity-at` (RFC3339 timestamp).

The API service writes the annotation via a `Patch` (cheaper than Update; doesn't conflict with Status writes). Reads happen via `metav1.ObjectMeta.Annotations[…]`.

**Migration #2: `Phase` writes from API service.**

Today: `SuspendWorkspace` and `ResumeWorkspace` write `Status.Phase = WorkspacePhaseSuspending` / `WorkspacePhaseResuming` directly via `UpdateStatus` (`workspace_service.go:400, 446`).

Migration: introduce `Spec.Suspend bool` (or, for orthogonality with terminating, a `Spec.Lifecycle` enum: `"running"`, `"suspended"`, `"terminated"`). The API service writes spec; the controller observes the spec change and transitions Phase as it does for any other reconcile-driven transition.

```go
// New field
type WorkspaceSpec struct {
    // ...
    // Suspend, when true, requests the controller suspend the workspace.
    // The controller transitions Phase: Active → Suspending → Suspended.
    // Setting Suspend=false from Suspended triggers the resume path.
    // +kubebuilder:default=false
    Suspend bool `json:"suspend,omitempty"`
}
```

Controller logic in `handleActive`: if `spec.Suspend == true`, transition to `Suspending`. In `handleSuspended`: if `spec.Suspend == false`, transition to `Resuming`.

This eliminates the API service's only Status writes for these flows.

**Linter rule:** add a check (extending `pkg/repolint`) that fails CI if:
- The controller's source contains `Status.LastActivityAt =` assignments.
- The API service's source contains `Status.Phase =` assignments outside the migration paths (which are removed in this story).

### Story 3 acceptance criteria

- Each `WorkspaceStatus` field has an owner doc comment.
- API service no longer calls `UpdateStatus` on workspaces. Suspend/Resume go through `Spec.Suspend`.
- `LastActivityAt` is moved out of Status to an annotation.
- All consumers of `LastActivityAt` are migrated to read from the annotation.
- The repolint linter rule prevents regressions.
- Existing Suspend/Resume integration tests still pass with the new spec-driven path.

### Story 3 files modified

- `pkg/apis/llmsafespace/v1/workspace_types.go` — owner doc comments, new `Spec.Suspend` field, removal of `LastActivityAt` from Status.
- `api/internal/services/workspace/workspace_service.go` — `SuspendWorkspace` / `ResumeWorkspace` write `Spec.Suspend` instead of Status.
- `api/internal/handlers/activity.go` — annotation Patch instead of Status update.
- `controller/internal/workspace/controller.go` — handle `Spec.Suspend` in `handleActive` / `handleSuspended`; read `LastActivityAt` from annotation.
- `pkg/repolint/sequence.go` (or similar) — lint rule for cross-owner writes.
- All call sites of `Workspaces(...).UpdateStatus` audited and migrated.

---

## Story 4 — API proxy auth-cache invalidation + response-header sanitization

### Mechanism

**Bug 1 — `pwCache` invalidation.** Extend `proxy.go:onPhaseChange` to invalidate the cache on:
- `Failed` (currently missing).
- `Active` from a non-Active prior phase (currently invalidates only `wsConfig`, not `pwCache`).

The Active branch needs to know the prior phase. Today `onPhaseChange` is called with the new phase only. Two implementation choices:

- **A) Track prior phase in handler state.** Add a `priorPhase map[string]v1.WorkspacePhase` cache. On every `onPhaseChange`, compare. If transitioning to Active from anything other than Active, invalidate `pwCache`.
- **B) Always invalidate `pwCache` on Active and Failed.** Wasteful but correct: every Active reconcile clears the cache; the next request fetches fresh. Adds one Secret read per workspace per phase change.

**Decision:** Option A. Option B's overhead is small but the principle is bad — caching exists for a reason; correctness should be precise. Extra state is one map.

```go
func (h *ProxyHandler) onPhaseChange(workspace *v1.Workspace) {
    phase := workspace.Status.Phase
    h.priorPhaseMu.Lock()
    prior := h.priorPhase[workspace.Name]
    h.priorPhase[workspace.Name] = phase
    h.priorPhaseMu.Unlock()

    // ... existing publish ...

    if phase == phaseSuspending || phase == phaseSuspended ||
       phase == phaseTerminating || phase == phaseTerminated ||
       phase == v1.WorkspacePhaseFailed {
        h.invalidateCaches(workspace.Name)
        metrics.APIProxyPasswordCacheInvalidationsTotal.WithLabelValues(string(phase)).Inc()
        // ... existing tracker stop ...

        // Cleanup priorPhase for terminal phases so the map doesn't grow
        // unboundedly. Failed is intentionally NOT cleaned up here because
        // the workspace can recover via Change A (RestartGeneration bump);
        // the Active→Active transition that follows recovery still needs
        // priorPhase to be correct.
        if phase == phaseTerminated || phase == phaseTerminating {
            h.priorPhaseMu.Lock()
            delete(h.priorPhase, workspace.Name)
            h.priorPhaseMu.Unlock()
        }
        return
    }
    if phase == phaseActive {
        // Password may have rotated during a non-Active interval (e.g.,
        // ensurePasswordSecret regenerated it after Failed→recovery).
        // Always invalidate pwCache when transitioning Active-from-non-Active.
        if prior != "" && prior != phaseActive {
            h.invalidateCaches(workspace.Name)
            metrics.APIProxyPasswordCacheInvalidationsTotal.WithLabelValues("active_from_non_active").Inc()
        } else {
            // Active→Active reconcile (no transition). Only wsConfig may be stale.
            h.wsConfigMu.Lock()
            delete(h.wsConfig, workspace.Name)
            h.wsConfigMu.Unlock()
        }
    }
}
```

**Bug 2 — Response header sanitization.** In `doProxy`, when the upstream response is 401, do NOT forward the `WWW-Authenticate` header. Convert the response to a structured 502:

```go
if resp.StatusCode == http.StatusUnauthorized {
    metrics.APIProxyUpstreamAuthFailuresTotal.WithLabelValues(string(workspace.Status.Phase)).Inc()
    h.invalidateCaches(workspace.Name)  // password may be stale; invalidate proactively
    h.logger.Warn("Upstream auth failed; password cache invalidated and request rejected",
        "workspaceID", workspace.Name, "path", c.Request.URL.Path)
    c.JSON(http.StatusBadGateway, gin.H{
        "error": "upstream authentication failed; please retry",
        "workspaceID": workspace.Name,
    })
    return nil
}

// Existing header copy paths...
```

The proactive `invalidateCaches` on 401 is a defensive safety net: even if the prior-phase tracking misses an edge case, a single 401 from upstream auto-recovers on the next request.

Additionally, define an explicit allow/deny list of headers to forward. `WWW-Authenticate`, `Proxy-Authenticate`, `Authorization` (request-side), `Cookie` (request-side from upstream), `Set-Cookie` (response-side) are all stripped. Only safe response headers (`Content-Type`, `Content-Length`, `Cache-Control`, `Last-Modified`, `ETag`, plus SSE-related `Content-Type: text/event-stream`, `X-Accel-Buffering`, etc.) are forwarded.

```go
// Safe response headers that are always forwarded; others are dropped.
var forwardedResponseHeaders = map[string]bool{
    "Content-Type":   true,
    "Content-Length": true,
    "Cache-Control":  true,
    "Last-Modified":  true,
    "ETag":           true,
    "Vary":           true,
    "X-Accel-Buffering": true,
}
```

Tightening the list eliminates the WWW-Authenticate issue and any other unexpected header pass-through.

### Story 4 acceptance criteria

- `pwCache` is invalidated when:
  - Workspace transitions to `Failed`.
  - Workspace transitions to `Active` from any non-Active phase.
  - Upstream returns 401 (defensive).
- Upstream 401 responses are converted to 502 with no `WWW-Authenticate` header.
- The forwarded-header allow-list is enforced; `WWW-Authenticate`, `Proxy-Authenticate`, `Set-Cookie` (from upstream) are never sent to the browser.
- Unit test: simulate password rotation between two requests; the second request fetches fresh password (mock-call assertion: `Secrets().Get` called twice).
- Unit test: simulate upstream 401; assert API returns 502 with no `WWW-Authenticate`.
- Integration test (against a fake opencode): full sequence (Failed → recovery → Active) → assert the next request uses the new password.
- Metrics increment correctly.

### Story 4 files modified

- `api/internal/handlers/proxy.go` — `onPhaseChange`, `doProxy`, header allow-list.
- `api/internal/handlers/proxy_test.go` — new tests.
- `controller/internal/metrics/metrics.go` — 2 new API-side metrics (cross-binary metric registration: API and controller share the prom registry via the metrics service).

---

## Test plan

### Unit tests (controller_test.go, proxy_test.go, fake-clients)

**Story 1:**
- Pod with DeletionTimestamp + Failed phase → no terminal Failed.
- Pod with DeletionTimestamp + Running phase → no failure path.
- Pod without DeletionTimestamp + Failed → terminal Failed (regression for genuine failures).

**Story 2:**
- Mock client returns Conflict on first Update attempt, success on second. Assert reconcile completes successfully and `WorkspaceStatusUpdateConflictsTotal` increments by 1.
- Mock client returns Conflict 5 times → final attempt fails; reconcile errors out (no infinite retry).

**Story 3:**
- API service's Suspend writes `Spec.Suspend=true`, not Status.
- Controller's `handleActive` transitions to Suspending when `Spec.Suspend=true`.
- `LastActivityAt` annotation Patch from API service does not conflict with controller Status writes (envtest scenario).
- Repolint rule fails on a synthetic source-tree that violates the single-writer rule.

**Story 4:**
- `pwCache` invalidated on `Failed` transition.
- `pwCache` invalidated on `Active`-from-`Pending` transition.
- `pwCache` NOT invalidated on `Active`-from-`Active` reconcile (regression — don't break performance).
- Upstream 401 → API returns 502; response Headers do NOT contain `WWW-Authenticate`.
- Unauthorized response invalidates `pwCache` defensively.

### Integration tests (envtest, real kube-apiserver+etcd)

This is **the** test infrastructure for Epic 23. Setup ~half day. Shared with Epic 21.

- **Story 1 mechanism reproduction:** start a workspace pod in envtest, trigger checkAgentHealth's pod-delete, observe handleCreating. Assert phase stays Creating, transient-failure-counter does not increment, no terminal Failed.
- **Story 2 conflict simulation:** start two writers (real controller + a goroutine simulating the API service's annotation update). Observe controller's status updates. Assert no permanent loss, eventual consistency, conflict counter > 0.
- **Story 3 single-writer enforcement:** in envtest, the API service path tries to write Phase. Assert the controller's Suspend reconciler picks it up via spec, writes status. Verify only the controller's resourceVersion bumps the status.
- **Story 4 password rotation:** envtest scenario — workspace flaps Failed → recovers via Change A → API receives a request → assert it fetches the new password (verified via Secret read counter).

### Chaos / failure-injection

- **Pod kill storm:** delete the pod 5 times in 10 seconds. Story 1 must keep handleCreating from going terminal. Story 2 must keep status writes converging. Without both stories landing, the test fails.
- **Concurrent API + controller writers:** stress test with 100 LastActivityAt updates from the API service while the controller is reconciling. Assert eventual consistency.
- **Controller crash-loop:** kill the controller pod mid-Status-Update. Replacement controller comes up and reconciles cleanly with no double-counting.
- **Password rotation under load:** during an ongoing user request, inject a password rotation. Assert the request fails with 502 (clean error), pwCache invalidates, the next request succeeds.

### Observability tests

- All 9 new metrics ship in Story 1 (registered with the metrics service, scraped at `/metrics`). 7 are populated by controller code paths Story 1 modifies; 2 (API proxy metrics) are registered in Story 1 but their counters increment only after Story 4 wires them.
- Counter values change on triggered events (e.g., inducing a DeletionTimestamp encounter increments `WorkspacePodTerminatingObservedTotal{handler="handleCreating"}` by 1).
- Gauge values for `WorkspaceConsecutiveFailures` track the corresponding status field for each workspace (after Epic 21 lands; pre-Epic-21, this gauge stays 0 for all workspaces).

---

## Sequencing

**Story 1 ships first** — hot-patch for the recurring incident reproduced 3 times this session. The DeletionTimestamp check is ~5 lines + tests; the heavy instrumentation lands in the same commit so metrics are populated from day one.

**Story 4 ships in parallel with Story 1** — the user-facing basic-auth dialog is a serious UX failure. Story 4 is in the API codebase, Story 1 is in the controller; they don't conflict at the file level. Both are hot-patches.

**Story 3 ships before Story 2** (re-sequenced in audit pass 3, refuting R4). Story 2's closure-rerun retry-on-conflict has a lost-update risk while two writers of `Phase` exist. Story 3 eliminates that race structurally; only after Story 3 lands is Story 2 safe to apply broadly. Story 3 is a larger surface area than Story 2 but lower risk because each migration is mechanical and the annotation lane (A12) provides clean isolation.

**Story 2 ships last** — once Story 1 and Story 3 have landed, the remaining conflict source is informer-cache lag (a much narrower failure mode). Story 1's metric `WorkspaceStatusUpdateConflictsTotal` will reveal the residual rate; if it's near zero post-Story-3, Story 2 may be deferred entirely.

---

## Out of scope

- Multi-controller consensus / leader election improvements. Today's controller-runtime defaults are fine.
- Replacing controller-runtime's reconcile loop with something else.
- Generalizing the conflict-retry helper into a multi-resource utility — keep it scoped to workspace status writes for this epic.
- Frontend rendering changes from Story 3's enriched status fields (separate frontend epic).
- Full audit of every API endpoint's response headers (only the proxy path is in scope; other endpoints are server-emitted JSON without upstream pass-through).

---

## Open questions for review

1. **~~Story 3 `LastActivityAt` decision.~~** Resolved (audit pass 3): annotation patch via the API service as sole writer. Controller's `handleResuming` redundant write is removed. The "belt-and-suspenders" race protection is no longer needed because annotation patches and Status updates use independent optimistic-concurrency lanes (A12).
2. **Story 2 retry attempt limit.** `retry.DefaultBackoff` (5 attempts, ~1s) matches the existing API-service pattern. Acceptable or want longer?
3. **Story 4 forwarded-header allow-list.** Is the proposed list complete? Specifically: should `Strict-Transport-Security` or similar security headers from upstream be forwarded? Today none of upstream's responses set them, so the list is safe; if upstream adds headers later, the explicit list catches them.
4. **~~Story 4 prior-phase tracking lifetime.~~** Resolved: cleanup happens on `Terminated` / `Terminating` (not on `Failed`, because Failed→Active is recoverable via Change A and the Active branch still needs `prior` to be correct).
5. **Should the DeletionTimestamp check be a `WorkspacePhase` precondition** rather than a per-handler check? Argument for: DRY. Argument against: handlers have different correct responses (handleCreating waits, handleActive transitions to Creating); the predicate is shared but the response isn't.
