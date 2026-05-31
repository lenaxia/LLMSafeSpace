# Epic 17 — Security Review & Penetration Test — Final Report

**Status:** All seven phases complete
**Cluster under test:** `admin@home-kubernetes` (Talos, Cilium, Longhorn) post-fix image `ghcr.io/lenaxia/llmsafespace/*:sha-eb5c33e`
**Period:** 2026-05-29 → 2026-05-30
**Worklogs:** 0078, 0082, 0083, 0084, 0085, 0086, 0087, 0088, 0089, 0090, 0091, 0092

---

## Executive Summary

The pentest exercised **108 distinct test cases** across 7 phases against a live cluster running the post-fix image (which closed G2/G16/G17/G18/G20 at code level). Phase 1 reconnaissance produced 57 findings via static + cluster analysis; Phases 2-7 produced **structured live-cluster verdicts** for each test:

| Phase | PASS | FAIL | INCONCLUSIVE | SKIP | Tests |
|---|---|---|---|---|---|
| 2 (authn/authz) | 7 | 6 | 4 | 1 | 18 |
| 3 (sandbox isolation) | 9 | 6 | 0 | 2 | 17 |
| 4 (credential & crypto) | 5 | 7 | 4 | 0 | 16 |
| 5 (proxy & network) | 5 | 3 | 6 | 0 | 14 |
| 6 (k8s & infra) | 7 | 8 | 1 | 0 | 16 |
| 7 (app logic + frontend) | 12 | 2 | 1 | 0 | 15 |
| **Total** | **45** | **32** | **16** | **3** | **96** |

(Phase 1 had no PASS/FAIL semantics — it was reconnaissance.)

**Headline:** the post-fix system is **substantially more secure than pre-fix** but still has **8 high-or-above-severity findings the threat model did not anticipate**, including default datastore passwords (G26 critical), unredacted secret values in API logs (G25 high), and the G18 fix being dormant in production (RT-4.13).

---

## Findings ranked by severity

### Critical (2)

| ID | Title | First seen | Fix layer |
|---|---|---|---|
| G26 | Postgres `POSTGRES_PASSWORD=changeme`, Valkey `requirepass=""` | Phase 4 RT-4.5 | Helm chart (generate at install time) |
| RT-2.18 / RT-6.10 | `Spec.Runtime` accepts arbitrary registry refs | Phase 1, re-confirmed Phase 2 + Phase 6 | Validating webhook |

### High (8)

| ID | Title | First seen | Fix layer |
|---|---|---|---|
| G25 | Secret `value` field logged unredacted | Phase 4 RT-4.2 | API middleware |
| G18 (dormant) | `/auth/logout` doesn't call `RevokeToken` | Phase 4 RT-4.13 | Logout handler |
| G28 | Workspace bind handler is a no-op for first-time delivery | Phase 4 RT-4.3 | API SecretsHandler |
| RT-1.6 | API SA `pods/exec` without label selector | Phase 1 | Controller code paths |
| RT-1.7 | API keys stored cleartext in DB | Phase 1 | Schema + handler |
| RT-1.4 | Plain HTTP secret reload between API and agentd | Phase 1 | Service mesh / mTLS |
| G14 | No egress request body inspection (accepted) | Phase 1 | Documented residual |
| RT-6.1 | Webhook accepts traversal/giant-storage spec | Phase 6 | Validating webhook |

### Medium (12)

G1, G4, G5, G6, G8, G9, G11, G13, G14*, G19, G21, G23, G27, G29, G30, G31 (some overlap with the rolled-up High table; full list in THREAT-MODEL.md §5).

### Low (4+)

G7 (accepted), G10, G12, G22, G24, G32 (accepted), RT-7.1, RT-3.7-when-mitigated.

---

## Pre-pentest fixes — verification status

