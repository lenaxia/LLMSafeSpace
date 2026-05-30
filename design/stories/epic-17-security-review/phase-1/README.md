# Phase 1 — Reconnaissance & Attack Surface Mapping

**Status:** Complete
**Cluster:** `admin@home-kubernetes` (LLMSafeSpace `sha-cdf2ddc` / chart rev 68 — pre-G16)
**Method:** static analysis + live cluster probing within the blast-radius rules in [`../phase-0-prod/README.md`](../phase-0-prod/README.md).

---

## Deliverables

| RT-x.y | Title | File |
|---|---|---|
| RT-1.1 | API endpoint enumeration | [`RT-1.1-api-endpoint-inventory.md`](./RT-1.1-api-endpoint-inventory.md) |
| RT-1.2 | CRD schema analysis | [`RT-1.2-crd-schema-analysis.md`](./RT-1.2-crd-schema-analysis.md) |
| RT-1.3 | RBAC privilege mapping | [`RT-1.3-rbac-privilege-map.md`](./RT-1.3-rbac-privilege-map.md) |
| RT-1.4 | Network topology + NetworkPolicy effective rules | [`RT-1.4-network-topology.md`](./RT-1.4-network-topology.md) |
| RT-1.5 + RT-1.6 | Dependency audit + container image analysis | [`RT-1.5-RT-1.6-deps-and-images.md`](./RT-1.5-RT-1.6-deps-and-images.md) |
| RT-1.7 | Secret storage map | [`RT-1.7-secret-storage-map.md`](./RT-1.7-secret-storage-map.md) |
| RT-1.8 | Frontend bundle inventory | [`RT-1.8-frontend-bundle.md`](./RT-1.8-frontend-bundle.md) |
| RT-1.9 | Build-time supply chain | [`RT-1.9-build-time-supply-chain.md`](./RT-1.9-build-time-supply-chain.md) |
| Mandatory | Attack surface inventory (consolidated) | [`attack-surface-inventory.md`](./attack-surface-inventory.md) |
| Mandatory | SBOM (CycloneDX 1.5) | [`../artefacts/sbom.json`](../artefacts/sbom.json) |
| Mandatory | CVE report (govulncheck) | [`../artefacts/cve-report.md`](../artefacts/cve-report.md) |

---

## Key numbers

- **287 components** in the SBOM (Go modules + npm direct deps + container images).
- **56 reachable CVEs** in the Go dependency graph (govulncheck v1.3.0).
- **227 advisories total** in the Go dep graph (most are imports-only / not reachable).
- **57 distinct findings** across 7 risk tiers in the consolidated attack-surface inventory.

---

## Notable concrete attack vectors found just from Phase 1

These are the ones a Phase 2 pentester should hit first:

1. **`Spec.Runtime` arbitrary image pull** (RT-1.2 F1.2.1) — single-request supply-chain compromise via Workspace CRD spec.
2. **Status forge → SSRF + pod-exec hijack** (RT-1.2 F1.2.2) — `patch workspaces/status` redirects API proxy and terminal to attacker-chosen pods.
3. **Cross-tenant lateral movement** (RT-1.4 F1.4.1) — live cluster has zero L3/L4 isolation. Verified by direct `kubectl exec` from sandbox A → sandbox B's agentd port.
4. **agentd `/v1/statusz` and `/v1/healthz` unauthenticated** (RT-1.4 F1.4.2) — cross-tenant info disclosure including session IDs and titles.
5. **API SA `pods/exec` lacks label selector** (RT-1.3 F1.3.6) — API SA can exec into the controller pod, which holds a cluster-wide SA token. 2-stage privilege escalation.
6. **API → sandbox secret reload over plain HTTP** (RT-1.7 F1.7.1) — decrypted user credentials transit the cluster network without TLS or auth.
7. **API keys cleartext in DB** (RT-1.7 F1.7.2) — bearer tokens stored as plain strings; same string is the Redis cache key.
8. **`autoApprovePermissions` schema drift** (RT-1.2 F1.2.6) — currently safe (always `false`), but a future CRD-fix without auth review = silent privilege escalation.

---

## Cluster baseline note

The cluster runs LLMSafeSpace at `sha-cdf2ddc` (chart rev 68) which **predates** the worklog 0078 fixes for G2/G16/G17/G18/G20. Specifically:

- `networkPolicy.enabled: false` → no NetworkPolicy enforcement (G16 baseline)
- `rbac.scope: cluster` → controller has cluster-wide SA (G5 baseline)
- Sandbox pod template has no `automountServiceAccountToken: false` (G17 baseline)

Phase 6 RT-6.13 will deliberately upgrade the chart and re-measure the post-fix system. Phase 1 documents the pre-fix gap surface as the baseline for that comparison.

---

## What's next

Phase 2 — Authentication & Authorization Testing — should prioritize:

1. T0 (open/unauth, externally reachable) findings — see consolidated inventory.
2. T1.2 + T1.3 (image-pull bypass + status forge) — devastating impact, single-request exploits.
3. T2.1–T2.6 (cross-tenant) — verify the live cluster is as wide-open as static analysis predicts.
