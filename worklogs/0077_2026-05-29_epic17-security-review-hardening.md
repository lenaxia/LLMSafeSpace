# Worklog: Epic 17 Security Review — Threat Model + Pentest Plan Hardening

**Date:** 2026-05-29
**Session:** Reviewed Epic 17 (Security Review & Pentest Plan); verified all gaps and assumptions against the codebase; rewrote both `README.md` and `THREAT-MODEL.md` with concrete file:line evidence; discovered 6 new gaps including a broken JWT revocation path
**Status:** Complete

---

## Objective

User requested a review of Epic 17. After producing initial review feedback (broken doc links, unverified assumptions, missing test cases), user instructed: "address all your fixes and gaps. dont worry about dates and timeline, its all LLM done anyway. verify all assumptions. add all missing test cases."

Goal: validate every gap (G1–G14) and every assumption (A1–A8) per `README-LLM.md` Rule 7 (Assumptions: State, Then Validate); add missing test cases; fix broken doc links; restructure scope/severity/reporting.

---

## Work Completed

### Gap verification — all 14 original gaps validated against code

Each gap now has a file:line citation in `THREAT-MODEL.md §5`. Notable confirmations:

- **G1** (no noexec on tmpfs): `controller/internal/workspace/controller.go:630-632` — emptyDir volumes for `sandbox-cfg`, `tmp`, `sandbox-home` have neither `Medium: Memory` nor noexec.
- **G2** (entrypoint shell injection): `runtimes/base/tools/entrypoints/entrypoint-common.sh:78` — `echo "export ${VAR_NAME}='${PLAINTEXT}'" >> "$ENV_FILE"` is a single-quote breakout. Same file lines 41/43/59 also unvalidated.
- **G4** (no mTLS): `api/internal/handlers/proxy.go:91-95` — plain `http.Transport`, no TLSClientConfig.
- **G5** (controller cluster-scope SA): `charts/llmsafespace/templates/rbac.yaml:1-95` — defaults to ClusterRole/ClusterRoleBinding when `rbac.scope == "cluster"` (the values.yaml default).
- **G13** (account lockout DoS): `api/internal/services/auth/auth.go:441,502` — lockout key is `lockout:<email>`; any attacker who knows a victim's email can lock them out.

### 6 new gaps discovered during verification

These were not in the original threat model and would have been missed by the planned pentest:

- **G15 (High)** — Sandbox emptyDir volumes are disk-backed (`controller.go:630-632` lacks `Medium: Memory`). Plaintext credentials in `/sandbox-cfg/secrets.json` survive on the kubelet's disk until volume reclamation.
- **G16 (Critical)** — The Helm chart ships **zero** NetworkPolicy templates (`charts/llmsafespace/templates/` audit). All "network isolation" claims in design docs are unsupportable on a fresh install. `values.yaml:301` documents this as operator responsibility, but no preflight or default-deny ships.
- **G17 (High)** — `controller/internal/workspace/controller.go:653-666` builds a sandbox PodSpec without `AutomountServiceAccountToken: false`. Default behavior automounts the SA token; compromised agents have an SA token by default.
- **G18 (High)** — JWT revocation is silently broken. `auth.go:203` writes cache key `token:<jti>`; `auth.go:270` reads `token:<hash(token)>`. Cache keys never collide → `RevokeToken` is a no-op. Confirmed by reading both functions; no test exercises the revoke→validate end-to-end path.
- **G19 (Medium)** — `runtimes/base/Dockerfile:120` sets `MISE_GITHUB_ATTESTATIONS=0`, explicitly disabling Sigstore attestation checks for runtime installs. Combined with `Dockerfile:86-98` (mise tarball downloaded over TLS only, no checksum), this is a build-time supply chain hole.
- **G20 (Medium)** — `entrypoint-common.sh:14, 19, 35` writes `/tmp/agent-config.json` with default umask permissions; never chmod-ed to 600.

### Assumptions A1–A8 validated; A9 and A10 added

Per Rule 7 every assumption now has a Validation Method, Status, and Evidence/Action column in `THREAT-MODEL.md §8`:

- **A1** (etcd encryption) — Unvalidated; no chart preflight. Action item: add `helm install` pre-upgrade hook.
- **A2** (NetworkPolicy CNI) — Unvalidated; combined with G16 means even a perfect CNI gets no policies applied.
- **A3** (Node OS patched) — Unvalidated; operator responsibility, no preflight.
- **A4** (TLS at ingress) — **REFUTED**. `charts/llmsafespace/values.yaml` defaults `frontend.ingress.tls: false` and api ingress similar. The threat model assumed TLS-by-default; reality is the opposite.
- **A5/A6** (Redis/Postgres not externally exposed) — Conditional. Chart references operator-deployed services; no NetworkPolicy ships to enforce.
- **A7** (trusted images) — PARTIAL. Base image is digest-pinned (`debian:bookworm-slim@sha256:...`); opencode and mise downloads (Dockerfile lines 67-78 and 86-98) are TLS-only, no signature verification.
- **A8** (JWT key rotation) — **REFUTED**. `api/internal/services/auth/auth.go` has no rotation primitives; key sourced once at startup. No `kid` header, no JWKS.
- **A9** (rehype-sanitize sufficient) — New; unvalidated; requires fuzz testing against bypass corpora.
- **A10** (operator preconditions) — New; unvalidated; chart README documents but does not check.

### Test cases added (60+ total)

New test cases that map directly to verified gaps:

- RT-2.13 — JWT revocation bypass (G18). Concrete reproduction: revoke token, immediately reuse, expect 401, observe 200.
- RT-2.14 — Long-lived JWT after key rotation (A8).
- RT-3.2 — SA token automount (G17). `cat /var/run/secrets/kubernetes.io/serviceaccount/token` from sandbox.
- RT-3.15 — Plaintext secrets on node disk (G15).
- RT-3.16 — `/tmp/agent-config.json` permissions (G20).
- RT-3.17 — Mise-installed runtime tampering (PVC-resident binaries survive suspend/resume).
- RT-4.12 — DEK lifecycle on workspace deletion.
- RT-4.13 — DEK lifecycle on session revocation (interlocks with G18).
- RT-4.14 — Concurrent credential rotation race.
- RT-4.15 — Mise binary tampering at build (G19).
- RT-4.16 — Opencode binary tampering at build (no Sigstore).
- RT-5.11 — Plain-HTTP MITM (G4).
- RT-5.12 — `stripPatchParts` JSON parser DoS (`proxy.go:519`).
- RT-5.13 — Header timeout exhaustion (G12).
- RT-5.14 — `verbose=true` filter bypass.
- RT-6.11 — PSA enforcement absence (G11).
- RT-6.12 — NetworkPolicy absence (G16).
- RT-6.13 — Helm preflight assumption check.
- RT-6.14 — Default-on TLS at ingress (A4).
- RT-6.16 — Controller cluster-scope default (G5).
- RT-7.9 — Frontend XSS bypass against rehype-sanitize (A9).
- RT-7.10–7.15 — Code blocks, tool rendering, diff viewer, CSP, JWT storage.

### Structural changes to README.md

- **Phase 0 added** for environment setup with control-fixture validation step (rules out false negatives in tooling).
- **Frontend XSS pulled in-scope** — was previously punted to "separate assessment."
- **Build & supply chain** added as an explicit layer.
- **Severity rubric** now includes deployment-gate semantics: Critical = HARD BLOCK with Release Manager (Lena) sign-off in worklog before any tag publishes; High = SOFT BLOCK with joint Release Manager + Security Lead sign-off.
- **Acceptance authority** for "Accepted Risk" is now a hard process (joint sign-off, documented rationale, 6-month re-evaluation).
- **Reporting template** gained chain-of-custody fields: Discoverer, internal/public disclosure dates, affected versions, validator name, worklog reference.
- **SBOM/CVE handling**: Phase 1 mandates committing CycloneDX SBOM; CI fails on new ≥ High CVEs.
- **Epic 10 cross-reference**: explicit invariant→test mapping showing G5 and G17 currently violate Epic 10 invariants.
- **Fixed broken doc links**: removed all `../../NNNN_*.md` worklog-style references; replaced with actual paths to `design/EVOLUTION-V2.md`, `design/SECURITY.md`, `design/NETWORK.md`, etc.

### Structural changes to THREAT-MODEL.md

