# Worklog: Auto-Session Creation on Workspace Create

**Date:** 2026-05-24
**Session:** Implement automatic sandbox + session creation when workspace is created or session is requested
**Status:** Complete

---

## Objective

Eliminate manual steps for users to get a functional chat session. Previously: create workspace → create sandbox → wait for Running → create session. Now: create workspace (or request session) → everything happens automatically.

---

## Work Completed

### New endpoint: `POST /workspaces/:id/sessions/new` (EnsureSession)

A synchronous endpoint that guarantees a functional session exists. Handles ALL workspace states:

| Starting State | Behavior |
|---|---|
| Active workspace, sandbox Running | Creates session immediately |
| Active workspace, sandbox Creating/Pending | Waits for Running (500ms poll, 60s timeout) |
| Active workspace, sandbox Failed/Terminated | Creates new sandbox, waits, creates session |
| Active workspace, no sandbox | Creates sandbox, waits, creates session |
| Suspended workspace | Resumes workspace, creates/finds sandbox, waits, creates session |
| Terminated/Failed workspace | Returns validation error |

### Auto-sandbox on workspace creation

`CreateWorkspace` now auto-creates a sandbox via `SetSandboxService` (setter injection to break circular dependency). If sandbox creation fails, workspace creation still succeeds (non-blocking, logged as warning).

### Frontend simplification

- `Sidebar.tsx`: Both "new workspace" and "new session" flows now use `workspacesApi.ensureSession()` — single call, blocks until ready, navigates to session.
- Removed the old multi-step flow (getSandboxes → find Running → createSession → error if not ready).
- Added `EnsureSessionResponse` type to frontend API layer.

---

## Key Decisions

1. **Synchronous blocking endpoint** — User said "not return until sandbox and session are created." The endpoint blocks up to 60s polling sandbox status at 500ms intervals.
2. **Setter injection for sandbox service** — Workspace service is created before sandbox service (sandbox depends on workspace). Used `SetSandboxService()` pattern (same as `SetSessionIndex()`) to break the circular dependency.
3. **OpencodePort in Config** — Made the opencode port configurable so tests can use httptest servers without port conflicts.
4. **Non-blocking auto-sandbox in CreateWorkspace** — Sandbox failure during workspace creation is a warning, not an error. The workspace is still usable; user can retry via EnsureSession.

---

## Blockers

None.

---

## Tests Run

```
go test -timeout 60s -short ./... → 30 packages pass, 0 failures

New tests (12 total):
- TestEnsureSession_ActiveWorkspace_RunningSandbox
- TestEnsureSession_SuspendedWorkspace_ResumesAndCreates
- TestEnsureSession_TerminatedWorkspace_ReturnsError
- TestEnsureSession_FailedWorkspace_ReturnsError
- TestEnsureSession_FailedSandbox_CreatesNew
- TestEnsureSession_CreatingSandbox_WaitsForRunning
- TestEnsureSession_WrongOwner_ReturnsForbidden
- TestCreateWorkspace_AutoCreatesSandbox
- TestEnsureSession_Route_Success
- TestEnsureSession_Route_Resumed
- TestEnsureSession_Route_Unauthorized
- TestEnsureSession_Route_ServiceError
```

---

## Next Steps

1. Frontend TypeScript tests for the updated Sidebar component (ensureSession mutation)
2. Consider adding SSE progress events during the wait (so frontend can show "Creating sandbox..." → "Starting..." → "Ready")
3. Integration test against real cluster to validate the full flow end-to-end

---

## Files Modified

```
api/internal/interfaces/interfaces.go              — Added EnsureSession to WorkspaceService interface
api/internal/mocks/workspace.go                    — Added EnsureSession mock method
api/internal/server/router.go                      — Added POST /:id/sessions/new route
api/internal/server/router_frontend_workspace_test.go — 4 new E2E router tests
api/internal/services/services.go                  — Wire SetSandboxService after sandbox init
api/internal/services/workspace/workspace_service.go — EnsureSession, ensureSandbox, waitForSandboxRunning, createSessionOnSandbox, auto-sandbox in CreateWorkspace
api/internal/services/workspace/workspace_ensure_session_test.go — 8 new unit tests (new file)
frontend/src/api/workspaces.ts                     — Added ensureSession, EnsureSessionResponse type
frontend/src/components/layout/Sidebar.tsx          — Simplified to use ensureSession for both flows
pkg/types/types.go                                 — Added SandboxID to Workspace, EnsureSessionResponse type
```
