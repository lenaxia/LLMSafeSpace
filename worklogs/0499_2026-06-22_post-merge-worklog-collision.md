# Worklog: Post-merge worklog-number collision (PR #343 vs #344)

**Date:** 2026-06-22
**Session:** Diagnose and clear the red CI run left on `main` after PR #343 merged into a worklog-number collision with PR #344, then re-trigger CI so build artifacts are produced.
**Status:** Complete

---

## Objective

PR #343 (org admin force-verify member) and PR #344 (EC2 leak fix) both merged to `main` within ~16 minutes of each other on 2026-06-22. Both had independently chosen worklog number `0474` (each was the next free number above origin/main's `0473` at the time their branches were pushed). The squash-merge of #343 landed `0474_org-admin-force-verify-member.md`; #344's merge minutes later landed `0474_relay-leak-fix-implementation.md` — a duplicate that `repolint` rejects.

The failure was caught by the post-merge `CI` workflow's `Lint` job. Because `lint` is a hard gate (`test`, `test-full`, and every build job `needs: [lint]` or transitively depends on it), **all build jobs were skipped** on the merge commit — no API / Controller / Runtime / Relay Docker images were produced for the #343 + #344 merge batch. Only `Build Frontend` ran (it depends solely on `prepare`, not on `test`).

This worklog records the root cause, the auto-fix that resolved the collision on HEAD, and opens a chore PR to re-trigger CI on the clean HEAD so the build artifacts are produced.

---

## Work Completed

### Diagnosis

- `gh run view 27974881114` showed `Lint` failed with:
  ```
  FAIL  worklogs sequence: 1 duplicate version(s):
    version 474 shared by:
      - 0474_2026-06-22_org-admin-force-verify-member.md
      - 0474_2026-06-22_relay-leak-fix-implementation.md
  ```
- Job-status inspection confirmed `Build API`, `Build Controller`, `Build Runtime Base`, `Build Relay Router`, `Build Relay Proxy` were all `skipped` (downstream of the failed `lint` gate). `Build Frontend` and `Merge Frontend Manifest` succeeded because they only depend on `prepare`.
- A local `repolint` run against origin/main HEAD showed the collision was **already resolved**: a post-merge bot pushed `0804f9be chore(repolint): auto-fix worklog numbering collisions [skip ci]`, renaming PR #344's worklog `0474 → 0498`. Main HEAD is clean.

### Why re-running the failed run does not produce artifacts

Re-running `27974881114` re-checks the tree of commit `ab1204e3` (the #343 merge), which still contains `0474_relay-leak-fix-implementation.md` from the pre-auto-fix state. `Lint` would fail again, and all build jobs would skip again. The fix exists only in the later commit `0804f9be`, which used `[skip ci]` and therefore produced no CI run of its own.

### Re-trigger

The `CI` workflow has no `workflow_dispatch` trigger (only `push` to main and `pull_request`), and direct pushes to `main` are forbidden by repo policy. The only path to a green run on the clean tree is a PR merge. This chore PR (off the clean main HEAD `0804f9be`) carries only this worklog; merging it triggers CI on a clean tree and produces the missing build artifacts.

---

## Key Decisions

1. **Chore PR rather than `gh run rerun`.** Re-running the failed run would re-execute against the `ab1204e3` tree and fail `lint` again. A chore PR off the current clean HEAD is the only mechanism that both respects the no-direct-push policy and produces a green CI run with build artifacts.

2. **Chore PR carries a real worklog, not an empty commit.** An empty commit would be the smallest possible change, but a worklog documenting the collision race is genuinely useful institutional memory — it explains why two adjacent merges can both pick the same number and why the post-merge bot exists. The repo's worklog discipline (README-LLM.md §Worklog Requirements) calls for an entry on "discovering a bug or unexpected behaviour"; a merge-time numbering collision qualifies.

3. **No code changes.** This PR is documentation-only. The actual fix (renaming #344's worklog) was already applied by the post-merge bot.

---

## Assumptions

| # | Assumption | Validation |
|---|---|---|
| A1 | The post-merge bot's rename (`0474 → 0498`) is the authoritative resolution; my worklog `0474_org-admin-force-verify-member.md` correctly keeps number `0474`. | Confirmed by `git show 0804f9be` — the rename touches only `0474_relay-leak-fix-implementation.md → 0498_relay-leak-fix-implementation.md`. Local `repolint` passes. |
| A2 | The `Lint` job's `repolint` step is the only blocker; no other job will fail on this clean tree. | Confirmed by the original PR #343 CI run (commit `0d6e14bf`), where all jobs passed except none — the PR-run CI was fully green. The chore PR carries only a worklog addition, so no code path changes. |
| A3 | Build artifacts are keyed to the merge commit, not to a tag. | The CI workflow builds on every push to `main`; images are tagged with the short SHA. The chore-PR merge commit will produce images tagged with its SHA. |

---

## Blockers

None.

---

## Tests Run

```bash
# Confirm main HEAD is clean after the bot's auto-fix
/tmp/repolint   # all checks passed
```

No code changed; no Go/TS tests are affected.

---

## Next Steps

- After this chore PR merges and CI goes green, the build artifacts (API / Controller / Runtime / Relay images) for the #343 + #344 merge batch will be available in ghcr.io tagged with the chore-PR merge SHA.
- **Process improvement (separate PR):** consider having the pre-merge bot re-check the highest worklog number on origin/main immediately before merge (not just at branch-push time), since two PRs can both observe `N` as free and both pick `N+1`. The post-merge auto-fix already handles the symptom; a pre-merge re-check would prevent the red-CI window entirely.

---

## Files Modified

- `worklogs/0499_2026-06-22_post-merge-worklog-collision.md` — this entry (new).