| Gap | Pre-fix state | Code-level fix | Live verification |
|---|---|---|---|
| **G2** | Bash entrypoint shell injection in secret values | `pkg/agentd/secrets` Go package; bash entrypoint reduced 224→35 lines | Phase 4 RT-4.3 **could not exercise** end-to-end because of G28 (bind no-op); G2 is held by 26 in-tree tests (mutation-validated) |
| **G16** | No NetworkPolicy ships | Chart adds workspace-default-deny-ingress + workspace-egress | Phase 3 RT-3.1, RT-3.8, RT-3.9 + Phase 6 RT-6.12 confirmed live ✅ |
| **G17** | `automountServiceAccountToken` defaulted to true | `controller.go:670` sets `false` | Phase 3 RT-3.2 confirmed live: `/var/run/secrets/kubernetes.io/` absent + spec field `false` ✅ |
| **G18** | RevokeToken cache key mismatch | RevokeToken writes both `token:<hash>` and `token:<jti>` | **Phase 4 RT-4.13 confirmed FAIL**: `/auth/logout` doesn't call RevokeToken; fix is dormant. Status downgraded. |
| **G20** | `/tmp/agent-config.json` mode race | `pkg/agentd/secrets` uses atomic `O_CREATE\|0o600` | Phase 3 RT-3.16 found a **separate** issue (G21 — `/sandbox-cfg/password` mode 0644 because init-script `cp` preserves source 0644). G20's specific path holds but the credential-init init-container has a parallel mode bug. |

**Two of the five "fixed" gaps need follow-up:**
- G18: wire `RevokeToken` into the logout handler.
- G20-adjacent: replace `cp` with `install -m 0600` in the init-container `credScript` (G21).

---

## New gaps surfaced by the pentest (G21–G32)

| ID | Severity | One-line | Phase |
|---|---|---|---|
| G21 | Medium | `/sandbox-cfg/password` mode 0644 (init-script `cp`) | 3 RT-3.16 |
| G22 | Low | `enableServiceLinks: true` leaks namespace topology | 3 RT-3.3 |
| G23 | Medium | `/workspace` PVC mount lacks `nosuid` | 3 RT-3.5 |
| G24 | Low | No `seccompProfile` on workspace pod | 3 RT-3.7 |
| G25 | High | Secret `value` field logged unredacted | 4 RT-4.2 |
| G26 | Critical | Default Postgres password + open Valkey | 4 RT-4.5 |
| G27 | Medium | Login timing reveals registered emails | 4 RT-4.10 |
| G28 | High | Bind handler no-op for first-time secret delivery | 4 RT-4.3 |
| G29 | Medium | Path-traversal mount_path accepted by API | 4 RT-4.4 |
| G30 | Medium | Egress NetPol allows external DNS resolvers | 5 RT-5.7 |
| G31 | Medium | Frontend ingress lacks CSP / XFO | 7 RT-7.13 |
| G32 | Low | No workspace quota (single-tenant accepted) | 7 RT-7.1 |

