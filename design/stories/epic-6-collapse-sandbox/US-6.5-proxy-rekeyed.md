# US-6.5: Proxy Rekeyed to Workspace ID

**Epic:** 6 — Collapse Sandbox into Workspace
**Status:** Planning
**Dependencies:** US-6.4

## Objective

Rewrite proxy handler to use workspace CRD directly. All routes `/sandboxes/:id` → `/workspaces/:id`. Rekey all internal maps. Replace `SandboxWatcher` with `WorkspaceWatcher`.

## Route Changes

| Old | New |
|-----|-----|
| `POST /api/v1/sandboxes/:id/sessions` | `POST /api/v1/workspaces/:id/sessions` |
| `GET /api/v1/sandboxes/:id/sessions` | `GET /api/v1/workspaces/:id/sessions` |
| `POST /api/v1/sandboxes/:id/sessions/:sessionId/message` | `POST /api/v1/workspaces/:id/sessions/:sessionId/message` |
| `POST /api/v1/sandboxes/:id/sessions/:sessionId/prompt` | `POST /api/v1/workspaces/:id/sessions/:sessionId/prompt` |
| `GET /api/v1/sandboxes/:id/sessions/:sessionId/message` | `GET /api/v1/workspaces/:id/sessions/:sessionId/message` |
| `POST /api/v1/sandboxes/:id/sessions/:sessionId/abort` | `POST /api/v1/workspaces/:id/sessions/:sessionId/abort` |
| `GET /api/v1/sandboxes/:id/events` | `GET /api/v1/workspaces/:id/events` |
| WS: `/api/v1/sandboxes/:id/stream` | WS: `/api/v1/workspaces/:id/stream` |

## Proxy Handler Changes

### `proxyToSandbox` → `proxyToWorkspace`

```go
workspaceID := c.Param("id")
workspace, err := h.k8sClient.LlmsafespaceV1().Workspaces(h.namespace).Get(workspaceID, metav1.GetOptions{})
if workspace.Status.Phase != v1.WorkspacePhaseActive || workspace.Status.PodIP == "" {
    c.Header("Retry-After", "10")
    c.JSON(503, gin.H{"error": "workspace not ready", "phase": workspace.Status.Phase})
    return
}
podIP := workspace.Status.PodIP
```

### Password secret

```go
secretName := fmt.Sprintf("workspace-pw-%s", workspaceID)
```

### Workspace config — no more indirection

```go
maxSessions := int(workspace.Spec.MaxActiveSessions)
// No more sandbox.Spec.WorkspaceRef → workspace lookup
```

### Internal maps — all rekeyed to workspaceID

- `pwCache map[string]string`
- `wsConfig map[string]workspaceConfig`
- `activeSess map[string]map[string]bool`
- `connCount map[string]int`

### `onPhaseChange` — receives `*v1.Workspace`

```go
func (h *ProxyHandler) onPhaseChange(workspace *v1.Workspace) {
    phase := workspace.Status.Phase
    if phase == v1.WorkspacePhaseSuspending || phase == v1.WorkspacePhaseSuspended || ... {
        h.invalidateCaches(workspace.Name)
        if h.sseTracker != nil { h.sseTracker.StopWatching(workspace.Name) }
    }
}
```

### `SandboxWatcher` → `WorkspaceWatcher`

`crd_watcher.go` (239 lines) + `crd_watcher_test.go` (25 tests, all sandbox-typed). Rewrite to watch Workspace CRDs instead. Same watch logic, different type.

### Ownership middleware

```go
// Before: reads user-id label on Sandbox CRD
// After: reads workspace.Spec.Owner.UserID directly
workspaceID := c.Param("id")
ws, err := proxyHandler.GetWorkspaceCRD(workspaceID)
if ws.Spec.Owner.UserID != userID { c.AbortWithStatusJSON(403, ...) }
c.Set("workspace", ws)
```

### Activity tracking

```go
// Before: derives workspaceID from sandbox.Spec.WorkspaceRef via cached wsConfig
// After: workspaceID is the direct key
h.activityTracker.Record(workspaceID)
```

### Connection retry

```go
// Before: re-fetches Sandbox CRD
// After: re-fetches Workspace CRD
freshWorkspace, _ := h.k8sClient.LlmsafespaceV1().Workspaces(h.namespace).Get(workspaceID, ...)
```

### Constant rename

`maxConnectionsPerSandbox` → `maxConnectionsPerWorkspace`

## Files Modified

| File | Change |
|------|--------|
| `api/internal/handlers/proxy.go` | Major rewrite — all sandbox lookups → workspace |
| `api/internal/handlers/crd_watcher.go` | Rewrite `SandboxWatcher` → `WorkspaceWatcher` |
| `api/internal/handlers/crd_watcher_test.go` | Rewrite all 25 tests for workspace type |
| `api/internal/server/router.go` | Route group `/sandboxes` → `/workspaces` |

## Acceptance Criteria

1. All proxy routes under `/workspaces/:id/...`
2. Proxy reads workspace CRD for PodIP
3. Password from `workspace-pw-{name}` secret
4. Phase check: `workspace.Status.Phase == Active`
5. Ownership via `workspace.Spec.Owner.UserID`
6. Session index keyed by workspaceID
7. SSE works with workspace-scoped watching
8. Connection retry uses fresh workspace CRD
9. Activity tracking records by workspaceID directly
10. Old `/sandboxes/:id` routes do not exist
11. E2E: create workspace → session → message → response → abort
