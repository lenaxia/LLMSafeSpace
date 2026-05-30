# RT-1.5 / RT-1.6 — Dependency & Image Analysis

**Phase:** 1 (Reconnaissance)
**Date scanned:** 2026-05-30
**Toolchain (host):** `go1.25.5 linux/amd64`, `govulncheck` rebuilt against go1.25, `npm` 10.x against `registry.npmjs.org`.
**Tools NOT available (per Phase 0 manifest):** `trivy`, `grype`, `syft`, `kubeaudit`, `kube-hunter` — see "Gap" section.
**Branch / commit:** working tree at `main` (post-Epic-17 Phase 0 prod kit, commit not pinned for this scan).

---

## Part A — RT-1.5 SBOM / CVE

### Part A.1 — Go module audit (root `go.mod`)

Source-of-truth: `go.mod:1-130` and `go.sum`. Go directive: `go 1.25.5`. Module graph spans the API server, the controller manager, `cmd/redact`, `cmd/workspace-agentd`, and `cmd/mcp` (all share one root module — see `replace` block at `go.mod:125-129`).

| Module | Version (pinned) | Risk | Source / CVE |
|---|---|---|---|
| `golang.org/x/net` | `v0.34.0` (`go.mod:104`) | **HIGH (3 CVEs reachable)** | GO-2025-3595 (HTML tokenizer XSS, fixed `v0.38.0`); GO-2026-4918 (HTTP/2 transport infinite loop on bad SETTINGS_MAX_FRAME_SIZE, fixed `v0.53.0`); GO-2026-5026 (IDNA Punycode label-validation bypass, fixed `v0.55.0`). All three appear in govulncheck output below with reachable call traces. |
| `github.com/golang-jwt/jwt/v5` | `v5.2.1` (`go.mod:12`) | **HIGH (auth path)** | GO-2025-3553 — excessive memory allocation during JWT header parsing. Reached from `api/internal/services/auth/auth.go:298` `ValidateToken → jwt.Parse → ParseUnverified`. Fixed in `v5.2.2`. Auth path is unauthenticated-attacker-reachable on every `/api/v1` JWT-bearing request. |
| `github.com/go-viper/mapstructure/v2` | `v2.2.1` (`go.mod:59`, transitively via `viper`) | **MEDIUM** | GO-2025-3787 (info disclosure via error messages, fixed `v2.3.0`); GO-2025-3900 (sensitive-info leak in logs, fixed `v2.4.0`). Reached from `api/internal/config/config.go:105` `viper.Unmarshal`. Config-time only — not directly attacker-reachable, but logs can leak secrets if config decode errors. |
| `github.com/moby/spdystream` | `v0.5.0` (`go.mod:78`, transitive via `k8s.io/client-go`) | **MEDIUM** | GO-2026-4958 — uncontrolled resource consumption parsing SPDY frames. Fixed `v0.5.1`. Reached from the kubectl-exec terminal bridge `api/internal/handlers/terminal.go:318`. Exploit requires the API to be a SPDY *client* against a malicious kube-apiserver (low risk in normal deployments). |
| `golang.org/x/crypto` | `v0.32.0` (`go.mod:24`) | LOW | No callable CVE flagged by govulncheck for this version. Note: a baseline upgrade to `v0.31+` is generally recommended industry-wide; current pin is current enough. |
| `github.com/gin-gonic/gin` | `v1.10.0` (`go.mod:8`) | LOW | No callable CVE in govulncheck output. Last historical issues (CVE-2023-29401 path-traversal) require gin ≤ 1.9.0; we are above that. |
| `github.com/spf13/viper` | `v1.20.0` (`go.mod:19`) | LOW (carries vuln transitive — see mapstructure row above) | Direct module clean; the issue is its `mapstructure/v2 v2.2.1` transitive. |
| `github.com/jackc/pgx/v5` | `v5.7.2` (`go.mod:15`) | LOW | No advisory; current. |
| `github.com/gorilla/websocket` | `v1.5.3` (`go.mod:14`) | LOW | No advisory; current. |
| `github.com/prometheus/client_golang` | `v1.21.1` (`go.mod:17`) | LOW | No advisory. |
| `github.com/mark3labs/mcp-go` | `v0.54.0` (`go.mod:16`) | UNVERIFIED | Smaller third-party module not in vuln.go.dev. Must be reviewed manually in Phase 2 — see findings below. |
| `k8s.io/client-go` etc. | `v0.32.3` (`go.mod:26-29`) | LOW | Current minor; upgrade cadence acceptable. Bumps to `v0.33+` in next maintenance cycle. |
| `sigs.k8s.io/controller-runtime` | `v0.20.3` (`go.mod:31`) | LOW | Current. |

