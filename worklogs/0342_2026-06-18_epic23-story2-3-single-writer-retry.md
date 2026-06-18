# Worklog: Epic 23 Stories 2+3 — Single-writer migration + Status().Update retry helper

**Date:** 2026-06-18
**Session:** Migrate `LastActivityAt` from Status to annotation; migrate Suspend/Resume from Status.Phase to Spec.Suspend; add `updateStatusWithRetry` helper
**Status:** Complete
**Epic:** 23 (Controller & API Race Hardening)

---

## Objective

Close Epic 23 by shipping the two deferred stories:

- **Story 3 (single-writer migration):** Each `WorkspaceStatus` field has exactly one owner. `LastActivityAt` moves from Status (three writers) to a metadata annotation (one writer: the API service). Suspend/Resume moves from API-written `Status.Phase` to API-written `Spec.Suspend`, observed by the controller.
- **Story 2 (retry-on-conflict):** Provide an `updateStatusWithRetry` helper that wraps `Status().Update` in `retry.RetryOnConflict`, mirroring the existing pattern in the API service.

Per the design's sequencing rule (R4), Story 3 must land before Story 2 is broadly applied — otherwise a retry-on-conflict closure can resurrect a Phase value the API just wrote. Story 3 eliminates that race.

---

## Work Completed

### Story 3a — `LastActivityAt` → metadata annotation

**New annotation key:** `llmsafespace.dev/last-activity-at` (RFC3339 timestamp).

**`pkg/apis/llmsafespace/v1/workspace_types.go`:**
- Added `AnnotationLastActivityAt` constant alongside the existing `AnnotationRequestedAt`.
- Added `GetLastActivityAt(ws)` helper — reads annotation (authoritative), falls back to deprecated `Status.LastActivityAt` for workspaces created before the migration.
- Added `SetLastActivityAtAnnotation(annotations, t)` helper.
- Marked `Status.LastActivityAt` field DEPRECATED in doc comment (retained for the migration window; no code writes to it now).

**`api/internal/services/activity/tracker.go` `flushOne`:** replaced `Get → ws.Status.LastActivityAt = ... → UpdateStatus` with a strategic-merge `Patch` on `metadata.annotations`. The Patch uses the main-resource optimistic-concurrency lane, which is independent of the `/status` subresource lane — a controller Status().Update and an API annotation Patch can no longer conflict on the same field family. Retained the existing `retry.RetryOnConflict` wrapper.

**`api/internal/services/workspace/workspace_service.go` `ActivateWorkspace`:** removed `crd.Status.LastActivityAt = &now`; now calls `v1.SetLastActivityAtAnnotation(crd.Annotations, now)` before `wsClient.Update(ctx, crd)`.

**`api/internal/services/workspace/workspace_service.go` `GetWorkspaceStatus` (around line 714):** replaced `crd.Status.LastActivityAt` read with `v1.GetLastActivityAt(crd)` so the API surfaces the annotation value to clients.

**`controller/internal/workspace/phase_active.go` idle auto-suspend check:** replaced `workspace.Status.LastActivityAt` read with `v1.GetLastActivityAt(workspace)`.

**`controller/internal/workspace/phase_suspend.go` `handleResuming`:** removed the `workspace.Status.LastActivityAt = &now` write. The controller is no longer a writer of LastActivityAt (single-writer principle). The annotation is written by the API service in `ActivateWorkspace` before transitioning to Resuming.

### Story 3b — Suspend/Resume via `Spec.Suspend` (tri-state pointer)

**`pkg/apis/llmsafespace/v1/workspace_types.go`:** added `Suspend *bool` to `WorkspaceSpec`. Pointer (not plain `bool`) so that three states are distinguishable:
- `nil` — field absent (workspace created before migration, or controller-driven lifecycle like TTL). The controller does NOT use this to make resume decisions; pre-existing Suspended workspaces stay suspended on upgrade.
- `&true` — API requests suspension. `handleActive` transitions to Suspending.
- `&false` — API requests resume from Suspended. `handleSuspended` transitions to Resuming.

**`api/internal/services/workspace/workspace_service.go`:**
- `SuspendWorkspace`: writes `suspendTrue := true; crd.Spec.Suspend = &suspendTrue` via `wsClient.Update` (was: `crd.Status.Phase = Suspending` via `UpdateStatus`).
- `ActivateWorkspace`: writes `suspendFalse := false; crd.Spec.Suspend = &suspendFalse` plus the annotation, via `wsClient.Update` (was: `crd.Status.Phase = Resuming` + `crd.Status.LastActivityAt` via `UpdateStatus`).

**`controller/internal/workspace/phase_active.go`:** added check at top of handler — if `workspace.Spec.Suspend != nil && *workspace.Spec.Suspend` → transition to Suspending. Placed before the restart-generation check.

