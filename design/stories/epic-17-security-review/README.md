# Epic 17: Security Review & Penetration Test Plan

**Status:** Planning
**Author:** mikekao
**Date:** 2026-05-29
**Depends On:** Epics 6, 8, 10 (core platform must be functional)
**Threat Model:** [THREAT-MODEL.md](./THREAT-MODEL.md)

---

## Objective

Conduct a red-team security assessment of LLMSafeSpace covering all trust boundaries, credential flows, sandbox isolation, and multi-tenant separation. Produce actionable findings with severity ratings and remediation guidance.

---

## Scope

### In Scope

| Layer | Components | Focus |
|-------|-----------|-------|
| API Server | Auth, proxy, handlers, middleware, MCP server | AuthN/AuthZ bypass, injection, SSRF, IDOR |
| Controller | Reconcilers, pod spec generation, RBAC | Privilege escalation, CRD manipulation |
| Sandbox Runtime | Entrypoints, credential materialization, opencode | Container escape, credential theft, network escape |
| Infrastructure | Helm chart, RBAC, NetworkPolicy, Secrets | Misconfig, over-permissioned SA, missing policies |
| Crypto | Key wrapping, JWT, password hashing, redaction | Weak crypto, key leakage, bypass |
| Data Stores | PostgreSQL, Redis, etcd (K8s Secrets) | Injection, unauthorized access, data at rest |

### Out of Scope

- LLM provider security (OpenAI/Anthropic infrastructure)
- opencode binary internals (upstream project)
- Physical/social engineering
- Client-side browser security (XSS in frontend — separate assessment)

---

## Red Team Methodology

### Phase 1: Reconnaissance & Attack Surface Mapping

**Duration:** 2 days

| Task | Technique | Target |
|------|-----------|--------|
| RT-1.1 | API endpoint enumeration | Swagger docs, route registration in `router.go` |
| RT-1.2 | CRD schema analysis | `pkg/crds/*.yaml` — identify mutable fields |
| RT-1.3 | RBAC privilege mapping | `charts/llmsafespace/templates/rbac.yaml` |
| RT-1.4 | Network topology mapping | Service definitions, NetworkPolicy templates |
| RT-1.5 | Dependency audit | `go.mod`, `go.sum` — known CVEs in deps |
| RT-1.6 | Container image analysis | `runtimes/base/Dockerfile` — installed packages, SUID binaries |
| RT-1.7 | Secret storage mapping | Where credentials exist at rest and in transit |

**Deliverable:** Attack surface inventory with entry points ranked by exposure.

---

### Phase 2: Authentication & Authorization Testing

**Duration:** 3 days

| ID | Test Case | Attack Vector | Expected Finding |
|----|-----------|---------------|-----------------|
| RT-2.1 | JWT signature bypass | Modify algorithm header (alg:none, HS256→RS256 confusion) | Should reject |
| RT-2.2 | JWT claim manipulation | Modify user_id, role, exp claims with valid signature | Should reject (signature invalid) |
| RT-2.3 | Expired token replay | Use token past exp with valid signature | Should reject |
| RT-2.4 | API key brute force | Enumerate `lsp_` prefixed keys | Rate limiting should block |
| RT-2.5 | Registration abuse | Mass account creation | Rate limiting (1/min documented) |
| RT-2.6 | Account lockout DoS | Send N failed logins for target user | **Known gap G13** — verify exploitability |
| RT-2.7 | First-user-admin race | Two concurrent registrations on fresh DB | **Known gap G8** — verify race condition |
| RT-2.8 | Auth bypass on skip paths | Craft requests matching `/health`, `/docs/` prefixes with path traversal | Should not bypass |
| RT-2.9 | CORS misconfiguration | Cross-origin requests with credentials | Should reject (AllowedOrigins: []) |
| RT-2.10 | Session fixation | Reuse session_id across users | Should be bound to user |
| RT-2.11 | Password reset without recovery key | Verify secrets are wiped, not accessible | Should be irrecoverable |
| RT-2.12 | Admin role escalation | Non-admin attempts admin-only operations | Should 403 |

---

### Phase 3: Sandbox Isolation & Container Escape

**Duration:** 4 days

