# Worklog 0197 — Migrate sandbox-home from emptyDir to PVC subPath

**Date:** 2026-06-09

## Problem

Workspace pods are being evicted by Kubernetes due to the `sandbox-home` emptyDir exceeding
its hardcoded `1Gi` sizeLimit. The eviction loop is:

1. Agent runs `gh run view --log` — downloads CI run log zips to `~/.cache/gh/`
2. Go build cache (`~/.cache/go-build/`) and npm cache (`~/.npm/_cacache/`) accumulate
3. `sandbox-home` hits 1Gi → Kubernetes evicts the pod immediately (no graceful shutdown)
4. Controller's infrastructure-recovery path rebuilds the pod **without** re-emitting
   `workspace-secrets-<id>` (separate bug, worklog TBD)
5. Pod boots without user credentials → `ProviderModelNotFoundError` for any non-relay model
6. Agent resumes work → downloads more logs → eviction loop repeats

Observed in production: workspace `a3a1c914` was evicted twice in the same session.
Workspace `9b7c0e76` was at 475M (47% of limit) with the same accumulation pattern.

Root cause of the size problem: `sandbox-home` is an emptyDir, so every pod restart
cold-starts all caches. The Go build cache and npm cache re-accumulate from zero each boot,
and unbounded `gh` log zips compound the problem.

## Decision: PVC subPath merge

Migrate `/home/sandbox` from a 1Gi emptyDir to a subPath on the existing workspace PVC.
**No second PVC.** Use `subPath: home` on the same PVC already mounted at `/workspace`.

Rationale:
- Single PVC = single billing unit; Longhorn thin-provisions so unused space costs nothing
- Go build cache + npm cache survive pod restarts → faster cold starts, less re-accumulation
- `.ssh/`, `.git-credentials`, `.secrets/`, enricher cache all survive suspend/resume
  (fixes a persistent UX complaint; currently re-materialized on every boot)
- `subPath` is a native Kubernetes feature, no new infrastructure required
- The existing `spec.storage.size` CRD field already controls PVC size; bump default from
  5Gi to 10Gi to accommodate combined workspace + home usage

Rejected alternative — just increase emptyDir sizeLimit:
- Still ephemeral; cold-start problem unchanged
- Consumes node ephemeral storage (shared across all pods on the node)
- Root cause (unbounded cache accumulation) unaddressed; just slower to trigger

## Scope of changes

This is **not** mostly helm. It is entirely controller Go code + CRD + one API change.
The helm chart has zero pod template YAML for workspace pods — the entire pod spec is
built in Go. The helm chart only needs a values default bump.

### 1. `controller/internal/workspace/pod_builder.go` — primary change

**Current (lines 114–141):**
```go
// VolumeMount — main container
{Name: "workspace",    MountPath: "/workspace"},
{Name: "sandbox-home", MountPath: "/home/sandbox"},

// Volumes
{Name: "workspace", VolumeSource: corev1.VolumeSource{
    PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
        ClaimName: workspace.Status.PVCName,
    },
}},
{Name: "sandbox-home", VolumeSource: corev1.VolumeSource{
    EmptyDir: &corev1.EmptyDirVolumeSource{
        SizeLimit: ptrQuantity("1Gi"),
    },
}},
```

**After:**
```go
// VolumeMount — main container
{Name: "workspace", MountPath: "/workspace", SubPath: "workspace"},
{Name: "workspace", MountPath: "/home/sandbox", SubPath: "home"},

// Volume — only one entry now; sandbox-home emptyDir removed entirely
{Name: "workspace", VolumeSource: corev1.VolumeSource{
    PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
        ClaimName: workspace.Status.PVCName,
    },
}},
```

Note: Kubernetes allows the same volume to appear in `volumeMounts` twice with different
subPaths and different mountPaths. The `volumes` list still has only one entry for
`workspace`.

Also update the `workspace-setup` init container mount (line 407–409) — it currently
mounts `workspace` at `/workspace` with no subPath. After the change it needs
`SubPath: "workspace"` to avoid touching the `home` subtree.

Remove the `sandbox-home` emptyDir entry from the volumes slice entirely.

