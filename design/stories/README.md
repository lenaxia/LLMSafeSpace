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
