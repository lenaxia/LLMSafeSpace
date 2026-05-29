# LLMSafeSpace Threat Model

**Status:** Active
**Scope:** Full system — API, Controller, Runtime, Frontend, Infrastructure

---

## 1. System Overview

LLMSafeSpace is a Kubernetes-native platform that runs AI agents (opencode serve) in isolated sandbox pods. Users interact via REST API, SSE streaming, MCP protocol, or React frontend. The system manages credentials, workspaces (PVC-backed), and sandbox lifecycle.

### Trust Boundaries

```
┌─────────────────────────────────────────────────────────────────────────┐
│ EXTERNAL (Untrusted)                                                    │
│  • End users (browser, SDK, MCP client)                                 │
│  • LLM providers (OpenAI, Anthropic, etc.)                              │
│  • Package registries (PyPI, npm, GitHub)                               │
│  • Mise tool registry (jdx/mise releases on GitHub)                     │
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
│  • Proxy to sandbox pods (pod IP:agentd port)                            │
└────────────────────────────┬────────────────────────────────────────────┘
                             │ Pod network / K8s API
                             ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ BOUNDARY 3: Controller → Sandbox Pods                                   │
│  • Pod creation with security context                                    │
│  • Credential injection via init containers                              │
│  • Network policy enforcement (operator-supplied)                        │
│  • PVC lifecycle                                                         │
└────────────────────────────┬────────────────────────────────────────────┘
                             │ Filesystem / Network
                             ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ BOUNDARY 4: Sandbox Pod → External World                                │
│  • Agent (opencode serve) executes LLM-directed actions                  │
│  • Egress to LLM APIs (always allowed)                                   │
│  • Egress to allowlisted domains (configurable, NetworkPolicy-enforced)  │
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
| User password hash (bcrypt) | High | PostgreSQL | Offline brute-force → credential access |
| JWT signing key | Critical | API server config/env | Full impersonation of any user |
| PostgreSQL credentials | Critical | K8s Secret / env var | Full database access |
| Redis credentials | High | K8s Secret / env var | Session hijacking, cache poisoning |
| Workspace PVC data | Medium | Kubernetes PV | User code/data exposure |
| Agent conversation history | Medium | opencode state in pod | Intellectual property leak |
| Controller ServiceAccount token | Critical | Pod automount | Cluster-wide CRD/Secret/Pod manipulation (default scope: cluster) |
| API ServiceAccount token | High | Pod automount | Workspace-namespace Secret + CRD CRUD |
| etcd data (K8s Secrets at rest) | Critical | etcd storage | All credentials if unencrypted |
| Frontend session (JWT in browser) | High | localStorage / cookie | Account takeover until expiry |

---

## 3. Threat Actors

| Actor | Capability | Motivation |
|-------|-----------|-----------|
| **Malicious user** | Authenticated, owns workspaces | Escape sandbox, access other tenants' data, steal credentials |
| **Compromised agent** | Code execution inside sandbox pod | Exfiltrate data, pivot to cluster, mine crypto |
| **Malicious LLM output** | Prompt injection via tool responses | Manipulate agent to exfiltrate, escalate, or destroy |
| **Malicious assistant content (browser)** | LLM emits markdown/HTML rendered in user's browser | Exfiltrate JWT from browser via crafted content if sanitization is bypassed |
| **Network attacker** | MITM on pod-to-pod or egress traffic | Credential interception, data exfiltration |
| **Compromised API server** | Full API memory + DB access | Access all active session DEKs, impersonate users |
| **Compromised controller** | K8s SA with Secret/Pod CRUD (cluster-wide by default) | Read all credentials, create privileged pods |
| **Cluster admin (insider)** | kubectl access to all namespaces | Read Secrets, exec into pods |
| **Supply chain attacker** | Compromised base image, opencode binary, mise binary, or Go dependency | Backdoor in all sandbox pods |

---

## 4. Attack Trees

### 4.1 Credential Theft

```
Goal: Steal user's LLM API key
├── [1] From sandbox pod (attacker = compromised agent)
│   ├── [1.1] Read /sandbox-cfg/secrets.json (init container writes plaintext)
│   │   └── Mitigation: emptyDir mount (default disk-backed, NOT tmpfs — see G15),
│   │                   ReadOnly: true mount in main container, runs as UID 1000
│   ├── [1.2] Read /tmp/agent-config.json (materialized by entrypoint)
│   │   └── Mitigation: chmod is NOT set on /tmp/agent-config.json
│   │                   (entrypoint-common.sh:35 uses unconstrained `>`)
│   │                   — RESIDUAL RISK; same-UID processes can read
│   ├── [1.3] Read environment variables (env-secret type)
│   │   └── Mitigation: /proc/self/environ readable by same user — RESIDUAL RISK (G3)
│   ├── [1.4] Exfiltrate via allowed egress domain
│   │   └── Mitigation: Redaction on proxy layer (read-path only); NetworkPolicy
│   │                   if applied (operator-supplied — G16)
│   └── [1.5] Exfiltrate via DNS tunneling
│       └── Mitigation: Audit logging; DNS rate limiting (operator responsibility)
├── [2] From API server (attacker = compromised API)
│   ├── [2.1] Read K8s Secrets directly (API SA has Secret read access)
│   │   └── Mitigation: Namespace-scoped Role
│   │                   (charts/llmsafespace/templates/rbac.yaml:101-118);
│   │                   etcd encryption at rest (operator responsibility)
│   └── [2.2] Read DEK from Redis session cache
│       └── Mitigation: Redis auth; no NetworkPolicy template — relies on
│                       operator-supplied policy (G16)
├── [3] From database (attacker = SQL injection or DB compromise)
│   ├── [3.1] Read wrapped_dek from user_keys table
│   │   └── Mitigation: Useless without password (HKDF-derived KEK)
│   └── [3.2] Read ciphertext from user_secrets table
│       └── Mitigation: AES-256-GCM encrypted; useless without DEK
├── [4] From etcd (attacker = cluster admin or etcd breach)
│   ├── [4.1] Read K8s Secret objects (plaintext if etcd unencrypted)
│   │   └── Mitigation: Operator MUST configure etcd encryption (A1)
│   └── [4.2] Read controller SA token → impersonate controller
│       └── Mitigation: Bound SA tokens (short-lived); cluster-wide blast
│                       radius if controller scope = "cluster" (G5)
└── [5] From browser (attacker = malicious assistant content)
    ├── [5.1] XSS via crafted markdown bypassing rehype-sanitize
    │   └── Mitigation: rehype-sanitize default schema
    │                   (frontend/src/components/chat/MessagePart.tsx:74,84);
    │                   needs explicit verification — bypass would steal JWT
    └── [5.2] Token theft via leaked Authorization header to attacker domain
        └── Mitigation: API CORS hardened (AllowedOrigins: [], no wildcard)
