# Worklog: Migration-safety CI parity — data-cleanup job + docker gate

**Date:** 2026-06-18
**Session:** Restore pre-existing stashed work for migration-safety local/CI parity, address PR review
**Status:** Complete

---

## Objective

The workspace had stashed changes (from a prior session) that close the gap between local `make migration-safety` (4 checks) and CI (3 of 4). Validate relevance, open PR, address review.

---

## Work Completed

### Stash assessment

Examined the stashed work (.githooks/pre-commit, .github/workflows/migration-safety.yml, Makefile, hack/migration-safety-docker.sh). Validated:
- `make migration-safety` on main runs 4 checks (Makefile:450): roundtrip, idempotent, fk-cascade, data-cleanup.
- CI workflow only had 3 jobs — `data-cleanup` (migration 000014 regression for non-UUID workspace_id cleanup) was never wired.
- `hack/migration-data-cleanup.sh` already existed on main but CI never ran it.

**Verdict:** real gap, right level of abstraction. Opened PR #226.

### PR #226 review fixes

AI reviewer (verdict APPROVE) flagged two issues:
1. **False "zero setup" claim.** The docker wrapper claims "only requirement is Docker" but `make migration-safety` invokes `psql`/`pg_dump`/`pg_isready` directly. A developer with Docker but no PostgreSQL client tools wastes 20-40s pulling postgres:16 before getting `"psql not installed"`.
2. **Missing worklog.**

Fixed both:
- Added `psql`/`pg_isready` precheck that fails fast (exit 2) before starting the container.
- Corrected all "zero setup" / "only Docker" claims in the script header, Makefile comment, and pre-commit comment.
- Created this worklog.

---

## Key Decisions

- **Precheck not docker-exec refactor.** The reviewer suggested either (a) `docker exec psql` inside the container or (b) correct the docs + add a precheck. Chose (b) because all existing `hack/migration-*.sh` scripts use host-side psql against the published port — using `docker exec` would require rewriting all of them. The precheck is honest and fast-fails before wasting time.

---

## Tests Run

No local test execution (no Docker in this environment). CI on PR #226: Lint pass, review pass (APPROVE), data-cleanup job passes in CI.

---

## Next Steps

Merge PR #226 once the review-fix push passes CI.

---

## Files Modified

- `.githooks/pre-commit` (migration-safety-docker gate + corrected comment)
- `.github/workflows/migration-safety.yml` (data-cleanup CI job)
- `Makefile` (migration-safety-docker target + corrected comment)
- `hack/migration-safety-docker.sh` (new script + psql precheck + corrected header)
- `worklogs/0338_2026-06-18_migration-safety-ci-parity.md` (this worklog)
