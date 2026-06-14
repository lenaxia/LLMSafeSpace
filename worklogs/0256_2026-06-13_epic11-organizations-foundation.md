# Worklog: Epic 11 — Organizations Foundation

**Date:** 2026-06-13
**Session:** Implement Epic 11 foundation stories (US-11.1, US-11.2, US-11.3 partial, US-11.4)
**Status:** In Progress

---

## Objective

Begin implementation of Epic 11 (Organizations) following the critical path from the dependency graph: US-11.1 (schema) → US-11.2 (OrgKeyService) → US-11.4 (Org CRUD API). Also implement US-11.3 partial (OrgAwareKeyService wrapper).

---

## Work Completed

### US-11.1: Schema Migration

Created `000024_organizations.up.sql` and `000024_organizations.down.sql` with:
- `organizations` table (id, name, slug, created_by, created_at, updated_at, deleted_at)
- `org_memberships` table (org_id, user_id, role, pending_key_wrap, created_at)
- `org_key_members` table (org_id, user_id, wrapped_dek, key_version, created_at, updated_at) with composite FK to org_memberships
- `workspaces.org_id` nullable column with FK to organizations
- Partial unique index `idx_orgs_slug_active` for slug uniqueness on active orgs only
- `idx_org_memberships_pending` for pending key wrap queries
- Idempotent (`IF NOT EXISTS` / `IF NOT EXISTS` on all DDL)
- Migration test SQL at `api/migrations/test/000024_organizations_test.sql`

### US-11.2: OrgKeyService + OrgKeyStore

Created:
- `pkg/secrets/org_key_store.go` — `OrgKeyStore` interface + `PgOrgKeyStore` implementation
- `pkg/secrets/org_key_service.go` — `OrgKeyService` with methods:
  - `InitializeOrgKeys` — generates org DEK, wraps with founding admin KEK
  - `UnlockOrgDEK` — unwraps single org DEK from pre-fetched record
  - `UnlockAllOrgDEKs` — batch unlock for login path (single DB query for records + single salt query)
  - `WrapOrgDEKForNewAdmin` — wraps cached org DEK for invited admin (key handshake Step 2)
  - `RewrapOrgDEKForAdmin` — re-wraps for password change
  - `RewrapAllOrgDEKsForAdmin` — batch rewrap across all admin orgs
  - `GetOrgDEK` — retrieves cached org DEK
- `pkg/secrets/org_key_service_test.go` — 16 unit tests with mockOrgKeyStore and mockDEKCache
- Sentinel errors: `ErrOrgDEKUnavailable`, `ErrOrgKeyNotFound`, `ErrOrgKeyStale`
- HKDF domain separation: `orgKEKInfo = "llmsafespace-org-kek"` (different from user KEK `"llmsafespace-kek"`)

### US-11.3 (Partial): OrgAwareKeyService

Created `pkg/secrets/org_aware_key_service.go` — thin wrapper around `KeyService` + `OrgKeyService` that satisfies `KeyServiceInterface` with the new `UnlockAllOrgDEKs` method. Wired in app.go to replace the raw keyService on authSvc.

### US-11.4: Org CRUD API + OrgStore + Guards

Created:
- `pkg/types/types.go` — added `OrgRole`, `Organization`, `OrgMember`, `CreateOrgRequest`, `UpdateOrgRequest`, `OrgResponse`, `AddOrgMemberRequest`, `AcceptOrgKeyRequest`, `ChangeOrgMemberRoleRequest`, and `OrgID *string` on `WorkspaceMetadata`
- `api/internal/services/database/pg_org_store.go` — `OrgStore` interface + `PgOrgStore` implementation with:
  - `CreateOrgWithAdmin` — atomic transaction (org + membership + key member)
  - `SoftDeleteOrg` — atomic transaction (null workspaces.org_id + set deleted_at)
  - `RemoveOrgMember` — atomic transaction (delete key member + membership)
  - `IsOrgMember`/`IsOrgAdmin` — JOIN organizations to filter soft-deleted
  - `ListOrgsForUser` — JOIN memberships + organizations for user's orgs
  - `GetUserSalt` — reads from user_keys for org KEK derivation