**`controller/internal/workspace/phase_suspend.go` `handleSuspended`:** added check at top of handler — if `workspace.Spec.Suspend != nil && !*workspace.Spec.Suspend` → transition to Resuming. A nil pointer (legacy) skips this branch and falls through to TTL evaluation.

**CRD YAML** (`charts/llmsafespace/crds/workspace.yaml`, `pkg/crds/workspace_crd.yaml`): added `suspend: {type: boolean, default: false}` under `spec`.

**`pkg/interfaces/kubernetes.go` `WorkspaceInterface`:** added `Patch(ctx, name, pt, data, opts)` method (needed by the annotation-Patch in tracker).

**`pkg/kubernetes/client_crds.go`:** added `Patch` implementation on the `workspaces` client.

**`mocks/kubernetes/mocks.go`:** added `Patch` mock method.

### Story 3c — Owner doc comments

**`pkg/apis/llmsafespace/v1/workspace_types.go` `WorkspaceStatus`:** added per-field ownership comments ("Controller-owned", "DEPRECATED") and a struct-level comment explaining the single-writer principle. No behavior change.

### Story 2 — `updateStatusWithRetry` helper

**New file:** `controller/internal/workspace/status_retry.go`
- `(r *WorkspaceReconciler) updateStatusWithRetry(ctx, nn, mutate)` — wraps `Status().Update` in `retry.RetryOnConflict`. On conflict, re-fetches the latest Workspace, re-applies the mutation closure, retries. Increments `WorkspaceStatusUpdateConflictsTotal{site="controller_retry"}` on each conflict.
- `reconcileResultFromStatusError(err)` — converts a Status().Update error to a `ctrl.Result` (requeue on conflict, return error otherwise).

**New file:** `controller/internal/workspace/status_retry_test.go` (7 tests)
- Success on first attempt; deterministic mutation; nil error; generic error; non-existent workspace; multiple fields in closure; zero-value mutation preserves object.

**Migration of 21 existing `r.Status().Update` sites to use the helper:** DEFERRED. Per Epic 23 design (`README.md:4, 632`): Story 1's `WorkspaceStatusUpdateConflictsTotal` metric should first confirm the residual conflict rate warrants the migration. With Story 3 landed, the cross-writer race is structurally eliminated; remaining conflicts come from informer-cache lag (much narrower). The helper is available and tested; the mechanical migration of the 21 sites is a follow-up PR.

### Test updates

The migration changed the K8s API call shape for Suspend/Activate (UpdateStatus → Update) and for activity flush (Get+UpdateStatus → Patch). Updated all affected tests:

- `api/internal/services/activity/tracker_test.go` — rewrote to mock `Patch` instead of `Get`+`UpdateStatus`. Added test asserting the Patch payload contains the annotation key. Added test for non-conflict error keeping entry pending. Added test for NotFound deleting entry.
- `api/internal/services/workspace/workspace_service_test.go` — `Suspend`/`Activate` tests now mock `Update` and assert `Spec.Suspend` pointer + annotation.
- `api/internal/services/workspace/max_active_test.go` — added `Update`/`Patch`/`Watch`/`Create`/`Delete` methods to `mockWSInterfaceForMaxActive` so it satisfies the now-larger `WorkspaceInterface`. Aliased `k8stypes` for `apimachinery/pkg/types` to avoid collision with `pkg/types`.
- `controller/internal/workspace/controller_test.go` — Suspended TTL/NoTTL tests now set `Spec.Suspend = &true` so the controller evaluates TTL instead of auto-resuming. Resuming test now asserts the controller does NOT write `Status.LastActivityAt` (single-writer).
- `api/internal/handlers/proxy_test.go` — two B5 activity-tracker tests updated to mock `Patch` instead of `Get`.

---

## Key Decisions

1. **`Spec.Suspend` is `*bool`, not `bool`.** Adversarial review (Rule 11 Phase 1) caught that a plain `bool` defaulting to `false` would auto-resume every pre-existing Suspended workspace on controller upgrade — `handleSuspended` would see `Suspend==false` and transition to Resuming. The pointer makes "field absent" (nil) distinguishable from "explicitly false", and the controller treats nil as "no resume requested". This is the difference between a zero-downtime upgrade and a mass-resume incident.

2. **`LastActivityAt` field retained in Status (DEPRECATED), not removed.** Removing it would break the CRD schema for any in-flight workspaces. The `GetLastActivityAt` helper falls back to the Status field so pre-migration workspaces keep working. Removal is a future cleanup once all live workspaces have cycled through the annotation writer.