### 2. `controller/internal/workspace/phase_pending.go` — PVC initialization

When the controller first creates the PVC (in `handlePending`), the PVC starts empty.
The `workspace` subPath directory will be created automatically by Kubernetes on first
mount. The `home` subPath directory also needs to exist before the pod can mount it.

Two options:
- **Option A (preferred):** Kubernetes creates subPath directories automatically on
  first mount if they don't exist. No explicit init required. Verify this behavior
  with Longhorn — it is standard Kubernetes behavior but worth confirming.
- **Option B (fallback):** Add a `pvc-init` init container that runs before
  `credential-setup` and `workspace-setup`, executes `mkdir -p /pvc/workspace /pvc/home`,
  mounting the PVC root at `/pvc` with no subPath.

If Option A works (expected), no change needed here.

### 3. `controller/internal/workspace/security_test.go`

Lines 110 and 144 assert `sandbox-home` exists in volumes and mounts. Update:
- Remove `"sandbox-home": false` from `expectedVolumes`
- Remove `"sandbox-home": false` from `expectedMounts`
- Add assertion that `workspace` volume appears in mounts **twice** (once for each subPath)
  — or verify the test structure supports duplicate mount names

The security test currently checks for presence/absence of volume names. The duplicate
`workspace` mount (two entries with different subPaths) may require adjusting the map
structure from `map[string]bool` to a count-based check.

### 4. `pkg/apis/llmsafespace/v1/workspace_types.go` — optional new CRD field

Currently `spec.storage.size` covers only the PVC. With the home dir on the same PVC,
the user's storage allocation covers both `/workspace` and `/home/sandbox`. No new field
is strictly required.

However, to give operators visibility: add optional `spec.storage.homeSize` as a
documentation/advisory field (not enforced by the controller — the PVC is one unit).
Alternatively, keep the CRD unchanged and just update the field description to say
"covers both /workspace and /home/sandbox".

**Recommendation:** update the description only, no new field. Avoids CRD schema churn.

### 5. `charts/llmsafespace/crds/workspace.yaml` + `pkg/crds/workspace_crd.yaml`

If field description is updated (recommendation above), update both copies of the CRD.
These are kept in sync manually — both must be updated together.

If a new `homeSize` field is added, add it to both CRD files and the Go type.

### 6. `charts/llmsafespace/values.yaml`

No direct pod-template values exist for workspace pods. The only needed change is
documentation/defaults:

- Add a comment to `maxWorkspaceStorageGi` clarifying it now covers both workspace and
  home subtrees
- No new helm value for `sandboxHomeSize` — the size is now governed entirely by
  `spec.storage.size` at workspace creation time

If the API service has a default PVC size it passes when creating workspaces, find and
bump that default from 5Gi to 10Gi (see item 7 below).

### 7. API service — default PVC size at workspace creation

**File:** `api/internal/handlers/workspaces.go` or `api/internal/services/workspace/workspace_service.go`

Need to find where the API sets `spec.storage.size` when a user creates a workspace
(the frontend likely does not send a storage size — the API fills in a default).
Grep for where `WorkspaceStorageConfig` is populated or where `"5Gi"` appears as a
literal default. Bump this default from `"5Gi"` to `"10Gi"`.

The test fixture in `controller_test.go:59` uses `"5Gi"` — update to `"10Gi"` to
match the new default, or leave it as-is (it's a unit test that doesn't need to track
the product default).

### 8. `cmd/workspace-agentd/secrets.go` — enricher cache dir

Current (line 93–103): `enricherCacheDir` defaults to `$HOME/.local/state/llmsafespace`.

After migration this directory will be on the PVC (persisted). This is an improvement —
the enricher's 24h model-list cache will survive pod restarts. No code change required;
the path is correct. The comment at line 74 referencing "sandbox-home emptyDir" should
be updated to reflect the new storage location.

### 9. README-LLM.md

Update the volume table that describes `sandbox-home` as an emptyDir. Update to reflect
the new PVC subPath layout.

## Migration path for existing workspaces

Existing PVCs have no `home/` subdirectory. On the first pod boot after the controller
is updated:

