# US-6.2: Workspace Reconciler Owns Pod

**Epic:** 6 — Collapse Sandbox into Workspace
**Status:** Planning
**Dependencies:** US-6.1

## Objective

Rewrite workspace reconciler to manage pod directly. Workspace reconciler becomes sole owner of PVC + Pod + Password Secret.

## Phase Behavior (After)

| Phase | Behavior |
|-------|----------|
| Pending / "" | Add finalizer; create PVC; create password secret (`workspace-pw-{name}`); wait for PVC bound; transition to Creating |
| Creating | Create pod (deterministic name `{workspaceName}-{uid[:8]}`, see D4); wait for pod Running; on Running: set PodIP/StartTime/Endpoint, transition to Active. If pod disappears: recreate (idempotent by name). Requeue every 5s. |
| Active | Pod exists and running. Check: (1) restart generation bumped → delete pod, revert to Creating; (2) credential secret changed → bump restart generation; (3) pod missing → transient recovery or Failed; (4) timeout exceeded → set phase to Suspending (D3); (5) idle timer exceeded → set phase to Suspending. Requeue every 30s (D5). |
| Suspending | Delete pod; clear PodName/PodIP/PodNamespace; transition to Suspended |
| Suspended | TTL countdown; transition to Terminating when TTL expires |
| Resuming | Create new pod (same deterministic name); transition to Creating; Creating handler waits for Running, then transitions to Active |
| Terminating | Delete pod; delete PVC; delete password secret; transition to Terminated; remove finalizer |

**Key change from v1:** No `spec.Suspend` field (D2). Timeout sets `status.Phase = Suspending` directly. API resume sets `status.Phase = Resuming`. Controller never writes to spec.

## Pod Creation Idempotency (see D4)

Pod name: `{workspaceName}-{uid[:8]}`. UID is the workspace's Kubernetes UID — immutable. If controller crashes after `Create(ctx, pod)` but before `Status().Update(ctx, workspace)`, the next reconcile attempts to create the same pod name and gets `AlreadyExists`. Handler:

```go
pod, err := r.Create(ctx, newPod)
if err != nil {
    if errors.IsAlreadyExists(err) {
        return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
    }
    return ctrl.Result{}, err
}
```

## Requeue Load (see D5)

In `handleActive`: single requeue after 30s. All periodic checks (idle timer, timeout, credential hash, transient recovery) run on every reconcile. No separate timers. At 100 workspaces: ~200 req/min, trivial for controller-runtime.

## Code Absorbed from Sandbox Reconciler

Each function maps to source line in `sandbox/controller.go`:

| Sandbox function | Lines | Workspace equivalent |
|-----------------|-------|---------------------|
| `createSandboxPod` | 654-699 | `createPod` — reads `workspace.Spec.Runtime` |
| `buildSandboxPodWithContext` | 730-880 | `buildPod` — reads workspace directly (no WorkspaceRef lookup) |
| `ensurePasswordSecret` | 702-727 | Same — named `workspace-pw-{name}` |
| `buildPodSecurityContext` | 900-916 | Reads `workspace.Spec.PodSecurityContext` (new field from US-6.1) |
| `buildCredentialSetupInit` | 921-990 | Reads `workspace-pw-{workspace.Name}` directly |
| `buildWorkspaceSetupInit` | 994-1013 | Reads workspace directly |
| `buildWorkspaceSetupScript` | 1016-1049 | Unchanged |
| Timeout check | 286-297 | In `handleActive` — sets `status.Phase = Suspending` (D3) |
| `recoverFromTransientPodLoss` | 318-348 | In `handleActive` — reverts to Creating |
| `markPodPersistentLossFailed` | 353-375 | In `handleActive` |
| `maybeResetTransientCounter` | 386-402 | In `handleActive` |
| `handleRestartRequest` | 409-444 | In `handleActive` — deletes pod, reverts to Creating |
| `checkCredentialSecretChanged` | 450-498 | In `handleActive` — watches `workspace-creds-{name}` |
| `hashSecretData` | 501-516 | Move as-is |
| `resolveRuntimeImage` | `runtime_resolver.go:39-114` | Move to `workspace/runtime_resolver.go` |

## Code Removed from Current Workspace Reconciler

| Function | Lines | Reason |
|----------|-------|--------|
| `listSandboxesForWorkspace` | 504-513 | No sandbox CRDs |
| `updateSandboxesToSuspended` | 515-529 | No sandbox CRDs |
| `deleteSandboxCRDs` | 531-547 | No sandbox CRDs |
| `deleteWorkspacePods` | 549-568 | Replaced by direct pod management |

## Watch Setup

```go
func (r *WorkspaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&v1.Workspace{}).
        Owns(&corev1.Pod{}).
        Owns(&corev1.Secret{}).
        Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.mapCredSecretToWorkspaces)).
        Complete(r)
}
```

`mapCredSecretToWorkspaces` maps `workspace-creds-*` secrets to workspace by name directly.

## Files Modified

| File | Change |
|------|--------|
| `controller/internal/workspace/controller.go` | Major rewrite — absorb pod lifecycle |
| `controller/internal/workspace/runtime_resolver.go` | New — moved from `controller/internal/sandbox/runtime_resolver.go` (only imports `v1` + `client`, no circular dep) |
| `controller/internal/workspace/constants.go` | New — `WorkspaceFinalizer`, `MaxTransientFailures`, `TransientFailureResetWindow` (moved from `common/constants.go`) |

## Acceptance Criteria

1. Pending → Creating → Active: PVC bound, pod created, pod running, PodIP set
2. Active → Suspending → Suspended: pod deleted, PVC retained
3. Suspended → Resuming → Creating → Active: new pod, same data
4. Pod spec matches current sandbox pod spec exactly
5. Runtime image resolution via RuntimeEnvironment CRD
6. Transient pod loss self-heals up to MaxRetries
7. Restart generation triggers pod recreation
8. Credential secret change triggers pod recreation
9. Timeout sets phase to Suspending (not Terminating), preserves PVC
10. Controller never writes to spec — only status
11. Pod name is deterministic: `{workspaceName}-{uid[:8]}`
12. `make test` passes; envtest covers every phase transition
13. Cluster validated: create → active → suspend → resume → delete
