# Epic 17 — Phase 0: Pentest Environment Bootstrap

This directory is the **reproducible Phase 0 kit**. Running these scripts in order produces a known-good pentest environment that satisfies the Phase 0 exit criteria in [`../README.md`](../README.md):

> A control fixture has confirmed at least one expected vulnerability;
> logging is verified; rollback plan exists.

After Phase 0 completes, the cluster is suitable for Phase 1 reconnaissance: any finding produced by Phase 1+ is **caused by LLMSafeSpace**, not by missing tooling, missing CNI, or a misconfigured baseline.

---

## Why this kit exists

Phase 0 must be **reproducible**, not improvised. Two operators (or two LLM sessions) running these scripts on the same hardware should land at byte-identical cluster state. The kit makes that possible by:

1. **Pinning every tool version** (`tools-manifest.txt`) with sha256.
2. **Pinning every container image** by digest after the cluster is up (`01-record-image-shas.sh`).
3. **Using a CNI that enforces NetworkPolicy** (Cilium). The default `kind` CNI (kindnet) does NOT enforce NetworkPolicy, which would make the G16 fix unobservable. Without Cilium, the workspace network policies render as YAML but have zero effect — the pentest would silently produce false-negatives.
4. **Deploying a control fixture** that is deliberately vulnerable so kube-hunter / nuclei finding rates can be sanity-checked. If the control fixture's known vulnerability isn't found, the tooling has a false-negative and Phase 1 results cannot be trusted.

---

## Prerequisites

| Tool | Version | Why |
|------|---------|-----|
| `kind` | ≥ 0.23 | Cluster runtime |
| `kubectl` | ≥ 1.29 | matches kind node version |
| `helm` | ≥ 3.13 | LLMSafeSpace chart |
| `docker` | ≥ 24 | image build/load |
| `cilium` CLI | ≥ 0.16 | CNI install |
| `jq` | any modern | JSON parsing in scripts |

Optional but used by Phase 1:

| Tool | Version | Purpose |
|------|---------|---------|
| `kube-hunter` | 0.6.8 | RT-0.3, RT-1.x |
| `trivy` | 0.55+ | RT-1.5 SBOM + CVE |
| `grype` | 0.79+ | RT-1.5 SBOM cross-check |
| `cosign` | 2.x | image signature verification |
| `syft` | 1.x | SBOM generation |

Pinned versions and install hints in `tools-manifest.txt`.

---

## Execution order

Run scripts in numeric order. Each is idempotent (safe to re-run).

```bash
# RT-0.1 + RT-0.2: cluster + LLMSafeSpace install + Cilium + image SHAs
./00-bootstrap.sh
./01-record-image-shas.sh

# RT-0.3: control fixture for tooling validation
./02-deploy-control-fixture.sh
./02-verify-control-fixture.sh    # asserts kube-hunter finds the planted bug

# RT-0.4: test accounts
./03-provision-accounts.sh

# RT-0.5: logging
./04-verify-logging-baseline.sh

# RT-0.6: baseline snapshot for post-test diffing
./05-snapshot-baseline.sh

# Verify all exit criteria are met
./exit-check.sh
```

Total runtime on a 4-core / 16 GB host: ~12 minutes for a clean run, ~2 minutes for re-runs.

---

## Exit criteria (gate to Phase 1)

`exit-check.sh` verifies every box is ticked before declaring Phase 0 complete:

- [ ] **Cluster up.** `kind-llmsafespace-pentest` cluster reachable; nodes Ready.
- [ ] **CNI enforces NetworkPolicy.** Cilium installed; smoke-test pod-to-pod policy works.
- [ ] **LLMSafeSpace deployed.** API + controller + frontend rollouts complete.
- [ ] **NetworkPolicies applied.** `kubectl get netpol -A` shows G16 fix's two policies.
- [ ] **Image SHAs recorded.** `phase-0-artefacts/image-shas.json` exists with all 4 images by digest.
- [ ] **Control fixture vulnerable.** `02-verify-control-fixture.sh` confirms kube-hunter finds the planted bug. If kube-hunter misses it, halt Phase 1 — tooling is broken.
- [ ] **Test accounts provisioned.** admin, regular-A, regular-B; JWTs in `phase-0-artefacts/accounts.json` (NOT committed; in `.gitignore`).
- [ ] **Logging baseline.** API audit logs, controller logs, K8s audit logs all flowing within the last 60s.
- [ ] **Baseline snapshot.** `phase-0-artefacts/baseline-${TIMESTAMP}.tar.gz` contains `helm get all`, `kubectl get all -A -o yaml`, and CRD definitions.

If any check fails, fix before Phase 1.

---

## What this Phase 0 deliberately does NOT do

- **Does not run pentest tools yet.** Trivy / kube-hunter / nuclei runs are part of Phase 1, not Phase 0. Phase 0 only validates that the tooling produces a known result against a known-vulnerable fixture.
- **Does not provision a multi-node cluster.** A single kind node is sufficient for everything Phase 1–7 measures. Multi-node testing for things like PVC scheduling or PSA enforcement across nodes is out of scope.
- **Does not run a real cloud cluster.** Cloud-specific findings (G16 metadata endpoint blocking, IRSA confusion, etc.) are tested in a follow-on cloud-pentest-session, separate from this on-prem-equivalent baseline.
- **Does not exercise GPU runtimes.** Out of scope for Epic 17.

---

## Rollback

```bash
./teardown.sh   # destroys the kind cluster + clears artefacts
```

The kit produces no state outside `kind-llmsafespace-pentest` (cluster) and `phase-0-artefacts/` (gitignored local dir).

---

## Cross-references

- [Epic 17 README](../README.md) — Phase 0 specification (RT-0.1 through RT-0.6).
- [`local/bootstrap.sh`](../../../../local/bootstrap.sh) — dev bootstrap; we *invoke* it from `00-bootstrap.sh` rather than reimplement, then add CNI + the pentest-specific layers.
- [Worklog 0078](../../../../worklogs/0078_2026-05-29_epic17-pre-pentest-remediation.md) — pre-Phase-0 remediation; if any of those fixes regress, Phase 0 will surface it via the deployed-state assertions.
