# Worklog: Epic 21 Change A — declarative recovery from Failed via spec.restartGeneration

**Date:** 2026-05-31
**Session:** Mike asked why a workspace stuck in `Failed` couldn't recover automatically and pushed for a real fix to the fragile design where `Failed` is terminal and only operator hand-edits can escape. Implemented Change A (declarative recovery via `spec.restartGeneration`), wired API endpoint, scoped Epic 21 for follow-on Changes B + C.
**Status:** Complete

---

## Objective

1. Make Failed workspaces recoverable via a declarative spec change instead of `kubectl patch --subresource=status`.
2. Surface that capability through the public API so the frontend can ship a "Retry" button.
3. Document Changes B (auto-retry with backoff) and C (failure-cause taxonomy) as the next two design steps in Epic 21 — not implementing now.

---

## Stated assumptions and validation

| # | Assumption | Validation |
|---|-----------|------------|
| A1 | The current `handleFailed` does nothing except secret cleanup; no recovery path exists from spec | Verified: controller.go:65-73 (pre-change) only calls `cleanupFailedWorkspaceSecrets` then returns. `RestartGeneration` is checked at controller.go:229 (handleActive) but nowhere in the Failed branch |
| A2 | Six distinct sites set `Phase=Failed`; five of them describe transient or non-input-error conditions (PVC issues, transient pod loss, transient pod-Failed-during-creation), so `Failed` shouldn't be terminal for any of them | Verified by reading controller.go:109, 140, 174, 215, 438. Only `buildPod` errors at line 174 are reliably permanent (e.g. invalid runtime image), but even those become recoverable once spec is fixed |
| A3 | The cleanup at line 71 deletes the password secret on every Failed reconcile, but `handlePending` recreates it on transition out of Failed | Verified: controller.go:148 calls `ensurePasswordSecret`, which `passwordSecretName(workspace.Name)` makes idempotent (creates if missing). Recovery generates a fresh password — the desired security posture after a failure |
| A4 | `WorkspaceInterface.Update(*v1.Workspace)` exists in the K8s client interface and can write spec | Verified: pkg/interfaces/kubernetes.go:41 |
| A5 | No active production users today, so backwards compat for the new behavior is irrelevant; existing CRDs with no observedRestartGeneration default to 0, which interacts correctly with the strict-greater-than check | Confirmed by user in this session |

---

## Work completed

### 1. Controller: `handleFailed` honors `spec.restartGeneration`

`controller/internal/workspace/controller.go:65-99` (post-change) now:

1. Cleans up secrets (preserved existing behavior — Bug 12 / worklog 0085).
2. **NEW:** if `spec.restartGeneration > status.observedRestartGeneration`, clears stale status fields (Message, PodName, PodNamespace, PodIP, Endpoint, TransientFailureCount, LastTransientFailureAt), increments `restartCount`, catches up `observedRestartGeneration`, and transitions to `Pending`.
3. Otherwise stays `Failed` (logging hint to operators that bumping the gen field will trigger retry).

The transition is to `Pending`, not `Creating`, so `handlePending` re-runs PVC verification and password-secret recreation. This is the same semantics as a fresh workspace creation, which is the desired clean-slate.

### 2. API: `RestartWorkspace` service method + `POST /api/v1/workspaces/:id/restart` route

New service method `Service.RestartWorkspace` (workspace_service.go:445-492):

1. Verifies ownership (same as Suspend/Resume).
2. Refuses Terminating/Terminated phases (would race with finalizer logic).
3. Bumps `spec.RestartGeneration++` and writes via `Update` (NOT `UpdateStatus` — restart is a spec change, not status).
4. The controller responds to the bump per Change A (from Failed) or its existing handling at controller.go:229 (from Active).

Wired into:

- `interfaces.WorkspaceService` (api/internal/interfaces/interfaces.go:118)
- `MockWorkspaceService` (api/internal/mocks/workspace.go:54)
- `POST /api/v1/workspaces/:id/restart` route (api/internal/server/router.go:561-577)
- Route presence test (api/internal/server/router_workspace_test.go:79)

### 3. Test coverage

