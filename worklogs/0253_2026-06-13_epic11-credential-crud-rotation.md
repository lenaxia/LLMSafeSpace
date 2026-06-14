# Worklog: Epic 11 — US-11.6 Org Credential CRUD + US-11.9 RotateOrgDEK

**Date:** 2026-06-13
**Session:** Complete US-11.6 (org credential CRUD), implement US-11.9 (RotateOrgDEK), fix compilation/test issues
**Status:** In Progress

---

## Objective

Complete the remaining backlog backend stories for Epic 11: US-11.6 (org credential CRUD handler + store) and US-11.9 (full RotateOrgDEK implementation), fix all compilation errors and test regressions, and verify the full project builds.

---

## Work Completed

### US-11.6: Org Credential CRUD (fixed and wired)

The previous session created `org_credentials.go` (handler) and `org_credential_store.go` (PgSecretStore methods) but they had compilation errors and weren't fully wired:
- Fixed `AutoApplyRule` redeclaration — removed duplicate from `org_credential_store.go`; reused existing type from `pg_credential_store.go` (which has `TargetID *string` and `Priority int`)
- Fixed `app.go` shadowing bug: `orgCredsHandler :=` was using short-declare, creating a local variable that shadowed the `var orgCredsHandler` declared at function scope. Changed to `=`.
- Router wiring already present — `registerOrgRoutes` takes `credit *handlers.OrgCredentialsHandler`, registers 7 routes under `orgAdminGroup`
- App wiring already present — `orgCredsHandler` in `RouterConfig.OrgCredentialsHandler`

### US-11.9: Full RotateOrgDEK Implementation

**OrgKeyService (`pkg/secrets/org_key_service.go`):**
- Added `credStore OrgCredentialReEncryptor` field + `SetCredentialStore` setter
- Defined `OrgCredentialReEncryptor` interface (single method: `ReEncryptOrgCredentials`)
- Implemented `RotateOrgDEK(ctx, orgID, adminUserID, adminPassword)`:
  - Fetches old DEK from cache (returns `ErrOrgDEKUnavailable` if not present)
  - Generates new random DEK
  - Derives admin KEK from password + salt via `DeriveKEK` with `orgKEKInfo`
  - Wraps new DEK with admin KEK
  - Starts transaction via `store.BeginTx()`
  - Re-encrypts all org credentials via `credStore.ReEncryptOrgCredentials` (within tx)
  - Deletes all old key members via `store.DeleteAllOrgKeyMembersTx`
  - Inserts rotating admin's new key member via `store.UpsertOrgKeyMemberTx`
  - Sets `pending_key_wrap=true` for all other admins via `store.SetPendingKeyWrapForOtherAdminsTx`
  - Commits transaction (rollback on any error)
  - Caches new DEK with 7-day TTL (`"org:<orgID>"` Redis key)
  - Returns `(count of re-encrypted credentials, nil)`

**OrgKeyStore (`pkg/secrets/org_key_store.go`):**
- Extended interface with 4 transaction-scoped methods:
  - `BeginTx(ctx) (pgx.Tx, error)`
  - `UpsertOrgKeyMemberTx(ctx, tx, record) error`
  - `DeleteAllOrgKeyMembersTx(ctx, tx, orgID) error`
  - `SetPendingKeyWrapForOtherAdminsTx(ctx, tx, orgID, excludeUserID) error`
- Implemented all 4 on `PgOrgKeyStore`

**PgSecretStore (`pkg/secrets/pg_secret_store.go`):**
- Added `ReEncryptOrgCredentials(ctx, tx, orgID, oldDEK, newDEK)`:
  - SELECTs all org credentials FOR UPDATE within tx
  - Decrypts each with old DEK, re-encrypts with new DEK
  - Updates `ciphertext` and bumps `key_version` for each
  - Returns count of re-encrypted credentials

**RotateKey handler (`api/internal/handlers/orgs.go`):**
- Replaced 501 stub with full implementation
- Validates password via `ShouldBindJSON`
- Calls `h.orgKeySvc.RotateOrgDEK(ctx, orgID, userID, []byte(password))`
- Maps `ErrOrgDEKUnavailable` to HTTP 409
- Returns `{message, credentials count, pendingAdmins: true}`

