# Worklog 0028: Rate Limiting, CORS Hardening, Account Lockout

**Date:** 2026-05-23
**Scope:** Security infrastructure — rate limiting, CORS, account lockout
**Status:** Complete

## What Changed

Wired three security subsystems that had infrastructure but were either disabled,
misconfigured, or missing: rate limiting, CORS, and account lockout. All three
are now configurable via environment variables with safe defaults.

### 1. Rate Limiting — Wired to Router

The `RateLimitMiddleware` existed in `middleware/rate_limit.go` but was never
added to the router middleware stack. The router now reads rate limit config
from `app.go` and passes it to `NewRouter`.

**Config (env vars):**

| Env Var | Default | Description |
|---------|---------|-------------|
| `LLMSAFESPACE_RATELIMITING_ENABLED` | `false` | Enable/disable rate limiting |
| `LLMSAFESPACE_RATELIMITING_DEFAULTLIMIT` | `100` | Requests per window |
| `LLMSAFESPACE_RATELIMITING_DEFAULTWINDOW` | `1m` | Window duration |
| `LLMSAFESPACE_RATELIMITING_BURSTSIZE` | `20` | Burst allowance |
| `LLMSAFESPACE_RATELIMITING_STRATEGY` | `token_bucket` | Strategy: token_bucket, fixed_window, sliding_window |

**Fix:** Rate limiter previously skipped unauthenticated requests entirely
(fell through when no `apiKey` in context). Now falls back to `c.ClientIP()`
so public auth endpoints (`/register`, `/login`) are rate-limited by IP.

### 2. CORS — Hardened

**Before:** `AllowedOrigins: ["*"]` + `AllowCredentials: true` — any website
could make authenticated cross-origin requests. This is the #1 most dangerous
CORS misconfiguration per OWASP/Mozilla.

**After:** `AllowedOrigins: []` (empty = no CORS by default), `AllowCredentials: false`.
Operators must explicitly set allowed origins.

**Config (env vars):**

| Env Var | Default | Description |
|---------|---------|-------------|
| `LLMSAFESPACE_SECURITY_ALLOWEDORIGINS` | (empty) | Comma-separated list of allowed origins |
| `LLMSAFESPACE_SECURITY_ALLOWCREDENTIALS` | `false` | Enable credentials in CORS |

WebSocket origins also inherit from `ALLOWEDORIGINS` when set (instead of wildcard `*`).

### 3. Account Lockout

After N consecutive failed login attempts for a given email, that email is
temporarily locked. Tracked in Redis with auto-expiry. Successful login clears
the counter.

**Config (env vars):**

| Env Var | Default | Description |
|---------|---------|-------------|
| `LLMSAFESPACE_AUTH_LOCKOUTENABLED` | `false` | Enable account lockout |
| `LLMSAFESPACE_AUTH_LOCKOUTATTEMPTS` | `0` | Failed attempts before lockout |
| `LLMSAFESPACE_AUTH_LOCKOUTDURATION` | `0` | Lockout duration (e.g. `15m`) |

Recommended production values: `LOCKOUTATTEMPTS=5`, `LOCKOUTDURATION=15m`.

**Implementation:** `lockout:<email>` keys in Redis. `recordFailedAttempt()`
increments and sets TTL. `clearFailedAttempts()` deletes on success. Lockout
check happens before any DB lookup — prevents bcrypt work for locked accounts.

## Tests

### Service-level (TDD)
- `TestLogin_LockoutAfterFailedAttempts` — 3 wrong passwords record 3 attempts
- `TestLogin_LockoutBlocksAfterMaxAttempts` — locked account gets generic error
- `TestLogin_SuccessResetsLockout` — correct password clears counter
- `TestLogin_LockoutDisabled` — default config has lockout disabled

### Results
- 26 Go test packages passing with `-race`
- All existing tests continue to pass

## Files Modified

| File | Change |
|------|--------|
| `api/internal/config/config.go` | Added `Security`, `Auth.Lockout*`, `RateLimiting.*` fields + env var overrides |
| `api/internal/middleware/security.go` | Default CORS: empty origins, credentials=false |
| `api/internal/middleware/rate_limit.go` | IP-based fallback for unauthenticated requests |
| `api/internal/middleware/auth.go` | Fixed double response write (M5); disabled query param + cookie token extraction (M4) |
| `api/internal/utilities/token_extractor.go` | Disabled query param + cookie extraction by default (M4) |
| `api/internal/utilities/token_extractor_test.go` | Updated tests: query/cookie return empty by default, work when explicitly enabled |
| `api/internal/services/auth/auth.go` | Lockout check + `recordFailedAttempt` + `clearFailedAttempts`; `hashToken()` for Redis cache keys (M2) |
| `api/internal/services/auth/auth_test.go` | 4 new lockout tests; updated cache key matchers for hashed keys |
| `api/internal/services/ratelimit/ratelimit.go` | New: Redis-backed rate limiter service implementing `RateLimiterService` |
| `api/internal/services/ratelimit/ratelimit_test.go` | New: 7 TDD tests for rate limiter service |
| `api/internal/services/services.go` | Added `RateLimiter` field + `GetRateLimiter()` method; creates `ratelimit.NewWithCache()` |
| `api/internal/interfaces/interfaces.go` | Added `GetRateLimiter()` to `Services` interface |
| `api/internal/server/router.go` | Wired `RateLimitMiddleware` into global middleware stack |
| `api/internal/app/app.go` | Wire config → router (rate limit, CORS, WebSocket origins) |
| `api/internal/server/router_*_test.go` | Added `GetRateLimiter()` to all mock `Services` implementations |

## Security Findings Resolved

| ID | Severity | Finding | Fix |
|----|----------|---------|-----|
| C2 | CRITICAL | CORS wildcard+credentials | Default `AllowedOrigins: []`, `AllowCredentials: false`, configurable via env var |
| C3 | CRITICAL | Rate limiter disabled + not wired | Created `ratelimit.Service`, wired into router + Services, configurable via env vars |
| M2 | MEDIUM | Raw JWT cached in Redis | `hashToken()` hashes token with MD5 before using as Redis key |
| M4 | MEDIUM | Tokens via query params | Default `QueryParamName: ""` and `CookieName: ""` — only header extraction |
| M5 | MEDIUM | Double response write | Removed `HandleAPIError` before `AbortWithStatusJSON` in middleware/auth.go |

## Remaining (documented)

- Password complexity rules beyond min length 8
