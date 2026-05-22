# US-2.2: Implement Workspace Reconciler

**Epic:** 2 - Workspaces
**Priority:** Critical
**Depends on:** US-2.1

## User Story

As a platform operator, I want the controller to automatically manage workspace lifecycles (create PVC, auto-suspend, auto-delete), so that resources are efficiently managed without manual intervention.

## Acceptance Criteria

- [ ] Pending → Active: creates PVC, transitions phase
- [ ] Active: requeues for idle timeout check
- [ ] Auto-suspend: deletes sandbox pods, transitions to Suspended after idle timeout
- [ ] Resuming: recreates pods, transitions to Active
- [ ] Terminating: deletes PVC, removes finalizer
- [ ] Failed: handles init container failures with timeout (5 min)
- [ ] TTL cleanup: transitions Suspended → Terminating after ttlSecondsAfterSuspended
- [ ] Race condition: final lastActivityAt check before suspend

## Technical Details

**New file:** `controller/internal/workspace/controller.go`

**Reconcile loop:**

```
1. Fetch Workspace CRD
2. Handle deletion (if has deletionTimestamp):
   a. Delete all Sandbox CRDs referencing this workspace
   b. Delete PVC (owner-ref'd, but explicit for safety)
   c. Remove finalizer
3. If Pending:
   a. Create PVC (owner-ref'd to Workspace)
   b. Set status.phase = Active
4. If Active:
   a. Count Sandbox CRDs referencing this workspace → status.activeSessions
   b. Check idle timeout: if time.Since(lastActivityAt) > idleTimeoutSeconds → phase = Suspending
   c. Requeue at idleTimeoutSeconds * 0.8 for next check
5. If Suspending:
   a. Final lastActivityAt check (race condition protection)
   b. Delete all sandbox pods (not CRDs)
   c. Update Sandbox CRDs status.phase = Suspended
   d. Set status.phase = Suspended
6. If Resuming:
   a. Wait for sandbox pods to be recreated by Sandbox reconciler
   b. When all pods Running → status.phase = Active
7. If Suspended:
   a. If ttlSecondsAfterSuspended > 0, requeue and check TTL
   b. If expired → status.phase = Terminating
```

**Update controller setup:**
- `controller/internal/controller/controller.go` — add WorkspaceReconciler setup
- `controller/main.go` — add Workspace CRD to manager scheme

## Design Reference

Section 5.3-5.6: Workspace lifecycle, auto-suspend, race condition handling

## Effort

Large (6-8 hours)
