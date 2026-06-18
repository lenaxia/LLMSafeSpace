# Epic 38: Architectural Remediation

**Status:** Planning
**Created:** 2026-06-12
**Priority:** High (4 critical security findings + foundational abstraction fixes)
**Depends on:** None (self-contained; stories ordered by dependency within this epic)

---

## Problem Statement

The architectural deep dive (worklog 0235) identified 4 CRITICAL, 8 HIGH, and 15 MEDIUM issues across the codebase. The root causes are:

1. **Broken security controls** — rate limiting is non-functional, API keys leak into Redis, HTML validation is inverted, key derivation uses the wrong primitive
2. **Wrong abstraction level** — ProxyHandler is a 1623-line god object, services are mis-packaged as handlers, the agent abstraction is speculative generality
3. **Frankenstein accumulation** — 6 dual patterns from multiple iterations coexist without cleanup
4. **Dead code** — 9 confirmed dead code locations consuming maintenance burden

This epic addresses all 12 prioritized findings from the deep dive as self-contained stories, ordered by dependency and impact.

---

## Stories

| Story | Title | Severity | Estimated Effort | Depends On |
|-------|-------|----------|-----------------|------------|
| US-38.1 | Fix rate limiter | CRITICAL | Large (3-4d) | None |
| US-38.2 | Decompose ProxyHandler | HIGH | Large (4-5d) | None |
| US-38.3 | Replace HKDF with Argon2id | CRITICAL | Medium (1-2d) | None |
| US-38.4 | Hash API keys in Redis cache keys | CRITICAL | Small (0.5d) | None |
| US-38.5 | Fix nohtml validator | CRITICAL | Small (0.25d) | None |
| US-38.6 | Fix controller metrics gauge drift | HIGH | Medium (1d) | None |
| US-38.7 | Remove dead code | MEDIUM | Small (0.5d) | None |
| US-38.8 | Consolidate dual patterns | MEDIUM | Medium (1-2d) | ✅ Complete |
| US-38.9 | Move services out of handlers | MEDIUM | Medium (1-2d) | US-38.2 |
| US-38.10 | Add retry to PushCredentials | MEDIUM | Small (0.5d) | None |
| US-38.11 | Fix K8s client wrapper | HIGH | Medium (1-2d) | None |
| US-38.12 | Add graceful shutdown to agentd | MEDIUM | Medium (1-2d) | None |
| US-38.13 | Consolidate credential handler/store triplication | MEDIUM | Medium (1.5d) | None |

---

## Dependency Graph

```
US-38.1 (rate limiter)          ──┐
US-38.2 (proxy decomposition)   ──┤
US-38.3 (Argon2id)              ──┤
US-38.4 (API key hashing)       ──┤── can start immediately (no deps)
US-38.5 (nohtml fix)            ──┤
US-38.6 (gauge drift)           ──┤
US-38.7 (dead code removal)     ──┤
US-38.10 (PushCredentials retry)──┤
US-38.11 (K8s client)           ──┤
US-38.12 (agentd shutdown)      ──┤
US-38.13 (credential triplication) ─┘
                                    
US-38.8 (dual patterns)  ────── US-38.7 (remove dead code first)
US-38.9 (move services)  ────── US-38.2 (decompose proxy first)
```

---

## Execution Strategy

**Phase 1 — Immediate fixes (day 1):** US-38.4, US-38.5 (one-line fixes, ship immediately)

**Phase 2 — Security critical (days 1-3):** US-38.3 (Argon2id), US-38.6 (gauge drift), US-38.10 (retry)

**Phase 3 — Infrastructure (days 3-7):** US-38.1 (rate limiter rewrite), US-38.11 (K8s client), US-38.12 (agentd shutdown)

**Phase 4 — Structural (days 7-14):** US-38.2 (proxy decomposition), US-38.7 (dead code), US-38.8 (dual patterns), US-38.9 (service relocation)

---

## Out of Scope

- ProxyHandler decomposition is limited to extracting services; it does NOT redesign the SSE event routing or session management protocols
- The agent abstraction (pkg/agent/) is left as-is; removing it is deferred until a second agent type is actually needed
- Frontend changes — no UI behavior changes; US-38.13 Phase 4 is a pure component extraction + type sharing (no new UI, no behavior change)
- New features — every story is a fix or refactor with no behavior change

---

## Success Criteria

1. All 4 CRITICAL findings from worklog 0235 are resolved with regression tests
2. All HIGH findings are resolved
3. ProxyHandler is under 400 lines (from 1623)
4. No dead code files remain
5. No dual patterns remain — one canonical pattern per concern
6. No credential type triplication — one `CredentialRow`, one `CredentialStore`, one `CredentialResponse` (US-38.13)
7. All existing tests pass unchanged
8. New tests cover every fix with regression assertions
