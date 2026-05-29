# Epic 14: Multi-Language SDKs & VS Code Extension

**Status:** Planning
**Created:** 2026-05-29
**Priority:** High
**Depends on:** Epic 3 (Proxy/Sessions), Epic 4 (MCP Server)

## Assumptions (Epic-Level)

These assumptions apply across all stories. Each story also lists its own specific assumptions.

| # | Assumption | Status | Notes |
|---|-----------|--------|-------|
| EA1 | The REST API surface is stable enough to generate SDKs against | ✅ | Epics 1-6 are complete; API routes are stable |
| EA2 | OpenAPI 3.1 has sufficient tooling support across all 4 languages | ⚠️ | Fallback: use OpenAPI 3.0.3 if any generator has 3.1 issues |
| EA3 | `POST /workspaces/:id/sessions/:sessionId/message` is synchronous (blocks until LLM responds) | ✅ | Verified in proxy.go — proxies to opencode's sync endpoint |
| EA4 | Ingress controllers support long-lived WebSocket connections | ⚠️ | Requires nginx annotation `proxy-read-timeout: 3600`. Must document in Helm values. |
| EA5 | Network policies allow API → kube-apiserver exec traffic | ✅ | Exec goes through control plane, not direct pod-to-pod |
| EA6 | VS Code Chat Participant API is stable (requires VS Code 1.95+) | ⚠️ | API was proposed in 1.93, stabilized ~1.95. Pin minimum version. |
| EA7 | The MCP server SSE transport is functional for VS Code MCP config | ✅ | Verified: `cmd/mcp/main.go` supports `--sse` flag |
| EA8 | Opencode's `/session/:id/message` response format is stable and documentable | ⚠️ | Proxy endpoints return opencode's native response. SDK types are version-coupled to opencode. Must pin opencode version in spec and add CI test against real instance. |
| EA9 | `workspace.Status.PodName` is populated by the controller when workspace is Active | ✅ | Verified: `controller/internal/workspace/controller.go:181` sets PodName on pod creation |

## Rationale

LLMSafeSpace currently has no client SDKs — consumers must hand-craft HTTP requests against the REST API. This creates adoption friction for Persona 2 (programmatic AI agents) and makes it impossible for Persona 1 (interactive developers) to use LLMSafeSpace from their IDE without the web frontend.

This epic delivers:
1. A canonical OpenAPI specification as the single source of truth for the API contract
2. Generated SDKs in TypeScript, Python, Go, and Java (REST-only; streaming deferred)
3. A VS Code extension providing MCP integration, workspace management panels, and terminal access to sandboxes

## Architecture

### SDK Generation Pipeline

```
router.go + types.go  →  hand-written  →  sdks/openapi.yaml (canonical)
                                                       │
                                    ┌──────────────────┼──────────────────┐
                                    ▼                  ▼                  ▼
                              sdks/typescript/   sdks/python/       sdks/go/
                              (openapi-fetch)    (openapi-python)   (oapi-codegen)
                                    │                  │                  │
                                    ▼                  ▼                  ▼
                              sdks/java/         Contract Tests     npm/pypi/pkg
                              (openapi-gen)      (Hurl files)
```

**Key decisions:**
- OpenAPI spec is hand-written from router.go + types.go (no swag annotations exist)
- SDKs are REST-only in v1 — no SSE/WebSocket streaming wrappers
- Each SDK is a thin typed HTTP client; no business logic in SDKs
- Contract tests validate all SDKs produce correct requests against a mock server

### VS Code Extension Architecture

```
vscode-llmsafespace/
├── src/
│   ├── extension.ts              # Activation, registration
│   ├── mcp/
│   │   └── config-provider.ts    # Auto-generates mcp.json for Copilot Chat
│   ├── providers/
│   │   ├── workspace-tree.ts     # TreeDataProvider — sidebar workspace list
│   │   ├── chat-participant.ts   # @llmsafespace Copilot Chat participant
│   │   └── terminal-provider.ts  # WebSocket terminal to sandbox
│   ├── services/
│   │   └── api-client.ts         # Uses TypeScript SDK
│   ├── views/
│   │   └── workspace-detail.ts   # Webview for workspace status/actions
│   └── commands/
│       ├── create-workspace.ts
│       ├── resume-workspace.ts
│       ├── suspend-workspace.ts
│       └── open-terminal.ts
└── package.json                  # contributes: views, chatParticipants, commands
```

