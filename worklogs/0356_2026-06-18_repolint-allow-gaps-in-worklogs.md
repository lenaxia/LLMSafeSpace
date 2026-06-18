# Worklog: Allow Gaps in Worklog Sequence Check

**Date:** 2026-06-18
**Session:** Relax worklog contiguity rule after concurrent-merge collision storm consumed ~2h on metadata churn
**Status:** Complete

---

## Objective

`pkg/repolint/sequence.go SequenceCheck` requires contiguous numbering above the grandfather threshold (97). Today's incident chain on main showed this rule's cost has come to exceed its value: concurrent merges + auto-rename hooks repeatedly produced sequence gaps the autofix bot could not heal without breaking `MainlineCheck` (a worklog already on `origin/main` must not be renumbered locally).

User decision (in conversation): keep uniqueness (no two files claim the same number — that's the original referential bug the rule was added to prevent), keep `MainlineCheck` strict, but downgrade gap-detection on worklogs from FAIL to WARN. Migrations stay strict — schema rebuild applies migrations in order, an out-of-sequence migration is operationally fatal.

## Work Completed

### `pkg/repolint/sequence.go`
- Added `SequenceConfig.AllowGaps bool`. When true, `OK()` ignores `MissingVersions`. Duplicates and unpaired-files still hard-fail.
- Added `SequenceReport.GapsAllowed bool` (mirrors config) so reports self-describe their policy.
- Added `SequenceReport.HasWarnings()` method: true iff `GapsAllowed && len(MissingVersions) > 0`. Lets callers print WARN-level output without forcing a failure.
- `OK()` updated to consult the new field; old behavior (gaps fail) preserved when `AllowGaps` is false.

### `cmd/repolint/main.go`
- `runWorklogs` now passes `AllowGaps: true`.
- After `OK()` passes, if `HasWarnings()` is true, prints `WARN  worklogs sequence has gaps at [...]` to stdout (not stderr, exit 0). Gaps stay visible without blocking CI.
- `runMigrations` unchanged — still does NOT pass `AllowGaps`.
- Failure-message text updated from "must be unique and contiguous" to "must be unique" — accurate to the new semantics.

### `pkg/repolint/sequence_test.go`
Five tests covering the new contract:
1. `TestSequenceCheck_AllowGaps_GapStillReportedButOKTrue` — gap → `OK()=true`, `HasWarnings()=true`, `MissingVersions` populated.
2. `TestSequenceCheck_AllowGaps_DuplicatesStillFail` — uniqueness invariant preserved when `AllowGaps=true`.
3. `TestSequenceCheck_AllowGapsFalse_DefaultBehaviorPreserved` — regression guard for migrations.
4. `TestSequenceCheck_AllowGaps_NoGaps_HasWarningsFalse` — happy-path: `AllowGaps=true` AND no gaps → `HasWarnings()=false`. Catches the bug class where someone simplifies `HasWarnings()` to `return r.GapsAllowed`.
5. `TestSequenceCheck_AllowGaps_GrandfatherBelowExcludesOldGaps` — production config: `AllowGaps=true` AND `GrandfatherBelow=N`. Verifies grandfathered gaps below the threshold are excluded from `MissingVersions` (and thus the WARN output) even when `AllowGaps` is on.

## Key Decisions

1. **Single config flag, not separate `WarnOnGaps`/`AllowGaps`.** The semantics of "gaps don't fail but stay visible" are inseparable for our use case. A second flag would add a state nobody wants (gaps fail silently).
2. **Mirror `AllowGaps` to the report as `GapsAllowed`.** Allows the `OK()` method to know the policy without re-passing the config. Makes the report self-contained for downstream consumers.
3. **`HasWarnings()` instead of overloading `String()`.** Keeps `String()` as "fatal report only" — preserves existing semantics for callers that already use it.
4. **Migrations explicitly do NOT opt in.** Tested by regression guard. The contiguity rule is operationally load-bearing for migrations (gaps break replay); cosmetic for worklogs.
5. **Followup `-fix-worklogs` gap-fill feature is cancelled.** Was originally planned to renumber local-only files downward to fill gaps. With gaps no longer being errors, there's nothing to fill. Saves complexity; gives `FixWorklogs` a single, narrow responsibility (resolve duplicates).

## Alternatives Considered

- **Patch the autofix bot to close gaps after deduplication.** Would require renaming files already on `origin/main`, which `MainlineCheck` correctly forbids. Dead end.
- **Add placeholder worklogs whenever a gap appears.** PR #237 took this approach; closed in favor of this PR. Treating the symptom; cluttered the worklog directory with empty placeholders.
- **Drop sequence checks on worklogs entirely.** Considered; rejected. Uniqueness is real value (prevents the original ambiguous-reference bug). Only contiguity needed to go.
- **Move worklogs to timestamp-based naming (e.g. `2026-06-18T13-47_slug.md`).** Bigger change; would invalidate years of cross-references. Out of scope.

## Blockers

None.

## Tests Run

```
$ go test ./pkg/repolint/...
ok  	github.com/lenaxia/llmsafespace/pkg/repolint	0.088s

$ make repolint   # current main, no gaps
ok    worklogs sequence (354 worklogs, max 0355, grandfathered <0097)
repolint: all checks passed

$ ./bin/repolint  # synthesized gap at 0340 by temporarily moving a file
WARN  worklogs sequence has gaps at [340] (max 0355) — accepted
ok    worklogs sequence (353 worklogs, max 0355, grandfathered <0097)
repolint: all checks passed

$ go vet ./...
(no output)

$ golangci-lint run --timeout=5m
0 issues.
```

## Next Steps

- Merge this PR; main becomes resilient to the concurrent-merge gap pattern.
- Cancel the planned `-fix-worklogs` gap-closing feature (no longer needed).
- If a future append-only artifact (design docs, ADRs) gets numbered, consider whether it should also opt into `AllowGaps`. Standardize policy: "append-only docs allow gaps; operational artifacts require contiguity."

## Files Modified

| File | Change |
|---|---|
| `pkg/repolint/sequence.go` | Added `AllowGaps` field, `GapsAllowed` field, `HasWarnings()` method; updated `OK()` |
| `pkg/repolint/sequence_test.go` | Five new tests covering gap-allowed semantics |
| `cmd/repolint/main.go` | `runWorklogs` opts in to `AllowGaps`; emits WARN line on gaps; updated failure text |
| `worklogs/0356_2026-06-18_repolint-allow-gaps-in-worklogs.md` | This file |
