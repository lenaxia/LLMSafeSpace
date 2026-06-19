# Worklog: /design and /merge Commands + --no-merge Hold Flag

**Date:** 2026-06-19
**Session:** Add a `/design` command (iterate on design docs before implementing), a `/merge` command (explicit finalize), and a `--no-merge` hold flag for the existing code-change commands.
**Status:** Complete

---

## Objective

The existing code-change commands (`/fix`, `/implement`, `/test`, `/security`) auto-merge after approval. Two gaps:

1. No command for **iterating on a design document** *before* implementation. Collaborators want to land a reviewed design under `design/` first, then point a subsequent `/implement` at it.
2. No way to **hold the merge** on a `/fix`/`/implement`/`/test`/`/security` run when they want to inspect the approved result before landing it.

Add `/design` (always holds) and `/merge` (explicit finalize), and a `--no-merge` modifier to hold the code-change commands.

---

## Work Completed

### New commands
- **`/design [text]`** — `design.md`: creates/updates a numbered design doc under `design/` (or `design/stories/<epic>/`), opens a PR, iterates through the automated review until APPROVE, then **holds** — never auto-merges. Refine with further `/design` invocations; land with `/merge`. Writes only the design document, no production code.
- **`/merge`** — `merge.md`: finalize-only. Verifies the latest review is APPROVE and required CI is green, then squash-merges (`gh pr merge --squash --delete-branch`). Refuses to merge unapproved/failing PRs and reports the blocker. No code changes.

### `--no-merge` hold flag
- Appending `--no-merge` to `/fix`, `/implement`, `/test`, `/security` holds the run: it iterates to approval but does NOT merge — it stops and waits for an explicit `/merge`.
- Default behaviour (auto-merge after approval) is unchanged when the flag is absent.

### Workflow wiring (`ai-comment.yml`)
- Added `/design` and `/merge` to the trigger `if` (both `startsWith` and inline `contains` forms).
- Added routing cases in both the detection `case` and the prompt-build `case`.
- `--no-merge` detection: a global modifier — stripped from NOTE for every command (so it never pollutes the description/topic), but only sets `HOLD_MERGE=1` for the four code-change commands. `/design` holds via its own prompt; `/merge` ignores the flag. The hold directive is appended last so it unambiguously overrides Code Change Workflow step 7.

### Doc updates
- `code-change-workflow.md`: documented the `--no-merge` hold and the `/merge` finalize path; `/design` always holds.
- `context.md`, `help.md`, `commands-footer.md`: added `/design` and `/merge`; documented `--no-merge` and the hold semantics.

---

## Key Decisions

1. **`/design` always holds; `/merge` is the release.** Matches the user's intent ("hold the merge until we are confident"). A design doc is a commit point in its own right and shouldn't auto-merge behind a review approval.
2. **`--no-merge` keeps auto-merge as the default** (opt-out, not opt-in). Chosen over flipping the default to never-merge so existing muscle memory (`/implement …`) is unchanged; the flag is the exception for when you want to hold.
3. **`--no-merge` stripped globally, acted on selectively.** Prevents the flag leaking into `/design` or `/merge` NOTE text, while only the four code-change commands respond to it. Simpler than per-command special-casing.
4. **`/merge` verifies approval + CI before merging.** It is finalize-only and refuses unapproved/failing PRs rather than force-landing them — consistent with the repo's "never merge before approval" hard rule.
5. **No new auto-merge wiring for `/design`.** Reused the existing `code-change-workflow.md` (branch → PR → iterate → approve) and overrode only the merge step via the prompt, avoiding duplicated workflow logic.

---

## Assumptions

1. **Assumed:** `--no-merge` as a trailing/mid token is unambiguous and won't appear in legitimate descriptions.
   **Validated:** Treated as a global substring; stripped via sed. No existing command description uses this token. Acceptable; if a description legitimately contains it, the run holds (safe failure direction).
2. **Assumed:** The opencode action has sufficient `GITHUB_TOKEN` scope for `gh pr merge --squash --delete-branch`.
   **Validated:** All three workflows already grant `contents: write` and `pull-requests: write`; the existing `/implement` auto-merge path relies on the same scope. `/merge` reuses it.
3. **Assumed:** `/design` docs should follow the existing `design/NNNN_YYYY-MM-DD_description.md` convention.
   **Validated:** Confirmed against `design/` listing (0021–0041 + stories/). The prompt instructs the model to pick the next free number and run repolint.
4. **Assumed:** `/merge` on a non-PR thread is a no-op/error.
   **Validated:** `merge.md` identifies "the current PR" via `gh pr view`; on an issue it will fail to find a PR and report — acceptable, since `/merge` is meaningless off a PR.

---

## Blockers

None.

---

## Tests Run

- `yaml.safe_load` on `ai-comment.yml` → OK.
- `bash -n` on the full Build-prompt script → OK.
- Routing + `--no-merge` harness (14 cases, all PASS): `/design` and `/merge` route (standalone + inline); `--no-merge` sets HOLD only for /fix,/implement,/test,/security and is stripped from NOTE everywhere; prefix safety (`/testing` does not route to `/test`); correct prompt files attached (design.md+workflow for /design; merge.md only for /merge); HOLD directive injected iff HOLD_MERGE=1; design holds via prompt even with `--no-merge`.
- (Pending) live validation once the PR's automated review runs and the footer/comment behaviour is observed.

---

## Next Steps

- Land PR #288 (footer dedup) first — this branch depends on it.
- After this PR is approved, `/merge` it.
- Monitor first live use of `/design` and `/merge`; refine prompts if the model mis-handles the hold/release semantics.

---

## Files Modified

- `.github/prompts/design.md` (new)
- `.github/prompts/merge.md` (new)
- `.github/prompts/code-change-workflow.md` (modified — `--no-merge` hold + `/merge`)
- `.github/prompts/context.md` (modified — new commands)
- `.github/prompts/help.md` (modified — new commands + `--no-merge`)
- `.github/prompts/commands-footer.md` (modified — new commands)  *(also changed by #288)*
- `.github/workflows/ai-comment.yml` (modified — triggers, routing, `--no-merge`)  *(also changed by #288)*
- `worklogs/0391_2026-06-19_ai-design-merge-commands.md` (new — this file)
