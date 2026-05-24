# Sandbox Lifecycle

> **Status:** Authoritative for `controller/internal/sandbox/controller.go`.
> Changes to phase transitions or recovery semantics must update this doc in the same commit.

This document specifies the sandbox state machine, what triggers each transition, and how to recover from each state. It applies to the `Sandbox` CRD reconciled by `SandboxReconciler` in the controller, and to client-facing operations exposed by `api/internal/server/router.go`.

---

## 1. Phase state machine

There are 9 phase values, declared at `pkg/apis/llmsafespace/v1/sandbox_types.go` and validated by `pkg/crds/sandbox_crd.yaml`:

```
Pending → Creating → Running ─┬─→ Suspending → Suspended → Resuming → Creating → Running
                              │                 ↓
                              │              Terminating → Terminated
                              ↓
                            (Failed)              (Terminated)
                              │
                              └─→ Pending [via POST /retry, fix #5]

Pod-not-found while Running:
  - parent workspace Suspending → Suspended (recovery via workspace resume)
  - first/second occurrence  → Pending (transient retry, fix #2)
  - third occurrence         → Failed (terminal until /retry, fix #5)

User-initiated restart [fix #1]:
  Running → (controller deletes pod) → Pending → Creating → Running

Credential change observed [fix #3]:
  Same as user-initiated restart, triggered automatically.
```

### Phase definitions

