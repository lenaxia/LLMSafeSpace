# Epic 17 — Master Remediation Tracker

**Started:** 2026-05-30
**Owner of secrets-mgmt findings:** other agent (worklog 0094)
**Owner of all other findings:** mikekao + opencode (this tracker)

This file is the single source of truth for "what is left to fix" after
Phase 7. Every Gxx, Fx.y.z, and RT-x.y entry below corresponds to a
verbatim row in REPORT.md or one of the per-phase findings.md files.
Validators: cross-check `Origin` against the cited file before trusting
the row.

---

## Classification key

| Class | Meaning |
|---|---|
| **MINE** | This tracker's owner fixes it. |
| **OTHER** | Belongs to the other agent's secrets-mgmt branch (worklog 0094). Off-limits per direct user instruction. |
| **DONE** | Already fixed in committed code; verification only. |
| **OPERATOR** | Operator-supplied configuration (etcd encryption, ingress TLS, etc.). Document and add a chart guard if cheap. |
| **OUT** | Out-of-scope per pentest plan (e.g. node-shell required, kind-only, upstream binary internals). |

---

## Top-line gaps (THREAT-MODEL §5)

| ID | Title | Severity | Class | Origin file:line |
|---|---|---|---|---|
| G1 | No `noexec` on emptyDir mounts | Medium | MINE | `controller/internal/workspace/controller.go:630-632` |
| G2 | Entrypoint shell injection via secret values | High | DONE | Fixed worklog 0078; live-test requires G28 fix → wait on OTHER |
| G3 | env-secret readable via /proc/self/environ | Medium | OTHER (secrets-mgmt) | `entrypoint-opencode.sh:14` |
| G4 | No mTLS between API and sandbox pods | Medium | MINE | `api/internal/handlers/proxy.go:91-95` |
| G5 | Controller SA cluster-wide Secret access (default) | High | MINE | `charts/llmsafespace/templates/rbac.yaml` |
| G6 | No rate limiting on sensitive secret endpoints | Medium | OTHER (touches `/secrets/*`) | `api/internal/server/router.go:171-180` |
| G7 | SSE bypasses injection-detection | Low | MINE (proxy) | `api/internal/handlers/proxy.go` |
| G8 | First-user-admin auto-promotion race | Medium | MINE | `api/internal/services/auth/auth.go:386-394` |
| G9 | No image signature verification | Medium | MINE | `runtimes/base/Dockerfile:67-78` |
| G10 | Redis session cache not encrypted at rest | Low | OPERATOR + chart docs | `charts/llmsafespace/values.yaml` |
| G11 | No PSA enforcement | Medium | MINE | `charts/llmsafespace/templates/namespace.yaml` |
| G12 | Proxy ResponseHeaderTimeout 300s | Low | MINE | `api/internal/handlers/proxy.go:95` |
| G13 | Account lockout DoS | Medium | MINE (auth, not secrets) | `api/internal/services/auth/auth.go:440-512` |
| G14 | No egress request body inspection | High | MINE (proxy) | accepted residual; doc only |
| G15 | Sandbox emptyDir disk-backed | High | OTHER (secrets-mgmt) | `controller/internal/workspace/controller.go:630-632` |
| G16 | NetworkPolicy templates ship | Critical | DONE | worklog 0078 |
| G17 | AutomountServiceAccountToken false | High | DONE | worklog 0078 |
| G18 | JWT revocation dormant — `/auth/logout` doesn't call RevokeToken | High | MINE | `api/internal/api/router.go:330-333` |
| G19 | mise no checksum verification | Medium | MINE | `runtimes/base/Dockerfile:119-130` |
| G20 | Credential files written without atomic mode 0600 | Medium | DONE | worklog 0078 |
| G21 | `/sandbox-cfg/password` mode 0644 (init `cp` preserves source) | Medium | OTHER (secrets-mgmt) | `controller/internal/workspace/controller.go:733-738` |
| G22 | `enableServiceLinks: true` leaks service env vars | Low | MINE | `controller/internal/workspace/controller.go` pod template |
| G23 | `/workspace` PVC mount lacks nosuid | Medium | MINE | StorageClass mountOptions |
| G24 | No seccompProfile | Low | MINE | `controller/internal/workspace/controller.go` PodSecurityContext |
| G25 | Secret `value` field logged unredacted | High | OTHER (secrets-mgmt logging path) | `api/internal/middleware/logging.go:54` |
| G26 | Postgres `changeme` + Valkey empty `requirepass` | Critical | MINE | Helm chart secret + NetPols |
| G27 | Login response timing reveals registered emails | Medium | MINE | `api/internal/services/auth/auth.go` |
| G28 | Bind handler no-op for first-time secret delivery | High | OTHER (secrets-mgmt) | `api/internal/handlers/secrets.go:307-356` |
| G29 | Path-traversal mount_path accepted by API | Medium | OTHER (secrets-mgmt) | `pkg/secrets/secret_service.go` validateMountPath |
| G30 | Egress NetPol allows external DNS resolvers | Medium | MINE | `charts/llmsafespace/templates/workspace-network-policy.yaml` |
| G31 | Frontend ingress lacks CSP/XFO | Medium | MINE | frontend ingress template |
| G32 | No per-user workspace quota | Low | MINE | workspace-create handler |

