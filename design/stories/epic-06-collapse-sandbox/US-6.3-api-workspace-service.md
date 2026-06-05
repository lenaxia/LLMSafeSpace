# US-6.3: API Workspace Service Changes

**Epic:** 6 — Collapse Sandbox into Workspace
**Status:** Planning
**Dependencies:** US-6.2

## Objective

Update workspace service to cover sandbox service responsibilities. Remove all sandbox indirection from the API layer. Rewrite `EnsureSession` to work directly with workspace pod. This must happen BEFORE US-6.4 deletes the sandbox service.

## Current Workspace Service Methods

From `workspace_service.go` (800 lines):

| Method | Change |
|--------|--------|
| `CreateWorkspace` | Set `spec.Runtime` (currently sets `DefaultRuntime`); remove auto-sandbox creation |
| `GetWorkspace` | Add PodIP, Endpoint, Runtime to response from CRD status |
| `ListWorkspaces` | No change |
| `DeleteWorkspace` | No change |
| `SuspendWorkspace` | No change — sets phase to Suspending via status |
| `ResumeWorkspace` | No change — sets phase to Resuming via status |
| `GetWorkspaceStatus` | Add PodIP, Endpoint to response |
| `SetCredentials` | No change |
| `DeleteCredentials` | No change |
| `ActivateWorkspace` | No change |
| `ListWorkspaceSandboxes` | **REMOVE** — no more sandboxes |
| `ListWorkspaceSessions` | No change |
| `RenameSession` | No change |
| `EnsureSession` | **MAJOR REWRITE** — no sandbox resolution, direct pod proxy |
| `ensureSandbox` | **DELETE** |
| `waitForSandboxRunning` | **REPLACE** with `waitForWorkspaceActive` |
| `createSessionOnSandbox` | **REPLACE** with `createSessionOnWorkspace` |

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

### `EnsureSession` Rewrite

**Before (current):** Resolves workspace → finds/creates sandbox → waits for sandbox Running → creates session on sandbox pod via `sandbox-pw-{sandboxID}`.

**After:** Checks workspace phase → if Suspended, resumes → waits for workspace Active with PodIP → creates session on workspace pod via `workspace-pw-{workspaceName}`.

```go
func (s *Service) EnsureSession(ctx context.Context, userID, workspaceID string) (*types.EnsureSessionResponse, error) {
    if err := s.verifyOwner(ctx, userID, workspaceID); err != nil { return nil, err }

    crd, err := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).Get(workspaceID, metav1.GetOptions{})
    if err != nil { return nil, apierrors.NewInternalError("workspace_get_failed", err) }

    resumed := false
    switch crd.Status.Phase {
    case v1.WorkspacePhaseSuspended:
        if err := s.ResumeWorkspace(ctx, userID, workspaceID); err != nil { return nil, err }
        resumed = true
    case v1.WorkspacePhaseTerminating, v1.WorkspacePhaseTerminated, v1.WorkspacePhaseFailed:
        return nil, apierrors.NewValidationError("workspace is not usable", ...)
    case v1.WorkspacePhaseActive:
        // Ready immediately
    case v1.WorkspacePhasePending, v1.WorkspacePhaseCreating, v1.WorkspacePhaseResuming:
        // Will wait below
    }

    // Wait for workspace to reach Active with PodIP
    podIP, err := s.waitForWorkspaceActive(ctx, workspaceID)
    if err != nil { return nil, err }

    // Create session directly on workspace pod
    sessionID, err := s.createSessionOnWorkspace(ctx, workspaceID, podIP)
    if err != nil { return nil, err }

    return &types.EnsureSessionResponse{
        WorkspaceID:    workspaceID,
        WorkspacePhase: "Active",
        SessionID:      sessionID,
        Resumed:        resumed,
    }, nil
}
```

### `waitForWorkspaceActive` (replaces `waitForSandboxRunning`)

```go
func (s *Service) waitForWorkspaceActive(ctx context.Context, workspaceID string) (string, error) {
    ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
    defer cancel()
    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()

    for {
        crd, err := s.k8sClient.LlmsafespaceV1().Workspaces(s.config.Namespace).Get(workspaceID, metav1.GetOptions{})
        if err != nil { return "", apierrors.NewInternalError("workspace_get_failed", err) }
        if crd.Status.Phase == v1.WorkspacePhaseActive && crd.Status.PodIP != "" {
            return crd.Status.PodIP, nil
        }
        if crd.Status.Phase == v1.WorkspacePhaseFailed || crd.Status.Phase == v1.WorkspacePhaseTerminated {
            return "", apierrors.NewInternalError("workspace_failed", ...)
        }
        select {
        case <-ctx.Done(): return "", apierrors.NewInternalError("workspace_timeout", ...)
        case <-ticker.C:
        }
    }
}
```

### `createSessionOnWorkspace` (replaces `createSessionOnSandbox`)

