# 0041: Workspace Access Middleware — Centralized Ownership Enforcement

**Date:** 2026-06-18 (revision 3 — final)
**Status:** Design — ready for implementation
**Depends on:** Epic 43 / design 0031 (Stories 1-9 merged), PR #228 (hardening)
**Blocks:** None (hardening epic)

---

## Problem

Design 0031 introduced org membership offboarding (D5). When a user is removed from an org, they should lose access to all org-attributed workspaces. The workspace service's `verifyOwner` was correctly updated (Story 5) — offboarded creators now get 403.

**But `verifyOwner` is called by only 12 of the ~30 workspace `/:id` routes.** The rest use different checks or none at all:

### Complete route inventory (verified against source)

**Routes WITH verifyOwner (via service methods):** 12 routes
- GET/PUT/DELETE `/:id`, POST `/:id/suspend`, POST `/:id/restart`, GET `/:id/status`
- POST `/:id/activate`, GET `/:id/sessions`, POST `/:id/sessions/new`
- PUT `/:id/sessions/:sessionId/title`, PUT `/:id/sessions/:sessionId/seen`
- POST `/:id/agent/reload`

**Routes with DIFFERENT checks (no D5 org membership):** 6 routes
- GET `/:id/models`, PUT `/:id/model` — handler-level `meta.UserID != userID` (no org awareness)
- POST `/:id/terminal/ticket` — CRD label `user-id` (stale, no org awareness)
- GET `/:id/terminal` (WebSocket) — trusts the ticket (which was issued with the stale-label check)
- `SecretService.verifyWorkspaceOwner` (has D6 admin check but misses D5 membership) — covers PUT/GET `/:id/bindings`, POST `/:id/reload-secrets`
- `SetWorkspaceEnv` reaches `AddBindings` which calls `verifyWorkspaceOwner` — but `CreateSecret`/`GetSecretByName` calls earlier in the handler run WITHOUT ownership checks

**Routes with NO ownership check at all:** ~12 routes
- All proxy routes: POST `/:id/sessions/:sessionId/message`, GET `/:id/sessions/:sessionId/message`, GET `/:id/sessions/:sessionId`, POST `/:id/sessions/:sessionId/abort`, DELETE `/:id/sessions/:sessionId`, GET `/:id/session-events`, queue ops, question/permission routes
- GET `/:id/sessions/active` — calls `proxyHandler.GetActiveSessions` directly
- GET `/:id/env`, DELETE `/:id/env/:name` — no check before reading/deleting env vars

### Impact

An offboarded user who knows a workspace ID can: send messages, read history, open a terminal, create secrets, read env var names, and trigger secret reloads. This defeats the D5 offboarding threat model.

### Root cause

Workspace ownership enforcement is scattered across 4+ independent implementations with different logic. There is no single gate.

---

## Solution

### D1: WorkspaceAccessMiddleware — single ownership gate

A gin middleware on all `/:id` workspace routes that:
1. Extracts `:id` from the path
2. Fetches `WorkspaceMetadata` from PostgreSQL (authoritative `org_id` source)
3. Calls `CheckOwnership(userID, meta)` — the authorization logic extracted from `verifyOwner`
4. Stores `meta` in gin context for downstream handlers
5. Returns 404 (not found) or 403 (not authorized)

All downstream handlers read ownership from context. The middleware replaces every scattered check.

### D2: Split verifyOwner into resolve + authorize

```
// Before (verifyOwner does both):
func (s *Service) verifyOwner(ctx, userID, workspaceID) error {
    meta := s.dbService.GetWorkspace(ctx, workspaceID)  // resolve
    if meta.UserID == userID { ... }                    // authorize
}

// After (split):
func ResolveWorkspace(ctx, workspaceID) (*types.WorkspaceMetadata, error)  // pure fetch
func CheckOwnership(userID string, meta *types.WorkspaceMetadata) error    // pure logic
```

