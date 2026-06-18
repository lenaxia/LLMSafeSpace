# 0041: Workspace Access Middleware — Centralized Ownership Enforcement

**Date:** 2026-06-18 (revised)
**Status:** Design — needs revision before implementation
**Depends on:** Epic 43 / design 0031 (Stories 1-9 merged)
**Blocks:** None (hardening epic)

---

## Motivation

Design 0031 introduced org membership offboarding (D5, D6). When a user is removed from an org, they should lose access to all org-attributed workspaces. The `verifyOwner` method in the workspace service was correctly updated (Story 5) — offboarded creators now get 403.

**However, `verifyOwner` is only called by the workspace CRUD endpoints.** Three other surfaces access workspaces by ID with different (or no) ownership checks:

| Surface | Routes | Access check today |
|---------|--------|--------------------|
| **Proxy** (message, history, terminal, questions) | `/workspaces/:id/sessions/...` | None — fetches CRD + password, proxies to pod |
| **Secrets API** (bindings, env, reload-secrets, model) | `/workspaces/:id/bindings`, `/env`, `/model` | Mixed — `SetBindings`/`GetBindings`/`ReloadSecrets` call `SecretService.verifyWorkspaceOwner` (has D6 but misses D5); `SetWorkspaceEnv`/`GetWorkspaceEnv`/`DeleteWorkspaceEnv` have **no check at all**; `ListModels`/`SetModel` use a handler-level `meta.UserID != userID` check (no org awareness) |
| **Terminal** (ticket + WebSocket) | `/workspaces/:id/terminal/ticket` | CRD label `user-id` (stale, no org awareness) |

An offboarded user who knows a workspace ID can still: send messages, read history, open a terminal shell, rebind credentials, set env vars, and change models. This defeats the offboarding threat model.

**Root cause:** Workspace ownership enforcement is scattered across 4+ independent implementations, each with different logic. There is no single gate that all workspace-access routes pass through.

---

## Design Flaws Identified During Self-Review (Revision 2)

The original design proposed a `WorkspaceAccessMiddleware` that fetches metadata from PostgreSQL and calls `verifyOwner`. Self-review against the actual codebase revealed four flaws:

### Flaw 1: Double metadata fetch

`verifyOwner` internally calls `s.dbService.GetWorkspace(ctx, workspaceID)` (workspace_service.go:756). If the middleware also fetches metadata, every request does two `SELECT` queries. The design must split `verifyOwner` into:
- `ResolveWorkspace(ctx, workspaceID) → (*types.WorkspaceMetadata, error)` — pure data fetch
- `CheckOwnership(ctx, userID, *types.WorkspaceMetadata) → error` — pure authorization

The middleware calls `ResolveWorkspace` once, stores `meta` in context, then calls `CheckOwnership`.

### Flaw 2: Performance regression on the proxy hot path

The proxy currently does zero DB queries for ownership — it fetches the CRD from K8s (cached by the client-go informer cache). Adding a PostgreSQL SELECT to every chat message is a real regression.

**Solution: Add `org_id` to the Workspace CRD.** Write `org_id` as a CRD annotation on workspace creation and update. The proxy already fetches the CRD — the annotation carries `org_id` without a DB query. The middleware reads `org_id` from the CRD, not PostgreSQL.

This is a controller change (the reconciler or the API writes the annotation) but it's the correct long-term solution — the CRD is the source of truth for pod lifecycle, and it should carry the ownership context for the proxy that communicates with the pod.

### Flaw 3: Router topology requires restructure

Routes are flat on `workspaceGroup` — there is no `/:id` subgroup. List/Create share the same group as `/:id` routes. The middleware must either:
(a) Run on the parent group and skip non-`:id` routes (fragile `c.Param("id") == ""` check), or
(b) Restructure into `workspaceGroup` (List/Create) + `idGroup` (all `/:id` routes with middleware).

Option (b) is correct but touches 20+ route registrations.

### Flaw 4: Secrets API has cross-layer ownership checks

