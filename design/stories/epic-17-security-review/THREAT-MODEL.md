# LLMSafeSpace Threat Model

**Date:** 2026-05-29
**Author:** mikekao
**Status:** Active
**Scope:** Full system — API, Controller, Runtime, Infrastructure

---

## 1. System Overview

LLMSafeSpace is a Kubernetes-native platform that runs AI agents (opencode serve) in isolated sandbox pods. Users interact via REST API, SSE streaming, or MCP protocol. The system manages credentials, workspaces (PVC-backed), and sandbox lifecycle.

### Trust Boundaries

```
┌─────────────────────────────────────────────────────────────────────────┐
│ EXTERNAL (Untrusted)                                                    │
│  • End users (browser, SDK, MCP client)                                 │
│  • LLM providers (OpenAI, Anthropic, etc.)                              │
│  • Package registries (PyPI, npm, GitHub)                               │
└────────────────────────────┬────────────────────────────────────────────┘
                             │ TLS / JWT / API Key
                             ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ BOUNDARY 1: Ingress → API Server                                        │
│  • Authentication (JWT + API key)                                        │
│  • Rate limiting                                                         │
│  • Input validation                                                      │
│  • CORS enforcement                                                      │
└────────────────────────────┬────────────────────────────────────────────┘
                             │ Internal HTTP / K8s API
                             ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ BOUNDARY 2: API Server → Kubernetes Cluster                             │
│  • RBAC (ServiceAccount scoped)                                          │
│  • CRD operations                                                        │
│  • Secret management                                                     │
│  • Proxy to sandbox pods (pod IP:4096)                                   │
└────────────────────────────┬────────────────────────────────────────────┘
                             │ Pod network / K8s API
                             ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ BOUNDARY 3: Controller → Sandbox Pods                                   │
│  • Pod creation with security context                                    │
│  • Credential injection via init containers                              │
│  • Network policy enforcement                                            │
│  • PVC lifecycle                                                         │
└────────────────────────────┬────────────────────────────────────────────┘
                             │ Filesystem / Network
                             ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ BOUNDARY 4: Sandbox Pod → External World                                │
│  • Agent (opencode serve) executes LLM-directed actions                  │
│  • Egress to LLM APIs (always allowed)                                   │
│  • Egress to allowlisted domains (configurable)                          │
│  • Credential access (tmpfs-mounted, never on PVC)                       │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Assets (What We Protect)

| Asset | Sensitivity | Location | Impact if Compromised |
|-------|-------------|----------|----------------------|
| User LLM API keys | Critical | K8s Secret → tmpfs in pod | Financial loss, unauthorized API usage |
| User SSH keys / Git tokens | Critical | K8s Secret → tmpfs in pod | Source code theft, supply chain attack |
| User DEK (data encryption key) | Critical | Redis session cache (memory) | All user secrets decryptable |
| User password hash (argon2id) | High | PostgreSQL | Offline brute-force → credential access |
| JWT signing key | Critical | API server config/env | Full impersonation of any user |
| PostgreSQL credentials | Critical | K8s Secret / env var | Full database access |
| Redis credentials | High | K8s Secret / env var | Session hijacking, cache poisoning |
| Workspace PVC data | Medium | Kubernetes PV | User code/data exposure |
| Agent conversation history | Medium | opencode state in pod | Intellectual property leak |
| Controller ServiceAccount token | Critical | Pod automount | Cluster-wide CRD/Secret/Pod manipulation |
| etcd data (K8s Secrets at rest) | Critical | etcd storage | All credentials if unencrypted |

---

## 3. Threat Actors

| Actor | Capability | Motivation |
|-------|-----------|-----------|
| **Malicious user** | Authenticated, owns workspaces | Escape sandbox, access other tenants' data, steal credentials |
| **Compromised agent** | Code execution inside sandbox pod | Exfiltrate data, pivot to cluster, mine crypto |
| **Malicious LLM output** | Prompt injection via tool responses | Manipulate agent to exfiltrate, escalate, or destroy |
| **Network attacker** | MITM on pod-to-pod or egress traffic | Credential interception, data exfiltration |
| **Compromised API server** | Full API memory + DB access | Access all active session DEKs, impersonate users |
| **Compromised controller** | K8s SA with Secret/Pod CRUD | Read all credentials, create privileged pods |
| **Cluster admin (insider)** | kubectl access to all namespaces | Read Secrets, exec into pods |
| **Supply chain attacker** | Compromised base image or dependency | Backdoor in all sandbox pods |

---

## 4. Attack Trees

### 4.1 Credential Theft

```
Goal: Steal user's LLM API key
├── [1] From sandbox pod (attacker = compromised agent)
│   ├── [1.1] Read /sandbox-cfg/secrets.json (init container writes plaintext)
│   │   └── Mitigation: tmpfs mount, read-only after init, non-root user
│   ├── [1.2] Read /tmp/agent-config.json (materialized by entrypoint)
│   │   └── Mitigation: File permissions 600, same-user only
│   ├── [1.3] Read environment variables (env-secret type)
│   │   └── Mitigation: /proc/self/environ readable by same user — RESIDUAL RISK
│   ├── [1.4] Exfiltrate via allowed egress domain
│   │   └── Mitigation: Redaction on proxy layer; network policy limits destinations
│   └── [1.5] Exfiltrate via DNS tunneling
│       └── Mitigation: Audit logging; DNS rate limiting (operator responsibility)
├── [2] From API server (attacker = compromised API)
│   ├── [2.1] Read K8s Secrets directly (API SA has Secret read access)
│   │   └── Mitigation: Namespace-scoped Role; encrypt etcd at rest
│   └── [2.2] Read DEK from Redis session cache
│       └── Mitigation: Redis auth; network policy; session TTL
├── [3] From database (attacker = SQL injection or DB compromise)
│   ├── [3.1] Read wrapped_dek from user_keys table
│   │   └── Mitigation: Useless without password (HKDF-derived KEK)
│   └── [3.2] Read ciphertext from user_secrets table
│       └── Mitigation: AES-256-GCM encrypted; useless without DEK
└── [4] From etcd (attacker = cluster admin or etcd breach)
    ├── [4.1] Read K8s Secret objects (plaintext if etcd unencrypted)
    │   └── Mitigation: Document etcd encryption requirement; operator responsibility
    └── [4.2] Read controller SA token → impersonate controller
        └── Mitigation: Bound SA tokens (short-lived); audit logging
