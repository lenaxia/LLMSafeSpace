# Worklog: repolint Auto-Renumber Worklogs After Rebase

**Date:** 2026-06-17
**Session:** Eliminate the manual "fix worklog number collision" chore by making repolint auto-renumber after every rebase on mainline, with correct incumbent selection.
**Status:** Complete

---

## Objective

The repo's history showed repeated `chore: fix worklog number collision (XXXX → YYYY)` commits — four in the last ~20 commits. The existing `repolint -fix-worklogs` flag and `make pre-commit-fix` could already auto-renumber, but only manually, and it picked which file to rename based on lexical slug order (a coin flip after a rebase). The user asked to make this automatic on every rebase onto mainline.

---

## Work Completed

### Mainline-aware incumbent selection in `FixWorklogs`

`pkg/repolint/sequence.go`: Split `FixWorklogs` into a thin public wrapper plus a testable core `fixWorklogs(dir, remoteByVersion)`. When origin/main is reachable, files present there are treated as incumbents — they stay; files unique to the working copy get renumbered. Added helpers `pickWorklogNewcomer`, `nextFreeWorklogNumber`, `remoteWorklogVersions`. Falls back to lexical tie-breaking when origin/main is unavailable (preserves every existing test's contract).

The previous implementation decided the incumbent by `sort.Strings(files); newcomer := files[len(files)-1]` (sequence.go:473 before this change). After `git rebase origin/main`, both your worklog and mainline's land side-by-side in `worklogs/`; lexical order is a coin flip and renumbered the wrong file ~50% of the time — the root cause of the repeated manual-fix commits.

### New `-fix-worklogs-only` flag

`cmd/repolint/main.go`: Runs the rename pass and exits without the verification checks. For the post-rewrite hook where the tree may be mid-rebase and sequence checks would produce confusing output. Refactored the rename+print logic into `runFixWorklogs(root)` so both `-fix-worklogs` and `-fix-worklogs-only` share one code path.

### `.githooks/post-rewrite` (new)

Fires after `git rebase` / `git commit --amend`. Runs `repolint -fix-worklogs-only`, stages any renames in `worklogs/`, prints a banner only when at least one rename happened (silent on the common no-collision case). Always exits 0 — never blocks a rebase; pre-commit is the safety net. Builds `bin/repolint` on first run if stale; skips gracefully (with a hint to run `make pre-commit-fix`) if the build fails.

### `.githooks/pre-commit` safety-net auto-fix

Added `run_repolint_gate`: when repolint fails specifically on worklog numbering (`FAIL  worklogs sequence|collide`), auto-runs `-fix-worklogs-only`, re-stages `worklogs/`, retries once. Catches the "committed on a stale branch" case that post-rewrite can't (because no rebase happened). Non-worklog failures (migrations, CRD drift, chart drift, gitleaks) still hard-block — those need human judgement.

### `Makefile install-hooks`

Now also `chmod +x .githooks/post-rewrite` and prints a line documenting both hooks.

---

## Key Decisions

1. **Belt-and-suspenders (both hooks) over single-hook.** Post-rewrite is the proactive path (matches "whenever we rebase on mainline" literally); pre-commit is the safety net for the stale-branch-commit case. Two hooks to maintain, but the alternative (single hook) either misses the stale-branch case or couples worklog-fixing to every commit gate.

2. **Mainline incumbent over lexical incumbent.** When asked, the user picked "prefer mainline as incumbent" over "keep lexical order". Correctness over simplicity — lexical was the actual bug behind the manual-fix commits. Lexical order retained as a fallback for when origin/main is unavailable.

3. **`-fix-worklogs-only` skips checks.** The post-rewrite hook fires mid-rebase when the working tree may not reflect every replayed commit; running sequence checks there produces noise. The next `git commit` runs the full gate.

4. **Post-rewrite never blocks.** A hook failure must never abort a rebase — that would leave the user in a detached-HEAD mid-rebase state. Pre-commit is the hard gate; post-rewrite is best-effort with a printed hint on failure.

5. **Pre-commit auto-fix retries exactly once.** Avoids an infinite loop if the "fix" itself introduces a new failure. If the retry fails, the new failure state is surfaced to the user with the standard remediation hint.

---

## Assumptions Stated and Validated

| Assumption | Validation |
|---|---|
| `git rebase` fires `post-rewrite` once at the end with all rewritten SHAs on stdin | Validated via end-to-end test in `/tmp/opencode/rebase-smoke5` — hook fired and renamed `0098_user-feature.md → 0099` while keeping mainline's `0098_mainline.md` |
| `cmd/repolint` emits `FAIL  worklogs sequence…` and `FAIL  worklogs collide…` for the two worklog failure modes | Verified by grepping `main.go:142` and `main.go:159`; matched by `run_repolint_gate`'s `grep -qE 'FAIL  worklogs (sequence\|collide)'` |
| `FixWorklogs(dir)`'s pre-mainline-aware behaviour is preserved when origin/main is unavailable | Validated by `TestFixWorklogs_NilRemoteFallsBackToLexical` + all 10 pre-existing `TestFixWorklogs_*` tests still passing |
| `scanWorklogGit` returns an empty map (not an error) when origin/main is missing | Verified at `sequence.go:667-669` — returns `nil, nil, 0, nil` on git error; `remoteWorklogVersions` translates that to `nil` |
| Grandfather threshold (97) still respected | Verified by `TestFixWorklogs_GrandfatheredVersionsUntouched` (unchanged) |

---

## Tests Run

```
go test -timeout 30s -race ./pkg/repolint/...
ok  github.com/lenaxia/llmsafespace/pkg/repolint  1.171s
# 22/22 pass (16 pre-existing + 6 new)

make repolint
ok    migrations sequence (35 migrations, max version 35)
ok    worklogs sequence (311 worklogs, max 0312, grandfathered <0097)
ok    worklogs no mainline collisions (next available: 0313)
ok    chart migrations match api/migrations/
ok    CRD drift (8 bindings checked)
repolint: all checks passed

go vet ./cmd/repolint/... ./pkg/repolint/...
# clean

gofmt -l pkg/repolint/sequence.go cmd/repolint/main.go pkg/repolint/sequence_test.go
# no output (clean)

bash -n .githooks/pre-commit .githooks/post-rewrite
# shell syntax OK

make pre-commit-fix
# clean on clean tree (CI smoke test contract preserved)
```

### End-to-end smoke tests (in throwaway temp git repos)

1. **Divergent-branch rebase, mainline claims your worklog number**: post-rewrite fired, renamed `0098_user-feature.md → 0099`, kept mainline's `0098_mainline.md`, staged the rename. PASS.
2. **Rebase with no worklog collision**: post-rewrite produced zero output (silent success). PASS.
3. **Commit on stale branch (no rebase) with mainline collision**: pre-commit auto-fix fired, renamed, re-staged, retried. PASS (remaining failures were artifacts of the minimal test repo missing `api/migrations/` and Go source).

---

## Adversarial Self-Review Findings

- **Infinite-loop risk in `fixWorklogs`**: First implementation treated "every local file at v is on mainline" as a fixable duplicate and looped forever. Fixed by narrowing `dupVers` detection: a version with exactly one local file is only fixable if that local file is NOT itself on mainline (`sequence.go:498-526`). Regression test: `TestFixWorklogs_AllOnMainlineFallsBackToLexical`.
- **Renumber could collide with mainline-only numbers**: First implementation used `maxVer++` which would re-collide if mainline had claimed `maxVer+1`. Fixed with `nextFreeWorklogNumber` that skips both local and remote taken numbers. Regression test: `TestFixWorklogs_AvoidsNumbersTakenOnMainline`.
- **Phantom-newcomer could cause no-op rename**: First `pickWorklogNewcomer` could return a mainline-only filename, then the caller would try to `os.Rename` a non-existent file. Fixed by passing `locals` separately and guaranteeing the return value is always a member of `locals`.

---

## Next Steps

- After merge, contributors run `make install-hooks` once to pick up `post-rewrite`.
- If the post-rewrite hook proves noisy in practice, consider gating it behind detecting whether `worklogs/` was touched by the rewrite (parse the `<old> <new>` SHAs on stdin). Defer until there's evidence of noise.

---

## Files Modified

- `pkg/repolint/sequence.go` — `FixWorklogs` split into wrapper + `fixWorklogs`; new `pickWorklogNewcomer`, `nextFreeWorklogNumber`, `remoteWorklogVersions`, `sliceContainsString`
- `pkg/repolint/sequence_test.go` — 6 new tests for mainline-aware incumbent selection
- `cmd/repolint/main.go` — new `-fix-worklogs-only` flag; `runFixWorklogs` helper
- `.githooks/post-rewrite` — new hook
- `.githooks/pre-commit` — `run_repolint_gate` with one-shot worklog auto-fix retry
- `Makefile` — `install-hooks` chmods `post-rewrite`
- `worklogs/0313_2026-06-17_repolint-auto-renumber-post-rebase.md` — this entry
