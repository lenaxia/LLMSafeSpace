# Worklog: Epic 9 Foundation — Schema, Services, API, Cleanup

**Date:** 2026-05-28
**Session:** Epic 9 Phase A foundation implementation (US-9.1 through US-9.7)
**Status:** In Progress

---

## Objective

Implement the foundation layer of Epic 9 (Configuration & Settings): declarative settings schema, instance/user services with typed accessors and caching, seed job, admin/user API endpoints with AdminGuard, and legacy dead code removal.

---

## Work Completed

### US-9.1: Declarative Settings Schema + DB Migration
- `pkg/settings/schema.go`: `SettingDef` struct, `SchemaVersion` constant, `InstanceSettings()` (28 keys), `UserSettings()` (12 keys), index builders
- `pkg/settings/validate.go`: Type-safe validation for bool, int (range), string (regex), enum, strings ([]string and []any from JSON)
- `api/migrations/000006_settings.up.sql`: Creates `instance_settings`, `user_settings`, `credential_sets` tables with triggers, constraints, partial unique index
- `api/migrations/000006_settings.down.sql`: Clean rollback

### US-9.2: Instance Settings Service
- `pkg/settings/instance_service.go`: `InstanceService` with typed accessors (`GetBool`, `GetInt`, `GetString`, `GetStrings`), full-map cache with `singleflight.Group` (60s TTL), cache invalidation on `Set`, audit logging, `Start()`/`Stop()` lifecycle

### US-9.3: User Settings Service
- `pkg/settings/user_service.go`: `UserService` with typed accessors, no cache (read infrequently), per-user isolation, schema default fallback

### US-9.4: Settings Seed Job
- `pkg/settings/seed.go`: `Seed()` function that inserts defaults for missing keys (`ON CONFLICT DO NOTHING` semantics), detects orphaned keys, is idempotent

### US-9.5: Admin API + AdminGuard + Auth Middleware Role Loading
- `api/internal/middleware/admin_guard.go`: Returns 404 for non-admin (no route existence leakage)
- `api/internal/handlers/settings.go`: `SettingsHandler` with admin and user endpoints
- `api/internal/services/auth/auth.go`: `AuthMiddleware()` now loads `userRole` into Gin context via `dbService.GetUser()`
- `pkg/types/types.go`: Added `ContextKeyUserRole`

### US-9.6: User Settings API
- Implemented as part of `SettingsHandler` — GET all, GET schema, PUT :key

### US-9.7: Legacy Cleanup
- Removed `RequireRoles` middleware (dead code, never wired)
- Removed `ExemptRoles` from `RateLimitConfig` and associated check
- Removed `TestRateLimitMiddleware_ExemptRoles` test

---

## Key Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | Used `InstanceStore`/`UserStore` interfaces (not `DatabaseService`) | Keeps settings package decoupled from the API's database service; allows testing with simple mocks |
| 2 | No cache for user settings | Read only on page load; caching adds complexity without benefit |
| 3 | Auth service loads role (not standalone middleware) | The auth service's `AuthMiddleware()` is what's actually wired in the router, not `middleware.AuthMiddleware()` |
| 4 | Removed ExemptRoles entirely (not just the field) | Per D18: admin endpoints are rate-limited to prevent brute-force probing |

---

## Blockers

None.

---

## Tests Run

```
go test -timeout 30s -race -count=1 ./pkg/settings/...     → 72 tests PASS
go test -timeout 30s -race ./api/internal/handlers/...      → 12 settings tests PASS (+ existing)
go test -timeout 30s -race ./api/internal/middleware/tests/ → 4 AdminGuard tests PASS (+ existing)
go test -timeout 120s -short -race ./...                    → ALL PASS (no regressions)
go build ./...                                              → BUILD OK
```

---

## Next Steps

1. **Wire settings routes into the main router** (`api/internal/server/router.go`) — add admin and user settings route groups with proper middleware chain
2. **Implement database store adapters** — `GetAllInstanceSettings`, `SetInstanceSetting`, `InsertInstanceSettingIfMissing`, `GetAllUserSettings`, `SetUserSetting` in `api/internal/services/database/database.go`
3. **Wire services into app bootstrap** (`api/internal/app/app.go`) — instantiate `InstanceService`, `UserService`, run `Seed()` on startup
4. **US-9.11: Max Active Workspaces Enforcement** — read `workspace.maxActiveWorkspacesPerUser` from `InstanceService` in workspace activate flow
5. **US-9.13: Credential Sets Entity** — full stack with AES-256-GCM encryption

---

## Files Modified

```
pkg/settings/schema.go                          (new)
pkg/settings/validate.go                        (new)
pkg/settings/instance_service.go                (new)
pkg/settings/instance_service_test.go           (new)
pkg/settings/user_service.go                    (new)
pkg/settings/user_service_test.go               (new)
pkg/settings/seed.go                            (new)
pkg/settings/seed_test.go                       (new)
pkg/settings/schema_test.go                     (new)
pkg/types/types.go                              (modified — added ContextKeyUserRole)
api/migrations/000006_settings.up.sql           (new)
api/migrations/000006_settings.down.sql         (new)
api/internal/middleware/admin_guard.go          (new)
api/internal/middleware/tests/admin_guard_test.go (new)
api/internal/handlers/settings.go               (new)
api/internal/handlers/settings_test.go          (new)
api/internal/services/auth/auth.go              (modified — role loading)
api/internal/middleware/auth.go                 (modified — removed RequireRoles)
api/internal/middleware/rate_limit.go           (modified — removed ExemptRoles)
api/internal/middleware/tests/rate_limit_test.go (modified — removed ExemptRoles test)
go.mod                                          (modified — added golang.org/x/sync)
```
