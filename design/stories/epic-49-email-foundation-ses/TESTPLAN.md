# Epic 49 — Comprehensive Email Test Plan

**Date:** 2026-06-20
**Goal:** 80%+ coverage across all email-related packages, with unit, integration, and e2e tests.

---

## Current Coverage Gaps

| Package | Current | Target | Root cause |
|---------|---------|--------|------------|
| `pkg/email/` (SESProvider) | 0% | 80%+ | No tests at all; SES mock never written |
| `pkg/email/` (NoopProvider) | 0% (indirect) | 100% | Used via tests but not directly tested |
| `api/internal/services/email/` | 68% | 85%+ | Nil-provider branches for SendPasswordChanged/SendEmailVerification untested |
| `api/internal/services/database/email_tokens.go` | 0% | 85%+ | PgEmailTokenStore never tested against sqlmock or real DB |
| `api/internal/handlers/email.go` | ~85% | 90%+ | Good but a few branches missing |
| `api/internal/handlers/password_reset.go` | ~80% | 85%+ | EmailVerifierAdapter error path, Register production path |
| `api/internal/handlers/email_verify.go` | ~80% | 85%+ | Good shape, a few edge cases |
| `api/internal/services/auth/auth.go` (email paths) | partial | 80%+ | Register-with-verifier path untested |

---

## Test Matrix

### 1. Unit Tests — `pkg/email/`

#### NoopProvider (`noop_provider.go`)
| # | Test | Path | Status |
|---|------|------|--------|
| 1.1 | Send returns nil, does not panic | Happy | MISSING |
| 1.2 | Send with empty message fields | Edge | MISSING |

#### SESProvider (`ses_provider.go`)
| # | Test | Path | Status |
|---|------|------|--------|
| 1.3 | NewSESProvider constructs without panic | Happy | MISSING |
| 1.4 | Send calls SES SendEmail with correct fields | Happy | MISSING |
| 1.5 | Send wraps SES error with context | Unhappy | MISSING |
| 1.6 | Send propagates context cancellation | Unhappy | MISSING |

### 2. Unit Tests — `api/internal/services/email/`

#### EmailService (`service.go`)
| # | Test | Path | Status |
|---|------|------|--------|
| 2.1 | NewService nil provider + ProviderName | Happy | EXISTS |
| 2.2 | ProviderName normalisation table | Happy | EXISTS |
| 2.3 | SendTest builds + sends | Happy | EXISTS |
| 2.4 | SendTest nil provider → ErrNotConfigured | Unhappy | EXISTS |
| 2.5 | SendTest provider error propagates | Unhappy | EXISTS |
| 2.6 | SendPasswordReset builds correct link | Happy | EXISTS |
| 2.7 | SendPasswordReset states expiry | Happy | EXISTS |
| 2.8 | SendPasswordReset nil provider → ErrNotConfigured | Unhappy | MISSING |
| 2.9 | SendPasswordReset URL-encodes token | Edge | EXISTS |
| 2.10 | SendPasswordReset HTML-escapes | Defense | EXISTS |
| 2.11 | SendPasswordChanged builds notification | Happy | EXISTS |
| 2.12 | SendPasswordChanged nil provider → ErrNotConfigured | Unhappy | MISSING |
| 2.13 | SendEmailVerification builds correct link | Happy | EXISTS |
| 2.14 | SendEmailVerification states 24h expiry | Happy | MISSING |
| 2.15 | SendEmailVerification nil provider → ErrNotConfigured | Unhappy | MISSING |
| 2.16 | buildLink trims trailing slashes | Edge | EXISTS |

### 3. Unit Tests — `api/internal/services/database/email_tokens.go`

#### PgEmailTokenStore (sqlmock)
| # | Test | Path | Status |
|---|------|------|--------|
| 3.1 | CreateEmailToken INSERT succeeds | Happy | MISSING |
| 3.2 | CreateEmailToken DB error wrapped | Unhappy | MISSING |
| 3.3 | GetEmailTokenByHash returns token | Happy | MISSING |
| 3.4 | GetEmailTokenByHash not found → nil,nil | Unhappy | MISSING |
| 3.5 | GetEmailTokenByHash DB error wrapped | Unhappy | MISSING |
| 3.6 | GetEmailTokenByHash consumed_at NULL → nil pointer | Edge | MISSING |
| 3.7 | ConsumeEmailToken success | Happy | MISSING |
| 3.8 | ConsumeEmailToken already consumed → ErrTokenAlreadyConsumed | Unhappy (TOCTOU) | MISSING |
| 3.9 | ConsumeEmailToken DB error wrapped | Unhappy | MISSING |

### 4. Unit Tests — `api/internal/handlers/email.go` (test-send)

#### EmailHandler
| # | Test | Path | Status |
|---|------|------|--------|
| 4.1 | SES success → 200 {sent,provider} | Happy | EXISTS |
| 4.2 | Noop → 200 {sent:false,provider:"noop"} | Happy | EXISTS |
| 4.3 | Missing to → 400 | Unhappy | EXISTS |
| 4.4 | Invalid email → 400 | Unhappy | EXISTS |
| 4.5 | SES error → 502 mapped | Unhappy | EXISTS |
| 4.6 | Unknown error → generic | Unhappy | EXISTS |
| 4.7 | Nil provider → noop response | Defense | EXISTS |
| 4.8 | Rate limit 6th call → 429 | Unhappy | EXISTS |
| 4.9 | Rate limiter nil → still works | Defense | EXISTS |
| 4.10 | Rate limiter Redis error → fail open | Defense | EXISTS |
| 4.11 | Display-name parsed to bare address | Edge | EXISTS |
| 4.12 | mapSESError table (8 cases) | Happy+Unhappy | EXISTS |

