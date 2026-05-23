# Worklog 0027: Auth Endpoints with Security Hardening

**Date:** 2026-05-23
**Scope:** API auth — register, login, API key CRUD
**Status:** Complete

## What Changed

Implemented 5 auth endpoints that were identified as missing (worklog 0026 bug #22: "No signup endpoint"). Credentials were previously injected directly into Postgres via SQL.

### New Endpoints

| Endpoint | Method | Auth | Purpose |
|----------|--------|------|---------|
| `/api/v1/auth/register` | POST | Public | Create user + return JWT |
| `/api/v1/auth/login` | POST | Public | Email+password → JWT |
| `/api/v1/auth/api-keys` | POST | JWT/API Key | Generate `lsp_`-prefixed API key |
| `/api/v1/auth/api-keys` | GET | JWT/API Key | List keys (secrets stripped) |
| `/api/v1/auth/api-keys/:id` | DELETE | JWT/API Key | Revoke a key |

### Implementation Layers

1. **Types** (`pkg/types/types.go`): Added `RegisterRequest`, `LoginRequest`, `AuthResponse`, `CreateAPIKeyRequest`, `APIKey`. Added `PasswordHash` field to `User` with `json:"-"` tag.

2. **Database** (`api/internal/services/database/database.go`): Added `GetUserByEmail`, `CreateAPIKey`, `ListAPIKeys`, `GetAPIKey`, `DeleteAPIKey`. Fixed `GetUser` and `CreateUser` to include `password_hash` column.

3. **Auth Service** (`api/internal/services/auth/auth.go`): Added `Register`, `Login`, `CreateAPIKey`, `ListAPIKeys`, `DeleteAPIKey`. Added `jti` claim to JWT tokens.

4. **Router** (`api/internal/server/router.go`): Added `registerAuthRoutes` with public register/login and authenticated API key management group.

5. **Interfaces** (`api/internal/interfaces/interfaces.go`): Extended `DatabaseService` and `AuthService` with new methods. Updated all mocks.

## Security Hardening

Adversarial security audit identified 16 findings across 5 severity levels. The following were fixed:

### Fixed (this PR)

| ID | Severity | Finding | Fix |
|----|----------|---------|-----|
| H2 | HIGH | Email enumeration via register returning "email already registered" | Changed to generic "registration failed" |
| H3 | HIGH | Auth error responses leaking internal details (`err.Error()`) | Generic errors in handlers; details only in server logs |
| H1 | HIGH | No request body size limits | Added `http.MaxBytesReader` (1 MiB) on all auth endpoints |
| C1+H4 | CRITICAL+HIGH | Rate limiter skipped unauthenticated requests entirely | Falls back to `c.ClientIP()` when no API key in context |
| M1 | MEDIUM | JWT tokens lack `jti` claim; revoking one token revokes ALL for user | Added `jti: uuid` to token claims |
| M3 | MEDIUM | No input sanitization beyond binding tags | Email lowercased + trimmed; username trimmed |
| L1 | LOW | bcrypt cost 10 is minimum | Increased to cost 12 |

### Documented as Pre-existing (out of scope)

| ID | Severity | Finding | Note |
|----|----------|---------|------|
| C2 | CRITICAL | CORS `AllowCredentials: true` with wildcard origins | Pre-existing in `security.go` |
| C3 | CRITICAL | Rate limiter defaults to disabled | Pre-existing config default |
| M2 | MEDIUM | Raw JWT cached in Redis | Pre-existing caching pattern |
| M4 | MEDIUM | Tokens accepted via query params | Pre-existing in token extractor |
| M5 | MEDIUM | Double response write in middleware auth | Pre-existing in unused middleware/auth.go |
| M6 | MEDIUM | WebSocket origin wildcard | Pre-existing in router config |

## Tests

### Service-level (TDD)
- `api/internal/services/auth/auth_test.go`: 15 new tests — Register (success, duplicate email, db errors), Login (success, not found, wrong password, inactive), API key CRUD (create, list, delete, not found, errors)
- `api/internal/services/database/database_test.go`: Fixed 3 existing tests broken by `password_hash` column addition

### Router-level (e2e)
- `api/internal/server/router_auth_test.go`: 19 tests — happy/unhappy paths for all 5 endpoints, route registration, response format validation
- `api/internal/server/router_auth_security_test.go`: 15 security-focused e2e tests — body size limits, email enumeration, error sanitization, password leak prevention, API key secret stripping, auth requirement, method rejection

### Shell e2e
- `local/test-auth.sh`: 17 test cases against a running server — covers register, login, API key CRUD with happy and unhappy paths

### Results
- 26 Go test packages passing with `-race`
- 49 new/updated tests total
- `go vet` clean

## Files Modified

| File | Change |
|------|--------|
| `pkg/types/types.go` | Added auth types, `PasswordHash` field, `APIKey.UserID` |
| `api/internal/interfaces/interfaces.go` | Extended `DatabaseService`, `AuthService` interfaces |
| `api/internal/mocks/database.go` | Added mock methods for new DB interface |
| `api/internal/mocks/middleware_mocks.go` | Added mock methods for new auth interface |
| `api/internal/middleware/tests/auth_test.go` | Updated local mock for new interface |
| `api/internal/middleware/rate_limit.go` | IP-based fallback for unauthenticated requests |
| `api/internal/services/database/database.go` | Added `GetUserByEmail`, API key CRUD |
| `api/internal/services/database/database_test.go` | Fixed tests for `password_hash` column |
| `api/internal/services/auth/auth.go` | Added Register, Login, API key methods; bcrypt cost 12; jti claim; error hardening |
| `api/internal/services/auth/auth_test.go` | 15 new service-level TDD tests |
| `api/internal/server/router.go` | Added `registerAuthRoutes` with body size limits, sanitized errors |

## Files Created

| File | Purpose |
|------|---------|
| `api/internal/server/router_auth_test.go` | Router-level e2e tests (19 tests) |
| `api/internal/server/router_auth_security_test.go` | Security-focused e2e tests (15 tests) |
| `local/test-auth.sh` | Shell e2e against running server (17 cases) |

## Assumptions Validated

1. `golang.org/x/crypto` (bcrypt) already in `go.mod` — confirmed
2. `google/uuid` used by service layer — confirmed, same package used
3. DB schema has `users.password_hash VARCHAR(255)` and `api_keys` table — confirmed in `000001_initial_schema.up.sql`
4. Auth middleware skip paths already include `/api/v1/auth/register` and `/api/v1/auth/login` — confirmed in `middleware/auth.go:43`
5. No external auth provider needed for V1 (NR8: single-org) — confirmed per EVOLUTION-V2.md

## Next Steps

- Wire rate limiting middleware to router (currently infrastructure exists but not enabled)
- Fix CORS wildcard + credentials misconfiguration
- Add password complexity rules beyond min length 8
- Add account lockout after N failed login attempts
