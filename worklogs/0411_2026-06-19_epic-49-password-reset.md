# Worklog: Epic 49 — US-49.5 Password Reset via Email

**Date:** 2026-06-19
**Session:** Implement the full password-reset-via-email flow: migration, EmailService methods, RevokeAllUserSessions, handler, route wiring, and types changes.
**Status:** In Progress (PR pending)

---

## Objective

Build the password-reset-via-email flow end-to-end. This is the largest story in Epic 49: it involves a DB migration, the EmailService message methods, a session-invalidation mechanism, the handler with its security controls (session revocation + post-reset notification + interstitial page), and the route wiring.

---

## Work Completed

### Migration
- `000039_email_tokens.up.sql` — `email_tokens` table (id, user_id, kind, token_hash, expires_at, consumed_at, created_at). One table parameterised by kind for both password-reset and email-verify tokens.
- `000040_user_email_verified.up.sql` — adds `users.email_verified` column, backfills existing users to `true`. Used by US-49.5 (reset gate) and US-49.6 (verification flow).
- Both synced to `charts/llmsafespaces/migrations/`.

### EmailService methods (restored from tranche 1)
- `SendPasswordReset(ctx, to, token)` — builds interstitial link `/reset-password?token=...`, URL-encodes the token (validator finding #4 fix), HTML-escapes the link attribute, states 15-minute expiry.
- `SendPasswordChanged(ctx, to)` — OWASP-mandated post-reset notification. "If this was not you, contact your administrator."
- `buildLink(path, token)` — shared helper with trailing-slash trim + `url.QueryEscape`.

### RevokeAllUserSessions (auth.Service)
- **jti tracking on Login:** `trackUserSession(ctx, userID, jti, ttl)` — stores a JSON array of jtis under `user-sessions:<userID>` in Redis via `SetObject`. Capped at 50 entries; TTL matches token duration. Best-effort (login never fails on tracking error).
- **RevokeAllUserSessions(userID):** reads the jti set, writes `token:<jti> = "revoked"` for each (the existing mechanism `ValidateToken` checks on every request), deletes the set. Reuses the existing revocation infra — no changes to the authenticate-every-request hot path.
- **Why not a token-version counter:** the SET approach pushes all work to login (off hot path) and reset-confirm (rare). The version-counter approach would add a claim to every token and a Redis read to every `ValidateToken` call.

### Password reset handler
- **Request (`POST /api/v1/auth/password-reset/request`):** Always returns 202 (no enumeration). Sends reset email only if user exists AND `email_verified=true` (don't send to unverified mailboxes — design §6.8).
- **Confirm (`POST /api/v1/auth/password-reset/confirm`):** Public (token IS the credential). 5-step flow:
  1. Consume token (single-use, before other steps)
  2. `InitializeUserKeys(userID, newPassword)` — reinitialises DEK (old DEK unrecoverable without old password/recovery key; old encrypted secrets are lost, workspace K8s Secrets unaffected)
  3. `UpdatePasswordHash(userID, newPassword)` — updates bcrypt
  4. `RevokeAllUserSessions(userID)` — revokes all outstanding JWTs (OWASP-mandated)
  5. `SendPasswordChanged(email)` — post-reset notification (OWASP-mandated)
  - Returns new recovery key so the user can save it.

### Types
- `types.EmailToken` — in `pkg/types/email.go` (avoids import cycle between handlers and database packages). Fields match the DB schema.
- `types.User.EmailVerified` — added to the User struct. Flows to the frontend via `AuthResponse.User` (used by US-49.6 for the verification banner).

### Route wiring
- Routes registered in the public auth group (no auth middleware): `POST /api/v1/auth/password-reset/request` and `POST /api/v1/auth/password-reset/confirm`.
- Handler constructed in `app.go` with: PgEmailTokenStore, database Service (user lookups), keyService (DEK reinit), bcryptPasswordUpdater, auth.Service (session revoker via type-assertion), emailService, logger.

---

## Key Decisions

1. **DEK reinitialisation, not rotation.** Password reset via email can't rotate the DEK (no old password or recovery key to decrypt it). `InitializeUserKeys` creates a fresh DEK; the old DEK and its encrypted secrets are lost. This is the correct cryptographic constraint — the recovery-key path (existing `RecoverAccount`) can preserve the DEK because the recovery key can decrypt it. Workspace K8s Secrets are unaffected (they're managed by the controller, not encrypted-at-rest in PG).

2. **email_verified gate on reset request.** Don't send reset links to unverified mailboxes (design §6.8). An unverified user who loses their password falls back to the recovery key. Documented as intended behaviour.

3. **Consume token before other steps.** Single-use is enforced before any credential change. If subsequent steps fail, the token is still consumed — the user must request a new one. This prevents retry-on-consumed-token attacks.

4. **`EmailToken` in `pkg/types/`, not `handlers`.** The database package needs to reference the type (PG store), and handlers also needs it. Putting it in handlers creates an import cycle (handlers ← database). Moving to pkg/types follows the existing convention (`types.OrgInvitation`, etc.).

---

## Blockers

None for this story. US-49.6 (email verification) is blocked on the gate-scope decision (design §6.3).

---

## Tests Run

- `go test -race -run "TestPasswordReset" ./api/internal/handlers/...` — PASS (9 tests: request happy/unknown/unverified/missing-email, confirm happy/expired/consumed/unknown/short-password)
- `go test -race ./api/internal/services/email/...` — PASS (10 tests incl. restored SendPasswordReset/SendPasswordChanged)
- `go test -race ./api/internal/server/...` — PASS (routes registered correctly)
- `go test -race ./pkg/types/...` — PASS
- `go build ./...` — clean

---

## Next Steps

1. PR review + merge.
2. US-49.6 — Email verification on signup (uses the `email_verified` column added here; **blocked on gate-scope decision**).
3. US-49.8 — E2E + integration tests across all flows.

---

## Files Modified

- `api/migrations/000039_email_tokens.up.sql` + `.down.sql` — NEW
- `api/migrations/000040_user_email_verified.up.sql` + `.down.sql` — NEW
- `charts/llmsafespaces/migrations/000039_*` + `000040_*` — synced copies
- `pkg/types/email.go` — NEW: EmailToken type
- `pkg/types/auth.go` — added EmailVerified field to User
- `api/internal/services/email/service.go` — restored SendPasswordReset/SendPasswordChanged/buildLink
- `api/internal/services/email/service_test.go` — restored tests for those methods
- `api/internal/services/auth/auth.go` — trackUserSession + RevokeAllUserSessions + jti hoisting in Login
- `api/internal/services/database/email_tokens.go` — NEW: PgEmailTokenStore
- `api/internal/handlers/password_reset.go` — NEW: PasswordResetHandler (Request + Confirm)
- `api/internal/handlers/password_reset_test.go` — NEW: 9 tests
- `api/internal/app/app.go` — wire PasswordResetHandler + PgEmailTokenStore
- `api/internal/server/router.go` — register public password-reset routes
