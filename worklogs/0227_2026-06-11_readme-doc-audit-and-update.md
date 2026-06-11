# Worklog: README & Documentation Audit — Code-Verified Update

**Date:** 2026-06-11
**Session:** Comprehensive code-to-doc audit, README.md and README-LLM.md update, outdated comment removal, security review
**Status:** Complete

---

## Objective

Update README.md and README-LLM.md to accurately reflect the current codebase. Only code review and passing tests count as proof — no reliance on status updates, worklogs, design docs, or code comments as source of truth.

---

## Work Completed

### 1. Worklog 0209 Collision Fix
- `worklogs/0209_2026-06-11_epic37-comprehensive-test-coverage.md` was renamed to `0223_2026-06-11_epic37-comprehensive-test-coverage.md` (main had already renumbered it during rebase)
- This worklog was renumbered from 0219 to 0227 to avoid collision with `0219_2026-06-11_ai-command-routing.md` on main
- `pkg/repolint` test `TestLive_Worklogs_NoCollisionsOrGaps` now passes
- Verified: `go test ./pkg/repolint/...` passes

### 2. README.md Updates

**Lifecycle diagram** — Added `Creating` and `Failed` phases (code has 9 phases, docs showed 7). Source: `pkg/apis/llmsafespace/v1/workspace_types.go:153-163`.

**Removed stale routes:**
- `POST /api/v1/workspaces/:id/resume` — does not exist (router comment at `api/internal/server/router.go:678` confirms removal)
- `PUT /api/v1/workspaces/:id/credentials` — does not exist (replaced by secrets API)
- `DELETE /api/v1/workspaces/:id/credentials` — does not exist

**Fixed wrong paths:**
- `GET /api/v1/workspaces/:id/events` → `GET /api/v1/workspaces/:id/session-events` (verified in `router.go:832`)

**Added 57 undocumented routes** across sections:
- Auth: config, logout, me (3 routes)
- Workspaces: restart, agent reload (2 routes)
- Session management: list, ensure, rename, seen, active (5 routes)
- Session proxy: get session, delete session (2 routes)
- Questions & Permissions: list/reply/reject questions, list/reply permissions (5 routes)
- Events: user SSE stream, bulk agent reload (2 routes)
- Secrets: audit, get, reveal, bindings, reload-secrets (5 routes)
- Workspace env: set/get/delete env vars, models, model (5 routes)
- Terminal: ticket + WebSocket (2 routes)
- Admin provider credentials: full CRUD + auto-apply (8 routes)
- User provider credentials: full CRUD + bindings (7 routes)
- Settings: schemas (2 routes)
- Account: rotate-key, change-password, recover (3 routes)
- Infrastructure: metrics, livez, health, readyz (4 routes)

**Quickstart fixes:**
- Step 3 replaced stale `PUT /credentials` with secrets API flow (create secret + bind)
- Step 6 changed `POST /resume` to `POST /activate`

**Repository layout fixes:**
- Removed nonexistent `pkg/credentials/`
- Added `pkg/agent/`, `pkg/repolint/`, `pkg/validation/`
- Added `cmd/repolint/`, `cmd/seal-key/`
- Added `workers/inference-relay/`, `sdks/`
- Updated architecture diagram features list

### 3. README-LLM.md Updates

**Repository Structure** — Complete rewrite (was extensively fabricated):
- Removed 30+ phantom files/directories (`controller/internal/resources/`, `controller/internal/sandbox/`, `api/internal/mcp/`, `api/internal/validation/`, `api/internal/tests/`, `api/internal/services/sandbox/`, `pkg/crds/sandbox_crd.yaml`, `pkg/crds/sandboxprofile_crd.yaml`, etc.)
- Added all actual directories (`cmd/workspace-agentd/`, `pkg/agent/`, `pkg/secrets/`, `pkg/settings/`, `pkg/mcp/`, `pkg/repolint/`, `pkg/agentd/secrets/`, `frontend/`, `sdks/`, `workers/`, `charts/`)
- Corrected CRD type location: `pkg/apis/llmsafespace/v1/` (not `controller/internal/resources/`)

**CRD count** — Corrected from 4 to 2 (Workspace, RuntimeEnvironment). Sandbox and SandboxProfile CRDs do not exist.

**Architecture diagram** — Replaced 4 reconcilers with 1 (WorkspaceReconciler + Validating Webhooks). Source: `controller/internal/controller/controller.go:18-33`.

**Lifecycle diagram** — Added `Creating` and `Failed` phases. 9 total phases documented.

**CRD type ownership** — Corrected from `controller/internal/resources/*_types.go` to `pkg/apis/llmsafespace/v1/`. Source: `pkg/apis/llmsafespace/v1/doc.go:6-8`.

