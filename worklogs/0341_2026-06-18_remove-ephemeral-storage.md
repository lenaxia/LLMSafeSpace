# Worklog: Remove ephemeral-storage resource from workspace pods

**Date:** 2026-06-18
**Session:** Investigated `webhooks.maxWorkspaceEphemeralStorageGi`; concluded the field, the cap, and the entire pod-level ephemeral-storage knob were dead weight; removed across the stack.
**Status:** Complete (PR #230)

---

## Trigger

User asked, in the context of a broader admin-settings-vs-Helm-config discussion, where `maxWorkspaceEphemeralStorageGi` was actually used and what it impacted. Tracing the field exposed that it was protecting against a threat the architecture had already mitigated.

## Investigation

### What the flag did

`controller/internal/webhooks/workspace_webhook.go:471-481` — the validating webhook compared `Workspace.spec.resources.ephemeralStorage` against the `MaxEphemeralStorageGi` cap and rejected creates above it. That was the only enforcement point.

### What `spec.resources.ephemeralStorage` actually controlled

`pod_builder.go:265-266,310,315` — the controller read the field and set both `Pod.Containers[].Resources.Requests.ephemeral-storage` and `Pod.Containers[].Resources.Limits.ephemeral-storage`. Kubelet honors these as scheduling input (request) and eviction trigger (limit) on the **node's local ephemeral filesystem** (`/var/log/pods/`, container overlay FS, non-memory emptyDirs).

### What actually consumes ephemeral storage on a workspace pod

Traced every writable path:

| Path | Backing | Counts toward ephemeral? |
|---|---|---|
| `/` (container root) | overlay FS | No — `readOnlyRootFilesystem: true` (verified `pod_builder.go:116`, asserted in `security_test.go:79-81`) |
| `/workspace`, `/home/sandbox`, `/tmp` | PVC subPaths | No — PVC-backed |
| `/sandbox-cfg` | emptyDir `Medium: Memory` (verified `pod_builder.go:151-154`) | No — counts toward memory |
| Container stdout/stderr | kubelet → `/var/log/pods/` | **Yes** |

Container logs are the sole consumer. Kubelet's own log rotation (`--container-log-max-size=10Mi × --container-log-max-files=5` = ~50 MiB per pod) already bounds them. The per-pod ephemeral-storage limit added zero protection beyond that.

### Threat model honesty check

The flag could only be triggered by an operator applying a `Workspace` CRD via `kubectl apply` with a custom `spec.resources.ephemeralStorage` (the API's `CreateWorkspaceRequest` doesn't expose the field — `pkg/types/types.go:419` and `workspace_service.go:875` only set CPU+Memory). Worst case absent the cap: the pod sits `Pending` because no node has 999 GiB free ephemeral. Annoying, not a DoS.

The flag was added in worklog `0109` (Epic 17 G4/F1.2.3) under the assumption that pods could write unbounded data to overlay FS. That assumption was retired by the `readOnlyRootFilesystem: true` + PVC-only-writes architecture before the cap shipped.

### Considered alternatives

1. **A — Delete just the cap, keep the field.** Saves 6 lines, leaves a dead field on the CRD.
2. **B — Delete the field, keep `requests.ephemeral-storage: 1Gi` on pods.** Preserves a weak bin-packing input.
3. **C — Delete the field AND the pod resource entries.** Cleanest. User correctly pushed back that bin-packing on ephemeral-storage at 1 GiB is meaningless when CPU/memory dominate placement.

Picked C.

## Changes

15 files modified, 56 insertions, 120 deletions:

| Layer | File | Change |
|---|---|---|
| Go types | `pkg/apis/llmsafespace/v1/workspace_types.go` | Removed `EphemeralStorage` from `ResourceRequirements` |
| Go types | `pkg/types/types.go` | Removed `EphemeralStorage` from request type and unreferenced `EphemeralStorageUsage` from status |
| CRD schema | `pkg/crds/workspace_crd.yaml`, `charts/llmsafespace/crds/workspace.yaml` | Dropped `ephemeralStorage` schema entry |
| Pod builder | `controller/internal/workspace/pod_builder.go` | Removed all ephemeral-storage handling from `resourceRequirementsFor`; updated docstring to explain why |
| Webhook | `controller/internal/webhooks/workspace_webhook.go` | Removed `MaxEphemeralStorageGi` validator field, validation branch, unused `parseStorageGi` helper |
| Controller | `controller/main.go` | Removed `--max-workspace-ephemeral-storage-gi` flag |
| Helm | `charts/llmsafespace/values.yaml`, `templates/controller-deployment.yaml` | Removed `maxWorkspaceEphemeralStorageGi` value and CLI passthrough |
| Tests | 5 test files | Updated assertions to verify ephemeral-storage is **absent** from pod spec (regression guard against reintroduction) |
| Docs | `README-LLM.md` | Rewrote "Ephemeral storage" section to document the removal and the forward-looking condition under which the limit should return |

## Operational impact on next deploy

Documented in PR body. Key points:

- Existing workspaces continue running unchanged until next pod recreation.
- Existing Workspace CRDs with `spec.resources.ephemeralStorage` populated: typed unmarshal silently drops the field on next reconcile. After CRD schema upgrade, **apiserver prunes** the field rather than rejecting (CRDs do not have `additionalProperties: false` set on `resources`, so v1 structural-schema pruning applies, not strict rejection). PR body initially said "rejected" — corrected mid-review.
- `webhooks.maxWorkspaceEphemeralStorageGi` in custom Helm values: warning about unused key, no failure.

## Forward-looking note

If a future feature introduces a node-disk-backed `emptyDir` (`Medium: ""`) or otherwise opens a meaningful ephemeral-storage write surface, per-pod ephemeral limits should come back, scoped to that actual concern. The README-LLM.md rewrite documents this trigger condition.

## Process notes

- Hit a pre-existing `0338` worklog collision on `main` (PRs #225 and #226 both landed `0338_*` files; repolint rejects every new PR until one is renamed). The post-rewrite git hook auto-fixed it on rebase but I initially reverted that as out-of-scope. CI then failed, confirming it was a real blocker. Bundled the rename as a separate `chore:` commit on the same PR.
- AI reviewer (`/review`) caught a PR-body inaccuracy: I'd written "apiserver will reject" YAML containing `spec.resources.ephemeralStorage` after the schema is applied, when CRDs default to **pruning** (silently stripping unknown fields) under v1 structural schemas, not rejection. Important distinction for release notes — operators don't need to pre-clean their YAML. Corrected.
- AI reviewer also flagged the missing worklog (this file). README-LLM.md treats worklogs as institutional memory; the rationale ("the cap defended against a mitigated threat") is exactly what belongs preserved.

## Files

- PR: https://github.com/lenaxia/LLMSafeSpace/pull/230
- Branch: `chore/remove-ephemeral-storage`
- Commits: `1e4dfaee` (the change), `930cf914` (worklog collision fix)
