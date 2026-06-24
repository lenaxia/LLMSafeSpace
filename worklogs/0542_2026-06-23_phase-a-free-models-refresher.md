# Worklog: Cluster-wide Free-Models ConfigMap Publisher (Phase A of #1a)

**Date:** 2026-06-23 / 2026-06-24
**Session:** Implement Phase A of the 2026-06-23 cold-start audit's #1a
item — controller-side periodic publisher that writes the opencode
free-tier model catalog to a cluster ConfigMap. Address PR #400 review
findings (no-op fast path correctness fix, missing tests, missing
worklog).
**Status:** Phase A complete on `feat/free-models-refresher` (PR #400).
Phase B/C/D will follow after Phase A merges.

---

## Why this is its own PR (split from #399)

The original consolidated PR #399 (Phase A through D, ~2300 lines, 17
files) hit the AI reviewer's apparent context-window or timeout limit.
Three review runs hung in `IN_PROGRESS` for 70-90+ minutes each before
being cancelled. PR #386 (smaller, ~7 files) reviewed cleanly in
~3-5 min twice. The pattern was clear: split into smaller focused PRs.

Phase A is the natural cut point — it adds a controller-side Runnable
that publishes a ConfigMap nothing yet consumes. Harmless to merge in
isolation; cleanly extends to Phase B (pod-side mount + cp), Phase C
(pre-boot agent-config relay rendering), and Phase D (in-pod injector
short-circuit) on a follow-up branch.

## What landed

### `controller/internal/freemodels/`

- **`fetcher.go` (`Fetcher.Fetch`).** Calls `models.dev/api.json`,
  filters to `provider == "opencode" AND cost.input == 0`, returns a
  `Model{ID, Name, ContextLimit, OutputLimit}` slice sorted by ID
  for stable ConfigMap diffs.