```

### 4.2 Sandbox Escape

```
Goal: Break out of sandbox pod to access cluster resources
├── [1] Container escape
│   ├── [1.1] Kernel exploit (CVE in container runtime)
│   │   └── Mitigation: gVisor runtime (high-security); seccomp profiles; regular patching
│   ├── [1.2] Exploit writable paths (/tmp, /workspace)
│   │   └── Mitigation: Read-only root; noexec on tmpfs (not currently enforced — GAP)
│   └── [1.3] Abuse capabilities
│       └── Mitigation: Drop ALL capabilities; no privilege escalation
├── [2] Network escape
│   ├── [2.1] Access K8s API server (metadata endpoint)
│   │   └── Mitigation: NetworkPolicy blocks kube-apiserver; blockKubeAPI=true
│   ├── [2.2] Access other pods in namespace
│   │   └── Mitigation: Default-deny ingress/egress NetworkPolicy
│   ├── [2.3] Access node metadata (169.254.169.254)
│   │   └── Mitigation: NetworkPolicy denies; cloud provider metadata blocking
│   └── [2.4] Access Redis/PostgreSQL directly
│       └── Mitigation: NetworkPolicy; services in separate namespace; auth required
├── [3] Kubernetes API abuse
│   ├── [3.1] SA token automount in sandbox pod
│   │   └── Mitigation: automountServiceAccountToken: false on sandbox pods
│   └── [3.2] Exploit mounted secrets/configmaps
│       └── Mitigation: Only /sandbox-cfg (emptyDir) and /workspace (PVC) mounted
└── [4] Resource exhaustion (DoS)
    ├── [4.1] Fork bomb / CPU exhaustion
    │   └── Mitigation: Resource limits (CPU/memory); PID limits
    ├── [4.2] Fill PVC storage
    │   └── Mitigation: Storage quotas; ephemeral storage limits
    └── [4.3] Open excessive network connections
        └── Mitigation: Connection limits in NetworkPolicy; conntrack limits