### 5. Unit Tests — `api/internal/handlers/password_reset.go`

#### PasswordResetHandler
| # | Test | Path | Status |
|---|------|------|--------|
| 5.1 | Request known verified → sends email | Happy | EXISTS |
| 5.2 | Request unknown → 202, no email | Unhappy | EXISTS |
| 5.3 | Request unverified → 202, no email | Unhappy | EXISTS |
| 5.4 | Request missing email → 400 | Unhappy | EXISTS |
| 5.5 | Request DB lookup error → 202 (no enum) | Unhappy | MISSING |
| 6.6 | Confirm valid → resets everything | Happy | EXISTS |
| 5.7 | Confirm expired → 410 | Unhappy | EXISTS |
| 5.8 | Confirm consumed → 410 | Unhappy | EXISTS |
| 5.9 | Confirm unknown token → 404 | Unhappy | EXISTS |
| 5.10 | Confirm short password → 400 | Unhappy | EXISTS |
| 5.11 | Confirm wrong kind → 404 | Unhappy | EXISTS |
| 5.12 | Confirm consume DB error → 500 | Unhappy | EXISTS |
| 5.13 | Confirm key init failure → 500 | Unhappy | MISSING |
| 5.14 | Confirm bcrypt update failure → 500 | Unhappy | MISSING |
| 5.15 | Routes registered (router-level) | E2E wiring | EXISTS |
| 5.16 | Request body size limit enforced | Edge | MISSING |

#### EmailVerifierAdapter
| # | Test | Path | Status |
|---|------|------|--------|
| 5.17 | Creates token + sends email | Happy | EXISTS |
| 5.18 | Token store error propagates | Unhappy | MISSING |
| 5.19 | Email send error propagates | Unhappy | MISSING |

### 6. Unit Tests — `api/internal/handlers/email_verify.go`

#### EmailVerifyHandler
| # | Test | Path | Status |
|---|------|------|--------|
| 6.1 | Verify valid → sets verified | Happy | EXISTS |
| 6.2 | Verify expired → 410 | Unhappy | EXISTS |
| 6.3 | Verify consumed → 410 | Unhappy | EXISTS |
| 6.4 | Verify wrong kind → 404 | Unhappy | EXISTS |
| 6.5 | Verify unknown → 404 | Unhappy | EXISTS |
| 6.6 | Verify missing token → 400 | Unhappy | EXISTS |
| 6.7 | Verify consume DB error → 500 | Unhappy | EXISTS |
| 6.8 | Resend known unverified → sends | Happy | EXISTS |
| 6.9 | Resend unknown → 202, no send | Unhappy | EXISTS |
| 6.10 | Resend already verified → no send | Unhappy | EXISTS |
| 6.11 | Resend invalid email → 400 | Unhappy | EXISTS |
| 6.12 | Resend nil resender → 202 (graceful) | Defense | MISSING |
| 6.13 | Verify UpdateUser failure → 500 | Unhappy | MISSING |

### 7. Auth Tests — Login gate + Register

| # | Test | Path | Status |
|---|------|------|--------|
| 7.1 | Login unverified → ErrEmailNotVerified | Unhappy | EXISTS |
| 7.2 | Login verified → success | Happy | EXISTS |
| 7.3 | Register dev-mode → auto-verify → Login works | E2E round-trip | EXISTS |
| 7.4 | Register with verifier → sends email, unverified | Happy (production) | MISSING |
| 7.5 | Register verifier send failure → non-fatal | Unhappy | MISSING |

### 8. Integration Tests (sqlmock)

| # | Test | Scope | Status |
|---|------|-------|--------|
| 8.1 | PgEmailTokenStore CRUD round-trip | DB layer | MISSING |
| 8.2 | PgEmailTokenStore consume TOCTOU | DB layer | MISSING |
| 8.3 | PasswordReset Confirm with sqlmock store | Handler→DB | MISSING |

### 9. E2E Tests (router-level)

| # | Test | Scope | Status |
|---|------|-------|--------|
| 9.1 | Password reset routes registered | Router wiring | EXISTS |
| 9.2 | Email verify routes registered | Router wiring | MISSING |
| 9.3 | Full register→verify→login round-trip | End-to-end | MISSING |

---

## Implementation Priority

1. **SESProvider tests** (1.3-1.6) — 0% coverage, load-bearing provider
2. **PgEmailTokenStore tests** (3.1-3.9) — 0% coverage, never run
3. **NoopProvider direct test** (1.1-1.2) — trivial but closes 0%
4. **EmailService nil-provider branches** (2.8, 2.12, 2.15, 2.14)
5. **Handler edge cases** (5.5, 5.13, 5.14, 5.16, 5.18, 5.19, 6.12, 6.13)
6. **Auth production-path tests** (7.4, 7.5)
7. **Integration tests** (8.1-8.3)
9. **E2E tests** (9.2, 9.3)
