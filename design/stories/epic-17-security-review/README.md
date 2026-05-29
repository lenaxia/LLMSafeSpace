# Epic 17: Security Review & Penetration Test Plan

**Status:** Pre-Pentest Remediation Complete; Phase 0 Pending
**Author:** mikekao
**Depends On:** Epics 6, 8, 10 (core platform must be functional)
**Threat Model:** [THREAT-MODEL.md](./THREAT-MODEL.md)
**Pre-Pentest Remediation:** [worklog 0078](../../../worklogs/0078_2026-05-29_epic17-pre-pentest-remediation.md) — closes G2, G16, G17, G18, G20

---

## Objective

Conduct a red-team security assessment of LLMSafeSpace covering all trust boundaries, credential flows, sandbox isolation, multi-tenant separation, and frontend rendering. Produce actionable findings with severity ratings, remediation guidance, and automated regression tests for every Critical/High finding.

This epic is structured as a Validator-Loop epic per `README-LLM.md` §Multi-Agent Workflow: every finding goes through skeptical validator → triage → remediation → re-validation cycles until zero real findings remain.

---

## Scope

### In Scope

| Layer | Components | Focus |
|-------|-----------|-------|
| API Server | Auth, proxy, handlers, middleware, MCP server | AuthN/AuthZ bypass, injection, SSRF, IDOR, revocation |
| Controller | Reconcilers, pod spec generation, RBAC, finalizers | Privilege escalation, CRD manipulation, leaked SA tokens |
| Sandbox Runtime | Entrypoints, credential materialization, opencode, agentd, mise | Container escape, credential theft, network escape, supply chain |
| Frontend (browser) | React UI, markdown/code rendering, JWT storage | XSS via assistant content, CSP, clickjacking, token exfiltration |
| Infrastructure | Helm chart, RBAC, NetworkPolicy, Secrets, ingress | Misconfig, over-permissioned SA, missing policies, default-deny absence |
| Crypto | Key wrapping, JWT, password hashing, redaction | Weak crypto, key leakage, bypass, revocation correctness |
| Data Stores | PostgreSQL, Redis, etcd (K8s Secrets) | Injection, unauthorized access, data at rest |
| Build & Supply Chain | Base image, opencode binary, mise binary, Go deps | Image signing, checksum verification, SBOM, attestation |

Frontend XSS is **in-scope** for this epic (pulled in from prior plan): the agent renders LLM-generated markdown via `react-markdown` + `rehype-sanitize`, which is a realistic cross-tenant attack surface if rendering is shared across users or sanitization is bypassed.

### Out of Scope (with Mitigation Owner)

| Risk | Owner | How It's Tracked |
|------|-------|------------------|
| LLM provider security (OpenAI, Anthropic) | LLM provider | Operator selects providers; documented in deployment guide |
| opencode binary internals | upstream `anomalyco/opencode` | Pin version per release; track upstream CVEs |
| Physical/social engineering | Operator | Documented in deployment guide |
| Browser zero-days (Chrome, Firefox, Safari) | Browser vendor | Out of scope; require modern browsers |

---

## Pre-Pentest Remediation Status

Worklog 0078 closed the four "fix-before-pentest" findings identified in worklog 0077. The pentest baseline therefore measures the **post-fix** system, not the broken one. Test cases for these gaps remain in the plan below but their goal has changed from "verify the bug exists" to "verify the fix holds and exercise the regression contract".

| Gap | Status | Fix | Regression Test | Pentest Test Case |
|-----|--------|-----|-----------------|-------------------|
| G2 — entrypoint shell injection | 🟢 Fixed | `pkg/agentd/secrets` (typed Go package replaces bash secret loop); bash entrypoint reduced from 224 to 35 lines | 26 tests across `pkg/agentd/secrets/secrets_test.go` and `cmd/workspace-agentd/secrets_test.go`, including 13-payload bash-subprocess corpus | RT-4.3 (validate fix) |
| G16 — no NetworkPolicy templates | 🟢 Fixed | `charts/llmsafespace/templates/workspace-network-policy.yaml` ships default-deny ingress + egress allow-list; `networkPolicy.enabled` default flipped to `true` | 5 helm-render tests in `charts/llmsafespace/chart_test.go` | RT-6.12, RT-3.1, RT-3.8, RT-3.9, RT-5.7, RT-5.8 (validate fix) |
| G17 — SA token automount | 🟢 Fixed | `controller/internal/workspace/controller.go:670` sets `AutomountServiceAccountToken: false` | 3 tests in `controller/internal/workspace/security_test.go` | RT-3.2 (validate fix) |
| G18 — JWT revocation cache key mismatch | 🟢 Fixed | `RevokeToken` writes both `token:<hash>` and `token:<jti>`; `ValidateToken` checks both | 6 tests in `api/internal/services/auth/auth_revocation_test.go` | RT-2.13 (validate fix) |
| G20 — credential files written without atomic 0600 | 🟢 Fixed (incidental to G2 refactor) | `os.OpenFile(... O_CREATE, 0o600)` in `pkg/agentd/secrets` | `TestG20_AllFilesCreatedWithMode0600` | RT-3.16 (validate fix) |

