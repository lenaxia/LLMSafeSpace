# Worklog — Epic 6 Cluster Test & Bug Fixes

**Date:** 2026-05-25  
**Operator:** opencode  
**Start state:** Helm release `llmsafespaces` rev 1 on sha-9b8e9ff (Epic 6 complete)  
**End state:** Helm release `llmsafespaces` rev 15 on sha-4c2f4a9  

---

## Phase 1: Initial Deployment Verification (sha-9b8e9ff)

- Uninstalled old Helm release (rev 6, pre-Epic-6)
- Deleted all 4 CRDs (workspaces, runtimeenvironments, sandboxes, sandboxprofiles)
- Fresh `helm install` with Epic 6 images
- Verified CRDs: only `workspaces` and `runtimeenvironments` (no sandbox/sandboxprofile)
- All pods running: api (2/2), controller (1/1), frontend (1/1)

## Phase 2: Initial Test Suite (T1-T9) — All PASS

| Test | Result | Notes |
|------|--------|-------|
| T1: Controller healthy | PASS | Leader elected, watching Secrets+Pods+Workspaces |
| T2: API healthy | PASS | `/livez` and `/readyz` returning 200 |
| T3: CRD schema | PASS | `spec.runtime`, `status.podIP`, phase enum includes `Creating` |
| T4: RuntimeEnvironment | PASS | `base` seeded by Helm |
| T5: Create→Active | PASS | Pending→Creating→Active in ~30s, podIP populated |
| T6: EnsureSession | PASS | `sessionId` returned, 63ms on active workspace |
| T7: Send message | PASS | Agent responded "Alpha" in 1.7s |
| T8: Suspend/Resume | PASS | Pod deleted, PVC preserved, new podIP on resume |
| T9: Delete | PASS | Full cleanup: Workspace, PVC, Secret all NotFound |

## Phase 3: Bug Discovery and Fixes

### Bug #1: Duplicate email returns 500 instead of 409
- **File:** `api/internal/services/auth/auth.go:330-338`
- **Root cause:** `Register()` returns generic `errors.New("registration failed")` for all errors
- **Fix:** Return `NewConflictError` for duplicate email path
- **Additional fix:** `router.go:217` used hardcoded `http.StatusInternalServerError` — changed to `respondWithError(c, err)`
- **PR:** #1 (merged as bae94013)

### Bug #2: Proxy timeout too short (30s)
- **File:** `api/internal/handlers/proxy.go:89`
- **Root cause:** Hardcoded `ResponseHeaderTimeout: 30s` kills agent ops >30s
- **Fix:** Increased to 120s initially, later 300s
- **Commits:** 9b8e9ff→be91d77→b6777df

### Bug #3: Session index never wired
- **Files:** `api/internal/app/app.go`, `api/internal/handlers/proxy.go`
- **Root cause:** `SetSessionIndex` never called, `Start()` never called, `proxyToWorkspace` never called `getMaxSessions` so `wsConfig` was never populated
- **Fix:** Wired `SessionIndexService` in `app.go`, added `Start()` call, added direct `RecordMessage` call in proxy path
- **PRs:** #1, #2 (merged as c766f11)

### Bug #4: Frontend sessionsApi.create hits wrong route
- **File:** `frontend/src/api/sessions.ts:4-5`
- **Root cause:** Calls `POST /workspaces/{id}/sessions` but API only registers `/sessions/new`
- **Fix:** Changed to `/sessions/new`, fixed response type from `{id}` to `{sessionId}`
- **PR:** Included in earlier fix commits

### Bug #5: SSE backoff never resets
- **File:** `api/internal/handlers/session_tracker.go`
- **Root cause:** Backoff stays at 30s after early failures, never resets to 2s after success
- **Fix:** Added backoff reset after successful `connectAndRead`
- **PR:** Included in earlier fix commits

### Bug #6: Empty workspace ID crashes /status
- **File:** `api/internal/services/workspace/workspace_service.go`
- **Root cause:** Empty string passed as UUID to Postgres `WHERE id = $1` on UUID column
- **Fix:** Early validation in `verifyOwner` / `GetWorkspace`
- **PR:** Included in earlier fix commits

## Phase 4: Frontend UX Fixes

### Chat silently fails on 401
- **Files:** `frontend/src/hooks/useChatStream.ts`, `frontend/src/api/client.ts`
- **Root cause:** No catch block in `useChatStream.send`, no 401 redirect in API client
- **Fix:** Added error state, added `handleUnauthorized` redirect to `/login` on 401
- **Commit:** 27d8e21

### Mobile chat input auto-zoom
- **File:** `frontend/src/components/chat/Composer.tsx`
- **Root cause:** `text-sm` = 14px, iOS auto-zooms inputs <16px
- **Fix:** Changed to `text-base` (16px), added `maximum-scale=1.0` to viewport meta
- **Commit:** 620499a

