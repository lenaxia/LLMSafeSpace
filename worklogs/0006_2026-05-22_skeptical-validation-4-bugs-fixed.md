# Worklog 0006 — 2026-05-22 — Skeptical validation: 4 bugs fixed

## Session goals

Skeptical code review of all work done in sessions 3-5. Read every changed file directly, not summaries. Verify correctness, completeness, type safety, idiomatic Go, robustness, and absence of over-engineering.

## Review methodology

- Read actual code, not memory of what I wrote
- Traced SQL parameter binding by hand
- Checked nil sentinel logic by reasoning about Redis behaviour
- Checked compile-time interface guards for correctness
- Checked test coverage gaps by listing all `func Test` in each file

## Bugs found and fixed

### 1. `UpdateUser` off-by-one SQL parameter numbering (CRITICAL)

**File:** `api/internal/services/database/database.go`

**Problem:** Counter `i` started at `1` and used `i+1` for parameter placeholders, producing `$2, $3...` instead of `$1, $2...`. With 2 fields the query looked like:

```
UPDATE users SET updated_at = NOW(), username = $2, email = $3 WHERE id = $4
```

With args `[username, email, userID]` — 3 args for a query referencing `$2, $3, $4`. PostgreSQL would error: `$1` is never referenced and `$4` is out of bounds. This was a silent runtime failure — no tests existed to catch it.

**Fix:** `i` starts at `0`, incremented before use (matching the correct `UpdateSandbox` pattern).

### 2. `TestUpdateUser` missing entirely

**File:** `api/internal/services/database/database_test.go`

**Problem:** No test existed for `UpdateUser`. The off-by-one bug would have gone undetected indefinitely.

**Fix (TDD):** Wrote 5 subtests first (all failed against the buggy implementation):
- `username_and_email` — verifies `$1=username, $2=email, $3=userID`
- `single_field_role` — verifies `$1=role, $2=userID`
- `all_fields` — verifies all 4 fields in correct order
- `no_fields_is_noop` — verifies no SQL issued for empty `UserUpdates{}`
- `db_error` — verifies error wrapping

Then fixed the implementation. All 5 now pass.

### 3. `MockDatabaseService` weak compile-time guard

**File:** `api/internal/mocks/database.go`

**Problem:**
```go
var _ interface {
    GetUser(...) ...
    UpdateUser(...) ...
    // 14 methods copied manually
} = (*MockDatabaseService)(nil)
```
This is an anonymous interface — a copy. If `interfaces.DatabaseService` gains or changes a method, this guard never fires. The mock silently diverges from the real interface.

**Fix:**
```go
var _ interfaces.DatabaseService = (*MockDatabaseService)(nil)
```
Now any interface change that `MockDatabaseService` doesn't implement causes a compile error immediately.

### 4. `CachedSession` nil sentinel — silent data loss

**File:** `api/internal/services/cache/cache.go`

**Problem:** `GetSession` detected a missing key via `session.SessionID == ""`:
```go
var session types.CachedSession
s.GetObject(ctx, key, &session)  // no-op when key missing
if session.SessionID == "" {
    return nil, nil  // WRONG: also returns nil for stored {SessionID:""}
}
```
Any session stored with `SessionID: ""` (e.g. a bug elsewhere, or a future field change) would silently disappear.

**Fix:** Check `redis.Nil` directly — that's the canonical Redis "key not found" signal:
```go
data, err := s.client.Get(ctx, key).Bytes()
if err == redis.Nil {
    return nil, nil  // definitively not found
}
```
No sentinel needed. Unmarshal errors are now also properly surfaced.

## Other observations (no action needed)

| Item | Assessment |
|------|------------|
| `UserUpdates`/`SandboxUpdates` pointer fields | Correct Go idiom for partial update structs. Standard pattern in gRPC/protobuf generated code too. |
| `SandboxUpdates.Labels map[string]string` | Not a pointer — nil map in Go is the zero value and is the correct sentinel. No issue. |
| `interfaces.DatabaseService` and `interfaces.CacheService` | Fully typed, no map[string]interface{}. Clean. |
| `MockCacheService` — no compile-time guard | Cache service has `var _ interfaces.CacheService = (*Service)(nil)` on the real impl. Mock doesn't have one. Low risk since it's in the same package and tests use it via the interface. Acceptable. |
| `CacheService.GetObject/SetObject interface{}` | Correct — these are generic JSON marshal/unmarshal helpers. Not a type safety violation. |
| `apierrors.APIError.Details map[string]interface{}` | Error payload, not domain type. Acceptable as-is. |
| SQL injection risk | All user-controlled values go through `$N` placeholders. Column names are hardcoded in the implementation, never from user input. Safe. |

## Final state

- 165 tests, 0 failures, 13/13 packages
- Zero `map[string]interface{}` in domain interfaces, service implementations, or mocks
- Zero warm pool/pod references in production code
- All compile-time interface guards use the real interface type

## Commit

`43dfc51` — Fix 4 bugs found during skeptical validation