All five remediations are mutation-validated: each fix was deliberately reverted and the regression test confirmed to fail before being marked complete.

The other 15 findings (G1, G3–G15, G19) remain open and are pentested as documented baseline behaviour. See [THREAT-MODEL.md §5](./THREAT-MODEL.md#5-identified-gaps--residual-risks) for current status of each.

---

## Red Team Methodology

### Phase 0: Environment Setup & Tooling Validation

Before any scanning begins, the test environment must be staged and tooling must be verified against a known-good fixture. Without this, recon results are unreliable.

| Task | Technique | Deliverable |
|------|-----------|-------------|
| RT-0.1 | Provision pentest cluster | Kind or Talos with Cilium/Calico CNI; documented version/CNI |
| RT-0.2 | Deploy LLMSafeSpace via Helm | Full install (API + Controller + Frontend); record image SHAs |
| RT-0.3 | Verify control fixture | Run kube-hunter against a vanilla pod; confirm expected findings; rules out false negatives |
| RT-0.4 | Provision test accounts | 3 users: admin, regular-A, regular-B (attacker); record JWTs |
| RT-0.5 | Confirm logging baseline | API audit logs, controller logs, K8s audit logs all flowing |
| RT-0.6 | Snapshot baseline | Cluster YAML dump + `helm get all` for diffing post-test |

**Exit criteria:** A control fixture has confirmed at least one expected vulnerability; logging is verified; rollback plan exists.

---

### Phase 1: Reconnaissance & Attack Surface Mapping

| Task | Technique | Target |
|------|-----------|--------|
| RT-1.1 | API endpoint enumeration | Swagger docs, route registration in `api/internal/server/router.go` |
| RT-1.2 | CRD schema analysis | `pkg/crds/*.yaml`, `controller/internal/resources/*_types.go` — identify mutable fields, kubebuilder validation |
| RT-1.3 | RBAC privilege mapping | `charts/llmsafespace/templates/rbac.yaml` (controller + api SA, both scopes) |
| RT-1.4 | Network topology mapping | Service definitions, NetworkPolicy templates (`workspace-network-policy.yaml` ships post-G16; map effective rules) |
| RT-1.5 | Dependency audit + SBOM | Run `trivy fs --format cyclonedx-json .` and `grype` against repository; record CVEs ≥ High |
| RT-1.6 | Container image analysis | `runtimes/base/Dockerfile`, `api/Dockerfile`, `controller/Dockerfile` — installed packages, SUID binaries, base image vulns via Trivy |
| RT-1.7 | Secret storage mapping | Where credentials exist at rest and in transit (etcd, Redis, PostgreSQL, files in pod) |
| RT-1.8 | Frontend asset inventory | Bundle analysis (`vite build --mode=analyze`); identify third-party JS shipped to browser |
| RT-1.9 | Build-time supply chain | Verify image digests match published SHAs; check for unsigned binary downloads in Dockerfiles |

**Phase 1 mandatory artefacts:**

1. **SBOM** in CycloneDX or SPDX format, committed to `design/stories/epic-17-security-review/artefacts/sbom.json`
2. **CVE report** with ≥ High severity items promoted to Phase-2 findings automatically
3. **Attack surface inventory** ranked by exposure and authentication gates

---

### Phase 2: Authentication & Authorization Testing

| ID | Test Case | Attack Vector | Expected Finding |
|----|-----------|---------------|------------------|
| RT-2.1 | JWT signature bypass | `alg:none`, HS256→RS256 confusion, unsigned tokens | `auth.go:283` enforces SigningMethodHMAC — should reject |
| RT-2.2 | JWT claim manipulation | Modify user_id, role, exp claims with valid signature | Should reject (signature invalid) |
| RT-2.3 | Expired token replay | Use token past exp with valid signature | Should reject |
| RT-2.4 | API key brute force | Enumerate `lsp_` prefixed keys | Rate limiting should block |
| RT-2.5 | Registration abuse | Mass account creation | Rate limiting (1/min documented) |
| RT-2.6 | Account lockout DoS | Send N failed logins for victim email | **Confirmed gap G13** — lockout is keyed on email at `auth.go:441,502`; verify exploitability and document remediation |
| RT-2.7 | First-user-admin race | Two concurrent registrations on fresh DB | **Confirmed gap G8** — no transaction at `auth.go:386-394`; race window between `CountUsers()` and INSERT; verify by spawning concurrent registers |
| RT-2.8 | Auth bypass on skip paths | Craft requests matching `/health`, `/docs/` prefixes with path traversal | Should not bypass |
| RT-2.9 | CORS misconfiguration | Cross-origin requests with credentials | Should reject (AllowedOrigins: [] default) |
| RT-2.10 | Session fixation | Reuse session_id across users | Should be bound to user |
| RT-2.11 | Password reset without recovery key | Verify secrets are wiped, not accessible | Should be irrecoverable |
| RT-2.12 | Admin role escalation | Non-admin attempts admin-only operations | Should 403 (AdminGuard middleware) |
| RT-2.13 | **JWT revocation enforcement** | Revoke a JWT via `RevokeToken`, immediately re-use it across multiple endpoints | **G18 fixed (worklog 0078)**: post-fix, `RevokeToken` writes both `token:<hash>` and `token:<jti>`, and `ValidateToken` checks both. Pentest must independently verify: (a) revocation visible to the very next request, (b) defense-in-depth path works when hash-cache entry evicted, (c) double-revoke is idempotent. Reproduce with `auth_revocation_test.go` payloads against a live cluster. |
| RT-2.14 | Long-lived JWT after credential rotation | Rotate JWT signing key, attempt to use old token | Old tokens must be rejected (currently no kid rotation — A8) |
| RT-2.15 | API key reveal in list endpoint | GET /api/v1/auth/api-keys with admin role | Confirmed in `README-LLM.md:1428` — secrets stripped on list; verify with admin token |

---

### Phase 3: Sandbox Isolation & Container Escape

| ID | Test Case | Attack Vector | Expected Finding |
|----|-----------|---------------|------------------|
| RT-3.1 | Read other pod's secrets | From sandbox pod, attempt to reach K8s API | **G16 fixed (worklog 0078)**: workspace egress NetworkPolicy denies traffic to in-cluster CIDRs by default. Pentest must independently verify with a live CNI that the K8s API is unreachable from the sandbox. |
| RT-3.2 | **SA token absence** | `cat /var/run/secrets/kubernetes.io/serviceaccount/token` and `ls /var/run/secrets/kubernetes.io/` in sandbox pod | **G17 fixed (worklog 0078)**: post-fix, `controller.go:670` sets `AutomountServiceAccountToken: false`. Pentest must independently verify the SA token directory is absent (not merely that the token file exists with empty content). Cross-check via `kubectl get pod -o yaml` for the field. |
| RT-3.3 | /proc exploration | Read /proc/1/environ, /proc/*/maps | Env vars readable (G3) |
| RT-3.4 | Write+exec in /tmp | Write binary to /tmp, chmod +x, execute | **Confirmed gap G1** — emptyDir at `controller.go:631` lacks `Medium: Memory` and noexec; expect success |
| RT-3.5 | Escape via /workspace | Write SUID binary to PVC, exec after resume | Should fail (verify nosuid mount option on PVC StorageClass) |
| RT-3.6 | Capability abuse | Attempt operations requiring dropped caps | Should fail (Drop ALL at `controller.go:616`) |
| RT-3.7 | Seccomp bypass | Attempt blocked syscalls (ptrace, mount, etc.) | Verify seccomp profile attached to sandbox pod (currently no SeccompProfile in PodSecurityContext) |
| RT-3.8 | Node metadata access | curl 169.254.169.254 from sandbox | **G16 fixed (worklog 0078)**: `blockedEgressCIDRs` includes 169.254.0.0/16. Pentest must independently verify metadata endpoint is unreachable on a real cloud K8s. |
| RT-3.9 | Cross-pod network access | From sandbox A, connect to sandbox B's pod IP:agentd port | **G16 fixed (worklog 0078)**: workspace default-deny ingress permits only the API proxy. Pentest must verify cross-sandbox traffic is denied with a live CNI. |
| RT-3.10 | DNS exfiltration | Encode data in DNS queries to external domain | Resolves (accepted risk) — verify audit logging |
| RT-3.11 | Resource exhaustion | Fork bomb, memory allocation, disk fill | Limits should contain |
| RT-3.12 | PID namespace escape | Attempt to signal processes outside pod | Should fail (PID namespace isolation) |
| RT-3.13 | Symlink escape | Create symlink in /workspace pointing to /etc/shadow | Should fail (read-only root + ReadOnlyRootFilesystem at `controller.go:613`) |
| RT-3.14 | Device access | Attempt to access /dev/kmsg, /dev/mem | Should fail (no device mounts) |
| RT-3.15 | **Plaintext secrets on node disk** | After secret materialization, dump node filesystem (kubelet pod-data dir) | **Confirmed gap G15** — emptyDir is disk-backed by default; `/sandbox-cfg/secrets.json` plaintext lands on node disk until volume is reclaimed. **High**. |
| RT-3.16 | **/tmp/agent-config.json permissions** | Spawn second process as same UID; read /tmp/agent-config.json | **G20 fixed (worklog 0078, incidental to G2 refactor)**: post-fix, `pkg/agentd/secrets` uses `os.OpenFile(... O_CREATE, 0o600)` so mode is atomic with creation. Pentest must verify `stat -c '%a' /tmp/agent-config.json` returns `600` immediately after pod boot, with no readable window. |
| RT-3.17 | **Mise-installed runtime tampering** | Modify `/workspace/.local/share/mise/installs/<runtime>/<version>/bin/python` after first install; trigger agent re-exec | mise resolves PVC-first; tampered binary survives suspend/resume cycle. Verify and document. |

---

### Phase 4: Credential & Crypto Testing

| ID | Test Case | Attack Vector | Expected Finding |
|----|-----------|---------------|------------------|
| RT-4.1 | Credential API IDOR | PUT/GET secrets for resource owned by another user | Should 403/404 |
| RT-4.2 | Secret value in logs | Trigger errors with credential operations, check API logs | Should be redacted (16-rule pkg/redact) |
| RT-4.3 | **Entrypoint shell injection neutralisation** | Set env-secret PLAINTEXT to `'; rm -rf /; echo '`, plus the full bypass corpus (`$(whoami)`, backticks, newlines, `\0`, CRLF, 4KB values) from `pkg/agentd/secrets/secrets_test.go::TestG2_EnvSecretShellInjection_Corpus` | **G2 fixed (worklog 0078)**: secret materialization moved out of bash into `pkg/agentd/secrets`. Bash entrypoint is now a 35-line shim. Pentest must independently verify against a live cluster: (a) crafted payload materializes without executing, (b) `bash -c 'source /tmp/secrets-env; echo $VAR_NAME'` returns the literal payload, (c) no side-effect commands ran (e.g. no `whoami` output, no `HIJACK` env var leak). |
| RT-4.4 | Secret file path traversal | Set mount_path to `../../etc/passwd` | `entrypoint-common.sh:67-68` strips `../`; verify exhaustively (Unicode escapes, double encoding) |
| RT-4.5 | DEK extraction from Redis | Connect to Redis from compromised API pod | Verify Redis auth. Note: workspace NetworkPolicy now denies cross-namespace traffic (G16 fixed); operator-supplied policy still required to restrict API↔Redis egress. |
| RT-4.6 | Wrapped DEK offline attack | Extract wrapped_dek from DB, attempt offline unwrap | Should require password (HKDF-derived KEK) |
| RT-4.7 | JWT signing key extraction | Check if key is in env var, config file, or hardcoded | Should be in Secret only |
| RT-4.8 | Redaction bypass | Craft credential patterns that evade all 16 regex rules in `pkg/redact/redact.go` | Document any bypass patterns; add new rules |
| RT-4.9 | Redaction DoS | Send 1MB+ payload to trigger maxInputBytes bypass | Verify large payloads pass unredacted; document threshold |
| RT-4.10 | Password hash timing attack | Measure response time for valid vs invalid usernames | Should be constant-time (bcrypt cost 12; verify identical-error path) |
| RT-4.11 | Recovery key brute force | Attempt to brute-force 128-bit recovery key | Should be infeasible (2^128) |
| RT-4.12 | **DEK lifecycle on workspace deletion** | Delete workspace; query Redis for `dek:<sessionID>` afterwards | Verify `EvictDEK` is called by workspace deletion path. Currently only on logout/expiry per `pkg/secrets/key_service.go:140-143`; trace controller finalizer to confirm |
| RT-4.13 | **DEK lifecycle on session revocation** | Revoke JWT (RevokeToken); confirm DEK evicted | **G18 fixed (worklog 0078)**: revocation now actually fires. Pentest must verify DEK eviction is wired to revocation: revoke → next request fails to decrypt secrets even if jti was previously cached. |
| RT-4.14 | **Concurrent credential rotation** | Two simultaneous `POST /api/v1/admin/credentials/rotate-key` calls | Verify atomicity; expect one to fail or both to converge to same final state |
| RT-4.15 | **mise binary tampering at build time** | Replace mise tarball URL or ARCH to point at attacker | **Confirmed gap G19** — `Dockerfile:86-98` uses `curl --fail` over TLS only; no checksum or Sigstore verification; `MISE_GITHUB_ATTESTATIONS=0` (line 120) explicitly disables attestation checks. **Medium**. |
| RT-4.16 | **opencode binary tampering at build time** | Same as RT-4.15 for opencode | **Confirmed** in `Dockerfile:67-78` — explicit comment notes upstream does not publish .sha256/Sigstore. Track upstream issue. |

---

### Phase 5: Proxy & Network Testing

| ID | Test Case | Attack Vector | Expected Finding |
|----|-----------|---------------|------------------|
| RT-5.1 | SSRF via proxy | Craft workspace that resolves to internal IP | Proxy uses CRD-reported pod IP only |
| RT-5.2 | Proxy to arbitrary port | Modify request to target port other than agentd port | Should be hardcoded |
| RT-5.3 | HTTP request smuggling | Chunked encoding / CL-TE mismatch through proxy | Gin should handle correctly |
| RT-5.4 | SSE injection | Inject SSE events into stream from sandbox | Verify stream integrity |
| RT-5.5 | Connection exhaustion | Open maxConnectionsPerWorkspace+1 connections | Should reject excess |
| RT-5.6 | Stale pod IP exploitation | Race condition: pod deleted, IP reassigned, proxy connects to wrong pod | Verify retry logic + ownership |
| RT-5.7 | NetworkPolicy bypass | Use allowed DNS to resolve attacker domain, exfiltrate | **G16 fixed (worklog 0078)**: chart now ships default-deny + egress allow-list with DNS narrowed to kube-dns. Pentest must attempt: (a) DNS rebinding, (b) DNS tunneling via TXT records, (c) bypass via allowed CIDRs. |
| RT-5.8 | Egress to kube-apiserver | From sandbox, attempt HTTPS to kubernetes.default.svc | **G16 fixed (worklog 0078)**: workspace egress denies in-cluster CIDRs (RFC1918 in `blockedEgressCIDRs`). Pentest must verify kube-apiserver is unreachable. |
| RT-5.9 | MCP transport injection | Malformed MCP messages via stdio/SSE | Should reject gracefully |
| RT-5.10 | WebSocket upgrade abuse | Attempt WebSocket upgrade on non-WebSocket endpoints | Should reject |
| RT-5.11 | **Plain-HTTP proxy MITM** | From within cluster network, MITM API→sandbox traffic | **Confirmed gap G4** — `api/internal/handlers/proxy.go:91-95` uses plain HTTP. Document residual risk; recommend service mesh. |
| RT-5.12 | **stripPatchParts JSON parser DoS** | Send response with deeply nested arrays / huge strings to opencode that flow back through `stripPatchParts` (`proxy.go:519`) | Standard `encoding/json` does not enforce depth/size limits. Verify behaviour with 100MB response and 10000-deep nesting. |
| RT-5.13 | **Proxy header timeout exhaustion** | Open many slow-response connections | **Confirmed gap G12** — `ResponseHeaderTimeout: 300s` at `proxy.go:95` is excessive; verify worker exhaustion under load |
| RT-5.14 | **verbose=true filter bypass** | Craft response that confuses `stripPatchParts` (e.g., `parts` field nested under unexpected key) | Verify filter fails-safe (returns original on parse error per `README-LLM.md:1551`) |

---

### Phase 6: Kubernetes & Infrastructure Testing

| ID | Test Case | Attack Vector | Expected Finding |
|----|-----------|---------------|------------------|
| RT-6.1 | CRD manipulation | Create Workspace CRD with malicious spec (huge resources, bad image, init scripts) | Webhook validation should reject |
| RT-6.2 | Controller SA abuse | If controller SA token leaked, what can attacker do? | **Cluster-wide blast radius** with default `rbac.scope: cluster` — confirmed at `rbac.yaml:1-95`. Document. |
| RT-6.3 | API SA abuse | If API SA token leaked, what can attacker do? | Namespace-scoped Secret + Pod CRUD per `rbac.yaml:101-118` — workspace namespace blast radius |
| RT-6.4 | Webhook bypass | Delete ValidatingWebhookConfiguration, create bad CRD | Requires cluster-admin (accepted) |
| RT-6.5 | Helm values injection | Malicious values in Helm install | Template injection in YAML |
| RT-6.6 | etcd Secret exposure | Verify etcd encryption at rest is configured | **Confirmed unvalidated assumption A1** — operator responsibility; chart has no preflight check. Add chart guard. |
| RT-6.7 | PVC cross-mount | Create pod that mounts another workspace's PVC | RWO + controller ownership should prevent |
| RT-6.8 | Namespace escape | From workspace namespace, access system namespace resources | RBAC should prevent |
| RT-6.9 | Leader election poisoning | Create fake lease to disrupt controller | Requires SA permissions |
| RT-6.10 | Image pull from untrusted registry | Modify RuntimeEnvironment to point to attacker image | Webhook validation should restrict (verify restriction exists) |
| RT-6.11 | **PSA enforcement absence** | Deploy chart, attempt to schedule a privileged pod in workspace namespace | **Confirmed gap G11** — no `pod-security.kubernetes.io/enforce` label set in `charts/llmsafespace/templates/namespace.yaml`; privileged pods not blocked at admission. |
| RT-6.12 | **NetworkPolicy enforcement** | Deploy chart with default values; verify `workspace-default-deny-ingress` and `workspace-egress` NetworkPolicies render and apply | **G16 fixed (worklog 0078)**: chart now ships `workspace-network-policy.yaml`; `networkPolicy.enabled` default is `true`. Pentest must independently verify against a live cluster with a real CNI: (a) NetworkPolicies are present (`kubectl get netpol`), (b) cross-tenant pod-to-pod traffic blocked, (c) DNS works, (d) operator opt-out via `networkPolicy.enabled: false` removes them. |
| RT-6.13 | **Helm chart guard preflight** | Deploy chart on cluster missing CNI / etcd encryption | No preflight; chart succeeds silently — assumption A2/A1 unvalidated |
| RT-6.14 | **Default-on TLS at ingress** | Helm install with default values; check ingress TLS config | **Confirmed assumption A4 partial** — `values.yaml` defaults `tls: false`. Recommend flipping default. |
| RT-6.15 | **A5/A6 — Redis/Postgres exposure check** | Verify operator-deployed Redis/Postgres are not LoadBalancer/NodePort | Operator responsibility; document Helm chart NOTES.txt warning |
| RT-6.16 | **Controller SA cluster scope on-by-default** | Verify default values produce ClusterRoleBinding | **Confirmed gap G5** — `values.yaml` default `rbac.scope: cluster`; flip default to namespace. |

---

### Phase 7: Application Logic & Business Logic Testing

| ID | Test Case | Attack Vector | Expected Finding |
|----|-----------|---------------|------------------|
| RT-7.1 | Workspace limit bypass | Create more workspaces than quota allows | Should enforce limits |
| RT-7.2 | Suspend/resume race | Rapidly suspend+resume to corrupt state | Controller should handle idempotently |
| RT-7.3 | Concurrent credential update | Race condition on credential write | Should be atomic |
| RT-7.4 | Session hijacking via workspace transfer | Transfer workspace while session active | Should invalidate sessions |
| RT-7.5 | Injection detection bypass | Craft prompts that evade all built-in patterns | Document novel bypass patterns |
| RT-7.6 | Activity tracking manipulation | Forge lastActivityAt to prevent auto-suspend | Should only accept from API server |
| RT-7.7 | Workspace name collision | Create workspace with name matching another user's | Should be per-user scoped |
| RT-7.8 | Delete workspace with active sessions | Delete while SSE streams are open | Should gracefully terminate |
| RT-7.9 | **Frontend XSS via crafted markdown** | Submit assistant content with payloads from a known XSS bypass corpus (e.g., `cure53/DOMPurify` test set, `htmlpurifier` bypasses) | `frontend/src/components/chat/MessagePart.tsx:74,84` uses default `rehype-sanitize` schema. Fuzz against: `<img onerror>`, `javascript:` URIs, `<svg onload>`, mathml/foreignObject confusion, mutation XSS via copy-paste, malformed UTF-8. **Assumption A9 unvalidated.** |
| RT-7.10 | **Frontend code-block injection** | Markdown code block with malicious tool input rendering | Verify `<pre>` and `<code>` paths in MessagePart.tsx do not parse HTML |
| RT-7.11 | **Frontend tool_use input/output rendering** | Tool input contains crafted JSON with HTML | `MessagePart.tsx:29` uses `JSON.stringify` then `<pre>` — React auto-escapes; confirm |
| RT-7.12 | **Frontend diff viewer bypass** | Tool input with `oldString`/`newString` containing HTML | `react-diff-viewer-continued` rendering — verify it escapes content |
| RT-7.13 | **CSP / clickjacking absence** | Inspect ingress response headers for `Content-Security-Policy`, `X-Frame-Options` | Likely absent; document operator responsibility or ship default headers via ingress chart |
| RT-7.14 | **JWT storage in browser** | Inspect frontend code: where is the JWT stored? localStorage / sessionStorage / cookie? | Document attack model; if localStorage, every XSS is fatal |
| RT-7.15 | **Workspace deletion DEK cleanup race** | Delete workspace mid-credential-write; verify partial state | Combined with RT-4.12; expect controller finalizer to clean up |

---

## Pentest Environment Requirements

| Component | Requirement |
|-----------|-------------|
| Kubernetes cluster | Kind or Talos with Calico/Cilium CNI; PSA enforcement available |
| LLMSafeSpace deployment | Full Helm install (API + Controller + Frontend) at fixed image SHA |
| Test accounts | 3 users (admin, regular-A, regular-B); separate API keys; documented |
| Network tools | nmap, curl, netcat, mitmproxy, dig, tcpdump available in test pod |
| Monitoring | kubectl logs, Prometheus metrics, audit logs enabled |
| Tooling | kube-hunter, trivy, kubeaudit, nuclei, ffuf, grype, cosign, syft (SBOM) |
| Frontend tooling | Headless Chrome via Playwright; XSS payload corpora (cure53, OWASP) |
| Recording | All RT-* test cases recorded with timestamps, request/response, evidence path |

---

## Severity Rating

| Rating | Definition | SLA | Deployment Gate |
|--------|-----------|-----|-----------------|
| **Critical** | Remote code execution, full credential theft, cluster compromise, broken isolation | Must be fixed before merge to main; remediation PR + regression test required | **HARD BLOCK**: Release Manager (Lena) must explicitly sign off in the worklog before any production tag is published. No `v*.*.*` release proceeds with unresolved Critical findings. |
| **High** | Cross-tenant data access, auth bypass, privilege escalation, plaintext secrets at rest | Fix within current sprint; regression test required | **SOFT BLOCK**: Release Manager + Security Lead joint sign-off required to ship; sign-off recorded in worklog with rationale. |
| **Medium** | Information disclosure, DoS, defense-in-depth bypass, race conditions | Fix within 2 sprints | Tracked in epic backlog; ship allowed with documented mitigation. |
| **Low** | Minor info leak, hardening gap, theoretical attack | Track and fix opportunistically | No deployment impact; tracked in backlog. |
| **Informational** | Best practice deviation, documentation gap | Note for future improvement | None. |

**Acceptance authority for "Accepted Risk" status:**

A finding may be marked "Accepted Risk" only with all of the following:

1. **Owner:** Released by Release Manager (Lena) AND Security Lead jointly
2. **Documentation:** Rationale recorded in `THREAT-MODEL.md` revision history, citing the finding ID, why mitigation is impractical, and what compensating controls exist
3. **Review:** Re-evaluated every 6 months per a recurring calendar invite
4. **Customer disclosure:** If the risk affects customer data confidentiality, integrity, or availability, it is documented in the public security model in `design/SECURITY.md`

This is a hard process gate — no informal accepted-risk dismissals.

---

## Reporting Template

Each finding must include:

```markdown
### [SEVERITY] Finding Title

**ID:** RT-X.Y
**CVSS:** X.X (vector string if applicable)
**CWE:** CWE-### (if applicable)
**Component:** API / Controller / Runtime / Frontend / Infra
**Status:** Open / Fixed / Accepted Risk / False Alarm

#### Chain of Custody
- **Discoverer:** <name / agent>
- **Disclosure date (internal):** YYYY-MM-DD
- **Public disclosure date:** YYYY-MM-DD or "Pending"
- **Affected versions:** <range, e.g. all versions ≤ commit SHA abc123>
- **Affected commit range:** <git refs>
- **Validator:** <name of skeptical validator who confirmed>

#### Description
What the vulnerability is.

#### Reproduction Steps
1. Step 1
2. Step 2
3. Observe: [result]

#### Impact
What an attacker gains. Be concrete.

#### Root Cause
Why the vulnerability exists. Cite file:line.

#### Remediation
Specific fix with code/config reference. Include the regression test that locks
the fix in place.

#### Evidence
- Screenshots / logs / PoC code path: `design/stories/epic-17-security-review/artefacts/RT-X.Y/`
- Regression test: `<file:line>`

#### Worklog
- Reference the worklog NNNN that documents the fix
```

---

## Workflow

This epic follows the Validator Loop in `README-LLM.md` §Multi-Agent Workflow:

```
For each Phase (1–7):
  1. Phase implementation delegation → execute test cases, produce findings
  2. Skeptical Validator delegation → re-run, confirm or refute each finding
     (Validator MUST NOT be the implementer per Rule 7)
  3. Findings Triage → mark each as Real / False Alarm; document false alarms
     with rationale
  4. Remediation Delegation → fix every Real finding, with regression test;
     no exceptions for "minor" findings
  5. Re-Validate → loop back to step 2 until validator returns zero real
     findings for the phase
  6. Worklog → record all findings (including false alarms), fixes, evidence,
     validator name, sign-offs
```

---

## Success Criteria

1. All Critical findings have remediation PRs merged AND regression tests AND Release Manager sign-off recorded in the worklog. **No exceptions.**
2. All High findings have remediation PRs merged AND regression tests AND joint Release Manager + Security Lead sign-off.
3. Threat model updated with any newly discovered attack vectors; revision history entry per change.
4. Known gaps G1–G20 each transitioned to one of: Fixed (with PR ref + regression test) / Accepted Risk (with sign-off per process above) / False Alarm (with rationale).
5. Automated security regression tests added in:
   - API: `api/internal/security/regression_test.go` per finding
   - Controller: `controller/internal/security/regression_test.go` per finding
   - Runtime: `runtimes/tests/security/test_RT_X_Y.py` per finding
   - Frontend: `frontend/tests/security/<finding>.spec.ts` per finding
6. Phase 1 SBOM committed to `design/stories/epic-17-security-review/artefacts/sbom.json`; CI pipeline runs `trivy fs` on every build and fails on new ≥ High CVE.
7. Phase 6 produces a Helm chart preflight job that validates assumptions A1, A2, A4 at install time.
8. Each Critical/High finding produces a numbered worklog entry per `README-LLM.md` Worklog Requirements.

---

## Cross-Reference: Epic 10 Multi-Tenant Trust Invariants

The following Phase-6 test cases directly validate Epic 10 invariants. If any of these fail, Epic 10 is not delivered:

| Epic 10 Invariant | Validating Test Cases |
|-------------------|----------------------|
| Workspaces are namespace-isolated by NetworkPolicy | RT-3.1, RT-3.8, RT-3.9, RT-5.7, RT-5.8, RT-6.12 (G16 fixed — pentest validates fix holds) |
| Cross-workspace PVC access is impossible | RT-6.7 |
| Controller does not have cluster-wide blast radius | RT-6.2, RT-6.16 (currently FAILS — G5 still open) |
| API SA cannot escape its namespace | RT-6.3, RT-6.8 |
| Sandbox pod cannot reach K8s API | RT-3.1, RT-3.2 (G17 fixed — pentest validates fix holds), RT-5.8 |
| Per-user secret encryption (DEK isolation) | RT-4.5, RT-4.6, RT-4.12, RT-4.13 |
| Sandbox pods isolated from shell-injection via secret payloads | RT-4.3 (G2 fixed — pentest validates fix holds) |
| JWT revocation actually revokes | RT-2.13 (G18 fixed — pentest validates fix holds) |

Findings in this set must be remediated before Epic 10 can be marked Complete. As of worklog 0078, G2/G16/G17/G18/G20 are fixed; G5 remains the last Epic 10 blocker.

---

## Related Documents

- [Threat Model](./THREAT-MODEL.md)
- [V2 Architecture (`design/EVOLUTION-V2.md`)](../../EVOLUTION-V2.md) — authoritative V2 design
- [V1 Security Model (`design/SECURITY.md`)](../../SECURITY.md) — defense-in-depth overview
- [V1 Network Policy Design (`design/NETWORK.md`)](../../NETWORK.md) — egress filter design
- [V1 Architecture (`design/ARCHITECTURE.md`)](../../ARCHITECTURE.md)
- [V1 Controller (`design/CONTROLLER.md`)](../../CONTROLLER.md)
- [Multi-Tenant Trust (Epic 10)](../epic-10-multi-tenant-trust/README.md)
- [Credential Health (Epic 8)](../epic-8-credential-health/README.md)
- [Worklog Index](../../../worklogs/) — historical decision record
