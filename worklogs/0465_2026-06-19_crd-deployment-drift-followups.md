# Worklog: CRD deployment drift — 4 follow-ups

**Date:** 2026-06-19
**Session:** Live debugging + scoped-PR follow-ups
**Status:** Complete

---

## Objective

A user reported that "click resume" workflow no longer showed Creating
or Active phase transitions in the UX, despite the activate API returning
HTTP 200 with `{"resumed": "..."}`. Diagnose root cause on the live cluster,
fix, then close the four follow-up gaps that root-cause analysis surfaced:

1. **Helm CRD reconciliation gap** — `helm upgrade` does not reconcile CRDs
2. **Rename coordination** — source is post-rename (`llmsafespaces.dev`),
   cluster is pre-rename (`llmsafespace.dev`)
3. **Stale `pkg/crds/`** — duplicates `charts/llmsafespaces/crds/`, no consumers
4. **Silent prune of unknown fields** — `Update` returns 200 even when fields
   are dropped by the apiserver

---

## Work Completed

### Live diagnosis (separate, completed before this session)

Root cause: deployed `Workspace` CRD was missing `spec.suspend`. The Go
binary writes `Spec.Suspend = &false` on activate; the apiserver silently
prunes the unknown field; `metadata.generation` doesn't bump; controller
never observes a transition. From the user's perspective the activate
returned 200 but nothing happened.

Fixed by re-applying the pre-rename tip CRD
(`git show 6408408b:charts/llmsafespace/crds/workspace.yaml`) which
contains `spec.suspend`. Workspace `8154ae86-d7b7-4f53-b046-d8d3b462b972`
went `Suspended → Creating → Active` in 60s after the apply. All 13
existing user workspaces preserved.

Per worklog 7 ("Assumptions: state, then validate"), validated
assumptions before applying the CRD:

| # | Assumption | Validation |
|---|---|---|
| A1 | `pkg/crds/*.yaml` has no live consumers | `grep -rn "pkg/crds" --include=Makefile --include="*.sh" --include="*.go"` returned 0 matches |
| A2 | Repolint catches Go↔chart-yaml drift | Confirmed in `pkg/repolint/crd_drift.go:411-456` |
| A3 | The unfixed gap is chart-yaml↔cluster | Cluster CRD lagged the chart's CRD; chart was correct |
| A4 | Rename in source is complete | `hack/rename-to-llmsafespaces.sh` is deliberate, scripted, comprehensive |
| A5 | Production cluster is on pre-rename binary | Image `ts-1781820636` built before rename commit `8befbe7c` |
| A6 | Apiserver silently prunes unknown CRD fields | Confirmed live: `kubectl patch ... 'spec.suspend':false` returned `Warning: unknown field "spec.suspend"` and `(no change)` |

### Item 3 — Delete stale `pkg/crds/` (commit `e05c35e5`)