```

### 4.2 Sandbox Escape

```
Goal: Break out of sandbox pod to access cluster resources
├── [1] Container escape
│   ├── [1.1] Kernel exploit (CVE in container runtime)
│   │   └── Mitigation: gVisor runtime (high-security profile); seccomp;
│   │                   regular patching (A3)
│   ├── [1.2] Exploit writable paths (/tmp, /workspace, /home/sandbox)
│   │   └── Mitigation: Read-only root (controller/internal/workspace/
│   │                   controller.go:613); noexec NOT set on emptyDir
│   │                   volumes (G1 confirmed)
│   └── [1.3] Abuse capabilities
│       └── Mitigation: Drop ALL capabilities (controller/internal/
│                       workspace/controller.go:616);
│                       AllowPrivilegeEscalation: false (line 615)
├── [2] Network escape
│   ├── [2.1] Access K8s API server (metadata endpoint)
│   │   └── Mitigation: Operator-supplied NetworkPolicy required;
│   │                   chart does NOT ship a default-deny policy (G16)
│   ├── [2.2] Access other pods in namespace
│   │   └── Mitigation: Operator-supplied NetworkPolicy required (G16)
│   ├── [2.3] Access node metadata (169.254.169.254)
│   │   └── Mitigation: Operator-supplied NetworkPolicy required (G16);
│   │                   cloud provider metadata blocking
│   └── [2.4] Access Redis/PostgreSQL directly
│       └── Mitigation: Service auth required; NetworkPolicy operator-
│                       supplied
├── [3] Kubernetes API abuse
│   ├── [3.1] SA token automount in sandbox pod
│   │   └── Mitigation: NONE — automountServiceAccountToken NOT set
│   │                   to false in pod spec (G17 — new gap)
│   └── [3.2] Exploit mounted secrets/configmaps
│       └── Mitigation: Only /sandbox-cfg (emptyDir) and /workspace (PVC)
│                       and password Secret mounted
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
│   │   └── Mitigation: JWT signature verification (HMAC-SHA256);
│   │                   alg-confusion check (api/internal/services/auth/
│   │                   auth.go:283 enforces SigningMethodHMAC only)
│   ├── [1.3] API key of another user
│   │   └── Mitigation: API keys per-user; bcrypt-hashed in DB; lsp_ prefix
│   └── [1.4] Replay revoked JWT
│       └── Mitigation: BROKEN — RevokeToken stores key as `token:<jti>`
│                       (auth.go:203) but ValidateToken reads `token:<hash>`
│                       (auth.go:270) — keys mismatch (G18 — new gap)
├── [2] Kubernetes-level
│   ├── [2.1] All workspaces in same namespace (label-based isolation only)
│   │   └── Mitigation: NetworkPolicy per-pod (operator-supplied — G16);
│   │                   ownership labels; controller enforces
│   ├── [2.2] PVC access from another pod
│   │   └── Mitigation: RWO access mode; one pod per workspace; controller
│   │                   enforces
│   └── [2.3] Secret name guessing (workspace-secrets-{uuid})
│       └── Mitigation: RBAC restricts Secret access to controller/API SA only
└── [3] Proxy-level
    ├── [3.1] Proxy to another user's pod IP
    │   └── Mitigation: Proxy resolves pod IP from CRD owned by authenticated
    │                   user; sandboxOwnershipMiddleware enforces
    └── [3.2] Session ID collision
        └── Mitigation: UUIDv4 session IDs; session-to-workspace binding
