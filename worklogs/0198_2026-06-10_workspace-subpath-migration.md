# Worklog 0198 — Migrate /workspace to PVC subPath: workspace

**Date:** 2026-06-10

## Context

PR #80 (worklog 0197) migrated `/home/sandbox` from a 1Gi emptyDir to
`subPath: home` on the workspace PVC. The `/workspace` mount was intentionally
left at PVC root for backward compatibility with existing data.

This worklog covers the follow-on migration to move `/workspace` to
`subPath: workspace` as well, so the PVC has a clean two-subtree layout:

```
pvc-root/
  workspace/   →  /workspace   (project files, opencode.db, .local/)
  home/        →  /home/sandbox (SSH keys, caches, enricher state)
  lost+found/  →  (not mounted, K8s filesystem artifact, left in place)
```

This is the correct long-term layout. It was deferred from PR #80 due to
migration complexity; the owner has accepted the migration pain.

## Current PVC root layout (surveyed 2026-06-10)

19 workspace PVCs exist. Surveyed representative samples:

| PVC | Contents at root |
|---|---|
| `workspace-05a981df` | `.gotmp/`, `.local/`, json files, `llmsafespace/`, `lost+found/`, `home/` (from PR #80 mount), `workspace/` (from earlier test pod) |
| `workspace-1aa87aec` (active) | `.local/`, `llmsafespace/`, `lost+found/`, `opencode/` |
| `workspace-9b7c0e76` (active) | `.cache/`, `.local/`, `llmsafespace/`, `lost+found/` |
| `workspace-a3a1c914` (active) | `.local/`, `.tmp/`, `llmsafespace/`, `lost+found/` |
| `workspace-1114e5c3` | `.local/`, `lost+found/` |
| `workspace-9532ac87` | `.local/`, `index.html`, `lost+found/` |
| `workspace-c3c8766d` | `.local/`, `lost+found/` |

**Key observations:**
1. Some PVCs already have `home/` and `workspace/` subdirs created by today's test
   pods and PR #80 subPath mounts. These are empty dirs and must be handled by the
   migration script.
2. All user data lives at PVC root — git repos, dotfiles, json files, `.local/`.
3. `lost+found/` is always present — must be excluded from the move.
4. `home/` and `workspace/` if present are empty artifacts — must be excluded from
   the move (they are the destination directories).

## Migration plan

### Phase 1: suspend all active workspaces

Use the API or `kubectl patch` to suspend all Active workspaces. This ensures no
pod is writing to any PVC during the migration. Suspended workspaces have no pod;
the PVC is detached and can be mounted by a migration job.

```bash
# Suspend all active workspaces via API (or kubectl patch phase → Suspending)
kubectl get workspace -n default -o json | \
  jq -r '.items[] | select(.status.phase=="Active") | .metadata.name' | \
  xargs -I{} kubectl patch workspace {} -n default \
    --type=merge -p '{"spec":{"suspended":true}}'
```

Wait for all pods to terminate before proceeding.

### Phase 2: run per-PVC migration job

For each workspace PVC, run a migration pod that:
1. Mounts the PVC root at `/pvc`
2. Creates `/pvc/workspace/` if it doesn't exist
3. Moves all entries at `/pvc/` into `/pvc/workspace/`, **excluding**:
   - `lost+found/` (filesystem artifact, not user data)
   - `workspace/` (destination directory)
   - `home/` (already migrated in PR #80, must stay at PVC root)

Migration script (run once per PVC):
```sh
set -euo pipefail
cd /pvc
mkdir -p workspace

for entry in $(ls -A); do
  case "$entry" in
    lost+found|workspace|home) continue ;;
    *) mv "$entry" workspace/ ;;
  esac
done

echo "Migration complete. workspace/ contents:"
ls -la workspace/
```

This is safe to run multiple times (idempotent: already-moved entries won't
exist at root on re-run).

A single Kubernetes Job manifest can be parameterized by PVC name and run in
sequence for all 19 PVCs. Parallelism is fine since each job touches a
different PVC.

### Phase 3: update controller

In `pod_builder.go`, add `SubPath: "workspace"` to the `/workspace` mount on
both the main container and the `workspace-setup` init container:

```go
// Main container:
{Name: "workspace", MountPath: "/workspace", SubPath: "workspace"},
// ...
{Name: "workspace", MountPath: "/home/sandbox", SubPath: "home"},

// workspace-setup init container:
{Name: "workspace", MountPath: "/workspace", SubPath: "workspace"},
```

Update the security test:
- Assert `/workspace` mount has `SubPath: "workspace"` (change from "must be empty"
  to "must equal workspace")
- Assert `/home/sandbox` mount has `SubPath: "home"` (unchanged)
- Assert workspace-setup init container `/workspace` mount has `SubPath: "workspace"`

### Phase 4: deploy controller update

```bash
make helm-deploy
```

The controller upgrade causes the running pod specs to be regenerated with the
new subPath. Since all workspaces are suspended (no pods), there is nothing to
restart. The new subPath takes effect on the next resume.

### Phase 5: resume workspaces

Resume all workspaces. Each new pod will mount `subPath: workspace` at
`/workspace` — pointing at the migrated data.

```bash
kubectl get workspace -n default -o json | \
  jq -r '.items[] | select(.spec.suspended==true) | .metadata.name' | \
  xargs -I{} kubectl patch workspace {} -n default \
    --type=merge -p '{"spec":{"suspended":false}}'
```

### Phase 6: verify

Spot-check a few workspaces:
- Confirm `/workspace` shows the expected git repos / project files
- Confirm `/home/sandbox` shows the `home/` subtree content (from PR #80)
- Confirm `ls /workspace/lost+found` fails (not moved into workspace/)

## Code changes

| File | Change |
|---|---|
| `controller/internal/workspace/pod_builder.go` | Add `SubPath: "workspace"` to `/workspace` mounts |
| `controller/internal/workspace/security_test.go` | Update assertions: `/workspace` must have `SubPath: "workspace"`, init container same |
| `worklogs/0198_…` | This worklog (migration plan and verification) |
| `README-LLM.md` | Update volume table: `/workspace` → `subPath: workspace` |

**No CRD changes.** The CRD `spec.storage.size` covers the whole PVC regardless
of how it is partitioned internally. No new fields needed.

**No migration files.** The data migration is a one-time operational procedure,
not a DB schema change.

## Rollback plan

If the controller is deployed with `SubPath: "workspace"` before migration
runs, workspaces will appear empty (fresh `workspace/` dir). Rollback:
1. Revert controller to previous image (no subPath on `/workspace`)
2. Workspaces immediately see root-level data again
3. Run migration, redeploy

The migration itself is non-destructive (`mv` not `rm`) so data is never lost.

## Edge cases

1. **PVCs with existing empty `workspace/` dir** (e.g. `workspace-05a981df`):
   Migration script handles this — `workspace/` is in the exclusion list and
   the `mkdir -p workspace` is idempotent.

2. **PVCs with existing data in `workspace/`** (would occur if migration ran
   partially): Detect with `ls workspace/`. If non-empty, the PVC was already
   migrated or a prior run partially succeeded. The script will move remaining
   root-level entries safely since they won't collide (different names).

3. **Active workspaces writing during migration**: Prevented by Phase 1
   (suspend all). The RWO access mode means only one pod can mount the PVC at
   a time, so the migration job and a workspace pod can't both have it mounted.

4. **`lost+found/` content**: Always empty on Longhorn volumes. Not moved.
   Verified in PVC survey — all `lost+found/` entries show `16384` bytes
   (just the directory itself).

5. **`.local/` vs `home/.local/`**: Pre-PR #80 opencode stored
   `XDG_DATA_HOME=/workspace/.local` for auth.json. This `.local/` at PVC root
   is workspace data and must be moved into `workspace/`. It is distinct from
   `/home/sandbox/.local/` (enricher cache, tool configs) which lives in the
   `home/` subtree. The migration script moves `.local/` into `workspace/`
   correctly since it is at PVC root.

## Open questions

None — all design decisions resolved. Ready to implement.