**MINE count:** 22. **OTHER count:** 8. **DONE count:** 4.

---

## RT-x.y / Fx.y.z items NOT already covered by a Gxx above

Many RT items are duplicates of Gxx (e.g. RT-3.16 = G21, RT-4.4 = G29). Below
is the list of distinct items.

### From Phase 1 reconnaissance (Fx.y.z)

| ID | Title | Severity | Class | Notes |
|---|---|---|---|---|
| F1.1.1 | `/readyz` leaks driver error strings | Medium | MINE | API readyz handler |
| F1.1.2 | `:sessionId` path traversal upstream | High | MINE | proxy session-id sanitisation |
| F1.1.3 | `/metrics` unauthenticated | Medium | MINE | metrics middleware |
| F1.1.4 | `/api/v1/workspaces/:id/stream` group has middleware but no handler | Low | MINE | router cleanup |
| F1.1.5 | `/api/v1/account/recover` outside `/account` auth group | Medium | MINE | router |
| F1.1.6 | `/events` SSE rate-limit-exempt | Low/medium | MINE | router |
| F1.1.7 | Login error messages enable user enumeration | Medium | duplicate of G27 | merged |
| F1.2.1 | Spec.Runtime arbitrary image pull | Critical | MINE | webhook validator (= RT-2.18, RT-6.10) |
| F1.2.2 | Status.PodIP/PodName forge → SSRF + pod-exec hijack | Critical | MINE | webhook subresource validation |
| F1.2.3 | Spec.Resources.* not applied to pod | High | MINE | controller pod-spec generation |
| F1.2.4 | Spec.NetworkAccess not enforced | High | MINE | NetPol generation per workspace |
| F1.2.5 | Spec.Packages[].Requirements[] shell injection | High | MINE | controller package init container |
| F1.2.6 | autoApprovePermissions schema drift | Medium | MINE | CRD schema |
| F1.2.7 | Helm CRD drift | Medium | MINE | chart vs upstream CRD |
| F1.2.8 | Spec.PodSecurityContext.SeccompProfile ignored | Medium | MINE | controller (relates G24) |
| F1.2.9 | Spec.Storage.StorageClassName no allowlist | Medium | MINE | webhook validator |
| F1.2.10 | RuntimeEnvironment.Spec.Image validated only for non-empty | Low/Medium | MINE | webhook validator |
| F1.3.1 | Controller cluster-scope grants 5 unused permissions | High | MINE | `charts/llmsafespace/templates/rbac.yaml` (relates G5) |
| F1.3.2 | `coordination.k8s.io/leases` cluster-wide | High | MINE | rbac.yaml |
| F1.3.3 | secrets/pods cluster-wide vs controller.watchNamespaces intent | Medium | MINE | rbac.yaml |
| F1.3.4 | `runtimeenvironments` full CRUD granted to API SA but unused | Medium | MINE | rbac.yaml |
| F1.3.5 | `pods/log` granted to API SA but unused | Medium | MINE | rbac.yaml |
| F1.3.6 | `pods/exec` in workspace ns extends to non-sandbox pods | High | MINE | rbac.yaml resourceNames or label selector |
| F1.3.7 | `storageclasses` grant degrades silently in namespace mode | Low | MINE | rbac.yaml |
| F1.4.1 | Zero L3/L4 isolation between tenants | High | DONE | G16 closed; verified post-fix RT-1.4 |
| F1.4.2 | agentd `/v1/statusz` and `/v1/healthz` unauthenticated | High | MINE | `pkg/agentd/...` health endpoints |
| F1.4.3 | Controller `/metrics` unauthenticated | Medium | MINE | controller metrics server |
| F1.4.4 | Workspace egress allow-list is wide | Medium | duplicate of G30 | merged |
| F1.7.1 | Decrypted user secrets transmitted over plain HTTP | Critical | duplicate of G4 (mTLS) | merged |
| F1.7.2 | API key stored cleartext in DB; full bearer as Redis key | Critical | OTHER (secrets-mgmt key handling) | hash + non-bearer-key |
| F1.7.3 | Sandbox pod password never rotated, cleartext at rest | High | OTHER (secrets-mgmt) | password lifecycle |
| F1.7.4 | Credential-set encryption key random dev fallback breaks data on restart | High | OTHER (secrets-mgmt) | KEK init |
| F1.7.5 | JWT secret never rotated by design | High | MINE (auth) | A8 in threat model |
| F1.7.6 | Ephemeral user-secrets K8s Secret lingers after pod boot | Medium | OTHER (already fixed in 0094 cleanup of Failed-phase Secrets) | verify |
| F1.7.7 | `user_secret_bindings.workspace_id` has no FK; orphans | Medium | OTHER (already fixed in 0094 MarkWorkspaceDeleted purge) | verify |
| F1.7.8 | secret_audit_log unbounded retention | Medium | OTHER (secrets-mgmt schema) | retention policy |
| F1.7.9 | Recovery key no rotation/disclosure record | Medium | OTHER (secrets-mgmt) | rotation primitive |

