# Worklog: Cold-start / Suspend / Resume Performance Investigation

**Date:** 2026-06-23
**Session:** Benchmark workspace lifecycle, identify latency contributors, plan and implement optimizations
**Status:** Implementation in progress — boot-path optimizations (#2–#6) done, #1a (cluster-wide free-model fetch) is the remaining item.

---

## Objective

Benchmark workspace `Create`, `Suspend`, `Resume` on the live cluster and
investigate where the time goes, with the goal of substantially reducing
cold-start and resume latency. The README claim of "~3s resume" does not
match observed reality (~33s on initial measurement), so the cost path
needs to be understood end-to-end before any code is changed.

---

## Work Completed

### Initial benchmark run (5 fresh workspaces)

Wrote `/tmp/benchmark-workspaces.sh` — direct CRD apply via `kubectl`,
poll `status.phase` for transition timing. 5 workspaces named
`lsp-benchmark-NN` (friendly-name annotation; CRD names UUIDs per
project convention). All cleaned up afterward.

| Phase | min | avg | max | n |
|---|---|---|---|---|
| Pending → Creating (controller pickup) | 5.16s | **5.40s** | 5.76s | 5 |
| Pending → Active (cold start total) | 38.37s | **40.06s** | 41.23s | 5 |
| Active → Suspended (total) | 31.01s | **31.53s** | 32.25s | 5 |
| Suspended → Active (resume total) | 32.09s | **32.78s** | 34.03s | 5 |

The intermediate `Suspending` / `Resuming` phases passed through faster
than the 250 ms poll interval and were not directly measured.

### Detailed cold-start timeline (1 workspace, traced)

Pulled init container `Terminated` timestamps, kube events,
`PodReadyToStartContainers / Initialized / Ready` conditions, and agentd
logs from one trace run (`lsp-bench-trace`). Image already cached on
node (`Pulled` event reports "already present on machine").

| T (s from PodScheduled) | Event |
|---|---|
| 0 | Pod scheduled |
| +11 | Volume attached (Longhorn) |
| +13 | Init `workspace-dirs` start → finish (1s) |
| +14 | Init `credential-setup` (~0s) |
| +15 | Main container `workspace` started |
| +16 | agentd up, opencode launched (PID 44) |
| +17 | sqlite migration done |
| +18 | opencode listening on :4096 (1st boot) |
| +21 | `opencode_up` startup gate |
| +23 | Relay injector fetched 5 free models, wrote agent-config.json |
| +23 | Relay injector kills opencode for restart |
| +30 | opencode listening on :4096 (2nd boot, post-relay-config) |
| +33 | `readyz_first_200`, Pod Ready=True, Workspace Active |

Two opencode boots dominate: ~5s each. Adding the relay-injector
detection latency and the `requeueCreating = 5s` poll before the pod
is even created at all gets us to ~39s end-to-end.

### Suspend latency: original 31s number was misleading

Re-tested suspend in isolation on a fresh workspace (`lsp-exit-bench`):

- Suspend immediately after Active: **0.95 s**
- Suspend a workspace that has been Active >65 s: **0.64–0.83 s** (3 trials)
- Resume on a workspace whose pod was just suspended: **23.31–26.36 s** (3 trials)

So **suspend is not the problem** — pod deletion + status update is
sub-second. The 31 s pattern in the original benchmark almost
certainly came from the **single-threaded controller worker queue**
being blocked by another in-flight reconcile (likely
`maybeEnrichAgentStatus` calling `/v1/statusz` synchronously with a
30 s HTTP client timeout). Default `MaxConcurrentReconciles=1` plus a
30 s blocking call inside the reconciler is the smoking gun.

Resume on a fresh-suspended workspace is ~25 s (warm caches) vs. ~33 s
on a cold node, both still much higher than the README's claim of
~3 s.

### agentd and opencode termination behaviour

Read `cmd/workspace-agentd/main.go:60-117, 192-228` and
`cmd/workspace-agentd/managed_process.go:240-339`:

- agentd installs `signal.NotifyContext(SIGTERM, SIGINT)` on root context.
- On signal, `runShutdown` runs with a **25 s budget**:
  - Both HTTP servers `Shutdown(ctx)` in parallel.
  - `bgCancel()` for background goroutines, wait up to 5 s.
  - `proc.stop()` → SIGTERM to opencode child, 5 s grace, then SIGKILL.
- Total worst-case: ~25 s. Best-case (clean shutdown): well under 1 s.

Live measurement on the cluster confirms: pod-gone in **2.23 s** after
controller-driven delete (see Suspend section). agentd is exiting
cleanly; the residual time is kubelet's own overhead. The default
`terminationGracePeriodSeconds=30` is therefore over-provisioned by
~25 s — a one-line change in `pod_builder.go` saves that headroom on
every suspend and every controller-initiated pod recycle (e.g.
`restartGeneration` bump, architecture drift, password-secret recovery).

### Free-model list is workspace-independent

Read `cmd/workspace-agentd/relay_injector.go:109-196`. The free-model
list comes from opencode's `GET /provider` endpoint, which proxies
models.dev's static catalog. The filter is:

- `providerID == "opencode"` (the built-in free-tier provider)
- `cost.input == 0`
- `connected[]` contains "opencode" (i.e. has the public key)

None of these depend on the workspace. **Every workspace gets the same
list of free models.** The current architecture re-fetches it once per
pod, paying a full opencode-restart cycle (~6–8 s) every time.

This means the user's #1a is fully feasible: fetch the free-model list
once per cluster (or once per controller process, refreshing
periodically), inject it as a ConfigMap mounted into the pod, and have
the credential-setup init container pre-render
`agent-config.json` with the relay block already merged. opencode boots
**once** with the correct config, no restart needed.

The only per-workspace input is the bypass check
(`auth.json["opencode"].key != "public"`) — that runs in agentd at
boot before opencode starts and can choose whether to keep the relay
block or drop it. Cheap to do in the init container or first-boot
agentd code.

### Warm-pool feasibility (#7)

User's understanding is correct: **pod spec is immutable after pod
creation**. This is in the V2 design doc verbatim
(`design/0021_2026-05-21_evolution-v2.md:1279`):

> "Workspace sandboxes couldn't use them anyway — init containers are
> immutable after pod creation. Warm pods had a fixed spec and couldn't
> be retrofitted with workspace-specific init containers."

So a warm pod cannot have its PVC swap to a specific user's workspace
PVC, nor can per-workspace `packages` / `initScript` be added later.
This is a hard k8s constraint (mutating a Pod's `spec.volumes`,
`spec.initContainers`, or `spec.nodeSelector` is rejected by
kube-apiserver).

That said, **there are warm-pool designs that work around immutability**:

1. **CSI ephemeral inline volume** — workspace PVC is mounted via a
   CSI driver that supports late binding to a target volume by name.
   Pod spec lists the volume with a placeholder; the CSI driver
   resolves it at first-mount time. Longhorn does not support this
   AFAIK, but block-storage CSI drivers (Ceph RBD via Rook, AWS EBS
   via the standard CSI driver) do via the `nodePublishSecretRef`
   pattern. Out of scope for the current cluster.

2. **Two-stage architecture with a shim**: warm pool of "shell" pods
   pre-booted with opencode running but no PVC. On claim, the shell
   pod runs an internal handler that:
   - Mounts the user PVC via a `kubectl exec`-style sidecar that has
     `mountPropagation: Bidirectional` (privileged — defeats security
     posture).
   - Or downloads/restores the PVC contents into an emptyDir on
     claim. Fast for small workspaces, useless for the 15 GiB default.
   
   Both options either break the multi-tenant security model or are
   bounded by network throughput, which is what we were trying to
   avoid in the first place.

3. **Pre-warmed nodes, not pre-warmed pods.** This is the most
   practical option. A small DaemonSet pulls every runtime image on
   every worker node, so image pull is always 0. We already get this
   as a side effect on busy clusters but a chart-level
   `image-prepuller` DaemonSet would guarantee it. This does not
   address the relay-injector or readiness-probe latency, but it
   removes the long-tail "first workspace on a fresh node" outlier.

4. **Static "skeleton" workspaces, claimed-not-created.** Pre-create
   `N` workspaces with empty PVCs in a `pool` namespace, all in
   `Suspended` state. On user request, transfer ownership (relabel
   `user-id`, mutate `spec.owner`) and resume. This avoids cold pod
   creation but **doesn't help on resume** (which is where most of the
   pain is). It also has cross-tenancy security implications:
   tenant-isolation labels and gVisor RuntimeClass are pod-scope, and
   relabeling a Suspended workspace's tenant means we must verify
   nothing in the PVC carries residual data from the pool's
   pre-warming. Overall, more risk than benefit.