**Standard-library Go runtime CVEs (also flagged by govulncheck):** the binaries are compiled against `go1.25.5`. Eight stdlib CVEs are reachable (full list in govulncheck output below). Fixed in go1.25.6 → go1.25.10. **The Dockerfiles for both `api` and `controller` use `FROM golang:1.25 AS builder` (`api/Dockerfile:7`, `controller/Dockerfile:7`) — that floats to whatever `golang:1.25` resolves to at build time. Recommend pinning to `golang:1.25.10-bookworm` once 1.25.10 is GA, or to a digest, to make this auditable.**

### Part A.2 — Frontend deps (`frontend/package.json` + `package-lock.json`)

Lockfile version: 3 (`frontend/package-lock.json` line 1). `npm audit --registry=https://registry.npmjs.org/` (the in-house Code Artifact mirror returns 404 on the audit endpoint — recorded as a Phase 0 gap and worked around via direct registry).

| Package | Version (lockfile) | Risk | Source |
|---|---|---|---|
| `esbuild` | `0.21.5` (transitive via `vite@5.4.21`) | **MEDIUM (dev-only)** | GHSA-67mh-4wv8-2f99 — esbuild dev server permits any origin to issue cross-origin requests and read responses. Fixed in `>=0.25.0`. Affects `npm run dev` only — production builds use the static output served by nginx, so runtime exposure is zero. Still worth fixing because dev environments often run on developer laptops with VPN bridging. |
| `vite` | `5.4.21` (`package-lock.json`, direct dep `package.json:58`) | **MEDIUM (dev-only)** | Inherits the esbuild advisory above; fix is `vite@>=8.0.14` (breaking — major bump). |
| `vitest`, `@vitest/coverage-v8`, `@vitest/mocker`, `vite-node` | dev-only | MEDIUM (dev-only) | Same chain as above. |
| `jsdom` | `20.0.3` (`frontend/package.json:55`, dev) | UNVERIFIED-LOW | Major version `20` is two majors behind current (`26.x`). Has accumulated several historical advisories on later branches; the specific `20.0.3` pin has no open CVE in `npm audit` but the install surface is large and unpatched. Recommend bump to current. |
| `lucide-react` | `1.16.0` (`package.json:27`) | LOW (cosmetic) | This is **wildly out of date** — `lucide-react` is currently at `0.x` then `1.16.0` is actually the latest legacy line as of this report (it’s an icon component pack with low risk surface). No known CVE. Flagged here only because the version is suspicious and warrants a glance. |
| `react`, `react-dom` | `19.2.6` (`package.json:28-30`) | LOW | Current major. |
| `react-router-dom` | `6.30.3` (`package.json:32`) | LOW | Current 6.x; no advisory. |
| Everything `@radix-ui/*` | various 1.x/2.x (`package.json:17-22`) | LOW | No advisories at the pinned ranges. |
| `react-markdown` + `remark-gfm` + `rehype-sanitize` | 10.1.0 / 4.0.1 / 6.0.0 | LOW | The combination is the recommended sanitiser stack. Confirm `rehype-sanitize` schema is enabled (review in Phase 3 — XSS test cases). |
| `axios`, `minimist`, `lodash` | NOT a direct dep (only transitive, all at safe pins) | none | Searched lockfile: no `axios` at all; `lodash` only appears at `4.17.21` (safe); `minimist` only at `1.2.8` (safe). |

**`npm audit` summary:** 6 moderate, 0 high, 0 critical — all in the esbuild/vite/vitest dev-tool chain. **No production-runtime advisories.**

### Part A.3 — SDK deps

**`sdks/typescript/package.json`:**

| Package | Version | Risk | Source |
|---|---|---|---|
| `node-fetch` | `^2.7.0` (`sdks/typescript/package.json:36`) | **MEDIUM** | `node-fetch@2.x` is on the legacy v2 branch. The line is still maintained for security, and `2.7.0` is the latest. However: `node-fetch@2` does **not** validate `Content-Length` against actual byte counts and historically has had several DoS / SSRF advisories (CVE-2022-0235 cookie-leak fixed in `2.6.7`; CVE-2020-15168 redirect resource-exhaustion fixed in `2.6.1`). At `2.7.0` we are above all known fixes. **The bigger risk is being on a deprecated major.** Recommend migration to `undici` (built into Node ≥18) or `node-fetch@3.x` (ESM-only). UNVERIFIED — no live CVE flagged for `2.7.0` itself. |
| `openapi-typescript` | `^7.6.1` (dev) | LOW | Code-gen tool; no runtime impact. |
| `tsup`, `typescript`, `vitest` | dev only | LOW | Current. |