### From Phase 2 (auth + authorisation)

| ID | Title | Severity | Class | Notes |
|---|---|---|---|---|
| RT-2.4 | API key brute-force resistance: 0 rate-limited in 200 reqs | Medium | MINE (auth/middleware) | new test + middleware |
| RT-2.5 | Registration rate limiting: 10/10 succeeded | INCONCLUSIVE→Medium | MINE | per-IP throttle |
| RT-2.6 | Account lockout DoS via email-keyed lockout | Medium | duplicate of G13 | merged |
| RT-2.13 | JWT revocation feature unreachable | Medium | duplicate of G18 | merged |
| RT-2.14 | No JWT signing-key rotation | Medium | duplicate of F1.7.5 | merged |
| RT-2.17 | `/api/v1/account/recover` no rate limit | Medium | OTHER (secrets-mgmt — recovery flow) | per-IP throttle |
| RT-2.18 | Spec.Runtime arbitrary image pull | CRITICAL | duplicate of F1.2.1 | merged |
| RT-2.7 | First-user-admin race needs clean DB | SKIP→re-run | MINE | requires ephemeral DB test |
| RT-2.10 | Cookie fixation path | INCONCLUSIVE | MINE | needs targeted unit test |
| RT-2.16 | `:sessionId` upstream traversal | INCONCLUSIVE | duplicate of F1.1.2 | merged |

### From Phase 3 (sandbox isolation)

| ID | Title | Severity | Class | Notes |
|---|---|---|---|---|
| RT-3.3 | enableServiceLinks: true | Low | duplicate of G22 | merged |
| RT-3.4 | Sandbox /tmp writable AND exec-allowed | High | duplicate of G1 | merged |
| RT-3.5 | /workspace mount missing nosuid | Medium | duplicate of G23 | merged |
| RT-3.7 | No seccomp profile | Low | duplicate of G24 | merged |
| RT-3.11 | Resource exhaustion (fork bomb) | OUT (kind-only quarantined) | OUT | `phase-0/` kind kit |
| RT-3.15 | Plaintext secrets on node disk | OUT (node-shell required) | OUT | doc only |
| RT-3.16 | /sandbox-cfg/password mode 0644 | Medium | duplicate of G21 | merged (OTHER) |
| RT-3.17 | Mise install path writable from sandbox | Medium | MINE | controller / runtime image |

### From Phase 4 (credential & crypto)

| ID | Title | Severity | Class | Notes |
|---|---|---|---|---|
| RT-4.2 | Secret value in logs | High | duplicate of G25 | OTHER |
| RT-4.3 | Bind no-op | High | duplicate of G28 | OTHER |
| RT-4.4 | Path-traversal mount_path | Medium | duplicate of G29 | OTHER |
| RT-4.5 | Redis credentials extraction | Critical | duplicate of G26 | MINE |
| RT-4.6 | Wrapped-DEK structure | INCONCLUSIVE | OTHER | needs DB inspection unit test |
| RT-4.9 | Redaction DoS | INCONCLUSIVE | MINE (pkg/redact) | needs fuzz test |
| RT-4.10 | Login timing leak | Medium | duplicate of G27 | merged |
| RT-4.11 | Recovery-key entropy | INCONCLUSIVE | OTHER | unit test |
| RT-4.12 | DEK lifecycle on workspace delete | INCONCLUSIVE | OTHER | secrets-mgmt |
| RT-4.13 | Token revocation broken | High | duplicate of G18 | merged |
| RT-4.15 | Mise binary integrity | Medium | duplicate of G19 | merged |