3. **Activity tracker uses `Patch`, not `Update`.** `Update` sends the full object including Status, which would race with the controller's Status().Update. `Patch` with `MergePatchType` sends only the annotation diff — a single field on `metadata.annotations`. Strategic merge semantics ensure only the named annotation is touched.

4. **Story 2 helper landed, but 21-site migration deferred.** Per the design's explicit sequencing note (line 632): with Story 3 eliminating cross-writer Phase races, the residual conflict rate (informer-cache lag only) may be low enough that the migration isn't worth the risk. The metric from Story 1 (`WorkspaceStatusUpdateConflictsTotal`) will tell us. The helper is tested and ready for incremental adoption.

5. **`updateStatusWithRetry` takes a `mutate` closure, not a pre-built Workspace.** This forces the caller to express the mutation as "what I want the status to look like" rather than "here's a stale snapshot, please write it". On conflict-retry, the closure re-applies against a freshly-fetched Workspace, eliminating the lost-update window.

---

## Assumptions Validated (Rule 7)

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | `metadata.annotations` Patch uses a separate optimistic-concurrency lane from `/status` subresource Update | K8s-spec-verified: Patch on the main resource uses `resourceVersion` but conflicts are evaluated per-write; an annotation Patch and a Status Update on the same object do not cross-conflict |
| A2 | RFC3339 is the canonical timestamp format for annotations | Verified by `time.Parse(time.RFC3339, ...)` round-trip in `GetLastActivityAt` |
| A3 | A plain `bool` Spec.Suspend would auto-resume pre-existing Suspended workspaces on upgrade | Adversarial review (Rule 11) confirmed this — fixed by using `*bool` |
| A4 | The existing activity tracker tests are the only tests that assert on `UpdateStatus` for the activity path | Verified via `grep -rn "UpdateStatus" api/internal/services/activity/` |
| A5 | `handleResuming` was the only controller writer of `Status.LastActivityAt` | Verified via `grep -rn "Status.LastActivityAt =" controller/` — single hit at `phase_suspend.go:77` (now removed) |
| A6 | `SuspendWorkspace`/`ActivateWorkspace` were the only API writers of `Status.Phase` | Verified via `grep -rn "Status.Phase =" api/` — only those two sites (now both migrated to Spec.Suspend) |
| A7 | `retry.RetryOnConflict` with `retry.DefaultBackoff` caps at 5 attempts | Verified from `k8s.io/client-go/util/retry` source — matches the existing API-side pattern |

---

## Tests Run

| Command | Outcome |
|---|---|
| `go build ./...` | PASS |
| `go test -race -count=1 ./controller/...` | PASS (all packages) |
| `go test -race -count=1 ./api/internal/services/workspace/` | PASS |
| `go test -race -count=1 ./api/internal/services/activity/` | PASS |
| `go test -race -count=1 ./api/internal/handlers/` | PASS (37.7s) |
| `go test -race -count=1 ./api/internal/server/ -run TestOpenAPIRouterContract` | PASS |
| `go test -race -count=1 -run TestUpdateStatusWithRetry ./controller/internal/workspace/` | PASS (7 tests) |
| `go test -race -count=1 ./api/... ./controller/... ./pkg/... ./mocks/...` | PASS (all green) |

---

## Files Modified

| File | Change |
|------|--------|
| `pkg/apis/llmsafespace/v1/workspace_types.go` | Added `AnnotationLastActivityAt`, `GetLastActivityAt`, `SetLastActivityAtAnnotation`; added `Spec.Suspend *bool`; added owner doc comments on `WorkspaceStatus`; marked `Status.LastActivityAt` DEPRECATED |
| `pkg/interfaces/kubernetes.go` | Added `types` import; added `Patch` to `WorkspaceInterface` |
| `pkg/kubernetes/client_crds.go` | Added `types` import; added `Patch` impl on `workspaces` client |
| `mocks/kubernetes/mocks.go` | Added `types` import; added `Patch` mock method |
| `api/internal/services/activity/tracker.go` | `flushOne` now uses annotation Patch (not Status.UpdateStatus) |
| `api/internal/services/activity/tracker_test.go` | Rewrote for Patch mock |
| `api/internal/services/workspace/workspace_service.go` | `SuspendWorkspace` writes `Spec.Suspend=&true`; `ActivateWorkspace` writes `Spec.Suspend=&false` + annotation; status reader uses `GetLastActivityAt` |
| `api/internal/services/workspace/workspace_service_test.go` | Updated Suspend/Activate tests for Update mock + pointer assertions |
| `api/internal/services/workspace/max_active_test.go` | Added missing interface methods to mock; aliased `k8stypes` |
| `controller/internal/workspace/phase_active.go` | Added `Spec.Suspend` check; idle check reads annotation via `GetLastActivityAt` |
| `controller/internal/workspace/phase_suspend.go` | `handleSuspended` checks `Spec.Suspend` for resume; `handleResuming` no longer writes `Status.LastActivityAt` |
| `controller/internal/workspace/controller_test.go` | Updated Suspended/Resuming tests for pointer Spec.Suspend + annotation |
| `controller/internal/workspace/status_retry.go` | NEW — `updateStatusWithRetry` helper + `reconcileResultFromStatusError` |
| `controller/internal/workspace/status_retry_test.go` | NEW — 7 unit tests |
| `api/internal/handlers/proxy_test.go` | Two B5 activity-tracker tests updated for Patch mock |
| `charts/llmsafespace/crds/workspace.yaml` | Added `suspend` field to spec schema (`nullable: true`, no default) |
| `pkg/crds/workspace_crd.yaml` | Added `suspend` field to spec schema (`nullable: true`, no default) |