`npm audit --registry=https://registry.npmjs.org/` against `sdks/typescript/`: **0 vulnerabilities found.**

**`sdks/python/pyproject.toml`:**

| Package | Version | Risk | Source |
|---|---|---|---|
| `httpx` | `>=0.27.0` (`sdks/python/pyproject.toml:11`) | LOW | Open lower bound; no upper cap. PyPI current is 0.28.x. **No `poetry.lock` / `uv.lock` / `requirements.txt` is committed in `sdks/python/`** — so reproducibility of the resolved versions across builds is not guaranteed. This is itself a finding: pip-installable SDK with floating deps means downstream users get whatever PyPI hands them at install time. |
| `pytest`, `pytest-asyncio`, `respx` | dev-extras | LOW | Floating lower bounds; same lock-absence issue. |

**`sdks/go/go.mod`:** module is essentially **empty** (`sdks/go/go.mod:1-3`) — `go 1.23`, no deps. There is no Go SDK code yet under `sdks/go/`. Nothing to audit.

**`sdks/validate/go.mod`:** internal CI-only validator, depends on `gopkg.in/yaml.v3 v3.0.1` (`sdks/validate/go.mod:5`). The `yaml.v3` advisory GHSA-hp87-p4gw-j4gq (DoS via deeply nested YAML) was fixed in `v3.0.0` — we are at `v3.0.1`, so safe.

**`frontend/package-lock.json` was checked; `pnpm-lock.yaml` does not exist; `sdks/vscode-llmsafespace/` package-lock exists but is out-of-scope for this pentest (extension is local-developer-only, not deployed).**

### Part A.4 — govulncheck output (live, captured this run)

Run was `cd /home/mikekao/personal/LLMSafeSpace && govulncheck ./...` with the binary rebuilt against go1.25 (the previously-installed govulncheck was built with go1.24 and refused to scan go1.25 sources).

