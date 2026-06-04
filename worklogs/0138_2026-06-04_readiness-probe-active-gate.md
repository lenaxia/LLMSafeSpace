# Worklog: Gate Active Phase on Readiness Probe

**Date:** 2026-06-04
**Session:** Fix 500 on POST /sessions/new caused by race between workspace Active transition and opencode startup
**Status:** Complete

---

## Objective

Eliminate a race condition where the API returned 500 (`connection refused`) on `POST /workspaces/:id/sessions/new` immediately after a workspace transitioned to Active phase, because opencode had not yet finished starting inside the pod.

---

## Work Completed

### Root Cause

`phase_creating.go` transitioned the workspace to `Active` on `PodRunning && PodIP != ""`. This only means the container process started — not that opencode was ready to serve HTTP on `:4096`. A readiness probe against `/v1/readyz:4096` already existed in the pod spec (added in pod_builder.go) but was never consulted by the controller when deciding to set `Active`.

The frontend fires `POST /sessions/new` as soon as the workspace reaches Active. During the window between "pod Running" and "readiness probe passes", the API proxied the request to a pod that was refusing connections → 500.

### Fix

Single-source fix in the controller — the correct place, since the controller owns the lifecycle state machine.

**`controller/internal/workspace/helpers.go`**
- Added `allContainersReady(pod *corev1.Pod) bool`: returns true only when every `ContainerStatus.Ready == true`. Kubernetes sets this field to true only after the readiness probe passes. Returns false for nil pod or empty ContainerStatuses.

**`controller/internal/workspace/phase_creating.go:105`**
- Changed condition from `PodRunning && PodIP != ""` to `PodRunning && PodIP != "" && allContainersReady(existingPod)`.
- One-line change. No other logic altered.

**`controller/internal/workspace/container_ready_test.go`** (new file)
- 6 unit tests for `allContainersReady`: nil pod, no statuses, all ready, one not ready, all not ready, single ready. Written before implementation (TDD).

**`controller/internal/workspace/controller_test.go`**
- `makeRunningPod` updated to include `ContainerStatuses: [{Ready: true}]` to match the new contract.
- Added `makeRunningPodNotReady` helper with `ContainerStatuses: [{Ready: false}]`.
- Added `TestReconcile_Creating_PodRunningNotReady_StaysInCreating`: verifies a Running pod that has not passed its readiness probe stays in Creating with no PodIP or Endpoint set.

### Why this approach and not proxy-side retry

Rejected: adding retry/backoff in the proxy handler.
- Violates single responsibility — the proxy shouldn't reason about sandbox startup state
- Masks real failures (a crashed opencode would retry forever)
- Doesn't fix the data model — `Active` would still mean "maybe ready"
- The controller already has the readiness probe on the pod; this fix just uses it

The correct invariant is: `WorkspacePhaseActive` means "opencode is ready to serve HTTP". The readiness probe already enforces this at the K8s layer; the controller just needed to respect it.

---

## Key Decisions

- Fix at the controller, not the proxy. The lifecycle state machine is the controller's responsibility.
- Use `ContainerStatus.Ready` (set by kubelet after probe passes) rather than probing from the controller directly — avoids introducing network calls in the reconcile loop.
- No change to `InitialDelaySeconds` (10s) or other probe tuning — those values are already set and this bug is unrelated to probe configuration.

---

## Blockers

None.

---

## Tests Run

```
cd controller && go test -timeout 60s -race ./internal/workspace/...
ok  github.com/lenaxia/llmsafespace/controller/internal/workspace  1.410s
```

---

## Next Steps

Deploy and confirm no more 500s on `POST /sessions/new` immediately after workspace reaches Active.

---

## Files Modified

- `controller/internal/workspace/helpers.go` — added `allContainersReady`
- `controller/internal/workspace/phase_creating.go` — added `allContainersReady` guard to Active transition
- `controller/internal/workspace/container_ready_test.go` — new file, 6 unit tests
- `controller/internal/workspace/controller_test.go` — updated `makeRunningPod`, added `makeRunningPodNotReady`, added `TestReconcile_Creating_PodRunningNotReady_StaysInCreating`
- `worklogs/0138_2026-06-04_readiness-probe-active-gate.md` — this file
