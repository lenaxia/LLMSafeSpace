# Worklog: Epic 9 Gap Remediation (G1–G5)

**Date:** 2026-05-28
**Session:** Fix all gaps identified in worklog 0056 (credential CRUD API, missing service methods, frontend components)
**Status:** Complete

---

## Objective

Close gaps G1–G5 from worklog 0056's Epic 9 verification:
- G1: No credential CRUD routes in router
- G2: No credential handler file
- G3: Missing service methods (Update, ListForUser, GetDefault)
- G4: No TagInput component (strings type uses comma-separated text)
- G5: No Create credential form in AdminCredentialsTab
- G6: Flaky singleflight test (already fixed in remote — confirmed identical to my fix)

---

## Work Completed

### G3: Missing Service Methods (`pkg/credentials/service.go`)
- Added `Update(ctx, id, req)` — partial update with re-encryption when providers change
- Added `ListForUser(ctx, userID)` — filters by `assigned_to` containing "all" or the user ID
- Added `GetDefault(ctx)` — delegates to store's `GetDefault`, returns nil if none set
- Added `isAssignedToUser` helper for JSON-based assignment filtering
- TDD: 7 new tests written first (Update name, Update providers re-encrypts, Update not found, GetDefault exists, GetDefault none, ListForUser all, ListForUser excludes other users)
- Also fixed mock store's `UpdateCredentialSet` to handle all fields (was only handling Name)

### G1/G2: Credential Handler + Route Registration
- Created `api/internal/handlers/credentials.go` with 7 endpoints:
  - `CreateCredentialSet` (POST) — validates name+providers required, returns 201
  - `GetCredentialSet` (GET /:id) — returns 404 for not found
  - `ListCredentialSets` (GET) — returns empty array if none
  - `UpdateCredentialSet` (PUT /:id) — partial update
  - `DeleteCredentialSet` (DELETE /:id) — returns 409 if referenced
  - `SetDefaultCredentialSet` (PUT /:id/default)
  - `RotateCredentialKey` (POST /rotate-key)
- Created `api/internal/handlers/credentials_test.go` — 10 handler tests covering happy + unhappy paths
- Defined `CredentialServiceInterface` for testability (handler depends on interface, not concrete type)
- Registered routes in `router.go` under `/api/v1/admin/credentials` with `AuthMiddleware()` + `AdminGuard()`
- Added `CredentialsHandler` field to `RouterConfig`
- Wired in `app.go`: creates `credentials.Service` with DB store + encryption key set

### Database Store (`api/internal/services/database/credentials.go`)
- Implements full `credentials.Store` interface (11 methods)
- Compile-time interface check: `var _ credentials.Store = (*Service)(nil)`
- Dynamic UPDATE query builder for partial updates
- Transaction-based `SetDefault` (clear all → set one)
- `CountWorkspacesUsingCredentialSet` for deletion guard

### Encryption Key Loading (`app.go`)
- `loadCredentialKeySet` reads `LLMSAFESPACE_CREDENTIAL_ENCRYPTION_KEY` env var (hex-encoded 32 bytes)
- Falls back to random key for development (not persisted across restarts)

### G4: TagInput Component (`frontend/src/components/ui/TagInput.tsx`)
- Chip/tag UI with add (Enter/comma) and remove (X button/Backspace)
- Accessible: proper aria-labels, keyboard navigation
- Wired into `SettingsForm.tsx` for `strings` type settings (replaces comma-separated text input)

### G5: Create Credential Form (`AdminCredentialsTab.tsx`)
- "Add" button in header toggles `CreateCredentialForm`
- Form: name input, provider builder (name + API key + optional base URL), submit/cancel
- Providers shown as removable list items
- On success: appends to list and closes form
- Error handling with inline message

### G6: Flaky Singleflight Test
- Confirmed the fix (adding `getLatency = 10ms` to mock store) was already in the remote branch from a prior commit. My local edit was identical — no diff.

---

## Key Decisions

1. **Handler uses interface, not concrete service** — `CredentialServiceInterface` allows testing without crypto/DB dependencies.
2. **Error classification by string matching** — "not found" → 404, "referenced" → 409. Acceptable for now; could use typed errors later.
3. **Encryption key from env var** — Standard pattern for secrets. Random fallback for dev only.
4. **Database store uses dynamic SQL for partial updates** — Avoids overwriting unset fields.

---

## Assumptions Validated

| Assumption | Validation |
|---|---|
| `credentials.Store` interface has all needed DB methods | Verified: `pkg/credentials/service.go` lines 12-22 |
| `CredentialSetUpdates` struct exists for partial updates | Verified: `pkg/credentials/service.go` lines 44-51 |
| `UpdateCredentialSetRequest` type exists | Verified: `pkg/credentials/types.go` |
| pgx/v5/stdlib handles TEXT[] arrays | Verified: `go build` succeeds with `[]string` scan targets |
| `AdminGuard()` middleware available | Verified: `api/internal/middleware/admin_guard.go` |
| `RouterConfig` pattern for optional handlers | Verified: existing `SettingsHandler`, `SecretsHandler` fields |

---

## Blockers

None.

---

## Tests Run

```
$ go test -timeout 120s -race -short ./...
# All packages pass (0 failures)

$ go test -timeout 30s -race -count=5 -run TestInstanceService_Singleflight ./pkg/settings/...
# 5/5 passes (flake fixed)

$ go test -timeout 30s -race ./pkg/credentials/...
# All 25+ tests pass including 7 new ones

$ go test -timeout 30s -race ./api/internal/handlers/...
# All handler tests pass including 10 new credential tests

$ npx tsc --noEmit (frontend)
# Clean — no type errors
```

---

## Next Steps

1. **Integration test**: Write an e2e test that exercises the full credential CRUD flow through the router (similar to `api_flow_test.go`)
2. **US-9.16**: Implement preferred model + chat integration (reads `preferredModel` from user settings, filters model picker by credential set allowlist)
3. **D8 verification**: Check if Tier 2 fields removed from Config struct (Auth.RegistrationEnabled, Auth.Lockout*, RateLimiting.*)
4. **Edit credential form**: Add inline edit capability to `AdminCredentialsTab` (currently only create/delete/set-default)

---

## Files Modified

### Created
- `api/internal/handlers/credentials.go` — Credential CRUD handler (7 endpoints)
- `api/internal/handlers/credentials_test.go` — 10 handler tests
- `api/internal/services/database/credentials.go` — DB store implementation (11 methods)
- `frontend/src/components/ui/TagInput.tsx` — Tag/chip input component

### Modified
- `api/internal/app/app.go` — Wire credential service + handler + loadCredentialKeySet helper
- `api/internal/server/router.go` — Add CredentialsHandler to RouterConfig + registerCredentialRoutes
- `frontend/src/components/settings/AdminCredentialsTab.tsx` — Add Create form + Add button
- `frontend/src/components/settings/SettingsForm.tsx` — Wire TagInput for strings type
- `pkg/credentials/service.go` — Add Update, ListForUser, GetDefault, isAssignedToUser
- `pkg/credentials/service_test.go` — 7 new tests + fix mock UpdateCredentialSet
