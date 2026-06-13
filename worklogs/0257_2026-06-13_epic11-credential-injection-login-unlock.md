# Worklog: Epic 11 — US-11.3, US-11.7, US-11.9 stub, US-11.10

**Date:** 2026-06-13
**Session:** US-11.3 login org DEK unlock, US-11.7 decryptBinding + SeedWorkspaceCredentials, US-11.10 password change re-wrap, US-11.9 rotate-key stub
**Status:** In Progress

---

## Objective

Complete the credential injection and login org DEK unlock stories so org credentials flow end-to-end from creation → workspace seeding → injection.

---

## Work Completed

### US-11.3 (completed): Login Unlocks Org DEKs

- Added `UnlockAllOrgDEKs` to `auth.KeyServiceInterface` (with userSalt accepted but internally fetched)
- Added `UnlockAllOrgDEKs` no-op stub to `KeyService` (satisfies interface when no org support wired)
- Added `UnlockAllOrgDEKs` to `OrgAwareKeyService` (delegates to `OrgKeyService.UnlockAllOrgDEKs`)
- Extended auth `Login` to call `s.keyService.UnlockAllOrgDEKs(...)` after `UnlockDEK` — non-fatal, Warn logged on failure
- Updated `fakeKeyService` in `auth_test.go` with no-op stub so tests compile

### US-11.7: decryptBinding org branch + SeedWorkspaceCredentials fix

**decryptBinding:**
- Added `orgKeySvc *OrgKeyService` field + `SetOrgKeyService` setter to `SecretService`
- Added `case "org":` branch to `decryptBinding` in `injection.go`:
  - Returns explicit error when `orgKeySvc == nil` (not silent)
  - Calls `orgKeySvc.GetOrgDEK(ctx, b.OwnerID)` — wraps `ErrOrgDEKUnavailable` on cache miss
  - Upstream `PrepareSecretsForInjection` catches the error, skips the binding, falls back to lower-priority credential
- Wired `secretService.SetOrgKeyService(orgKeyService)` in `app.go`

**SeedWorkspaceCredentials:**
- Changed signature from `(ctx, workspaceID, userID string)` to `(ctx, workspaceID, userID string, orgID *string)`
- When `orgID == nil`: unchanged personal workspace behaviour
- When `orgID != nil`: adds two additional SQL blocks:
  1. Org auto-apply rules (`credential_auto_apply` where `target_type='org' AND target_id=orgID`)
  2. All org-owned credentials (`provider_credentials` where `owner_type='org' AND owner_id=orgID`) at `within_priority=5`
- Removed the `userID`-as-org-id placeholder from the admin auto-apply block
- Updated everywhere that touches the signature:
  - `CredentialStore` interface in `credential_store.go`
  - `AsyncAuditLogger.SeedWorkspaceCredentials` in `pg_secret_store.go`
  - `CredentialProvisioner` interface in `workspace_service.go`
  - Call site in `workspace_service.go` (now passes `meta.OrgID`)
  - `fakeCredentialProvisioner` in `workspace_service_test.go`
  - `mockCredentialStore` in `credential_precedence_test.go`
  - Integration test call sites in `credential_store_integration_test.go` (passing `nil`)
  - testify `.On("SeedWorkspaceCredentials", ...)` in `workspace_defaults_test.go` (added 4th `mock.Anything`)

### US-11.10: Password Change Re-wrap Org DEK

- Added `orgKeyService *secrets.OrgKeyService` field + `SetOrgKeyService` setter to `RotateKeyHandler`
- Extended `ChangePassword` to call `h.orgKeyService.RewrapAllOrgDEKsForAdmin(...)` after bcrypt update:
  - Non-fatal: `RewrapAllOrgDEKsForAdmin` never returns non-nil
  - Cache ordering preserved: `ChangePassword` evicts `sessionID` DEK key; does NOT evict `org:<orgID>` keys, so `RewrapOrgDEKForAdmin` can still read the org DEK from cache
- Wired `rotateKeyHandler.SetOrgKeyService(orgKeyService)` in `app.go`

### US-11.9 stub: rotate-key endpoint

- Added `RotateKey` handler stub to `OrgsHandler` (returns 501 until full RotateOrgDEK implementation)
- Registered `POST /api/v1/orgs/:id/rotate-key` under `OrgAdminGuard` in router

---

## Key Decisions

1. **UnlockAllOrgDEKs on both OrgAwareKeyService and base KeyService** — The auth `Login` method always calls `UnlockAllOrgDEKs` via the interface, so the base `KeyService` needs a no-op to avoid breaking deployments that don't wire org support.

2. **SeedWorkspaceCredentials org logic in three separate SQL execs** — Following the design doc's "three separate Exec calls" approach to keep each SQL statement clear and independently debuggable. ON CONFLICT DO NOTHING on all three makes them idempotent.

3. **Admin auto-apply userID placeholder removed** — The old `OR (caa.target_type = 'org' AND caa.target_id = $2)` (using userID as org_id) is gone. Now org auto-apply only fires when `orgID` is non-nil and matches real `target_id` values.

4. **RewrapAllOrgDEKsForAdmin is non-fatal** — Per design doc: ChangePassword has already succeeded. Stale wrap is detected at next login; admin is directed to `rotate-key`.

---

## Blockers

- Disk space (~1.1GB free); full compilation blocked. All files pass `gofmt -e`.
- US-11.9 full `RotateOrgDEK` not yet implemented (returns 501).

---

## Tests Run

- `gofmt -e` on all 18 modified files — zero syntax errors

---

## Next Steps

1. US-11.6: Org credential CRUD handler (`org_credentials.go`)
2. US-11.9: Full `RotateOrgDEK` implementation in `OrgKeyService`
3. US-11.11: Frontend org UI
4. US-11.12: Integration tests
5. Push branch and open PR once compilation verified in CI

---

## Files Modified

- `pkg/secrets/key_service.go` — added `UnlockAllOrgDEKs` no-op stub
- `pkg/secrets/org_aware_key_service.go` — added `UnlockAllOrgDEKs` delegation
- `pkg/secrets/secret_service.go` — added `orgKeySvc` field + `SetOrgKeyService`
- `pkg/secrets/injection.go` — added `case "org":` to `decryptBinding`
- `pkg/secrets/credential_store.go` — updated `SeedWorkspaceCredentials` signature
- `pkg/secrets/pg_credential_store.go` — implemented fixed `SeedWorkspaceCredentials` with orgID
- `pkg/secrets/pg_secret_store.go` — updated `AsyncAuditLogger.SeedWorkspaceCredentials`
- `pkg/secrets/credential_precedence_test.go` — updated fake
- `pkg/secrets/credential_store_integration_test.go` — updated call sites
- `api/internal/services/auth/auth.go` — added `UnlockAllOrgDEKs` to interface + login call
- `api/internal/services/auth/auth_test.go` — added `UnlockAllOrgDEKs` to `fakeKeyService`
- `api/internal/services/workspace/workspace_service.go` — updated `CredentialProvisioner` + call site
- `api/internal/services/workspace/workspace_service_test.go` — updated fake
- `api/internal/services/workspace/workspace_defaults_test.go` — updated mock `.On()` args
- `api/internal/handlers/secrets.go` — added `orgKeyService` to `RotateKeyHandler` + `ChangePassword` rewrap
- `api/internal/handlers/orgs.go` — added `RotateKey` stub + `RotateKey` in router
- `api/internal/server/router.go` — registered `POST /orgs/:id/rotate-key`
- `api/internal/app/app.go` — wired `secretService.SetOrgKeyService` + `rotateKeyHandler.SetOrgKeyService`
