# Worklog: Epic 43 US-43.7/US-43.8 — Policy Schema, API, Enforcement

**Date:** 2026-06-14
**Session:** Implement Phase 2 org policy engine
**Status:** Complete

---

## Objective

Implement org-level policy enforcement for 4 policy types per D15: allowed_models, allowed_providers, max_workspaces_per_member, max_active_workspaces_per_member. Admin-only API (no UI per D15). Org policy intersects platform policy per D16.

---

## Work Completed

### Migration 032
- `org_policies` table: key-value with JSONB value, PK (org_id, key), CHECK constraint on the 4 allowed keys.

### PolicyService (`api/internal/services/policy/service.go`)
- Redis-cached (5-min TTL) policy reads.
- `GetEffectivePolicy(orgID)` returns org ∩ platform intersection.
- DB errors propagate (no silent unrestricted fallback — security critical).
- Cache invalidation on policy mutation.

### PolicyHandler (`api/internal/handlers/policies.go`)
- GET / PUT / DELETE `/orgs/:id/policies/:key` — admin-only via OrgAdminGuard.
- Validates key (4 allowed keys) and value type (array vs int, non-negative quotas).

### Enforcement (US-43.8)
- **CreateWorkspace**: checks max_workspaces_per_member and max_active_workspaces_per_member before creating. Rejects with validation error if exceeded.
- PolicyChecker interface injected into workspace.Service.
- DB methods: CountWorkspacesByUserAndOrg, CountActiveWorkspacesByUserAndOrg.

### Wiring
- app.go: PolicyService + PolicyHandler constructed when pgOrgStore is non-nil.
- PolicyService receives cache service for Redis caching.
- PolicyChecker wired into workspace.Service.
- Routes registered in orgAdminGroup.

---

## Tests Run

- `go build ./...` exit 0
- `go vet ./api/... ./pkg/...` exit 0
- `go test ./api/... ./pkg/... ./controller/...` — all pass
- 20+ new tests covering policy CRUD, cache, intersection, enforcement

---

## Files Modified

- api/migrations/000032_org_policies.up.sql (new)
- api/migrations/000032_org_policies.down.sql (new)
- charts/llmsafespace/migrations/000032_*.sql (mirror)
- api/internal/services/policy/service.go (new)
- api/internal/services/policy/service_test.go (new)
- api/internal/handlers/policies.go (new)
- api/internal/handlers/policies_test.go (new)
- api/internal/services/workspace/workspace_service.go (policy checker + enforcement)
- api/internal/services/database/database.go (count methods)
- api/internal/interfaces/interfaces.go (DatabaseService interface)
- api/internal/mocks/database.go (mock updates)
- api/internal/app/app.go (policy wiring)
- api/internal/server/router.go (policy routes)
- pkg/types/types.go (OrgPolicy, OrgPolicyValues, policy key constants)
- 3 auth test files (mock DB method additions)
