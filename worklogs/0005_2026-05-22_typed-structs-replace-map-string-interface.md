# Worklog 0005 — 2026-05-22 — Replace map[string]interface{} with typed structs

## Session goals

Replace all remaining `map[string]interface{}` in service interfaces and domain code with properly typed structs, following the type-safety rule from README-LLM.md.

## Context

Identified during validation session (worklog 0004). Three interface methods accepted or returned untyped maps:
- `DatabaseService.UpdateUser(... map[string]interface{})` — allowed fields: `username`, `email`, `active`, `role`
- `DatabaseService.UpdateSandbox(... map[string]interface{})` — allowed fields: `status`, `name`, `labels`
- `CacheService.GetSession(...) (map[string]interface{}, error)` / `SetSession(... map[string]interface{})`

## New types (pkg/types/types.go)

```go
// UserUpdates — nil field means "do not update"
type UserUpdates struct {
    Username *string
    Email    *string
    Active   *bool
    Role     *string
}

// SandboxUpdates — nil field means "do not update"; nil Labels means "do not touch labels"
type SandboxUpdates struct {
    Status *string
    Name   *string
    Labels map[string]string  // non-nil = replace entire label set
}

// CachedSession — typed replacement for map[string]interface{} session bag
type CachedSession struct {
    SessionID string
    UserID    string
    SandboxID string
}
```

## TDD sequence

**Tests written first** (both test files failed to compile — confirmed red):

- `database_test.go TestUpdateSandbox`: rewrote to use `types.SandboxUpdates{Status: &status, ...}` — compile error on `map[string]interface{}` mismatch
- `cache_test.go TestSessionOperations`: rewrote to use `types.CachedSession{...}` — compile error on missing `types` import and old return type
- Added `TestUpdateSandbox/no_fields_set_is_noop`: verifies empty `SandboxUpdates{}` does not open a DB transaction

**Implementation written to make tests pass:**

### database.go UpdateUser
- Replaced map iteration with explicit nil-pointer checks per field
- Deterministic column ordering: no more map iteration → no more non-deterministic SQL → sqlmock ordering issues eliminated
- No functional change in behaviour

### database.go UpdateSandbox
- Short-circuits before `BeginTx` when all fields are nil (was opening a transaction even for empty maps)
- Nil-pointer checks for `Status` and `Name`
- `Labels != nil` triggers full label replace; `Labels == nil` skips label table entirely
- Cleaner than previous implementation which had a misplaced early-return after the transaction was open

### cache.go GetSession / SetSession
- `GetSession` uses existing `GetObject` to unmarshal into `*types.CachedSession`; returns `nil, nil` when key not found (empty `SessionID` sentinel)
- `SetSession` uses existing `SetObject` to marshal `types.CachedSession`

## Mock changes

- `MockDatabaseService.UpdateUser`: `map[string]interface{}` → `types.UserUpdates`
- `MockDatabaseService.UpdateSandbox`: `map[string]interface{}` → `types.SandboxUpdates`
- `MockDatabaseService`: deleted three stale convenience methods (`GetUserByID`, `GetSandboxByID`, `GetSandboxMetadata`) that returned `map[string]interface{}` and had no callers anywhere in the codebase
- `MockCacheService.GetSession`: `(map[string]interface{}, error)` → `(*types.CachedSession, error)`
- `MockCacheService.SetSession`: `map[string]interface{}` → `types.CachedSession`
- `MockCacheService` moved back to `cache.go` exclusively — removed duplicate declaration from `database.go` that was erroneously introduced

## Remaining map[string]interface{} (acceptable)

| Location | Reason |
|----------|--------|
| `apierrors.APIError.Details map[string]interface{}` | Error payload struct, not domain type. The `apierrors` package owns this. Replacing with typed error details is a separate, lower-priority story. |
| `CacheService.GetObject / SetObject` parameters | Generic cache primitives that deliberately accept `interface{}` for JSON marshal/unmarshal. This is the correct design — callers provide typed structs. |

## Build and test status

All 13 test packages, 164 tests pass, 0 failures.

## Commit

`4e7a248` — Replace map[string]interface{} with typed structs across service interfaces

## Next steps

- US-1.5: Build `redact` binary for log sanitization
- US-1.7: Create entrypoint scripts for runtime containers
- US-1.8: Rewrite base Dockerfile
- Epic 2: Workspace CRD (replaces warm pools)
- Wire `RateLimiterService` into `services.go` and `router.go`
- Optional: replace `apierrors.APIError.Details map[string]interface{}` with typed error detail structs