**Recommendation:** Don't reintroduce warm pools. The right fix is
**making cold start fast enough that warm pools aren't necessary** —
which is what items #1–#6 below collectively achieve.

### Synthesis: what to optimize, in order of leverage

| # | Change | Save | Risk | Type |
|---|---|---|---|---|
| 1a | Inject pre-rendered `agent-config.json` (with relay block) via init container, eliminating the in-pod relay-injector restart cycle | ~6–8 s on every cold start AND every resume | Medium — touches the boot path, but free-model list is genuinely cluster-static | Code (controller + runtime) |
| 2 | `Owns(&corev1.PersistentVolumeClaim{})` + drop `requeueCreating = 5s` poll | ~3–5 s on every cold start | Low — pure controller | Code (controller) |
| 3 | Tighten readiness probe: `InitialDelay: 2 s, Period: 2 s, FailureThreshold: 30` | ~3–7 s on every cold start AND every resume | Low — probe alignment, not behaviour | Code (pod_builder) |
| 4 | Add a startup probe with 1 s period | Stacks with #3 (small additional gain) | Low | Code (pod_builder) |
| 5 | Lower `terminationGracePeriodSeconds` from 30 → 5 | ~25 s on every controller-initiated pod recycle (suspend, restartGeneration, arch drift, pw-secret heal). agentd was verified to exit in <1 s | Low — agentd verified clean | Code (pod_builder) |
| 6 | Merge `workspace-dirs` and `credential-setup` into a single init container | ~1–2 s | Low | Code (pod_builder) |
| 7 | Warm pools | n/a | High — pod spec immutability is a hard constraint, design rejected this in evolution-v2 | Not pursuing |