- **`refresher.go` (`Refresher` Runnable).** Periodic fetch+publish
  loop. Implements `manager.Runnable` AND
  `manager.LeaderElectionRunnable` (returns true so multi-replica
  controllers don't fan out). First fetch runs immediately at
  `Start()`; subsequent fetches every `Interval` (default 6h).
- **`SyncConfigMap`.** Creates or updates the
  `llmsafespaces-free-models` ConfigMap. No-op fast path when
  `data["models.json"]` is byte-identical AND no ownerReferences need
  stripping.

### Helm chart

- New `controller.freeModelsRefresher` block in `values.yaml`
  (default: enabled, 6h refresh, models.dev as upstream).
- Three new flags wired into `controller-deployment.yaml`:
  `--enable-free-models-refresher`, `--free-models-refresh-interval`,
  `--free-models-api-url`.
- RBAC: configmaps verbs (get/list/watch/create/update/patch — no
  `delete`) added to the controller's release-namespace Role when
  `freeModelsRefresher.enabled=true` AND `inferenceRelay.enabled=false`.

### Test policy update

- `chart_test.go::TestF131_ControllerDoesNotGrantUnusedResources`
  split into two subtests:
  - `freeModelsRefresher_disabled_no_configmaps`: opt out of the
    refresher and assert no controller role grants configmaps.
    Preserves the original F1.3.1 invariant for clusters that don't
    need the optimization.
  - `freeModelsRefresher_enabled_narrow_configmaps`: with the default
    (refresher on), at least one controller role MUST grant
    configmaps but verbs are scoped — no `delete` (the refresher
    only publishes; never garbage-collects).

## Key correctness decision: split FetchedAt/Source out of the data payload

**The first version of this PR had a subtle correctness bug** that
the reviewer caught:

`refreshOnce` built the catalog with `FetchedAt: time.Now().UTC()` on
every tick, then called `SyncConfigMap`, which marshalled the WHOLE
`Catalog` (including `FetchedAt`) and compared bytes against the
existing CM. Since `time.Now()` differs every tick, the byte-equality
check was **always false** at the 6h refresh — the CM was Updated
every 6h even when the model list was byte-identical, defeating the
advertised no-op fast path entirely.

The unit test `TestSyncConfigMap_NoOpWhenIdentical` masked this
because it called `SyncConfigMap` directly with a fixed timestamp,
bypassing `refreshOnce` (the only production caller).

**Fix:** split the diagnostic fields out of the data payload and into
ConfigMap annotations:

| Field | Before | After |
|---|---|---|
| `models` | `data["models.json"]` (in JSON envelope) | `data["models.json"]` (sole content) |
| `fetched_at` | inside `data["models.json"]` | annotation `freemodels.llmsafespaces.dev/fetched-at` |
| `source` | inside `data["models.json"]` | annotation `freemodels.llmsafespaces.dev/source` |

The no-op fast path now compares only the model payload — which is
byte-stable across refreshes. Annotations are still updated on a
genuine catalog change but NOT on a pure timestamp refresh (because
the no-op path returns before any Update is called). Operators can
inspect freshness via:

    kubectl get configmap llmsafespaces-free-models \
      -o jsonpath='{.metadata.annotations}'

This is option (1) from the reviewer's three suggested fixes and
preserves the optimization fully.

### Why the no-op fast path matters for Phase B

Phase B (the pod-side consumer) mounts this ConfigMap as a projected
volume into the credential-setup init container. Every CM Update can
trigger a kubelet volume refresh and — depending on Phase B's
consumption pattern — spurious agent-config rebuilds. The stable-sort
+ no-op design exists precisely to avoid that.

The PR #400 review caught this would silently break in production;
without the fix, Phase B would have inherited the bug.

## Other PR #400 review fixes

| Finding | Fix |
|---|---|
| Periodic `case <-tick.C:` branch untested | New `TestRefresher_PeriodicTickFiresMoreThanOnce` with 50 ms interval + counting HTTP server, asserts ≥2 fetches before cancel |
| No integration test for the refreshOnce → SyncConfigMap no-op path | New `TestRefresher_IntegrationNoOpWhenModelsUnchanged` runs the refresher end-to-end and asserts CM `ResourceVersion` does NOT bump across multiple refresh ticks when the model list is unchanged |
| Missing compile-time interface assertions | Added `var _ manager.Runnable = (*Refresher)(nil)` and `var _ manager.LeaderElectionRunnable = (*Refresher)(nil)` mirroring `controller/internal/relay/orphan_detector.go:214-215` |
| `SyncConfigMap` returned speculative `[]byte` | Dropped to `error`-only; the sole caller discarded the bytes, the doc comment claimed they were "for hashing/logging" but no consumer existed |
| Permanent-empty ambiguity in zero-models guard | Documented as a known limitation: cannot distinguish transient outage from permanent free-tier deprecation. Acceptable today (no consumer in this PR; free tier is durable) |
| Optional: nil-check `Refresher.Fetcher` | Added defensive nil check + log; production always sets it via main.go but defends against future caller mistakes |
| Style: `time.Sleep(200ms)` instead of polling | Two `TestRefresher_*` tests aligned to the polling pattern used by `TestRefresher_Start_PublishesOnFirstTick` |

## Failure semantics (operator reference)

| Failure | Behavior |
|---|---|
| Initial fetch fails at controller startup | Log, retry on next interval. CM absent. (Phase B+ pods would fall through to legacy in-pod relay-injector path; not relevant in this PR — no consumer yet.) |
| Periodic refresh fails | Keep existing CM. Stale-but-valid is strictly better than absent. |
| Empty fetch (transient outage / schema drift) | Same as failure — keep existing CM. Without this guard, an upstream blip would overwrite the CM with `[]`. |
| `opencode` entry missing from upstream | Empty list, no error, preserve existing CM. |
| Leader election lost mid-refresh | Refresher stops; the new leader runs its own refresh on Start. No half-written CM (Update is atomic). |

## Validation

- `go test -race -count=1 ./controller/internal/freemodels/... ./charts/llmsafespaces/` — all pass
- `golangci-lint run ./controller/internal/freemodels/... ./charts/llmsafespaces/...` — clean
- `helm template charts/llmsafespaces` — renders the new flags conditionally

## Files Modified / Created

```
charts/llmsafespaces/chart_test.go                 — split F131 test into 2 subtests
charts/llmsafespaces/templates/controller-deployment.yaml  +13 lines
charts/llmsafespaces/templates/rbac.yaml           +9 lines
charts/llmsafespaces/values.yaml                   +23 lines
controller/internal/freemodels/fetcher.go          (new, ~196 lines)
controller/internal/freemodels/fetcher_test.go     (new, ~201 lines)
controller/internal/freemodels/refresher.go        (new, ~225 lines)
controller/internal/freemodels/refresher_test.go   (new, ~417 lines)
controller/main.go                                 +45 lines (flag wiring + mgr.Add)
worklogs/0542_2026-06-23_phase-a-free-models-refresher.md  (this file)
```

## Next steps

1. Wait for PR #400 to clear the bot review and merge.
2. Open Phase B/C/D PR (rebased onto merged Phase A). That PR adds the
   pod-spec changes (mount the CM, propagate INFERENCE_RELAY_BASEURL
   into the init container, copy models.json to /sandbox-cfg/),
   `applyRelayConfigPreBoot` in agentd's materialize subcommand, and
   the `hasRelay()` short-circuit in the in-pod relay injector.
3. Build + push controller and runtime images, deploy to live cluster,
   re-run `/tmp/benchmark-workspaces.sh` to validate the savings (PR #386
   target was ~25-30s cold start; #1a target is ~22s).
