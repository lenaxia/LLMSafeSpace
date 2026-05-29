# Epic 14 Validation Report

**Date:** 2026-05-29
**Validator:** Skeptical review per Rule 7 (Assumptions: State, Then Validate)

---

## 1. Internal Consistency

### Findings

| # | Issue | Severity | Location |
|---|-------|----------|----------|
| IC-1 | **Story map dependency arrow incorrect.** US-14.7 (Contract Tests) depends on US-14.3 only, but logically it should depend on ALL SDKs being at least partially implemented to validate them. The story says "at least one SDK must exist" which is fine, but the parallelization plan puts US-14.7 in Week 3 alongside US-14.5 and US-14.6 — meaning it can't test those SDKs until they're done in the same week. | Low | README.md story map |
| IC-2 | **US-14.1 claims `swag init` generates baseline.** But the swagger.go file has `"paths": {}` — it's a stub with zero endpoint annotations. There are NO `@Router`, `@Summary`, or `@Param` annotations anywhere in the handler code. `swag init` will produce an empty spec. | **Critical** | US-14.1 |
| IC-3 | **US-14.1 lists endpoint `POST /workspaces/:id/activate` and `POST /workspaces/:id/suspend`.** The actual routes are `POST /:id/activate` and `POST /:id/suspend` (correct), but also `POST /:id/resume` (not `activate` for resuming). The story conflates `activate` and `resume` — they are separate endpoints in the codebase. | Medium | US-14.1 |
| IC-4 | **US-14.9 references `api/internal/mcp/` path.** The MCP server actually lives at `pkg/mcp/` and `cmd/mcp/main.go`. The `api/internal/mcp/` directory does not exist. | Medium | US-14.9 |
| IC-5 | **US-14.2 says "no new ports."** Correct — but it also says the command is `/bin/sh`. The sandbox runs as UID 1000 with `readOnlyRoot` filesystem. Need to validate that `/bin/sh` is available and executable in the runtime images with those constraints. | Medium | US-14.2 |
| IC-6 | **Proxy routes are under `/api/v1/workspaces/:id/...`** not `/api/v1/sandboxes/:id/...`. The proxy routes (SendMessage, GetHistory, etc.) are registered on the `workspaceGroup`. US-14.3/14.4/14.5/14.6 SDK examples correctly use `workspaces` path. Consistent. | ✅ OK | — |
| IC-7 | **US-14.9 says chat participant sends messages via `client.sessions.sendMessage()`.** This is a synchronous call. But the actual API flow for `session_message` in the MCP server uses `prompt_async` + SSE polling. The SDK (REST-only, no streaming) would need to use the synchronous `POST /:id/sessions/:sessionId/message` endpoint which proxies to opencode and waits for completion. Need to verify this endpoint actually blocks until response is ready. | **High** | US-14.9, US-14.3 |

### IC-7 Deep Dive

Looking at the proxy handler, `SendMessage` proxies to opencode's `POST /session/:id/message` which is synchronous (blocks until LLM responds). This works for the SDK. However, for long-running prompts (>30s), the HTTP connection may timeout. The MCP server handles this via `prompt_async` + SSE. The SDK's synchronous approach needs a configurable timeout and clear documentation that long prompts may timeout.

---

## 2. Consistency with Existing Code

### Findings