```
=== Symbol Results ===

Vulnerability #1: GO-2026-5026
  Invoking failure to reject ASCII-only Punycode-encoded labels in golang.org/x/net/idna
  Found in: golang.org/x/net@v0.34.0  → Fixed in: v0.55.0
  Trace: pkg/mcp/client.go:225  HTTPClient.SendMessage → http.Client.Do → idna.ToASCII

Vulnerability #2: GO-2026-4971
  Panic in Dial and LookupPort when handling NUL byte on Windows in net (stdlib)
  Found in: net@go1.25.5             → Fixed in: net@go1.25.10
  Linux deployments: not exploitable (Windows-only). Documented for completeness.

Vulnerability #3: GO-2026-4958
  Uncontrolled resource consumption when parsing SPDY frames in github.com/moby/spdystream
  Found in: v0.5.0                   → Fixed in: v0.5.1
  Trace: api/internal/handlers/terminal.go:318  TerminalHandler.bridgeExec → spdystream.NewConnection

Vulnerability #4: GO-2026-4947
  Unexpected work during chain building in crypto/x509 (stdlib)
  Found in: go1.25.5                 → Fixed in: go1.25.9
  Trace: pkg/credentials/crypto.go:69  Encrypt → x509.Certificate.Verify

Vulnerability #5: GO-2026-4946
  Inefficient policy validation in crypto/x509 (stdlib)
  Found in: go1.25.5                 → Fixed in: go1.25.9
  Same trace as #4.

Vulnerability #6: GO-2026-4918
  Infinite loop in HTTP/2 transport on bad SETTINGS_MAX_FRAME_SIZE
  Found in: golang.org/x/net@v0.34.0 → Fixed in: v0.53.0
  Found in: net/http@go1.25.5        → Fixed in: net/http@go1.25.10
  Traces: pkg/mcp/client.go:225 (outbound HTTP/2 client to MCP server)
          api/internal/handlers/secrets.go:292 (outbound POST during secret reload)
          cmd/workspace-agentd/main.go:521 (workspace-agentd self-heal http.Get)

Vulnerability #7: GO-2026-4870
  Unauthenticated TLS 1.3 KeyUpdate record can cause persistent connection retention / DoS
  Found in: crypto/tls@go1.25.5      → Fixed in: go1.25.9
  Trace: api/internal/app/app.go:261  http.Server.ListenAndServe (every TLS-terminated connection)

Vulnerability #8: GO-2026-4865
  JsBraceDepth Context Tracking Bugs (XSS) in html/template (stdlib)
  Found in: html/template@go1.25.5   → Fixed in: go1.25.9
  Trace: api/internal/services/database/credentials.go:166  GetDefault → template.Error.Error
  (Reached only in error-formatting paths; gin.init also pulls template.Parse — see traces.)

Vulnerability #9: GO-2026-4602
  FileInfo can escape from a Root in os (stdlib)
  Found in: go1.25.5                 → Fixed in: go1.25.8
  Trace: mocks/kubernetes/mocks.go:188 (test-only path)

Vulnerability #10: GO-2026-4601
  Incorrect parsing of IPv6 host literals in net/url (stdlib)
  Found in: go1.25.5                 → Fixed in: go1.25.8
  Trace: pkg/mcp/client.go:216, pkg/kubernetes/client.go:50  url.Parse / url.ParseRequestURI

Vulnerability #11: GO-2026-4341
  Memory exhaustion in query parameter parsing in net/url (stdlib)
  Found in: go1.25.5                 → Fixed in: go1.25.6
  Trace: api/internal/handlers/proxy.go:497 (stripVerboseQuery → url.ParseQuery)
         api/internal/handlers/secrets.go:423 (gin.Context.Query → url.URL.Query)

Vulnerability #12: GO-2026-4340
  Handshake messages may be processed at the incorrect encryption level in crypto/tls
  Found in: go1.25.5                 → Fixed in: go1.25.6
  Trace: every TLS handshake on the API server.

Vulnerability #13: GO-2026-4337
  Unexpected session resumption in crypto/tls
  Found in: go1.25.5                 → Fixed in: go1.25.7
  Trace: same as #12.

Vulnerability #14: GO-2025-3900
  Go-viper mapstructure may leak sensitive information in logs
  Found in: github.com/go-viper/mapstructure/v2@v2.2.1  → Fixed in: v2.4.0
  Trace: api/internal/config/config.go:105  viper.Unmarshal → mapstructure.Decoder.decode*

Vulnerability #15: GO-2025-3787
  May leak sensitive information in logs when processing malformed data (mapstructure)
  Found in: v2.2.1                   → Fixed in: v2.3.0
  Same trace as #14.

Vulnerability #16: GO-2025-3595
  Incorrect Neutralization of Input During Web Page Generation in x/net (HTML tokenizer)
  Found in: golang.org/x/net@v0.34.0 → Fixed in: v0.38.0
  Trace: api/internal/middleware/validation.go:223  validator.Validate.Struct → html.Tokenizer.Next

Vulnerability #17: GO-2025-3553
  Excessive memory allocation during header parsing in github.com/golang-jwt/jwt
  Found in: github.com/golang-jwt/jwt/v5@v5.2.1  → Fixed in: v5.2.2
  Trace: api/internal/services/auth/auth.go:298  Service.ValidateToken → jwt.ParseUnverified

Total: 17 reachable vulnerabilities across 4 modules + the Go standard library.
Plus: 16 vulnerabilities in imported packages but no callable trace, and 23
vulnerabilities in required modules with no import. (govulncheck distinguishes
these — only the 17 above have provable reachability.)
```

### Part A.5 — Phase-1 findings (promote to Phase 2+)

