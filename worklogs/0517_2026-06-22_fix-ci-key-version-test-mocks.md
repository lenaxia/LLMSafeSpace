# Worklog: Fix CI — key_version Test Mocks + repolint Race-Window Skip

**Date:** 2026-06-22
**Session:** Diagnose and fix the failing CI workflows on main (CI + Secrets Integration).
**Status:** Complete.

---

## Objective

Get the CI workflow healthy again. Multiple CI runs on `main` were failing across two workflows (CI and Secrets Integration) after the merge of US-50.6 (PR #364) and US-50.12 (PR #363). Three test failures were identified:

1. `TestCreateAPIKey_WithDEKWrappingColumns` — argument count mismatch
2. `TestCreateAPIKey_WithoutDEKWrappingColumns` — argument count mismatch
3. `TestListAPIKeysWithDecrypt` — Scan destination count mismatch
4. `TestLive_Worklogs_NoDuplicates` — false positive on push-to-main

---

## Root Cause Analysis

### Failures 1–3: Stale test mocks after `key_version` column addition

Migration `000042_api_keys_key_version.up.sql` (US-50.3, PR #359) added a `key_version INTEGER NOT NULL DEFAULT 1` column to `api_keys`. US-50.6 (PR #364) then wired the production code to write it:

- `database.go:CreateAPIKey` — INSERT now lists 16 columns (added `key_version` between `key_ciphertext` and `allowed_cidrs`) and passes `apiKey.KeyVersion` as the 15th argument.
- `database.go:ListAPIKeysWithDecrypt` — SELECT now includes `key_version` (13 columns total) and scans into `&k.KeyVersion`.

The sqlmock-based unit tests in `database_test.go` were not updated to match. They still expected 15 INSERT arguments (missing `key_version`) and 12 SELECT columns. This is a classic mock-drift failure: production schema changed, mocks didn't follow.

**Evidence:** CI log showed `arguments do not match: expected 15, but got 16 arguments` and `sql: expected 12 destination arguments in Scan, not 13`.

### Failure 4: repolint sentinel test races the post-merge bot

`TestLive_Worklogs_NoDuplicates` (`sequence_test.go:533`) verifies that `origin/main` has no `NNNN_` sentinel worklog files — the post-merge bot (`repolint-autofix` job in `ci.yml`) is responsible for renaming them at merge time.

The race: when CI runs on a **push-to-main** event (the merge commit), `origin/main` IS that merge commit — which legitimately carries `NNNN_` sentinels from the just-merged PR. The post-merge bot runs AFTER CI and commits with `[skip ci]`, so CI never re-runs on the cleaned state. The test would false-positive on every merge.

The CI workflow comment at `ci.yml:1341-1343` explicitly documents this as non-gating ("warns only; a persistent NNNN_ on main means this job is broken... but should not block builds"), but the test used `t.Fatalf` which IS gating.

---

## Work Completed

### database_test.go — align mocks with production schema

- `TestCreateAPIKey_WithDEKWrappingColumns`: added `apiKey.KeyVersion` as the 15th mock argument (between `keyCiphertext` and the `allowed_cidrs` nil). Also added `mock.ExpectationsWereMet()` assertion for consistency with the sibling test.
- `TestCreateAPIKey_WithoutDEKWrappingColumns`: added `sqlmock.AnyArg()` as the 15th mock argument (consistent with the existing `AnyArg()` pattern for the other unset byte-slice fields in this test).
- `TestListAPIKeysWithDecrypt`: added `"key_version"` to the `sqlmock.NewRows` column list and a value (`1`) to each `AddRow`.

### sequence_test.go — skip on push-to-main race window

Added an early `t.Skip()` when `GITHUB_EVENT_NAME == "push"` AND `GITHUB_REF == "refs/heads/main"`. In that context, sentinels on `origin/main` are expected (the merge commit just introduced them; the bot hasn't run yet). The test still runs on every PR (where `origin/main` should be clean) and catches genuine bot breakage. Added a detailed comment explaining the race window and why the skip is safe.

---

## Key Decisions

1. **Mock arg type for `key_version` in `_WithoutDEKWrappingColumns`**: used `sqlmock.AnyArg()` rather than a literal `0`. The test's existing style uses `AnyArg()` for all unset DEK-related byte-slice fields (kekSalt, wrappedDEK, keyCiphertext) to be robust against any default-setting logic in the production path. Following the same convention for `key_version` is consistent, even though the production code passes `apiKey.KeyVersion` straight through (Go's zero value for `int` is `0`).

2. **Env-var detection for push-to-main (vs. git-state detection)**: considered checking `HEAD == origin/main` via `git rev-parse`, but that also triggers locally after `git pull`, which would suppress the test for local development. The `GITHUB_EVENT_NAME`/`GITHUB_REF` env vars precisely capture the CI race window without affecting local runs. This is the same env-var pattern GitHub Actions exposes to all steps.

3. **Did NOT add `key_version` to `GetAPIKeyRecordByHash`**: this query (database.go:891) selects 13 columns but does NOT include `key_version`. The `types.APIKey.KeyVersion` field is left at its zero value (`0`) on this read path. This is a pre-existing inconsistency (the per-request API-key lookup doesn't populate the version), but it is out of scope for CI health — the production code and its test are consistent with each other. Flagged for follow-up if rotation logic needs the version at lookup time.

---

## Assumptions Stated and Validated

1. **Migration 000042 is the only schema change affecting these tests.** Validated by `git show 547ff337 --stat` — the US-50.6 commit only touched `database.go` (INSERT/SELECT) and `auth.go` (sets KeyVersion). No other queries reference `key_version` in the api_keys table.

2. **Migration 00043 (org_sso_configs.key_version) does not break SSO tests.** Validated by reading `pg_org_store.go:1660-1710` — the SSO store queries do not reference `key_version`; the column relies on its `DEFAULT 1`. The SSO tests (`pg_org_store_sso_test.go`) don't reference it either.

3. **The env-var skip does not mask real bot breakage.** Validated by reasoning: if the bot fails to rename sentinels, they persist on `origin/main`. The next PR's CI run (where `GITHUB_EVENT_NAME=pull_request`) reads `origin/main` and fails the test. The skip only applies to the push-to-main window where sentinels are expected.

---

## Adversarial Self-Review (Rule 11)

### Phase 1 — Weaknesses

1. **Could the env-var skip hide a permanently broken bot?** If the bot is broken AND no PRs are opened, the sentinel persists indefinitely without CI catching it. But this is the same as the pre-existing situation (the bot job itself would also need to be monitored). The PR-run path catches it as soon as any PR is opened.

2. **Could `sqlmock.AnyArg()` for key_version hide a real bug?** If production code passed a wrong type (e.g., string instead of int), `AnyArg()` would still match. But `_WithDEKWrappingColumns` uses the literal `apiKey.KeyVersion` (typed `int`), which would catch a type mismatch. The two tests together provide both type-safety and robustness.

3. **Is the `ExpectationsWereMet()` addition in `_WithDEKWrappingColumns` necessary?** The original test didn't have it. The CI failure was caught by `assert.NoError(t, err)` (sqlmock returns an error on arg mismatch). Adding `ExpectationsWereMet()` is defense-in-depth — it also catches leftover/unmatched expectations. It's consistent with the sibling test.

### Phase 2 — Validation

All three findings are false alarms or low-risk. The fixes are minimal, targeted, and match production code exactly. No production code was changed — only test mocks and test skip logic.

---

## Tests Run

```bash
# Reproduce failures (before fix)
go test -run 'TestCreateAPIKey_WithDEKWrappingColumns|TestCreateAPIKey_WithoutDEKWrappingColumns|TestListAPIKeysWithDecrypt' ./api/internal/services/database/...
# → FAIL (3 failures)

# Verify fixes (after)
go test -run 'TestCreateAPIKey_WithDEKWrappingColumns|TestCreateAPIKey_WithoutDEKWrappingColumns|TestListAPIKeysWithDecrypt' ./api/internal/services/database/...
# → ok

go test -timeout 120s -race ./api/internal/services/database/... ./pkg/repolint/... ./pkg/secrets/...
# → ok (all three packages)

go test -timeout 60s -run 'TestLive_Worklogs_NoDuplicates' -v ./pkg/repolint/...
# → PASS (skip not triggered locally; origin/main clean after pull)

make repolint
# → all checks passed

gofmt -l api/internal/services/database/database_test.go pkg/repolint/sequence_test.go
# → (no output = clean)

go vet ./api/internal/services/database/... ./pkg/repolint/...
# → (no findings)
```

---

## Files Modified

- `api/internal/services/database/database_test.go` — updated 3 test mocks for `key_version` column
- `pkg/repolint/sequence_test.go` — added push-to-main race-window skip + documentation
- `worklogs/0517_2026-06-22_fix-ci-key-version-test-mocks.md` — this worklog