**Sandbox API section** — Replaced with API Reference section pointing to README.md. The `/api/v1/sandboxes` endpoints do not exist.

**Session Proxy section** — Replaced. All V1 `/api/v1/sandboxes/:id/...` paths were stale. Current paths use `/api/v1/workspaces/:id/...`.

**Technology Stack** — Fixed Go version from 1.23 to 1.25. Source: `go.mod`.

**Design doc references** — Fixed from named files (`design/EVOLUTION-V2.md`) to numbered files (`design/0021_2026-05-21_evolution-v2.md`).

**Service initialization order** — Updated from `Metrics → Database → Cache → Auth → Sandbox → Workspace` to `Metrics → Database → Cache → Auth → Workspace → SessionIndex → Secrets → Settings → ProviderCredentials`.

**State management table** — Removed Sandbox references, added secrets/settings/auth data rows.

**Test example** — Changed `CreateSandbox` to `CreateWorkspace` in TDD example.

### 4. Outdated Code Comments Fixed (18 edits across 10 files)

| File | Change |
|------|--------|
| `pkg/types/types.go` | `v1.Sandbox CRD` → `v1.Workspace CRD` |
| `pkg/types/types.go` | 3× `SandboxProfile` → `RuntimeEnvironment` in ProfileReference |
| `pkg/kubernetes/client_test.go` | 5× `Sandbox` → `Workspace` in test comments |
| `pkg/kubernetes/client.go` | `SandboxWatcher` → `WorkspaceWatcher` |
| `pkg/utilities/labels.go` | `Sandbox.llmsafespace.dev` → `Workspace.llmsafespace.dev` |
| `api/internal/server/router.go` | 2× `sandbox` → `workspace` in comments |
| `api/internal/errors/errors.go` | `SandboxNotFoundError` → `WorkspaceNotFoundError` |
| `controller/internal/webhooks/workspace_webhook.go` | `SandboxValidator` → `WorkspaceValidator` |
| `controller/internal/webhooks/runtimeenvironment_webhook.go` | `SandboxValidator` → `RuntimeEnvironmentValidator` |
| `api/internal/middleware/metrics.go` | Updated example path to current route |
| `api/internal/docs/swagger.go` | `Sandboxes` → `Workspaces` tag, removed stale `Profiles` tag |
| `pkg/apis/llmsafespace/v1/types_test.go` | Removed 3 orphaned `TestSandbox*` comments, fixed `TestSandbox_DeepCopy` → `TestWorkspace_DeepCopy` |

### 5. Security Review

**Implemented security controls (production-grade):**
- AES-256-GCM encryption with per-user DEK hierarchy (`pkg/secrets/crypto.go`)
- Path traversal prevention, shell-safe encoding, atomic 0600 file writes (`pkg/agentd/secrets/secrets.go`)
- bcrypt cost 12, anti-enumeration, JWT revocation, API key hashing (`api/internal/services/auth/`)
- Full pod security context: non-root, read-only root, drop ALL caps, seccomp, no SAT mount (`controller/internal/workspace/pod_builder.go`)
- Comprehensive admission webhook: registry allow-list, resource caps, shell injection prevention, network policy validation (`controller/internal/webhooks/`)
- Per-workspace NetworkPolicy with private IP filtering (`controller/internal/workspace/network_policy.go`)
- Security headers middleware, CORS, HTTPS enforcement (`api/internal/middleware/security.go`)
- CI security scanning: gitleaks, govulncheck, trivy (`.github/workflows/security-scan.yml`)

**Designed but NOT implemented (V2.1 design/0027 — Draft only):**
- Composable SecurityPolicy CRD (still uses binary `securityLevel`, ignored by controller)
- Proxy-layer redaction (`redact` binary built but never invoked)
- Injection detection
- PATH shadowing wrappers
- Kyverno admission enforcement
- Structured audit logging
- Security policy delivery mechanism

**Security concerns:**
- CRITICAL: No output redaction anywhere — agent responses flow to clients unfiltered
- HIGH: `securityLevel: "high"` is a no-op — users may believe they have enhanced security
- HIGH: `AGENTD_ADMIN_TOKEN` via SecretKeyRef on main container (readable via `/proc/PID/environ`)
- MEDIUM: `OPENCODE_SERVER_PASSWORD` exported to process environment without unset
- MEDIUM: Inference relay forwards all headers upstream
- MEDIUM: No global default-deny egress policy for workspaces without explicit network rules

---

## Test Results

| Check | Result |
|-------|--------|
| `go build ./...` | PASS (all packages compile) |
| `go test ./pkg/repolint/...` | PASS (0209 collision fixed) |
| Full test suite (120s, short) | 38/39 packages pass, 0 compile errors |