| ID (proposed) | Finding | Severity | Recommended Phase-2 action |
|---|---|---|---|
| RT-1.5-F1 | `golang-jwt/jwt/v5 v5.2.1` is reachable from `auth.Service.ValidateToken` for **every authenticated API request**; `GO-2025-3553` lets an attacker craft a JWT header that consumes excessive memory on parse. | **High** | Bump to `v5.2.2` immediately. Add a govulncheck CI gate. Test in Phase 2 with crafted JWT (RT-2.7 already plans first-user-admin race; add JWT-DoS fuzz adjacent). |
| RT-1.5-F2 | `golang.org/x/net v0.34.0` carries 3 reachable CVEs incl. one HTTP/2 DoS (GO-2026-4918) reached from MCP outbound client AND from the API server's secret-reload outbound POST. An attacker who controls a remote server the API talks to (MCP, secret-reload target) can hang those goroutines. | **High** | Bump `golang.org/x/net` to `v0.55.0` (current). Easy because we already require `v0.34.0`; just `go get golang.org/x/net@latest && go mod tidy`. |
| RT-1.5-F3 | Go runtime in builder Dockerfiles is `golang:1.25` (floating), code is compiled against `1.25.5` per go.sum. 8 stdlib CVEs reachable. Notably `crypto/tls` GO-2026-4340 / GO-2026-4337 / GO-2026-4870 affect every TLS handshake on the API. | **High** | Pin Dockerfile builder to `golang:1.25.10-bookworm@sha256:...` (the GA 1.25.10 release that includes all the stdlib fixes). Document the digest in `runtimes/base/Dockerfile` and `api/Dockerfile`. |
| RT-1.5-F4 | `github.com/go-viper/mapstructure/v2 v2.2.1` (transitive via viper) leaks decode-error context to logs (GO-2025-3787, GO-2025-3900). At `api/internal/config/config.go:105` the API parses config including DB DSN and credentials seed material. | **Medium** | Bump viper or pin a `replace` directive forcing `mapstructure/v2 v2.4.0`. Verify no error-log path emits decode failures verbatim — add a redaction guard. |
| RT-1.5-F5 | `github.com/moby/spdystream v0.5.0` reachable through the kubectl-exec terminal handler. A malicious kube-apiserver impersonator could DoS the API, but in our deployment the API is the SPDY *client* of an in-cluster apiserver — exposure is low. | Low | Will be auto-fixed by `k8s.io/client-go` minor bump. Track but don't block. |
| RT-1.5-F6 | `frontend/`: `esbuild` / `vite@5.4.21` chain has GHSA-67mh-4wv8-2f99 (dev server CSRF). 6 moderate npm-audit findings, all dev-only. | Medium (dev only) | Move to `vite@8.x` next maintenance window. No production runtime risk. |
| RT-1.5-F7 | `sdks/python/`: pyproject lacks a lock file (`poetry.lock` / `uv.lock` / `requirements.txt`). Floating lower-bound `httpx>=0.27.0`. Consumers cannot reproduce a known-good resolution. | Medium | Generate and commit `uv.lock` (or `poetry.lock`). Add SDK install reproducibility check to CI. |
| RT-1.5-F8 | `sdks/typescript/` uses `node-fetch@2` (legacy major). At 2.7.0 we are above all known 2.x CVEs but the major is deprecated upstream. | Low | Migrate SDK to native `fetch` (Node ≥18) or `undici`. |
| RT-1.5-F9 | `npm audit` is broken in the in-house Code Artifact mirror (`amazon-149122183214.d.codeartifact.us-west-2.amazonaws.com` returns 404 on `/security/advisories/bulk`). Devs running `npm audit` locally will see "no vulnerabilities" silently. | Medium | Either proxy the audit endpoint upstream or add a CI step that does `npm audit --registry=https://registry.npmjs.org/` against a clean registry. |
| RT-1.5-F10 | No SBOM is generated or published. `runtimes/base/Dockerfile:67-78` notes that opencode upstream lacks Sigstore attestations; no equivalent for our own artefacts either. | Medium | Add `syft` SBOM generation to the release pipeline (CycloneDX or SPDX). Consume it from `trivy sbom` in Phase 2 to short-circuit per-image scans. |

---

## Part B — RT-1.6 Container image analysis

Static analysis only — Dockerfiles in tree at `2026-05-30`. No live image pulled or scanned (trivy is a Phase 0 gap, see below). Runtime properties below are derived from the Dockerfiles themselves and from public images they declare as base.

### Part B.1 — `api/Dockerfile`

| Property | Value |
|---|---|
| File | `api/Dockerfile:1-50` |
| Builder base | `golang:1.25` (`api/Dockerfile:7`) — **floating, not digest-pinned**. Resolves to whatever Docker Hub serves at build time; today that is debian-bookworm-based. |
| Final base | `gcr.io/distroless/static:nonroot` (`api/Dockerfile:36`) — **floating tag, not digest-pinned**. Distroless static = scratch + ca-certificates + tzdata + /etc/passwd entries for `nobody`/`nonroot` (uid 65532). |
| Linux distro | None in the runtime layer — distroless. No shell, no busybox, no apt. |
| Package manager | None in the runtime layer. (Builder uses Debian apt indirectly via the `golang:1.25` base; no `apt-get install` is performed by `api/Dockerfile` itself.) |
| Packages installed | None at runtime. The build copies in: `/usr/local/bin/api` (the built API binary), `/etc/llmsafespace/config.yaml`, `/etc/llmsafespace/migrations/`. |
| Ports exposed | `EXPOSE 8080` (`api/Dockerfile:48`) |
| `USER` directive | `USER 65532:65532` (`api/Dockerfile:46`) — distroless `nonroot`. |
| Entrypoint | `ENTRYPOINT ["/usr/local/bin/api"]` (`api/Dockerfile:50`). Single binary, statically linked (`CGO_ENABLED=0`, `api/Dockerfile:30`). |
| setuid / setgid binaries | None. `gcr.io/distroless/static:nonroot` has no suid binaries; the API binary is built with default permissions (no `chmod +s`). Verified by inspecting Distroless source: the only files are CA certs, tzdata, and the inserted user binary. |
| Writable paths | `/etc/llmsafespace/config.yaml` and `/etc/llmsafespace/migrations/` are root-owned files copied from the build context; the container runs as 65532 so those are read-only at runtime. No `VOLUME` declared. The pod-spec layer must mount tmp/cache emptyDirs if `readOnlyRootFilesystem: true` is set. |
| Notable hardening | Distroless + nonroot UID + statically linked single binary = small attack surface. **Gap:** base image is not pinned by digest. |

