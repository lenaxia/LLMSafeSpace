# US-6.3: API Workspace Service Changes

**Epic:** 6 — Collapse Sandbox into Workspace
**Status:** Planning
**Dependencies:** US-6.2

## Objective

Update workspace service to cover sandbox service responsibilities. This must happen BEFORE US-6.4 deletes the sandbox service.

## Current Workspace Service Methods

From `workspace_service.go` (586 lines):

| Method | Line | Change |
|--------|------|--------|
| `CreateWorkspace` | 87 | Set `spec.Runtime` (currently sets `DefaultRuntime` at line 502) |
| `GetWorkspace` | 155 | Add PodIP, Endpoint, Runtime to response from CRD status |
| `ListWorkspaces` | 201 | No change |
| `DeleteWorkspace` | 237 | No change |
| `SuspendWorkspace` | 265 | No change — sets phase to Suspending via status |
| `ResumeWorkspace` | 303 | No change — sets phase to Resuming via status |
| `GetWorkspaceStatus` | 339 | Add PodIP, Endpoint to response |
| `SetCredentials` | 378 | No change |
| `DeleteCredentials` | 441 | No change |
| `ActivateWorkspace` | 523 | No change |
| `ListWorkspaceSandboxes` | 540 | **REMOVE** — no more sandboxes |
| `ListWorkspaceSessions` | 565 | No change |
| `RenameSession` | 577 | No change |

## New Methods

### `RestartWorkspace(ctx, userID, workspaceID) error`

Replaces `sandbox.RestartSandbox`. Bumps `spec.RestartGeneration` on workspace CRD.

```go
func (s *Service) RestartWorkspace(ctx context.Context, userID, workspaceID string) error {
    if err := s.verifyOwner(ctx, userID, workspaceID); err != nil { return err }
    crd, err := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).Get(workspaceID, metav1.GetOptions{})
    if err != nil { return apierrors.NewInternalError("workspace_get_failed", err) }
    if crd.Status.Phase != v1.WorkspacePhaseActive {
        return apierrors.NewValidationError("can only restart active workspace", ...)
    }
    crd.Spec.RestartGeneration = time.Now().UnixNano()
    if _, err := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).Update(crd); err != nil {
        return apierrors.NewInternalError("workspace_restart_failed", err)
    }
    return nil
}
```

## API Route Changes

| Old Route | New Route | Handler |
|-----------|-----------|---------|
| `POST /sandboxes` | **Removed** | — |
| `GET /sandboxes` | **Removed** | — |
| `GET /sandboxes/:id` | **Removed** | — |
| `DELETE /sandboxes/:id` | **Removed** | — |
| `GET /sandboxes/:id/status` | **Removed** | — |
| `POST /sandboxes/:id/restart` | `POST /workspaces/:id/restart` | Calls `workspace.RestartWorkspace` |
| `POST /sandboxes/:id/retry` | **Removed** | Self-healing replaces retry |
| `GET /workspaces/:id/sandboxes` | **Removed** | — |

## Router Changes

The `/:id/sessions/active` endpoint (router.go:97-122) currently calls `ListWorkspaceSandboxes` to find sandbox IDs, then calls `proxyHandler.GetActiveSessions(sb.ID)` for each. After collapse, active sessions are keyed by workspaceID directly:

```go
workspaceGroup.GET("/:id/sessions/active", func(c *gin.Context) {
    workspaceID := c.Param("id")
    active := proxyHandler.GetActiveSessions(workspaceID)
    c.JSON(200, types.ActiveSessionsResponse{Active: active, MaxActive: 5})
})
```

## Transfer Object Changes (`pkg/types/types.go`)

**WorkspaceStatusResult — add fields:**
```go
type WorkspaceStatusResult struct {
    Phase          string
    PVCName        string
    ActiveSessions int
    Message        string
    PodIP          string    // NEW
    Endpoint       string    // NEW
    Runtime        string    // NEW
}
```

**Remove types:**
- `SandboxMetadata` (types.go:312)
- `SandboxUpdates` (types.go:562)
- `SandboxListItem`
- `SandboxStatus`
- `CreateSandboxRequest`
- `SandboxNotFoundError`

**Keep:**
- Session types (unchanged — already keyed by workspaceID)
- `WorkspaceMetadata`, `WorkspaceListResult`, etc.

## Files Modified

| File | Change |
|------|--------|
| `api/internal/services/workspace/workspace_service.go` | Add RestartWorkspace; remove ListWorkspaceSandboxes; update CreateWorkspace/GetWorkspace/GetWorkspaceStatus |
| `api/internal/server/router.go` | Remove sandbox CRUD routes; add workspace restart; simplify sessions/active |
| `api/internal/interfaces/interfaces.go` | Remove SandboxService from Services interface |
| `pkg/types/types.go` | Add workspace status fields; remove sandbox types |
| `api/internal/mocks/database.go` | Remove sandbox DB methods |
| `api/internal/mocks/sandbox.go` | Delete |

## Acceptance Criteria

1. `POST /workspaces/:id/restart` bumps RestartGeneration, pod recreated
2. `GET /workspaces/:id/status` returns PodIP, Endpoint when workspace Active
3. `POST /workspaces` sets `spec.Runtime` on CRD
4. `ListWorkspaceSandboxes` method no longer exists
5. All sandbox CRUD routes removed
6. `/:id/sessions/active` works without sandbox lookup
7. All workspace service tests pass