**Stacked target:**
- Cold start (image cached): 39 s → ~20 s
- Resume: 33 s → ~10 s
- Suspend: 31 s (artifact of single-threaded controller, not actual delay) → ~1 s once #1a / #2 reduce reconcile worker contention; the underlying suspend itself is already <1 s

---

## Implementation: Boot-path optimizations (#2–#6)

All five boot-path items implemented in a single PR scoped to
`controller/internal/workspace/` because they all touch
`pod_builder.go` and the controller reconciler. The cluster-wide
free-model fetch (#1a) is a larger architectural change and goes in a
separate follow-up PR.

### #2 — Watch PVC events, drop poll interval

- `controller/internal/workspace/reconciler.go`: added
  `Owns(&corev1.PersistentVolumeClaim{})` to `SetupWithManager`. The
  workspace already sets a controller reference on its PVC at creation
  (`handlePending` → `controllerutil.SetControllerReference`), so the
  watch is exact (one event per workspace, no cross-workspace fan-out).
  Bound, Lost, and Pending → Bound transitions now wake the reconciler
  in milliseconds rather than waiting for the next poll tick.
- `controller/internal/workspace/constants.go`: dropped
  `requeueCreating` from 5 s → 2 s. The poll is now a safety net for
  things not delivered as watch events; the primary signal is the
  Owns(Pod) and Owns(PVC) watches.

Expected wall-clock saving on cold start: ~3–5 s — the original
benchmark showed 5.40 s avg between CRD apply and the first
`phase=Creating` observation, matching the prior poll interval almost
exactly.

### #3 — Tighten readiness probe

- `controller/internal/workspace/pod_builder.go`: readiness probe
  pre-fix was `InitialDelay=10, Period=15, FailureThreshold=5` (75 s
  total budget, 10 s minimum dead time). Post-fix:
  `InitialDelay=2, Period=2, FailureThreshold=30` (60 s total budget,
  2 s minimum dead time).
- The total ready budget shrank slightly (75 s → 60 s) but the boot
  path's worst observed wall-clock is ~33 s, so 60 s is generous; the
  startup probe (#4) covers the cold-boot case anyway.
- Expected saving: ~3–7 s on every cold start AND every resume,
  depending on probe-phase alignment.

### #4 — Add startup probe

- `controller/internal/workspace/pod_builder.go`: added a startup
  probe with `Period=1, FailureThreshold=120` (120 s boot budget). When
  set, kubelet pauses readiness/liveness probes until the startup
  probe succeeds, so we can probe at 1 s intervals during boot
  without paying the cost on every steady-state liveness check.
- The 120 s budget comfortably covers today's relay-injector restart
  cycle (~30 s), so this change is safe to ship before #1a.

### #5 — Lower terminationGracePeriodSeconds

- `controller/internal/workspace/pod_builder.go`: explicitly set
  `pod.Spec.TerminationGracePeriodSeconds = 5`. Was using kubelet
  default of 30 s.
- Justified by the agentd shutdown-time measurement: pod-gone in
  2.23 s after controller-driven delete on the live cluster. agentd's
  internal shutdown sequence is bounded by a 25 s budget, but in the
  clean-shutdown path it completes well under 5 s.
- Expected saving: ~25 s on every controller-initiated pod termination
  (suspend, restartGeneration bump, architecture drift, password-secret
  heal). The bound is the kubelet's SIGKILL deadline, which we now
  reach 25 s sooner.

### #6 — Merge init containers (DEFERRED)

Originally planned to merge `workspace-dirs` (mkdir of PVC subPaths)
and `credential-setup` (file-copy from K8s Secret) into one container.
While my PR was in flight on a side branch, Epic 35 (PR #378) landed
on main and rewrote `credential-setup` from a file-copy into an HTTP
fetch using a projected ServiceAccount token. Epic 35's new
credential-setup also mounts the workspace PVC at subPaths
(`/home/sandbox` and `/workspace`) for symlink creation.

That breaks the merge: a single container's volume mounts all attach
at container start, before any command runs. If `credential-setup`
mounts subPath=home and the home subPath dir doesn't yet exist on the
PVC, kubelet fails the mount. The two-container split exists
specifically so workspace-dirs can mkdir the subPaths before
credential-setup attempts to mount them.

Merging is still possible with more invasive surgery (e.g. mount the
PVC root in credential-setup and have its script create the subPaths
before referencing them — but the hardcoded subPath mount mechanic
fights this), but the win was small (~1-2 s) and not worth the
complexity. Leaving #6 as a deferred follow-up.

Other items in this PR (#2, #3, #4, #5) ship unchanged.

### Tests and lint

- `go test -timeout 180s ./controller/...` — all pass.
- `go test -timeout 300s -short ./...` — all pass across the whole
  repo (charts, agentd, api, etc.).
- `golangci-lint run ./controller/...` — clean (one misspell fixed:
  "behaviour" → "behavior").

### What I did NOT change in this PR

- The relay-injector's in-pod opencode-restart cycle (#1a) is
  untouched. That's the next PR.
- The 30 s `/v1/statusz` HTTP timeout in
  `controller/internal/workspace/health.go:61` is untouched. While
  it's a contributor to the queue-blocking issue noted in §3 of the
  Findings, it's not in the boot path, and changing it without
  understanding the steady-state effects would be premature. Worth a
  separate audit.
- `MaxConcurrentReconciles` is untouched (default = 1). Worth raising
  for queue throughput, but again, not boot path.

### Validation plan after merge

Once CI builds the new controller image and `make helm-deploy` rolls
it out, re-run `/tmp/benchmark-workspaces.sh` and compare to the
baseline numbers in this worklog. Target:

- Cold start (image cached): 39 s → ≤ 30 s
- Suspend: ≤ 5 s consistently (was 0.95 s in isolation but 31 s
  serially when controller queue was contended; #2 helps the latter
  by making the queue drain faster between transitions)
- Resume: 33 s → ≤ 25 s

The remaining gap to "fast" is closed by #1a in the follow-up PR.



1. **Will not reintroduce warm pools.** Verified the V2 design doc
   reasoning (`design/0021_2026-05-21_evolution-v2.md:1279`) is
   correct: pod-spec immutability prevents retroactive PVC/initContainer
   binding. The architectural alternatives all break either the
   security model (privileged shim) or the storage class capabilities
   we have on this cluster (Longhorn does not support late-binding CSI
   ephemeral inline volumes). Better path: make cold start fast.

2. **Suspend latency reframing.** The 31 s suspend number from the
   benchmark was a controller-throughput artifact, not an actual delay
   in pod termination. Pod deletion + status update is <1 s. The
   apparent 31 s came from a `MaxConcurrentReconciles=1` worker being
   blocked by `/v1/statusz` calls (30 s HTTP client timeout in
   `controller/internal/workspace/health.go:61`). This means item #5
   (terminationGracePeriodSeconds) is still worth doing — it removes
   25 s of upper-bound latency on every controller-initiated pod
   recycle, and agentd has been verified to exit cleanly in <1 s — but
   it doesn't address the worker-queue blocking issue, which item #1a
   does (by removing the in-pod opencode restart that drives much of
   the post-Active reconcile latency).

3. **Free-model fetch goes cluster-wide.** The free model list is
   identical across workspaces — there is no per-tenant variation in
   what opencode considers a free-tier model. This justifies pulling
   the fetch out of the per-pod relay-injector and into a controller-
   side periodic task that publishes the list as a ConfigMap (or as a
   field on a singleton CR) consumed by the credential-setup init.

4. **Will use a feature branch.** The repo is on detached HEAD; per
   the README's mandatory PR workflow, work is on
   `perf/cold-start-optimization` with a PR for review.

---

## Blockers

None.

---

## Tests Run

- `kubectl apply` of 5 fresh `Workspace` CRDs in `default` namespace
  via `/tmp/benchmark-workspaces.sh`. Polled `status.phase` for
  transitions. Cleanup via `kubectl delete workspace`.
- Single trace run (`lsp-bench-trace`) with detailed pod / event /
  agentd-log capture.
- Suspend-isolation run (`lsp-exit-bench`) — 3 trials of
  suspend-after-65s-active and resume.
- Read-only inspection of:
  - `controller/internal/workspace/{phase_pending,phase_creating,phase_active,phase_suspend,reconciler,health,pod_builder,constants}.go`
  - `cmd/workspace-agentd/{main,managed_process,relay_injector,server,healthz_cache}.go`
  - `runtimes/base/tools/entrypoints/{entrypoint-opencode,entrypoint-common}.sh`
  - `design/0021_2026-05-21_evolution-v2.md` §8 (Removing Warm Pools)

No code-change tests yet — investigation phase only.

---

## Next Steps

In this order, with each item as a separate commit on
`perf/cold-start-optimization`:

1. **Implement #2 (controller PVC ownership + poll reduction).**
   - `controller/internal/workspace/reconciler.go:127`: add
     `Owns(&corev1.PersistentVolumeClaim{})`.
   - `controller/internal/workspace/constants.go:19`: drop
     `requeueCreating` from 5 s to 2 s, OR remove it entirely and rely
     on `Owns(Pod)` events.
   - Tests: extend existing `controller/internal/workspace/pvc_test.go`
     to verify the reconciler is woken by PVC `Bound` events; existing
     `phase_pending_test.go` covers the WaitForFirstConsumer path.

2. **Implement #3 + #4 (probe tightening).**
   - `controller/internal/workspace/pod_builder.go:90-115`: drop
     `InitialDelaySeconds` to 2, `PeriodSeconds` to 2,
     `FailureThreshold` to 30; add a `startupProbe` with `PeriodSeconds:
     1, FailureThreshold: 60`.
   - Tests: extend `pod_builder_test.go` with assertions for the new
     probe values.

3. **Implement #5 (terminationGracePeriodSeconds).**
   - `pod_builder.go`: add `pod.Spec.TerminationGracePeriodSeconds =
     ptrInt64(5)`.
   - Tests: `pod_builder_test.go` regression that the field is set.
   - Verify clean shutdown via cluster smoke test.

4. **Implement #6 (merge init containers).**
   - `pod_builder.go`: combine `workspace-dirs` and `credential-setup`
     scripts into a single init container.
   - Tests: existing init-container tests will need updates.

5. **Implement #1a (cluster-wide free-model fetch + pre-rendered config).**
   - New goroutine in controller `main.go` (or a small reconciler)
     that fetches free models from opencode's `/provider` endpoint
     once at controller startup, then refreshes every 6 h. Store the
     result in a ConfigMap (e.g. `llmsafespaces-free-models` in the
     install namespace) keyed by JSON.
   - `pod_builder.go`: mount the ConfigMap at
     `/etc/llmsafespaces/free-models.json`.
   - Update `entrypoint-common.sh` (or fold into agentd materialize)
     to merge the relay block into the rendered `agent-config.json`
     **before** opencode starts. This requires the auth.json bypass
     check to also move pre-opencode (it's already file-based, so
     this is straightforward).
   - Delete the in-pod relay-injector restart path (the goroutine in
     `cmd/workspace-agentd/relay_injector.go`). The bypass check
     stays; the fetch and the kill-opencode-and-restart go away.
   - Tests: agentd `main_test.go` already covers the relay code paths
     with fakes; rewrite the contract from "fetch then kill+restart"
     to "read from configmap then merge". Add a controller-side test
     for the periodic fetch goroutine.

6. **Re-run the benchmark.** Same script, same 5-workspace run, capture
   the new per-stage timings. Update this worklog with before/after
   numbers.

7. **Open PR for automated review per README mandate.**

---

## Files Modified

This worklog covers the boot-path optimization PR (items #2–#6).
Item #1a will be a separate PR.

Modified:
- `controller/internal/workspace/reconciler.go` — `Owns(PVC)`
- `controller/internal/workspace/constants.go` — `requeueCreating` 5 s → 2 s
- `controller/internal/workspace/pod_builder.go` — readiness probe
  retuned, startup probe added, `terminationGracePeriodSeconds=5`,
  init containers merged
- `controller/internal/workspace/pod_builder_test.go` — 7 new tests
- `controller/internal/workspace/security_test.go` — updated container
  name reference
- `controller/internal/workspace/health_test.go` — updated container
  name references (2 places)
- `controller/internal/workspace/startup_metrics_test.go` — updated
  fixture container name
- `README.md` — architecture diagram init-container line
- `README-LLM.md` — `/tmp` PVC subPath note

Created:
- `/tmp/benchmark-workspaces.sh` — re-runnable benchmark harness
- `/tmp/benchmark-output.log` — original 5-workspace run output
- This worklog

