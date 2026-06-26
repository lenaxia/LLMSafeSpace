# Worklog: Epic 56 implementation — migration 000045 (jwt_sessions table)

**Date:** 2026-06-26
**Session:** Foundation PR (#413) for Epic 56 implementation. Schema-only — application code ships in a follow-up. TDD per Rule 0: wrote the 11-invariant integration test against `postgres:16`, then the migration SQL, then the chart parity + CI hook.

**Status:** Approved on pass-2; awaiting `/merge`.

---

## What this PR ships

- `api/migrations/000045_jwt_sessions.{up,down}.sql` — the `jwt_sessions` table with PK on `jti`, FK CASCADE on `user_id`, indexes on `user_id` and `expires_at`. All DDL uses `IF NOT EXISTS` for idempotency.
- `charts/llmsafespaces/migrations/000045_*.sql` — byte-identical chart parity, synced via `make chart-sync-migrations`.
- `hack/migration-jwt-sessions.sh` — 11 invariants asserted against a throwaway postgres:16. Glob simplified per pass-1 review to `api/migrations/*.up.sql | sort` so future migrations don't require script edits.
- `.github/workflows/migration-safety.yml` — new `jwt-sessions` job mirrors the existing `org-id-backfill` pattern.

## TDD trail

1. **Wrote the failing test first.** `hack/migration-jwt-sessions.sh` was authored before the `.up.sql`. Initial run errored because `000045_*.up.sql` did not exist — exactly the right red bar.
2. **Wrote the migration SQL.** Test passed locally: 11/11 invariants green.
3. **CI flagged idempotency:** the "Migration idempotency (apply ups twice)" job rejected the bare `CREATE TABLE` / `CREATE INDEX` on second apply. Added `IF NOT EXISTS` to all three DDL statements. CI green.
4. **CI flagged `TestLive_Worklogs_NoDuplicates`:** main carried an unnumbered `NNNN_*` worklog from PR #411 (Epic 56 design) because the post-merge bot was skipped (CI path-filter skipped the docs-only merge). Resolved by directly pushing a `[skip ci]` rename commit to main (assigned worklog 0550), then rebasing this PR. Documented for future docs-only merges.
5. **Pass-2 review:** APPROVE. One non-blocking style nit (test glob simplification) — applied for maintainability.

## Adversarial review

- **What if a future migration uses a different ID format?** The `IF NOT EXISTS` guard handles re-creation; the test's `up + down + up` cycle verifies recovery. Migration sequence index covers ordering.
- **What if two replicas apply the migration simultaneously?** `migrate` (the migration-runner image) uses an advisory lock on `schema_migrations`. Race-free.
- **What if jti collides across users?** `PRIMARY KEY (jti)` rejects. Test invariant 6 covers this.
- **What if a user is deleted while their JWT is still valid?** FK CASCADE removes the durable row; subsequent rehydrate on that JWT will fail (no row); the user is gone so the failure is correct.
- **What if the table grows unbounded?** The janitor goroutine (forthcoming impl PR) prunes `expires_at < NOW()`. Index on `expires_at` makes the scan O(log N).

## Out of scope (for the next PR)

The actual application logic. Specifically: DAL methods (`GetJWTSession`, `WriteJWTSession`, `DeleteJWTSession`, `DeleteExpiredJWTSessions`), `parseTokenAcceptingRotatedKeys` returning the matched key, token-validation cache value format change, auth middleware setting `jwt_signing_key` in gin context, `KeyService.GetDEK` signature change + rehydrate body, login durable write, soft-unlock endpoint, revocation eviction, janitor goroutine, and tests for each.

## Refs

- PR #411 (Epic 56 design — merged)
- This PR: #413
- Design doc: `design/stories/epic-56-durable-dek-session/README.md`
- Worklog `0550_2026-06-26_epic-56-durable-dek-design.md` (design worklog from #411)
- Issue #412 (pgx CVE GO-2026-5004; pre-existing, not caused by this PR)
