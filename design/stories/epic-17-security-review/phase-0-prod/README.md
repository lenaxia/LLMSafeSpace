# Epic 17 — Phase 0 (Production Cluster Variant)

This kit targets the existing production cluster, where LLMSafeSpace is already deployed in the `default` namespace as a single-tenant test deployment. Unlike `phase-0/` (which provisions a fresh kind cluster), this kit:

- **Does NOT install LLMSafeSpace.** The cluster already runs it.
- **Does NOT install Cilium.** The cluster already has it.
- **Does NOT modify cluster-scoped resources** (RBAC, CRDs, webhooks, etc.) outside the LLMSafeSpace release.
- **Does NOT touch namespaces other than `default` and `pentest-control-fixture`.** All other namespaces (databases, home, flux-system, mechanic, etc.) are off-limits even though the operator has cluster-admin credentials.

It DOES:
- Verify the cluster matches expected shape (cilium present, llmsafespace deployed, audit pipeline live).
- Record the deployed image SHAs for reproducibility.
- Plant a control fixture in a dedicated `pentest-control-fixture` namespace (NOT `default`) for tooling calibration.
- Provision three test accounts (admin, regular-a, regular-b) via the deployed API.
- Verify audit logs are flowing.
- Snapshot **only the `default` namespace** state for post-test diffing — no other-namespace data leaves the cluster.
- Provide a cleanup script that removes ONLY the artefacts this kit created.

---

## Why a separate kit

The `phase-0/` (kind) kit assumes a throwaway environment: install everything, plant fixtures freely, dump the whole cluster on snapshot, tear it all down. Production is the opposite: assume nothing is throwaway except what we explicitly own.

Mixing both modes into one kit (`PENTEST_TARGET=kind|prod` flag) would require every script to branch on every operation. Keeping them separate keeps each script's intent loud — there is no path where a `phase-0-prod/*.sh` script could create a cluster, and there is no path where a `phase-0/*.sh` script could probe a real production resource.

---

## Blast-radius rules

These rules apply to **every** Phase 0–7 test against this cluster. If any test plan calls for an action that would violate these, the test is **off-limits** and either skipped or scoped down.

**Allowed:**