### From Phase 5 (proxy & network)

| ID | Title | Severity | Class | Notes |
|---|---|---|---|---|
| RT-5.4 | SSE injection | INCONCLUSIVE | MINE (proxy) | needs fuzz |
| RT-5.5 | Connection limit not observed in 15-burst test | INCONCLUSIVE | MINE (proxy) | conn-limit middleware |
| RT-5.6 | Stale pod IP exploitation | INCONCLUSIVE | MINE (proxy) | integration test |
| RT-5.7 | Sandbox can resolve via 8.8.8.8 | Medium | duplicate of G30 | merged |
| RT-5.9 | MCP transport injection | INCONCLUSIVE | MINE (mcp) | unit test |
| RT-5.11 | Plain HTTP proxy | Low | duplicate of G4 | merged |
| RT-5.12 | stripPatchParts DoS | INCONCLUSIVE | MINE (pkg/proxy) | fuzz test |
| RT-5.13 | ResponseHeaderTimeout=300s | Low | duplicate of G12 | merged |
| RT-5.14 | stripPatchParts unexpected-shape handling | INCONCLUSIVE | MINE (pkg/proxy) | unit test |

### From Phase 6 (k8s & infra)

| ID | Title | Severity | Class | Notes |
|---|---|---|---|---|
| RT-6.1 | Webhook accepts traversal/giant-storage spec | High | duplicate of F1.2.9, F1.2.1 | merged |
| RT-6.2 | Controller SA cluster-bound by default | Medium | duplicate of G5 | merged |
| RT-6.6 | No preflight Job for etcd encryption check | Low | MINE | helm chart preflight |
| RT-6.10 | Webhook accepts arbitrary registry | Critical | duplicate of F1.2.1 | merged |
| RT-6.11 | Default ns lacks PSA `enforce: restricted` | Medium | duplicate of G11 | merged |
| RT-6.13 | No Helm preflight for CNI/etcd assumptions | Low | duplicate of RT-6.6 | merged |
| RT-6.14 | Ingress TLS not enabled by default | Low | MINE | values.yaml |
| RT-6.16 | Helm default rbac.scope=cluster | Medium | duplicate of G5 | merged |
| RT-6.9 | Lease integrity | INCONCLUSIVE | OUT (controller-SA forge) | doc only |

### From Phase 7 (app logic + frontend)

| ID | Title | Severity | Class | Notes |
|---|---|---|---|---|
| RT-7.1 | No quota | Low | duplicate of G32 | merged |
| RT-7.9 | XSS bypass corpus needs vitest | INCONCLUSIVE | MINE (frontend) | new vitest |
| RT-7.13 | Frontend ingress lacks CSP/XFO | Medium | duplicate of G31 | merged |
| RT-7.15 | DEK race on workspace delete | INCONCLUSIVE | OTHER (secrets-mgmt) | merged with RT-4.12 |

---

## My execution order (Phase C)

Per user directive: per-finding fix → test → validator → commit+push → live re-pentest → next.

**Group 1 — Critical (must close first):**
1. **G26** — Helm chart datastore credentials + NetPols. (RT-4.5 dup)
2. **F1.2.1 / F1.2.2 / RT-2.18 / RT-6.10 / RT-6.1** — webhook validation for `Spec.Runtime`, `Spec.Status` subresource forgery, storage-class allowlist, traversal in spec. (Single webhook PR closes 5 findings.)

**Group 2 — High (one logical area each):**
3. **G18** — wire `RevokeToken` into logout handler.
4. **F1.2.3, F1.2.4, F1.2.5** — controller pod-spec / NetPol-per-workspace / package init container hardening.
5. **F1.3.1–F1.3.7** — RBAC tightening (single chart-rbac PR closes 7 rows + G5).
6. **F1.1.2 / RT-2.16** — `:sessionId` proxy path traversal.
7. **F1.4.2** — agentd healthz/statusz auth.
8. **F1.7.5** — JWT signing-key rotation.