| # | Issue | Severity | Location |
|---|-------|----------|----------|
| CC-1 | **No swag annotations exist.** US-14.1 assumes `swag init` produces a usable baseline. In reality, the OpenAPI spec must be written from scratch by reading `router.go` and `types.go`. The effort estimate of "3 days" is likely accurate for hand-writing, but the story's approach section is wrong. | **Critical** | US-14.1 |
| CC-2 | **US-14.2 claims `remotecommand` is "already in go.mod."** `k8s.io/client-go` is in go.mod (confirmed), and `remotecommand` is a sub-package of client-go, so this is technically correct. However, the API server currently communicates with pods via HTTP proxy (pod IP + port), NOT via K8s exec. Adding exec requires the API server's ServiceAccount to have `pods/exec` RBAC permissions, which may not be granted today. | **High** | US-14.2 |
| CC-3 | **The MCP server tools are workspace-centric** (`workspace_create`, `workspace_activate`, `session_create`, `session_message`, `session_history`). US-14.9 correctly aligns with this. The old story US-4.1 referenced `sandbox_create` etc. — the actual implementation uses workspace terminology. Epic 14 is consistent with the CURRENT code. | ✅ OK | — |
| CC-4 | **US-14.2 proposes `gorilla/websocket`.** It's in go.sum (transitive) but NOT in go.mod (not a direct dependency). The codebase has WebSocket security middleware but no actual WebSocket upgrade handler. Adding gorilla/websocket as a direct dependency is a new addition. | Low | US-14.2 |
| CC-5 | **The proxy handler uses pod IP directly** (reads `workspace.Status.PodIP` from the CRD). US-14.2's terminal proxy would need the same pattern — get workspace CRD, extract pod IP, then exec into that pod. But exec uses pod NAME (not IP). The terminal handler needs both: pod name for exec, and the workspace must be Active. | Medium | US-14.2 |
| CC-6 | **US-14.3 SDK example shows `client.sessions.create(workspace.id)`.** The actual API endpoint is `POST /workspaces/:id/sessions/new` which returns `EnsureSessionResponse` (not just a session ID). The SDK wrapper needs to map this correctly. | Low | US-14.3 |

---

## 3. Quality Assessment (All Dimensions)

### Robustness

| Aspect | Assessment |
|--------|-----------|
| Error handling | ✅ All SDK stories define typed error hierarchies. Good. |
| Timeout handling | ⚠️ US-14.9 (chat participant) calls `sendMessage` synchronously. Long LLM responses (60s+) will timeout the HTTP request. Need explicit timeout config and user feedback ("thinking..."). |
| Reconnection | ✅ US-14.10 defines exponential backoff for terminal WebSocket. Good. |
| Graceful degradation | ⚠️ US-14.8 doesn't specify behavior when API is unreachable (show cached data? show error state?). |

### Maintainability

| Aspect | Assessment |
|--------|-----------|
| Single source of truth | ✅ OpenAPI spec drives all SDKs. Good. |
| Code generation | ✅ 80% generated, 20% hand-written. Correct split. |
| Contract tests | ✅ Prevents drift across SDKs. Good. |
| VS Code extension | ⚠️ Extension depends on TypeScript SDK which depends on OpenAPI spec. Three-layer dependency chain means a breaking API change requires: spec update → SDK regen → extension update. Need CI to catch this. |

### Scalability

| Aspect | Assessment |
|--------|-----------|
| Terminal proxy | ⚠️ Each terminal session holds a WebSocket + SPDY exec connection. At scale (100+ concurrent terminals), the API server holds 200+ long-lived connections. Need connection limits per user and per API instance. US-14.2 mentions "max 5 concurrent terminal sessions per workspace" but not per-API-instance limits. |
| SDK generation | ✅ Adding a new language is just a new Makefile target. Scales well. |

### Security

| Aspect | Assessment |
|--------|-----------|
| Terminal auth | ⚠️ US-14.2 passes auth token as `?token=` query param for WebSocket upgrade. Query params appear in access logs, proxy logs, and browser history. Consider: (1) use a short-lived one-time token exchanged via REST before upgrade, or (2) send token in first WebSocket message after upgrade. |
| API key in mcp.json | ⚠️ US-14.9 writes API key into `.vscode/mcp.json` on disk. This file may be committed to git. Need: (1) add to `.gitignore` template, (2) use VS Code's `${env:VAR}` interpolation instead of raw key, or (3) use a reference to VS Code secret storage. |
| SDK credential storage | ✅ Extension uses VS Code SecretStorage (encrypted). Good. |
| Terminal shell | ✅ Fixed `/bin/sh`, non-root user, no configurable command. Good. |

