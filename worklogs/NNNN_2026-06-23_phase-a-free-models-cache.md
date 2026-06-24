# Worklog: Cold-start Optimization, Phase A — Cluster-wide Free-Models Cache (#1a kickoff)

**Date:** 2026-06-23
**Session:** Implement Phase A of the #1a cluster-wide free-models fetch; resolve rebase conflict between PR #386 and Epic 35 (PR #378) on main.
**Status:** Phase A committed locally on `feat/cluster-wide-free-models-cache` (built on top of rebased PR #386 branch). Phases B–D in progress. PR #386 force-pushed after rebase resolution; CI re-running.

---

## Context

PR #386 (`feat/workspace-cold-start-optimization`) shipped boot-path
optimizations (#2–#6) and was open for review. While that was in
flight, PR #378 (Epic 35: secretless credential injection) merged to
main. That PR rewrote the credential-setup init container from a
file-copy pattern into an HTTP-fetch using a projected SA token, AND
mounts the workspace PVC at subPaths inside credential-setup for
US-35.7 symlink creation.

That made PR #386 conflict on main (`mergeStateStatus: DIRTY` per
`gh pr view`). Two of my proposed changes also no longer applied
cleanly:

1. **#6 (merge init containers).** The plan was to fold
   `workspace-dirs` (mkdir of PVC subPaths) into `credential-setup`
   (file-copy of password / user-secrets). Epic 35's new
   credential-setup mounts subPath=home and subPath=workspace at
   container start. A single container's volume mounts are all
   established before the command runs, so kubelet would fail the
   subPath mount on a fresh PVC — the whole point of `workspace-dirs`
   was to create the subPath dirs *before* anything tries to mount
   them by subPath. The merge was incompatible with Epic 35's
   architecture. **Dropped #6 from PR #386**, kept the other four
   items.

2. **The renamed-to-`workspace-bootstrap` test assertions.** Reverted
   to expect `workspace-dirs` since #6 didn't ship.

## Work Completed

### PR #386 rebase

- Aborted the dirty rebase, hard-reset to my pushed branch, then
  rebased onto `origin/main` again, resolving conflicts manually:
  - `controller/internal/workspace/reconciler.go` — both sides added
    `Owns(...)` on different types. Took both: Epic 35's
    `Owns(&corev1.ServiceAccount{})` AND my
    `Owns(&corev1.PersistentVolumeClaim{})` are independent and both
    correct. The PR retains #2 (PVC ownership) verbatim.
  - `controller/internal/workspace/pod_builder.go` — six conflict
    blocks. Took HEAD (Epic 35) as base, re-applied my probe and
    grace-period changes on top:
    - Readiness probe: 10/15/3/5 → 2/2/2/30 (#3)
    - StartupProbe added: 1/1/2/120 (#4)
    - LivenessProbe unchanged (steady-state, conservative)
    - `pod.Spec.TerminationGracePeriodSeconds = ptr(int64(5))` (#5)
    - Init container merge (#6): **DROPPED** for the reason above.
  - `controller/internal/workspace/health_test.go` — kept HEAD's
    Epic 35 assertions (TestInitContainerScript_BootstrapEnvVars,
    bootstrap-token mount checks). My old assertions about
    `user-secrets` mount no longer apply (Epic 35 removed that whole
    flow).
  - `controller/internal/workspace/security_test.go` — reverted my
    `workspace-bootstrap` rename back to `workspace-dirs` (since #6
    didn't ship).
  - `controller/internal/workspace/pod_builder_test.go` — deleted
    three tests that pinned the merged-container behavior:
    `TestPodBuilder_InitContainers_BootstrapSingleContainer`,
    `TestPodBuilder_InitContainers_WithPackagesIncludesSetup`,
    `TestPodBuilder_InitContainerSecurityContext_Hardened`.
    Kept the four behavioral tests for #3, #4, #5 (Readiness,
    StartupProbe, Liveness, TerminationGracePeriod).
  - `README.md` and `README-LLM.md` — reverted the init-container
    architecture line back to `workspace-setup + credential-setup`
    (matching reality, since #6 didn't merge).
  - Worklog `0534_2026-06-23_workspace-perf-investigation.md`
    auto-renumbered by the post-rewrite hook on rebase. Updated the
    `### #6` section in-place from "Implemented" → "DEFERRED" with the
    rationale above.
- Final diff vs main: 7 files, +680/-8 lines. Tests green
  (`go test ./controller/...`), lint clean (`golangci-lint run
  ./controller/...`).
- Force-pushed with `--force-with-lease` (rebase justification per
  README §"Branch and PR workflow"). PR #386 transitioned from DIRTY
  → MERGEABLE; CI is now re-running checks.

### Phase A: Controller-side free-models ConfigMap publisher

New package `controller/internal/freemodels/`:

- **`fetcher.go` (`Fetcher.Fetch`).** Calls `models.dev/api.json`,
  filters to `provider == "opencode" AND cost.input == 0`, returns
  a sorted slice of `Model{ID, Name, ContextLimit, OutputLimit}`.
  Stable sort matters because `SyncConfigMap` short-circuits on
  identical bytes.
- **`refresher.go` (`Refresher` Runnable).** Periodic
  fetch+publish loop. Implements `manager.Runnable` and
  `manager.LeaderElectionRunnable` (returns true) so multi-replica
  controllers don't all fetch independently. First fetch runs
  immediately at `Start()`; subsequent fetches every `Interval`
  (default 6 h, configurable via `--free-models-refresh-interval`).
- **`SyncConfigMap`.** Creates or updates
  `llmsafespaces-free-models` in the controller's namespace.
  No-op fast path when bytes are identical AND no ownerReferences
  need stripping. Strips any pre-existing ownerReferences on update
  (matches `relay-router-peers` CM lifecycle pattern in
  `controller/internal/relay/router_configmap.go`).

Failure semantics (worklog-style for post-deploy operators):

| Failure | Behavior |
|---|---|
| Initial fetch fails at controller startup | Log, retry on next interval. CM is absent. Pods that boot before first success skip the optimization and fall back to the legacy in-pod relay-injector path (Phase D in this plan). |
| Periodic refresh fails | Keep existing CM. Stale-but-valid is strictly better than absent. |
| Empty fetch (transient upstream issue, schema drift) | Same as failure — keep existing CM. Without this guard, an upstream blip would overwrite the CM with `[]` and break every new pod's relay until next refresh. |
| models.dev `opencode` entry missing | Same as empty — preserve existing. |

Tests covering each of these: see `fetcher_test.go` and
`refresher_test.go` (16 tests total, all pass).

Wired into `controller/main.go`:
- New flags: `--enable-free-models-refresher` (default true),
  `--free-models-refresh-interval` (default 6 h),
  `--free-models-api-url` (default empty → models.dev).
- `mgr.Add(&freemodels.Refresher{...})` after the existing gauge
  seeder.

Helm chart wiring:
- New `values.yaml` block:
  ```yaml
  controller:
    freeModelsRefresher:
      enabled: true
      refreshInterval: 6h
      apiURL: ""
  ```
- `controller-deployment.yaml`: adds the three flags conditionally.
- `rbac.yaml`: when `freeModelsRefresher.enabled` is true (and
  `inferenceRelay.enabled` is false), grants the controller's
  release-namespace Role get/list/watch/create/update/patch on
  configmaps. When `inferenceRelay.enabled` is also true the broader
  rule that already includes secrets covers this; the new rule is
  the narrower fallback.

Validation:
- `go test ./controller/internal/freemodels/...` — 16 tests pass.
- `go test ./controller/...` — full controller suite passes.
- `golangci-lint run ./controller/...` — clean.
- `helm template charts/llmsafespaces` — renders the new flags
  conditionally; verified output contains
  `--free-models-refresh-interval=6h`.
- Pre-commit hooks all pass on commit (repolint, gofmt, goimports,
  golangci-lint, helm-render, gitleaks).

## Decisions

1. **Free-models is cluster-wide, not per-workspace.** Confirmed by
   reading `cmd/workspace-agentd/relay_injector.go:109-196`: the
   filter applied (`providerID == "opencode"` AND `cost.input == 0`)
   is independent of the workspace. Today every pod re-fetches the
   list and pays a full opencode-restart cycle (~6–8 s) on every
   cold start. Centralizing it in the controller with a 6 h refresh
   means each pod reads the file directly during init.

2. **Source is `models.dev/api.json`, not opencode's own
   `/provider`.** opencode just proxies models.dev's static catalog,
   so going to the source avoids needing a "scout" pod just for
   discovery. The trade-off is that we trust models.dev's wire
   format; if that schema changes (the `opencode` block disappears
   or model entries grow new fields), the fetcher gracefully
   degrades to "no models found, keep existing CM."

3. **No ownerReference on the ConfigMap.** Matches the
   `relay-router-peers` lifecycle pattern: a CM with an
   ownerReference can race kubelet's volume-mount sync during
   garbage collection. Since this CM is controller-managed
   directly, no GC race exists in the first place.

4. **`NeedLeaderElection() == true`.** With multi-replica
   controllers each replica would otherwise fetch and write
   independently — wasteful, generates spurious ResourceVersion
   churn, and could thrash the CM when fetches return at slightly
   different times.

5. **Phase A ships standalone.** The pod-side consumption (mounting
   the CM, pre-rendering agent-config.json, removing the in-pod
   restart cycle) is bigger work and goes in Phase B–D, on the same
   branch. Phase A is harmless if Phases B–D never land — it just
   publishes a ConfigMap nothing reads.

## Files Modified / Created

```
controller/internal/freemodels/fetcher.go          (new, 192 lines)
controller/internal/freemodels/fetcher_test.go     (new, 165 lines)
controller/internal/freemodels/refresher.go        (new, 161 lines)
controller/internal/freemodels/refresher_test.go   (new, 234 lines)
controller/main.go                                  (+34 lines: flags + mgr.Add)
charts/llmsafespaces/values.yaml                   (+25 lines: freeModelsRefresher block)
charts/llmsafespaces/templates/controller-deployment.yaml  (+13 lines)
charts/llmsafespaces/templates/rbac.yaml           (+10 lines)
```

Plus the rebased PR #386 commit (which I touched only to resolve conflicts):

```
README-LLM.md, README.md                            (init-container line reverted)
controller/internal/workspace/constants.go          (requeueCreating, comments)
controller/internal/workspace/pod_builder.go        (+52: probes, grace, no-merge)
controller/internal/workspace/pod_builder_test.go   (+118 / -103: 4 tests, 3 dropped)
controller/internal/workspace/reconciler.go         (+10: Owns(PVC) + Owns(SA))
controller/internal/workspace/startup_metrics_test.go (+5)
worklogs/0534_2026-06-23_workspace-perf-investigation.md (auto-renumbered)
```

## Next Steps

1. **Phase B:** Mount `llmsafespaces-free-models` ConfigMap into the
   credential-setup init container at `/mnt/freemodels` (volume
   `optional: true`). Add `cp /mnt/freemodels/models.json
   /sandbox-cfg/free-models.json` to the init script.
   Propagate `INFERENCE_RELAY_BASEURL` env into the init container
   (currently only on the main container).

2. **Phase C:** New `applyRelayConfig` in
   `cmd/workspace-agentd/secrets.go`. Reads
   `/sandbox-cfg/free-models.json` AND `INFERENCE_RELAY_BASEURL`
   env. Checks the auth.json bypass condition (existing
   `shouldSkipRelay` logic, which can run pre-opencode because
   auth.json lives on the PVC). If proceeding, calls
   `buildRelayProviderEntry` (already exists in
   `agent_config_writer.go`) and writes the merged
   agent-config.json. Called from `runMaterializeCommand` after
   `applyWorkspaceConfig`.

3. **Phase D:** Modify `cmd/workspace-agentd/relay_injector.go`'s
   `startRelayInjector` to short-circuit when the materialize
   subcommand has already injected the relay block (idempotency
   check via `agentConfigWriter.hasRelay()`).

4. Build + push controller image, deploy to live cluster, re-run
   `/tmp/benchmark-workspaces.sh` to validate the savings.

## Blockers

None. PR #386 CI is running; awaiting automated review per
README §"Branch and PR workflow" step 4–6. Will iterate on review
findings if any, then continue Phases B–D on this branch.
