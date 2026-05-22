# Worklog 0007 — 2026-05-22 — Skeptical validation round 2: 3 issues fixed

## Methodology

Read every changed file directly. Stated all assumptions upfront and validated each one before drawing conclusions. Did not draw any conclusion that could not be validated from the source code.

## Assumptions stated and validation results

| Assumption | How validated | Result |
|---|---|---|
| `database.Stop()` closes the connection pool | Read `database.go:70` — `return s.DB.Close()` | CONFIRMED: closes pool |
| `metrics.Stop()` is also harmful per-request | Read `metrics.go:103` — `return nil` | metrics Stop is a no-op; harmless but wrong in principle |
| `db.Start()/Stop()` calls are only in `CreateSandbox`, not other methods | `grep -n 'dbService.Start\|dbService.Stop'` | CONFIRMED: only in CreateSandbox |
| `Start()/Stop()` pattern was introduced by me (not inherited) | `git show HEAD~6:...sandbox_service.go` | WRONG: was in the pre-session original too. I copied it faithfully. Still a bug. |
| Tests mask the bug via `.Return(nil)` | Read test file | CONFIRMED: every CreateSandbox test set `db.On("Start").Return(nil)` and `db.On("Stop").Return(nil)` |
| `redis.Nil` is the correct not-found sentinel for `go-redis/v8` | Read `cache.go` import + `go.mod` | CONFIRMED: `github.com/go-redis/redis/v8 v8.11.5`, `redis.Nil` is correct |
| `UpdateSandbox` defer rollback uses closure variable correctly | Read the defer and all assignment sites of `err` | CONFIRMED: correct — `err` is shared via closure, committed path sets `err=nil` so rollback doesn't fire |
| `MockCacheService` had no compile-time guard | Read `cache.go` | CONFIRMED: missing |
| `UpdateUser` had a duplicate comment | `grep -n 'UpdateUser updates'` | CONFIRMED: line 144 and 145 both had identical comment |

## Issues found and fixed

### 1. CRITICAL: `CreateSandbox` closes DB connection pool on every request

`database.Service.Stop()` calls `s.DB.Close()`. `CreateSandbox` deferred `s.dbService.Stop()` after every request. Result: the first sandbox creation succeeds; every subsequent one fails with `sql: database is closed`.

**Root cause:** Lifecycle methods (`Start`/`Stop`) are application-boot concerns, not request-scoped concerns. The sandbox service does not own the database or metrics services — it depends on them. Calling lifecycle methods on dependencies you don't own is wrong.

**Fix:** Removed the `Start`/`Stop` calls and their `defer` from `CreateSandbox`.

**Test added:** `TestCreateSandbox_ServiceIsStateless` — calls `CreateSandbox` twice on the same `Service` instance, asserts both succeed. Would have failed against the buggy implementation.

**Test fix:** Removed `db.On("Start").Return(nil)` and `db.On("Stop").Return(nil)` from all 11 `CreateSandbox` tests that had them. These stubs were masking the bug.

### 2. `MockCacheService` missing compile-time interface guard

**Fix:** Added `var _ interfaces.CacheService = (*MockCacheService)(nil)` with the real `interfaces.CacheService` type, matching the pattern used in `MockDatabaseService`.

### 3. Duplicate GoDoc comment on `UpdateUser`

Line 144 and 145 were identical. Removed the duplicate.

## Final state

- 166 tests, 0 failures, 13/13 packages (up from 165 — added stateless test)
- `CreateSandbox` no longer closes the DB pool on every call
- All mock compile-time guards reference the real interface type

## Commit

`1cfd78e` — Fix Start/Stop lifecycle misuse in CreateSandbox and two minor issues

## Next steps

- US-1.5: Build `redact` binary
- US-1.7: Entrypoint scripts for runtime containers
- US-1.8: Rewrite base Dockerfile
- Epic 2: Workspace CRD
