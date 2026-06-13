# Worklog: Epic 11 — US-11.6 Org Credential CRUD + US-11.9 RotateOrgDEK foundation

**Date:** 2026-06-13
**Session:** US-11.6 org credential CRUD handler and store, US-11.9 RotateOrgDEK store support
**Status:** In Progress

---

## Objective

Implement org credential CRUD so org admins can create/list/update/delete org-level LLM provider credentials. Also lay the store foundation for RotateOrgDEK.

---

## Work Completed

### US-11.6: Org Credential CRUD

**Store** (`pkg/secrets/org_credential_store.go`):
- Defined `OrgCredentialMetadata`, `OrgCredentialRow`, `AutoApplyRule` types
- Defined `OrgCredentialStore` interface: `CreateOrgCredential`, `ListOrgCredentials`, `GetOrgCredential`, `UpdateOrgCredential`, `DeleteOrgCredential`, `BindCredentialToAllOrgWorkspaces`, `CreateOrgAutoApply`, `ListOrgAutoApply`, `DeleteOrgAutoApply`
- All implemented on `PgSecretStore` with proper `owner_type='org'` scoping

**Handler** (`api/internal/handlers/org_credentials.go`):
- `Create` — encrypts `LLMProviderData` with org DEK (from cache), stores with `owner_type='org'`, binds to all existing org workspaces
- `List` — returns names/providers/timestamps only, no ciphertext
- `Update` — re-encrypts with org DEK when API key changes, bumps key_version
- `Delete` — removes credential; FK cascade handles bindings and auto-apply
- `CreateAutoApply` / `ListAutoApply` / `DeleteAutoApply` — org-scoped auto-apply with `target_type='org'`
- All protected by `OrgAdminGuard` (consistent with design doc)

**Wiring**:
- `RouterConfig.OrgCredentialsHandler` added
- Routes registered under `OrgAdminGuard` at `/api/v1/orgs/:id/credentials`, `/credentials/:credID`, `/credentials/:credID/auto-apply`
- Handler constructed and wired in `app.go`

### US-11.9 (foundation): RotateOrgDEK store support

- Extended `OrgKeyStore` interface with `BeginTx`, `UpsertOrgKeyMemberTx`, `DeleteAllOrgKeyMembersTx`, `SetPendingKeyWrapForOtherAdminsTx`
- All four implemented on `PgOrgKeyStore` using `pgx.Tx` parameter
- Added `ReEncryptOrgCredentials(ctx, tx, orgID, oldDEK, newDEK)` to `PgSecretStore` — decrypts each org credential with old DEK, re-encrypts with new DEK, atomically within tx. Uses `FOR UPDATE` row locking.

---

## Key Decisions

1. **`OrgCredentialStore` as separate interface** (not merged into `CredentialStore`) — follows Interface Segregation; org credential operations have different access patterns (scoped to org, admin-only) than personal/user credential operations.
2. **Auto-apply defaults to `within_priority=5`** — slots between user-auto (10) and admin-auto (0) in the credential priority tier.
3. **RotateOrgDEK uses `pgx.Tx`** — all store methods that participate in rotation accept a `pgx.Tx` parameter for atomic multi-table updates. `BeginTx` on `OrgKeyStore` keeps transaction management out of the service layer.

---

## Blockers

- Disk space (~1.1GB free); full compilation blocked
- The mock `mockOrgKeyStore` in `org_key_service_test.go` needs `BeginTx`, `UpsertOrgKeyMemberTx`, `DeleteAllOrgKeyMembersTx`, `SetPendingKeyWrapForOtherAdminsTx` before tests will compile

---

## Tests Run

- `gofmt -e` on all new/modified files — zero syntax errors

---

## Next Steps

1. Update `mockOrgKeyStore` in `org_key_service_test.go` with new interface methods
2. US-11.9: Full `RotateOrgDEK` method in `OrgKeyService` + handler
3. Verify full compilation in CI
4. Push branch and open PR

---

## Files Modified

- `pkg/secrets/org_credential_store.go` (new) — OrgCredentialStore interface + PgSecretStore implementation
- `api/internal/handlers/org_credentials.go` (new) — OrgCredentialsHandler
- `pkg/secrets/org_key_store.go` — added BeginTx, UpsertOrgKeyMemberTx, DeleteAllOrgKeyMembersTx, SetPendingKeyWrapForOtherAdminsTx to interface + PgOrgKeyStore
- `pkg/secrets/pg_secret_store.go` — added ReEncryptOrgCredentials
- `api/internal/server/router.go` — OrgCredentialsHandler in RouterConfig + registerOrgRoutes
- `api/internal/app/app.go` — wire orgCredsHandler