### Performance

| Aspect | Assessment |
|--------|-----------|
| SDK overhead | ✅ Zero-dependency clients (fetch, net/http, httpx). Minimal overhead. |
| Tree refresh | ⚠️ US-14.8 polls every 30s. With 50 workspaces, that's a list call every 30s. Fine for single user, but consider: only refresh visible items, or use SSE events endpoint for push updates. |
| Terminal latency | ✅ WebSocket is low-latency. SPDY exec is the standard K8s mechanism. Good. |

### SOLID Principles

| Principle | Assessment |
|-----------|-----------|
| Single Responsibility | ✅ Each story has one clear deliverable. SDK stories don't mix concerns. |
| Open/Closed | ✅ SDK generation pipeline is extensible (add new language = new target). |
| Liskov Substitution | ✅ All SDKs implement the same logical interface (same operations, same error types). |
| Interface Segregation | ✅ MCP client interface (`APIClient`) is minimal — only the operations MCP needs. |
| Dependency Inversion | ✅ Extension depends on SDK interface, not HTTP details. SDK depends on OpenAPI contract, not server internals. |

### Idiomatic Design

| Language | Assessment |
|----------|-----------|
| Go SDK | ✅ Functional options, context propagation, `(value, error)` returns. Idiomatic. |
| Python SDK | ✅ Sync + async, context managers, Pydantic models. Idiomatic. |
| TypeScript SDK | ✅ Promises, typed errors, ESM + CJS. Idiomatic. |
| Java SDK | ✅ Builder pattern, records, sealed exceptions. Idiomatic for Java 17+. |
| VS Code Extension | ✅ TreeDataProvider, commands, SecretStorage. Standard extension patterns. |

---

## 4. Assumptions Audit

### Stated Assumptions (from the stories)

| # | Assumption | Validated? | Evidence |
|---|-----------|-----------|----------|
| A1 | `swag init` produces a usable OpenAPI baseline | ❌ **FALSE** | `swagger.go` has `"paths": {}`. No handler annotations exist. |
| A2 | `k8s.io/client-go/tools/remotecommand` is available | ✅ True | `k8s.io/client-go v0.32.3` in go.mod; remotecommand is a sub-package |
| A3 | `gorilla/websocket` is already in go.mod | ❌ **FALSE** | Only in go.sum (transitive). Not a direct dependency. |
| A4 | Proxy routes are under `/api/v1/workspaces/` | ✅ True | `workspaceGroup := router.Group("/api/v1/workspaces")` confirmed |
| A5 | MCP server exists and is functional | ✅ True | `cmd/mcp/main.go` + `pkg/mcp/server.go` with 6 tools registered |
| A6 | `POST /workspaces/:id/sessions/:sessionId/message` is synchronous | ⚠️ **Partially** | It proxies to opencode which blocks until LLM responds. Works for short prompts. May timeout for long ones (>30s default HTTP timeout). |
| A7 | Sandbox pods have `/bin/sh` available | ⚠️ **Unvalidated** | Runtime images are Debian bookworm-slim based (likely has sh), but `readOnlyRoot` is set. Need to verify sh is in a writable/executable path. |
| A8 | API server ServiceAccount has `pods/exec` RBAC | ⚠️ **Unvalidated** | Current proxy uses HTTP to pod IP. Exec requires additional RBAC. |
| A9 | VS Code Chat Participant API is stable (1.90+) | ⚠️ **Unvalidated** | Chat Participant API was introduced in VS Code 1.93 (proposed) and stabilized later. Need to verify minimum version. |
| A10 | `openapi-typescript` + `openapi-fetch` produce usable output from OpenAPI 3.1 | ⚠️ **Unvalidated** | These tools are actively maintained but version compatibility with 3.1 should be verified. |

### Unstated Assumptions (discovered during review)