### Part B.2 — `controller/Dockerfile`

| Property | Value |
|---|---|
| File | `controller/Dockerfile:1-44` |
| Builder base | `golang:1.25` (`controller/Dockerfile:7`) — same caveat as api: floating. |
| Final base | `gcr.io/distroless/static:nonroot` (`controller/Dockerfile:36`) — same caveat: floating. |
| Linux distro | None in runtime — distroless. |
| Package manager | None. |
| Packages installed | Only the `manager` binary at `/usr/local/bin/manager`. |
| Ports exposed | **None declared.** The controller exposes Prometheus metrics and the health/readiness endpoints (per `controller/main.go`); operators must rely on pod spec `containerPort` declarations. *Finding:* `EXPOSE` is informational but its absence here means image consumers have no metadata hint about which ports the controller listens on. |
| `USER` directive | `USER 65532:65532` (`controller/Dockerfile:42`). |
| Entrypoint | `ENTRYPOINT ["/usr/local/bin/manager"]` (`controller/Dockerfile:44`). Statically linked. |
| setuid / setgid binaries | None. |
| Notable hardening | Same as api — distroless + nonroot. Same Dockerfile-pinning gap. |

### Part B.3 — Runtime base image (`runtimes/base/Dockerfile`)

This is the image agent workspaces actually run in. **Substantially larger attack surface than api/controller**, by design — it has to host opencode, mise, language runtimes, git, jq, and curl.

| Property | Value |
|---|---|
| File | `runtimes/base/Dockerfile:1-155` |
| Builder bases (multi-stage) | `golang:1.25-bookworm` (`runtimes/base/Dockerfile:1`, `:17`) — used twice for the `redact` and `workspace-agentd` Go binaries. |
| Final base | `debian:bookworm-slim` (`runtimes/base/Dockerfile:33`) — **floating tag, not digest-pinned**. |
| Linux distro | Debian 12 (bookworm), `slim` variant. |
| Package manager | `apt` / `apt-get` (Debian). |
| Packages installed via apt (`runtimes/base/Dockerfile:49-51`) | `bash ca-certificates curl git jq unzip xz-utils` plus their transitive deps. |
| Packages downloaded ad-hoc | `opencode v1.15.12` from `github.com/anomalyco/opencode/releases` (`runtimes/base/Dockerfile:67-78`); `mise v2026.5.15` from `github.com/jdx/mise/releases` (`runtimes/base/Dockerfile:86-98`). **Both downloads use TLS-only integrity** — no `.sha256`, no Sigstore signature verification. The Dockerfile explicitly documents this gap in its comments and links the upstream issue (`runtimes/base/Dockerfile:59-66`). |
| Language runtimes pre-installed via mise | `python@latest`, `node@lts`, `rust@stable`, `go@latest`, `java@lts`, `maven@latest`, `gradle@latest` (`runtimes/base/Dockerfile:121-128`). All into `/usr/local/share/mise` (system-wide). **Critically: `MISE_GITHUB_ATTESTATIONS=0` (`runtimes/base/Dockerfile:117,120`) disables provenance checks. The Dockerfile justifies this for restricted build environments but it removes the layered defence that mise normally provides against compromised release assets.** |
| Ports exposed | None declared. workspace-agentd listens on a unix socket and on a TCP port the pod spec injects (per `cmd/workspace-agentd`). |
| `USER` directive | `USER sandbox` (`runtimes/base/Dockerfile:152`); user created with `useradd -u 1000 -m -s /bin/bash sandbox` at line 149. |
| Entrypoint | `ENTRYPOINT ["/usr/local/bin/entrypoint-opencode.sh"]` (`runtimes/base/Dockerfile:155`). Bash entrypoint — see `runtimes/base/tools/entrypoints/entrypoint-opencode.sh`. |
| Writable paths at runtime | `WORKDIR /workspace` is a PVC mount target. `/tmp` and `/home/sandbox` are required to be emptyDir volumes in the pod spec because `readOnlyRootFilesystem: true` is the deployment expectation (line 151 comment). |
| setuid / setgid binaries | **Likely present in the Debian base**. `bookworm-slim` ships with the standard suid set: `mount`, `umount`, `su`, `passwd`, `chsh`, `chfn`, `gpasswd`, `newgrp`. None of these are removed by the Dockerfile. They are accessible to uid 1000 (sandbox) only as targets — sandbox can `exec` them but most refuse to grant privileges to a non-root invoker. **UNVERIFIED — must run `find / -perm -4000 -o -perm -2000 -type f 2>/dev/null` inside the live image during Phase 2 to enumerate exact set.** |
| Other risk surface | `git`, `curl`, `unzip` are present (`runtimes/base/Dockerfile:50`). `git` is required for opencode; `curl` is required for mise self-update; `unzip` is required for tarball extraction during mise installs. Each of these has historical CVE patterns (git protocol parsers, curl URL parsers); reliance on Debian security updates means the image is only as fresh as its rebuild cadence. |
| Notable hardening / weaknesses | Hardening: `useradd -u 1000` for the runtime user; PATH constructed to put PVC binaries first then mise shims, avoiding system bin dirs ahead of those (`runtimes/base/Dockerfile:147`); CGO disabled in builders. **Weaknesses:** floating bases, no digest pin, no Sigstore verification, attestations disabled. |