---

## Reviewer-driven fixes (PR #231, round 1)

The skeptical validator caught two CRITICAL production-breaking bugs that the fake-client tests masked:

### CRITICAL 1: CRD `default: false` defeats the `*bool` tri-state
The CRD schema had `default: false`, which causes kube-apiserver to inject `false` on every create/update where the field is absent. In production, `Spec.Suspend` would ALWAYS be `&false` (non-nil), never `nil`. This means `handleSuspended` would immediately resume every suspended workspace. The fake client doesn't apply CRD defaults, so tests passed.

**Fix:** Removed `default: false` from both CRD YAMLs. Added `nullable: true`. The Go `*bool` now correctly deserializes absent fields as `nil`. Updated the kubebuilder annotation to `+kubebuilder:validation:Optional` + `+nullable` (removed `+kubebuilder:default=false`).

### CRITICAL 2: Controller never clears `Spec.Suspend` → infinite suspend/resume loop
After `ActivateWorkspace` sets `Spec.Suspend = &false`, the field stayed `&false` indefinitely. When the controller later auto-suspended (idle timeout), `handleSuspended` would see stale `&false` → resume → idle → suspend → resume... infinite loop.

**Fix:** Added `clearSuspendRequest(ctx, workspace)` helper in `status_retry.go`. The controller calls it after acting on a suspend or resume request. The helper uses `Update` (spec subresource) with `retry.RetryOnConflict` to set `Spec.Suspend = nil`. Called in both `handleActive` (after transitioning to Suspending) and `handleSuspended` (after transitioning to Resuming).

### MAJOR: `reconcileResultFromStatusError` conflict path + dead code
The helper returned `ctrl.Result{RequeueAfter: 0}` which (with default `Requeue: false`) means NO requeue — the opposite of the comment's intent. Since the helper had zero production callers (the 21-site migration is deferred), removed it entirely per Rule 5 (zero tech debt). Will re-add with correct semantics when the migration lands.

### Tests added
- `TestClearSuspendRequest_SetsToNil`, `TestClearSuspendRequest_AlreadyNilIsNoop`, `TestClearSuspendRequest_NonExistentReturnsError` — cover the new clear helper.
- `TestHandleSuspended_StaleFalseAfterControllerSuspend_DoesNotResume` — the infinite-loop regression test (nil Spec.Suspend after controller-initiated suspend must NOT resume).
- `TestHandleSuspended_NilSpecSuspend_NoTTL_StaysSuspended` — pre-migration workspace safety (nil Spec.Suspend stays suspended).

### Contract test expanded
Reviewer noted only 6 of ~16 proxy routes were covered. Expanded to 13 routes (added SendMessage, SendPromptAsync, ListQuestions, QuestionReply, QuestionReject, ListPermissions, PermissionReply).

### Style fixes
- Converted `TestSecretServiceSatisfiesEnvService` to package-level `var _` (cleaner compile-time check).
- Fixed inaccurate comment in `controller_test.go` about `Spec.Suspend` in TTL test.
- Updated Epic 23 README to document the `*bool` design (reviewer noted the doc still said `bool`).

---

## Next Steps

- **Story 2 full migration (21 sites):** defer pending metric data from `WorkspaceStatusUpdateConflictsTotal`. When conflict rate post-Story-3 justifies it, migrate each `r.Status().Update` site to `updateStatusWithRetry`. Sites listed in `metrics_wiring.go:151` comment (now stale — actual count is 21, not 18).
- **Remove deprecated `Status.LastActivityAt` field:** after all live workspaces have cycled through the annotation writer (one PVC-and-pod recycling window). Then remove the fallback in `GetLastActivityAt`.
- **Update Epic 23 README:** mark Stories 2+3 as shipped (Story 2 partial — helper landed, migration deferred).
- **Epic 29 remaining:** US-29.1 (AgentClient interface) is the next highest-value extraction.
