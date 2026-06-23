# Worklog: Fix "failed to list users" — UUID/text COALESCE type error

**Date:** 2026-06-23
**Session:** Diagnosed and fixed the platform-admin Users tab 500 ("failed to list users") reported from a live deployment.
**Status:** Complete

---

## Objective

The platform-admin Users tab at `/admin/users` showed `failed to list users` + `No users found`. Diagnose the root cause and fix it.

---

## Root Cause

`Service.ListAllUsers` (`api/internal/services/database/database.go:338`) ran:

```sql
SELECT u.id, u.email, u.role, u.status, u.created_at,
       COALESCE(m.org_id, ''), COALESCE(o.name, '')
FROM users u
LEFT JOIN org_memberships m ON m.user_id = u.id
LEFT JOIN organizations o ON o.id = m.org_id AND o.deleted_at IS NULL
ORDER BY u.created_at DESC
LIMIT $1 OFFSET $2
```

`org_memberships.org_id` is a **`UUID`** column (migration `000029_organizations.up.sql:28`). `COALESCE(uuid_col, '')` forces Postgres to resolve the two arguments to a common type: it picks `uuid` (the concrete type wins over the `unknown` text literal) and coerces `''` → `''::uuid`. When the LEFT JOIN yields a **NULL** `org_id` (any user with no org membership — extremely common: the admin account, users created before joining an org, users whose org was deleted), COALESCE evaluates the fallback `''::uuid` → **`ERROR: invalid input syntax for type uuid: ""`** → the whole query fails → HTTP 500 → "failed to list users".

The error appeared as soon as the deployment had a single user without an org membership. Deployments where every user happens to belong to an org never hit the bug, which is why it wasn't caught earlier.

### Why the existing tests missed it

The sqlmock unit tests (`TestListAllUsers_*`) pattern-match the SQL string against a regex; they never execute it against Postgres, so a type-level incompatibility between the query and the schema is invisible to them. There was no integration test for `ListAllUsers`.

---

## Fix

Cast `org_id` to text inside the COALESCE so both arguments are text-typed:

```sql
COALESCE(m.org_id::text, '')
```

- When `org_id` is a non-NULL UUID → returns its canonical text form (e.g. `550e8400-e29b-41d4-a716-446655440000`), which the Go `UserListEntry.OrgID string` field already expects.
- When `org_id` is NULL (no membership) → returns `''`, which the existing Go logic at `database.go:361` (`if e.OrgID != "" { e.OrgCount = 1 }`) treats as "no org".

The second COALESCE (`o.name`) is already text-vs-text, so it needs no change.

---

## Work Completed

### Fix
- `api/internal/services/database/database.go:340` — `COALESCE(m.org_id, '')` → `COALESCE(m.org_id::text, '')`.

### Regression test (integration, against real Postgres)
- `api/internal/services/database/platform_admin_integration_test.go` (new) — `TestIntegration_ListAllUsers_UserWithoutOrgMembership`. Seeds two users: one in an org, one with no org membership (the bug trigger). Asserts `ListAllUsers` returns both without error, the in-org user carries its `orgID`/`orgCount=1`, and the no-org user has empty `OrgID`/`orgCount=0`. Runs under the `-tags=integration` build tag via the `testharness` harness (skips when Postgres is unreachable, matching the project's integration-test contract; runs in the CI "Secrets Integration" job).

---

## Key Decisions

- **`::text` cast over returning NULL / handling in Go.** The Go Scan already targets a `string` (`UserListEntry.OrgID`), and the downstream logic keys off `OrgID != ""`. Casting to text keeps the SQL contract (non-NULL empty string for no-membership) identical to the pre-bug intent, requiring zero Go changes.
- **Integration test, not another sqlmock test.** Adding a sqlmock assertion that the query string contains `::text` would be brittle (it tests the spelling, not the behaviour) and would not have caught the original bug. The integration test reproduces the exact failure shape (NULL org_id from a LEFT JOIN) and will catch any future type-coercion regression in this query.
- **Scoped fix.** Audited all other `COALESCE` usages in `database.go` (lines 471, 472, 719, 720, 824, 1142, 1162, 1200): all are text/bool/timestamp columns, none involve a UUID column. The bug was isolated to this one query.

---

## Assumptions (validated)

1. `org_memberships.org_id` is `UUID` → verified, migration `000029_organizations.up.sql:28`: `org_id UUID NOT NULL REFERENCES organizations(id)`. ✓
2. `UserListEntry.OrgID` is a Go `string` → verified, `pkg/types/auth.go:127`. ✓
3. Postgres `COALESCE(uuid, 'literal')` coerces the literal to uuid, failing on empty string when the value is NULL → standard Postgres type-resolution semantics; the deployed error ("failed to list users", a 500 from `platform_admin.go:219`) confirms the query errored at runtime. ✓ (to be definitively confirmed by the CI integration test run.)
4. The `::text` cast returns the canonical UUID text form that the Go Scan accepts → `database/sql` scans a text column into a `string` field directly. ✓
5. No other admin-list query has the same UUID/COALESCE shape → grep-verified (only line 340 matched). ✓

---

## Blockers

None. Local environment has no Postgres, so the integration test is confirmed to compile/vet but executes in CI.

---

## Tests Run

- `go build -tags=integration ./api/internal/services/database/` — PASS (integration test compiles).
- `go vet -tags=integration ./api/internal/services/database/` — PASS.
- `go test -run TestListAllUsers ./api/internal/services/database/` — PASS (sqlmock unit tests still match the query).
- `go test ./api/internal/services/database/` — PASS (full package).
- `go test -run TestListUsers -count=1 ./api/internal/handlers/` — PASS (HTTP handler layer).
- `gofmt -l` — clean after formatting the new test file.
- CI "Secrets Integration" job will run `TestIntegration_ListAllUsers_UserWithoutOrgMembership` against real Postgres.

---

## Next Steps

None.

---

## Files Modified

- `api/internal/services/database/database.go` (one-line fix: `COALESCE(m.org_id::text, '')`)
- `api/internal/services/database/platform_admin_integration_test.go` (new regression test)
