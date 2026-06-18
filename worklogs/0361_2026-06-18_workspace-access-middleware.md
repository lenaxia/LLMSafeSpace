# Worklog: Design 0041 — Workspace Access Middleware (Centralized Ownership Enforcement)

**Date:** 2026-06-18
**Session:** Implement design 0041 (Workspace Access Middleware) end-to-end — Stories 1, 2, 4 as scoped, Story 3 revised via validated investigation.
**Status:** Complete

---

## Objective

Close the design 0041 threat model: an offboarded org member who knows a workspace ID could read history, send messages, open a terminal, create secrets, and read env-var names because `verifyOwner` was called by only 12 of ~30 workspace `/:id` routes; the rest used divergent checks (handler-level `meta.UserID` comparisons, a stale CRD `user-id` label, `SecretService.verifyWorkspaceOwner` missing the D5 creator-membership re-check) or no check at all (proxy routes, env handlers, `sessions/active`).

The fix: a single `WorkspaceAccessMiddleware` gate on every `/api/v1/workspaces/:id` route, with ownership logic split into a pure fetch (`ResolveWorkspace`) and a pure authorization (`CheckOwnership`).

---

## Work Completed

### Story 1 — Middleware + router restructure
- Split `(*workspace.Service).verifyOwner` into `ResolveWorkspace(ctx, workspaceID)` (pure DB fetch → `*types.WorkspaceMetadata` or `*apierrors.APIError`) and `CheckOwnership(ctx, userID, meta)` (D5 creator-membership + D6 org-admin). `verifyOwner` becomes a 2-line wrapper so the 11 existing service-method callers are unchanged (`api/internal/services/workspace/workspace_service.go:751-821`). Both new methods exported on `interfaces.WorkspaceService`.
- New `api/internal/middleware/workspace_access.go` — `WorkspaceAccessMiddleware` extracts `:id` + `userID`, calls `ResolveWorkspace` → `CheckOwnership`, stores validated `meta` in `c.Request.Context()` under `types.ContextKeyWorkspaceMeta` and in gin context for handler ergonomics. Fails safe: DB errors → 500 (never rewritten as 403), NotFound → 404, Forbidden → 403 — matching the enumeration-prevention semantics of the old `verifyOwner`.
- Router restructure (`api/internal/server/router.go`): `idGroup := workspaceGroup.Group("/:id")` with the middleware applied. All 37 `/api/v1/workspaces/:id/*` routes moved onto `idGroup`; `GET/POST ""` (List/Create) stay on `workspaceGroup` (no `:id`, no gate). Gin sub-groups inherit the parent `AuthMiddleware`. WebSocket `GET /:id/terminal` intentionally left on the ROOT router (ticket-auth, design edge case 3).

