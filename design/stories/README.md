# Implementation Stories

Organized by epic, following the V2 design roadmap (design/EVOLUTION-V2.md v2.4).

## V1 Scope (Weeks 1-9)

| Epic | Goal | Weeks |
|------|------|-------|
| 0 | Unbreak: fix deepcopy generation, webhook decoders | 0 (pre-epic) |
| 1 | Fix compile errors, remove warm pools, add security tools | 1-3 |
| 2 | Workspace CRD, PVC persistence, suspend/resume | 3-5 |
| 3 | Proxy to opencode, session endpoints | 5-7 |
| 4 | MCP server for external LLM tools | 7-8 |
| 5 | Helm chart | 8-9 |

## V2 Scope (Post-Foundation)

| Epic | Goal | Depends On |
|------|------|------------|
| 6 | Collapse Sandbox into Workspace | Epics 1-5 |
| 7 | Runtime Interception Layer — system daemon, shadow PATH wrappers, RuntimePolicy CRD | Epic 6 |

| 8 | Credential Health & Agent Abstraction — detect credential loss, agent degradation, self-heal | Epic 6 |
| 9 | Configuration & Settings — tiered config system, admin/user settings, credential sets, Radix UI | Epic 6 |
| 10 | Multi-Tenant Trust & Secret Management — zero-knowledge secret store, key wrapping, virtual namespaces, S3 shared folder, audit logging | Epics 6, 8 |
| 12 | Usage Metering & Billing — per-tenant usage tracking, compute/LLM/storage/API metering, quota enforcement, billing provider interface | Epics 6, 10 |
| 13 | Settings Enforcement — wire all 29 inert settings to their side effects (workspace defaults, user prefs, security hot-reload, lifecycle automation) | Epic 9 |
| 14 | Multi-Language SDKs & VS Code Extension — OpenAPI spec, TS/Python/Go/Java SDKs, VS Code extension with MCP + terminal | Epics 3, 4 |
| 15 | Streaming State Resilience & Mid-Stream Reconnect — server-driven streaming indicator, fetch-on-boundary reconnect, idle reconciliation | Epics 3, 6 |
| 16 | Agent Input Requests — user confirmation flow for agent-initiated actions | Epics 3, 6 |
| 17 | Security Review & Penetration Testing — red-team assessment, threat model, pentest plan | Epics 6, 8, 10 |
| 18 | Hot Migration — zero-downtime pod replacement for workspace upgrades | Epics 6, 8 |
| 21 | ~~Workspace Recovery State Machine~~ — **Superseded by Epic 24** | Epic 22, 23 |
| 22 | agentd Health-Endpoint Redesign — **Complete** (worklog 0105) | None |
| 23 | Controller Race Hardening — Stories 1+4 **Complete**, Stories 2+3 deferred | None |
| 24 | **Self-Healing Workspace Lifecycle** — burstable QoS, failure classification, per-class recovery, remove terminal Failed from transient causes, controller file split | Epics 22, 23 Story 1 |
| 25 | **API Server Robustness & Correctness** — fix double-release/double-write bugs, proxy decomposition, body size limits, SSE write deadlines, session cleanup, context propagation | Epics 22, 23 Story 1 |
| 27a | **Credential Reload Foundation** — explicit user-driven `agent/reload` endpoint replaces auto-dispose; `workspace_agent_state` schema with `pending_refresh` flag; banner UI; Bug 11 type-mismatch fix | Epic 10 |
| 27b | **Credential Reload Polish** — drain mode (event-driven via existing SSETracker), bulk reload (streaming NDJSON), chat-proxy error enrichment with refresh hints, SDK ergonomics, Prometheus metrics | Epic 27a |

## V2.1 (Deferred)

| Story | Reason |
|-------|--------|
| US-1.6: Injection detection | Not on critical path |
| US-5.1: PATH-shadowing wrappers | Redact binary is sufficient for V1 |
| US-5.2: Hardened Dockerfile | Only needed for high-security mode |
| US-5.3: Kyverno policies | Pod security contexts cover V1 |
| WebSocket↔SSE bridge | SSE is sufficient for browsers |
| MCP file upload/download tools | Agent can handle files through its own tools |
| Session-level credential override | Workspace-level credentials sufficient for V1 |
| High-security mode (mode-gate, sentinel, network policy) | Standard security sufficient for V1 |

## Story Dependency Graph

```
US-0.1 (deepcopy) ──┐
US-0.2 (webhooks) ──┼── US-1.1 (API) ──┐
                      │                   ├─ US-1.3 (remove warm pools) ──┐
                      └── US-1.2 (ctrl) ─┘                                │
                                                                        ▼
US-1.5 (redact) ────── US-1.7 (entrypoints) ── US-1.8 (Dockerfile)     │
                                                                         │
                                               US-2.1 (Workspace CRD) ──┤
                                               US-2.2 (Workspace rec.)  │
                                               US-2.3 (Workspace API) ──┤
                                               US-2.4 (Sandbox update) ──┤
                                               US-2.5 (DB migration) ────┤
                                                                         ▼
                                                     US-3.1 (proxy) ────┤
                                                     US-3.2 (routes)    │
                                                     US-3.3 (activity)  │
                                                                         ▼
                                                     US-4.1 (MCP) ──────┤
                                                                         ▼
                                                     US-5.4 (Helm) ──────┘
```