1. Kubernetes attempts to mount `subPath: home` on the existing PVC
2. If Kubernetes auto-creates the directory (standard behavior), the mount succeeds and
   `/home/sandbox` starts empty (equivalent to current emptyDir behavior for that boot)
3. Subsequent boots find the persisted home directory

There is **no data migration required** and **no compatibility break** — existing PVCs
simply gain a new subdirectory on first mount. PVCs that were `5Gi` will have less
headroom (workspace + home sharing the same 5Gi), which is why the default bump to 10Gi
matters for new workspaces.

Existing 5Gi workspaces: the `/workspace` subPath currently uses ~4.3G on `a3a1c914`.
After migration, those workspaces should be resized or the home dir will quickly fill
the remaining ~700MB. Longhorn supports online PVC expansion — this can be done via
`kubectl patch pvc` without pod restart.

## Separate issue: unbounded gh log cache

Regardless of storage layout, the agent should not accumulate unbounded `gh run view --log`
zips. Short-term mitigation (can be done independently, before this migration):

```bash
GH_NO_UPDATE_NOTIFIER=1
# After each gh run view --log call:
rm -rf ~/.cache/gh/run-log-*.zip
```

Or redirect `XDG_CACHE_HOME` for gh to `/tmp` (already 64Mi tmpfs limit, will naturally
bound the cache).

This should be addressed in the base image or agentd startup, not blocked on the PVC
migration.

## File change summary

| File | Change | Complexity |
|------|--------|-----------|
| `controller/internal/workspace/pod_builder.go` | Remove sandbox-home emptyDir; add SubPath to workspace mounts | Low |
| `controller/internal/workspace/security_test.go` | Update volume footprint assertions | Low |
| `controller/internal/workspace/phase_pending.go` | No change needed (subPath auto-creation verified) | — |
| `cmd/workspace-agentd/secrets.go` | Update comment at line 74 | Trivial |
| `pkg/apis/llmsafespace/v1/workspace_types.go` | Update field description (no schema change) | Trivial |
| `charts/llmsafespace/crds/workspace.yaml` | Update field description | Trivial |
| `pkg/crds/workspace_crd.yaml` | Update field description | Trivial |
| `charts/llmsafespace/values.yaml` | Update comment on maxWorkspaceStorageGi | Trivial |
| `api/internal/…/workspaces.go` (TBD) | Bump default PVC size 5Gi → 10Gi | Low |
| `README-LLM.md` | Update volume table | Trivial |

**No helm pod template changes** — confirmed that all workspace pod construction is in Go.

## Open questions before implementation

1. ~~**Confirm Longhorn auto-creates subPath directories on first mount.**~~ **VERIFIED
   2026-06-09.** Tested live on the cluster against `workspace-05a981df` (Longhorn RWO):
   pod with `subPath: workspace` + `subPath: home` on the same PVC started cleanly, both
   directories auto-created, pod exited `SUCCESS`. No pvc-init container needed.

2. ~~**Confirm duplicate volumeMount names are accepted by the kube API.**~~ **VERIFIED
   2026-06-09.** Test pod with two `workspace` volumeMount entries (different `subPath`
   and `mountPath`) admitted and ran successfully. No webhook rejection.

3. ~~**Find exact location in the API where default PVC size is set.**~~ **RESOLVED.**
   The `"5Gi"` default was hardcoded in `frontend/src/api/workspaces.ts:45` — the
   `instanceSettings` fallback in `workspace_service.go:166` existed but was never
   exercised (no DB row seeded). Fix: migration `000018_default_storage_size` seeds
   `workspace.defaultStorageSize = "10Gi"` into `instance_settings`; frontend `create()`
   no longer sends `storageSize`. API-direct and SDK callers now get the server default.

4. **Decide on resize strategy for existing 5Gi PVCs.** Existing workspaces at 5Gi will
   have less headroom once `/home/sandbox` moves onto the PVC. `a3a1c914` is already at
   89% (4.3G/4.9G). Recommendation: manual `kubectl patch pvc` for workspaces above 70%
   usage. Longhorn supports online resize without pod restart. No automated resize
   implemented in this PR — out of scope.