**Extension capabilities:**
- Sidebar: workspace list with status badges, click-to-resume
- Chat: `@llmsafespace` participant routes prompts to sandbox agent via MCP
- Terminal: WebSocket-based terminal connected to sandbox shell
- Commands: workspace lifecycle from command palette

### Terminal Proxy (API-side)

The VS Code extension's "Open Terminal" feature requires a WebSocket terminal endpoint:

```
Client (VS Code) ←WebSocket→ API /api/v1/workspaces/:id/terminal ←exec→ Sandbox Pod /bin/sh
```

This reuses the existing auth middleware and workspace ownership checks. No SSH, no new ports — all traffic flows through the HTTPS API.

## Scope

### Prerequisites (must be done before or during implementation)
- Helm chart RBAC: add `pods/exec` permission to API server ServiceAccount (US-14.2)
- Helm chart ingress: document `proxy-read-timeout: 3600` annotation for WebSocket support (US-14.2)
- Add `gorilla/websocket` as direct dependency in go.mod (US-14.2)
- Verify `/bin/sh` is executable in runtime images under readOnlyRoot (US-14.2)

### In Scope
- OpenAPI spec formalization and curation
- SDK generation for TypeScript, Python, Go, Java (REST-only)
- SDK packaging and publishing pipeline (npm, PyPI, Go module, Maven)
- Contract test suite
- VS Code extension with MCP, sidebar, chat participant, terminal
- WebSocket terminal proxy endpoint in the API
- Documentation for SDK usage and extension installation

### Out of Scope (Deferred)
- SSE/WebSocket streaming in SDKs (v2 SDK feature)
- VS Code Remote Development / code-server in sandboxes
- Mobile SDKs
- SDK-level retry/circuit-breaker logic (callers handle this)
- Marketplace publishing (manual install for v1)

## Story Map

```
US-14.1 (OpenAPI spec) ──┐
                          ├── US-14.3 (TypeScript SDK) ──┐
                          ├── US-14.4 (Python SDK)       ├── US-14.7 (Contract tests)
                          ├── US-14.5 (Go SDK)           │
                          └── US-14.6 (Java SDK) ────────┘
                                                              │
US-14.2 (Terminal proxy) ─────────────────────────────────────┤
                                                              ▼
                          US-14.8 (VS Code extension core) ──┤
                          US-14.9 (Chat participant) ─────────┤
                          US-14.10 (Terminal integration) ─────┘
```

## Stories

| ID | Title | Priority | Effort | Depends On |
|----|-------|----------|--------|------------|
| US-14.1 | Formalize OpenAPI Specification | Critical | M (3 days) | — |
| US-14.2 | WebSocket Terminal Proxy Endpoint | Critical | L (5 days) | — |
| US-14.3 | TypeScript SDK | Critical | M (3 days) | US-14.1 |
| US-14.4 | Python SDK | High | M (3 days) | US-14.1 |
| US-14.5 | Go SDK | High | S (2 days) | US-14.1 |
| US-14.6 | Java SDK | Normal | M (3 days) | US-14.1 |
| US-14.7 | SDK Contract Test Suite | High | M (3 days) | US-14.3 |
| US-14.8 | VS Code Extension Core (Sidebar + Commands) | Critical | L (5 days) | US-14.3 |
| US-14.9 | VS Code Chat Participant (@llmsafespace) | High | L (5 days) | US-14.8 |
| US-14.10 | VS Code Terminal Integration | High | M (3 days) | US-14.2, US-14.8 |

**Total estimated effort:** ~35 days (one engineer) or ~3 weeks (two engineers parallelized)

---

## Parallelization Plan

```
Week 1:  US-14.1 (OpenAPI) + US-14.2 (Terminal proxy)     [parallel, no deps]
Week 2:  US-14.3 (TS SDK) + US-14.4 (Python SDK)         [parallel, both need 14.1]
Week 3:  US-14.5 (Go SDK) + US-14.6 (Java SDK) + US-14.7 (Contract tests)
Week 4:  US-14.8 (Extension core)
Week 5:  US-14.9 (Chat participant) + US-14.10 (Terminal)  [parallel]
```
