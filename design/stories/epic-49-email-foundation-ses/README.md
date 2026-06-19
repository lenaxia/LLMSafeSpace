# Epic 49: Email Foundation via SES

**Status:** Scoped — pending implementation
**Created:** 2026-06-19
**Depends on:** Epic 9 (Configuration & Settings), Epic 11/Epic 43 (Organizations — invitations are the existing consumer)
**Design doc:** [`design/0042_2026-06-19_email-foundation-ses.md`](../../0042_2026-06-19_email-foundation-ses.md)

---

## Problem

The platform has a working `EmailProvider` abstraction with an SES implementation, but it is half-wired: no helm config, env-var-only, no IRSA docs, and it powers only org invitations. Two high-value auth flows (password reset, email verification) are missing, and there is dead config (`bounce_type` columns, `notifyOnSessionComplete`/`notifyOnWorkspaceReady` settings) that this epic does **not** revive but flags.

**Goal:** make SES production-ready end-to-end and deliver password reset + email verification, with a helm-precedence "read-only when helm-managed" UX model. SMTP is a future provider behind the same interface — not in scope here.

**Non-goals:** SMTP provider, SES bounce webhook, reviving the dead notification settings, retrofitting Tier-1 onto relay/billing/SSO.

---

## Prerequisites (operator responsibilities)

- AWS account with SES enabled in the target region
- A verified SES identity (domain or email address) that matches `email.fromAddress`
- An IAM role with `ses:SendEmail` permission, trusted by the API pod's IRSA OIDC provider
- If SES is in sandbox mode: recipient addresses must also be verified (production requires moving out of sandbox)

These are operator-side; the epic documents them in NOTES.txt / chart README but cannot automate them.

---

## User Stories

### US-49.1 — Helm `email:` config + ConfigMap rendering

**Files:** `charts/llmsafespaces/values.yaml`, `charts/llmsafespaces/templates/configmap-api.yaml`, `charts/llmsafespaces/templates/_helpers.tpl` (if helper needed), `charts/llmsafespaces/README.md`

- Add `email:` section to `values.yaml`: `enabled` (bool, default false), `provider` (string, default `""`), `sesRegion`, `fromAddress`, `baseUrl`
- Render email block into `configmap-api.yaml` (mirrors the existing `auth`/`rateLimiting`/`security` rendering pattern)
- Document IRSA annotation example in chart README: set `serviceAccount.api.annotations."eks\.amazonaws\.com/role-arn"` (the `serviceaccount.yaml` template already renders annotations — no template change needed)
- Add a NOTES.txt warning when `email.enabled=true` but `provider=ses` and no IRSA annotation is set
- **Acceptance:** `helm template` renders the email block; `helm install` with SES values produces a pod that constructs `SESProvider` at boot (verified via `/readyz` green + a test send in US-49.4)

### US-49.2 — Tier-1 helm-precedence settings model

**Files:** `pkg/settings/schema.go`, `pkg/settings/instance_service.go`, `api/internal/handlers/settings.go`, `api/internal/config/config.go`

- Add a `ReadOnly bool` field (or `Locked`) to `SettingDef`; the schema endpoint surfaces it so the frontend can disable fields
- Add email settings to the Tier-2 schema: `email.provider`, `email.sesRegion`, `email.fromAddress`, `email.baseUrl` (string type; provider is enum `["", "ses"]`)
- On boot, ingest the helm-managed-key list from config; for each managed key, mark its `SettingDef.ReadOnly=true` and override the served value with the helm value
- `Set()` rejects writes to read-only keys with a typed error → handler returns **409 Conflict** `"setting is helm-managed"`
- **Acceptance:** with helm email block set, `GET /admin/settings` returns email keys with `readOnly: true`; `PUT /admin/settings/email.fromAddress` returns 409; with helm email block omitted, fields are editable and `PUT` succeeds

### US-49.3 — Admin UX: Email section (read-only vs editable)

**Files:** `frontend/src/api/settings.ts`, `frontend/src/components/settings/SettingsForm.tsx` (or new `EmailSettingsSection.tsx`), `frontend/src/pages/AdminSettingsPage.tsx`

