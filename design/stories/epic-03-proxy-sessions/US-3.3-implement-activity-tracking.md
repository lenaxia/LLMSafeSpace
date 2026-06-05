# US-3.3: Implement Activity Tracking

**Epic:** 3 - Proxy and Sessions
**Priority:** High
**Depends on:** US-3.1, US-2.2

## User Story

As a platform operator, I want the API to track when users interact with sandboxes, so that idle workspaces can be auto-suspended for cost savings.

## Acceptance Criteria

- [ ] Proxy handler updates `status.lastActivityAt` on Workspace CRD after each proxied request
- [ ] Updates batched (at most once per 60 seconds per workspace)
- [ ] Controller reads lastActivityAt for auto-suspend decisions

## Technical Details

The API server patches the Workspace CRD status on each proxied request. To avoid excessive K8s API calls:

1. Maintain an in-memory map: `workspaceID → lastActivityTimestamp`
2. A background goroutine flushes this map to CRD status every 60 seconds
3. Only patches if timestamp changed since last flush

Activity is recorded on **any proxied request** to a sandbox in the workspace — including read-only operations (GET history). This ensures the auto-suspend timer reflects actual user engagement, not just active sessions.

Active session transitions (a session going from active to idle via SSE event) also count as activity — the user was recently engaged with that workspace even if the last request was the agent finishing a response.

**Resolve workspaceID:** The proxy handler already has the sandbox→workspace mapping from `wsConfig[sandboxID].workspaceID` (populated during maxActiveSessions resolution in US-3.1). No additional CRD lookup needed.

### ActivityTracker struct

```go
type ActivityTracker struct {
    mu        sync.Mutex
    activity  map[string]time.Time // workspaceID → last activity
    lastFlush map[string]time.Time // workspaceID → last flush time
    k8sClient kubernetes.KubernetesClient
    namespace string
    logger    logger.Logger

    stopCh chan struct{}
}
```

### K8s API interaction

The current `WorkspaceInterface.UpdateStatus()` performs a full PUT (not a strategic merge patch). To update only `status.lastActivityAt` without overwriting other status fields:

1. Read the current Workspace CRD via `Workspaces(ns).Get()`
2. Update only `status.lastActivityAt`
3. Call `Workspaces(ns).UpdateStatus()` with the modified object
4. Wrap in `k8s.io/client-go/util/retry.RetryOnConflict` with 3 attempts

This read-modify-write pattern is safe because:
- `lastActivityAt` is owned by the API (§5.5a) — the controller never writes it
- Other status fields are owned by the controller — the API never writes them
- Conflict is only possible between API replicas, and RetryOnConflict handles this
- The 60-second batch window means conflicts are extremely rare

```go
func (t *ActivityTracker) flushOne(ctx context.Context, workspaceID string, activityTime time.Time) error {
    return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
        ws, err := t.k8sClient.LlmsafespaceV1().Workspaces(t.namespace).Get(workspaceID, metav1.GetOptions{})
        if err != nil {
            return err
        }
        ws.Status.LastActivityAt = &metav1.Time{Time: activityTime}
        _, err = t.k8sClient.LlmsafespaceV1().Workspaces(t.namespace).UpdateStatus(ws)
        return err
    })
}
```

### Lifecycle

```go
func (t *ActivityTracker) Start() error {
    go t.runFlushLoop()
    return nil
}

func (t *ActivityTracker) Stop() error {
    close(t.stopCh)
    // Final flush before shutdown
    t.Flush()
    return nil
}

func (t *ActivityTracker) runFlushLoop() {
    ticker := time.NewTicker(60 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            t.Flush()
        case <-t.stopCh:
            return
        }
    }
}
```

### Integration with ProxyHandler

The `ProxyHandler` holds a reference to the `ActivityTracker`. On every proxied request:

```go
// In ProxyToSandbox, after resolving workspaceID:
h.activityTracker.Record(workspaceID)
```

The `ActivityTracker` is created in `ProxyHandler` construction, started in `ProxyHandler.Start()`, and stopped in `ProxyHandler.Stop()` (which does a final flush).

**Design reference:** §5.5a says the API is explicitly allowed to write `status.lastActivityAt` — this is the one status field the API owns.

## Design Reference

Section 5.5a (State Management), 5.6 (Auto-suspend)

## Effort

Medium (3-4 hours)
