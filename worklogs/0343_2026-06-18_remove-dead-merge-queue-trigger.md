# Worklog: Remove Dead Merge Queue Trigger

**Date:** 2026-06-18
**Session:** Clean up the `merge_queue` trigger added in PR #234 after learning the repo's branch protection uses Repository Rulesets (no Merge Queue support)
**Status:** Complete

---

## Objective

PR #234 added a `merge_queue` trigger to `ci.yml` as the "primary defense" against worklog collisions (Option B), with the post-merge autofix job (Option D) as the safety net. The user then attempted to enable Merge Queue and discovered the repo uses **Repository Rulesets**, which do not support Merge Queue — only classic branch protection does.

The `merge_queue` trigger is therefore dead code: it never fires (no queue exists, none can exist under the current setup). Worse, the autofix job's comments describe Merge Queue as the "primary defense," which is misleading now that the autofix is the *only* defense. This PR removes the dead trigger and corrects the comments.

---

## Work Completed

### Removed the dead trigger

```yaml
# removed:
  merge_queue:
    branches: [main]
```

### Corrected the autofix job comments

The previous comment block called Merge Queue the "primary defense" and the autofix a "safety net for direct pushes, emergency merges with checks bypassed, or any race the queue doesn't catch." That framing assumed Option B was active. Rewrote to:

> *This is the only merge-time defense. The repo uses Repository Rulesets for branch protection, which do not support GitHub Merge Queue; the residual race (two PRs passing within the same minute) is accepted as the cost of that setup. See worklog 0343 for the decision.*

The user explicitly accepted this trade-off earlier ("not the end of the world").

---

## Key Decisions

1. **Removed the trigger rather than leaving it dormant.** Rule 5 (zero tech debt) applies to CI config too. A dead trigger is misleading: future readers see `merge_queue:` and assume the queue is in use. The trigger is trivial to re-add if the repo ever switches to classic branch protection or GitHub adds Merge Queue to rulesets.

2. **Kept the autofix job as-is.** The job logic is correct regardless of whether Merge Queue exists. Only its documentation comments needed updating.

3. **Did not re-evaluate Options 2 or 3 from the prior conversation** (adding a classic branch protection rule alongside the ruleset, or switching to classic entirely). The user implicitly chose Option 1 ("do nothing further") by accepting the residual race. This PR only cleans up the inconsistency that decision created.

---

## Assumptions Stated and Validated

| Assumption | Validation |
|------------|------------|
| Repository Rulesets do not support Merge Queue | User reported the available options in their UI — no "Require a merge queue" entry; confirmed by GitHub docs (Merge Queue requires classic branch protection) |
| The `merge_queue` trigger never fired since PR #234 merged | Verified via `gh run list --limit 50` — zero runs with `event: merge_queue` in the repo's history |
| The autofix job is the only active defense | Verified — `grep -n "merge_queue" .github/workflows/*.yml` after this PR returns zero matches |

---

## Blockers

None.

---

## Tests Run

- **YAML validation:** `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))"` — passes. 2 triggers (push, pull_request), 20 jobs (unchanged).
- No Go test changes — this PR only touches CI config and comments.

---

## Next Steps

1. User review of this PR. If approved, merge.
2. If the repo ever switches to classic branch protection (or GitHub adds Merge Queue to rulesets), re-add the `merge_queue` trigger from PR #234's diff — it's a 2-line change.

---

## Files Modified

| File | Action |
|------|--------|
| `.github/workflows/ci.yml` | Removed `merge_queue` trigger (2 lines); rewrote `repolint-autofix` job comments (8 lines) to reflect it's the sole defense |
| `worklogs/0343_2026-06-18_remove-dead-merge-queue-trigger.md` | Created (this file) |
