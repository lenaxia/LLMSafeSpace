# Design: API group rename — `llmsafespace.dev` → `llmsafespaces.dev`

**Date:** 2026-06-19
**Status:** Proposed
**Authors:** mikekao
**Worklog reference:** 0397 (the deploy-blocker incident that surfaced this)

---

## Context

Commit `8befbe7c` (2026-06-18) executed a complete source-tree rename from
`llmsafespace.dev` (singular) to `llmsafespaces.dev` (plural). The commit
message states "Project is not live" — meaning the rename was treated as a
clean break with no rollout coordination. That assertion was incorrect: the
repository owner runs a personal production cluster against the pre-rename
binary (image `ts-1781820636`, built before the rename) with 13 active user
workspaces, real persistent volumes, and ongoing user data on those volumes.

The current state on the cluster:

- API group: `llmsafespace.dev` (singular)
- 13 `Workspace` CRs in that group, with finalizers `workspace.llmsafespace.dev/finalizer`
- 13 PVCs owned by those CRs via `ownerReferences[].apiVersion: llmsafespace.dev/v1`,
  `blockOwnerDeletion: true`, `controller: true`
- `longhorn` StorageClass `reclaimPolicy: Delete` — when a PVC is deleted, the
  underlying Longhorn volume is reclaimed (data destroyed)

The current state in source (post-rename):

- API group: `llmsafespaces.dev` (plural)
- All Go code (`pkg/apis/llmsafespaces/v1/...`), all chart files
  (`charts/llmsafespaces/...`), all webhooks, all controllers reference the
  plural group
- Helm release on the cluster is named `llmsafespace` (singular chart name);
  that's a release name, not a chart-content concern, but it's a hint that
  the chart was renamed too

The next deploy of any post-rename image (any image with a build timestamp
> `8befbe7c`) will:

1. Try to register a new CRD group `llmsafespaces.dev` alongside the existing
   `llmsafespace.dev`
2. The controller binary will only watch `llmsafespaces.dev` — the existing
   13 workspaces in `llmsafespace.dev` will be invisible to it
3. Existing pods for those workspaces will keep running (they have no live
   tie to the controller after boot)
4. But:
   - The API will not list those workspaces (it queries `llmsafespaces.dev`)
   - Suspend, resume, restart, terminate, status — all broken for existing
     workspaces
   - Users who try to use existing workspaces see "workspace not found" errors

A naive fix — uninstall the old CRD and let the new one take over — destroys
all user data:

1. CRD deletion cascades to all CRs in that group (Kubernetes garbage collection
   semantics)
2. CR deletion blocks on the controller-managed finalizer; once the controller
   is gone or the new controller doesn't recognise the old group, the finalizer
   eventually clears via force-delete or escalation
3. Cascade then propagates to PVCs (`ownerReferences[].blockOwnerDeletion: true`)
4. PVC deletion + `pvc-protection` finalizer waits for no pods to use the
   volume, which is true once the workspace pod is gone
5. PVC delete triggers PV delete (`reclaimPolicy: Delete`)
6. PV delete tells Longhorn to delete the backing volume
7. **All 13 workspaces' user data is permanently lost**

This document describes a migration plan that preserves PVC data through
the rename.

---

## Goals

1. Preserve all 13 existing workspaces' PVC data through the rename
2. Preserve workspace UIDs/names where possible so user-facing references
   (URLs, session bookmarks, API keys) continue to work
3. Idempotent: if the migration is interrupted partway, re-running it is
   safe and converges
4. Reversible at every step until a final cutover commit
5. No downtime longer than a single deploy window (~5 minutes)

## Non-goals

- Migrating the data inside the PVCs (it's user files; agnostic to the
  rename)
- Renaming the underlying Longhorn volume IDs (the PV's `volumeHandle`
  is opaque to the K8s layer)
- Re-establishing CRDs in a way that keeps both groups alive long-term
  (the singular group must go away eventually)

---

## Design

The core insight: PVCs are not the problem. PVCs survive any operation
that doesn't delete them. The problem is the `ownerReferences` chain
that ties PVCs to CRs in the old group. Break that chain before the
old CRs go away, then re-establish it under the new group.

### Phase 0: prerequisites (ship + bake)

1. Tag a release of the CURRENT (post-rename) source tree as
   `pre-migration-baseline`. We're deploying this image but not letting
   it touch the cluster yet — it goes to a staging registry path so we
   can confirm the binary's behaviour against a test cluster.
