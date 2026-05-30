# Worklog: Workspace phase divergence — debug + fix

**Date:** 2026-05-30
**Session:** Debug 3 broken workspaces a user reported (2 not fully launching, 1 stuck Suspended). Found and fixed two latent bugs, then a follow-on UX bug surfaced by the fix.
**Status:** Two bugs fixed and deployed. One UX issue (chat-page polling gap) discovered at the end and deferred.

---

## Context

User reported three workspaces in production:
- `ce4c03b1`, `3017590d` — appeared as grey dots in the sidebar, "can't start a session"
- `51b1c286` — stuck showing as paused, resume didn't work

The platform is `default`-namespace Helm release `llmsafespace`. The user has cluster access via `admin@home-kubernetes`. `default`-ns Postgres holds the API's metadata DB.

---

## How I almost screwed this up

The user pushed back hard mid-session: "are you really sure about the findings? state your assumptions, and verify them." I had been speculating from partial evidence and presenting it as conclusion. After that nudge I retracted three assumptions, listed what I had vs hadn't verified, and ran live commands instead of guessing. The user pushed back twice more later ("you cannot make assumptions you haven't verified", "test driven design?") when I drifted again. Each time the result was better data.

Concrete examples of speculation I had to retract:
- "Edge fired zero /status calls so the browser is broken" — refuted: Edge fires /status fine; the user simply hadn't been on the chat page in that window.
- "The frontend never re-polls /status" — partially refuted: it DOES on chat-page mount; what doesn't poll is the post-mount cycle (which is the actual bug, but for a different reason).
- "DB-CRD divergence is purely a UX bug" — that part held up; the deeper question of whether to drop the DB columns required separate validation.

The lesson is repetitive but kept being needed: tail logs, SQL the actual table, watch the controller in real time. Don't infer from "tools say X" without proving X.

---

## What was actually wrong

### Bug 1 — DB phase cache diverges from CRD on every workspace creation

`api/internal/services/workspace/workspace_service.go::CreateWorkspace` (former line 219) called `s.syncPhase(created.Name, created.Status.Phase)` immediately after `Create`. The freshly-returned CRD has `Status.Phase=""` because the controller hasn't reconciled yet. `syncPhase` short-circuits on empty phase (former lines 45-47), so the DB row's `phase` column stayed at its schema default (empty).

`ListWorkspaces` reads from the DB and the response struct uses `Phase string \`json:"phase,omitempty"\``. Empty phase → no `phase` field in the JSON → frontend `Sidebar.tsx:280` `isActive = workspace.phase === "Active"` is `false` → grey dot.

The DB only updates later if some other code path calls `syncPhase` with a non-empty phase. The path that does is `GetWorkspaceStatus` (former line 491), which is only called from `ChatPage.tsx::useWorkspaceStatus`. So the workspace stays grey in the sidebar **until the user opens its chat page**. Refresh alone doesn't fix it.

Verified directly:
- DB: `phase` column empty for both reported workspaces (psql showed `<EMPTY>`).
- API logs: only ONE `/status` call ever for those two workspaces, ~190ms after creation, both returning `phase: ""`.

### Bug 2 — Resume re-suspends within ~12 seconds

`controller.go::handleResuming` (line 336-344) and `workspace_service.go::ResumeWorkspace` (line 460) both transition the phase but never reset `Status.LastActivityAt`. The next reconcile lands in `handleActive` (line 281-289), which compares `now − LastActivityAt > idleTimeout` and re-suspends.

For `51b1c286` the controller logged three suspend events:
- 23:06:00 (first idle, legit, lastActivity 22:06:00, idle 1h0m0s)
- 23:31:41 (re-suspend after first user-driven resume, still using lastActivity 22:06:00, idle 1h25m41s)
- 23:33:50 (third loop, idle 1h27m50s)

I reproduced this live by patching `phase=Resuming` on the CRD at 05:07:01Z; the controller suspended again at 05:07:13Z with `lastActivity` still at 22:06:00.

