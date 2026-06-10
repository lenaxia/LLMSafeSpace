# Worklog: Longhorn 1.11.2 → 1.12.0 Upgrade Incident — Renovate PR Cluster Outage

**Date:** 2026-06-07
**Agent:** agent-audit-0606
**Repos affected:** `talos-ops-prod`, `LLMSafeSpace`
**Duration:** ~2.5 hours
**Status:** Resolved — Longhorn v1.12.0 stable, Flux reconciling normally

---

## What Happened

### Root Cause

Renovate bot opened and auto-merged two PRs in `talos-ops-prod` at 04:13–04:14 UTC:

| PR | Title | Effect |
|----|-------|--------|
| #1815 | `feat(helm): update chart longhorn to 1.12.0` | Bumped `spec.chart.spec.version: 1.11.2 → 1.12.0` in `kubernetes/apps/storage/longhorn/app/helm-release.yaml` |
| #1805 | `feat(container): update image docker.io/longhornio/longhorn-engine to v1.12.0` | Bumped `defaultSettings.defaultEngineImage` to v1.12.0 |

Flux picked up the changes and triggered a Helm upgrade from Longhorn v1.11.2 → v1.12.0.

### Why the Upgrade Failed (Pre-Upgrade Hook Race — Helm 4 Bug)

The Longhorn 1.12.0 chart includes a `pre-upgrade` Job (`longhorn-pre-upgrade`) with `hook-delete-policy: hook-succeeded,before-hook-creation,hook-failed`. In Helm v4.0.5, the hook wait loop has a race condition:

1. Helm creates the Job
2. The Job runs successfully (verified: completes in ~9s)
3. The `hook-succeeded` delete policy deletes the Job immediately on completion
4. Helm's wait loop polls the Job and gets `NotFound` — treats it as failure

Helm then reports: `pre-upgrade hooks failed: timeout waiting for: [Job/longhorn-system/longhorn-pre-upgrade status: 'NotFound']`

This is a known Helm 4 regression. The workaround is `--no-hooks`.

### The Downgrade Loop

Flux's upgrade remediation (`retries: 7`) kept retrying the failed upgrade. Each retry:
1. Applied the chart with v1.11.2 binary images (from the last successful release)
2. Failed at the pre-upgrade hook
3. Rolled back to the last "deployed" state

Meanwhile the Longhorn data on disk was already at v1.12.0 schema (from a previous successful partial upgrade that wrote the CRDs/settings). The v1.11.2 managers refused to start against v1.12.0 data:

```
FATAL "Error starting manager: failed to upgrade since downgrading from v1.12.0 to v1.11.2 is not supported"
```

This created the outage: all Longhorn managers in CrashLoopBackOff → `longhorn-backend:9500` unreachable → all workspace PVCs unable to mount → all workspace pods stuck in `Init:0/1`.

### Why It Was Hard to Fix

The upgrade was a deadlock:
- Managers need v1.12.0 binary to start (data is v1.12.0)
- Driver-deployer needs healthy managers to register CSI
- Helm's pre-upgrade hook needs the cluster in a state where the Job can be waited on
- Flux kept fighting manual patches by re-applying the last known state

Manual interventions that made it worse:
- Deleting Helm release secrets broke the rollback chain (`MissingRollbackTarget`)
- Manually patching the DaemonSet image was reverted by Flux on every reconcile cycle
- Suspending/resuming Flux at wrong times triggered new upgrade attempts

### What Actually Fixed It

1. **Suspend Flux** to stop the interference loop
2. **Manually patch DaemonSet** to v1.12.0 — managers became healthy, backend came up (HTTP 200)
3. **Use `helm rollback`** to restore a clean "deployed" baseline (revision 22)
4. **Use `helm upgrade --no-hooks`** to bypass the Helm 4 pre-upgrade hook race — this produced a clean deployed revision 26 with v1.12.0
5. **Force-delete stuck terminating** `longhorn-csi-plugin` pod to unblock DaemonSet recreation
6. **Resume Flux** — it found revision 26 as the deployed baseline and the subsequent upgrade attempt (revision 29) succeeded because the CSI components were already running

---

## Fix Required in talos-ops-prod

The pre-upgrade hook race WILL happen again on the next Longhorn upgrade. The ops repo needs a workaround until Helm 4 fixes the race.

**Option A: Add `skipCRDs` and disable hooks via Flux HelmRelease**
```yaml
upgrade:
  crds: Skip
  disableHooks: true    # bypass the pre-upgrade hook entirely
  remediation:
    retries: 3
    remediateLastFailure: true
```

**Option B: Increase timeout so hook has time to be observed before deletion**
```yaml
timeout: 15m    # give hook time to complete AND be observed
upgrade:
  remediation:
    retries: 3
```

**Option C: Pin Renovate to require manual approval for Longhorn PRs**
Add to `renovate.json`:
```json
{
  "packageRules": [{
    "matchPackageNames": ["longhorn"],
    "automerge": false,
    "labels": ["requires-manual-review"]
  }]
}
```

**Recommended: all three.** Longhorn upgrades involve data migrations and should never be auto-merged.

---

## Cluster State After Recovery

| Component | Before | After |
|-----------|--------|-------|
| Longhorn managers | 4× CrashLoopBackOff (v1.11.2 vs v1.12.0 data) | 4× 2/2 Running (v1.12.0) |
| Longhorn backend | `connect: no route to host` | HTTP 200 |
| CSI driver | Not registered on any node | Registered on all 4 workers |
| CSI plugin pods | Stuck terminating | 4× 3/3 Running (v1.12.0) |
| Driver deployer | CrashLoopBackOff | 1/1 Running |
| Helm release | revision 34 failed (v1.11.2) | revision 29 deployed (v1.12.0) |
| Flux HelmRelease | Stalled / retrying | Reconciling normally |
| Workspace PVCs | Unable to mount | Bound, attachable |

---

## Action Items

- [ ] PR in talos-ops-prod: add `upgrade.disableHooks: true` to Longhorn HelmRelease
- [ ] PR in talos-ops-prod: add `timeout: 15m` to Longhorn HelmRelease
- [ ] PR in talos-ops-prod: add Renovate package rule requiring manual approval for Longhorn
- [ ] Resume affected workspace (5b573d58) and verify PVC mounts cleanly
- [ ] Check all other workspace pods that were stuck during outage and resume them

---

## Timeline

| Time (UTC) | Event |
|------------|-------|
| 04:13 | Renovate merges PR #1815 (longhorn chart 1.12.0) |
| 04:14 | Renovate merges PR #1805 (longhorn engine v1.12.0) |
| ~04:20 | Flux picks up changes, begins Helm upgrade |
| 04:34 | First Helm upgrade failure (revision 31) — pre-upgrade hook race |
| 04:38–04:57 | Flux retries 3 more times (revisions 32–34), all fail the same way |
| 04:57 | Flux stalled with `MissingRollbackTarget` |
| ~05:00 | Discovery: all Longhorn managers in CrashLoopBackOff, workspace pods stuck |
| ~05:10 | Manual DaemonSet patch to v1.12.0 — managers start |
| ~05:30 | Helm rollback to clean baseline, then `--no-hooks` upgrade — revision 26 deployed |
| ~06:05 | CSI plugin DaemonSet deletion timeout resolved — all components healthy |
| ~06:15 | Flux reconciles cleanly, revision 29 deployed |