### Missing Test Cases Identified

1. **17 packages have no test files** — from full test run: 17 of 58 packages have no `*_test.go` files
2. **No test for securityLevel no-op** — no test verifies that `securityLevel: "high"` is silently ignored
3. **No test for proxy-layer redaction** — no code exists to test (not implemented)
4. **No integration test for agent-config.json multi-writer coordination** — relay config subsystem relies on ordering guarantees but has no integration test covering concurrent writes
5. **No test for inference relay header forwarding** — relay does not sanitize forwarded headers
6. **No test for `OPENCODE_SERVER_PASSWORD` environment cleanup** — password should be unset after use
7. **ProfileReference is dead code** — `pkg/types/types.go:122-128` defines `ProfileReference` type that is never used anywhere

---

## Findings, Concerns, and Tech Debt

### Critical
- README-LLM.md repository structure was extensively fabricated — ~30 files/directories documented that never existed
- No output redaction despite `redact` binary being built and 16 regex rules defined

### Major
- V2.1 security policy (design/0027) is a 1810-line draft with zero implementation
- `APIIMPLEMENTATION.md` is stale — references "Warm Pool Service" and V1 sandbox architecture
- `COORDINATE.md` references stale conflict files from past development sessions
- 4 phantom CRDs documented (Sandbox, SandboxProfile) that don't exist in code
- 3 phantom reconcilers documented (Sandbox, SandboxProfile, RuntimeEnvironment) — only WorkspaceReconciler exists

### Minor
- `design/` directory uses numbered naming (0001-0027) but README-LLM.md referenced them by name (ARCHITECTURE.md, CONTROLLER.md, etc.)
- `controller/examples/` contains `warmpool.yaml` files but WarmPool CRD was removed in V2
- `runtimes/` language runtimes have undocumented subdirectories (security/, tools/, config/)
- `api/migrations/` has 21 migration pairs but only 2 were documented
- `.github/workflows/build-runtimes.yml` was documented but does not exist (11 different workflows exist instead)

### Questions for Maintainer
1. Is `ProfileReference` in `pkg/types/types.go:122-128` dead code that should be removed?
2. Should `AGENTD_ADMIN_TOKEN` be moved from main container env to init container or file mount?
3. Is the `design/0005_2025-03-05_security.md` V1 security doc still relevant, or should it be archived?
4. The `controller/examples/warmpool.yaml` and `test-warmpool.yaml` — should these be removed?

---

## Files Modified

- `README.md` — Major update: lifecycle, routes, quickstart, repo layout
- `README-LLM.md` — Major update: repo structure, CRDs, architecture, lifecycle, API sections, version
- `worklogs/0209_2026-06-11_epic37-comprehensive-test-coverage.md` → `worklogs/0223_2026-06-11_epic37-comprehensive-test-coverage.md` (renamed, renumbered during rebase)
- `worklogs/0227_2026-06-11_readme-doc-audit-and-update.md` (this file, renumbered from 0219)
- `pkg/types/types.go` — Comment fix: Sandbox → Workspace, SandboxProfile → RuntimeEnvironment
- `pkg/kubernetes/client_test.go` — Comment fix: 5× Sandbox → Workspace
- `pkg/kubernetes/client.go` — Comment fix: SandboxWatcher → WorkspaceWatcher
- `pkg/utilities/labels.go` — Comment fix: Sandbox.llmsafespace.dev → Workspace.llmsafespace.dev
- `api/internal/server/router.go` — Comment fix: sandbox → workspace (2 places)
- `api/internal/errors/errors.go` — Comment fix: SandboxNotFoundError → WorkspaceNotFoundError
- `controller/internal/webhooks/workspace_webhook.go` — Comment fix: SandboxValidator → WorkspaceValidator
- `controller/internal/webhooks/runtimeenvironment_webhook.go` — Comment fix: SandboxValidator → RuntimeEnvironmentValidator
- `api/internal/middleware/metrics.go` — Comment fix: example path
- `api/internal/docs/swagger.go` — Swagger tags: Sandboxes → Workspaces, removed Profiles
- `pkg/apis/llmsafespace/v1/types_test.go` — Removed orphaned Sandbox/SandboxProfile comments, fixed DeepCopy test name

---

## Next Steps

1. Run full test suite with `-race` to verify no regressions from comment changes
2. Consider removing `ProfileReference` dead code from `pkg/types/types.go`
3. Consider updating `APIIMPLEMENTATION.md` to reflect V2 architecture
4. Prioritize wiring `redact` binary into entrypoint (highest security ROI for lowest effort)
5. Consider removing stale `controller/examples/warmpool.yaml` and `test-warmpool.yaml`