### Session switch shows shared history
- **File:** `frontend/src/pages/ChatPage.tsx`
- **Root cause:** `localMessages` state never cleared on sessionId change
- **Fix:** Added `useEffect(() => setLocalMessages([]), [sessionId])`
- **Commit:** 1527fae

### Message history not loading
- **File:** `frontend/src/api/messages.ts`
- **Root cause:** API returns `{info:{role:"user"},parts:[...]}` but frontend expects `{role:"user",parts:[...]}`
- **Fix:** Added `transformHistory()` to map opencode format to frontend `Message[]`
- **Commit:** 2bd75b1

## Phase 5: UX Overhaul

### Hierarchical sidebar
- **File:** `frontend/src/components/layout/Sidebar.tsx`
- **Changes:** Workspaces expand to show nested sessions, chevron toggle, color-coded phase dots
- **Commit:** 857ca60

### 24h idle auto-suspend
- **Files:** `pkg/apis/llmsafespaces/v1/workspace_types.go`, `controller/internal/workspace/controller.go`
- **Changes:** CRD default 3600s→86400s, enabled by default, implemented idle check in `handleActive()`
- **Commit:** 857ca60

### Mobile swipe gestures
- **File:** `frontend/src/components/layout/AppShell.tsx`
- **Changes:** Touch handlers with `preventDefault()` on edge swipes to block browser back gesture
- **Commits:** 857ca60→1295bca→c2e38cb

### Resume button, auto-select session, new session button
- **File:** `frontend/src/components/layout/Sidebar.tsx`
- **Changes:** Click suspended workspace auto-resumes, expanding workspace navigates to latest session, "+" button per workspace
- **Commits:** 27d8e21→aae5ea3→32263a6

### Workspace creation spinner
- **File:** `frontend/src/pages/ChatPage.tsx`, `frontend/src/hooks/useWorkspaces.ts`
- **Changes:** Show spinner during Pending/Creating phases, poll during Pending
- **Commit:** aae5ea3

## Phase 6: Workspace State Reconciliation

### Problem
- `ListWorkspaces` queried PostgreSQL (no phase), CRD had phase → divergence
- Orphaned DB rows for deleted CRDs showed phantom workspaces in UI
- No way to track PVC state (cluster vs S3)

### Solution: DB-backed phase tracking
- **Migration 000005:** Added `phase` and `pvc_state` columns to workspaces table
- **PVCState enum:** `""` (none), `"cluster"`, `"s3"` — ready for future S3 offload
- **Fire-and-forget sync:** Every CRD touch point calls `syncPhase()` goroutine
- **Lazy orphan cleanup:** CRD NotFound triggers `markDeleted()` (soft delete via `deleted_at`)
- **ListWorkspaces:** Now includes phase, filters `deleted_at IS NULL`
- **DeleteWorkspace:** Soft delete (sets `deleted_at`) instead of hard delete

### Files changed
- `api/migrations/000005_workspace_state.up.sql` — new columns
- `api/internal/services/database/database.go` — SyncWorkspacePhase, MarkWorkspaceDeleted, updated queries
- `api/internal/services/workspace/workspace_service.go` — syncPhase helper, orphan cleanup, soft delete
- `api/internal/interfaces/interfaces.go` — new interface methods
- `pkg/apis/llmsafespaces/v1/workspace_types.go` — PVCState constants
- `pkg/types/types.go` — Phase, PVCState on WorkspaceMetadata
- Tests updated: `database_test.go`, `workspace_service_test.go`, `mocks/database.go`

### Orphan cleanup
- Seeded phase for 7 active workspaces
- Marked 19 orphaned DB rows as deleted (CRDs gone but DB rows remained)
- Result: 7 clean workspaces with `phase=Active, pvc_state=cluster`

---

## Deployment History

| Rev | SHA | Summary |
|-----|-----|---------|
| 1 | 9b8e9ff | Initial Epic 6 fresh install |
| 2-3 | 9b8e9ff | Iterative fixes (values, config) |
| 4-5 | be91d77→c766f11 | Bug fixes (409, session index, migration) |
| 6 | f0a4a0a | Frontend chat fixes (401 redirect, history transform) |
| 7 | 620499a | Mobile zoom fix |
| 8 | 1527fae→2bd75b1 | Session switch, message history |
| 9-10 | 857ca60→1295bca | UX overhaul (sidebar, idle timeout, swipe) |
| 11-14 | c2e38cb→aae5ea3 | Swipe fix, session button, creating spinner |
| 15 | 4c2f4a9 | Workspace state reconciliation |

---

## Current State

- **Helm release:** llmsafespaces rev 15 on sha-4c2f4a9
- **Namespace:** default
- **Ingress:** https://safespace.thekao.cloud (TLS via letsencrypt-production)
- **Components:** api (2 replicas), controller (1), frontend (1), MCP disabled
- **DB:** 7 active workspaces, 19 soft-deleted orphans, session_index populated
- **All CI green** on latest commit