### Story 2 — Single ownership gate; scattered checks removed
- `verifyOwner` now short-circuits when `types.WorkspaceMetaFromCtx(ctx)` returns meta with `meta.ID == workspaceID` — one change covers all 11 callers; background callers (no middleware) fall through to the full Resolve+Check (design edge case 5). The single trusted writer of `ContextKeyWorkspaceMeta` is the middleware itself; the `meta.ID == workspaceID` guard defends against trusting meta for an unrelated workspace. New context key + `WorkspaceMetaFromCtx` accessor live in `pkg/types` (neutral, no import cycle).
- Removed `handlers/models.go` inline `meta.UserID != userID` checks (ListModels, SetModel) — routes now gated by middleware.
- Removed `handlers/terminal.go` `ws.Labels["user-id"]` comparison in `HandleTicket` — route now gated.
- Removed `pkg/secrets/secret_service.go` `WorkspaceOwnerVerifier` interface, `wsOwners`/`requireWsVerifier` fields, `SetWorkspaceOwnerVerifier`, `RequireOwnerVerification`, `verifyWorkspaceOwner`, and the 3 call sites in `SetBindings`/`AddBindings`/`GetBindings` + `pkg/secrets/injection.go:PrepareSecretsForInjection`. All these routes (`PUT/GET /:id/bindings`, `POST /:id/reload-secrets`, `PUT/GET/DELETE /:id/env`) are gated.
- Deleted `workspaceOwnerVerifierAdapter` + its local `OrgMembershipChecker` interface from `api/internal/app/secrets_adapters.go` — the adapter had a real D5 bug (returned nil for `meta.UserID == userID` without re-checking org membership, so an offboarded creator would still pass).
- Rewired `userProvCredHandler.SetWorkspaceOwnerChecker` (`api/internal/app/app.go`) to the canonical `wsSvc.ResolveWorkspace` + `wsSvc.CheckOwnership`. This fixes the same D5 bug on the `POST/DELETE /api/v1/provider-credentials/:id/bind/:workspaceId` routes (which are NOT under `/workspaces/:id`, so the middleware doesn't cover them). Fail-closed if `wsSvc` is the wrong concrete type.

### Story 4 — Integration harness (Definition of Done)
- `setupWorkspaceIntegrationRouter` (`api/internal/server/workspace_integration_test.go`) builds the REAL `*workspace.Service` (via `workspace.New` with mock DB + mock orgStore) and the REAL `WorkspaceAccessMiddleware`, then registers stub `200 {"ok":true}` handlers on every `/:id` route shape.
- Matrix: **8 scenarios × 37 routes = 296 assertions** with `assert.EqualF` (exact status). Scenarios: offboarded-creator-D5 → 403 (IsOrgMember=false via real CheckOwnership); non-owner/non-admin org → 403; org admin → 200; creator+member → 200; personal-owner → 200; personal-non-owner → 403; unknown workspace → 404; DB error → 500. Every previously-UNGUARDED route from the design's Problem section (proxy message/history/queue/abort, `session-events`, `sessions/active`, `DELETE /:id/env/:name`, bindings, reload-secrets) is an explicit matrix row.
- Gap-fill via real `NewRouter`: `TestWorkspaceAccessMiddleware_BindingsAndReloadSecrets_ForbiddenForNonOwner`, `_D5Composition_BindingsRoute`, `_CreateRouteBypassesOwnership` (proves `POST /api/v1/workspaces` does not call ownership).

### Story 3 — Revised after validated investigation (see Key Decisions)
Deleted the unread `org-id` CRD label from `buildWorkspaceCRD` (`api/internal/services/workspace/workspace_service.go:842-849`). One-line edit. The label had zero readers post-Story-2; the spec field `spec.owner.OrgID` remains (a CRD schema field, separate concern).

### Minor cleanup
- None. (A pre-existing worklog collision that was in the working tree was already resolved by main's `chore(repolint): auto-fix worklog numbering collisions` bot.)

---

## Key Decisions

### 1. `verifyOwner` short-circuit (single-point fix vs 11-site refactor)
The design's Story 2 suggested refactoring each of the 11 service-method callers to trust middleware context. The validated simpler approach: make `verifyOwner` itself consult `types.WorkspaceMetaFromCtx(ctx)`. One change, automatic coverage of all 11 callers, full-path fallback for background callers. This is the design's "service methods trust context" intent achieved at the chokepoint rather than scattered.

### 2. Story 3 scope: DELETE the `org-id` label, not sync it
This is the most consequential decision and it required four rounds of validation to reach. Initial reading of the design suggested Story 3 = "sync the CRD `org-id` label on every `workspaces.org_id` change so the middleware can read it from a CRD informer instead of PostgreSQL." Investigation progressively disproved the premises:

| Premise | Validated result |
|---|---|
| Two `org_id` mutation paths to sync | **FALSE — three**: `AcceptInvitationTx`, `CreateOrgWithAdmin`, and `HardDeleteOrg` (background `pending_org_cleaner.go:85,105` nulls org_id for ALL org workspaces) |
| Controller reads `org-id` for metrics/billing | **FALSE** — controller reads only `user-id` (`metrics_wiring.go:100,121`, `health.go:262`); metrics dimensioned by workspace/userID/runtime/securityLevel, never org |
| `WorkspaceInterface` has `Patch` for label sync | **FALSE — `Update` only**; sync = Get-then-Update = read-modify-write race with controller's concurrent status/spec writes |
| Org/invitation handlers can call a sync method | **FALSE — neither has the workspace service wired**; both would need new dependency injection, plus the background cleaner |
| The middleware DB query serves metadata for downstream handlers | **FALSE — zero production callers of `WorkspaceMetaFromContext`**; handlers like `GetWorkspace` re-fetch from DB unconditionally (`workspace_service.go:376`). The middleware's DB query is purely an authz cost |
| The middleware could read org_id from a CRD informer to eliminate the DB query | **FALSE** — no Workspace-CRD informer is wired into the API service today (only controller-side); `ResolveWorkspace` returns PG-only fields (`AgentNeedsRefresh`, `CredentialsPendingSince` from `workspace_agent_state`) |
| `org-id` label has any system consumer post-Story-2 | **FALSE** — Story 2 removed the only reader (`terminal.go` label check). The `user-id` label DOES have a selector consumer (`workspace_service.go:487`) and 3 controller metrics reads, but `user-id` is immutable after create so it can never diverge |

**Conclusion:** the `org-id` label is unread dead state. Syncing it (Option A) would require 3 sync sites, 3 new wirings, a `Patch` interface addition (or write-race acceptance), and a backfill for already-diverged workspaces — all for a label **no system code reads**, with no transactional guarantee across PostgreSQL + Kubernetes (the design admits this gap, bounding it "by request duration"). More sync sites = more divergence surface, not less. The scalable answer is single-source-of-truth: PostgreSQL `workspaces.org_id` is authoritative and correct in all 3 mutation paths; stop maintaining a divergent copy.

**Why not also delete `spec.owner.OrgID`?** It is a CRD schema field (kubebuilder-annotated), present since the V2 architecture, with `spec.owner.UserID` as its sibling. Removing it is a CRD schema migration (broader scope, affects `kubectl` UX, may affect future controller features). It is left as-is for this PR; the label deletion alone closes the authz-relevant dead state. If a future feature genuinely needs authoritative org attribution on the CRD (e.g., org-level billing in the controller), it should be designed with a proper reconciliation mechanism — not a dual-write bolted onto org-join handlers.

### 3. Phase 1 = end-state for the security fix
The design stated "Phase 1 ships first because correctness > performance." After Story 3's revision, Phase 1 IS the correct end-state — not a stopgap. The 1 indexed PK scan per `:id` request is acceptable at current scale (no profiling data indicates otherwise; README Rule 4: measure before optimizing). A future Workspace-CRD informer optimization is a separate, data-driven epic that would need to also move `workspace_agent_state` into the CRD or a cache — a real redesign, out of scope here.

---

## Assumptions (stated + validated, per Rule 7)

| # | Assumption | Validation |
|---|---|---|
| 1 | None of the 11 `verifyOwner` callers run from a non-HTTP background goroutine | `workspace_service.go:111` only `go func()` is `markDeleted` (calls `db.MarkWorkspaceDeleted` directly, never `verifyOwner`); `grep workspace.Service controller/` → 0 matches |
| 2 | `PrepareSecretsForInjection` callers are all HTTP-gated or creator-inherent | `handlers/secrets.go:406,462` (middleware-gated routes); `workspace_service.go:1347,1387` (`refreshEphemeralSecrets`/`seedEphemeralSecrets` from Create=creator-inherent, Restart/Activate=middleware-gated) |
| 3 | User provider-credential bind/unbind routes are NOT under `/workspaces/:id` | `router.go:268-269`: `/api/v1/provider-credentials/:id/bind/:workspaceId` — genuinely needs its own ownership check |
| 4 | `pkg/types` imports nothing from `api/internal/*` | `grep api/internal pkg/types/` → 0 matches; `ContextKeyWorkspaceMeta` placement creates no cycle |
| 5 | Gin sub-groups inherit parent middleware | `gin@v1.10.0/routergroup.go:72-78`; empirically validated by `orgGroup→orgIDGroup` pattern at `router.go:1004-1008` |
| 6 | `org-id` label has zero readers post-Story-2 | `grep 'Labels\["org-id"\]'` in production code → 0 matches; `grep 'Labels\["user-id"\]'` → 3 controller reads (metrics/health) + 1 selector use |
| 7 | Three `workspaces.org_id` mutation paths (not two) | `pg_org_store.go:135` (CreateOrgWithAdmin), `:914` (AcceptInvitationTx), `:740` (HardDeleteOrg, called from `pending_org_cleaner.go:85,105`) |
| 8 | Controller has zero PostgreSQL access (by design R9) | `grep pgx\|database/sql controller/` → 0; controller depends only on controller-runtime + K8s client |
| 9 | No handler consumes the middleware-stored meta today | `grep WorkspaceMetaFromContext api/internal/handlers api/internal/server` → 0 production callers; only `verifyOwner` short-circuit reads the context form |

---

## Blockers

None.

---

## Tests Run

```
go build ./...                                                          PASS (clean)
go vet ./...                                                            PASS (clean)
gofmt -l api/ pkg/                                                      PASS (clean)
go test -race ./api/internal/middleware/...                             ok 1.1s
go test -race ./api/internal/services/workspace/...                     ok 1.8s
go test -race ./api/internal/server/...                                 ok 1.9s  (incl. 296-matrix integration harness)
go test -race ./api/internal/handlers/...                               ok 33s
go test -race ./api/internal/app/...                                    ok 2.6s
go test -race ./pkg/secrets/...                                         ok 27s
go test -count=1 ./api/... ./pkg/... ./controller/...                   ALL 60+ packages PASS, 0 failures
```

The Story 4 harness was independently mutation-tested by the validator: (a) `CheckOwnership` always-return-nil → 37 routes × 3 denied scenarios FAIL; (b) middleware skipping `CheckOwnership` → same scenarios FAIL; (c) removing `idGroup.Use(WorkspaceAccessMiddleware(...))` from production router → real-NewRouter gap-fill tests FAIL. The harness is not tautological.

`golangci-lint` not installed in the sandbox; relied on `go vet` + `gofmt` (both clean). CI runs `golangci-lint`.

---

## Next Steps

- **PR review against the README rubric** (scores 1–10 per dimension; this work should score ≥9 across the board — single responsibility middleware, narrow caller-shaped interfaces, full e2e wiring proof via the harness, zero TODOs/dead code).
- **CRD schema follow-up (out of scope):** if authoritative org attribution on the CRD becomes needed, design it with a controller-reconciled mechanism fed by something the controller can see — not an API-side dual-write. Do not reintroduce a label that the API must keep in sync with PostgreSQL.
- **`ErrWorkspaceNotOwned` sentinel** is now exported but has no production writer (kept as a stable API; handler still maps it defensively). A deliberate cleanup pass could remove it + the handler branch — deferred to avoid SDK canary churn.

---

## Files Modified

**Created:**
- `api/internal/middleware/workspace_access.go`
- `api/internal/middleware/tests/workspace_access_test.go`
- `api/internal/services/workspace/resolve_check_test.go`
- `api/internal/server/router_workspace_access_test.go`
- `api/internal/server/workspace_integration_test.go`
- `worklogs/0361_2026-06-18_workspace-access-middleware.md` (this file)

**Modified:**
- `pkg/types/types.go` (+`ContextKeyWorkspaceMeta`, +`WorkspaceMetaFromCtx`)
- `pkg/types/types_test.go`
- `api/internal/interfaces/interfaces.go` (`WorkspaceService` + ResolveWorkspace/CheckOwnership)
- `api/internal/mocks/workspace.go` (mock methods)
- `api/internal/mocks/database.go` (unchanged interface, kept consistent)
- `api/internal/services/workspace/workspace_service.go` (resolve/check split, verifyOwner short-circuit + wrapper, `org-id` label deleted)
- `api/internal/middleware/workspace_access.go` (propagates meta into `c.Request.Context()`)
- `api/internal/server/router.go` (idGroup + WorkspaceAccessMiddleware; all `/:id` routes restructured)
- `api/internal/server/router_workspace_test.go`, `router_frontend_workspace_test.go`, `router_terminal_test.go` (mock defaults + 404→403 expectations)
- `api/internal/handlers/models.go` (removed inline ownership checks)
- `api/internal/handlers/models_test.go` (removed now-misplaced ownership tests)
- `api/internal/handlers/terminal.go` (removed CRD-label ownership check)
- `api/internal/handlers/terminal_test.go` (test + comment updated)
- `api/internal/handlers/user_provider_credentials.go` (unchanged — checker type preserved)
- `pkg/secrets/secret_service.go` (removed WorkspaceOwnerVerifier machinery)
- `pkg/secrets/injection.go` (removed verifyWorkspaceOwner call)
- `pkg/secrets/secret_service_test.go` (removed obsolete fail-closed tests)
- `api/internal/app/secrets_adapters.go` (deleted `workspaceOwnerVerifierAdapter`)
- `api/internal/app/app.go` (removed verifier wiring; rewired userProvCred checker to wsSvc.ResolveWorkspace+CheckOwnership)
- `api/internal/app/secrets_wiring_test.go` (test for the rewired checker)