### Non-bug — Edge "fired zero /status calls"

Sub-agent reported Edge fired no /status. The user clarified they'd been on `/settings`, not the chat page. ChatPage hasn't mounted → useWorkspaceStatus didn't run → no /status. After they navigated to the chat URL directly, /status fired and worked normally. Edge was fine.

---

## Fixes

### Fix 1 — drop the DB phase cache

The DB column was a denormalised cache populated by `syncPhase` from the CRD. With the cache gone, the CRD is the only source of truth. `ListWorkspaces` now does one label-scoped CRD list per call (`LabelSelector: "user-id=<uuid>"`) and joins phase by workspace name. On k8s API failure the items return with empty phase — fine, because every other workspace operation also depends on k8s, so there's no degraded-mode worth fabricating.

The `pvc_state` column was written by the same `syncPhase` and read by zero code paths (verified by grep across api/, controller/, pkg/). Dropped at the same time.

`SyncWorkspacePhase` removed from the `DatabaseService` interface and from three test mocks (`MockDatabaseService`, `fullMockDB`, `mockDB`). `WorkspaceMetadata.Phase` and `.PVCState` removed from the struct. SQL SELECTs in `database.go` updated. `MarkWorkspaceDeleted`'s SQL no longer sets `phase = 'Deleted'`.

### Fix 2 — reset LastActivityAt on resume

Belt-and-suspenders, both sides:
- `controller.go::handleResuming`: now sets `LastActivityAt = metav1.Now()` alongside the existing phase/SuspendedAt updates.
- `workspace_service.go::ResumeWorkspace`: also sets it when flipping the phase to Resuming.

### `enforceMaxActiveWorkspaces` refactor

This was the only function that read `WorkspaceMetadata.Phase`. Previously it pre-filtered by DB phase (`Running`/`Active`), then re-verified each candidate via N k8s GETs (`verifyActivePhases`). Refactored to use `fetchUserWorkspacePhases` (one LIST) directly, which is simpler and also drops the dead `verifyActivePhases` helper.

---

## TDD discipline

Wrote Red tests for the new contracts before changing production code:
- `pkg/types/types_test.go::TestWorkspaceMetadata_DoesNotCachePhaseOrPVCState` — reflective struct test, fails until fields are removed.
- `api/internal/interfaces/interfaces_test.go::TestDatabaseService_NoSyncWorkspacePhase` — reflective interface test, fails until method is removed.
- `api/internal/services/database/database_test.go::TestGetWorkspace`/`TestListWorkspaces` — updated to expect new SELECT shape (without `phase`, `pvc_state`).
- `api/internal/services/workspace/workspace_service_test.go::TestListWorkspaces_*` — updated existing happy/empty/db-fail tests to mock the new k8s LIST call; added `TestListWorkspaces_K8sListFails_ReturnsItemsWithEmptyPhase` and `TestListWorkspaces_CRDMissing_PhaseEmpty`.
- `api/internal/services/workspace/workspace_service_test.go::TestResumeWorkspace_HappyPath` — captures the UpdateStatus payload and asserts `LastActivityAt` is recent.
- `controller/internal/workspace/controller_test.go::TestReconcile_Resuming_TransitionsToCreating` — same assertion at the controller layer.
- `api/internal/services/workspace/max_active_test.go` — DB rows no longer carry `Phase: "Running"`; tests rely entirely on CRD mocks.

All Red, then I made production changes one source-tree-error at a time using LSP feedback to drive fixups. All Green. `go vet` clean. `go test -count=1 ./...` clean.

---

## Migration

`migrations/000009_drop_workspace_phase_cache.up.sql`:

```sql
DROP INDEX IF EXISTS idx_workspaces_phase;
ALTER TABLE workspaces DROP COLUMN IF EXISTS pvc_state;
ALTER TABLE workspaces DROP COLUMN IF EXISTS phase;
```

Plus the down migration to recreate them with the same defaults as migration 5. Mirrored into both `charts/llmsafespace/migrations/` (Helm bundles these into a ConfigMap for the migration job) and `api/migrations/` (the legacy location).

