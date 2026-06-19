# Worklog: Suspend/Resume Debug & CrashLoopBackOff Detection

**Date:** 2026-05-29
**Session:** Debug why workspaces eb598645 and c3bce097 cannot resume; fix stale DB phases, non-idempotent suspend/resume, read-only mount crash, and CrashLoopBackOff detection gap
**Status:** Complete

---

## Objective

Users reported that workspaces `eb598645-245e-40ca-a6ba-cdea88fb2b11` and `c3bce097-a5a7-42ab-999b-2b7d2aba70c2` could not resume. Both were stuck in `Suspended` phase. The system had broader problems with suspend and resume flows that needed investigation and fix.

---

## Work Completed

- Investigated workspace CRD states via `kubectl get workspace -o yaml` on default namespace — both in `Suspended` phase, `c3bce097` had stale DB phase of "Resuming"
- Traced API logs (`kubectl logs deploy/llmsafespaces-api`) — found `enforceMaxActiveWorkspaces` trying to suspend `3facbcb6` which was already `Suspended` on the live K8s CRD, producing "cannot suspend workspace in phase Suspended" error and blocking activation of `eb598645`
- Root caused three bugs (commit `ce79464`):
  - `enforceMaxActiveWorkspaces` (`max_active.go:43-49`) read workspace phases from PostgreSQL. When the controller auto-suspends, it updates K8s CRDs but the DB is only synced when the API explicitly calls `syncPhase`. Stale DB phases caused the API to count already-suspended workspaces as active, attempt to suspend them, and fail.
  - `SuspendWorkspace` (`workspace_service.go:416-422`) rejected requests for already-suspended/suspending workspaces instead of returning nil (not idempotent).
  - `ResumeWorkspace` (`workspace_service.go:454-465`) rejected requests for already-active/creating/resuming workspaces instead of returning nil (not idempotent).
- Fix: added `verifyActivePhases()` to `max_active.go` — queries live K8s CRD for each DB-"Active" workspace before counting toward cap. Stale entries excluded and DB synced. Made both `SuspendWorkspace` and `ResumeWorkspace` idempotent for terminal/in-progress states.
- 8 new tests covering stale DB scenarios and idempotent suspend/resume.
- After deploying API fix, `eb598645` resumed successfully but pod entered CrashLoopBackOff with error `/sandbox-cfg/env: Read-only file system`.
- Root caused two more bugs (commit `538a72b`):
  - `entrypoint-common.sh` wrote `ENV_FILE` to `/sandbox-cfg/env` (line 9), which is mounted read-only on the main container. Fixed by changing to `/tmp/secrets-env` (writable EmptyDir, already sourced by `entrypoint-opencode.sh:12-14`).
  - `handleActive` in `controller.go:269-272` only checked `pod.Status.Phase != Running` — K8s reports CrashLoopBackOff pods as `Running` with IP. Added container state check for `CrashLoopBackOff` waiting reason, triggers `recoverFromTransientPodLoss`.
- Deployed all components: API (`sha-ce79464`), controller (`sha-538a72b`), updated `RuntimeEnvironment base` to `sha-538a72b`.
- Verified controller correctly detects CrashLoopBackOff and transitions workspace to `Failed` after max retries.
- Discovered new issue: `workspace-agentd --supervise` exits immediately with code 0 in the new runtime image. Pod cycles between `Completed` and restart. This is a separate runtime image issue (binary may be missing/misconfigured), not a suspend/resume issue.

---

## Key Decisions

- **`ENV_FILE` to `/tmp/secrets-env` instead of making `sandbox-cfg` read-write:** The read-only mount is an intentional security boundary — init container writes secrets, main container only reads them. Writing derived env state to `/tmp` (separate EmptyDir) preserves this boundary.
- **`verifyActivePhases` queries K8s directly rather than adding a periodic DB sync:** Adding a background sync loop would be more complex and still have a window for stale data. Querying K8s at enforcement time is deterministic and only costs N extra K8s API calls when at/above the active cap.
- **Idempotent suspend/resume returns nil with `syncPhase` call:** Ensures the DB phase is always corrected on retry, even if the original trigger was a stale read.

---

## Blockers

- `workspace-agentd --supervise` exits immediately with code 0 in runtime image `sha-538a72b`. No logs produced. Pod enters CrashLoopBackOff cycling `Completed` → restart. This blocks `eb598645` (and potentially other workspaces) from reaching a fully functional `Active` state. Need to investigate whether the agentd binary is present in the image and why it exits cleanly.

---

## Tests Run

- `go test -timeout 120s -race -run 'TestSuspendWorkspace|TestResumeWorkspace|TestEnforceMaxActive|TestE2E_Suspend|TestE2E_Resume' ./api/internal/services/workspace/` — all 29 tests PASS
- `go vet ./api/internal/services/workspace/` — clean
- `go vet ./controller/internal/workspace/` — clean
- CI run `26618109413` (commit `ce79464`) — all 6 jobs PASS
- CI run `26618974997` (commit `538a72b`) — all 6 jobs PASS

---

## Next Steps

1. Investigate why `workspace-agentd --supervise` exits with code 0 and no output in runtime base image `sha-538a72b`. Check if the binary exists in the image (`kubectl exec` or `docker run` the image and run `which workspace-agentd`). If missing, check the Dockerfile build stage that copies it.
2. After fixing agentd, re-verify `eb598645` resumes to a healthy `Active` state with pod responding on port 4096.
3. Verify `c3bce097` resume flow (user has not triggered activation yet — workspace is still `Suspended`).
4. Consider adding a controller test for the CrashLoopBackOff detection path (existing tests timed out due to slow build, but `go vet` passed).

---

## Files Modified

- `api/internal/services/workspace/max_active.go` — added `verifyActivePhases()`, `isActivePhase()`, new imports
- `api/internal/services/workspace/max_active_test.go` — 3 new stale DB tests, `SyncWorkspacePhase` on mock, updated `TestEnforceMaxActive_AtCap_SuspendsStalest` mock data
- `api/internal/services/workspace/workspace_service.go` — idempotent `SuspendWorkspace` (Suspended/Suspending), idempotent `ResumeWorkspace` (Active/Creating/Resuming)
- `api/internal/services/workspace/workspace_service_test.go` — 5 new idempotency tests, updated E2E test tables
- `controller/internal/workspace/controller.go` — CrashLoopBackOff container state check in `handleActive`
- `runtimes/base/tools/entrypoints/entrypoint-common.sh` — `ENV_FILE` to `/tmp/secrets-env`, removed stale write to `$CREDS_FILE`