**Group 3 — Medium:**
9. **G27** — login timing constant-time bcrypt.
10. **G13** — account-lockout key on IP not email.
11. **G8** — first-user-admin race.
12. **G11** — PSA enforce: restricted.
13. **G19** — mise checksum / attestations.
14. **G23** — PVC mountOptions nosuid,nodev.
15. **G30** — Cilium FQDN egress (or doc operator switch).
16. **G31** — frontend ingress CSP/XFO.
17. **F1.1.1** — readyz leaks driver errors.
18. **F1.1.3 / F1.4.3** — `/metrics` auth.
19. **F1.1.5** — `/account/recover` auth.
20. **F1.1.6** — `/events` SSE rate-limit.
21. **F1.2.6 / F1.2.7 / F1.2.8 / F1.2.10** — webhook schema-drift fixes (single PR).
22. **RT-2.4 / RT-2.5** — per-IP throttle on api-key validation + register.
23. **RT-3.17** — mise install path writability.
24. **G22** — enableServiceLinks: false.
25. **G24** — seccompProfile RuntimeDefault.
26. **G32** — workspace quota (env-driven).

**Group 4 — Low:**
27. **G1** — emptyDir noexec / Memory backing.
28. **G7** — SSE injection-detection.
29. **G12** — proxy ResponseHeaderTimeout per-route.
30. **F1.1.4** — stream group cleanup.
31. **F1.3.7** — storageclasses grant.
32. **G14** — egress request body inspection (accepted residual; doc only).
33. **RT-6.6 / RT-6.13 / RT-6.14** — chart preflight + TLS default.
34. **G10** — Redis at-rest doc.

**Group 5 — Inconclusive → measured PASS/FAIL:**
35. **RT-2.7 / RT-2.10** — clean-DB test.
36. **RT-4.9** — redaction DoS fuzz.
37. **RT-5.4 / RT-5.5 / RT-5.6 / RT-5.9 / RT-5.12 / RT-5.14** — proxy / MCP fuzz suite.
38. **RT-7.9** — XSS bypass vitest.

**Group 6 — Validation only (DONE / verified):**
39. G2, G16, G17, G20 — re-confirmed live each pentest cycle.

---

## Live re-pentest cycle (per fix)

After each commit lands and CI builds the new image:
1. `kubectl --context admin@home-kubernetes -n default rollout restart deploy/<component>` (for chart changes use `helm upgrade`).
2. Re-run the relevant phase harness (`phase-X/run-phaseX.py`) targeted to the fix, OR a focused targeted test for the specific RT-x.y.
3. Confirm previously-FAIL test now PASSes.
4. Confirm no regression in previously-PASS tests for the same phase.
5. Update this tracker: tick the box, link the test evidence path.

---

## Status

| Group | Total | DONE | OTHER | OUT | OPERATOR | TODO (mine) |
|---|---|---|---|---|---|---|
| Top-line G1-G32 | 32 | 4 (G2, G16, G17, G20) | 8 (G3, G6, G15, G21, G25, G28, G29, + verify G6) | 0 | 1 (G10) | 19 |
| Phase-1 F1.x.y | 38 | 1 (F1.4.1) | 6 (F1.7.1-F1.7.4 OTHER overlap, F1.7.6-F1.7.9 OTHER) | 0 | 0 | 31 |
| Phase-2 RT-2.x | 10 distinct | 0 | 1 (RT-2.17) | 0 | 0 | merged or 4 (RT-2.4, 2.5, 2.7, 2.10) |
| Phase-3 RT-3.x | 8 distinct | 0 | 1 (RT-3.16=G21) | 2 (RT-3.11, RT-3.15) | 0 | merged or 1 (RT-3.17) |
| Phase-4 RT-4.x | 11 distinct | 0 | 6 | 0 | 0 | merged or 2 (RT-4.5=G26, RT-4.9) |
| Phase-5 RT-5.x | 9 distinct | 0 | 0 | 0 | 0 | merged or 6 (5.4/5.5/5.6/5.9/5.12/5.14) |
| Phase-6 RT-6.x | 9 distinct | 0 | 0 | 1 (RT-6.9) | 0 | merged or 2 (RT-6.6, RT-6.14) |
| Phase-7 RT-7.x | 4 distinct | 0 | 1 (RT-7.15) | 0 | 0 | merged or 1 (RT-7.9) |

**Bottom line for me to fix (this branch):** ~26 distinct logical PRs, grouped to land in ~12 commits.

---

## Revision history

| When | What |
|---|---|
| 2026-05-30 13:50 | Initial tracker built from REPORT.md + phase-{1,2,3,4,5,6,7}/findings.md + phase-1/RT-1.*. Classification: MINE/OTHER/DONE/OPERATOR/OUT verified by inspecting cited file:line. |
