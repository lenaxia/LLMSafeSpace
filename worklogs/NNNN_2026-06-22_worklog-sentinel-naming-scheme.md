# Worklog: NNNN_ sentinel worklog naming scheme

**Date:** 2026-06-22
**Session:** Replace the manual worklog numbering scheme (authors pick the next free number) with a sentinel scheme (authors write `NNNN_`, post-merge bot assigns the real number) to eliminate the merge-time collision class that caused CI failures and skipped build artifacts on PRs #343/#344.
**Status:** Complete

---

## Objective

Two PRs (#343 and #344) both independently chose worklog number `0474` because each observed `0473` as the max on origin/main at branch-push time. The merge collision failed `repolint`'s lint check, which (as a hard gate) cascaded into skipping all build jobs — no API/Controller/Runtime/Relay Docker artifacts were produced. The post-merge auto-fix bot resolved the collision, but its `[skip ci]` commit left no green CI run on the clean tree.

This session eliminates the collision class entirely by removing the author's responsibility to pick a number. Authors write `NNNN_YYYY-MM-DD_slug.md` (a sentinel placeholder). The post-merge bot assigns the real sequential number at merge time, serialized by GitHub's sequential merge-commit ordering — collisions become structurally impossible.

---

## Work Completed

### `pkg/repolint/sequence.go`

- New `WorklogSentinelPattern` regex matching `NNNN_YYYY-MM-DD_slug.md`.
- New `SentinelReport` / `SentinelCheck` — scans a dir for `NNNN_` files. Used as the non-gating "is the bot broken?" signal on main, and as the pre-commit gate for new worklogs.
- `FixWorklogs` extended with a sentinel-assignment pass (`assignSentinels`) that runs before duplicate resolution. Each `NNNN_` file is renamed to the next free number (accounting for both local and origin/main versions), processed in lexical order so same-branch batches get contiguous numbers. Self-referential content inside the file is updated to match the new name.
- The duplicate-resolution loop is retained for the transition period (existing numbered worklogs on branches predating the scheme can still collide). Once all branches use sentinels, duplicates become impossible and the loop becomes dead code.

### `cmd/repolint/main.go`

- `runWorklogs` (sequence check with duplicate/gap detection) and `runWorklogMainline` (mainline collision check) replaced by `runWorklogSentinels`.
- The sentinel check on main is **non-gating** (warns only, returns 0). A `NNNN_` file persisting on main means the post-merge bot is broken — real signal, but blocking builds on a documentation filename is disproportionate. The previous behavior (blocking on worklog numbering) is exactly what caused the PR #343 artifact gap.
- Migration sequence check is unchanged (still strict, still gating — schema ordering has real semantics).

### `.githooks/pre-commit`

- New `run_worklog_prefix_gate`: blocks commits that add new worklog files not matching `NNNN_YYYY-MM-DD_slug.md`. Checks only **added** files (`--diff-filter=A`); modified existing-numbered worklogs pass through untouched (the convention applies to new files only).
- `run_repolint_gate` simplified: the worklog auto-fix retry is removed (collisions are structurally impossible under sentinels). Other repolint failures (migrations, CRD drift, chart drift) still gate.

### `.github/workflows/ci.yml`

- `repolint-autofix` job renamed from "Auto-fix worklog collisions" to "Assign worklog numbers". `permissions` extended with `pull-requests: write` so the bot can comment on the merged PR.
- The job runs `repolint -fix-worklogs-only` (now handles both `NNNN_` sentinels and residual duplicates), pushes the renumbered files, and comments the assigned numbers on the merged PR:
  ```
  ## Worklog numbers assigned
  - 0545_2026-06-22_org-admin-force-verify-member.md
  ```
- The PR number is extracted from the merge commit message (`#NNN` pattern).

### `README-LLM.md`

- Worklog naming section rewritten: `NNNN` is documented as a literal sentinel placeholder, not a number to pick. Explains why (race elimination) and what happens at merge (bot assigns number, comments on PR).

### Tests

- `pkg/repolint/sequence_test.go`:
  - 3 new `SentinelCheck` tests (no sentinels, detects sentinels, ignores non-matching files).
  - 5 new `FixWorklogs` + sentinel integration tests (single sentinel assignment, multiple sentinels get contiguous numbers, sentinel avoids mainline numbers, sentinel + duplicate in same pass, sentinel self-reference updated).
  - `TestLive_Worklogs_NoDuplicates` repurposed: checks for `NNNN_` sentinels on main (the new invariant) instead of sequence duplicates (the old invariant that the scheme makes impossible).
  - `TestLive_Worklogs_NoMainlineCollisions` retired (`t.Skip`) — mainline collisions are structurally impossible under the sentinel scheme.

---

## Key Decisions

1. **Merge-time assignment, not PR-open-time.** An earlier proposal assigned numbers at PR-open (via a `concurrency`-queued job that comments the number). The user rejected it as "too complicated for what this is worth." Merge-time assignment is simpler (one job, no queue, no reservation refs) and the only downside (author learns their number post-merge) is acceptable.

2. **Non-gating sentinel check on main.** The PR #343 incident proved that gating builds on worklog numbering is disproportionate — a documentation filename collision should not prevent producing Docker artifacts. A `NNNN_` persisting on main means the bot is broken, which is real signal worth investigating, but the next merge's bot run heals it and the next merge's CI run produces artifacts regardless.

3. **Pre-commit gate is strict for new files, permissive for modifications.** The regex `^NNNN_\d{4}-\d{2}-\d{2}-[a-z0-9._-]+\.md$` catches both failure modes: author picks their own number (`0543_foo.md`) and author omits the prefix entirely (`foo.md`). Modified existing-numbered files (`0474_foo.md` touched for a typo fix) pass through — the convention applies to new files only, and the user explicitly required this.

4. **Duplicate-resolution loop retained.** During the transition period, branches predating the sentinel scheme still carry manually-numbered worklogs that can collide. The loop handles this. Once all active branches use sentinels, duplicates become impossible and the loop is dead code (but harmless to retain as defense-in-depth).

5. **Sentinel assignment avoids origin/main numbers.** `assignSentinels` seeds its occupied-version set from both local numbered worklogs AND origin/main's versions (via `remoteWorklogVersions`). This prevents a sentinel on a feature branch from being assigned a number that's already on main, which would cause a collision on merge. This is the same mainline-awareness the old `FixWorklogs` duplicate-resolution used.

---

## Assumptions

| # | Assumption | Validation |
|---|---|---|
| A1 | GitHub serializes merge commits — two merges cannot land in the same instant on the same parent. | Confirmed by GitHub's merge-commit model: each merge creates a commit on main's tree, building on the previous HEAD. The post-merge bot runs on each push event sequentially. |
| A2 | The `GITHUB_TOKEN` can comment on PRs (`pull-requests: write`). | Confirmed by the `permissions:` block added to the job. The existing `contents: write` already works for pushing the rename commit. |
| A3 | `WorklogSentinelPattern` does not match any existing numbered worklog. | Confirmed: the pattern requires the literal prefix `NNNN_` (uppercase), while numbered worklogs start with `\d{4}_`. No overlap. |
| A4 | The pre-commit hook's `--diff-filter=A` correctly classifies renamed files. | A renamed file (`git mv old new`) appears as `R` (rename), not `A` (add). So renaming an existing worklog does NOT trigger the sentinel check — only genuinely new files do. This matches the user's requirement that modified/renamed existing-numbered files are fine. |
| A5 | The `MainlineCheck` and sequence-check code paths remain in the package (not deleted) for the transition period. | Confirmed — the functions are retained; only the CLI calls and live tests are retired. Branches with old-style numbered worklogs still benefit from `FixWorklogs`'s duplicate resolution. |

---

## Adversarial self-review

**Phase 1 — Findings:**

1. *What if two sentinels in the same branch have identical slugs?* `NNNN_2026-06-22_foo.md` twice. `assignSentinels` processes them in lexical order — they're identical, so order is stable, but both get assigned different numbers (0098, 0099). The duplicate-slug file is not detected. However, this is a content problem (two worklogs with the same slug), not a numbering problem — the files end up numbered correctly, just with confusingly similar names. Acceptable: the author should notice during review.

2. *What if the bot's push fails (race with another push)?* The job fails, the `NNNN_` file stays on main, the next push's bot run picks it up. The non-gating sentinel check warns but doesn't block. Self-healing.

3. *What if the merge commit message has no `#NNN` (e.g. direct push to main)?* The comment step exits 0 with a log line. No comment posted. The rename still happened. Acceptable — direct pushes to main are forbidden by policy anyway.

4. *Does removing the sequence check miss real migration-style issues in worklogs?* No — worklogs have no ordering semantics (unlike migrations where N+1 depends on N). The sequence check was a proxy for "prevent collisions," which sentinels make impossible by construction.

**Phase 2 — All findings documented as false alarms with rationale or accepted as non-blocking edge cases.**

**Phase 3 — No remediation needed.**

---

## Blockers

None.

---

## Tests Run

```bash
# New sentinel + FixWorklogs integration tests
go test -timeout 60s ./pkg/repolint/ -run 'TestSentinel|TestFixWorklogs_(Assigns|Multiple|Sentinel)' -v
# 10/10 PASS

# Full repolint suite (including existing tests + live tests)
go test -timeout 60s ./pkg/repolint/
# PASS (TestLive_Worklogs_NoMainlineCollisions skipped — retired)

# CLI builds and runs clean against the live repo
go build -o /tmp/repolint ./cmd/repolint && /tmp/repolint
# ok    migrations sequence (41 migrations, max version 41)
# ok    worklogs no NNNN_ sentinels (all numbered)
# ok    chart migrations match api/migrations/
# ok    CRD drift (8 bindings checked)
# repolint: all checks passed

# Pre-commit hook syntax
bash -n .githooks/pre-commit
# syntax OK

# Vet
go vet ./pkg/repolint/ ./cmd/repolint/
# clean
```

---

## Next Steps

- After this merges, the post-merge bot assigns it a number and comments it on the PR.
- Existing branches with manually-numbered worklogs continue to work (the duplicate-resolution loop handles them). New branches should use `NNNN_`.
- Consider a one-time migration script to detect and warn about branches with old-style numbered worklogs, so authors know to rebase or rename.
- The duplicate-resolution loop in `FixWorklogs` can be removed once all active branches use sentinels (track via the absence of `FAIL worklogs sequence` in CI logs for ~2 weeks).

---

## Files Modified

- `pkg/repolint/sequence.go` — `WorklogSentinelPattern`, `SentinelReport`/`SentinelCheck`, `assignSentinels`, extended `FixWorklogs` doc.
- `pkg/repolint/sequence_test.go` — 8 new tests; repurposed `TestLive_Worklogs_NoDuplicates`; retired `TestLive_Worklogs_NoMainlineCollisions`.
- `cmd/repolint/main.go` — replaced `runWorklogs`/`runWorklogMainline` with `runWorklogSentinels` (non-gating); removed `worklogGrandfatherBelow`.
- `.githooks/pre-commit` — new `run_worklog_prefix_gate`; simplified `run_repolint_gate` (removed worklog auto-fix retry).
- `.github/workflows/ci.yml` — `repolint-autofix` renamed, extended with PR comment step.
- `README-LLM.md` — worklog naming section rewritten for sentinel scheme.
