# Worklog: credential_sets scan failure on model_allowlist (TEXT[])

**Date:** 2026-06-03
**Reporter:** Operator hit `scan credential_set: sql: Scan error on column index 5, name "model_allowlist": unsupported Scan, storing driver.Value type string into type *[]string` after deploying `sha-d5a3d64`.

The previous fix (worklog 0129) addressed the response-shape null-slice
crash, but the request never even got that far in production: the SQL
read failed at the driver layer.

---

## Bug

`api/internal/services/database/credentials.go` scans Postgres column
`model_allowlist` (type `TEXT[]`) directly into a Go `*[]string`:

```go
err := row.Scan(..., &r.ModelAllowlist, ...)
```

The runtime driver is `pgx-stdlib` (loaded via
`_ "github.com/jackc/pgx/v5/stdlib"` in `database.go:12`), exposed as
`database/sql`. **Through the `database/sql` interface, pgx does NOT
natively decode Postgres array types into Go `[]string`.** It returns
the textual representation `{a,b,c}` as a string, which `database/sql`
then refuses to assign to `*[]string` — exactly the error the operator
reported.

(The native `pgx` interface — `pgx.Conn`/`pgxpool` — DOES decode
arrays. The mistake was assuming the same applies through the
`database/sql` adapter. It does not.)

---

## Why CI didn't catch it

- `pkg/credentials/service_test.go` uses a `mockCredStore` and never
  touches Postgres. Worklog 0129 added 4 regression tests there, but
  they exercise the in-memory store.
- `api/internal/services/database/database_test.go` uses
  `go-sqlmock`, which lets the test author specify exactly what the
  driver returns — bypassing real Postgres array decoding entirely. A
  test that says `mock.ExpectQuery(...).WillReturnRows(sqlmock.NewRows(...).AddRow(..., []string{"x"}, ...))`
  succeeds against the mock and fails against real Postgres.
- The `secrets-integration` GitHub workflow runs build-tagged tests
  against a real Postgres container, but only for `pkg/secrets/...`.
  The `api/internal/services/database/...` package had **zero**
  integration coverage. This was the structural gap that allowed the
  bug to ship.

---

## Fix

### 1. `pq.Array` at every []string boundary

Added `github.com/lib/pq` (drop-in compatible with both `lib/pq` and
`pgx-stdlib`; implements `driver.Valuer` for binds and `sql.Scanner`
for scans). Wrapped every `model_allowlist` bind and scan:

| Function | Change |
|---|---|
| `CreateCredentialSet` | bind `pq.Array(modelAllowlist)` |
| `GetCredentialSet` | scan `pq.Array(&r.ModelAllowlist)` |
| `ListCredentialSets` | scan `pq.Array(&r.ModelAllowlist)` |
| `UpdateCredentialSet` | bind `pq.Array(*updates.ModelAllowlist)` |
| `GetDefault` | scan `pq.Array(&r.ModelAllowlist)` |
| `ListByKeyVersionBelow` | scan `pq.Array(&r.ModelAllowlist)` |

No call-site changes outside this file; the wrap is hidden in the SQL
boundary. The Go-side type stays `[]string`.

### 2. Real-Postgres integration test

`api/internal/services/database/credentials_pg_integration_test.go`
(NEW, build tag `integration`, 6 tests):

| Test | Asserts |
|---|---|
| `TestPgCredentialSet_CreateAndGet_RoundTripsModelAllowlist` | **Direct guard for the user error.** Insert `[]string{"gpt-4","gpt-4o","claude-3"}`, Get it back, assert deep equality. Pre-fix the SELECT fails with the literal user-reported error message. |
| `TestPgCredentialSet_CreateAndGet_EmptyAllowlist` | `[]string{}` round-trips as `[]string{}`, NOT `nil`. Postgres distinguishes empty arrays from NULL. |
| `TestPgCredentialSet_List_ScansAllRows` | Three rows with varying allowlist sizes; all scan OK. Pre-fix the loop body fails on the first row and the function returns an error. |
| `TestPgCredentialSet_Update_BindsModelAllowlist` | UPDATE → SELECT round-trip. Catches a missing wrap on the dynamic SET clause. |
| `TestPgCredentialSet_GetDefault_ScansModelAllowlist` | The default-credential lookup runs on every chat request. Same scan invariant. |
| `TestPgCredentialSet_ListByKeyVersionBelow_ScansModelAllowlist` | The rotation path. Same invariant. |

Pre-fix verification: I temporarily reverted the `pq.Array` wraps and
re-ran the first test against a real Postgres 16 container. Output:

```
Error Trace: credentials_pg_integration_test.go:131
Error:       Received unexpected error:
             get credential_set: sql: Scan error on column index 5, name "model_allowlist":
             unsupported Scan, storing driver.Value type string into type *[]string
Test:        TestPgCredentialSet_CreateAndGet_RoundTripsModelAllowlist
Messages:    Get should scan TEXT[] via pq.Array; pre-fix this is the user-reported error
```

The test message is **byte-identical** to the operator's report. TDD
red. After restoring the fix, all 6 pass.

### 3. CI: extend `secrets-integration.yml`

The workflow already provisions Postgres + Redis + applies migrations.
Added a step:

```yaml
- name: Run -tags=integration tests for api/internal/services/database
  run: go test -timeout 120s -race -tags=integration -count=1 ./api/internal/services/database/...
```

And the matching env vars (`POSTGRES_HOST`, `POSTGRES_PORT`, etc.) so
the test can connect.

The workflow is named `Secrets Integration` for historical reasons; it
now covers credential_sets too. Renaming the workflow is out of scope
here.

---

## Verification

| Command | Result |
|---|---|
| `go build ./...` | OK |
| `go test -count=1 -short ./api/... ./pkg/credentials/` | All packages PASS |
| `golangci-lint run ./api/internal/services/database/...` | 0 issues |
| Integration tests against live Postgres 16 (manual run with `POSTGRES_PORT=15432 ...`) | 6/6 PASS |
| Pre-fix verification (revert the wraps, re-run integration test) | First test FAILS with the operator's exact error string |

---

## Future-proofing notes

The same pattern applies to any future Postgres array column (`TEXT[]`,
`UUID[]`, `JSONB[]`, etc.) accessed via `database/sql`. The grep that
catches it is:

```
git grep -nE "TEXT\[\]|UUID\[\]" api/migrations/ | \
  awk -F: '{print $1, $2}'
```

Cross-reference each match against the corresponding Go scan/bind
sites; every one needs `pq.Array(...)`. There is currently only one
such column (`model_allowlist`), but a future migration adding another
without the wrap will silently fail in production the same way.

A linter rule could enforce this — out of scope for this commit, but
worth filing as a follow-up.

---

## Files Modified

- `api/internal/services/database/credentials.go` (pq.Array on 6 sites + import + design comment)
- `api/internal/services/database/credentials_pg_integration_test.go` (NEW, 6 tests)
- `.github/workflows/secrets-integration.yml` (env vars + new step)
- `go.mod`, `go.sum` (add github.com/lib/pq)
- `worklogs/0130_2026-06-03_credential-set-pg-array-scan.md` (this file)