All entered into [THREAT-MODEL.md §5](./THREAT-MODEL.md#5-identified-gaps--residual-risks) revision 1.3.

---

## Recommended remediation queue (priority order)

**P0 — Critical (within 1 sprint):**
1. **G26** — Generate postgres + redis passwords at chart install; add NetworkPolicies for postgres + valkey.
2. **RT-1.2 / RT-2.18 / RT-6.10** — Add registry allow-list validation to the validating webhook for `Spec.Runtime`.
3. **G18 (dormant)** — Wire `authSvc.RevokeToken(token)` into `/api/v1/auth/logout` handler. 5 LoC + 1 regression test.

**P1 — High (within 2 sprints):**
4. **G25** — Route logged request bodies through `pkg/redact` OR add `"value"` to the SensitiveFields list AND audit all credential field names. Disable body-logging on `/api/v1/secrets/*`.
5. **G28** — Investigate why bind PUT silently no-ops; restore K8s-Secret manifest write + agent reload.
6. **RT-1.6** — Add label-selector to controller's pod-exec calls (prevent cross-tenant exec).
7. **RT-1.7** — Hash API keys in DB (currently cleartext).

**P2 — Medium (within 1 quarter):**
8. **G5** — Flip Helm default `rbac.scope: namespace`; document opt-in cluster scope.
9. **G11** — Add `pod-security.kubernetes.io/enforce: restricted` to namespace template.
10. **G27** — Dummy bcrypt verify on no-such-user path.
11. **G29** — API-layer `mount_path` validation (mirror agentd's check).
12. **G30** — Cilium FQDN policies for sandbox egress.
13. **G31** — Add CSP + XFO to frontend ingress.
14. **G19** — Pin mise tarball SHA at build time; enable attestations.
15. **G21, G23** — `install -m 0600`; add nosuid mount option to PVC StorageClass.
16. **G13 + G27 combined** — IP-based throttling instead of email-keyed lockout.

**P3 — Low / accepted:**
17. **G24** — `seccompProfile: RuntimeDefault` (defence-in-depth).
18. **G22** — `EnableServiceLinks: false` (one-line recon-leak fix).
19. **G32** — Workspace quota for SaaS deployments.

---

## Methodology notes (lessons learned)

### Assumption-first discipline paid off
Every phase began with explicit assumptions stated up-front, validated against the live cluster *before* test execution. **Every refuted assumption produced a finding or test correction**:
- A6 in Phase 3: "sandbox runs as uid 1001" → REFUTED, actual uid 1000 (plan was wrong; corrected and tests adjusted).
- A8 in Phase 4: "register issues a usable token" → PARTIALLY REFUTED (register doesn't cache DEK; only login does). Forced register-then-login pattern.
- A5 in Phase 7: "frontend ingress has CSP" → REFUTED → G31.
- A6 in Phase 7: "API has workspace-quota middleware" → REFUTED → G32.

### Mutation-validation caught harness false-positives
Three harness-side bugs were caught and fixed mid-run:
- **RT-3.6** initially "FAIL" because perl bind-to-port-80 succeeded — but `CapEff=0`. Root cause: `ip_unprivileged_port_start=0` (sysctl, not capability). Test rewritten to read `/proc/self/status` for the authoritative cap mask. Result became PASS.
- **RT-3.3** initially flagged `OPENCODE_SERVER_PASSWORD` as a leaked credential. Root cause: same-trust password (the attacker is already in the pod). Reclassified to a SAME_TRUST allowlist; real recon leak (service-link env vars, G22) surfaced separately.
- **RT-5.3** initially "FAIL critical (smuggling)" because two HTTP/1.1 responses appeared. Root cause: HTTP pipelining — Go's net/http correctly handled CL-TE (TE wins) and processed the trailing bytes as a new pipelined request. PASS, with note to retest if an intermediate proxy is added.

This is **mutation-validation applied to the harness itself**: every interpretation must be supported by observable evidence. A test that produces FAIL in unrealistic conditions is a bug in the test.

### Live-cluster vs unit-test split
Some tests genuinely cannot be black-box exercised:
- **RT-5.12, RT-5.14**: `stripPatchParts` runs on opencode RESPONSES, not user requests. Need pkg/proxy unit fuzz.
- **RT-7.9**: full XSS bypass corpus needs jsdom + ReactMarkdown render. The 11-payload corpus is emitted to `phase-7/evidence/RT-7.9-xss-corpus.json` for a follow-up vitest.
- **RT-3.11 (fork bomb)**: node OOM blast-radius too high for a real cluster. Quarantined to the kind kit at `phase-0/`.
- **RT-3.15 (node disk forensics)**: requires node-shell access; off-scope per "default + pentest-* namespaces only" rule.

These are honestly recorded as INCONCLUSIVE/SKIP with explicit follow-up actions, not silently dropped.

### Cleanup discipline
All `@pentest.local` users + workspaces deleted at end of each phase. Phase 2 left 42 stale users which Phase 3 surfaced and cleaned. Final cluster state: **0 pentest users, 0 pentest workspaces** in DB (verified post-Phase-7).

---

## Artefacts

- `design/stories/epic-17-security-review/phase-{1,1-postfix,2,3,4,5,6,7}/` — 96 RT-x.y per-test JSON evidence files + per-phase findings.md + per-phase harness scripts
- `design/stories/epic-17-security-review/phase-1/artefacts/` — CycloneDX SBOM (287 components), govulncheck CVE report (56 reachable), attack-surface inventory
- `design/stories/epic-17-security-review/THREAT-MODEL.md` — gaps G1-G32, assumptions A1-A10, STRIDE, revision history v1.3
- `worklogs/0078,0082,0083,0084,0085,0086,0087,0088,0089,0090,0091,0092*.md` — full chronological narrative

---

## Sign-off readiness

The pentest is **complete by plan coverage** — every RT-x.y in the plan got a verdict (PASS / FAIL / INCONCLUSIVE / SKIP with reason). The remediation queue above is what blocks "production-ready" status. None of the open findings make the system catastrophically unsafe for the **current single-tenant home deployment** of the founder's account, but G26 (datastore credentials) is unsuitable for any deployment beyond that.