- SettingsForm already renders by category; ensure `readOnly`/`disabled` is honoured on `SettingControl` (the `disabled` prop is already plumbed at `SettingsForm.tsx:13,74` — verify the schema's `readOnly` flows into it)
- When a setting is read-only, show a "Managed by Helm" badge next to the label
- Group email keys under an "Email" category header
- After a successful edit to a non-helm-locked email key, show a "Restart required for changes to take effect" toast (per A8 — restart-acceptable)
- **Acceptance:** with helm-managed email, the Email section shows values disabled + badge; with helm-omitted, fields are editable and saving shows the restart notice

### US-49.4 — Test-send endpoint

**Files:** `api/internal/handlers/settings.go` (or new `email_test_send.go`), `api/internal/server/router.go`

- `POST /api/v1/admin/email/test` with body `{ "to": "<address>" }`, admin-only
- Sends a fixed test email via the resolved `EmailProvider` (SES in prod, Noop in dev)
- Returns `200 { sent: true }` on success or a structured error: identity-not-verified, region-misconfigured, IRSA-missing — mapped from the SES SDK error, not leaked verbatim
- Rate-limited (e.g. 5/hour/admin) to prevent abuse
- With NoopProvider, returns `200 { sent: false, provider: "noop" }` so admins know no real email went out
- **Acceptance:** in dev (Noop), returns provider=noop; in SES, returns sent=true and the recipient receives the email; SES rejection returns a mapped 4xx with a helpful message (not a raw 500)

### US-49.5 — Password reset via email (request + confirm)

**Files:** new `api/internal/handlers/password_reset.go`, `api/internal/services/auth/auth.go` (extend with `RevokeAllUserSessions`), `api/migrations/000039_email_tokens.up.sql` + `.down.sql`, `api/internal/server/router.go`, `api/internal/app/app.go`, frontend `ResetPasswordPage.tsx`

This story implements three OWASP-mandated security controls that the original scoping omitted. Each is a hard requirement, not optional hardening.

#### 5.1 Endpoints + token lifecycle

- Migration creates `email_tokens` table (see design §5.4)
- `POST /api/v1/auth/password-reset/request { email }` — public, returns **202 always** (no enumeration, mirrors `auth.go`'s existing pattern at `:704-710`). If user exists + `email_verified=true`, generate token, store hash, send email. If not found or unverified, no-op but still 202.
- `POST /api/v1/auth/password-reset/confirm { token, newPassword }` — public. Verify hash, check expiry, consume (single-use), then execute the four-step confirm sequence (§5.2–5.5 below).
- Token expiry: **15 minutes** (account takeover is time-sensitive). Single-use (`consumed_at` set on confirm).

#### 5.2 Credential rotation (existing logic, reused)

Call existing `RotateKeyWithPassword` so secrets stay decryptable, update bcrypt hash via existing `PasswordHashUpdater`. Reuse the `RecoverAccount` logic path at `secrets.go:679-708` — extract the shared "rotate DEK + update bcrypt" into a helper so both paths use it. This is the original requirement; no change.

#### 5.3 Session invalidation (Gap 1 — OWASP mandated, NEW)

**Threat:** attacker steals a JWT. Victim discovers the compromise, resets their password. Without session invalidation, the attacker's stolen JWT keeps working until it expires — up to **30 days** for remember-me tokens (`auth.go:736-738`). That is unacceptable.

**Control:** after successful password rotation, revoke **all** of the user's outstanding sessions.

**Implementation — reuse existing revocation infra, no hot-path changes:**

The existing `RevokeToken` (`auth.go:228-293`) stores `token:<jti> = "revoked"` in Redis, and `ValidateToken` (`auth.go:407-411`) checks it on every request. To revoke all sessions, we feed that same mechanism:

1. **On login** (`auth.go:740`, after `GenerateTokenWithDuration`): `SADD user-sessions:<userID> <jti>` into a Redis SET, with `EXPIRE` set to the token duration. One extra Redis call on login — off the hot path.
2. **New method** `RevokeAllUserSessions(userID)`:
   - `SMEMBERS user-sessions:<userID>` → list of jtis
   - For each jti: `SET token:<jti> "revoked" <remaining-ttl>` (reuses the EXACT key pattern `ValidateToken` already checks)
   - `DEL user-sessions:<userID>`
3. **On password-reset confirm:** call `RevokeAllUserSessions(userID)` after credential rotation, before returning 200.

**Why this approach over a token-version counter:** the SET approach requires zero changes to JWT claims or `ValidateToken` — it only writes keys the existing jti-check already reads. The version-counter approach would add a claim to every token and a Redis read to every `ValidateToken` call (the hot path). The SET approach pushes all work to login (off hot path) and reset-confirm (rare). Stale SET entries are bounded by the SET TTL (= max token duration); expired jtis in the SET are harmless because `ValidateToken` rejects expired tokens via the `exp` claim before the revocation check matters.

**Why not just revoke the one presented token:** password-reset confirm is unauthenticated (the token IS the credential). The API has no idea which JWTs are outstanding — it must revoke them all via the tracked jti set.

#### 5.4 Post-reset notification email (Gap 2 — OWASP mandated, NEW)

**Threat:** attacker completes a password reset (they had the email inbox). The legitimate owner has no signal until they try to log in and fail. Early detection limits damage.

**Control:** after successful confirm, send a "your password was changed" email to the same address via `EmailService.Send`. Body: *"Your LLMSafeSpaces password was changed. If this was you, no action is needed. If this was not you, contact your administrator immediately or use your recovery key."*

This is a notification, not a gate — it does not block the reset. It gives the victim the earliest possible signal. Send failure is logged but does not fail the reset (the credential change already succeeded; failing here would leave the user with a changed password and a 500).

#### 5.5 Interstitial page — GET must not consume (Gap 3 — NEW)

**Threat:** enterprise email scanners (Proofpoint, Mimecast, Office ATP "detonation") follow links in emails to scan them. If the link itself consumes the token (e.g. `GET /verify?token=...` sets `consumed_at`), the scanner consumes it and the real user's later click gets 410. This is a documented production failure mode, not theoretical.

**Control:** the email link must land on a **frontend page that renders a form**, not an API endpoint that consumes.

- Email link: `{baseUrl}/reset-password?token=...` (frontend route, not API)
- Frontend `ResetPasswordPage.tsx`: reads `?token=`, renders "Set your new password" + password input + [Reset Password] button
- Button calls `POST /api/v1/auth/password-reset/confirm { token, newPassword }` — the POST consumes
- A scanner hitting the frontend GET just gets HTML; it cannot fill and submit the form (basic scanners); advanced sandboxed scanners that do fill forms are a residual risk documented below

**Residual risk:** advanced "detonation" sandboxes (Office ATP Safe Links in some configs) render the page and click buttons. The interstitial defeats the majority of scanners (link-followers) but not all. Full mitigation requires a two-step code (email delivers a 6-digit code the user types in), which is out of scope for MVP. The interstitial is the industry-standard first line of defence and is consistent with the existing invitation pattern (`invitations.go` — see US-49.9 §9.3). Document the residual risk in the design; revisit if production reports scanner-consumed tokens.

#### 5.6 Acceptance criteria

- Request returns 202 for known + unknown emails (identical response shape, identical timing within bcrypt-dummy variance)
- Request for unverified email returns 202 but sends no email (no enumeration)
- Confirm with valid token resets password; old password no longer logs in
- Confirm with consumed/expired token returns 410
- **Confirm revokes all outstanding sessions** — a second JWT (issued before reset, different jti) is rejected by `ValidateToken` after reset (regression test)
- **Confirm sends a "password changed" notification** to the user's email
- **Email link lands on a frontend page**, not a consuming API endpoint — verified by confirming the link URL is a frontend route, not `/api/...`
- Recovery-key path still works independently (not affected by session revocation — it issues a new token)

### US-49.6 — Email verification on signup

**Files:** `pkg/types/auth.go` (add field), `api/internal/services/auth/auth.go` (extend register + login surface), `api/internal/services/sso/sso.go` (mark SSO users verified), new `api/internal/handlers/email_verify.go`, new `api/internal/services/email/verify.go`, `api/migrations/000040_user_email_verified.up.sql` + `.down.sql`, `api/internal/server/router.go`, frontend `VerifyEmailPage.tsx` + banner

This story has more decision surface than the others, so it is broken into explicit sub-sections. The gate decision (§6.6.2) blocks implementation start.

#### 6.1 Data model

- Migration adds `users.email_verified BOOLEAN NOT NULL DEFAULT false`.
- Add `EmailVerified bool` to the `User` struct (`pkg/types/auth.go:9-19`). Flows to the frontend automatically via the existing `AuthResponse{User: *user}` return at `auth.go:763` — no new response shape needed.
- **Decision — separate field, not folded into `Status`:** `Status` (`UserStatusActive`/`UserStatusSuspended`) is operational — an admin suspends an account. `EmailVerified` is provenance — the mailbox was reachable at signup. Conflating them violates single-responsibility on the field (one boolean would mean "active AND verified", making admin-suspend indistinguishable from unverified). Keep them separate.
- **Backfill:** `UPDATE users SET email_verified = true` for all existing rows. Rationale: existing users authenticated with their email at signup pre-feature; locking them out of any gate is hostile. This is the same precedent as migration `000037` (user status) which defaulted existing users to active.

#### 6.2 The gate decision (BLOCKER — needs user confirmation)

What does `email_verified` gate? This determines the size and risk of the story. Design §6.3 lists the same options; this is the full tradeoff analysis.

| Option | What's blocked for unverified users | Pros | Cons / risks |
|--------|-------------------------------------|------|--------------|
| **(a) Nothing — advisory only** | Nothing. A dismissible banner nags the user to verify. | Smallest blast radius; no one is ever locked out; pure UX nudging. | Verification does nothing useful; invites/password-reset still trust the mailbox without proof. Defeats the purpose of the feature. |
| **(b) Invitation acceptance only** *(recommended MVP)* | `POST /api/v1/invitations/:token/accept` returns 403 if `!email_verified`. Everything else allowed. | Closes the token-theft → account-takeover chain at the only point today where an external token grants org membership. Matches the existing email-match defence at `invitations.go:317` (which proves the JWT user's email equals the invited email; verification adds "and we proved the mailbox is theirs"). Minimal scope. | A stolen-jwt attacker can still do non-invitation actions as the unverified user — but that attacker already has the JWT, so verification adds nothing there. |
| **(c) Destructive/privilege actions** | Invitation accept + credential creation + secret write + org admin actions. Read-only allowed. | Stronger posture; verification gates anything that creates durable state. | Larger surface to instrument; must enumerate every "destructive" endpoint (error-prone, easy to miss one). Risk of locking out legitimate new users mid-workflow. |
| **(d) All non-read actions** | Login itself is allowed (so they can see the banner), but every mutating endpoint returns 403 until verified. | Strongest. | Hostile to users whose verification email landed in spam; they log in, can do nothing, may not know why. High support burden. Existing unverified-by-mail users (post-backfill, none today) would be locked out. |

**Recommendation: (b).** It is the minimal gate that delivers real security value at the one externally-tokened entry point, and it composes cleanly with the existing `invitations.go:317` email-match check. Expanding to (c)/(d) is a separate product decision once the verification primitive exists and operators see real signup conversion data.

**This must be confirmed before US-49.6 implementation begins.**

#### 6.3 Login behaviour for unverified users

The current `Login` (`auth.go:681-764`) gates on `Status==Suspended` (`:719`) and `!Active` (`:725`) only. It does **not** check email verification, and it should not start to — regardless of which gate option is chosen above:

- Under (a)/(b): unverified users must log in to reach the "verify your email" flow and the resend endpoint. Blocking login traps them.
- Under (c)/(d): login is the one action that must remain open so the user can see *why* they're gated and act on it. Blocking login produces a "can't log in, can't verify, can't reset" deadlock — the only escape is the recovery key, which most users won't have saved.

**Decision: login always succeeds for unverified users (given correct credentials).** The `AuthResponse.User.EmailVerified=false` flag is the signal the frontend uses to render the verification banner. The chosen gate option is enforced at the gated endpoint(s), not at login.

#### 6.4 SSO users — must be marked verified

SSO users do not pass through `Register` (`auth.go:559`). They are created or resolved in `sso.go:420` (`resolveUser`) with `Status: UserStatusActive` (`sso.go:446`), and the IdP token's `email` claim is the source of their email (`sso.go:385-386` rejects a missing email claim).

The IdP is the verification authority for SSO users — the operator's contract with the IdP is "the emails it asserts are real and owned." Therefore:

- `resolveUser` must set `EmailVerified = true` for both the auto-provisioned new user and the existing user on re-login.
- Without this, any gate would break SSO login entirely (SSO users could never verify via email because they have no password and no password-reset path), which is wrong.
- Test: `sso_test.go` gains a case asserting `EmailVerified == true` after callback for both new and existing users.

#### 6.5 Endpoints + token lifecycle

- `POST /api/v1/auth/verify-email { token }` — public. Verify hash, check expiry, consume (single-use), set `email_verified=true`. Returns 200 on success, 410 on expired/consumed.
- `POST /api/v1/auth/verify-email/resend { email }` — public. **This endpoint is required**, not optional: the verification email can land in spam, the user may close the tab before clicking, or the SES send may have transiently failed (see §6.6). Without resend, an unverified user with no recovery key is permanently stuck under any gate. Returns 202 always (no enumeration, mirroring password-reset request). Rate-limited (e.g. 3/hour/email) to prevent abuse.
- **Multiple outstanding tokens:** allow. If the user clicks resend twice, two valid tokens exist. Cheap (rows are small), and single-use + 24h TTL bounds the set. Do **not** invalidate prior tokens on resend — that races with an in-flight click and offers no security gain (each token is already single-use and hashed).
- **Interstitial page — GET must not consume (Gap 3):** the email link lands on a **frontend page** (`{baseUrl}/verify-email?token=...` → `VerifyEmailPage.tsx`), not a consuming API endpoint. The page shows "Verify your email for LLMSafeSpaces" + a [Verify Email] button; the button POSTs to `/api/v1/auth/verify-email { token }`, which is the only thing that consumes. A scanner hitting the frontend GET just gets HTML. **Never implement `GET /api/v1/auth/verify-email?token=...` that consumes directly** — that is the scanner-vulnerable pattern. This matches the password-reset interstitial (US-49.5 §5.5) and the existing invitation invariant (US-49.9 §9.3).
- **Token in URL:** the verification link carries the token in the query string. Risks: Referer header, browser history, email forwarding. Mitigations already accepted by the invitation flow (`invitations.go:390`, same pattern): single-use (consumed on first POST), 24h expiry, hash-only storage. The confirming request is a POST with the token in the body, so it does not leak via Referer. This matches the codebase's existing accepted tradeoff for invitation tokens.
- **Expiry:** 24 hours (signup is not urgent; password-reset in US-49.5 is 15m because account takeover is time-sensitive).

#### 6.6 Failure modes

- **Send fails at register (SES transient error):** fail-**open**. The account is created with `email_verified=false`, the verify-token row is still written, and the failure is logged. The user uses the resend endpoint (§6.5) to get the email. Fail-closed (abort the registration) loses the signup on a transient SES blip and gives the user no recovery path. The resend endpoint is precisely what makes fail-open safe.
- **Send fails at register (SES permanent error — e.g. identity not verified):** same — fail-open, log loudly so the operator notices their SES config is broken. The user is not penalised for the operator's misconfiguration.
- **Register when provider is Noop:** set `email_verified=true` immediately. No email to verify with in dev; the gate (any option) is a no-op in Noop mode because everyone is verified. This keeps local dev and air-gapped deployments working without an email provider.
- **Race — verify then login in two tabs:** user clicks verify (sets `email_verified=true`) while a login request is in flight that read the user before the verify commit. The login returns `EmailVerified=false` in its response snapshot, but the DB is now true. Acceptable: the frontend re-fetches user state on the next page load / on receiving the verify-success response; the stale value is momentary and self-heals. Do not add a lock for this — it is cosmetic and the existing login/refresh pattern already tolerates snapshot staleness.
- **User never verifies:** under gate (b), they can use the platform except invitation acceptance, indefinitely. No forced re-verification, no account reaping. Verification is a gate, not a prerequisite for account existence.

#### 6.7 What "verified" does and does not mean

Verified means: "at time T, a link was sent to this mailbox and someone with access to it clicked it." It does **not** mean:

- The user still controls the mailbox (email accounts are compromised/transferred)
- The mailbox is the user's forever (no re-verification on a schedule)
- The signup email wasn't a typo for a mailbox the attacker does control (they verified their own mailbox, then used someone else's identity elsewhere — out of scope; this is why invitation-acceptance *additionally* checks the JWT user's email matches the invited email at `invitations.go:317`)

The gate is a point-in-time ownership proof composing with the existing JWT-email-match defence — not a perpetual identity guarantee. Document this in the design so future readers don't over-trust the flag.

#### 6.8 Cross-feature interactions

- **Password reset (US-49.5):** reset-request requires `email_verified=true` (don't send a reset link to an unverified mailbox — that would let an attacker who signed up with someone else's email lock them out). Consequence: an unverified user who loses their password can only recover via the recovery key (US-49.5 keeps that path). This is correct and must be documented as intended behaviour, not a bug. If a user complains "I can't reset my password," the answer is "verify your email first, or use your recovery key."
- **Email change (does not exist today):** no endpoint lets a user change their email, so there is no re-verification concern in MVP. Flag: if email-change is ever added, it **must** re-verify the new address before committing the change and flipping `email_verified` back to false until re-verified. Tracked as a future interaction, not built here.
- **Admin user-management (Epic 43):** if an admin can later set a user's email, that path must also re-verify. Out of scope here; flagged.

#### 6.9 Acceptance criteria

- Migration adds `email_verified` defaulting false; existing users backfilled to true
- `User` struct carries `EmailVerified`; login/register/SSO responses surface it
- New user in SES mode receives verification email at register; `email_verified=false`
- `POST /verify-email { token }` with valid token → 200, `email_verified=true`, token consumed
- Expired or already-consumed token → 410
- `POST /verify-email/resend { email }` → 202 for known and unknown emails (identical); rate-limited
- SSO users (new + existing on re-login) have `email_verified=true` with no email sent
- Noop mode: users start verified; no verification email sent
- Register send failure (SES down) → account still created, `email_verified=false`, failure logged; resend works once SES recovers
- Gate per §6.2 confirmed option enforced at the chosen endpoint(s) with a clear 403 body
- Frontend: `/verify-email` page consumes `?token=`; unverified users see a dismissible banner with a "Resend verification" action; banner clears on successful verify

### US-49.7 — Email service + wiring

**Files:** new `api/internal/services/email/service.go`, `api/internal/app/app.go`, `api/internal/services/services.go`

- Extract an `EmailService` (see design §5.3) that wraps `EmailProvider` and owns message-building for reset/verify/test (moves the inline `fmt.Sprintf` body-building out of `invitations.go:379-404` into shared templating — invitation migration is optional in this epic but the service is the future home)
- Wire `EmailService` into `app.go` alongside the existing mailer construction at `app.go:558-581`
- Inject `EmailService` into the password-reset, email-verify, and test-send handlers
- **Acceptance:** all three consumers send via `EmailService.Send`; existing invitation sending still works (refactor invitation to use the service is a follow-up, not required for acceptance)

### US-49.8 — E2E + integration tests

Per Rule 0 (TDD) and the PR Review Guide E2E wiring requirement.

- `password_reset_test.go` — request (known + unknown email both 202), confirm (happy, expired, consumed, wrong token), rotation preserves secret decryptability
- `email_verify_test.go` — register triggers send (SES mocked), verify happy/expired/consumed, Noop mode auto-verifies, invitation-gate behaviour
- `email_tier1_test.go` — helm-managed key is read-only (GET shows readOnly, PUT returns 409), non-managed is editable
- `email_test_send_test.go` — admin-only, rate-limited, Noop returns provider=noop, SES mocked returns mapped errors
- E2E integration test exercising router → service → token store → (mocked) provider for reset + verify
- Canary scenario: `s-password-reset` and `s-email-verify` (Go + Python + TS per existing canary convention)
- **Acceptance:** all tests pass with `-race`; unhappy paths covered; e2e wiring demonstrated

### US-49.9 — Invitation flow hardening + scanner-safety invariant

**Files:** `api/internal/handlers/invitations_test.go` (add invariant test), no production code change required

The existing invitation flow (`invitations.go`) is **already safe** against the scanner-pre-click failure mode (Gap 3) by design. This story locks in that invariant so a future refactor cannot break it, and documents the convention for all future email-link flows.

#### 9.1 Why invitations are already scanner-safe

The email-link scanner threat (Gap 3) requires a GET request to consume the token. The invitation flow does not have this shape:

| Handler | Method | Auth | Consumes the invitation? |
|---------|--------|------|--------------------------|
| `GetByToken` (`invitations.go:225`) | GET | None (public) | **No** — reads and returns `InvitationDetail` only |
| `Accept` (`invitations.go:261`) | POST | JWT required | Yes — calls `AcceptInvitationTx` |
| `Decline` (`invitations.go:356`) | POST | None | Yes — calls `DeclineInvitation` |

A scanner following the link in the invitation email hits `GetByToken` (GET), which only reads. It does **not** consume the invitation. The user's later Accept (POST, with their JWT) still works. A scanner cannot Accept because it has no JWT. Therefore the scanner cannot break the user's flow.

This is the correct design and predates this epic — it is not a new fix.

#### 9.2 The invariant to lock in

Add a regression test in `invitations_test.go` that asserts `GetByToken` does not mutate the invitation row (no `accepted_at`, no `declined_at`, no `token_hash` change). This test must fail if a future "simplification" makes the GET consume — which would introduce the scanner vulnerability.

```
TestInvitation_GetByToken_DoesNotConsume:
  - create invitation
  - GET /api/v1/invitations/:token → 200 with details
  - GET /api/v1/invitations/:token again → 200 (same details, still valid)
  - POST /api/v1/invitations/:token/accept (with JWT) → 200 (accept succeeds)
  - GET /api/v1/invitations/:token → 404 or 409 (now consumed)
```

#### 9.3 The convention for all email-link flows

Established by this epic — all three email-link flows follow the same shape:

| Flow | Email link → | Consume action |
|------|-------------|----------------|
| Invitation accept (existing) | Frontend page showing invitation details | POST `/invitations/:token/accept` (JWT) |
| Password reset (US-49.5) | Frontend page with password form | POST `/password-reset/confirm` |
| Email verify (US-49.6) | Frontend page with verify button | POST `/verify-email` |

**Rule: no GET endpoint ever consumes an email-link token.** Document this in `pkg/email/` or a CONTRIBUTING note so future email-link features follow it without re-deriving the reasoning.

#### 9.4 Acceptance criteria

- Regression test `TestInvitation_GetByToken_DoesNotConsume` passes and would fail if `GetByToken` were changed to consume
- Convention documented (code comment on `GetByToken` + design note)
- No production behaviour change — invitations work exactly as before

---

## Security controls & threat model

Consolidates the security posture across all stories in this epic. Every control maps to a specific threat.

### Token-link flows (password reset, email verify, invitations)

| Threat | Control | OWASP ref | Story |
|--------|---------|-----------|-------|
| Token brute-force | 256-bit `crypto/rand` tokens | ASVS V3.1 | US-49.5, US-49.6 |
| DB leak exposes tokens | sha256 hash stored only (never raw) | — | US-49.5, US-49.6 |
| Token replay after use | Single-use (`consumed_at` on confirm) | ASVS V3.5 | US-49.5, US-49.6 |
| Token lives too long | 15m reset / 24h verify expiry | — | US-49.5, US-49.6 |
| Email enumeration via reset/verify request | 202-always response for known + unknown | OWASP Auth Cheat Sheet | US-49.5, US-49.6 |
| Scanner pre-clicks single-use link | Interstitial page: GET renders form, POST consumes | Industry (Proofpoint/Mimecast) | US-49.5 §5.5, US-49.6 §6.5 |
| Invitation scanner consumes token | Already safe: GET non-consuming + POST JWT-required (US-49.9 locks it in) | — | US-49.9 |

### Credential-change flows (password reset)

| Threat | Control | OWASP ref | Story |
|--------|---------|-----------|-------|
| Stolen JWT survives password reset | `RevokeAllUserSessions` revokes all outstanding jtis via existing `token:<jti>` revocation | OWASP Auth Cheat Sheet ("destroy all active sessions") | US-49.5 §5.3 |
| Victim unaware of takeover | Post-reset notification email to the same address | OWASP Forgot Password Cheat Sheet | US-49.5 §5.4 |
| Secrets undecryptable after reset | `RotateKeyWithPassword` rewraps DEK (existing path, reused) | — | US-49.5 §5.2 |
| Reset sent to unverified mailbox | Request requires `email_verified=true` | — | US-49.5 §5.1 |

### Credential storage (SES)

| Threat | Control | Story |
|--------|---------|-------|
| AWS key leak | IRSA only — no static keys in app config, PG, or helm | US-49.1 |
| SMTP password in plaintext (future) | Deferred to SMTP epic; encrypt via master key; must reconcile with "creds never in PG" rule | design §6.2 |

### Email content

| Threat | Control | Story |
|--------|---------|-------|
| XSS via email body (org name injection) | `html.EscapeString` on dynamic values (existing in `invitations.go:392`); centralise in `EmailService` | US-49.7 |
| Phishing from spoofed sender | DKIM/SPF/DMARC prerequisites documented for operator | Prerequisites |

### Residual risks (documented, accepted)

| Risk | Mitigation status |
|------|-------------------|
| Advanced sandboxed scanners (Office ATP detonation) fill forms and click buttons | Interstitial defeats link-followers (majority); two-step code is full mitigation (out of scope MVP). Revisit if production reports scanner-consumed tokens. |
| No HIBP/breached-password check at register/reset | Out of scope; note as future hardening. |
| `email_verified` is point-in-time ownership, not perpetual | Documented (US-49.6 §6.7); no re-verification on schedule. Composes with JWT-email-match at `invitations.go:317`. |
| Email deliverability (spam filters) defeats verification | DKIM/SPF/DMARC prerequisites + resend endpoint mitigate; not fully solvable in-app. |

---

## Assumptions (full list in design §3)

The load-bearing ones for implementation:

1. **API pod egress to SES is unconstrained** — validated during scoping (workspace NetworkPolicy does not apply to the API deployment). Re-verify at implementation.
2. **IRSA is the credential path** — no static AWS keys. Validated.
3. **`aws-sdk-go-v2/service/ses` is in go.mod** — `ses_provider.go` imports it, so it must be; run `go mod tidy` to confirm.
4. **Recovery-key flow stays as offline fallback** — reset-via-email is additive. Validated.
5. **`email_verified` gates invitation acceptance only** (MVP) — **needs user confirmation** during implementation.

---

## Open questions for implementation

1. **Email-verification gate scope** — confirm MVP gates invitation acceptance only (design §6.3). Expanding to gate all non-read actions is a product decision affecting existing users.
2. **Should invitation emails migrate to `EmailService`?** — Optional in this epic. The service is the future home, but migrating `invitations.go:379-404` mid-epic risks touching a working path. Recommend: build the service, leave invitation as-is, migrate in a follow-up.

---

## Follow-ups (tracked, not in this epic)

| Item | Where |
|------|-------|
| SMTP provider behind `EmailProvider` | future epic; password storage decision in design §6.2 |
| SES bounce/complaint SNS webhook → populate dead `bounce_type`/`bounced_at` columns | makes `invitations.go:194` guard live |
| Wire up `notifyOnSessionComplete` / `notifyOnWorkspaceReady` user settings | currently dead (`schema.go:100-101`) |
| Migrate invitation email-building into `EmailService` | optional consolidation |
| Retrofit Tier-1 helm-precedence onto relay/billing/SSO config | mechanism is general once US-49.2 ships |

---

## Definition of Done

- All stories above implemented with TDD (tests first, happy + unhappy + e2e)
- `make build && make test && make lint` green
- SES path verified end-to-end against a real (or LocalStack) SES endpoint
- Noop path still works (dev/air-gapped)
- Helm-precedence model demonstrated: helm-managed = read-only UX, helm-omitted = editable UX
- **All security controls in the "Security controls & threat model" section implemented and tested**
- **Password reset revokes all sessions + sends notification email + uses interstitial page**
- **Email verify uses interstitial page (no GET-consumes)**
- **Invitation scanner-safety invariant locked in with regression test (US-49.9)**
- Worklog created per Worklog Requirements
- Adversarial self-review (Rule 11) completed and documented in the PR