- `api/internal/middleware/org_guard.go` — `OrgMemberGuard` and `OrgAdminGuard` middlewares using minimal `orgMemberChecker` interface
- `api/internal/handlers/orgs.go` — `OrgsHandler` with Create, List, Get, Update, Delete, ListWorkspaces endpoints
- `api/internal/server/router.go` — added `OrgsHandler` to `RouterConfig`, `registerOrgRoutes` function
- `api/internal/app/app.go` — wired `pgOrgKeyStore`, `orgKeyService`, `orgAwareKS`, `pgOrgStore`, `orgsHandler`

---

## Key Decisions

1. **PgOrgStore uses database/sql** (not pgxpool) to match the existing Service pattern in database.go. The OrgKeyStore uses pgxpool (in pkg/secrets) for consistency with other secrets stores.
2. **OrgMemberGuard/OrgAdminGuard use minimal interface** (`orgMemberChecker`) to avoid circular dependencies between middleware and database packages.
3. **OrgsHandler defines local interfaces** (`orgStore`, `orgAuthService`) for its dependencies — follows Interface Segregation from existing handlers.
4. **Non-fatal org DEK operations** — login/password change never blocked by org DEK failures (consistent with the design doc's "non-fatal per org" principle).

---

## Blockers

1. **Disk space constraint** — the workspace filesystem is ~4.9GB with only 300-600MB free. Compiling the full dependency tree (including kubernetes) requires >600MB of temp space. Could not run `go test` or `go vet` on the full package. All files pass `gofmt -e` syntax checking. Compilation verified for `pkg/types` and `pkg/secrets` packages individually.
2. **Full build + test verification needed** — requires running in CI or an environment with more disk space.

---

## Tests Run

- `gofmt -e` on all 7 new files — zero syntax errors
- `go build ./pkg/types/` — success
- `go build ./pkg/secrets/` — success (timed out on second run due to disk space, but first run completed clean)
- Full `go test` and `go vet` could not be completed due to disk space constraints

---

## Next Steps

1. **US-11.5:** Member management + key handshake endpoints (POST /members, POST /accept-key, DELETE /members/:userID, PUT /members/:userID, GET /members)
2. **US-11.8:** Workspace org attribution (add OrgID to CRD WorkspaceOwner, CreateWorkspaceRequest, ListWorkspaces query, verifyOwner/verifyOrgAdmin)
3. **US-11.6:** Org credential CRUD handler
4. **US-11.7:** decryptBinding org branch + SeedWorkspaceCredentials fix
5. **US-11.9:** Org DEK rotation
6. **US-11.10:** Password change re-wrap (extend RotateKeyHandler)
7. **Verify full compilation** in CI with adequate disk space
8. **US-11.12:** Integration tests

---

## Files Modified

- `api/migrations/000024_organizations.up.sql` (new)
- `api/migrations/000024_organizations.down.sql` (new)
- `api/migrations/test/000024_organizations_test.sql` (new)
- `pkg/secrets/org_key_service.go` (new)
- `pkg/secrets/org_key_store.go` (new)
- `pkg/secrets/org_key_service_test.go` (new)
- `pkg/secrets/org_aware_key_service.go` (new)
- `pkg/types/types.go` (modified — added org types + OrgID on WorkspaceMetadata)
- `api/internal/services/database/pg_org_store.go` (new)
- `api/internal/middleware/org_guard.go` (new)
- `api/internal/handlers/orgs.go` (new)
- `api/internal/server/router.go` (modified — OrgsHandler + registerOrgRoutes)
- `api/internal/app/app.go` (modified — org wiring)
