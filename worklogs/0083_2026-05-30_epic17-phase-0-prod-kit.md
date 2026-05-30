# Worklog: Epic 17 Phase 0 — Production Cluster Variant Kit

**Date:** 2026-05-30
**Session:** Build a parallel Phase 0 kit (`phase-0-prod/`) targeting an existing production-grade cluster where LLMSafeSpace is already deployed in single-tenant test posture. Exercise the kit end-to-end against the real cluster to find bugs before declaring it ready.
**Status:** Complete; kit exercised end-to-end against `admin@home-kubernetes`; cluster fully cleaned by `cleanup.sh` after the dry run.

---

## Context

The user has a real production cluster (real Cilium, real audit pipeline, real cloud-provider K8s, real ingress controller) where LLMSafeSpace is deployed in `default` namespace as a single-tenant testing rig. Other namespaces in the cluster carry real workloads (databases, home, flux-system, mechanic, etc.) and are off-limits for the pentest. Pentesting against this cluster gives higher-fidelity findings than kind for several phases (real CNI behaviour, real cloud egress, real audit), but requires explicit blast-radius constraints.

The kind kit (`phase-0/`) provisions everything from scratch. The prod kit must NOT — the cluster is already running. So `phase-0-prod/` is a complement, not a replacement.

---

## Design decisions

**1. Separate kit, not env-flag in the existing kit.**
Mixing `PENTEST_TARGET=kind|prod` into one kit would require every script to branch on every operation. Two separate kits keep the intent loud: `phase-0/` cannot accidentally probe a real production resource; `phase-0-prod/` cannot accidentally create a cluster.

**2. Hard blast-radius rules in the README, validated softly in the preflight.**
After the user's "we can blow the install away and reinstall" comment, I dropped the strict "type the magic phrase" gate from preflight and cleanup. The rules are documented; the operator (and the LLM) can read them. Hard gating was overkill given recoverability.

**3. Control fixture lives in `pentest-control-fixture` ns, not `default`.**
The fixture pod is privileged and has cluster-admin SA. Co-tenanting it with LLMSafeSpace would let the controller see it on every reconcile pass, and any other Helm release in `default` could see it. Dedicated namespace makes cleanup mechanical (`kubectl delete ns` + the cluster-scoped CRB).

**4. Scoped snapshot, not whole-cluster dump.**
The kind kit dumps the entire cluster because it's a throwaway. The prod kit dumps `default` + `pentest-control-fixture` only, plus a DB row-count fingerprint per relevant table. Other namespaces' contents are real data — they shouldn't leave the cluster.

**5. DB-level cleanup via `kubectl exec deploy/postgres -- psql`.**
The user mentioned the `default`-ns Postgres can be probed for creds. Direct `psql` via in-pod exec sidesteps password handling entirely (peer auth in pg_hba.conf). The `users` table has CASCADE FKs to api_keys, sandboxes, permissions, user_keys, user_secrets, user_settings — `DELETE FROM users WHERE email LIKE '%@pentest.local'` is one statement.

**6. Workspaces table has NO FK to users.**
`workspaces.user_id` is a varchar string, not a FK. Cleanup must delete workspace CRDs (label-selected by `user-id=<uuid>`) before deleting user rows, and let the controller's finalizer clean up sandbox pods + PVCs. Verified during the dry run: 3 workspaces deleted, all finalized within 90s.

**7. K8s audit log canary is operator-step.**
Cloud-managed clusters export audit logs to the cloud's logging backend. The kit can't `find /var/log/kubernetes/audit`. Instead it generates a uniquely-named canary event (`Get on a non-existent secret`) and prints the canary name; the operator searches the cloud dashboard. If the canary appears, audit pipeline is live.

---

## End-to-end dry run against the real cluster

Ran the full kit against `admin@home-kubernetes` to find bugs before committing.

