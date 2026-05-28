# Worklog: Epic 10 Production Bug Fixes

**Date:** 2026-05-28
**Session:** Fix critical production bugs found by live validation (worklog 0063)
**Status:** Complete

---

## Objective

Address the 3 critical bugs found during live production validation of Epic 10.

---

## Work Completed

### BUG 2 Fix: sessionID not set in auth service middleware
- Root cause: The router uses `services.GetAuth().AuthMiddleware()` (auth service method at line 547), NOT the middleware package's `AuthMiddleware` function. Our fix was in the wrong location.
- Fix: Added `c.Set("sessionID", jti)` to the auth service's `AuthMiddleware()` method
- Test: `TestAuthMiddleware_SetsSessionID` — fails before fix, passes after

### BUG 3 Fix: No key initialization for existing users
- Root cause: `UnlockDEK` silently returns nil when user has no keys. Pre-Epic 10 users never get keys initialized.
- Fix: Login now calls `HasKeys()` first; if false, calls `InitializeUserKeys()` before `UnlockDEK()`
- Test: `TestAuthMiddleware_LoginAutoInitsKeysForExistingUser`

### BUG A Fix: In-memory adapters (zero persistence)
- Root cause: `app.go` used `dbKeyStoreAdapter` and `dbSecretStoreAdapter` (Go maps) instead of `PgKeyStore`/`PgSecretStore`
- Fix: Create `pgxpool.Pool` in app.go, wire `secrets.NewPgKeyStore(pool)` and `secrets.NewPgSecretStore(pool)`. Falls back to in-memory only if pgxpool creation fails.
- All secrets, keys, bindings, and audit now persist to PostgreSQL

### BUG B Fix: ChangePassword/Recover don't update bcrypt hash
- Root cause: `ChangePassword` only re-wraps DEK with new KEK. The user's `password_hash` in the users table is never updated. User gets locked out.
- Fix: Added `PasswordHashUpdater` interface, `bcryptPasswordUpdater` implementation, wired via `SetPasswordUpdater`. Both `ChangePassword` and `RecoverAccount` handlers now update the bcrypt hash.
- Added `PasswordHash *string` to `UserUpdates` type
- Added `password_hash` handling in `database.UpdateUser`
- Test: `TestE2E_RealAuth_ChangePassword` — proves old fails, new works, secrets survive

### E2E Tests Added
- `TestE2E_RealAuth_SecretCRUD` — register → login → create → list → delete with REAL auth
- `TestE2E_RealAuth_WorkspaceEnv` — PUT/GET/DELETE env vars
- `TestE2E_RealAuth_ChangePassword` — change → old fails → new works → secrets work
- `TestE2E_RealAuth_ChangePassword_WrongOld` — 403
- `TestE2E_RealAuth_Recover` — recovery key → new password → login → old key invalid
- `TestE2E_RealAuth_RotateKey_ThenSecrets` — create before + after rotation

---

## Key Decisions

1. **pgxpool separate from database/sql** — The existing DB service uses `database/sql` with pgx stdlib driver. Rather than refactoring it, we create a separate `pgxpool.Pool` with the same connection params for the secrets stores. This avoids touching the existing DB service.
2. **Graceful fallback** — If pgxpool creation fails (e.g., no DB configured in dev), fall back to in-memory adapters rather than crashing.
3. **PasswordHashUpdater as interface** — Keeps the handler testable without coupling to the database service directly.

---

## Tests Run

```
go test -timeout 120s -short ./...  → 34 packages, 0 failures
TestE2E_RealAuth_* (6 tests) → all PASS
TestAuthMiddleware_SetsSessionID → PASS
TestAuthMiddleware_LoginAutoInitsKeysForExistingUser → PASS
```

---

## Next Steps

1. Deploy latest commit (`0fcde9e`) and re-run the 10-phase production validation
2. Verify Phase 7 (ChangePassword) now passes
3. Verify Phase 10 (Database Verification) shows data in PostgreSQL tables

---

## Files Modified

- `api/internal/services/auth/auth.go` — sessionID in AuthMiddleware, auto-init keys on login
- `api/internal/services/auth/auth_sessionid_test.go` (new)
- `api/internal/services/auth/auth_e2e_secrets_test.go` (new)
- `api/internal/services/auth/auth_e2e_all_test.go` (new)
- `api/internal/app/app.go` — pgxpool wiring, PasswordUpdater
- `api/internal/app/secrets_adapters.go` — bcryptPasswordUpdater
- `api/internal/handlers/secrets.go` — PasswordHashUpdater interface, SetPasswordUpdater
- `api/internal/services/database/database.go` — password_hash in UpdateUser
- `pkg/types/types.go` — PasswordHash field in UserUpdates
- `worklogs/0061_2026-05-28_epic10-verification-and-gap-analysis.md` (renamed from 0057)
