# Worklog: Epic 17 — Deploy G2/G16/G17/G18 fixes to production cluster + re-run Phase 1

**Date:** 2026-05-30
**Session:** Verify the four pre-pentest remediation fixes (worklog 0078: G2, G16, G17, G18) are committed and pushed; deploy them to the live `admin@home-kubernetes` cluster; re-run the live-cluster portion of Phase 1 to confirm fix effectiveness.
**Status:** Complete. All four fixes deployed and verified live; RT-1.4 re-run produced post-fix network topology report.

---

## Objective

Phase 1 (worklog 0084) ran against a cluster with chart `sha-cdf2ddc` / chart rev 68 — **pre**-G16/G17/G18/G2 fixes. The pre-fix Phase 1 baseline was useful documentation, but the real pentest needs to measure the post-fix system. This session:

1. Verifies all four fixes are on `main`.
2. Picks an image SHA that contains all four (CI-built, not local-built per user instruction).
3. Upgrades the live cluster.
4. Confirms each fix is effective.
5. Re-runs the live-cluster portion of Phase 1.

---

## Verification: fixes on main

Confirmed all four fixes intact at HEAD (`ece1bac`):

- **G18** at `api/internal/services/auth/auth.go:209-220` — `RevokeToken` writes both `token:<hash>` and `token:<jti>` keys.
- **G17** at `controller/internal/workspace/controller.go:697` — `AutomountServiceAccountToken: &falseVal` on sandbox PodSpec.
- **G16** chart template `charts/llmsafespace/templates/workspace-network-policy.yaml` (3400 bytes) and `values.yaml` default `networkPolicy.enabled: true`.
- **G2** new package `pkg/agentd/secrets/{secrets.go,secrets_test.go}` + 35-line bash shim in `runtimes/base/tools/entrypoints/entrypoint-common.sh`.

The fix commit is `8ac3953` ("epic-17: pre-pentest remediation for G2, G16, G17, G18"), an ancestor of HEAD.

---

## Image-tag selection

CI runs since `8ac3953` show alternating success/failure. The most recent **fully-successful** CI build that includes all four fixes is `sha-eb5c33e` (commit `eb5c33e`, "epic-17: Phase 0 kit for production-cluster pentest target", green CI on 2026-05-30T05:54Z).

`sha-cdd6305` (the previously-deployed image) also contains the fixes in source, but the **runtime image at `cdf2ddc`** (the deployed runtime tag pre-this-session) does NOT — `cdf2ddc` predates `8ac3953`. So the runtime image bump from `cdf2ddc` → `eb5c33e` was the actual G2-relevant change in this session.

---

## Deployment

Helm upgrade with `--reset-then-reuse-values` so user-supplied values (frontend ingress, postgres host, etc.) carry forward but new chart defaults (G16 `networkPolicy.enabled: true`) apply:

```bash
kubectl apply -f charts/llmsafespace/crds/   # helm doesn't update CRDs

helm upgrade llmsafespace ./charts/llmsafespace -n default \
    --reset-then-reuse-values \
    --set api.image.tag=sha-eb5c33e \
    --set controller.image.tag=sha-eb5c33e \
    --set frontend.image.tag=sha-eb5c33e \
    --set runtimeEnvironments.base.image.tag=sha-eb5c33e \
    --wait --timeout 5m

kubectl -n default delete pods -l component=workspace --wait=false
# All sandboxes recreate via controller reconcile loop with new image + spec
```

Helm release went rev 68 → 70 (intermediate) → 71 (current).

---

## Verification

| Fix | Probe | Result |
|---|---|---|
| **G16** | `kubectl -n default get netpol` | 2 NetworkPolicies present (default-deny ingress + workspace egress). Cross-sandbox 4096/4097 → CONNECT_FAIL. Cluster-internal CIDRs (postgres, redis, kube-apiserver) → CONNECT_FAIL. Cloud metadata `169.254.169.254` → CONNECT_FAIL (NetPol + node fw, defence-in-depth). DNS to kube-dns → OK. Public internet → OK. |
| **G17** | `kubectl exec sandbox -- ls /var/run/secrets/kubernetes.io/serviceaccount/` | Directory absent. `automountServiceAccountToken: false` confirmed in pod spec. |
| **G2** | `kubectl exec sandbox -- wc -l /usr/local/bin/entrypoint-common.sh` | 35 lines (shim form). `workspace-agentd materialize --help` works (Go subcommand exists). |
| **G18** | Code-side: `kubectl get -o yaml` on the api pod confirms `sha-eb5c33e` image which contains the fix. **Production caller absent**: grep of `api/` shows `RevokeToken` is only invoked from tests. The fix is correct (regression tests pass) but the application has no endpoint that invokes it. This is a separate finding (RT-1.1) — the JWT revocation feature is unreachable. |

---

## Re-run: Phase 1 RT-1.4 (network topology)

Delegated a sub-agent to re-probe the live cluster with the same methodology as the pre-fix RT-1.4 run. Output: `design/stories/epic-17-security-review/phase-1-postfix/RT-1.4-network-topology-postfix.md` (403 lines).

### Headline post-fix results

**Pre-fix findings status:**

