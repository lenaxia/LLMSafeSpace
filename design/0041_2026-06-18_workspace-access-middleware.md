# 0041: Workspace Access Middleware — Centralized Ownership Enforcement

**Date:** 2026-06-18
**Status:** Design — ready for implementation
**Depends on:** Epic 43 / design 0031 (Stories 1-9 merged)
**Blocks:** None (hardening epic)

---

## Motivation

Design 0031 introduced org membership offboarding (D5, D6). When a user is removed from an org, they should lose access to all org-attributed workspaces. The `verifyOwner` method in the workspace service was correctly updated (Story 5) — offboarded creators now get 403.

**However, `verifyOwner` is only called by the workspace CRUD endpoints** (GET/PUT/DELETE/suspend/activate). Three other surfaces access workspaces by ID without calling `verifyOwner`:

| Surface | Routes | Access check today |
|---------|--------|--------------------|
| **Proxy** (session message, history, terminal, questions) | `/workspaces/:id/sessions/...` | None — fetches CRD + password, proxies to pod |
| **Secrets API** (bindings, env, reload-secrets, model) | `/workspaces/:id/bindings`, `/env`, `/model` | `workspaceOwnerVerifierAdapter` — has D6 admin check but misses D5 membership |
| **Terminal** (ticket + WebSocket) | `/workspaces/:id/terminal/ticket` | CRD label `user-id` (stale, no org awareness) |

An offboarded user who knows a workspace ID can still: send messages, read history, open a terminal shell, rebind credentials, set env vars, and change models. This defeats the offboarding threat model.

**Root cause:** Workspace ownership enforcement is scattered across 3+ independent implementations, each with different logic. There is no single gate that all workspace-access routes pass through.

---

## Decision: WorkspaceAccessMiddleware

**D1: A single gin middleware on the `workspaceGroup` resolves ownership once.**

```
Request → AuthMiddleware → WorkspaceAccessMiddleware → Handler
                              ↓
                    1. Extract :id from path
                    2. Fetch WorkspaceMetadata from PostgreSQL (not CRD)
                    3. Call canonical verifyOwner(userID, meta)
                    4. Store meta + ownership result in gin.Context
                    5. Reject (403/404) if not owned
```

All downstream handlers (CRUD, proxy, secrets, terminal) read ownership from context — they no longer implement their own checks. The middleware replaces:
- `workspace_service.verifyOwner` (inline calls in CRUD handlers)
- `workspaceOwnerVerifierAdapter.VerifyWorkspaceOwner` (secrets adapter)
- `terminal.go` CRD-label check

**D2: The middleware fetches metadata from PostgreSQL, not the CRD.**

The CRD (`v1.Workspace`) does not carry `org_id` — it's only in the `workspaces` PostgreSQL table. The middleware must read from the DB to get `OrgID` for the membership check. The CRD fetch (for phase/PodIP) remains in the proxy handler — it's a different concern (readiness, not ownership).

**D3: The middleware is opt-in per route (not blanket).**

Routes without `:id` (List, Create) don't need it. The middleware is applied to the `/:id` subgroup, not the parent group. This avoids unnecessary DB lookups on list/create.

---

## Implementation Stories

### Story 1: WorkspaceAccessMiddleware (backend)
Create `api/internal/middleware/workspace_access.go`. The middleware:
- Extracts `:id` from the gin context
- Calls `dbService.GetWorkspace(ctx, workspaceID)` to get metadata
- Calls `workspaceService.VerifyOwner(ctx, userID, workspaceID)` (exported from the service)
- Stores the metadata in gin context (`c.Set("workspaceMeta", meta)`)
- Returns 404 if workspace not found, 403 if not owned
Wire it on the `/:id` route group in `router.go`, covering CRUD + proxy + secrets + terminal.

**Effort:** 4h
**Verification:** Offboarded user calling any workspace endpoint → 403. Creator still member → 200. Personal workspace creator → 200.

### Story 2: Remove scattered ownership checks (backend)
Remove the duplicate ownership logic from:
- `workspace_service.go` — CRUD handlers read from context instead of calling `verifyOwner`
- `secrets_adapters.go` — `VerifyWorkspaceOwner` delegates to context-stored result
- `terminal.go` — replace CRD-label check with context-stored result

**Effort:** 3h
**Depends on:** Story 1
**Verification:** All existing workspace tests pass. No behavioral change for authorized users.

### Story 3: Integration test harness (backend)
Build `setupWorkspaceIntegrationRouter` that mounts real `AuthMiddleware` + `WorkspaceAccessMiddleware` + real handlers + mock store. Tests verify:
- Offboarded user → 403 on all workspace endpoints
- Creator still member → 200
- Non-owner → 403
- Personal workspace → 200

**Effort:** 3h
**Depends on:** Story 2
**Verification:** Route-wiring regressions caught.

---

## What This Does NOT Change

- The proxy's CRD fetch (phase/PodIP check) stays in the proxy handler — it's a readiness concern, not ownership.
- The workspace password mechanism stays — the middleware is an authorization gate, not an authentication mechanism for the pod.
- `verifyOwner` stays as a method on the workspace service — the middleware calls it. It's not deleted, just no longer called redundantly by each handler.

---

## Deferred

- **M7 (fat interfaces):** The `orgStore`/`OrgStore` ISP violations are real but low-impact (Go's implicit interfaces mitigate). Deferred to a future cleanup epic.
- **M8 (redundant queries in Get):** Fold into Story 2 — the middleware already fetches metadata, so `Get` can read from context instead of re-querying.
- **Caching the ownership result:** A short-TTL Redis cache of `{userID, workspaceID} → bool` would avoid the DB lookup on every request. Deferred — the DB lookup is a single indexed SELECT, acceptable at current scale. Add caching if profiling shows it's hot.
