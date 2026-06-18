# Worklog: Sequence placeholder

**Date:** 2026-06-18
**Status:** Placeholder

---

## Why this exists

This is a placeholder worklog filling sequence gap `0340` left after the
auto-fix bot renumbered files away from this slot during the post-merge
collision cascade on 2026-06-18.

See `worklogs/0349_2026-06-18_repolint-merge-queue-autofix.md` for the
incident context. Briefly: PRs #228, #229, #230 all merged within 12
minutes and each independently renamed an orphaned worklog into the
next-free slot. The autofix bot from PR #234 then ran multiple times,
moving files out of `0340` and `0341` to resolve duplicates. The renames
left those slots empty.

`pkg/repolint/sequence.go` `SequenceCheck` requires contiguous numbering
above the grandfather threshold (97); empty slots fail the check.
The bot's `FixWorklogs` renames duplicates but does not fill gaps, so
this manual placeholder closes the sequence.

The followup work to teach `FixWorklogs` to fill gaps using local-only
files (without renaming mainline files, which would break
`MainlineCheck`) is tracked separately.

## Files

| File | Change |
|---|---|
| `worklogs/0340_2026-06-18_sequence-placeholder.md` | This file |