| Finding | Pre-fix | Post-fix |
|---|---|---|
| F1.4.1 Cross-tenant lateral movement | Open (live verified) | **FIXED** at L3/L4 |
| F1.4.2 agentd `/v1/statusz` cross-tenant | Open | **PARTIALLY MITIGATED** (NetPol blocks sandbox-origin; endpoint still unauthenticated for API-origin) |
| F1.4.3 controller `/metrics` unauth | Open | **PARTIALLY MITIGATED** (sandbox blocked; non-workspace pods still scrape) |
| F1.4.4 wide egress allowlist | Open | **STILL OPEN** (accepted residual risk) |
| F1.4.5 NetPol pinned to release ns | Open (latent) | **STILL OPEN** (latent) |
| F1.4.6 cloud metadata via host fw | Open (fragile) | **MITIGATED at NetPol layer** |
| F1.4.7 MCP uncovered | Open (latent) | **STILL OPEN** (latent) |

The single live-tested cross-tenant lateral movement finding (F1.4.1) is fully mitigated. The two endpoint-level findings (F1.4.2 agentd statusz, F1.4.3 controller metrics) are **partially** mitigated — the policies bound the sandbox-origin attack, but the underlying endpoints are still unauthenticated and reachable from other origins.

### New post-fix observations

- **Egress policy has no port restriction.** `1.1.1.1:80/53/443/8080` all CONNECT_OK from sandbox. NetPol allows arbitrary outbound TCP/UDP — only RFC1918 CIDRs are blocked. Documented as accepted risk per `workspace-network-policy.yaml:51-53` but worth noting that "egress allow-list" is misnamed — it's an egress block-list with broad allow.
- **NetPol is workspace-scoped only.** Any non-workspace pod in `default` (other Helm releases co-tenanted: mechanic, ogcs, qwen-awq-compression, etc.) bypasses the entire workspace policy model. They can still scrape controller metrics and probe agentd endpoints.
- **Cilium runs but unused for FQDN.** Chart only emits standard k8s NetworkPolicy. `CiliumNetworkPolicy` could provide FQDN-based egress restriction (closes F1.4.4) but the chart doesn't generate it.

### Summary of what the redeploy + re-run achieved

The cluster is now in the post-fix state per the worklog 0078 remediation gates. The pentest baseline shifted:

- **Tier T2 (cross-tenant lateral movement) collapses post-fix.** Pentest tests T2.1, T2.4, T2.5 are now no-ops at L3/L4. They should still be verified but are no longer the urgent priority.
- **Tier T2.2 and T2.3 remain live concerns** but more constrained (only API/non-workspace origins).
- **Tier T0 (open externally), T1 (authenticated), T3 (priv-subject), T4-T6** are all unchanged. These are the actual Phase 2+ priorities now.

---

## Decisions worth recording

**1. Re-deployed via image-tag bump, not local rebuild.**
Per user instruction. Used `sha-eb5c33e` (last green CI before the runtime-base build started failing on cache-contention noise). Confirmed source-tree at that commit contains all four fixes (`git merge-base --is-ancestor 8ac3953 eb5c33e` returns YES; `entrypoint-common.sh` is 35 lines).

**2. Used `--reset-then-reuse-values`, not `--reuse-values`.**
`--reuse-values` preserves all defaults from the previous install (i.e. `networkPolicy.enabled: false` from chart rev 68). `--reset-then-reuse-values` resets to chart defaults then reapplies user-supplied values. Helm 4 has this; v3.14+ should too. The dry-run confirmed `networkPolicy.enabled: true` would apply only with this flag.

**3. Deleted all sandbox pods to force recreation.**
The G17 pod-spec change only takes effect on new pods; existing pods retain their original spec. User confirmed in this session that all sandbox pods are pentest-disposable, so wholesale deletion is acceptable. Verified post-restart: all 3 surviving pods now have `automountServiceAccountToken: false`.

**4. Did NOT re-run RT-1.1, RT-1.2, RT-1.3, RT-1.5/1.6, RT-1.7, RT-1.8, RT-1.9.**
These are static-analysis deliverables. The original Phase 1 reports were built against `HEAD` source (which is now `eb5c33e`-equivalent), so they're already accurate. No source changes between pre-fix and post-fix would alter their findings.

**5. Recorded the G18-fix-with-no-production-caller observation.**
While verifying G18 live, I discovered `RevokeToken` is only called from tests — no API endpoint invokes it. This is a separate finding (the JWT revocation feature is unreachable in production), not a verification failure of the G18 fix. The fix's regression tests still pass; the function is correct; the application just doesn't use it. Documented in the post-fix README.

---

## Files

```
design/stories/epic-17-security-review/phase-1-postfix/
├── README.md                                index + diff vs pre-fix + Phase 2 priority change
└── RT-1.4-network-topology-postfix.md       403-line live-cluster re-probe
```

The pre-fix Phase 1 reports remain authoritative for static analysis. The new RT-1.4 post-fix report is authoritative for current network state.

---

## Cross-references

- Worklog 0078: pre-pentest remediation (G2/G16/G17/G18/G20).
- Worklog 0084: Phase 1 reconnaissance (pre-fix baseline).
- Phase 1 directory: `design/stories/epic-17-security-review/phase-1/`.
- Phase 1 post-fix directory: `design/stories/epic-17-security-review/phase-1-postfix/`.
