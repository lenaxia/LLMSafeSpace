# Worklog: Fix stale go-sqlmock expectations after US-50.6 key_version column

**Date:** 2026-06-22
**Session:** main is red — `api/internal/services/database` mock tests fail because US-50.6 added the `key_version` column to two SQL queries but the corresponding go-sqlmock expectations in `database_test.go` were not updated. Surgical test-only fix.
**Status:** Complete

---

## Objective

Restore main's CI to green. The `Secrets Integration` workflow has failed on every main commit since `88effe22` (US-50.12) / `547ff337` (US-50.6): three mock-based tests in `api/internal/services/database/database_test.go` fail with column-count mismatches. This was discovered while preparing an unrelated docs PR (#367, threat-model sync) — the docs PR's CI went red on the same pre-existing failure.

---

## Root cause

US-50.6 (PR #364, worklog 0514) added a `key_version` column to two queries in `database.go`:

| Query | Change | File:line |
|---|---|---|
| `CreateAPIKey` INSERT | added `key_version` (15th col) + `apiKey.KeyVersion` arg | `database.go:786-815` |
| `ListAPIKeysWithDecrypt` SELECT | added `key_version` (13th col) + `&k.KeyVersion` scan dest | `database.go:937-960` |

PR #364 did **not** update the go-sqlmock expectations that assert the exact arg/scan counts, so:

- `TestCreateAPIKey_WithDEKWrappingColumns` — `arguments do not match: expected 15, but got 16 arguments` (database_test.go:1043)
- `TestCreateAPIKey_WithoutDEKWrappingColumns` — same (database_test.go:1083)
- `TestListAPIKeysWithDecrypt` — `sql: expected 12 destination arguments in Scan, not 13` → `panic: runtime error: index out of range [0] with length 0` (database_test.go:1235)

This is a Rule 0 violation in PR #364 ("tests must pass before marking work complete").

---

## Work Completed

`api/internal/services/database/database_test.go`:

1. `TestCreateAPIKey_WithDEKWrappingColumns` — inserted `sqlmock.AnyArg()` for `key_version` before the trailing `allowed_cidrs` arg (now 16 args, matching production).
2. `TestCreateAPIKey_WithoutDEKWrappingColumns` — same insertion (now 16 args).
3. `TestListAPIKeysWithDecrypt` — added `"key_version"` as the 13th `NewRows` column and `1` as the 13th value in each `AddRow`; added `assert.Equal(t, 1, keys[0].KeyVersion)` so the test actually verifies the new column is scanned (regression coverage for the read path, not just arg-count matching).

Used `sqlmock.AnyArg()` for the new `key_version` insert arg because neither `CreateAPIKey` test asserts on the value (the DEK-wrapping path is the test's subject, not the key version); the concrete `1` in `ListAPIKeysWithDecrypt` is appropriate because that test reads the value back and the assertion exercises it.

---

## Key Decisions

1. **Test-only fix; no production change.** The production queries in `database.go` are correct (US-50.6's intent). Only the test mocks were stale. Changing production to match the tests would be backwards.

2. **Did not extend `GetAPIKeyRecordByHash`.** That query (`database.go:890-893`) does not select `key_version` and its tests pass. The per-request auth path does not need `key_version` for decryption (it uses the DEK). Expanding it would be scope creep beyond "fix the red CI."

3. **Separate PR, not folded into the docs PR (#367).** Mixing a Go test fix into a "docs: threat model sync" commit would violate the repo's single-responsibility / faithful-to-the-ask principle (Rule 4) — the very principle the threat model documents. The docs PR re-runs CI green once this fix lands.

---

## Assumptions

| # | Assumption | Validation |
|---|---|---|
| A1 | These three are the only failing tests in the `database` integration step | CI log shows exactly 3 `--- FAIL` lines (2 CreateAPIKey + 1 ListAPIKeysWithDecrypt) + 1 panic, then `FAIL package`. No other FAIL markers. |
| A2 | The mock tests run without a real Postgres | `database_test.go` has no `//go:build` tag; the failing tests use `setupMockDB` (go-sqlmock), not a live DB. Confirmed by local run below. |
| A3 | `key_version` default value `1` is a valid row value for the List test | Migration `000042_api_keys_key_version.up.sql`: `INTEGER NOT NULL DEFAULT 1`. Confirmed. |

---

## Blockers

None.

---

## Tests Run

```bash
# The three previously-failing tests
go test -run 'TestCreateAPIKey_WithDEKWrappingColumns|TestCreateAPIKey_WithoutDEKWrappingColumns|TestListAPIKeysWithDecrypt' ./api/internal/services/database/ -count=1
# ok  github.com/lenaxia/llmsafespaces/api/internal/services/database  0.027s

# Full package mock suite (catches adjacent-expectation regressions)
go test ./api/internal/services/database/ -count=1
# ok  github.com/lenaxia/llmsafespaces/api/internal/services/database  0.032s

# Format + vet
gofmt -l api/internal/services/database/database_test.go   # (empty)
go vet ./api/internal/services/database/                    # exit 0
```

(Rocky-DB integration tests under `-tags=integration` require a Postgres service container; not run locally. The CI job that failed runs them and will validate on push.)

---

## Next Steps

- Merge this fix → main goes green → docs PR #367 re-runs CI green.
- Process note for the maintainer: PR #364 (US-50.6) merged with these tests broken. Worth checking whether the `Secrets Integration` job is a required check on PRs (it clearly was not enforced on #364), or whether #364's CI genuinely skipped these mock tests. Either way, this fix restores the invariant.

---

## Files Modified

- `api/internal/services/database/database_test.go` — 3 tests updated for the `key_version` column added by US-50.6.