| Test | What it verifies |
|---|---|
| `TestReconcile_Failed_NoBump_NoRecovery` | Pre-existing behavior: Failed without bump stays Failed. Replaces deleted `TestReconcile_Failed_NoAction` |
| `TestReconcile_Failed_RestartGenerationBump_Recovers` | Bumped gen → Pending; clears Message, Pod*, transient counters; increments restartCount; observedRestartGeneration catches up |
| `TestReconcile_Failed_RestartGenerationStale_NoRecovery` | Equal or lesser gen does not recover. Three sub-cases (equal, observed-ahead, zero-zero) |
| `TestReconcile_Failed_CleansUpSecrets` | Pre-existing test untouched; secret cleanup still works |
| `TestRestartWorkspace_FromFailed_BumpsRestartGeneration` | Service bumps spec, calls Update (not UpdateStatus), preserves status fields untouched (controller's job) |
| `TestRestartWorkspace_FromActive_BumpsRestartGeneration` | Force-restart Active workspace also works |
| `TestRestartWorkspace_WrongOwner_ReturnsForbidden` | Authz |
| `TestRestartWorkspace_FromTerminating_Rejected` | Two sub-cases (Terminating, Terminated); both rejected |
| `TestRestartWorkspace_K8sGetFails` | Get-error → no Update attempt |
| `TestRestartWorkspace_K8sUpdateFails` | Update-error → wrapped internal error |

10 new tests, all pass. Existing test `TestReconcile_Active_RestartGeneration_RecreatesPod` at controller_test.go:241 already covered the Active → recover path; left untouched.

### 4. Epic 21 design doc

`design/stories/epic-21-workspace-recovery/README.md` scopes Changes B and C with:

- Problem statement linking back to worklog 0099 (this session's Mike-thread)
- Change B (exponential backoff for transient pod loss) — proposed mechanism, status fields, schedule, acceptance criteria, edge cases, files to modify, risk assessment
- Change C (FailureReason taxonomy) — enum definition, per-cause retry policy table, frontend implications, acceptance criteria, files to modify
- Out of scope: agentd SSE-load performance; hot migration; cross-workspace blast radius
- Sequencing recommendation: B first (bigger UX win — auto-recovery without operator awareness), then C (mostly observability + frontend copy)
- Story breakdown: 9 stories US-21.1 through US-21.9

---

## Key Decisions

| Decision | Rationale |
|----------|-----------|
| Recovery transitions to `Pending` (not `Creating`) | `handlePending` is the clean-slate entry point — verifies PVC, recreates the password secret that `cleanupFailedWorkspaceSecrets` just deleted. Going straight to Creating would skip secret recreation and the pod would fail to mount |
| Change A is its own commit; Changes B and C are separate | A is a 20-line patch with 10 unit tests. B and C are each their own design with 6+ AC items and integration testing requirements. Bundling them would exceed the user's stated proportional response |
| `RestartWorkspace` rejects Terminating/Terminated explicitly | Those phases have finalizers actively cleaning up; mutating spec there could leave dangling resources or race conditions. Better to fail loudly with a 409 Conflict |
| Restart bumps spec in one call instead of read-modify-write loops | The K8s API serializes Update calls per-resource via resourceVersion; if two restart calls race, the second gets a `Conflict` and returns 500 to the user — that's correct behavior. No retry loop needed because the user can simply click again |
| `RestartCount` increments on Change A recovery | Existing field; observability metric. Operators can alert on workspaces that have been restarted frequently |
| Don't validate restart in CRD admission webhook | The webhook lives outside this commit's scope; the service-layer 409 on Terminating is sufficient. If we add admission validation later it should reject `restartGeneration` decreases (which is a malformed write) |

---

## Live cluster validation

Before this commit landed I confirmed the manual recovery (status patching) worked. After the controller image rolls out with Change A, the proper validation is:

```bash
# Recover c98963e7-… (currently Suspended after my prior status patch)
# via the new declarative path — proves Change A reaches the cluster
kubectl patch workspace -n default c98963e7-0cd5-42cb-9630-8f988b3e5f33 \
  --type=merge -p '{"spec":{"restartGeneration":1}}'

# Or via the API:
# curl -X POST -H "Authorization: Bearer $TOKEN" \
#   https://safespace.thekao.cloud/api/v1/workspaces/c98963e7-…/restart
```

Validation has to wait until CI builds `sha-<this-commit>` (~10 min) and a `helm upgrade` rolls the new controller image. The path is fully tested by `TestReconcile_Failed_RestartGenerationBump_Recovers` so the live test is just confirming the mechanism reaches production.

---

## Tests Run

```bash
# RED before fix
go test -run 'TestReconcile_Failed_RestartGenerationBump_Recovers' ./controller/internal/workspace/...
  → FAIL (status fields not cleared, phase stays Failed)

# GREEN after fix
go test -run 'TestReconcile_Failed' ./controller/internal/workspace/...
  → PASS — 4/4 tests

# Service-layer tests
go test -run 'TestRestartWorkspace_' ./api/internal/services/workspace/...
  → PASS — 6/6 tests (including 2 sub-cases for terminating phases)

# Full controller suite
go test -timeout 60s -race ./controller/...
  → ALL PASS

# Full api suite
go test -timeout 120s -race -short ./api/...
  → ALL PASS (auth tests need >60s due to bcrypt cost 12; with -timeout 120s clean)

# Static analysis
go vet ./...
  → clean

# Layout lint (epic-18 / worklog 0098 regression net)
make repolint
  → ok    migrations sequence (11 migrations, max version 11)
  → ok    worklogs sequence (97 worklogs, max 0099, grandfathered <0097)
  → ok    chart migrations match api/migrations/
```

---

## Files Modified

- `controller/internal/workspace/controller.go` — `handleFailed` honors RestartGeneration
- `controller/internal/workspace/controller_test.go` — 3 new tests (1 replacing the deleted no-action test)
- `api/internal/services/workspace/workspace_service.go` — new `RestartWorkspace` method
- `api/internal/services/workspace/workspace_service_test.go` — 6 new tests
- `api/internal/interfaces/interfaces.go` — `RestartWorkspace` in `WorkspaceService` interface
- `api/internal/mocks/workspace.go` — `RestartWorkspace` in mock
- `api/internal/server/router.go` — `POST /api/v1/workspaces/:id/restart`
- `api/internal/server/router_workspace_test.go` — route presence assertion
- `design/stories/epic-21-workspace-recovery/README.md` — new (scope of Changes B + C)

---

## Next Steps

1. **Commit + push to main**: pre-commit runs `make repolint`, then push triggers CI (Lint + Test + Build jobs from epic-18 / worklog 0098).
2. **Deploy** after CI green: `helm upgrade` to the new image sha; both API and controller need it (controller for `handleFailed`, API for the new endpoint).
3. **Validate on cluster**: `kubectl patch workspace c98963e7-… --type=merge -p '{"spec":{"restartGeneration":1}}'`; confirm phase walks Failed → Pending → Creating → Active and a fresh pod materializes.
4. **Frontend (separate session, not Epic 21)**: add a "Retry" button on Failed workspaces that calls `POST /workspaces/:id/restart`. This is a small UX delta independent of Changes B and C.
5. **Pick up Epic 21 stories US-21.1..21.4** (Change B) when the team next has cycles for it. US-21.5..21.9 (Change C) come after.