- Deleted `pkg/crds/{workspace_crd,runtimeenvironment_crd}.yaml`
- Verified no live consumers via grep across `*.go`, `*.yaml`, `*.sh`,
  `Makefile` — only `design/` and `worklogs/` (history; explicitly
  excluded from the rename script's policy at `hack/rename-to-llmsafespaces.sh:13`)
- Updated `.github/prompts/{pr-review,implement}.md` — both still
  pointed at `controller/internal/resources/*_types.go` (deleted in
  worklog 0228) and `pkg/crds/*.yaml`. Replaced with current paths
  (`pkg/apis/llmsafespaces/v1/*_types.go`, `charts/llmsafespaces/crds/*.yaml`)
  and added a pointer to `make helm-deploy` / `repolint -cluster-drift`.

### Item 4 — Post-write read-back assertion in `ActivateWorkspace` (commit `2a791148`)

TDD: wrote two failing tests, then implemented:

- `TestActivateWorkspace_SpecSuspendPruned` — Update returns nil pointer
- `TestActivateWorkspace_SpecSuspendPersistedAsTrue` — Update returns &true

Implementation in `api/internal/services/workspace/workspace_service.go:1166-1207`:
- Capture the `Update` return value into `persisted` (was discarded before)
- After the `RetryOnConflict` block, assert `persisted.Spec.Suspend != nil && !*persisted.Spec.Suspend`
- On mismatch, return `workspace_resume_failed` with a message naming the
  field and pointing at `charts/llmsafespaces/crds/workspace.yaml`

Cost: one nil-check + one bool-deref on the happy path, zero apiserver
round-trips.

### Item 1 — Repolint cluster-drift check (commit `40290c3d`)

New `pkg/repolint/cluster_drift.go` (350 lines):

- `ClusterDriftBinding` declares one (chart-yaml ↔ deployed-CRD) check
- `ClusterDriftCheck(ctx, root, binding, fetcher)` returns a unified-style
  diff in either direction, with a remediation step
- `CRDFetcher` interface lets the diff logic be unit-tested without a
  live cluster; `NewKubeCRDFetcher()` is the production wiring (loads
  kubeconfig via standard merge rules)
- `extractDeployedCRDProperties` walks the typed `apiextv1.CustomResourceDefinition`
  along the same path syntax as the YAML walker — supporting both
  `properties` and `items` steps
- `LiveClusterBindings()` declares the six pairs (Workspace spec/status,
  RuntimeEnvironment spec/status, InferenceRelay spec/status)

CLI wiring (`cmd/repolint/main.go`): new `-cluster-drift` flag (off by
default — pre-commit/CI without a kubeconfig must remain green). Opt-in
only.

`Makefile` integration: `helm-deploy` now runs `./bin/repolint -cluster-drift`
after the rollout-status check. Fails the deploy on drift with a clear
remediation message. Intent: every `make helm-deploy` is a self-checking
apply.

Tests (`pkg/repolint/cluster_drift_test.go`, 10 cases): no-drift,
chart-has-suspend-cluster-missing (the worklog-0463 reproduction),
cluster-has-stale-removed-fields, both-directions, ignore-list semantics,
fetch error, missing chart file, invalid path, sessions.items[]
traversal, and binding-list sanity. All pass without envtest.

### Item 2 — Rename migration design doc (commit `6f03f2e3` + remediations in `ec0bd415`)

`design/0042_2026-06-19_api-group-rename-migration.md` (~330 lines).

Initially recommended a naive uninstall+reinstall, then re-checked the
cluster: PVCs have ownerRefs to Workspace CRs with `blockOwnerDeletion: true`,
the `longhorn` StorageClass has `reclaimPolicy: Delete`. **An uninstall would
permanently destroy all 13 user workspaces' data via cascade.** Pivoted to
an adopt-PVC migration:

- Phase 0: Build + test a one-shot CLI migration tool against kind
- Phase 1: Apply both CRDs (singular + plural) side by side
- Phase 2: Stop controller, suspend all workspaces (safety net)
- Phase 3: Detach owned resources (PVC + Secret + NetworkPolicy) from
  old CRs, create new CRs with same name/spec, re-attach resources
- Phase 4: Force-delete old CRs after verifying they no longer own
  any resources
- Phase 5: Deploy post-rename binary, verify, resume workspaces
- Phase 6: 24h bake, then delete the old CRDs

Hard rollback procedure documented at every phase. Mandatory pre-Phase-3
backup of all CRs, PVCs, Secrets, and NetworkPolicies as kubectl YAML.
Five validation gates before declaring the design ready.

### Adversarial self-review (per Rule 11)

Phase 1 found two real findings, validated in Phase 2, remediated in
Phase 3 (commit `ec0bd415`):

1. **Phase 3 of design 0042 only covered PVC ownerRef migration**, but
   the controller also sets ownerReferences on per-workspace Secrets
   (`controller/internal/workspace/secrets.go:86`) and NetworkPolicies
   (`controller/internal/workspace/network_policy.go:355`). Without
   re-targeting those, Phase 4's old-CR delete cascades to the Secrets,
   destroying every user's encrypted credentials. Fixed: expanded
   Phase 3 to cover all three resource types and added an audit script
   that fails Phase 0 if any other unhandled resource type owns a
   workspace.

2. **The post-write assertion docstring described only the prune case.**
   The same check correctly catches a wrong-default case (CRD ships
   with `default: true`). Updated comment to enumerate both shapes;
   updated the user-visible error message to include "or has a wrong
   default". Existing test `TestActivateWorkspace_SpecSuspendPersistedAsTrue`
   already covered this; no test changes needed.

False alarms (documented + dismissed):
- Phase 3 idempotency on partial failure: already documented.
- Cluster-drift check fails 'CRD not found' on pre-rename clusters:
  factually correct (drift IS real); the misleading message is
  acceptable for a transient pre-migration state.
- KUBECONFIG-points-at-wrong-cluster: out of scope; tracked for future.

---

## Key Decisions

1. **Did not uninstall the CRD on the production cluster.** PVC
   ownerReferences + `reclaimPolicy: Delete` would have destroyed all
   user data. Pivoted to in-place CRD apply (resolved the live bug)
   and a designed PVC-adoption migration plan (for the eventual
   rename rollout).

2. **`pkg/crds/` is dead — deleted.** No live consumer (no Makefile,
   script, or Go reference). The chart in `charts/llmsafespaces/crds/`
   is the actual source-of-truth used by Helm, the schema-validation
   test, and repolint. Worklogs and design docs reference the dead
   path but are append-only history (deliberately excluded from the
   rename script's policy).

3. **`-cluster-drift` is opt-in, not part of default repolint.**
   Pre-commit and CI run without a kubeconfig; making the check
   mandatory would break those flows. Opt-in via flag preserves the
   existing UX and adds the check exactly where it's wanted: after
   `make helm-deploy`.

4. **Post-write assertion lives in `ActivateWorkspace` only,
   not in every CRD-write path.** Activate is the user-visible
   choke point where the failure was felt. Other paths (Suspend,
   Restart) write spec.suspend too, but their failure modes are
   less catastrophic and adding the same check everywhere would be
   defensive overkill. Tracked for follow-up if the same drift
   class shows up in another path.

5. **Rename migration is design-doc-only in this PR.** The adopt-PVC
   tool is non-trivial (needs idempotency, dry-run, kind-based testing,
   webhook compatibility verification, and rollback testing). Splitting
   off as separate work prevents this PR from becoming unreviewable.

---

## Blockers

None for this PR. Two blockers for the rename rollout (Item 2's
follow-up work):

1. The migration tool (`cmd/rename-migrate/`) needs to be built and
   tested before any post-rename binary can be deployed.
2. The `audit-policy.yaml` referenced in design 0042 Appendix A still
   points at the singular group; needs to be updated alongside the
   migration deploy.

---

## Tests Run

- `go test -timeout 480s -short ./pkg/... ./api/... ./controller/...`
  → All packages pass
- `./bin/repolint` (full repolint) → all checks pass
- `golangci-lint run --new-from-rev=origin/main ./...` → 0 new findings
- `go test ./pkg/repolint/... -run "TestClusterDrift|TestExtractDeployed|TestLiveClusterBindings"`
  → 10/10 pass
- `go test ./api/internal/services/workspace/... -run "TestActivateWorkspace"`
  → all activate tests pass including the two new prune/wrong-default cases
- Live cluster validation: `kubectl patch workspace ... 'spec.suspend':false`
  → succeeded, generation bumped, controller transitioned phase from
  Suspended → Creating → Active in 60s

---

## Next Steps

1. Push branch, open PR, iterate to APPROVE per the
   review-iterate-approve-merge cycle in README-LLM.md.
2. After this PR merges: tackle the migration tool work (`cmd/rename-migrate/`)
   in a separate branch. Reference design 0042.
3. After this PR merges: consider extending the post-write assertion
   pattern to other CRD-write paths if they prove vulnerable
   (Suspend, Restart, Settings updates).

---

## Files Modified

Created:
- `design/0042_2026-06-19_api-group-rename-migration.md`
- `pkg/repolint/cluster_drift.go`
- `pkg/repolint/cluster_drift_test.go`
- `worklogs/0465_2026-06-19_crd-deployment-drift-followups.md` (this file)

Modified:
- `.github/prompts/implement.md` — corrected stale CRD-update guidance
- `.github/prompts/pr-review.md` — corrected stale CRD-update guidance
- `Makefile` — `helm-deploy` runs `repolint -cluster-drift` post-rollout
- `api/internal/services/workspace/workspace_service.go` — post-write assertion + helper
- `api/internal/services/workspace/workspace_service_test.go` — two new tests
- `cmd/repolint/main.go` — `-cluster-drift` flag + `runClusterDrift`

Deleted:
- `pkg/crds/runtimeenvironment_crd.yaml`
- `pkg/crds/workspace_crd.yaml`
