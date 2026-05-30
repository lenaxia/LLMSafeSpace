# Phase 1 — Post-Fix Re-Run

**Status:** Re-run against post-G2/G16/G17/G18 deployment
**Date:** 2026-05-30
**Cluster:** `admin@home-kubernetes` — LLMSafeSpace `sha-eb5c33e` (Helm rev 70/71), runtime `sha-eb5c33e`

---

## What changed

The cluster previously ran chart `sha-cdf2ddc` / chart rev 68 — **pre**-G16/G17/G2/G18 fixes. After this re-run, the cluster runs:

| Component | Pre-fix | Post-fix | Verified by |
|---|---|---|---|
| API image | `sha-cdd6305` | `sha-eb5c33e` | `kubectl get deploy llmsafespace-api -o jsonpath='{...image}'` |
| Controller image | `sha-cdd6305` | `sha-eb5c33e` | same |
| Frontend image | `sha-cdd6305` | `sha-eb5c33e` | same |
| Runtime base image | `sha-cdf2ddc` | `sha-eb5c33e` | sandbox pod containers |
| `networkPolicy.enabled` | `false` | `true` | `helm get values` |
| Workspace NetworkPolicies | 0 | 2 (default-deny ingress + egress allow-list) | `kubectl -n default get netpol` |
| Sandbox `automountServiceAccountToken` | unset (default true) | `false` | pod spec inspection + filesystem probe |
| Bash secret-loop in entrypoint | 224-line script | 35-line shim → `workspace-agentd materialize` Go subcommand | `wc -l /usr/local/bin/entrypoint-common.sh` |

---

## What this re-run covers

The original 9 RT-1.x deliverables were a mix of static-analysis and live-cluster probing. Only the live-cluster portions are re-validated here, since the static analysis was done against `HEAD` source and remains accurate at `sha-eb5c33e` (which is now actually deployed).

| RT | Re-run? | Reason |
|---|---|---|
| RT-1.1 API endpoints | No | Static analysis of `router.go` — no source changes between `cdd6305` and `eb5c33e` affecting this surface. |
| RT-1.2 CRD schema | No | Static analysis; no schema changes. |
| RT-1.3 RBAC | No | Static analysis; G5 (rbac.scope=cluster) still default — out of pre-pentest remediation scope. |
| **RT-1.4 Network topology** | **Yes** | The G16 fix is fundamentally a network-layer change. Live re-probing required. |
| RT-1.5/1.6 Deps + images | Partial | Image SHAs updated in SBOM addendum (below). govulncheck output unchanged at this commit range. |
| RT-1.7 Secret storage | No | Static analysis. F1.7.1 (plain-HTTP secret reload) is unchanged; the G2 fix moves materialization to a Go binary but the API→sandbox transport path is untouched. |
| RT-1.8 Frontend | No | No frontend code changes between the two commits. |
| RT-1.9 Build chain | No | Same Dockerfiles. |

---

## Files

| File | Description |
|---|---|
| [`RT-1.4-network-topology-postfix.md`](./RT-1.4-network-topology-postfix.md) | Live re-probe of all RT-1.4 connectivity tests against post-fix cluster. Headline: **G16 + G17 working as designed**; F1.4.4 (wide egress allowlist), F1.4.5 (NetPol pinned to release ns), F1.4.7 (MCP uncovered) **still open** as documented residual risks. |

---

## Updated finding status

Cross-referenced against the original consolidated attack-surface inventory (`../phase-1/attack-surface-inventory.md`):

| ID | Pre-fix status | Post-fix status | Evidence |
|---|---|---|---|
| F1.4.1 — Cross-tenant lateral movement | Open (live verified) | **FIXED** at L3/L4 | TCP probes from sandbox A → sandbox B's 4096/4097 now `CONNECT_FAIL` (verified) |
| F1.4.2 — agentd `/v1/statusz` cross-tenant | Open | **PARTIALLY MITIGATED** | Now blocked at NetPol layer for non-API ingress, but endpoint itself remains unauthenticated; API-authz path still requires testing in Phase 3 (RT-3.18) |
| F1.4.3 — controller `/metrics` unauth | Open | **PARTIALLY MITIGATED** | Sandbox pods now blocked. Still reachable from any non-workspace pod in cluster (verified with probe pod). |
| F1.4.4 — Wide egress allowlist | Open | **STILL OPEN** (accepted residual risk per chart docs) | Verified: arbitrary public IP/port still reachable from sandbox |
| F1.4.5 — NetPol pinned to release ns | Open (latent) | **STILL OPEN** (latent) | Same source code; would manifest if operator splits namespaces |
| F1.4.6 — Cloud metadata via host fw | Open (fragile) | **MITIGATED at NetPol layer** | Defence-in-depth: `blockedEgressCIDRs: 169.254.0.0/16` now also blocks at NetPol |
| F1.4.7 — MCP uncovered by NetPols | Open (latent) | **STILL OPEN** (latent) | MCP not deployed in this cluster; would need its own NetPol |
| F1.4.8 — All services ClusterIP | Info | Info | Unchanged |
| **G17 (sandbox SA token)** | Open | **FIXED** | `/var/run/secrets/kubernetes.io/serviceaccount/` directory absent in sandbox container (verified by exec) |
| **G2 (entrypoint shell injection)** | Open | **FIXED** | Bash entrypoint is 35-line shim; `workspace-agentd materialize` subcommand exists in deployed runtime |
| **G18 (JWT revocation cache key)** | Open | **FIXED in code, no production caller** | Code path correct; no production endpoint invokes `RevokeToken` (separate RT-1.1 finding) |

---

## Pentest priority change

The Phase 2+ test plan ranking from `attack-surface-inventory.md` should be updated:

**Tier T2 (cross-tenant lateral movement) collapses post-fix.** Tests T2.1, T2.4, T2.5 are now no-ops at L3/L4 — they would all `CONNECT_FAIL`. The pentest should still verify these, but they are no longer the urgent priority.

**Tier T2.2 and T2.3 remain live concerns**:
- T2.2 — agentd `/v1/statusz` unauth — still a finding, just more constrained (only API can reach it now, but the endpoint itself is still unauthenticated; if API path-based authz fails, cross-tenant still exploitable through the API proxy).
- T2.3 — controller `/metrics` — only mitigated for sandbox-origin traffic; non-workspace cluster pods can still scrape.

**Tier T0 (open/unauthenticated externally reachable) is unchanged.** None of the four pre-pentest fixes addressed `/readyz` driver-error leak, `/metrics` unauth at ingress, account-recovery brute-force, or login user-enumeration.

**Tier T1 (authenticated/namespace-wide) is unchanged.** `Spec.Runtime` arbitrary image pull, status-forge SSRF, init-container shell injection, `Spec.Resources` not enforced — all still open.

**Tier T3 (privileged-subject blast radius) is unchanged.** G5 (cluster-wide controller SA) is the next big remediation.

**Tiers T4 (supply chain), T5 (CVEs), T6 (latent footguns) are unchanged.**

---

## What's next

The pre-pentest remediation gates (worklog 0078) are complete. The cluster is now in the **measure-the-post-fix-system** state.

Phase 2 should begin against this baseline. The first targets:

1. **T0.3** account recovery brute-force (no creds needed, externally reachable)
2. **T1.2** Spec.Runtime arbitrary image pull
3. **T1.3** Status forge → SSRF + pod-exec hijack
4. **T2.2** agentd /v1/statusz cross-tenant (verify whether API path-authz prevents it post-G16)

The pre-fix Phase 1 reports remain authoritative for static analysis. The new RT-1.4 post-fix report is authoritative for current network state.