The secrets surface has ownership enforcement in two places:
- Handler layer: `ListModels`/`SetModel` (inline `meta.UserID != userID`)
- Service layer: `SecretService.verifyWorkspaceOwner` (called by `SetBindings`/`GetBindings`/`ReloadSecrets`)

The middleware (handler layer) can't cleanly remove service-layer checks without making `SecretService` context-aware or trusting the middleware's result. And `SetWorkspaceEnv`/`GetWorkspaceEnv`/`DeleteWorkspaceEnv` have NO check at all — the middleware fixes these.

---

## Revised Decision

### D1: Phase 1 — WorkspaceAccessMiddleware (handler-layer gate)

Add a gin middleware that resolves workspace ownership for ALL `/:id` routes. It:
1. Extracts `:id` from the path
2. Fetches metadata from PostgreSQL (Phase 1 — DB query; Phase 2 adds CRD annotation)
3. Calls `CheckOwnership(userID, meta)` — the extracted authorization logic
4. Stores metadata in gin context
5. Returns 404/403 if not authorized

Apply via router restructure: `workspaceGroup` → `idGroup` with middleware.

Remove all inline ownership checks from handlers (`ListModels`, `SetModel`, `terminal.go`).

### D2: Phase 2 — `org_id` on the Workspace CRD

Write `org_id` as a CRD annotation (`llmsafespace.dev/org-id`) on workspace creation (API service) and update (workspace migration on org join/leave). The middleware reads from the CRD annotation instead of PostgreSQL, eliminating the DB query on the proxy hot path.

This is a schema change (add annotation to the CRD type) + a controller validation (the annotation is set by the API, not the controller).

### D3: Phase 3 — Remove service-layer ownership checks

Once the middleware is the single handler-layer gate, remove `SecretService.verifyWorkspaceOwner` and the `workspaceOwnerVerifierAdapter`. Services trust the middleware's context-stored metadata.

---

## Implementation Stories (Revised)

### Story 1: Split verifyOwner + router restructure (backend)
- Split `verifyOwner` into `ResolveWorkspace` + `CheckOwnership`
- Restructure `workspaceGroup` into `workspaceGroup` (List/Create) + `idGroup` (all `/:id` routes)
- Add `WorkspaceAccessMiddleware` on `idGroup` (DB-backed metadata fetch)
- Remove inline checks from `ListModels`, `SetModel`, `terminal.go`

**Effort:** 6h
**Verification:** Offboarded user → 403 on all endpoints. Authorized user → 200. List/Create unaffected.

### Story 2: CRD `org_id` annotation (backend + controller)
- Add `org_id` annotation to `v1.Workspace` type
- Write annotation on workspace creation (API)
- Update annotation on workspace org_id migration (org join/leave)
- Middleware reads annotation from CRD (falls back to DB if missing)

**Effort:** 4h
**Depends on:** Story 1
**Verification:** No DB query on proxy hot path. CRD carries org_id.

### Story 3: Remove scattered checks + secrets cleanup (backend)
- Remove `SecretService.verifyWorkspaceOwner` + `workspaceOwnerVerifierAdapter`
- Add ownership to `SetWorkspaceEnv`/`GetWorkspaceEnv`/`DeleteWorkspaceEnv` (they currently have none — the middleware covers them now)
- Services read metadata from context

**Effort:** 3h
**Depends on:** Story 1
**Verification:** All existing workspace/secrets tests pass.

### Story 4: Integration test harness (backend)
Build `setupWorkspaceIntegrationRouter` with real middleware + mock store.

**Effort:** 3h
**Depends on:** Story 3
**Verification:** Route-wiring regressions caught. All 4 ownership scenarios tested.

---

## What This Does NOT Change

- The proxy's CRD fetch (phase/PodIP) stays in the proxy handler — readiness concern, not ownership.
- The workspace password mechanism stays — the middleware is authorization, not pod authentication.
- `verifyOwner` (as `CheckOwnership`) stays as a service method — internal callers that don't go through the middleware (rare) can still call it directly.

---

## Deferred

- **Redis caching of ownership result:** the CRD annotation (Story 2) eliminates the need — the CRD is already cached by client-go.
- **Fat interface cleanup (M7):** low-priority ISP improvement.
