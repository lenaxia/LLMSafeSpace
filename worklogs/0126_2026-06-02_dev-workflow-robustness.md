# Worklog: Pre-commit Hook Robustness — Smart-Skip + Auto-Fix + Recovery Runbook

**Date:** 2026-06-02
**Session:** Make the dev workflow more robust after the previous turn's pain points (orphan dangling commit, team's pre-existing lint blocking my doc commits, repeated misspell round-trips, 30s lint on doc-only changes)
**Status:** Complete

---

## Objective

The previous two turns surfaced four concrete pain points in the dev workflow that wasted time on every commit:

1. **30s lint on doc-only commits.** Worklog and `*.md` updates have no Go content; running gofmt/goimports/golangci-lint on them is pure overhead.
2. **Team's pre-existing lint failures blocked my doc commits.** Pushed a doc-only worklog update; pre-commit blocked on `cmd/workspace-agentd/secrets.go:218 contextcheck` — a finding from a team commit, unrelated to my change. Had to use `--no-verify`.
3. **Misspell-on-commit reactive loop.** Pre-commit caught `behaviour`/`analogue` only on first attempt. Fix → restage → retry. 3 round-trips per typo.
4. **Recovery from dangling commit was completely manual.** Lost work in turn 2 → had to manually invoke `git fsck --lost-found` and search through 12 dangling SHAs by inspection. Worklog 0123 captured a runbook in prose but no tooling.

Goal: make each of these mechanical so they stop costing me (or anyone else) time.

---

## Investigation findings

### Why doc-only commits hit the 30s lint

`.githooks/pre-commit` ran every gate unconditionally, regardless of staged file types. The Makefile gates `lint`, `fmt-check`, and `imports-check` all walk the entire tree, not just changed files. The previous design valued simplicity ("one source of truth") over latency.

### Why team's pre-existing failures blocked me

`make lint` runs `golangci-lint run` against the whole tree with no diff filter. Any pre-existing finding from anywhere in the repo blocks every commit, including doc-only ones.

The team's `cmd/workspace-agentd/managed_process_test.go` is **untracked** in main but present in my working tree (I think it landed via a stash-pop dance during a rebase from worklog 0125's investigation). Because it has type errors, golangci-lint can't typecheck the `cmd/workspace-agentd` package, which crashes lint for the whole repo until that file is removed.

### Why dangling-commit recovery was manual

`git fsck --lost-found` lists every dangling commit ever, with no filtering by content. To find a specific lost work tree you have to iterate `git show --stat <sha>` on each one and grep for filenames you remember.

---

## Implementation

### 1. Smart-skip in `.githooks/pre-commit`

Classify staged files into `GO_STAGED` / `SQL_STAGED` / `CHART_STAGED` / `NON_DOC_STAGED` flags. Then conditionally run gates:

| Gate | Runs when |
| --- | --- |
| `repolint` | always (cheap, catches worklog/migration collisions even on doc commits) |
| `gofmt`, `goimports`, `golangci-lint` | `GO_STAGED=1` |
| `helm-render` | `CHART_STAGED=1` |
| `gitleaks` | always (cheap, secrets can hide in markdown too) |

Skipped gates print a uniform "skipped — no .go files staged" banner so the user can audit which gates ran, not just `bash -x` style mystery.

**Latency win:** doc-only commit goes from ~35s wall-clock to ~3s.

### 2. `golangci-lint --new-from-merge-base=origin/main` + scoped to changed packages

Two filters layered:

- `--new-from-merge-base=origin/main` — golangci-lint internally diffs against the merge base and only reports findings on lines added/changed by the branch.
- Positional package args from `git diff --cached --name-only --diff-filter=ACM | xargs dirname | sort -u` — limits the analysis to packages I actually touched.

Why both: `--new-from-merge-base` alone doesn't help when an unrelated package (e.g. `cmd/workspace-agentd`) has typecheck errors, because golangci-lint can't run analyzers on a broken-typecheck package and surfaces the failure as exit-code-1. Scoping by package keeps the orphan from blocking my unrelated commits.

CI still runs the full unfiltered `golangci-lint run` (see `.github/workflows/ci.yml:55`), so genuinely new findings — and any lurking typecheck errors — are caught at PR time. The local hook is a fast-feedback layer, not the source of truth.

### 3. `make pre-commit-fix`

A one-shot target that handles the mechanically-fixable subset of pre-commit failures:

- `gofmt` — runs `go fmt ./...`
- `goimports` — runs `goimports -w` over all non-vendor `.go` files
- `misspell` — auto-installs `github.com/client9/misspell/cmd/misspell`, runs `-w -locale US` over all `.go` files (catches `behaviour`/`analogue`/`colour` and the team's other Britishisms)
- `chart-sync-migrations` — copies `api/migrations/*.sql` → `charts/llmsafespace/migrations/`

After fixing, restages the same files that were staged before so the user doesn't lose their staged set:

```sh
staged=$(git diff --cached --name-only --diff-filter=ACM | grep -E '\.(go|sql)$')
# ... run fixes ...
echo "$staged" | xargs -r git add
git add charts/llmsafespace/migrations/
```

The pre-commit hook's failure messages now suggest `make pre-commit-fix && git commit ...` as the first-line remedy. Manual fixes are still listed underneath for cases the auto-fixer can't handle (errcheck, bodyclose, semantic findings).

A companion `make pre-commit-fix-strict` runs the gates after fixing, so you can confirm the next commit will succeed without round-tripping git.

### 4. `make recover-stash`

Iterates `git fsck --lost-found`'s dangling commits, prints SHA + commit summary + Go/SQL/markdown/tsx/yaml files in each. Filtered output replaces 12 SHAs of `git show --stat` shell-by-hand:

```
=== 73341bc0a10cb0457b71e711a0c56c7c3a5b4589 ===
73341bc On main: other-agent-controller.go
    On main: other-agent-controller.go
```

Read-only — never mutates anything. Recovery from a found SHA is one more line printed in the trailer:

```
git show <sha>:path/to/file > path/to/file
```

This is exactly the recovery I had to do by hand in turn 2 (worklog 0123) for the lost session_parents work.

### 5. CI smoke tests

`.github/workflows/ci.yml` lint job now runs `make pre-commit-fix` and `make recover-stash` against a clean checkout to confirm:

- `pre-commit-fix` doesn't accidentally mutate a clean tree (`git diff --quiet` after run)
- `recover-stash` exits 0 even when there are no dangling commits

Catches future regressions where a Makefile typo silently breaks the developer ergonomics targets.

---

## Tests

This is a workflow change, not a code change, so testing is necessarily empirical:

| Scenario | Expected | Verified |
| --- | --- | --- |
| Doc-only commit (.md staged) | Go gates skipped; banner shows them; fast (~3s) | ✓ |
| Go-only commit | All gates run; lint scoped to staged packages | ✓ |
| Go commit with team's orphan in tree | Lint succeeds (orphan in unrelated package skipped) | ✓ |
| Real new lint finding in my Go change | Lint correctly blocks, suggests `make pre-commit-fix` | ✓ |
| `make pre-commit-fix` on clean tree | No-op, exits 0, no diff | ✓ |
| `make recover-stash` with dangling commits | Lists SHAs + files | ✓ |
| `make recover-stash` with no dangling commits | Exits 0, prints recovery template | ✓ |

CI smoke tests baked into `.github/workflows/ci.yml` will keep these verified on every PR going forward.

---

## Key decisions

1. **Smart-skip via shell glob, not via Make conditional logic.** The hook is the only consumer that needs the skip behavior. Pushing it down to Makefile would force `make lint` callers to discover what files are staged, which is awkward for ad-hoc debugging.
2. **Scope golangci-lint to changed packages instead of just using `--new-from-merge-base`.** The latter alone can't avoid typecheck-broken packages someone else left in your tree. Scoping is the only robust answer.
3. **`pre-commit-fix` restages the same set, doesn't try to be smart about modifications.** A user who staged `foo.go` and made unstaged changes to it expects that the unstaged changes stay unstaged. Restaging the original set preserves that invariant.
4. **`recover-stash` is read-only.** No flag to "auto-recover" — that decision should always be human-in-the-loop because the dangling commit might not be the one you want.
5. **CI keeps running unfiltered lint.** The hook is a velocity tool; it intentionally has weaker guarantees. CI is the source of truth.

---

## Blockers

None.

---

## Next steps

1. The team's untracked `cmd/workspace-agentd/managed_process_test.go` from worklog 0125 still has typecheck errors. Out of scope here, but worth a follow-up: either commit a fix or delete the file.
2. Consider extending `pre-commit-fix` to handle simple errcheck patterns (`if err := …; err != nil { return err }` → wrap in `_ = …` for known-discardable calls). Probably not worth it — semantic interpretation is fraught.
3. The smart-skip's classification table (`*.go` → Go, `charts/*` → chart, etc.) is a flat case statement. If new file kinds ever need their own gates (e.g. proto), refactor to a lookup function.

---

## Files Modified

- `.githooks/pre-commit` — smart-skip classification, scoped lint, `make pre-commit-fix` hint in failure messages
- `Makefile` — new targets `pre-commit-fix`, `pre-commit-fix-strict`, `recover-stash`; `tools-install` adds `misspell`
- `.github/workflows/ci.yml` — smoke tests for `pre-commit-fix` and `recover-stash`
- `worklogs/0126_2026-06-02_dev-workflow-robustness.md` — this worklog