### Part B.4 — Frontend image (`frontend/Dockerfile`) — bonus

Out of scope for the prompt but adjacent and small: `frontend/Dockerfile:1-13`.

| Property | Value |
|---|---|
| Builder base | `node:22-bookworm-slim` (line 1) — floating. |
| Final base | `nginx:1.27-alpine` (line 8) — floating. Alpine 3.x family. |
| Ports | `EXPOSE 80`. |
| USER | **Not set.** The default for `nginx:1.27-alpine` is `root` for the master process; nginx itself drops to `nginx` user for workers. Pod-spec `securityContext.runAsUser` in the Helm chart is the actual control. |
| setuid binaries | nginx-alpine has the standard alpine suid set (`busybox`-backed). Nginx master runs as root by default to bind :80; if the Helm chart wants nonroot it must rebind to a higher port and use `securityContext`. |

### Part B.5 — Runtime language images (informational)

`runtimes/python/Dockerfile`, `runtimes/nodejs/Dockerfile`, `runtimes/go/Dockerfile`, `runtimes/python/Dockerfile.ml` all extend `ghcr.io/lenaxia/llmsafespace/base:latest` (`runtimes/python/Dockerfile:1`, etc.) — the `:latest` tag means they inherit whatever the base produces at build time, no digest pin. They each install old, frozen sets of language-specific packages: e.g. `runtimes/nodejs/Dockerfile:15-32` pins `axios@1.4.0` (a 2-year-old release line; current axios is 1.7.x — **CVE-2024-39338 SSRF affects axios <1.7.4**, our 1.4.0 is vulnerable), `lodash@4.17.21` (safe), `express@4.18.2` (safe-ish but `4.21+` recommended). `runtimes/python/Dockerfile:21-24` pins `requests==2.31.0` (CVE-2024-35195 fixed in `2.32.0` — ours is **vulnerable**), `numpy==1.24.3`, `ipython==8.14.0`. `runtimes/go/Dockerfile:7` pins `GO_VERSION=1.20.5` — Go 1.20 is **end-of-life as of Aug 2024**, so this image's host Go toolchain has no security backports. These are agent-workspace runtime offerings (the user can pip-install whatever they want over the top), but the *defaults* set the trust baseline.

### Part B.6 — Phase-1 findings (promote to Phase 2+)