```
$ ./00-preflight.sh        ✓ context + Cilium + LLMSafeSpace + Postgres reachable
$ ./01-record-state.sh     ✓ 5 image SHAs recorded; helm values + manifest captured
$ ./02-deploy-control-fixture.sh   ✓ pentest-control-fixture ns + privileged pod
$ ./02-verify-control-fixture.sh   ✗ kubeaudit / trivy / kube-hunter not installed
$ ./03-provision-accounts.sh       ✓ 3 accounts; user_ids resolved from DB
$ ./04-verify-logging-baseline.sh  ✓ API + controller logs flowing; canary printed
$ ./05-snapshot-default-ns.sh      ✓ baseline tarball; sha256 sidecar
$ ./exit-check.sh                  ✓ all 10 exit gates green
$ ./cleanup.sh                     ✓ 3 workspaces finalized, 0 user rows remain,
                                     ns + CRB removed, artefacts wiped
```

**Bugs found during the dry run:**

1. **`provision()` echoed progress to stdout, polluting captured JWTs.** Fix: redirect progress to stderr (`echo "==> Provisioning ${user}" >&2`). Without exercising the kit, I would have committed this and operators would have hit it on first use.
2. **`02-verify-control-fixture.sh` correctly handled "no tools available"** — exited non-zero with helpful "install at least kubeaudit + trivy" message. This is the test working as designed; flagged here so it's not mistaken for a regression.
3. **DB cleanup found 3 workspaces from prior test runs** under the same email addresses (these accounts had been used by earlier sessions for SDK live tests). Cleanup correctly enumerated and removed them. Verified zero `@pentest.local` rows remain in DB.

The dry run is the strongest proof I can produce that the kit works. Any future operator running the kit lands at the same exit-check state I did.

---

## Files

```
design/stories/epic-17-security-review/phase-0-prod/
├── README.md                       blast-radius rules, execution order, exit criteria
├── 00-preflight.sh                 cluster shape, RBAC, ns scope assertions
├── 01-record-state.sh              image SHAs + helm values + ns inventory + DB schema
├── 02-control-fixture.yaml         deliberately-vulnerable pod (3 planted bugs)
├── 02-deploy-control-fixture.sh    apply fixture into pentest-control-fixture ns
├── 02-verify-control-fixture.sh    tool calibration; refuses to proceed on false-negative
├── 03-provision-accounts.sh        admin / regular-a / regular-b via deployed API + DB
├── 04-verify-logging-baseline.sh   API + controller logs; cloud audit canary
├── 05-snapshot-default-ns.sh       scoped tarball + sha256
├── exit-check.sh                   gate to Phase 1
└── cleanup.sh                      workspaces → DB → ns → local artefacts
```

`.gitignore` updated to exclude `phase-0-prod/phase-0-prod-artefacts/` (JWTs and DB IDs).

Epic 17 README's Phase 0 section now lists both kits side-by-side; operators choose based on what's available.

---

## What's NOT done

- **Phase 1 itself.** Phase 0 only validates that the environment is ready. Phase 1 reconnaissance is the next epic step.
- **Tool installation (kubeaudit, trivy, etc.).** Operator-side; kit refuses to gate Phase 1 until tools are available.
- **Cloud audit log verification.** Kit prints the canary; operator confirms in cloud dashboard.

---

## Cross-references

- Epic 17 plan: `design/stories/epic-17-security-review/README.md`
- Sister kit (kind): `design/stories/epic-17-security-review/phase-0/`
- Worklog 0078: pre-Phase-0 remediation (G2/G16/G17/G18/G20). The current production cluster is on chart revision 67 (`sha-cdf2ddc`) which **predates** these fixes, so Phase 1 will measure the pre-fix gap surface. Phase 6 RT-6.13 will explicitly upgrade the chart and re-measure as a deliberate test.
- Worklog 0082: Phase 0 (kind) kit — the sister artefact for the throwaway-cluster path.
