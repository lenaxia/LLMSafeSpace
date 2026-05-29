# Worklog — Suspend/Resume Debug & CrashLoopBackOff Detection

**Date:** 2026-05-29
**Operator:** opencode
**Start state:** main at sha-23a2c13
**End state:** main at sha-538a72b

---

## Summary

Debugged and fixed three bugs preventing workspace suspend/resume from working, plus a CrashLoopBackOff detection gap in the controller and a read-only mount crash in the runtime entrypoint.

**Commits:** 2 (ce79464, 538a72b)
**Test packages:** 34 pass, 0 failures (Go +race)
**Components changed:** API (workspace service), controller (handleActive), runtime base (entrypoint)

---

## Investigation

### Reported Issue

Workspaces `eb598645` and `c3bce097` could not resume. Both were stuck in `Suspended` phase in K8s CRDs.

### Root Cause Analysis

Three bugs blocked the resume flow:

| # | Bug | File | Impact |
|---|-----|------|--------|
| 1 | `enforceMaxActiveWorkspaces` read phases from PostgreSQL, but DB goes stale when controller auto-suspends (no sync) | `api/.../max_active.go:43-49` | API counted already-suspended workspaces as active, tried to suspend them, hit "cannot suspend workspace in phase Suspended" error, blocked activation |
| 2 | `SuspendWorkspace` rejected requests for already-suspended/suspending workspaces | `api/.../workspace_service.go:416-422` | No idempotency — retries or concurrent requests errored |
| 3 | `ResumeWorkspace` rejected requests for already-active/creating/resuming workspaces | `api/.../workspace_service.go:454-465` | c3bce097 stuck at "Resuming" in DB but "Suspended" in K8s — resume attempt rejected |

Additional issues found during verification:

| # | Bug | File | Impact |
|---|-----|------|--------|
| 4 | `entrypoint-common.sh` wrote `ENV_FILE` to `/sandbox-cfg/env`, mounted read-only on main container | `runtimes/base/.../entrypoint-common.sh:9` | Pod CrashLoopBackOff: `Read-only file system` |
| 5 | `handleActive` only checked `pod.Status.Phase != Running`; K8s reports CrashLoopBackOff pods as `Running` | `controller/.../controller.go:269-272` | Workspace stayed `Active` with green dot while pod was crash-looping |

---

## Fixes

### Commit ce79464 — Stale DB phases block workspace resume

**api/internal/services/workspace/max_active.go:**
- Added `verifyActivePhases()` method: when at/above active cap, queries live K8s CRD for each DB-"Active" workspace. Stale entries (actually Suspended/Failed/etc.) are excluded and their DB phase synced via `syncPhase()`.
- Added `isActivePhase()` helper (Active, Creating, Resuming = active).

**api/internal/services/workspace/workspace_service.go:**
- `SuspendWorkspace`: returns nil for `Suspended`/`Suspending` phases (idempotent).
- `ResumeWorkspace`: returns nil for `Active`/`Creating`/`Resuming` phases (idempotent).

**Tests added (8 new):**
- `TestEnforceMaxActive_StaleDBPhase_SkipsAlreadySuspended`
- `TestEnforceMaxActive_StaleDBPhase_AllStale_NoSuspension`
- `TestEnforceMaxActive_StaleDBPhase_Mixed_SuspendsCorrectStalest`
- `TestSuspendWorkspace_Idempotent_AlreadySuspended`
- `TestSuspendWorkspace_Idempotent_AlreadySuspending`
- `TestResumeWorkspace_Idempotent_AlreadyActive`
- `TestResumeWorkspace_Idempotent_AlreadyResuming`
- `TestResumeWorkspace_Idempotent_AlreadyCreating`

### Commit 538a72b — CrashLoopBackOff detection & read-only entrypoint fix

**controller/internal/workspace/controller.go:**
- `handleActive`: added container state check after pod phase check. Iterates `pod.Status.ContainerStatuses` for `CrashLoopBackOff` waiting reason, triggers `recoverFromTransientPodLoss`.

**runtimes/base/tools/entrypoints/entrypoint-common.sh:**
- Changed `ENV_FILE` from `/sandbox-cfg/env` (read-only mount) to `/tmp/secrets-env` (writable EmptyDir, already sourced by `entrypoint-opencode.sh:12-14`).
- Removed stale write to `/sandbox-cfg/credentials` for `llm-provider` secret type (read-only mount; `/tmp/agent-config.json` is sufficient).

---

## Deployment

| Component | Image | Deployed |
|-----------|-------|----------|
| API | `sha-ce79464` | Manual `kubectl set image` |
| Controller | `sha-538a72b` | Manual `kubectl set image` |
| RuntimeEnvironment `base` | Updated to `sha-538a72b` | `kubectl patch runtimeenvironment base` |

---

## Verification

- `eb598645` successfully transitioned `Suspended` → `Resuming` → `Creating` → `Active` — API and controller resume flow confirmed working.
- Controller detected CrashLoopBackOff and correctly transitioned workspace to `Failed` after max retries.
- All 34 suspend/resume/enforce tests pass with `-race`.

---

## Open Issues

### workspace-agentd exits immediately (exit code 0)

After the entrypoint fix, the workspace container starts but `exec workspace-agentd --supervise` exits immediately with code 0 and no output. Pod enters CrashLoopBackOff cycling between `Completed` and restart. This is a **runtime image issue** (agentd binary may be missing or misconfigured in the new build), separate from the suspend/resume flow.

---

## Assumptions Stated and Validated

| # | Assumption | Validated How |
|---|-----------|---------------|
| A1 | `/tmp` is a separate EmptyDir volume, not on PVC | `kubectl describe pod` confirmed EmptyDir for `/tmp` |
| A2 | `syncPhase` goroutine handles context correctly | Read `syncPhase()` — uses `context.Background()`, `defer recover()` |
| A3 | CrashLoopBackOff pods report `pod.Status.Phase == Running` | K8s behavior confirmed via `kubectl get pod` + `describe` output |
| A4 | `/tmp/secrets-env` is sourced by entrypoint-opencode.sh | Read lines 12-14: `if [[ -f /tmp/secrets-env ]]; then source /tmp/secrets-env; fi` |
| A5 | `sandbox-cfg` read-only mount is intentional security boundary | Init container writes secrets; main container should only read them |