| Phase | Pod state | Sandbox is reachable? | Triggered by |
|-------|-----------|------------------------|--------------|
| `Pending` | not yet created | no | initial reconcile after CRD created; transient pod-not-found recovery (fix #2); restart request observed (fix #1); retry from Failed (fix #5) |
| `Creating` | created, not yet `Running` | no | controller transitions from Pending after Pod create returns |
| `Running` | `corev1.PodPhase == Running` | yes (proxy routes to it) | pod becomes Ready |
| `Suspending` | being deleted gracefully | no | `POST /workspaces/:id/suspend` (cascades to attached sandboxes) |
| `Suspended` | absent (PVC retained) | no | suspending pod is gone |
| `Resuming` | being recreated from PVC | no | `POST /workspaces/:id/resume` |
| `Terminating` | being deleted with finalizers | no | `DELETE /sandboxes/:id`; spec.timeout exceeded |
| `Terminated` | gone | no | finalizers cleared after Terminating |
| `Failed` | unrecoverable error state | no | pod-not-found 3rd occurrence (fix #2); pod entered failed state outside suspend window |

### Reconciler entry points (cite: `controller/internal/sandbox/controller.go:108`)

```go
switch sandbox.Status.Phase {
case "", SandboxPhasePending:    handlePendingSandbox     // creates pod
case SandboxPhaseCreating:       handleCreatingSandbox    // waits for Running
case SandboxPhaseRunning:        handleRunningSandbox     // monitors; reacts to absence/timeout
case SandboxPhaseSuspending:     handleSuspendingSandbox  // deletes pod
case SandboxPhaseResuming:       handleResumingSandbox    // recreates pod
case SandboxPhaseTerminating:    handleTerminatingSandbox // finalizers + cleanup
case SandboxPhaseTerminated, SandboxPhaseFailed, SandboxPhaseSuspended:
    return ctrl.Result{}, nil    // no-op
}
```

`Failed`, `Suspended`, and `Terminated` are *not* the same: `Suspended` resumes via workspace API; `Terminated` is end-of-life; `Failed` requires explicit retry (fix #5).

---

## 2. What triggers each transition

| From | To | Trigger | Code |
|------|-----|---------|------|
| (initial) | `Pending` | CRD created via API | `api/internal/services/sandbox/sandbox_service.go::CreateSandbox` |
| `Pending` | `Creating` | reconcile sees empty/Pending phase, calls `createSandboxPod` | `controller.go:133` |
| `Creating` | `Running` | pod reaches `PodRunning` and Ready | `controller.go:163` |
| `Creating` | `Pending` | pod was created then went missing before `Running` | `controller.go:151` |
| `Creating` | `Suspended` | parent workspace is suspending; pod absent | `controller.go:200` |
| `Creating` | `Failed` | pod absent (not workspace-suspending) | `controller.go:209` |
| `Running` | `Failed` (current; fix #2 changes this) | pod went missing while Running | `controller.go:209` |
| `Running` | `Suspended` | parent workspace is suspending | `controller.go:200` |
| `Running` | `Terminating` | spec.timeout exceeded | `controller.go:260` |
| `Running` | `Pending` (fix #1) | user POSTed `/sandboxes/:id/restart`; controller observed `spec.restartRevision` change | new in fix #1 |
| `Running` | `Pending` (fix #2) | pod went missing, transient counter < threshold | new in fix #2 |
| `Running` | `Pending` (fix #3) | credential Secret data hash changed | new in fix #3 |
| `Suspending` | `Suspended` | controller deleted pod successfully | `controller.go:298` |
| `Resuming` | `Creating` | controller recreated pod | `controller.go:313` |
| `Suspended` | `Resuming` | `POST /workspaces/:id/resume` cascades | `workspace/controller.go::handleResuming` |
| `Failed` | `Pending` (fix #5) | user POSTed `/sandboxes/:id/retry` | new in fix #5 |
| `Terminating` | `Terminated` | finalizers cleared after pod gone | `controller.go:331,354` |

---

## 3. Recovery semantics

| Phase | Recoverable? | How |
|-------|--------------|-----|
| `Pending` | (in motion) | wait for reconcile |
| `Creating` | (in motion) | wait for reconcile |
| `Running` | n/a (healthy) | — |
| `Suspending` | (in motion) | wait for `Suspended` |
| `Suspended` | yes | `POST /api/v1/workspaces/:id/resume` (cascades to all sandboxes attached to that workspace) |
| `Resuming` | (in motion) | wait for `Running` |
| `Terminating` | no | terminating is terminal in spirit; finalizers will complete |
| `Terminated` | no | resource is being garbage-collected; recreate via `POST /sandboxes` |
| `Failed` | yes (fix #5) | `POST /api/v1/sandboxes/:id/retry` clears failure conditions, sets phase `Pending`. Bounded by `Spec.MaxRetries`. |

---

## 4. Operator rules (do not break these)

### 4.1 — Do **NOT** force-delete a sandbox pod

```
# WRONG: causes Sandbox to be marked Failed
kubectl delete pod sb-xxx-yyyy --grace-period=0 --force

# RIGHT: graceful delete; controller observes Terminating window and stays healthy
kubectl delete pod sb-xxx-yyyy
```

`--force --grace-period=0` removes the pod from etcd immediately. The reconciler's next pass sees `Pod not found` while phase is `Running`. With fix #2, the first two occurrences revert to `Pending` and recreate; the third marks `Failed`. Without fix #2 (current code), even one force-delete is permanent.

### 4.2 — Restart sandbox via API, never directly via `kubectl delete pod`

```
# WRONG: timing-dependent; if you force-delete, you fail the sandbox
kubectl delete pod sb-xxx-yyyy

# RIGHT (fix #1): declarative, idempotent
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://api/api/v1/sandboxes/sb-xxx/restart
```

The API patches `Spec.RestartRevision`; the controller observes the change and performs a graceful pod replace. No race, no `Failed` state.

### 4.3 — Set workspace credentials BEFORE first sandbox attaches, OR use the auto-recycle (fix #3)

Two valid flows:

**Flow A — Pre-set credentials (recommended for deterministic tests):**
1. `POST /api/v1/workspaces` (create workspace)
2. `PUT /api/v1/workspaces/:id/credentials` (set creds; updates K8s Secret)
3. `POST /api/v1/sandboxes` with `workspaceRef: <id>` (pod starts with creds already mounted)

**Flow B — Post-set credentials (rotation, or sandbox already running):**
1. Sandbox already running.
2. `PUT /api/v1/workspaces/:id/credentials` (Secret updated).
3. **Fix #3: controller observes Secret hash change; auto-restarts attached sandboxes.** No client action needed beyond step 2.

Until fix #3 lands, post-set is operationally a manual restart (`POST /api/v1/sandboxes/:id/restart` after fix #1, or `kubectl delete pod` graceful before that).

### 4.4 — Auto-create-workspace flow + credential-required runtimes

**Fix #4:** if `RuntimeEnvironment.Spec.RequiresCredentials == true`, `POST /sandboxes` without `workspaceRef` returns **409 Conflict** with the message:

> "Runtime <name> requires workspace credentials. Create a workspace explicitly, set credentials with `PUT /workspaces/:id/credentials`, then create the sandbox with `workspaceRef`."

This prevents a race where the auto-created workspace has no credentials and the sandbox pod starts with `{}` for its provider config — silently broken at first prompt.

### 4.5 — Credentials are read once at pod start

Per `controller/internal/sandbox/controller.go:649-653`, the init container copies `/mnt/secrets/credentials/provider-config` to `/sandbox-cfg/credentials` once. Opencode reads the local file at process start. Subsequent updates to the K8s Secret reach the volume mount but **not** opencode unless the pod is restarted (kubelet syncs Secret updates ~60s).

**Implication:** changing credentials requires a pod restart. Fix #3 automates this when the API is used (PUT credentials → restart triggered). Manually editing the Secret (e.g. `kubectl edit secret workspace-creds-...`) does NOT trigger the restart unless fix #3's hash-change detection sees the difference.

---

## 5. In-flight requests during restart

When a restart (fix #1, fix #3, or workspace suspend/resume) deletes the running pod, any HTTP request from the proxy to opencode that's mid-flight will fail.

**Behaviour by request type:**

| Request | Behaviour during restart |
|---------|---------------------------|
| `POST /sandboxes/:id/sessions/:sid/message` (synchronous prompt) | fails with proxy error (5xx). Client should retry. The session ID and history persist in `/workspace` PVC and are visible after restart. |
| `POST /sandboxes/:id/sessions/:sid/prompt` (async) | the 204 may have already been returned; the assistant turn that was in flight is lost. SSE consumers will see a stream close. |
| `GET /sandboxes/:id/events` (SSE) | stream closes. Client reconnects. |
| `GET /sandboxes/:id/sessions/:sid/message` (history read) | fails or returns up-to-the-restart history. Retry succeeds. |

**Session state is preserved across restart** because opencode persists session JSON to `/workspace/.local/opencode/storage/...` which is on the workspace PVC. The PVC is unaffected by pod restart.

This is verified by the `restart-during-prompt` probe in `local/test.sh` (added by fix #7).

---

## 6. Failure modes and how to detect them

### 6.1 — Sandbox stuck in `Pending` for > 60s

- **Cause:** RuntimeEnvironment for `spec.runtime` not found, or pod-spec build failed.
- **Detect:** `kubectl get sandbox <name> -o jsonpath='{.status.conditions}'` will show `Ready=False, Reason=PodCreationFailed`.
- **Fix:** check `kubectl logs deploy/llmsafespace-controller` for build errors; ensure runtime is registered.

### 6.2 — Sandbox stuck in `Creating` for > 5 min

- **Cause:** image pull failure, init container failure (often credential-fetch script issue), node has no capacity.
- **Detect:** `kubectl describe pod <pod>` events.
- **Fix:** image push, fix node, verify credential secret exists.

### 6.3 — Sandbox in `Failed` after Running

- **Pre-fix-#2:** even one accidental pod-delete causes this. Recovery: `DELETE` and recreate.
- **Post-fix-#2:** transient losses self-heal up to 3 times; persistent loss after that means a real problem (e.g. node permanently lost, image evicted from local cache and pull is failing now). Recovery: `POST /retry` (fix #5) once the underlying issue is fixed.

### 6.4 — Sandbox in `Suspended` unexpectedly

- **Cause:** parent workspace was suspended.
- **Detect:** `kubectl get workspace <ws> -o jsonpath='{.status.phase}'` will be `Suspending` or `Suspended`.
- **Fix:** `POST /api/v1/workspaces/:id/resume`.

### 6.5 — Prompt fails with `connection refused` or 5xx

- **Cause:** pod is restarting (fix #1 or fix #3 in flight), pod was OOM-killed, kubelet eviction.
- **Detect:** check sandbox phase. If still `Running`, it's a transient pod problem; phase will move shortly. If `Pending`, restart is in progress.
- **Fix:** retry the request after a few seconds. If persistent, see 6.3.

---

## 7. Status conditions (separate from phase)

The phase is a coarse signal. Conditions carry the why. Three conditions are set:

| Condition Type | True / False / Unknown | Reason | Meaning |
|----------------|------------------------|--------|---------|
| `Ready` | True | `PodRunning` | Sandbox is reachable; proxy will route to it |
| `Ready` | False | `PodNotRunning` | Pod absent or not in Running phase |
| `Ready` | False | `PodCreationFailed` | Pod could not be created (image pull, scheduling, etc.) |
| `PodRunning` | True | `PodRunning` | `corev1.PodPhase == Running` |
| `PodRunning` | False | `PodNotRunning` | Pod absent, Pending, Failed |
| `PodRunning` | False | `PodCreationFailed` | Pod-create attempt errored |
| `Restarting` (new in fix #1) | True | `UserInitiated` / `CredentialRotation` | Restart in flight; goes False when phase returns to Running |

UIs/SDKs/CLIs should **read conditions for the why**, **read phase for the what**.

---

## 8. References

- Reconciler: `controller/internal/sandbox/controller.go`
- Phase constants: `controller/internal/common/constants.go:47-55`
- Condition helpers: `controller/internal/common/condition_adapter.go`
- API routes: `api/internal/server/router.go`
- CRD schema: `pkg/apis/llmsafespace/v1/sandbox_types.go`, `pkg/crds/sandbox_crd.yaml`
- Worklog 0035: cluster validation that surfaced the fragilities this doc + the seven fixes address
- Plan: `design/SANDBOX-ROBUSTNESS-PLAN.md`