2. Build a one-shot CLI tool `cmd/rename-migrate/` that takes a kubeconfig
   pointing at the cluster and:
   - Lists all `llmsafespace.dev/v1` Workspace CRs
   - For each, builds an equivalent `llmsafespaces.dev/v1` Workspace CR
     with the same name, namespace, spec, status, and metadata.uid (where
     UID preservation is possible — see Phase 3)
   - Lists every PVC with an ownerRef to a singular-group Workspace and
     verifies it can be re-targeted to the plural-group CR
   - Outputs a dry-run plan (no writes)
3. Write the tool with a strict dry-run-by-default pattern. `--apply` is
   the only flag that enables writes. Print every write before doing it,
   with confirmation prompts on first-of-N writes.
4. Test the tool against a kind cluster seeded with 13 fake workspaces and
   their PVCs. Verify the cluster ends in the expected state for each of
   six migration phases.

### Phase 1: pre-deploy — apply both CRDs

1. Apply the new CRD (`workspaces.llmsafespaces.dev`) alongside the
   existing one (`workspaces.llmsafespace.dev`). They're separate API
   resources from K8s' perspective, so this is non-destructive.
2. Apply the same for `runtimeenvironments` and `inferencerelays`.
3. Verify both CRDs are Established and Accepted in the cluster.
4. **Rollback at this point:** delete the new CRD. Old workspaces unaffected.

### Phase 2: stop the controller

1. Scale `llmsafespace-controller` to 0 replicas. The 13 existing pods
   keep running (already-bound). New pods cannot be created and existing
   pods cannot be reconciled, but no user data is at risk during this
   window.
2. Suspend all 13 workspaces using the OLD API surface
   (`spec.suspend: true` on each `llmsafespace.dev/v1` Workspace). This
   is a workspace-level safety net: if the migration fails partway, users
   can manually re-resume after we restore the old controller.
3. Wait for all pods to terminate. PVCs remain bound; Longhorn volumes
   intact.

### Phase 3: detach owned resources from old CRs, re-attach to new CRs

The controller sets `ownerReferences` on three kinds of per-workspace
resources (`controller/internal/workspace/{pvc,secrets,network_policy}.go`):

- The PVC at `/workspace`, `/home/sandbox`, and `/tmp` (all the same PVC
  via subPaths)
- The credential `Secret` (encrypted user credentials — destroying this
  forces every user to re-enter every credential after the migration)
- The per-workspace `NetworkPolicy`

All three must be re-targeted before the old CR is deleted; otherwise
the cascade kills them too. The Pod also has an ownerRef but is
short-lived (already terminated in Phase 2) and is regenerated by the
new controller, so it needs no migration.

For each Workspace UUID:

1. **Read** ownerReferences on the PVC, the credential Secret
   (`workspace-<uuid>-credentials`), and the NetworkPolicy
   (`workspace-<uuid>`).
2. **Patch each** to remove the singular-group ownerReference. Resources
   are now ownerless; `pvc-protection` finalizer keeps the PVC alive,
   and Secrets/NetworkPolicies have no protection finalizer but won't
   be GC'd as long as no one deletes them.
3. **Create** the corresponding plural-group Workspace CR with:
   - Same `metadata.name` (UUID, e.g. `8154ae86-d7b7-4f53-b046-d8d3b462b972`)
   - Same `metadata.namespace`
   - Same `spec`, **including `spec.suspend: true`** (set in Phase 2 —
     keeps the new controller from auto-resuming workspaces during the
     migration window)
   - Same `metadata.annotations` (preserves `name`, `created-by`, `requested-at`)
   - **Empty status** — the new controller will rebuild status from
     observation. We do NOT copy phase across (the workspace is in
     "stopped" state from Phase 2).
   - **No** `metadata.uid` field set — the apiserver assigns a new UID.
     Cross-group UID preservation is not supported by K8s.
4. **Patch each** of the three resources to add an ownerReference
   pointing at the new plural-group CR (using its newly-assigned UID).
5. **Verify** each resource's `metadata.ownerReferences[0]` matches the
   new CR exactly.

This phase is idempotent: running it twice on the same workspace is a
no-op because (a) the new CR already exists, (b) each resource already
has the new ownerRef, (c) the patch is a strategic merge that converges.

