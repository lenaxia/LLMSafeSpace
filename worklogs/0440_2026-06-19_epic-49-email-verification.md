# Worklog: Epic 49 — US-49.6 Email Verification (Login Gate Model)

**Date:** 2026-06-19
**Session:** Implement email verification with the login-gate model: unverified users cannot log in until they verify via email link.
**Status:** Complete

---

## Objective

Implement email verification where **login is the gate**. An unverified user with correct credentials is told to verify their email. No endpoint-level checks needed — if you can't log in, you can't do anything.

---

## Work Completed

### Login gate (auth.Service)
- Added `ErrEmailNotVerified` sentinel. `Login` returns it after bcrypt succeeds but `!user.EmailVerified`. Not recorded as a failed attempt (credentials are valid).

### Register flow
- Added `EmailVerifier` interface + `SetEmailVerifier()` on auth.Service.
- When verifier wired (SES): account created unverified, verification email sent.
- When not wired (dev): `UpdateUser` persists `email_verified=true` immediately.
- Added `EmailVerifierAdapter`: creates single-use `email_verify` token (24h TTL), stores hash, sends link via EmailService.

### Verify + Resend handlers
- `POST /api/v1/auth/verify-email` — verify hash → consume → set email_verified=true.
- `POST /api/v1/auth/verify-email/resend` — 202-always, no enumeration, skips verified/unknown users.
- Both have body-size limits (1 MiB MaxBytesReader).
- Token kind validation (`email_verify` only).
- Consume error mapping: `ErrTokenAlreadyConsumed` →410, DB transient →500.

### Types + DB
- `UserUpdates.EmailVerified` added; `database.UpdateUser` handles it.
- `EmailService.SendEmailVerification` restored (interstitial link, URL-encoded, HTML-escaped).
- All `GetUser`/`GetUserByEmail`/`GetUserByAPIKey` SELECTs already include `email_verified` (from PR #296).

### Tests
- 11 handler tests (verify happy/expired/consumed/wrong-kind/unknown/missing + resend happy/unknown/already-verified/invalid-email + adapter).
- 1 login gate test (`TestLogin_UnverifiedUser`).
- 1 Register→Login round-trip (`TestRegister_DevMode_AutoVerifiesAndLoginWorks`).
- All existing register tests updated with UpdateUser mock + EmailVerified=true fixtures.

---

## Key Decisions

1. **Login is the gate, not individual endpoints.** Simpler, covers everything. The user specified this explicitly.

2. **OIDC deferred.** Multi-tenant OIDC verification is a separate concern. SSO users continue to work as before.

3. **Dev-mode auto-verify persists to DB.** The original bug set the in-memory flag but Login reads from DB → permanent lockout. Fixed by calling `UpdateUser`.

4. **Correct-credentials unverified → tell them to verify.** Safe because the attacker already knows the email AND password to reach this branch.

---

## Blockers

None. OIDC verification is a future concern.

---

## Tests Run

- `go test -race -run "TestEmailVerify|TestEmailVerifierAdapter" ./api/internal/handlers/...` — PASS (11 tests)
- `go test -race -run "TestLogin_Unverified|TestRegister" ./api/internal/services/auth/...` — PASS
- `go test -race ./api/internal/server/...` — PASS
- `go test -race ./api/internal/services/email/...` — PASS

---

## Next Steps

1. PR review + merge.
2. Update design docs §6.3/§6.4 to reflect the login-gate model (replaces the 4-option gate analysis).
3. US-49.8 — E2E + integration tests across all flows.

---

## Files Modified

- `api/internal/services/auth/auth.go` — EmailVerifier interface, ErrEmailNotVerified, login gate, register auto-verify
- `api/internal/services/auth/auth_test.go` — login gate test, register mock updates, round-trip test
- `api/internal/handlers/email_verify.go` — NEW: EmailVerifyHandler + EmailVerifierAdapter
- `api/internal/handlers/email_verify_test.go` — NEW: 11 tests
- `api/internal/services/email/service.go` — restored SendEmailVerification
- `api/internal/services/database/database.go` — UpdateUser handles EmailVerified
- `api/internal/app/app.go` — wire EmailVerifyHandler + verifier adapter
- `api/internal/server/router.go` — register verify routes
- `pkg/types/auth.go` — UserUpdates.EmailVerified field