- All actions inside the `default` namespace (LLMSafeSpace's deployment lives here).
- All actions inside the `pentest-control-fixture` namespace (created by this kit; deleted by `cleanup.sh`).
- Reading any other namespace metadata (list pods, services, etc.) for Phase 1 reconnaissance — but ONLY metadata, not data.
- Reading `default`-namespace LLMSafeSpace logs, secrets, configmaps.
- Creating Workspace CRDs in `default` ns (and consequent sandbox pods).
- Destroying any sandbox pod, workspace, or PVC in `default` ns. **All sandbox pods in `default` are pentest-disposable.**

**OFF-LIMITS:**

- Any write/mutation/delete in `databases`, `home`, `flux-system`, `cert-manager`, `cilium-secrets`, `actions-runner-system`, `authelia-test`, `fission`, `kube-system`, `kube-public`, `kube-node-lease`, or any namespace not listed in "Allowed".
- Any cluster-scoped mutation outside the LLMSafeSpace Helm release (no creating/deleting ClusterRoles, ClusterRoleBindings, ValidatingWebhookConfigurations, MutatingWebhookConfigurations, CRDs, StorageClasses, etc.).
- Helm upgrade/uninstall of releases other than `llmsafespace` (we may upgrade/downgrade `llmsafespace` to test G16 enforcement on/off).
- Mounting host paths, modifying node OS state, or any test that requires SSH/SSM access to nodes.
- Egress to attacker-controlled domains for exfiltration tests (RT-3.10 DNS exfil, RT-5.7 NetworkPolicy bypass) — these would generate real cloud egress costs and trigger the operator's monitoring. Use loopback/in-cluster destinations only for these tests.
- Test accounts must use `pentest.local` email domain; never use a real email address.

**If a Phase X test plan would require an off-limits action, the kit MUST refuse it** (preflight fails) rather than silently degrade. Document it in the worklog as "test deferred to throwaway-cluster session".

---

## Pre-fix state of this cluster

At the time of writing, this cluster runs LLMSafeSpace at commit `sha-cdf2ddc` (Helm chart version 0.1.0, revision 67). This is **before** the worklog 0078 fixes for G2/G16/G17/G18/G20:

```
$ helm -n default get values llmsafespace --all | grep -A1 "networkPolicy:"
networkPolicy:
  enabled: false        ← G16 not deployed

$ helm -n default get values llmsafespace --all | grep -A1 "rbac:"
rbac:
  scope: cluster        ← G5 baseline (cluster-wide controller)
```

This is **deliberate**: the production cluster reflects what an operator who hasn't upgraded yet sees. The pentest measures both:

1. **Pre-fix gap surface** at the current commit — useful for "should we fast-track the upgrade" arguments.
2. **Post-fix system** after we run `helm upgrade` to the latest chart mid-pentest (Phase 6 will do this as a deliberate test).

Phase 1 will document the pre-fix state. Phase 6 RT-6.13 explicitly upgrades LLMSafeSpace to the post-fix chart and re-measures so the diff between pre- and post-fix is a deliverable.

---

## Prerequisites

- Operator has cluster-admin credentials on the kubeconfig context targeting this cluster (the kit asserts this in `00-preflight.sh`).
- `kubectl`, `helm`, `jq`, `curl` on PATH.
- Optional but used in Phase 1+: `trivy`, `kubeaudit`, `kube-hunter`, `nuclei`, `cosign`, `syft`.

The kit does NOT install or upgrade any of these. They're pre-existing on the operator's machine.

---

## Execution order

```bash
# Set the kubeconfig context, then:
export KUBE_CONTEXT="<your context name>"

./00-preflight.sh                  # verify cluster shape, RBAC, ns scope
./01-record-state.sh               # image SHAs, helm release version, default-ns inventory
./02-deploy-control-fixture.sh     # plant fixture in pentest-control-fixture ns
./02-verify-control-fixture.sh     # tool calibration
./03-provision-accounts.sh         # admin, regular-a, regular-b in default-ns LLMSafeSpace
./04-verify-logging-baseline.sh    # API + controller log check (audit log via cloud)
./05-snapshot-default-ns.sh        # baseline-default-${TS}.tar.gz
./exit-check.sh                    # gate to Phase 1

# When done with the pentest:
./cleanup.sh                       # removes ONLY pentest fixture + accounts
```

Each script is idempotent. None mutate cluster state outside the allowed scope.

---

## Exit criteria (gate to Phase 1)

`exit-check.sh` verifies:

- [ ] Cluster reachable on the configured context.
- [ ] LLMSafeSpace deployed in `default` ns; release name `llmsafespace`.
- [ ] Cilium present and healthy.
- [ ] Image SHAs recorded.
- [ ] Control fixture pod is Ready in `pentest-control-fixture` ns.
- [ ] Tool calibration passed (kubeaudit and trivy detect the planted bugs).
- [ ] Test accounts provisioned; JWTs in `phase-0-prod-artefacts/accounts.json` (mode 0600, gitignored).
- [ ] API server logs flowing within last 60s.
- [ ] Controller logs flowing within last 60s.
- [ ] Default-ns snapshot present.

If any check fails, fix before Phase 1.

---

## Cleanup

`cleanup.sh` removes:

- The `pentest-control-fixture` namespace (cascades to fixture pod, SA, CRB).
- The three test accounts via `DELETE /api/v1/auth/account` (or DB delete if API doesn't expose it — see script).
- The `phase-0-prod-artefacts/` directory (gitignored anyway).

It does **not** touch:
- Any sandbox pod or workspace not owned by the test accounts.
- Anything in `default` ns belonging to LLMSafeSpace itself.
- Anything in any other namespace.

Run cleanup after the pentest is complete. Re-running cleanup on a partially-applied state is safe.

---

## Cross-references

- Epic 17 plan: [`../README.md`](../README.md)
- Throwaway-cluster kit (kind): [`../phase-0/README.md`](../phase-0/README.md) — same RT-0.x deliverables, different target.
- Worklog 0078: [`../../../../worklogs/0078_2026-05-29_epic17-pre-pentest-remediation.md`](../../../../worklogs/0078_2026-05-29_epic17-pre-pentest-remediation.md) — fixes deployed in later releases; this cluster doesn't have them yet.