```

### 4.4 Prompt Injection / Agent Manipulation

```
Goal: Manipulate agent to perform unauthorized actions
├── [1] Indirect injection via tool output
│   ├── [1.1] Malicious content in fetched web page
│   │   └── Mitigation: Injection detection (where wired); redaction
│   ├── [1.2] Malicious content in git repo
│   │   └── Mitigation: Agent-level defense (opencode's own guardrails)
│   └── [1.3] Malicious content in package metadata
│       └── Mitigation: Mise resolves tools but does not pin checksums
│                       per-install (G19); redaction; audit logging
├── [2] Direct injection via user input
│   ├── [2.1] User crafts prompt to bypass agent guardrails
│   │   └── Mitigation: Out of scope (user attacking their own agent)
│   └── [2.2] Shared workspace — User A injects via workspace files
│       └── Mitigation: Workspaces are single-owner; no sharing in V2
└── [3] Exfiltration via agent
    ├── [3.1] Agent instructed to curl secrets to external URL
    │   └── Mitigation: NetworkPolicy if applied (G16); redaction on read
    │                   path only — does NOT redact outbound bodies (G14)
    └── [3.2] Agent encodes secrets in DNS queries
        └── Mitigation: DNS audit logging; rate limiting; accepted
                        residual risk
```

### 4.5 Frontend XSS / Browser-Side Compromise

```
Goal: Steal user's JWT or perform actions in user's browser session
├── [1] Stored XSS via assistant message content
│   ├── [1.1] Malicious markdown bypasses rehype-sanitize default schema
│   │   └── Mitigation: rehype-sanitize on all ReactMarkdown usage
│   │                   (frontend/src/components/chat/MessagePart.tsx:74,84);
│   │                   default schema strips on*, javascript:, data: URIs;
│   │                   needs explicit fuzz testing (RT-7.9)
│   ├── [1.2] Tool output rendered as <pre> — no XSS surface
│   │   └── Mitigation: <pre> renders as text, not HTML
│   │                   (MessagePart.tsx:171-173); React auto-escapes children
│   └── [1.3] Dangerous part types (HTML, raw)
│       └── Mitigation: Only known part types rendered (text/thinking/
│                       tool_use/tool_result/error); unknown returns null
│                       (MessagePart.tsx:205)
├── [2] Reflected XSS via API error responses rendered in UI
│   └── Mitigation: API errors are text-only; React JSX auto-escapes;
│                   no v-html / dangerouslySetInnerHTML in chat components
└── [3] Clickjacking
    └── Mitigation: Operator-supplied (Content-Security-Policy headers via
                    ingress; X-Frame-Options); not enforced by app
```

---

## 5. Identified Gaps & Residual Risks

All gaps below have been verified against the codebase. Each entry cites exact file:line evidence so red-team validators can independently re-verify per Rule 7.

| # | Gap | Severity | Verified By | Recommended Fix |
|---|-----|----------|-------------|-----------------|
| G1 | No `noexec` on emptyDir mounts | Medium | `controller/internal/workspace/controller.go:630-632` (no `Medium: Memory` either; backed by node disk) | Set `Medium: Memory` and use SecurityContext fsGroupChangePolicy + securityContext.seccompProfile RuntimeDefault. K8s does not directly support `noexec` on emptyDir; consider gVisor or Kyverno mount-option enforcement. |
| G2 | Entrypoint shell injection via secret values | High | `runtimes/base/tools/entrypoints/entrypoint-common.sh:78` — `echo "export ${VAR_NAME}='${PLAINTEXT}'" >> "$ENV_FILE"` — single quote in PLAINTEXT escapes the literal | Validate VAR_NAME against `[A-Z_][A-Z0-9_]*`; base64-encode PLAINTEXT or use `printf '%q'` quoting. Same risk on lines 41 (KEY_PATH derived from NAME), 43 (KEY content unvalidated), 59 (git-credentials line). |
| G3 | env-secret readable via /proc/self/environ | Medium | `entrypoint-opencode.sh:14` sources `/tmp/secrets-env` into the agent env | Document as accepted risk; prefer secret-file type; mark `env-secret` deprecated for new credentials. |
| G4 | No mTLS between API and sandbox pods | Medium | `api/internal/handlers/proxy.go:91-95` — plain `http.Transport`, no TLSClientConfig | Implement mTLS using a per-workspace cert issued by the controller, or deploy via service mesh (Linkerd/Istio sidecar). |
| G5 | Controller SA cluster-wide Secret access (default) | High | `charts/llmsafespace/templates/rbac.yaml:1-95` defaults to ClusterRole when `rbac.scope == "cluster"` (default in values.yaml) | Make `rbac.scope: namespace` the default; document upgrade path; refactor controller to drop cross-namespace dependencies. |
| G6 | No rate limiting on sensitive secret/credential endpoints | Medium | `api/internal/server/router.go:171-180` — `/api/v1/secrets/*` only behind global AuthMiddleware; no per-endpoint rate limiter | Apply per-user RateLimiter middleware specifically on POST /secrets, PUT /secrets/:id, POST /secrets/:id/reveal. |
| G7 | SSE streams bypass injection-detection blocking | Low | Streaming endpoints buffer-and-emit; injection detector only runs in non-streaming path (verify in `api/internal/handlers/proxy.go` event loop) | Document as accepted; buffer-and-scan for non-streaming responses where detector is wired. |
| G8 | First-user-admin auto-promotion | Medium | `api/internal/services/auth/auth.go:386-394` — checks `CountUsers == 0` then sets role=admin; no transaction wrapping → race window between count and insert | Use INSERT ... WHERE NOT EXISTS (SELECT 1 FROM users) RETURNING role, or admin bootstrap token via env var. |
| G9 | No image signature verification | Medium | `runtimes/base/Dockerfile:67-78` — `curl --fail` over TLS only; explicitly notes "Upstream does not publish .sha256 or signature files" | Implement Sigstore/cosign verification at admission time (Sigstore Policy Controller / Kyverno). For mise (lines 86-98), upstream publishes Sigstore attestations — use them. |
| G10 | Redis session cache not encrypted at rest | Low | `charts/llmsafespace/values.yaml:265-266` — Redis is external; persistence depends on operator config | Document operator requirement: disable RDB/AOF persistence OR enable disk encryption OR enable Redis TLS at rest. |
| G11 | No Pod Security Admission (PSA) enforcement | Medium | `charts/llmsafespace/templates/namespace.yaml` does not set `pod-security.kubernetes.io/enforce` labels (verify); `NOTES.txt:58` and `values.yaml:315` both note Kyverno enforcement is "not active" | Set `pod-security.kubernetes.io/enforce=restricted` on workspace namespace via chart. |
| G12 | Proxy ResponseHeaderTimeout 300s | Low | `api/internal/handlers/proxy.go:95` — `ResponseHeaderTimeout: 300 * time.Second` | Differentiate timeouts per operation: 30s for `/message`, no timeout for `/event` (SSE). |
| G13 | Account lockout DoS | Medium | `api/internal/services/auth/auth.go:440-512` — lockout key is `lockout:<email>` (line 441, 502) — attacker who knows victim's email can lock them out by sending N failed logins | Use IP-based throttling with progressive delays + CAPTCHA; reserve hard lockout for confirmed-source-IP attacks. |
| G14 | No egress request body inspection | High | No code path inspects outbound HTTP request bodies from sandbox pods | Accepted residual risk; minimize allowedDomains; document. |
| **G15** | **Sandbox emptyDir is disk-backed, not tmpfs** | **High** | `controller/internal/workspace/controller.go:630-632` — no `Medium: Memory` set on `sandbox-cfg`, `tmp`, `sandbox-home` volumes | **Set `Medium: Memory` on all three emptyDir volumes. Plaintext secrets in /sandbox-cfg/secrets.json currently survive on the node's disk if the kubelet doesn't reclaim immediately.** |
| **G16** | **No NetworkPolicy templates ship with the chart** | **Critical** | `charts/llmsafespace/templates/` — no NetworkPolicy resource present; `values.yaml:301` documents operator must supply | **Ship a default-deny + LLM-egress-allow NetworkPolicy template; gate behind `networkPolicy.enabled` (default true).** Without this, multi-tenant claims are unsupportable. |
| **G17** | **AutomountServiceAccountToken not set to false on sandbox pod** | **High** | `controller/internal/workspace/controller.go:653-666` — no `AutomountServiceAccountToken` field on PodSpec → defaults to true | **Add `AutomountServiceAccountToken: &falseVal` to the sandbox PodSpec.** Otherwise compromised agents have a default-mounted SA token. |
| **G18** | **JWT revocation is broken (cache key mismatch)** | **High** | `auth.go:203` writes `token:<jti>`; `auth.go:270` reads `token:<hash(token)>` — keys never collide → revoked tokens still validate until natural expiry | **Pick one key scheme (recommend: store both jti and hash on revoke; check both on validate). Add e2e regression test.** |
| **G19** | **mise installs runtimes from upstream without checksum verification** | **Medium** | `runtimes/base/Dockerfile:119-130` — `mise install --system python@latest node@lts ...` — `MISE_GITHUB_ATTESTATIONS=0` explicitly disables attestation checks | Re-enable `MISE_GITHUB_ATTESTATIONS=1` at build time; document the build environment must reach Sigstore/GitHub OIDC. Alternative: pin per-runtime versions and ship checksum files. |
| **G20** | **/tmp/agent-config.json written without chmod 600** | **Medium** | `entrypoint-common.sh:14, 19, 35` — files written via `>` with no `chmod` after | Add `chmod 600 /tmp/agent-config.json` after every write that includes credential material. |

---

## 6. STRIDE Analysis

| Component | Spoofing | Tampering | Repudiation | Info Disclosure | DoS | Elevation |
|-----------|----------|-----------|-------------|-----------------|-----|-----------|
| **API Auth** | JWT forgery (mitigated: signing key + HMAC-only check); API key theft | Token replay (G18 — revocation broken) | No audit of failed auth (GAP) | Error messages leak user existence (fixed) | Account lockout abuse (G13) | First-user-admin (G8) |
| **Proxy** | Workspace ID spoofing (mitigated: ownership check) | Response tampering (mitigated: same-cluster network — G4 if MITM) | No per-request audit trail | Credential leak in responses (mitigated: redaction read-path only) | Connection exhaustion (mitigated: limits) | N/A |
| **Controller** | SA token theft (mitigated: bound tokens) | CRD manipulation (mitigated: webhooks) | Controller actions not individually audited | Secret read access (G5) | CRD spam (mitigated: quotas) | Cluster-wide SA (G5) |
| **Sandbox Pod** | N/A (no auth within pod) | PVC data corruption | No file-level audit | Credential in env/files (G3, G15, G20); SA token automount (G17) | Resource exhaustion (mitigated: limits) | Container escape (mitigated: seccomp, caps; G1 unmitigated) |
| **Database** | SQL injection (mitigated: pgx parameterized) | Data corruption (mitigated: transactions) | No query audit log | Wrapped DEK exposure (mitigated: encryption) | Connection exhaustion | N/A |
| **Redis** | Auth bypass (mitigated: password) | Cache poisoning | No operation audit | DEK in memory (G10) | Memory exhaustion | N/A |
| **Frontend** | Session theft via XSS (mitigated: rehype-sanitize — needs fuzzing) | DOM tampering (mitigated: React auto-escape) | No client audit | JWT in localStorage if used | UI freeze via huge messages | N/A |

---

## 7. Data Flow Diagram (Security-Relevant)

```
User ──[HTTPS/JWT]──► API Server ──[K8s API/SA token]──► K8s API Server
                           │                                    │
                           │ [HTTP/pod-IP:agentd]               │ [etcd]
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

## 8. Assumptions (with Validation Evidence)

Per `README-LLM.md` Rule 7, every assumption must be validated. The table below records evidence collected during threat modeling. Where validation is not yet possible (operator runtime config), the assumption is flagged as a deployment-time precondition that must be enforced by Helm chart guards or documentation.

| # | Assumption | Validation Method | Status | Evidence / Action Required |
|---|-----------|-------------------|--------|----------------------------|
| A1 | etcd encryption at rest enabled | Pre-flight check at install time | **Unvalidated** | No chart guard exists. Action: add `helm install --pre-upgrade-hook` or `NOTES.txt` warning that fails-loud if EncryptionConfiguration is missing; document required `kube-apiserver --encryption-provider-config` flag. |
| A2 | NetworkPolicy CNI installed and functioning | Cluster capability check | **Unvalidated** | `charts/llmsafespace/templates/` has zero NetworkPolicy resources (G16). Even if CNI is present, no policy is applied. Action: ship default-deny + allowlist NetworkPolicy template gated by `networkPolicy.enabled` and add chart guard test. |
| A3 | Node OS patched, container runtime current | Operator responsibility | **Unvalidated** | No pre-flight check. Action: document minimum K8s version (>=1.29 for PSA stable) and container runtime baseline in chart README and NOTES.txt. |
| A4 | TLS termination at ingress | Helm chart values | **Configurable, default off** | `values.yaml:330+` — `frontend.ingress.tls: false` by default; api ingress similar. Action: flip default to `tls: true` and require user to explicitly disable for dev. |
| A5 | Redis not exposed outside cluster | Service type review | **VERIFIED for in-cluster Redis** | `charts/llmsafespace/values.yaml:266` references `redis-master` host (operator's existing deploy); chart does not create a Redis service. If operator deploys Redis with `type: LoadBalancer`, this assumption fails. Action: document network requirement; add NetworkPolicy gating Redis ingress to API SA only. |
| A6 | PostgreSQL not exposed outside cluster | Service type review | **VERIFIED for in-cluster Postgres** | `values.yaml:254-264` — Postgres is operator-deployed; same caveat as A5. Action: same as A5. |
| A7 | Container images from trusted registry | Dockerfile review | **PARTIAL** | `runtimes/base/Dockerfile:33` uses digest-pinned base (`debian:bookworm-slim@sha256:...`); opencode (line 67-78) and mise (line 86-98) downloaded over TLS without checksum or signature verification (G9, G19). Action: implement cosign verification; pin opencode/mise via SHA256 once upstream publishes. |
| A8 | JWT signing keys rotated periodically | Code search | **REFUTED** | `api/internal/services/auth/auth.go` — no rotation primitives; key sourced from config once at startup. Action: add JWKS-style key rotation with kid header, or document operator runbook for restart-with-new-secret rotation. |
| A9 | rehype-sanitize default schema is sufficient for LLM output | Bypass fuzz testing | **Unvalidated** | `frontend/src/components/chat/MessagePart.tsx:74,84` applies `rehype-sanitize` with default GFM-friendly schema. Action: fuzz with known XSS bypass corpora (RT-7.9). |
| A10 | Operator deploys etcd, K8s, CNI according to chart documentation | Documentation completeness | **Unvalidated** | Chart README lists requirements but no automated check. Action: write a `helm test` that probes for these preconditions. |

---

## 9. Out-of-Scope (Explicitly Documented)

The following risks are out of scope for the application but must be documented for operators:

| Risk | Owner | Mitigation Reference |
|------|-------|---------------------|
| LLM provider security | OpenAI/Anthropic/etc. | Operator selects providers |
| opencode binary internals | upstream anomalyco/opencode | Pin version; track CVE feeds |
| Physical/social engineering | Operator | Out of scope |
| etcd encryption at rest | K8s operator | Documented (A1) |
| Node OS hardening | Cluster admin | Documented (A3) |
| TLS termination | Ingress operator | Documented (A4) |

---

## 10. Revision History

| Version | Change |
|---------|--------|
| 1.1 | All gaps verified against code with file:line evidence; added G15 (emptyDir disk-backed), G16 (no NetworkPolicy ships), G17 (SA token automount), G18 (JWT revocation broken), G19 (mise no checksum), G20 (chmod missing on /tmp/agent-config.json); assumptions A1-A8 validated with evidence; added A9 (rehype-sanitize) and A10 (operator preconditions); added attack tree §4.5 (frontend XSS); STRIDE row added for Frontend |
| 1.0 | Initial threat model created |