| ID | Test Case | Attack Vector | Expected Finding |
|----|-----------|---------------|-----------------|
| RT-3.1 | Read other pod's secrets | From sandbox pod, attempt to reach K8s API | NetworkPolicy should block |
| RT-3.2 | SA token access | Check automountServiceAccountToken in sandbox pod | Should be false |
| RT-3.3 | /proc exploration | Read /proc/1/environ, /proc/*/maps | Env vars readable (G3) |
| RT-3.4 | Write+exec in /tmp | Write binary to /tmp, chmod +x, execute | **Gap G1** — noexec not enforced |
| RT-3.5 | Escape via /workspace | Write SUID binary to PVC, exec after resume | Should fail (nosuid mount) |
| RT-3.6 | Capability abuse | Attempt operations requiring dropped caps | Should fail (drop ALL) |
| RT-3.7 | Seccomp bypass | Attempt blocked syscalls (ptrace, mount, etc.) | Should EPERM |
| RT-3.8 | Node metadata access | curl 169.254.169.254 from sandbox | NetworkPolicy should block |
| RT-3.9 | Cross-pod network access | From sandbox A, connect to sandbox B's pod IP:4096 | NetworkPolicy should block |
| RT-3.10 | DNS exfiltration | Encode data in DNS queries to external domain | Resolves (accepted risk) — verify audit logging |
| RT-3.11 | Resource exhaustion | Fork bomb, memory allocation, disk fill | Limits should contain |
| RT-3.12 | PID namespace escape | Attempt to signal processes outside pod | Should fail (PID namespace isolation) |
| RT-3.13 | Symlink escape | Create symlink in /workspace pointing to /etc/shadow | Should fail (read-only root) |
| RT-3.14 | Device access | Attempt to access /dev/kmsg, /dev/mem | Should fail (no device mounts) |

---

### Phase 4: Credential & Crypto Testing

**Duration:** 3 days

| ID | Test Case | Attack Vector | Expected Finding |
|----|-----------|---------------|-----------------|
| RT-4.1 | Credential API IDOR | PUT/GET credentials for workspace owned by another user | Should 403 |
| RT-4.2 | Secret value in logs | Trigger errors with credential operations, check API logs | Should be redacted |
| RT-4.3 | Entrypoint injection | Set secret value containing `'; rm -rf /; echo '` | **Gap G2** — verify shell injection |
| RT-4.4 | Secret file path traversal | Set mount_path to `../../etc/passwd` | Entrypoint sanitizes (verify) |
| RT-4.5 | DEK extraction from Redis | Connect to Redis from compromised API pod | Verify Redis auth + network isolation |
| RT-4.6 | Wrapped DEK offline attack | Extract wrapped_dek from DB, attempt offline unwrap | Should require password (HKDF) |
| RT-4.7 | JWT signing key extraction | Check if key is in env var, config file, or hardcoded | Should be in Secret only |
| RT-4.8 | Redaction bypass | Craft credential patterns that evade all 16 regex rules | Document bypass patterns |
| RT-4.9 | Redaction DoS | Send 1MB+ payload to trigger maxInputBytes bypass | Verify large payloads pass unredacted |
| RT-4.10 | Password hash timing attack | Measure response time for valid vs invalid usernames | Should be constant-time (argon2id) |
| RT-4.11 | Recovery key brute force | Attempt to brute-force 128-bit recovery key | Should be infeasible (2^128) |

---

### Phase 5: Proxy & Network Testing

**Duration:** 3 days

| ID | Test Case | Attack Vector | Expected Finding |
|----|-----------|---------------|-----------------|
| RT-5.1 | SSRF via proxy | Craft workspace that resolves to internal IP | Proxy uses CRD-reported pod IP only |
| RT-5.2 | Proxy to arbitrary port | Modify request to target port other than 4096 | Should be hardcoded |
| RT-5.3 | HTTP request smuggling | Chunked encoding / CL-TE mismatch through proxy | Gin should handle correctly |
| RT-5.4 | SSE injection | Inject SSE events into stream from sandbox | Verify stream integrity |
| RT-5.5 | Connection exhaustion | Open maxConnectionsPerWorkspace+1 connections | Should reject excess |
| RT-5.6 | Stale pod IP exploitation | Race condition: pod deleted, IP reassigned, proxy connects to wrong pod | Verify retry logic + ownership |
| RT-5.7 | NetworkPolicy bypass | Use allowed DNS to resolve attacker domain, exfiltrate | Verify domain resolution refresh |
| RT-5.8 | Egress to kube-apiserver | From sandbox, attempt HTTPS to kubernetes.default.svc | NetworkPolicy should block |
| RT-5.9 | MCP transport injection | Malformed MCP messages via stdio/SSE | Should reject gracefully |
| RT-5.10 | WebSocket upgrade abuse | Attempt WebSocket upgrade on non-WebSocket endpoints | Should reject |

---

### Phase 6: Kubernetes & Infrastructure Testing

**Duration:** 3 days

| ID | Test Case | Attack Vector | Expected Finding |
|----|-----------|---------------|-----------------|
| RT-6.1 | CRD manipulation | Create Workspace CRD with malicious spec (huge resources, bad image) | Webhook validation should reject |
| RT-6.2 | Controller SA abuse | If controller SA token leaked, what can attacker do? | Document blast radius |
| RT-6.3 | API SA abuse | If API SA token leaked, what can attacker do? | Document blast radius |
| RT-6.4 | Webhook bypass | Delete ValidatingWebhookConfiguration, create bad CRD | Requires cluster-admin (accepted) |
| RT-6.5 | Helm values injection | Malicious values in Helm install | Template injection in YAML |
| RT-6.6 | etcd Secret exposure | Verify etcd encryption at rest is configured | Operator responsibility — document |
| RT-6.7 | PVC cross-mount | Create pod that mounts another workspace's PVC | RWO + controller ownership should prevent |
| RT-6.8 | Namespace escape | From workspace namespace, access system namespace resources | RBAC should prevent |
| RT-6.9 | Leader election poisoning | Create fake lease to disrupt controller | Requires SA permissions |
| RT-6.10 | Image pull from untrusted registry | Modify RuntimeEnvironment to point to attacker image | Webhook validation should restrict |

---

### Phase 7: Application Logic & Business Logic Testing

**Duration:** 2 days

| ID | Test Case | Attack Vector | Expected Finding |
|----|-----------|---------------|-----------------|
| RT-7.1 | Workspace limit bypass | Create more workspaces than quota allows | Should enforce limits |
| RT-7.2 | Suspend/resume race | Rapidly suspend+resume to corrupt state | Controller should handle idempotently |
| RT-7.3 | Concurrent credential update | Race condition on credential write | Should be atomic |
| RT-7.4 | Session hijacking via workspace transfer | Transfer workspace while session active | Should invalidate sessions |
| RT-7.5 | Injection detection bypass | Craft prompts that evade all 5 built-in patterns | Document novel bypass patterns |
| RT-7.6 | Activity tracking manipulation | Forge lastActivityAt to prevent auto-suspend | Should only accept from API server |
| RT-7.7 | Workspace name collision | Create workspace with name matching another user's | Should be per-user scoped |
| RT-7.8 | Delete workspace with active sessions | Delete while SSE streams are open | Should gracefully terminate |

---

## Pentest Environment Requirements

| Component | Requirement |
|-----------|-------------|
| Kubernetes cluster | Kind or Talos with Calico/Cilium CNI |
| LLMSafeSpace deployment | Full Helm install (API + Controller + Frontend) |
| Test accounts | 3+ users (admin, regular, attacker) |
| Network tools | nmap, curl, netcat available in test pod |
| Monitoring | kubectl logs, Prometheus metrics, audit logs enabled |
| Tooling | kube-hunter, trivy, kubeaudit, nuclei, ffuf |

---

## Severity Rating

| Rating | Definition | SLA |
|--------|-----------|-----|
| **Critical** | Remote code execution, full credential theft, cluster compromise | Fix before any production deployment |
| **High** | Cross-tenant data access, auth bypass, privilege escalation | Fix within 1 sprint |
| **Medium** | Information disclosure, DoS, defense-in-depth bypass | Fix within 2 sprints |
| **Low** | Minor info leak, hardening gap, theoretical attack | Track and fix opportunistically |
| **Informational** | Best practice deviation, documentation gap | Note for future improvement |

---

## Reporting Template

Each finding should include:

```markdown
### [SEVERITY] Finding Title

**ID:** RT-X.Y
**CVSS:** X.X (if applicable)
**Component:** API / Controller / Runtime / Infra
**Status:** Open / Fixed / Accepted Risk

#### Description
What the vulnerability is.

#### Reproduction Steps
1. Step 1
2. Step 2
3. Observe: [result]

#### Impact
What an attacker gains.

#### Root Cause
Why the vulnerability exists.

#### Remediation
Specific fix with code/config reference.

#### Evidence
Screenshots, logs, or PoC code.
```

---

## Timeline

| Week | Phase | Deliverable |
|------|-------|-------------|
| 1 | Recon + Auth (Phase 1-2) | Attack surface map, auth findings |
| 2 | Sandbox + Crypto (Phase 3-4) | Isolation findings, crypto findings |
| 3 | Network + Infra (Phase 5-6) | Network findings, K8s findings |
| 4 | App Logic + Report (Phase 7) | Final report with all findings |

---

## Success Criteria

1. All Critical/High findings have remediation PRs merged
2. Threat model updated with any newly discovered attack vectors
3. Known gaps (G1-G14) validated and either fixed or formally accepted
4. Automated security regression tests added for each Critical/High finding
5. Security policy presets validated against actual attack scenarios

---

## Related Documents

- [Threat Model](./THREAT-MODEL.md)
- [Security Policy V2.1](../../0027_2026-05-24_security-policy-v21.md)
- [Network Policy Design](../../0020_2025-03-05_network.md)
- [V1 Security Model](../../0005_2025-03-05_security.md)
- [Multi-Tenant Trust (Epic 10)](../epic-10-multi-tenant-trust/README.md)
- [Evolution V2 §16 Risk Assessment](../../0021_2026-05-21_evolution-v2.md)
