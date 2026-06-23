# Lint fix: `behaviour` → `behavior` and De Morgan simplification

## Context

After PR #376 (`feat(epic-49): delete secrets + suspend workspaces on password reset`)
landed on `main` at commit `95ffd5c8`, the post-merge `Lint` job on `main`
went red with two `golangci-lint` v2.12.2 findings:

```
pkg/secrets/pg_key_store.go:48:33: `behaviour` is a misspelling of `behavior` (misspell)
api/internal/services/workspace/workspace_service.go:616:7: QF1001: could apply De Morgan's law (staticcheck)
```

The two findings come from PR #376's diff:
- `pkg/secrets/pg_key_store.go:48` — comment in `CreateUserKey` describing the
  `ON CONFLICT DO UPDATE` reset semantics ("desired reset behaviour").
- `api/internal/services/workspace/workspace_service.go:616` — conflict-error
  suppression in `NeutralizeUserWorkspaces` written as
  `if !(errors.As(err, &apiErr) && apiErr.Type == apierrors.ErrorTypeConflict)`.

The PR-level lint check on #376 evidently passed because the upstream `main`
state at that point did not include both of these new lines together with the
v2.12.2 ruleset state that flagged them, but the merged-state CI on `main`
(which is the gate other PRs are evaluated against) is now red. This blocks
clean post-merge CI for any subsequent PR landing on `main`, so we fix it
immediately as a small targeted change.

## Why these specific fixes

### `behaviour` → `behavior`

The repo's `golangci-lint` config enables the `misspell` linter with US-English
locale, so `behaviour` (UK spelling) is consistently flagged. Other files in
the repo use US spelling (`behavior`). One-character comment edit; zero
runtime impact.

### `!(A && B)` → `!A || !B`

`staticcheck` rule `QF1001` (a Quick Fix family rule) advises De Morgan's law
where it improves readability. The rule is enabled in this repo's config.

The original:
```go
if !(errors.As(err, &apiErr) && apiErr.Type == apierrors.ErrorTypeConflict) {
    s.logger.Warn(...)
}
```
becomes:
```go
if !errors.As(err, &apiErr) || apiErr.Type != apierrors.ErrorTypeConflict {
    s.logger.Warn(...)
}
```

These are logically equivalent by De Morgan's law:
`¬(A ∧ B) ≡ ¬A ∨ ¬B`. The behavior is identical: the `Warn` fires when
either (a) the error is not an `*apierrors.APIError` at all, or (b) it is one
but not of type `ErrorTypeConflict`. The conflict-suppression behavior tested
by `TestNeutralizeUserWorkspaces_NonActiveConflictIsNotNoisy` is preserved.

### Short-circuit safety

The De Morgan'd form short-circuits the second clause (`apiErr.Type != ...`)
when `errors.As(...)` returns `false`. This is **safer** than the original,
not less safe: in the original, `errors.As(...)` returning `false` meant the
inner `&&` short-circuited and `apiErr.Type` was never read; in the new form,
`!errors.As(...)` returning `true` short-circuits the outer `||` and
`apiErr.Type` is also never read. Both forms guarantee `apiErr` is set
before its `.Type` field is accessed.

`errors.As` documents that on a successful match it sets the target and
returns `true`, and on no-match it leaves the target unmodified and returns
`false`. The original code relied on this contract (and on `&&`
short-circuit) for safety; the rewrite relies on the same contract and on
`||` short-circuit for the same safety. No new nil-deref risk.

## Validation

- `golangci-lint run --timeout=5m ./...` from the repo root: **0 issues**
  (matches CI's invocation in `.github/workflows/ci.yml`'s Lint job).
- `go build ./pkg/secrets/... ./api/internal/services/workspace/...`: clean.
- `go test -run TestNeutralizeUserWorkspaces -count=1 ./api/internal/services/workspace/...`:
  PASS (3 tests including
  `TestNeutralizeUserWorkspaces_NonActiveConflictIsNotNoisy` which exercises
  the modified branch).

## Adversarial self-review

- **Did I change semantics?** No. De Morgan's law produces a logically
  equivalent expression. Truth-tabled both forms manually:
  | `errors.As` | `Type==Conflict` | original `!(A∧B)` | new `!A∨!B` |
  |---|---|---|---|
  | true | true | false (suppress) | false (suppress) |
  | true | false | true (warn) | true (warn) |
  | false | — | true (warn) | true (warn) |
  Identical.
- **Could the `apiErr.Type` access panic if `errors.As` returns false?**
  No, because `||` short-circuits the second clause when the first is true.
  This matches the original `&&` short-circuit behavior in the inner clause.
- **Is the `behaviour → behavior` change safe across the repo?** Yes — it's
  a single-file comment-only change; no string literals, no keys, no exports.
- **Did I touch sibling-agent work beyond what was asked?** No. Only the two
  exact lines flagged by `golangci-lint` were modified. The user explicitly
  asked for the lint fix, overriding the standing "don't touch other agents'
  files" guidance for these specific lines.
- **Should I have rewritten the De Morgan form to keep the original
  `errors.As(...)` boolean polarity (e.g., introduce a helper
  `isConflictError`)?** That would be a larger refactor and is not what the
  linter asked for. `staticcheck`'s suggested fix is exactly the De Morgan
  rewrite; mirror it to keep the diff minimal and the intent clear.

## Files touched

- `pkg/secrets/pg_key_store.go` — comment spelling fix (1 char)
- `api/internal/services/workspace/workspace_service.go` — De Morgan rewrite
  (1 conditional, 5 lines reformatted into idiomatic form)
- `worklogs/0525_2026-06-23_lint-fix-misspell-demorgan.md` — this worklog

## Followups

None. After this lands, `main`'s `Lint` check should go green again, and
subsequent PRs will start from a clean lint baseline.