The middleware calls `ResolveWorkspace` once, stores `meta`, then calls `CheckOwnership`. Downstream service methods that still call `verifyOwner` internally are refactored to accept `meta` from context (or the middleware's result is trusted — see D4).

### D3: Router restructure

```
workspaceGroup (AuthMiddleware only)
├── GET  ""           (List — no :id)
├── POST ""           (Create — no :id)
└── idGroup (AuthMiddleware + WorkspaceAccessMiddleware)
    ├── GET/PUT/DELETE "/:id"
    ├── POST "/:id/suspend", "/:id/activate", "/:id/restart"
    ├── GET  "/:id/status", "/:id/sessions", "/:id/sessions/active"
    ├── POST "/:id/sessions/new", "/:id/sessions/:sid/message", ...
    ├── PUT/GET "/:id/bindings", POST "/:id/reload-secrets"
    ├── PUT/GET/DELETE "/:id/env"
    ├── GET "/:id/models", PUT "/:id/model"
    ├── POST "/:id/terminal/ticket"
    └── POST "/:id/agent/reload"
```

All `/:id` routes move to `idGroup`. The middleware runs once per request.

### D4: Phase 2 — eliminate the DB query on the proxy hot path

The CRD already has an `org-id` **label** (`workspace_service.go:808`), but it's set at creation time and never updated when `workspaces.org_id` changes (migration on org join). So the label is stale after migration.

Phase 2 makes the label authoritative by updating it on every `org_id` change:
- `AcceptInvitationTx` (org join migration) → update CRD labels
- `RemoveOrgMember` (offboarding) → no CRD change needed (membership check is the gate, not the label)
- `CreateWorkspace` auto-attribution → already sets the label correctly

With the label authoritative, the middleware can read `org_id` from the CRD (cached by client-go's informer) instead of PostgreSQL. This eliminates the DB query from the proxy hot path.

**Phase 1 uses PostgreSQL (correct, slightly slower). Phase 2 adds CRD label sync (fast, eventually consistent).** Phase 1 ships first because correctness > performance.

### D5: Remove scattered checks

Once the middleware is the single gate:
- Remove inline checks from `ListModels`, `SetModel` (`models.go`)
- Remove CRD-label check from `terminal.go` HandleTicket
- Remove `SecretService.verifyWorkspaceOwner` + `workspaceOwnerVerifierAdapter`
- The `sessions/active` handler, env handlers, and all proxy routes inherit the middleware's check

Service-layer methods (`SetBindings`, `GetBindings`, `PrepareSecretsForInjection`) that currently call `verifyWorkspaceOwner` internally are refactored to trust the middleware's context-stored metadata. If called outside a request context (e.g., by a background job), they fall back to a direct `CheckOwnership` call.

---

## Implementation Stories

### Story 1: WorkspaceAccessMiddleware + router restructure (6h)
- Split `verifyOwner` → `ResolveWorkspace` + `CheckOwnership`
- Create `api/internal/middleware/workspace_access.go`
- Restructure router: `workspaceGroup` → `idGroup` with middleware
- Middleware fetches metadata from PostgreSQL (Phase 1)

### Story 2: Remove scattered ownership checks (4h, depends on 1)
- Remove inline checks from `models.go`, `terminal.go`
- Remove `SecretService.verifyWorkspaceOwner` + `workspaceOwnerVerifierAdapter`
- Service methods trust context or accept metadata parameter
- Verify `SetWorkspaceEnv`/`GetWorkspaceEnv`/`DeleteWorkspaceEnv` now covered by middleware

### Story 3: CRD label sync (3h, depends on 1)
- Update CRD `org-id` label when `workspaces.org_id` changes (org join migration)
- Middleware reads label from CRD (falls back to DB if missing/stale)
- Eliminates DB query from proxy hot path

### Story 4: Integration test harness (3h, depends on 2)
- `setupWorkspaceIntegrationRouter` with real middleware + mock store
- Tests: offboarded user → 403 on ALL endpoints; authorized → 200; non-owner → 403; personal → 200

**Total: 16h**

---

## Consistency Analysis

| Dimension | Assessment |
|-----------|------------|
| **Internal consistency** | ✅ The middleware is the single gate. No route can bypass it. The resolve/authorize split eliminates double-fetch. |
| **Robustness** | ✅ Every `:id` route gets the check. No "forgot this surface" gaps. Middleware fails closed (DB error → 403). |
| **Maintainability** | ✅ One ownership implementation, one place to update. New routes automatically inherit the check by being on `idGroup`. |
| **Reliability** | ✅ Middleware is deterministic (same input → same output). No race conditions (single-threaded per request). DB lookup is indexed PK scan. |
| **Scalability** | ✅ Phase 1: one indexed SELECT per request (acceptable). Phase 2: zero DB queries (CRD cache). No locks, no contention. |
| **Security** | ✅ Fail-closed (DB error → deny). Offboarded users blocked on ALL surfaces. No enumeration (404 for unknown workspace, 403 for known-but-not-owned). |
| **Performance** | ⚠️ Phase 1 adds 1 DB query to proxy hot path (every chat message). Phase 3 eliminates it. Acceptable for Phase 1 given current scale. |
| **SOLID** | ✅ SRP: middleware does only ownership. OCP: new routes inherit automatically. ISP: `CheckOwnership` is a narrow function. DIP: middleware depends on interfaces, not concretions. |
| **Idiomatic** | ✅ Gin middleware is the standard pattern. Context propagation is idiomatic Go. Interface satisfaction is implicit. |

---

## Edge Cases Addressed

1. **Workspace deleted between middleware and handler:** The metadata is fetched at middleware time; if the workspace is deleted mid-request, the handler's operation will fail naturally (K8s 404, DB not-found). No corruption risk.

2. **Org deleted between middleware and handler:** Same as above — `IsOrgMember` is checked at middleware time; if the org is deleted mid-request, the worst case is a single request succeeds that shouldn't have. Acceptable (no transactional guarantee across K8s + PostgreSQL).

3. **WebSocket terminal:** The ticket endpoint (`POST /:id/terminal/ticket`) goes through the middleware. The WebSocket endpoint (`GET /:id/terminal`) uses the ticket, not JWT — it's on the root router, not `workspaceGroup`. The ticket was issued after middleware verification, so it inherits the ownership check. If the user is offboarded AFTER getting a ticket but BEFORE using it, the ticket (60s TTL) expires naturally.

4. **Session events SSE:** `GET /:id/session-events` is a long-lived SSE stream. The middleware checks ownership at connection time. If the user is offboarded mid-stream, the stream continues until the next reconnect (which will fail). This is acceptable — SSE reconnect is automatic and the window is bounded by the stream duration.

5. **Service-to-service calls:** Background jobs (e.g., pending org cleaner) that call workspace service methods directly don't go through the middleware. `CheckOwnership` remains available as a standalone function for these paths.

---

## What This Does NOT Change

- Proxy CRD fetch (phase/PodIP) stays in the proxy handler — readiness, not ownership.
- Workspace password mechanism stays — middleware is authorization, not pod auth.
- `verifyOwner` logic stays (as `CheckOwnership`) — just called from one place instead of many.

---

## Deferred

- **Redis ownership cache:** Phase 3's CRD-label sync eliminates the need.
- **Fat interface cleanup (M7):** low-priority ISP improvement.
- **Transactional consistency across K8s + PostgreSQL:** the middleware checks at request time; mid-request state changes are an accepted gap (bounded by request duration).