**Resources NOT migrated:**

- Pod: terminated in Phase 2, recreated by new controller from spec
- StatefulSet/Deployment/Service for the workspace: none exist (the
  workspace is just a single pod)
- HPA, PDB, ServiceMonitor: none configured per-workspace

**Audit script** (run before Phase 3 starts, must return zero):

```bash
# Find any non-pod-non-PVC-non-Secret-non-NetworkPolicy resource owned by an
# old-group Workspace. If anything else exists, the migration plan needs
# extending before we proceed.
kubectl get all,secret,configmap,networkpolicy,pdb,hpa,servicemonitor -A -o json | \
  jq '.items[] | select(.metadata.ownerReferences != null) |
      select(.metadata.ownerReferences[].apiVersion == "llmsafespace.dev/v1") |
      "\(.kind)/\(.metadata.namespace)/\(.metadata.name)"'
```

### Phase 4: delete old CRs

1. For each `llmsafespace.dev/v1` Workspace CR:
   - **Patch** `metadata.finalizers` to `[]` (remove
     `workspace.llmsafespace.dev/finalizer`). The old controller is at 0
     replicas and won't see this; the finalizer is therefore safe to
     clear directly. Skipping this would let the apiserver wait forever
     for the gone-controller to clear it.
   - **Delete** the CR.
2. The cascade *does not* propagate to PVCs, Secrets, or NetworkPolicies
   because we already removed those resources' ownerReferences in Phase 3.
   All three survive.

### Phase 5: deploy the new binary + restart workspaces

1. `make helm-deploy IMAGE_TAG=<post-rename-tag>`. The helm-deploy target
   re-applies CRDs (idempotent — the plural CRDs from Phase 1 stay; the
   chart no longer ships the singular CRDs, so they're not touched —
   they just sit there until Phase 6).
2. Cluster CRD apply step: `kubectl apply -f charts/llmsafespaces/crds/`.
   This is the post-rename chart, so only plural CRDs.
3. New controller binary starts. It watches `llmsafespaces.dev`, finds
   the 13 plural-group CRs we created in Phase 3, all with phase empty
   (the controller initializes them). Each goes Pending → Suspended (the
   controller observes the suspend state we set on the spec... wait, we
   didn't set it on the new CRs. Adjust Phase 3 step 3: copy over
   `spec.suspend: true` so workspaces stay suspended through the deploy).
4. Verify all 13 workspaces are visible in the API and listed in the UI.
5. Resume workspaces one at a time — each resume creates a new pod that
   mounts the existing PVC at `/workspace`. User data is unchanged.

### Phase 6: cleanup — remove the old CRDs

1. Wait 24 hours. Verify no application errors, no missing workspaces,
   no failed reconciliations.
2. `kubectl delete crd workspaces.llmsafespace.dev runtimeenvironments.llmsafespace.dev inferencerelays.llmsafespace.dev`
   — there are no CRs left in those groups (Phase 4 removed them all),
   so deletion is non-destructive.
3. Verify cluster state: `kubectl get crd | grep llmsafespace` should
   show only the plural-group CRDs.

---

## Failure modes and rollback

| Phase | Failure | Recovery |
|---|---|---|
| 0 | Migration tool dry-run reveals impossible state | Pause, investigate, do not deploy |
| 1 | Apply of new CRDs rejected | Fix YAML, retry. No data risk. |
| 2 | Some workspace fails to suspend | Resume the others, escalate manually, do not proceed |
| 3 | Patch of resource ownerReference fails (e.g. apiserver throttling) | Retry; idempotent. If sustained, restore old ownerRef and abort. |
| 3 | Plural-group CR creation rejected by webhook | Webhook bug. Fix webhook, retry. |
| 3 | Audit script (above) finds an unhandled resource type owning a workspace | Stop. Extend Phase 3 to migrate that type before proceeding. |
| 4 | Old CR delete hangs (somehow finalizer reappeared) | `kubectl edit` to manually clear; investigate why. |
| 5 | New controller doesn't see the new CRs | Check informer cache, restart controller, verify CRD established. |
| 5 | Resume creates a new pod but PVC won't bind | Longhorn issue, not migration issue; investigate Longhorn. |
| 6 | Cleanup CRD delete cascades unexpectedly | Should not happen (no CRs left). If it does, investigate immediately. |

