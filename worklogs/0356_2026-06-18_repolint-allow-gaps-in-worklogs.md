# Worklog: Allow gaps in worklog sequence check

**Date:** 2026-06-18
**Status:** Complete (PR pending)

---

## Trigger

Hours of pain caused by repolint's `SequenceCheck` requiring contiguous
worklog numbering. Today's incident chain on main (2026-06-18):

1. PR #225 and PR #226 both landed worklogs at `0338` (the original
   collision) — `MainlineCheck` reads stale `origin/main`, so both PRs
   passed checks at submission time.
2. PRs #228, #229, #230 all rebased and each renumbered the orphan
   `us-46.2-keep-annotatemodels-guard.md` to a different next-free
   slot. Each PR landed within 12 minutes.
3. PR #234 added an autofix bot (post-merge `[skip ci]` push) to heal
   collisions. Bot ran multiple times, creating gaps where it moved
   files away from low slots.
4. `SequenceCheck` rejects gaps; main went red and stayed red.
5. PR #235 (manual collision cleanup) landed but raced with another
   incoming PR and produced a fresh `0342` collision.
6. PR #236 fixed an unrelated `merge_queue` ↔ `merge_group` ci.yml
   typo from PR #234, also blocking main.

Total: ~2 hours of metadata churn, 0 product value, 4-5 PRs.

## Decision

User: "is it worth it to enforce worklog numbering?"

The unique-reference guarantee (no two files claim `0231`) is
load-bearing — it prevents the original confusion where ambiguous
worklog references silently picked the wrong file. Keep that.

The contiguity guarantee (no gaps in `[1, max]`) was a nice-to-have
that turned into a tarpit because the autofix bot cannot heal gaps
without breaking `MainlineCheck` (renaming a file already on
`origin/main` produces phantom collisions).

Migrations still need contiguity — schema rebuild applies them in
order, a missing `000031` between `000030` and `000032` is
operationally fatal. Worklogs are append-only documentation; gaps are
cosmetic.

So: relax contiguity for worklogs only, keep it strict for migrations.

## Implementation

`pkg/repolint/sequence.go`:
- Added `SequenceConfig.AllowGaps bool`. When true, `OK()` ignores
  `MissingVersions`. Duplicates and unpaired-files still fail.
- Added `SequenceReport.GapsAllowed bool` (mirrors config), so the
  report self-describes whether gaps were tolerated.
- Added `SequenceReport.HasWarnings()` — true iff
  `GapsAllowed && len(MissingVersions) > 0`. Lets callers print
  WARN-level output when gaps exist but checks pass.
- Migration check stays strict (no `AllowGaps`).

`cmd/repolint/main.go`:
- `runWorklogs` now passes `AllowGaps: true`.
- After `OK()` passes, if `HasWarnings()` is true, prints
  `WARN  worklogs sequence has gaps at [...]` to stdout (not stderr,
  not exit non-zero) so the gap is visible without blocking.
- Failure-message text updated from "must be unique and contiguous"
  to "must be unique" — accurate to new semantics.

`pkg/repolint/sequence_test.go`:
- `TestSequenceCheck_AllowGaps_GapStillReportedButOKTrue` — gap is
  populated, `OK()` is true, `HasWarnings()` is true.
- `TestSequenceCheck_AllowGaps_DuplicatesStillFail` — uniqueness
  invariant preserved under `AllowGaps`.
- `TestSequenceCheck_AllowGapsFalse_DefaultBehaviorPreserved` —
  regression guard against accidentally flipping the default.

## What this does NOT change

- `MainlineCheck` still hard-fails when a local worklog collides with
  one on `origin/main`. The "don't rename mainline files" rule is
  what makes references stable across history.
- `FixWorklogs` still resolves duplicates the same way. Doesn't
  attempt gap-closing.
- The post-rewrite git hook still fires `repolint -fix-worklogs-only`
  to renumber duplicates introduced by rebase.
- Migration sequence check unchanged.

## Operational note

The pending followup to make `-fix-worklogs` close gaps using
local-only files is **cancelled** by this change — gaps are no longer
errors, so closing them is no longer a goal.

## Files

| File | Change |
|---|---|
| `pkg/repolint/sequence.go` | `AllowGaps` field, `GapsAllowed` field, `HasWarnings()` method, `OK()` updated |
| `cmd/repolint/main.go` | `runWorklogs` opts in to `AllowGaps`; emits WARN line on gaps |
| `pkg/repolint/sequence_test.go` | 3 new tests covering gap-allowed semantics |
| `worklogs/0356_*.md` | This file |
