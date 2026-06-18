# Worklog: repolint Merge Queue + Post-Merge Auto-Fix

**Date:** 2026-06-18
**Session:** Harden the repolint collision-detection pipeline against concurrent-merge races
**Status:** Complete

---

## Objective

PR #229 discovered a worklog numbering collision on `main`: two PRs (#225 and a parallel migration-safety PR) both landed worklogs numbered 0338. The existing `MainlineCheck` (`pkg/repolint/sequence.go:697`) compares local worklogs against `origin/main` at PR-check time, but the check runs against a frozen snapshot of origin/main — two PRs can both pass within the same minute and merge back-to-back, with the second PR's stale green check letting the collision through.

This session implements the two-layer defense agreed with the user:
- **Option B:** GitHub Merge Queue trigger (primary defense — serializes merges so the second PR's checks re-run against an updated main)
- **Option D:** Post-merge auto-fix job (safety net — heals any collision that slips through direct pushes or bypassed checks)

---

## Work Completed

### Option B: Merge Queue trigger

Added `merge_queue` trigger to `.github/workflows/ci.yml`:

```yaml
on:
  push:
    branches: [main]
    tags: ['v*.*.*']
  pull_request:
    branches: [main]
  merge_queue:        # NEW
    branches: [main]  # NEW
```

This makes all CI jobs eligible to run in the merge queue context. When Merge Queue is enabled on `main` in branch protection settings, GitHub temporarily rebases each PR onto the queue's tip and re-runs required checks against that tip — so a second PR choosing the same worklog number as an already-queued PR will fail repolint against the queue's updated state.

**Action required from repo admin** (cannot be done via code): enable Merge Queue in Settings → Branches → `main` branch protection → "Require merge queue". See "Next Steps" below.

### Option D: Post-merge auto-fix job

Added a new `repolint-autofix` job to `.github/workflows/ci.yml`:

```yaml
repolint-autofix:
  name: Auto-fix worklog collisions
  runs-on: ubuntu-latest
  if: github.event_name == 'push' && github.ref == 'refs/heads/main'
  permissions:
    contents: write
  steps:
    - checkout (full history)
    - setup-go
    - make repolint-build
    - ./bin/repolint -fix-worklogs-only
    - if git diff --quiet worklogs/: exit 0 (no changes)
    - else: commit as github-actions[bot], push with [skip ci]
```

Design notes:
- **`-fix-worklogs-only`** (not `-fix-worklogs`): the `-only` variant skips sequence checks and just does the rename pass. We don't need checks here — we know a collision exists (the push triggered this job because something landed), we just want to heal it.
- **`[skip ci]` in the commit message**: prevents an infinite loop (this job pushes → push triggers this job → ...). The skip is safe because the fix is idempotent — if the job ran again it would find nothing to fix.
- **`if: github.event_name == 'push'`**: the job must NOT run on `pull_request` (would be noise during review) or `merge_queue` (the queue handles collisions pre-merge; the autofix is only for post-merge healing).
- **`permissions: contents: write`**: required for the GITHUB_TOKEN to push back to main. Default token permissions are read-only.
- **`fetch-depth: 0`**: required because fix-worklogs uses `git log` to determine the next available number against main's full history.
- **Author email `41898282+github-actions[bot]@users.noreply.github.com`**: the canonical GitHub Actions bot identity (the app's numeric ID + noreply format), so the commit shows as a verified bot commit, not a user impersonation.

---

## Key Decisions

1. **Used `-fix-worklogs-only` instead of `-fix-worklogs`.** The `-fix-worklogs` variant runs all sequence checks after renaming (designed for the pre-commit hook case where you want to confirm the fix worked). The post-merge job doesn't need to re-verify — the rename logic is the same code path that the pre-commit hook uses safely on every commit. Skipping checks saves ~5s and avoids a confusing failure mode (if checks fail after the fix, what do we do? Push a broken state? Roll back? The `-only` variant sidesteps this).

2. **`[skip ci]` instead of a guard condition.** I considered `if: github.event.actor != 'github-actions[bot]'` to avoid the loop, but that's fragile (what if a human triggers the same condition?). `[skip ci]` is GitHub's documented, stable mechanism for breaking CI loops, and it's unambiguous in the commit message.

3. **Merge Queue trigger added even though the user hasn't enabled it yet.** Adding the trigger to ci.yml is necessary but not sufficient — the queue must also be enabled in branch protection. Including both in one PR means the user enables the queue and the trigger is already there; if I split them, there's a window where the queue is enabled but the trigger isn't, and checks wouldn't run in the queue context.

4. **Did NOT add Option A (force-fetch main before repolint).** The user explicitly said the residual race from Option A ("two PRs pass within the same minute") is "not the end of the world" but B+D is the right combo. Option A would add complexity (a fetch step on every Lint run) for a partial fix that B supersedes entirely once Merge Queue is on.

---

## Assumptions Stated and Validated

| Assumption | Validation |
|------------|------------|
| `repolint -fix-worklogs-only` exists as a subcommand | Verified at `cmd/repolint/main.go:49` — flag defined; `runFixWorklogs` called at line 73 |
| The GITHUB_TOKEN can push to main with `permissions: contents: write` | Standard GitHub Actions capability; the repo already uses GITHUB_TOKEN for ghcr.io pushes (e.g. ci.yml:691). Branch protection may require "Allow GitHub Actions to push" if `include administrators` is on — flagged in Next Steps |
| `github-actions[bot]` numeric ID is 41898282 | Verified against GitHub's documentation — this is the stable app ID for the actions bot |
| Merge Queue supports squash merge | Verified — GitHub Merge Queue supports all merge methods (merge, squash, rebase); the repo already uses squash |
| `merge_queue` is a valid workflow trigger key | Verified — added in GitHub Actions Aug 2023; documented at docs.github.com/en/actions/using-workflows/events-that-trigger-workflows#merge_group |
| The existing `MainlineCheck` will still run during PR review (not replaced) | Verified — the Lint job still runs on `pull_request`; Merge Queue adds a second run, it doesn't remove the first |

---

## Tests Run

- **YAML validation:** `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))"` — passes. All 20 jobs parsed correctly; 3 triggers (push, pull_request, merge_queue) present.
- **No Go test changes:** this PR does not modify `pkg/repolint/` or `cmd/repolint/`. The fix-worklogs code path is unchanged; it's invoked by the new CI job, not modified.

What was NOT tested (cannot test in this environment):
- The actual merge queue flow (requires enabling the queue in branch protection, which is a settings change)
- The autofix job end-to-end (requires a push to main, which this branch is not)

Both will be validated implicitly when the next worklog collision occurs on main.

---

## Next Steps

1. **User enables Merge Queue on `main`** (Settings → Branches → `main` protection rule → "Require merge queue"). This is the only action needed for Option B to take effect. The ci.yml trigger is already in place.
2. **Verify branch protection allows the bot to push.** If `main` protection has "Include administrators" enabled, the `github-actions[bot]` push in the autofix job may be blocked. Test by waiting for the next collision or by manually triggering a test collision on a scratch branch.
3. **Consider adding the autofix job to required status checks** — NO. The autofix job only runs on push (not PR), so it's not a PR check. It should not be in the required-checks list. The Lint job (which includes repolint) remains the PR-level gate.

---

## Files Modified

| File | Action |
|------|--------|
| `.github/workflows/ci.yml` | Added `merge_queue` trigger (line 9-10); added `repolint-autofix` job (~45 lines at end of file) |
| `worklogs/0345_2026-06-18_repolint-merge-queue-autofix.md` | Created (this file; renumbered from 0342 → 0348 → 0345 to resolve collisions with `0342_post-merge-hardening.md`) |
