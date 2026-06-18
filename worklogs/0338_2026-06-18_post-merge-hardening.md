# Worklog: Post-Merge Hardening — Straightforward Fixes from Validation

**Date:** 2026-06-18
**Session:** Post-merge validation of Epic 43 identified 13 findings. This worklog implements the 5 straightforward fixes; the remaining architectural finding (C1/H1/H2 scattered ownership) is documented in design 0041.
**Status:** Complete — awaiting review

---

## Objective

After all 9 stories of Epic 43 (design 0031) were merged, 4 independent validators reviewed the implementation. This worklog addresses the straightforward (self-contained) findings. The one architectural finding (workspace access middleware) is documented in `design/0041_2026-06-18_workspace-access-middleware.md`.

---

## Fixes Applied

### H3: Invitation Accept email binding (security)
**Problem:** Anyone with an invitation token could accept it as themselves, regardless of which email was invited.
**Fix:** Added `GetUserEmail` to the `invitationStore` interface + `PgOrgStore`. The `Accept` handler now fetches the caller's email and compares (case-insensitive) to `inv.Email`. Mismatch → 403.
**Files:** `invitations.go`, `pg_org_store.go`, `invitations_test.go`

### H5: Credential binding is now fire-and-forget (performance)
**Problem:** `BindAllOrgCredentialsToOrgWorkspaces` (CROSS JOIN) ran synchronously in the accept request path, blocking the user's response.
**Fix:** Wrapped in a `go func()` with `context.Background()`. The user's accept response returns immediately; the binding runs in the background. Updated tests to use `require.Eventually` (polling) since the call is now async.
**Files:** `invitations.go`, `invitations_test.go`

### M1: CreateOrgWithAdmin now migrates owner's personal workspaces (D4 consistency)
**Problem:** `CreateOrgWithAdmin` (platform-admin org creation) didn't migrate the owner's existing personal workspaces to the org, unlike `AcceptInvitationTx` (invitation acceptance). Inconsistent with D4.
**Fix:** Added the same `UPDATE workspaces SET org_id` statement inside `CreateOrgWithAdmin`'s transaction, after the membership insert. Both "join the org" paths now behave identically.
**Files:** `pg_org_store.go`

### M6: Removed dead code (Rule 5)
Removed three dead-code items with zero production callers:
- `CountOrgAdmins` — interface method + impl + mock (zero call sites; `RemoveOrgAdminIfNotLast`/`DemoteOrgAdminIfNotLast` count internally)
- `OrgBilling.CreateCustomer` + `stripeBilling.CreateCustomer` — vestigial from the removed self-service flow
- `CreateOrgResponse.CheckoutURL` — always empty, frontend never reads it
**Files:** `orgs.go`, `pg_org_store.go`, `orgs_test.go`, `org_billing.go`, `org_create_billing_test.go`, `orgs_admin_create_test.go`, `types.go`, `frontend/src/api/orgs.ts`

### M9: Type drift fix (UserRole omitempty)
**Problem:** Backend can return `OrgRole("")` (caller is not a member). TS type was `"admin" | "member"` — empty string unrepresentable.
**Fix:** Added `omitempty` to `UserRole` in `OrgResponse`. Empty role is now omitted from JSON entirely.
**Files:** `types.go`

---

## What's Documented as Design (Not Implemented Here)

**C1/H1/H2 (scattered ownership):** The proxy, secrets API, and terminal all access workspaces without the D5 membership check. This is a pre-existing architectural gap (not a Story 5 regression) — workspace ownership was always scattered. The correct fix is a `WorkspaceAccessMiddleware` (design 0041). This is medium complexity (new middleware, router changes, 3 surface refactors) and belongs in a follow-on epic, not a hotfix.

**M2 (account enumeration):** Validated as FALSE POSITIVE — D1 design-intended (the 404 "owner not found" is the specified response).

**M10 (HardDeleteOrg nulls org_id):** Validated as FALSE POSITIVE — correct by design (hard delete ≠ soft delete; hard delete reaps pending_activation orgs with no members/workspaces).

---

## Tests Run

- `go test -race ./api/internal/handlers/` — PASS (38s)
- `go test -race ./api/internal/services/{database,workspace}/` — PASS
- `go test ./api/internal/server/ ./api/internal/app/` — PASS
- `npx tsc --noEmit` — PASS
- `golangci-lint run` — 0 issues
- `bin/repolint` — all checks passed

---

## Files Modified

- `api/internal/handlers/invitations.go` — H3 email binding; H5 fire-and-forget
- `api/internal/handlers/invitations_test.go` — async test pattern + email mock
- `api/internal/handlers/orgs.go` — M6 removed CountOrgAdmins from interface
- `api/internal/handlers/orgs_test.go` — M6 removed CountOrgAdmins mock
- `api/internal/handlers/orgs_admin_create_test.go` — M6 removed CheckoutURL/customerCalls refs
- `api/internal/handlers/org_billing.go` — M6 removed CreateCustomer from OrgBilling
- `api/internal/handlers/org_create_billing_test.go` — M6 removed CreateCustomer from fakeOrgBilling
- `api/internal/services/database/pg_org_store.go` — M1 migration in CreateOrgWithAdmin; M6 removed CountOrgAdmins; H3 GetUserEmail
- `pkg/types/types.go` — M6 removed CheckoutURL; M9 omitempty on UserRole
- `frontend/src/api/orgs.ts` — M6 removed checkoutUrl
- `design/0041_2026-06-18_workspace-access-middleware.md` (new) — C1/H1/H2 design doc