**App wiring (`api/internal/app/app.go`):**
- Added `orgKeyService.SetCredentialStore(pgStore)` to wire credential re-encryption capability

### Test Fixes

**mockOrgKeyStore (`org_key_service_test.go`):**
- Updated `BeginTx` return type from `(interface{}, error)` to `(pgx.Tx, error)`
- Added no-op stubs for `UpsertOrgKeyMemberTx`, `DeleteAllOrgKeyMembersTx`, `SetPendingKeyWrapForOtherAdminsTx`

**Auth test mocks:**
- `dekJKeyService` (`auth_apikey_dek_test.go`): Added `UnlockAllOrgDEKs` no-op
- `capturingKeyService` (`auth_e2e_all_test.go`): Added `UnlockAllOrgDEKs` delegation
- `trackingKeyService` (`auth_sessionid_test.go`): Added `UnlockAllOrgDEKs` no-op

**Workspace tests (`workspace_session_test.go`):**
- Updated 3 error message assertions from "does not own" to org-aware messages:
  - Read ops (ListWorkspaceSessions, MarkSessionSeen): "does not have access to workspace"
  - Destructive ops (RenameWorkspace): "does not have admin access to workspace"

**Unused variables:**
- Suppressed `store` variable in `TestUnlockAllOrgDEKs_BatchQuery` and `TestWrapOrgDEKForNewAdmin_DEKNotInCache` with `_ = store`

---

## Key Decisions

1. **OrgCredentialReEncryptor as minimal interface** — single method, keeps OrgKeyService decoupled from PgSecretStore. Wire-once in app.go.

2. **Transaction scope includes credential re-encryption** — re-encryption happens within the same tx as key member deletion/insertion. If re-encryption fails, key members are not modified (rollback).

3. **7-day TTL on rotated DEK** — extends beyond session TTL. Ensures the new DEK survives short admin absence without requiring immediate re-login. Other admins must accept-key to get the new DEK.

4. **PgSecretStore.ReEncryptOrgCredentials does SELECT FOR UPDATE** — prevents concurrent modification of credentials during rotation. Safe since rotation is admin-gated and infrequent.

---

## Blockers

- Full test suite cannot run locally due to `proxy.golang.org` being unreachable and `GOPROXY=direct` being extremely slow for K8s dependencies. Individual targeted tests pass.
- US-11.11 (Frontend) and US-11.12 (Integration tests) still pending.

---

## Tests Run

- `go build ./...` — full project compiles cleanly (with TMPDIR= in workspace)
- `go test -count=1 -short ./pkg/secrets/` — all secrets tests pass
- `go test -count=1 -short -run TestRegister_Success ./api/internal/services/auth/` — passes
- `go test -count=1 -short -run TestListWorkspaceSessions_WrongOwner ./api/internal/services/workspace/` — passes

---

## Next Steps

1. **US-11.11**: Frontend org management UI
2. **US-11.12**: Integration tests + canary
3. Push branch, open PR, run full CI pipeline
4. Address PR review feedback

---

## Files Modified

- `pkg/secrets/org_credential_store.go` — removed duplicate `AutoApplyRule`, fixed scan field name
- `pkg/secrets/org_key_service.go` — added `OrgCredentialReEncryptor`, `RotateOrgDEK`, `pgx` import
- `pkg/secrets/org_key_store.go` — added `BeginTx`, `UpsertOrgKeyMemberTx`, `DeleteAllOrgKeyMembersTx`, `SetPendingKeyWrapForOtherAdminsTx`
- `pkg/secrets/pg_secret_store.go` — added `ReEncryptOrgCredentials`
- `pkg/secrets/org_key_service_test.go` — updated mock interface, suppressed unused vars, added `pgx` import
- `api/internal/handlers/orgs.go` — implemented `RotateKey`, removed unused `userID` in `AddMember`
- `api/internal/app/app.go` — fixed shadowing bug, wired `SetCredentialStore`
- `api/internal/services/auth/auth_apikey_dek_test.go` — added `UnlockAllOrgDEKs` to mock
- `api/internal/services/auth/auth_e2e_all_test.go` — added `UnlockAllOrgDEKs` to mock
- `api/internal/services/auth/auth_sessionid_test.go` — added `UnlockAllOrgDEKs` to mock
- `api/internal/services/workspace/workspace_session_test.go` — updated error message assertions