- Added attack tree §4.5 for frontend XSS / browser-side compromise.
- Added STRIDE row for Frontend.
- Added §9 Out-of-Scope with mitigation owner per item.
- Revision history records the 1.1 changeset.

---

## Key Decisions

- **Verify, don't assume.** Initial review feedback called out unverified gap claims. Following Rule 7, every gap was re-read in source. This caught G18 (JWT revocation broken) which had been mis-described as a working feature in the threat model and in `README-LLM.md:1432`.
- **Promote frontend XSS to in-scope.** The agent renders LLM-generated markdown via `react-markdown` + `rehype-sanitize`. Even if this is technically frontend, it directly affects multi-tenant safety (a malicious assistant message could exfiltrate the user's JWT). Punting it to a separate assessment was a scoping error.
- **Hard sign-off gates.** Per `README-LLM.md` zero-tech-debt principle, Critical findings cannot be "shipped with mitigation" — they hard-block production tags. Soft-block for High requires named-role joint sign-off, recorded in a worklog so the trail is auditable.
- **Document validation status in the threat model itself.** Rather than burying assumption validation in worklogs, the threat model now carries a status column. This forces every revision to own its evidence.

---

## Blockers

None for this work. However, several **findings should be remediated before pentest begins**, because they are confirmed-by-reading-the-code, not hypothetical:

- **G18 (JWT revocation broken)** — fix is small (unify cache key scheme) and prevents wasted pentest cycles validating a feature that's verifiably broken.
- **G16 (no NetworkPolicy ships)** — pentest results for Phase 5 are largely predetermined without baseline policies. Either ship default-deny NetworkPolicy templates first, or scope Phase 5 around "operator-supplied policy effectiveness" instead of platform claims.
- **G17 (SA token automount)** — one-line fix in pod spec; without it, container-escape testing in Phase 3 measures the wrong thing.
- **G2 (entrypoint shell injection)** — confirmed exploitable; small fix (validate VAR_NAME, base64-encode PLAINTEXT). Pentest can validate the fix instead of re-discovering the bug.

Recommended sequencing: fix G2, G16, G17, G18 in a remediation epic *before* Phase 1 of Epic 17 begins, so the pentest measures the post-fix state. Unfixed gaps remain in the test plan as known-baselines (G1, G15, G20, etc.) where the pentest's job is to confirm exploitability and quantify blast radius.

---

## Tests Run

No code changes; this session was design + threat modeling only. Verification was via:

- `read` and `grep` against `controller/internal/workspace/controller.go`, `api/internal/handlers/proxy.go`, `api/internal/services/auth/auth.go`, `runtimes/base/Dockerfile`, `runtimes/base/tools/entrypoints/entrypoint-common.sh`, `charts/llmsafespace/templates/rbac.yaml`, `charts/llmsafespace/values.yaml`, `frontend/src/components/chat/MessagePart.tsx`.
- Cross-checked claims in `README-LLM.md:1432` (JWT jti revocation) against `auth.go:155-205` (RevokeToken) and `auth.go:260-296` (ValidateToken) — disproved.

---

## Next Steps

1. **Decide remediation strategy.** Either (a) open a small remediation epic for G2/G16/G17/G18 to be merged before Epic 17 Phase 1 begins, or (b) accept that Phase 1 will largely re-discover these and produce remediation as part of the epic's normal validator loop.
2. **Confirm severity assignments** with Security Lead. Specifically: should G16 (no NetworkPolicy ships) be Critical or High? Argument for Critical: multi-tenant claims are unsupportable. Argument for High: operators can supply their own policies and existing deployments may already do so.
3. **Pre-flight Helm chart guards.** Implement chart-side preflight for A1 (etcd encryption), A2 (NetworkPolicy CNI), A4 (TLS-at-ingress default flip).
4. **Phase 0 environment** for the pentest itself — provision pentest cluster, accounts, and tooling.

---

## Files Modified

- `design/stories/epic-17-security-review/README.md` — major rewrite (326 lines changed)
- `design/stories/epic-17-security-review/THREAT-MODEL.md` — major rewrite (249 lines changed)
- `worklogs/0077_2026-05-29_epic17-security-review-hardening.md` — this file