---

## What I screwed up during deploy

I applied the migration to the live DB **before** deploying the new code. The currently-running API pods were the old code that SELECTs `phase`, `pvc_state`. Every list/get request 500'd with `column "phase" does not exist` for ~9 minutes. The user noticed: "have you deployed the code yet?" Correct order would have been: deploy code first (which is forward-compatible because it doesn't read those columns), then run the migration. Lesson logged.

Once they pointed it out, the recovery was straightforward — `git commit && git push && gh run watch && helm upgrade --reuse-values --set api.image.tag=sha-cdd6305 --set controller.image.tag=sha-cdd6305 --set frontend.image.tag=sha-cdd6305`. Rollout was clean within 30 seconds.

---

## Live verification after deploy

- Resume of `de04f989`: user clicked resume in UI, CRD went Suspended → Active, lastActivityAt=05:57:46, no re-suspend in the next 3+ minutes. Fix 2 working.
- New workspace `c98963e7`: created at 05:59:07, controller reconciled to Creating by 05:59:14, eventually Active. Sidebar list returned correct phase (no grey dot).
- Manually-unstuck `51b1c286` (patched earlier in session) still Active.

---

## Follow-on bug surfaced (deferred)

`c98963e7`'s API trace showed only TWO `/status` calls on the chat page (at 05:59:07 and 05:59:14, both returning `Creating`), then nothing. No SSE `/events` connection ever opened. Inspecting the frontend:

- `useWorkspaceStatus` is documented as "fetch once, no polling" and relies on SSE for updates.
- `useEventStream` returns early if `workspaceId` is undefined.
- `ChatPage.tsx:63` sets `activeWorkspaceId = isReady ? workspaceId : undefined`, where `isReady = phase === "Active"`.
- Result: while phase is "Creating", SSE doesn't open, so the chat page never learns when phase transitions to Active. The user has to refresh.

This is a separate frontend bug from anything in today's commit. Filing here because we found it while verifying today's fix; should be a follow-up. Two reasonable shapes:
1. Add periodic refetch in `useWorkspaceStatus` while phase is non-Active (simple; UI eventually-consistent in ~5s).
2. Open SSE on a per-user events stream that's not gated on workspace phase, so phase transitions push through immediately.

---

## Files changed

```
 api/internal/interfaces/interfaces.go              |  1 -
 api/internal/interfaces/interfaces_test.go         | new
 api/internal/mocks/database.go                     |  4 --
 api/internal/services/auth/auth_e2e_secrets_test.go|  1 -
 api/internal/services/auth/auth_sessionid_test.go  |  1 -
 api/internal/services/database/database.go         | 26 ++------
 api/internal/services/database/database_test.go    | 18 +++---
 api/internal/services/workspace/max_active.go      | 34 ++--------
 api/internal/services/workspace/max_active_test.go | 71 +++++++++++++-------
 api/internal/services/workspace/workspace_service.go | 75 +++++++++++-----------
 api/internal/services/workspace/workspace_service_test.go | 73 ++++++++++++++++++++-
 api/migrations/000009_drop_workspace_phase_cache.{up,down}.sql | new
 charts/llmsafespace/migrations/000009_drop_workspace_phase_cache.{up,down}.sql | new
 controller/internal/workspace/controller.go        |  6 ++
 controller/internal/workspace/controller_test.go   |  7 ++
 pkg/types/types.go                                 | 10 ++-
 pkg/types/types_test.go                            | 13 ++++
```

Commit `cdd6305`, deployed as helm revision 68, image tag `sha-cdd6305`.

---

## Open questions

- The chat-page polling gap is real and reproducible. Worth fixing soon — it's the user-visible symptom that drove the original report ("can't start a session").
- The `/sessions` background polling continues even when the user is on `/settings` (saw Edge make these calls without ChatPage mounted). Probably from `useQuery` with `refetchOnWindowFocus`. Not a bug per se, but worth understanding before adding more polling.