| # | Assumption | Risk |
|---|-----------|------|
| U1 | The API server can hold long-lived WebSocket connections without being killed by load balancers/ingress controllers | Medium — need to configure ingress timeout annotations |
| U2 | Pod exec works through network policies | Medium — existing network policies may block API→pod exec traffic |
| U3 | The OpenAPI spec can be kept in sync with code changes without CI enforcement | High — without CI validation, spec will drift from implementation |
| U4 | VS Code extension can bundle `@llmsafespace/sdk` without size issues | Low — but worth checking bundle size |
| U5 | Prism mock server correctly validates against OpenAPI 3.1 (not just 3.0) | Low — Prism supports 3.1 but edge cases exist |

---

## 5. Remediation Required

### Critical (must fix before implementation)

1. ✅ **FIXED: US-14.1: Rewrite approach section.** Removed swag assumption. Spec is now documented as hand-written from router.go + types.go.

2. ✅ **FIXED: US-14.2: Add RBAC requirement.** Added `pods/exec` RBAC to acceptance criteria, documented Helm chart ClusterRole update, added as epic prerequisite.

3. ✅ **FIXED: US-14.2: Fix auth token in query param.** Replaced with one-time ticket pattern: `POST /terminal/ticket` → short-lived ticket → WebSocket upgrade with ticket. Ticket is single-use, 30s expiry, stored in Redis.

### High (should fix before implementation)

4. ✅ **FIXED: US-14.9: Address synchronous timeout for long prompts.** Added configurable timeout (120s default), progress indicator, graceful timeout message with `/history` fallback.

5. ✅ **FIXED: US-14.9: Fix mcp.json API key exposure.** Now uses `${env:LLMSAFESPACE_API_KEY}` interpolation. Raw key never written to disk. User prompted to set env var.

6. ✅ **FIXED: US-14.2: Validate `/bin/sh` availability.** Added as acceptance criterion with explicit verification command. Added to epic prerequisites.

7. ✅ **FIXED: US-14.8: Add offline/error state behavior.** Added acceptance criterion for disconnected state: last-known data with stale indicator, "Disconnected" status bar, welcome view with configure button.

### Medium (fix during implementation)

8. ✅ **FIXED: US-14.1: Add `resume` endpoint.** Added `POST /workspaces/:id/resume` to endpoint list.

9. ✅ **FIXED: US-14.2: Document pod name resolution.** Added label selector approach with code example. Documented decision to avoid CRD schema change in v1.

10. ✅ **FIXED: US-14.2: Add per-API-instance connection limits.** Added 500 global limit + 5 per-workspace limit with implementation code.

11. ✅ **FIXED: US-14.8/14.9: Fix VS Code minimum version.** Changed to 1.95+ (Chat Participant API stability).

---

## 6. Summary

| Dimension | Grade | Notes |
|-----------|-------|-------|
| Internal consistency | A- | All findings addressed. Endpoint paths, MCP references, and session API mappings corrected. |
| Code consistency | A- | False assumptions (swag, gorilla/websocket) corrected. RBAC gap documented and planned. |
| Robustness | A- | Timeout handling added for long prompts. Offline state defined. Reconnection logic specified. |
| Maintainability | A- | Generation pipeline sound. CI drift detection added to US-14.1. |
| Scalability | A | Connection limits (global + per-workspace) explicitly defined. Pod resolution via label selector. |
| Security | A | Ticket-based auth replaces query-param tokens. mcp.json uses env var interpolation. |
| Performance | A- | Minimal overhead. Polling acceptable for v1. |
| SOLID | A | Clean separation of concerns throughout. |
| Idiomatic | A | Each language follows its ecosystem conventions. |
| Assumptions | A- | All assumptions now stated explicitly with validation status. Unvalidated items flagged with fallback plans. |

**Overall: A-** — All critical, high, and medium findings addressed. Ready for implementation.

**Remaining risks (acceptable):**
- OpenAPI 3.1 tool support across all generators (fallback: use 3.0.3)
- VS Code Chat Participant API stability (mitigated: pin to 1.95+)
- Ingress WebSocket timeout configuration (documented in prerequisites)