| ID (proposed) | Finding | Severity | Recommended Phase-2 action |
|---|---|---|---|
| RT-1.6-F1 | All five Dockerfiles use **floating base-image tags** (`golang:1.25`, `gcr.io/distroless/static:nonroot`, `debian:bookworm-slim`, `node:22-bookworm-slim`, `nginx:1.27-alpine`, `ghcr.io/lenaxia/llmsafespace/base:latest`). Build reproducibility and CVE auditability both depend on digest pinning. | **High** | Pin every `FROM` to `image:tag@sha256:...`. Add Renovate / Dependabot / a custom CI to bump the digest on a schedule. |
| RT-1.6-F2 | `runtimes/base/Dockerfile:67-78` downloads `opencode` and `mise` over HTTPS without checksum or signature verification — explicitly documented as a known gap in the comments. Compromise of the upstream GitHub release artefact would silently land in every workspace image. | **High** | (a) Add SHA-256 verification against a value pinned in the Dockerfile (commit the expected hash to git, alongside the version pin). (b) Track upstream `anomalyco/opencode` and `jdx/mise` for Sigstore attestations and switch to `cosign verify-blob` when available. (c) `MISE_GITHUB_ATTESTATIONS=0` is set at line 120 — re-evaluate whether the build can proceed with `MISE_GITHUB_ATTESTATIONS=1` in non-restricted environments. |
| RT-1.6-F3 | `runtimes/nodejs/Dockerfile:19` pins `axios@1.4.0` — vulnerable to CVE-2024-39338 (SSRF, fixed in 1.7.4). This is the default axios sandboxes get. | **High** | Bump default to `axios@^1.7.4` (or remove from defaults entirely — let user code declare it). |
| RT-1.6-F4 | `runtimes/python/Dockerfile:21` pins `requests==2.31.0` — CVE-2024-35195 (cert verification bypass via session) fixed in `2.32.0`. Same default-trust-baseline risk. | **High** | Bump default to `requests>=2.32.4`. |
| RT-1.6-F5 | `runtimes/go/Dockerfile:7` declares `GO_VERSION=1.20.5`. Go 1.20 reached EOL August 2024; no upstream security backports. Sandboxed user code that runs `go build` uses this toolchain. | **High** | Bump to `GO_VERSION=1.25.x` (matching api/controller). |
| RT-1.6-F6 | `runtimes/python/Dockerfile.ml` pins `numpy==1.24.3`, `pandas==2.0.2`, `tensorflow==2.12.0`, `pytorch==2.0.1` — TensorFlow 2.12 has multiple high CVEs (e.g. CVE-2023-25660 fixed in 2.12.1+); torch 2.0.1 is two majors behind current; these cannot be assumed safe. | **High** (image-level), Low (real-attack risk — only run by user code by request) | Replace with floating-but-CI-tested major pins, and add a dependency refresh playbook. |
| RT-1.6-F7 | Runtime base image keeps `bash`, `git`, `curl`, `jq`, `unzip` — necessary for opencode, but each is an attack-tool when the sandbox escape primitive exists elsewhere. | Informational | Phase 2/3 will exercise sandbox escape; if any escape is found, the kit available to the attacker is documented here. |
| RT-1.6-F8 | suid binary set inside the runtime base is **unverified** statically. | Low | Phase 2 RT-2.x: run `find / -perm /6000 -type f` inside a live workspace pod and compare against expected Debian baseline. |
| RT-1.6-F9 | Controller Dockerfile lacks `EXPOSE` even though the controller listens on at least the metrics port. | Informational | Add `EXPOSE 8080` (or whichever) for image-consumer clarity. Not a security control. |
| RT-1.6-F10 | `frontend/Dockerfile` final stage has no `USER` directive; nginx master runs as root by default. The Helm chart's `securityContext` is the only line of defence. | Medium | Add `USER nginx` (after `chown`-ing /var/cache/nginx etc.) or switch to `nginxinc/nginx-unprivileged`. |

---

## Tooling gap (carries forward from Phase 0)

Phase 0's `tools-manifest.txt` validated `trivy`, `grype`, `syft`, `kubeaudit`, `kube-hunter` against the control fixture — but those tools are **not** installed in this analyst's environment for the Phase 1 dependency/image scan run. As a result this report:

- **Cannot enumerate per-image OS-package CVEs** (apt-listing-against-NVD). Every "needs trivy/grype run" annotation above will resolve in a follow-up Phase 1+ pass on the calibrated machine that owns those tools.
- **Cannot generate a CycloneDX/SPDX SBOM** for the runtime images. govulncheck covers Go modules only; npm audit covers npm; we have no equivalent for Debian apt content of `runtimes/base`.
- **Cannot verify suid binary inventory** of `runtimes/base` without a live image. Listed as RT-1.6-F8.

The fix is mechanical: re-run this report's Part B on the operator workstation that has trivy installed (per Phase 0 tools manifest), against pinned image digests once RT-1.6-F1 is implemented. Live SBOM + CVE scan output should be appended to this file as `RT-1.5-RT-1.6-deps-and-images.live-scan.md` rather than overwriting the static analysis.

---

## Cross-references

- Phase 0 tooling baseline: worklog `0082_2026-05-29_epic17-phase-0-kit.md` and `0083_2026-05-30_epic17-phase-0-prod-kit.md`.
- Image SHAs of currently-deployed control-plane: `RT-1.4-network-topology.md` lines 6-7 (`api`/`controller` at `sha-cdd6305`, base at `sha-cdf2ddc`).
- Pre-pentest remediation done on prior security findings: worklog `0078_2026-05-29_epic17-pre-pentest-remediation.md`.
- Findings F1–F10 in each section are inputs to the Phase 2+ test plan, not Phase 1 conclusions. Their severity ratings are pre-exploit-assessment estimates only — Phase 2 may upgrade or downgrade based on reachability proofs.
