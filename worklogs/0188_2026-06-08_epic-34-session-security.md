# Worklog 0187 — Epic 34: Session Security (Remember Me + DEK Encryption Enforcement)

**Date:** 2026-06-08
**Epic:** design/stories/epic-34-session-security/README.md
**Stories:** US-34.1 (Remember Me), US-34.2 (Master Key Enforcement)

---

## Summary

Implemented two independently motivated but related security improvements:

1. **US-34.1 — Remember Me:** Users can opt into a 30-day session at login
   (`{"rememberMe": true}`). Cookie `Max-Age` is now always derived from the JWT
   TTL rather than hardcoded. Fixed the dead `cfg.Auth.CookieName` config field —
   cookie name is now configurable and consistent across all 4 hardcoded sites.
   `OptionalAuthMiddleware` also fixed (missed in original audit).

2. **US-34.2 — Master Key Enforcement:** `LLMSAFESPACE_MASTER_SECRET` is now
   required at startup. Fixed the `deriveServerKey` minimum-length gate (was
   accepting 16-byte keys, below AES-256 minimum). Added `validateMasterSecret`
   as the very first operation in `app.New` so startup fails fast with a clear
   error before any infrastructure is touched — and so the enforcement is
   unit-testable without a live cluster.

---

## Files changed

### New files
- `api/internal/logger/observed.go` — exports `NewObserved()` for cross-package log-assertion tests
- `api/internal/app/app_master_key_test.go` — `validateMasterSecret` unit tests + `app.New` wiring tests
- `api/internal/app/secrets_adapters_test.go` — `deriveServerKey` unit tests

### Modified files
- `pkg/types/types.go` — `LoginRequest.RememberMe bool`, `AuthResponse.TokenTTL time.Duration \`json:"-"\``
- `api/internal/config/config.go` — `Auth.RememberMeDuration`, `LLMSAFESPACE_AUTH_REMEMBEREDURATION` env override
- `api/config/config.yaml` — `rememberMeDuration: 720h`, `cookieName: lsp_session`
- `api/internal/services/auth/auth.go` — `GenerateTokenWithDuration`, `Login` TTL selection, `Register` `TokenTTL`, `extractToken` → method, both middleware call sites, `New` warn
- `api/internal/server/router.go` — `RouterConfig.CookieName`, `cookieName()` helper, `registerAuthRoutes` signature, `setSessionCookie` signature, all 4 hardcoded `"lsp_session"` sites
- `api/internal/app/secrets_adapters.go` — `deriveServerKey` length gate fixed (32 bytes decoded), both format paths documented
- `api/internal/app/app.go` — `validateMasterSecret` first in `New()`, defensive nil-check in secrets block, `RouterConfig.CookieName` wired
- `api/internal/services/auth/auth_test.go` — new remember-me tests, `New` warn tests
- `api/internal/server/router_frontend_auth_test.go` — updated cookie tests with explicit `TokenTTL`, new remember-me and cookie-name tests

---

## Key decisions

**`TokenTTL time.Duration \`json:"-"\`` on `AuthResponse`:** Avoids interface change
(`AuthService.Login` returns `*AuthResponse` — adding a second return value would
require updating 2 mocks and any future implementors). The `json:"-"` tag is the
standard Go idiom for in-process-only transport fields. Field is clearly named and
documented.

**`validateMasterSecret` first in `app.New`:** Placement before `kubernetes.New`
makes the enforcement unit-testable — a test calling `app.New` with no master
secret gets the validation error before any infra is attempted. This is the only
position that tests the actual wiring without requiring a live cluster.

**Both format paths in `deriveServerKey`:** Helm `randAlphaNum 64` produces
alphanumeric (non-hex) secrets; `hex.DecodeString` always fails on these.
Removing the raw-bytes fallback (initially proposed) would have broken every
Helm deployment. The fix: accept both formats explicitly with a 32-byte minimum
on the decoded/raw key, not the string length.

**`GenerateTokenWithDuration` not on the `AuthService` interface:** Called only
internally from `Login` and `Register`. Callers outside the `auth` package use
`GenerateToken` (the existing interface method). No mock changes needed.

---

## Tests added

- `TestLogin_RememberMe_True_Generates30dJWT`
- `TestLogin_RememberMe_False_Generates24hJWT`
- `TestLogin_RememberMe_Absent_DefaultsFalse`
- `TestLogin_RememberMe_DEKTTLIs30d`
- `TestLogin_NoRememberMe_DEKTTLIsStandard`
- `TestLogin_RememberMeDurationZero_FallsBackToTokenDuration`
- `TestLogin_TokenTTLPopulated`
- `TestLogin_TokenTTLPopulated_RememberMe`
- `TestRegister_TokenTTLPopulated`
- `TestGenerateTokenWithDuration_CorrectExpiry`
- `TestGenerateToken_DelegatesWithTokenDuration`
- `TestNew_RememberMeShorterThanToken_LogsWarning`
- `TestNew_RememberMeZero_NoWarning`
- `TestNew_RememberMeLongerThanToken_NoWarning`
- `TestLogin_SetsCookie` (updated — explicit `TokenTTL=24h`)
- `TestRegister_SetsCookie` (updated — explicit `TokenTTL=24h`)
- `TestLogin_RememberMe_CookieMaxAge30Days`
- `TestLogin_ZeroTokenTTL_FallbackMaxAge`
- `TestLogin_TokenTTLNotInResponseBody`
- `TestCookieName_FromRouterConfig`
- `TestCookieName_DefaultsToLspSession`
- `TestLogout_ClearsCorrectCookie`
- `TestConfig_RememberMeDuration_DefaultFromYAML`
- `TestConfig_RememberMeDuration_EnvOverride`
- `TestConfig_RememberMeDuration_InvalidEnvIgnored`
- `TestConfig_RememberMeDuration_ZeroEnvIgnored`
- `TestConfig_CookieName_DefaultFromYAML`
- `TestDeriveServerKey_AbsentEnv_ReturnsNil`
- `TestDeriveServerKey_EmptyEnv_ReturnsNil`
- `TestDeriveServerKey_ShortRawBytes_ReturnsNil`
- `TestDeriveServerKey_Exactly32RawBytes_Returns32ByteKey`
- `TestDeriveServerKey_AlphanumericHelmFormat_Returns32ByteKey`
- `TestDeriveServerKey_ValidHex64Chars_Returns32ByteKey`
- `TestDeriveServerKey_ShortHex_ReturnsNil`
- `TestDeriveServerKey_InvalidHexLongEnough_FallsBackToRawBytes`
- `TestDeriveServerKey_LegacyEnvVar_Accepted`
- `TestDeriveServerKey_PrimaryEnvTakesPrecedence`
- `TestDeriveServerKey_NoSideEffects`
- `TestValidateMasterSecret_AbsentEnv_ReturnsError`
- `TestValidateMasterSecret_TooShort_LogsWarnAndReturnsError`
- `TestValidateMasterSecret_TooShort_DoesNotLogSecret`
- `TestValidateMasterSecret_AlphanumericHelmFormat_Succeeds`
- `TestValidateMasterSecret_HexFormat_Succeeds`
- `TestValidateMasterSecret_LegacyEnvVar_Accepted`
- `TestApp_New_FailsWithoutMasterSecret`
- `TestApp_New_FailsWithTooShortMasterSecret`
- `TestApp_New_WithValidMasterSecret_FailsAtInfra`