```go
func (s *Service) createSessionOnWorkspace(ctx context.Context, workspaceID, podIP string) (string, error) {
    secretName := fmt.Sprintf("workspace-pw-%s", workspaceID)
    secret, err := s.k8sClient.Clientset().CoreV1().Secrets(s.config.Namespace).Get(ctx, secretName, metav1.GetOptions{})
    if err != nil { return "", apierrors.NewInternalError("workspace_password_failed", err) }
    password := string(secret.Data["password"])

    url := fmt.Sprintf("http://%s:%d/session", podIP, s.config.OpencodePort)
    req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
    req.SetBasicAuth("opencode", password)

    resp, err := http.DefaultClient.Do(req)
    // ... decode session ID from response
}
```

## EnsureSession Response Type Change

**Before:**
```go
type EnsureSessionResponse struct {
    SandboxID    string `json:"sandboxId"`
    SandboxPhase string `json:"sandboxPhase"`
    SessionID    string `json:"sessionId"`
    Resumed      bool   `json:"resumed"`
}
```

**After:**
```go
type EnsureSessionResponse struct {
    WorkspaceID    string `json:"workspaceId"`
    WorkspacePhase string `json:"workspacePhase"`
    SessionID      string `json:"sessionId"`
    Resumed        bool   `json:"resumed"`
}
```

## Workspace Response Type — Remove SandboxID

**Before:** `CreateWorkspace` returns `types.Workspace` with `SandboxID` field populated by auto-sandbox creation.

**After:** Remove `SandboxID` field from `types.Workspace`. Remove `sandboxService` dependency from workspace service. Remove `SetSandboxService()` method.

## API Route Changes

| Old Route | New Route | Handler |
|-----------|-----------|---------|
| `POST /sandboxes` | **Removed** | — |
| `GET /sandboxes` | **Removed** | — |
| `GET /sandboxes/:id` | **Removed** | — |
| `DELETE /sandboxes/:id` | **Removed** | — |
| `GET /sandboxes/:id/status` | **Removed** | — |
| `POST /sandboxes/:id/restart` | `POST /workspaces/:id/restart` | Calls `workspace.RestartWorkspace` |
| `GET /workspaces/:id/sandboxes` | **Removed** | — |
| `POST /workspaces/:id/sessions/new` | **Kept** (rewritten internals) | Calls rewritten `EnsureSession` |

## Router Changes

The `/:id/sessions/active` endpoint currently calls `ListWorkspaceSandboxes` to find sandbox IDs. After collapse, active sessions are keyed by workspaceID directly:

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
    Phase          string    // existing
    PVCName        string    // existing
    ActiveSessions int       // existing
    Message        string    // existing
    PodIP          string    // NEW
    Endpoint       string    // NEW
    Runtime        string    // NEW
}
```

**EnsureSessionResponse — rewrite:**
- `SandboxID` → `WorkspaceID`
- `SandboxPhase` → `WorkspacePhase`

**Workspace — remove field:**
- Remove `SandboxID string`

**Remove types:**
- `SandboxMetadata`
- `SandboxUpdates`
- `SandboxListItem`
- `SandboxStatus`
- `CreateSandboxRequest`
- `SandboxNotFoundError`

**Keep:**
- Session types (unchanged — already keyed by workspaceID)
- `WorkspaceMetadata`, `WorkspaceListResult`, etc.

## Service Dependency Removal

- Remove `sandboxService apiinterfaces.SandboxService` field from workspace `Service` struct
- Remove `SetSandboxService(sb)` method
- Remove `ensureSandbox()` private method
- Remove `waitForSandboxRunning()` private method
- Remove `createSessionOnSandbox()` private method

## Files Modified

| File | Change |
|------|--------|
| `api/internal/services/workspace/workspace_service.go` | Major rewrite: EnsureSession, RestartWorkspace, remove sandbox methods, remove sandbox dependency |
| `api/internal/server/router.go` | Remove sandbox CRUD routes, sandbox ownership middleware, `/:id/sandboxes` route; add workspace restart; simplify sessions/active |
| `api/internal/interfaces/interfaces.go` | Remove SandboxService from Services interface |
| `pkg/types/types.go` | Rewrite EnsureSessionResponse; add workspace status fields; remove sandbox types; remove SandboxID from Workspace |
| `api/internal/mocks/database.go` | Remove sandbox DB methods |
| `api/internal/mocks/sandbox.go` | Delete |
| `api/internal/services/services.go` | Remove SandboxService from service registry |

## Acceptance Criteria

1. `POST /workspaces/:id/restart` bumps RestartGeneration, pod recreated
2. `GET /workspaces/:id/status` returns PodIP, Endpoint when workspace Active
3. `POST /workspaces` sets `spec.Runtime` on CRD; does NOT auto-create sandbox
4. `POST /workspaces/:id/sessions/new` (EnsureSession) works without sandbox — resumes if suspended, waits for Active, creates session directly on workspace pod
5. EnsureSession response returns `workspaceId`/`workspacePhase` (not sandboxId/sandboxPhase)
6. `ListWorkspaceSandboxes` method no longer exists
7. `GET /workspaces/:id/sandboxes` route removed
8. All sandbox CRUD routes removed
9. `/:id/sessions/active` works without sandbox lookup
10. `Workspace` response type has no `SandboxID` field
11. All workspace service tests pass