**Hard rollback** (Phase 3 and beyond): re-create the singular-group
Workspace CRs from a `kubectl get -o yaml` backup taken before Phase 3,
re-add ownerRefs to the PVCs, Secrets, and NetworkPolicies pointing at
those CRs, restart the old controller. Painful but possible if a
backup exists.

**Mandatory pre-Phase-3 backup:**

```bash
kubectl get workspaces.llmsafespace.dev -A -o yaml > /backup/old-workspaces-$(date -u +%Y%m%dT%H%M%SZ).yaml
kubectl get pvc -n default -o yaml > /backup/pvcs-$(date -u +%Y%m%dT%H%M%SZ).yaml
kubectl get secret -n default -l app=llmsafespace -o yaml > /backup/secrets-$(date -u +%Y%m%dT%H%M%SZ).yaml
kubectl get networkpolicy -n default -o yaml > /backup/networkpolicies-$(date -u +%Y%m%dT%H%M%SZ).yaml
```

These backups are the only thing standing between us and total data
loss if Phase 3 goes wrong. Each must be verified non-empty before
proceeding.

---

## Open questions

1. Is preserving `metadata.uid` strictly impossible? Investigate
   `--feature-gates` on the apiserver. If possible, makes Phase 3 less
   risky for any system that records UIDs.
2. Does any user-facing artefact (API key, OAuth grant, audit log) include
   the old API group string? Audit before deploying. If yes, those need
   migration too.
3. Should the migration tool live in `cmd/rename-migrate/` permanently
   or be deleted after use? Recommend keeping it as a reference for any
   future API-group migration; mark deprecated.
4. Are there any scheduled jobs (Helm test hooks, CronJobs) that
   reference the singular group? Audit `kubectl get cronjobs -A -o yaml | grep llmsafespace`.

## Validation gates before declaring this design ready

- [ ] Run dry-run migration tool against a kind cluster seeded with 13
      fake workspaces; verify the dry-run plan matches expectations
- [ ] Run apply migration tool against the same kind cluster; verify
      end state byte-for-byte against expected
- [ ] Verify hard-rollback procedure on a freshly-migrated kind cluster
      (must produce identical pre-migration state from backups)
- [ ] Webhook unit tests for plural group: confirm validating webhook
      accepts the migrated-style CR (no defaults applied during create)
- [ ] Integration test: end-to-end resume of a migrated workspace creates
      a working pod that can read pre-migration files from `/workspace`

---

## Appendix A: out-of-band cleanup needed

The audit-policy in `design/stories/epic-17-security-review/phase-0/audit-policy.yaml:44`
still references the singular group. Per `hack/rename-to-llmsafespaces.sh`,
`design/` is intentionally excluded from the rename (history-only). When
this migration ships, audit-policy should be updated as part of the same
deploy because audit-policy IS active config (it's loaded by the
apiserver), not history. Currently the apiserver is logging audit events
under `llmsafespace.dev` per that file; after migration, log volume will
drop to zero unless the file is updated.

## Appendix B: things that are NOT changing

- PVC names (`workspace-<uuid>` — same UUID before/after)
- Pod names (regenerated by the controller; users don't reference these)
- Workspace IDs (UUIDs, opaque strings — same before/after)
- Workspace display names (PostgreSQL — separate from K8s, unchanged)
- User credentials (K8s Secrets, separate from CRDs, unchanged)
- Session histories (in `/workspace/.local/opencode/...` on the PVC,
  unchanged because PVC is unchanged)
- API endpoint URLs (the API server's HTTP routes — these are not tied
  to the K8s API group)

---

## Appendix C: why not other approaches

**Why not just re-apply the old singular CRD with the new schema?** That's
what worklog 0397 did as an emergency fix, and it works for keeping the
existing binary running. But it doesn't get us to the new (plural-group)
binary, which is where ongoing development is happening. We need a path
forward, not a stationary fix.

**Why not run both controllers (singular + plural) in parallel during the
transition?** Two controllers managing distinct CRDs is fine. Two
controllers fighting over the same PVCs is not. There's no clean way to
have a PVC owned by one Workspace in one group and simultaneously by
another Workspace in another group. We have to break the ownership at
some point; the question is just whether we do it explicitly (Phase 3)
or accept the chaos (don't).

**Why not use a CRD conversion webhook?** Conversion webhooks convert
between *versions* of the same group, not between groups. They don't
apply here.