```

### 4.3 Cross-Tenant Data Access

```
Goal: User A accesses User B's workspace/credentials
├── [1] API-level
│   ├── [1.1] IDOR — guess workspace ID (UUID)
│   │   └── Mitigation: Ownership check on every API call; UUIDv4 unguessable
│   ├── [1.2] JWT manipulation (change user_id claim)
│   │   └── Mitigation: JWT signature verification; HMAC-SHA256 or RS256
│   └── [1.3] API key of another user
│       └── Mitigation: API keys are per-user; bcrypt-hashed in DB; lsp_ prefix
├── [2] Kubernetes-level
│   ├── [2.1] All workspaces in same namespace (label-based isolation only)
│   │   └── Mitigation: NetworkPolicy per-pod; ownership labels; controller enforces
│   ├── [2.2] PVC access from another pod
│   │   └── Mitigation: RWO access mode; one pod per workspace; controller enforces
│   └── [2.3] Secret name guessing (workspace-creds-{uuid})
│       └── Mitigation: RBAC restricts Secret access to controller/API SA only
└── [3] Proxy-level
    ├── [3.1] Proxy to another user's pod IP
    │   └── Mitigation: Proxy resolves pod IP from CRD owned by authenticated user
    └── [3.2] Session ID collision
        └── Mitigation: UUIDv4 session IDs; session-to-workspace binding
