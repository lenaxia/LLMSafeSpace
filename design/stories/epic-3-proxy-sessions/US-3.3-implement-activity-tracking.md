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

**Add to proxy handler:**

```go
type ActivityTracker struct {
    mu       sync.Mutex
    activity map[string]time.Time // workspaceID → last activity
    lastFlush map[string]time.Time // workspaceID → last flush time
    client   kubernetes.KubernetesClient
}

func (t *ActivityTracker) Record(workspaceID string) { ... }
func (t *ActivityTracker) Flush() { ... } // called every 60s by ticker
```

**Design reference:** §5.5a says the API is explicitly allowed to write `status.lastActivityAt` — this is the one status field the API owns.

## Design Reference

Section 5.5a (State Management), 5.6 (Auto-suspend)

## Effort

Medium (3-4 hours)
