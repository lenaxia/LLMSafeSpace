# Worklog 0344 — API RBAC for InferenceRelay ClusterRole

**Date:** 2026-06-17 (revised 2026-06-18)
**Session:** Fix deployment 403 when the API server reconciles the `relay-fleet` InferenceRelay CR
**Status:** Complete

---

## Objective

Fix the deployment error where the API server could not create or read InferenceRelay CRs because `InferenceRelay` is cluster-scoped but the API service account only held a namespace-scoped Role.

---

## Root cause

The InferenceRelay CRD is declared `scope=Cluster` (`+kubebuilder:resource:scope=Cluster` at `pkg/apis/llmsafespace/v1/inferencerelay_types.go:202`). The controller had a ClusterRole granting access to `inferencerelays`, but the API service account — which creates/updates the `relay-fleet` CR via the admin handler (`api/internal/handlers/relay_admin.go`) — only had a namespace-scoped Role for `workspaces`. Result: `forbidden: cannot get resource "inferencerelays" at the cluster scope`.

---

## Work Completed

### Helm chart
- `charts/llmsafespace/templates/rbac.yaml`: added a `ClusterRole` + `ClusterRoleBinding` (gated on `controller.inferenceRelay.enabled`) granting the API service account **least-privilege** access — `get`, `list`, `create`, `update` on `inferencerelays` only.

### Chart tests
- `TestRelay_APIInferenceRelayClusterRole_DisabledByDefault` — asserts neither the ClusterRole nor its Binding renders when the relay subsystem is disabled (default), guarding the `{{- if }}` gate on both documents.
- `TestRelay_APIInferenceRelayClusterRole_RendersWhenEnabled` — asserts the ClusterRole grants exactly `[get, list, create, update]` (no `watch`/`patch`/`delete`, no `/status` or `/finalizers` subresources), and that the Binding is fully wired (`roleRef.name` → the rendered ClusterRole; one `ServiceAccount` subject in the release namespace).

---

## Key Decisions

1. **Least-privilege grant instead of mirroring the controller's full-CRUD grant.** Reviewers (rounds 1–2) flagged that the initial grant mirrored the controller's `[get, list, watch, create, update, patch, delete]` plus the `/status` and `/finalizers` subresources, but the API handler only needs `get/list/create/update` on the main resource. Validated by grepping every `InferenceRelays()` call site in `api/` — all usage is in `relay_admin.go` (`List`/`Get`/`Create`/`Update` only; no `Patch`/`Delete`/`Watch`/`UpdateStatus` anywhere in `api/`, `controller/`, or `pkg/`). The grant is trimmed to the verified verbs/resources so the Security dimension (least-privilege by default) is satisfied by construction.

2. **Resolved a pre-existing `0342` collision on `origin/main` (per Rule 5).** PR #235 (which fixed the earlier `0340`/`0341` collisions) renamed `post-merge-hardening` to `0342`, colliding with PR #234's `repolint-merge-queue-autofix` worklog already at `0342`. Because CI's `repolint` checks the merge tree (branch + main), this duplicate would fail the check on this PR regardless of our own worklog. Resolution: renumbered the lexically-later newcomer `repolint-merge-queue-autofix` `0342` → `0345` via the blessed `repolint -fix-worklogs` (the file's single basename self-reference was updated automatically). This PR's own worklog is `0344`, the next contiguous slot.

---

## Assumptions Stated and Validated

| Assumption | Validation |
|------------|------------|
| `InferenceRelay` is cluster-scoped | `pkg/apis/llmsafespace/v1/inferencerelay_types.go:202` — `+kubebuilder:resource:scope=Cluster` |
| API handler needs only `get/list/create/update` on `inferencerelays` (no status/finalizers) | `grep -rnE "InferenceRelays\(\)" api/ controller/ pkg/` — all call sites in `relay_admin.go`; no `Patch`/`Delete`/`Watch`/`UpdateStatus` |
| `controller.inferenceRelay.enabled` defaults to `false` | `charts/llmsafespace/values.yaml` |
| 0344 / 0345 are the correct numbers post-rebase | `repolint`: main carried a `0342` duplicate; after renumbering `repolint-merge-queue-autofix`→0345 and placing this worklog at 0344, `repolint` reports all checks pass (sequence gap-free 0097..0345, no duplicates, no mainline collisions) |

---

## Blockers

None.

---

## Tests Run

- `go test -run 'TestRelay_APIInferenceRelayClusterRole' -v -count=1 ./charts/llmsafespace/` with `helm v3.16.3` on PATH — both new tests PASS (disabled-by-default gate on both documents; enabled renders exactly `[get,list,create,update]`, no subresources, fully-wired binding).
- `go test -timeout 120s -count=1 ./charts/llmsafespace/` — full chart suite PASS (no regression to the G5 cluster/namespaced-scope invariants).
- `repolint` — all checks pass (migrations, worklogs sequence with max 0345, no mainline collisions, chart-migrations drift, CRD drift ×8).
- `go vet ./charts/llmsafespace/` — clean.

---

## Next Steps

1. Merge once the automated reviewer posts APPROVE and CI is green.

---

## Files Modified

- `charts/llmsafespace/templates/rbac.yaml` — added least-privilege ClusterRole + ClusterRoleBinding for the API SA on `inferencerelays`.
- `charts/llmsafespace/chart_test.go` — two tests covering the gate, exact verb set, and binding wiring.
- `worklogs/0344_2026-06-17_api-rbac-inferencerelay-clusterrole.md` — this worklog.
- `worklogs/0345_2026-06-18_repolint-merge-queue-autofix.md` — renamed from `0342_…` to clear the pre-existing main `0342` collision (mechanical; only its basename self-reference was updated).