```

### 4.4 Prompt Injection / Agent Manipulation

```
Goal: Manipulate agent to perform unauthorized actions
├── [1] Indirect injection via tool output
│   ├── [1.1] Malicious content in fetched web page
│   │   └── Mitigation: Injection detection (log/block/flag); redaction
│   ├── [1.2] Malicious content in git repo
│   │   └── Mitigation: Agent-level defense (opencode's own guardrails)
│   └── [1.3] Malicious content in package metadata
│       └── Mitigation: PATH shadowing; redaction; audit logging
├── [2] Direct injection via user input
│   ├── [2.1] User crafts prompt to bypass agent guardrails
│   │   └── Mitigation: Out of scope (user attacking their own agent)
│   └── [2.2] Shared workspace — User A injects via workspace files
│       └── Mitigation: Workspaces are single-owner; no sharing in V2
└── [3] Exfiltration via agent
    ├── [3.1] Agent instructed to curl secrets to external URL
    │   └── Mitigation: Network policy; redaction on egress (partial — see §14.2)
    └── [3.2] Agent encodes secrets in DNS queries
        └── Mitigation: DNS audit logging; rate limiting; accepted residual risk
```

---

## 5. Identified Gaps & Residual Risks

| # | Gap | Severity | Current State | Recommended Fix |
|---|-----|----------|---------------|-----------------|
| G1 | No `noexec` on tmpfs mounts | Medium | Agent can write+execute binaries in /tmp | Add `noexec` mount option to tmpfs volumes |
| G2 | Entrypoint shell injection via secret values | High | `entrypoint-common.sh` uses `jq -r` output in shell variables without quoting | Validate secret values; use heredoc or base64 encoding |
| G3 | env-secret readable via /proc/self/environ | Medium | Any process in pod can read all env vars | Document as accepted risk; prefer secret-file type |
| G4 | No mutual TLS between API and sandbox pods | Medium | Proxy uses plain HTTP to pod IP:4096 | Implement mTLS or use K8s service mesh |
| G5 | Controller SA has cluster-wide Secret access (cluster scope) | High | Compromised controller reads all Secrets | Use namespace-scoped RBAC; virtual namespaces per tenant |
| G6 | No rate limiting on credential API endpoints | Medium | Brute-force credential enumeration possible | Add per-user rate limiting on PUT /credentials |
| G7 | SSE streams bypass injection detection blocking | Low | SSE uses "flag" mode only (can't block mid-stream) | Document as accepted; buffer-and-scan for non-streaming |
| G8 | First-user-admin auto-promotion | Medium | First registered user gets admin role | Add admin bootstrap token or manual promotion |
| G9 | No image signature verification | Medium | Base images pulled by tag/SHA but not cosign-verified | Implement Sigstore/cosign verification in CI + admission |
| G10 | Redis session cache not encrypted at rest | Low | DEKs in Redis memory; Redis persistence writes to disk | Enable Redis TLS; disable RDB/AOF or encrypt volume |
| G11 | No pod security admission (PSA) enforcement | Medium | Relies on controller generating correct spec | Enable PSA restricted profile on workspace namespace |
| G12 | Proxy HTTP client has 300s timeout | Low | Long-running requests hold connections | Implement per-request timeout based on operation type |
| G13 | Account lockout DoS | Medium | Attacker can lock any account by sending N failed logins | Use CAPTCHA or progressive delays instead of hard lockout |
| G14 | No egress request body inspection | High | Agent can send secrets TO allowed domains | Accepted residual risk; document; minimize allowedDomains |

---

## 6. STRIDE Analysis

| Component | Spoofing | Tampering | Repudiation | Info Disclosure | DoS | Elevation |
|-----------|----------|-----------|-------------|-----------------|-----|-----------|
| **API Auth** | JWT forgery (mitigated: signing key); API key theft | Token replay (mitigated: short TTL, jti) | No audit of failed auth (GAP) | Error messages leak user existence (fixed) | Account lockout abuse (G13) | First-user-admin (G8) |
| **Proxy** | Workspace ID spoofing (mitigated: ownership check) | Response tampering (mitigated: same-cluster network) | No per-request audit trail | Credential leak in responses (mitigated: redaction) | Connection exhaustion (mitigated: limits) | N/A |
| **Controller** | SA token theft (mitigated: bound tokens) | CRD manipulation (mitigated: webhooks) | Controller actions not individually audited | Secret read access (G5) | CRD spam (mitigated: quotas) | Cluster-wide SA (G5) |
| **Sandbox Pod** | N/A (no auth within pod) | PVC data corruption | No file-level audit | Credential in env/files (G3) | Resource exhaustion (mitigated: limits) | Container escape (mitigated: seccomp, caps) |
| **Database** | SQL injection (mitigated: pgx parameterized) | Data corruption (mitigated: transactions) | No query audit log | Wrapped DEK exposure (mitigated: encryption) | Connection exhaustion | N/A |
| **Redis** | Auth bypass (mitigated: password) | Cache poisoning | No operation audit | DEK in memory (G10) | Memory exhaustion | N/A |

---

## 7. Data Flow Diagram (Security-Relevant)

```
User ──[HTTPS/JWT]──► API Server ──[K8s API/SA token]──► K8s API Server
                           │                                    │
                           │ [HTTP/pod-IP:4096]                 │ [etcd]
                           ▼                                    ▼
                      Sandbox Pod                          K8s Secrets
                           │                              (credential store)
                           │ [HTTPS/API key]                    │
                           ▼                                    │
                      LLM Provider                              │
                                                                │
User ──[HTTPS/JWT]──► API Server ──[pgx/TLS]──► PostgreSQL     │
                           │                    (user metadata,  │
                           │                     wrapped DEKs)   │
                           │                                    │
                           └──[go-redis]──► Redis               │
                                           (session DEKs,       │
                                            rate limits,        │
                                            cache)              │
```

---

## 8. Assumptions

| # | Assumption | Risk if False |
|---|-----------|---------------|
| A1 | Kubernetes cluster has etcd encryption at rest enabled | All Secrets readable from etcd backup |
| A2 | NetworkPolicy controller (Calico/Cilium) is installed and functioning | No network isolation between pods |
| A3 | Node OS is patched and container runtime is current | Container escape via kernel CVE |
| A4 | TLS termination happens at ingress (cert-manager or cloud LB) | MITM on user traffic |
| A5 | Redis is not exposed outside the cluster | Session DEK theft from external network |
| A6 | PostgreSQL is not exposed outside the cluster | Database compromise from external network |
| A7 | Container images are pulled from trusted registry | Supply chain compromise |
| A8 | Operators rotate JWT signing keys periodically | Long-lived compromised keys |

---

## 9. Revision History

| Date | Change |
|------|--------|
| 2026-05-29 | Initial threat model created |
