# Worklog: AI Command Reference — Once-Per-Thread Posting

**Date:** 2026-06-19
**Session:** Fix the AI command footer never reaching PR/issue threads; move it to a dedicated, deduplicated post-run step.
**Status:** Complete

---

## Objective

Collaborators and reviewing LLMs had no visible list of available AI commands (`/ai`, `/review`, `/fix`, …) on any PR or issue thread. Example evidence: PR #276 has ~7 bot comments and zero command footers. Make the command reference appear exactly once per thread.

---

## Work Completed

### Root cause
All three AI workflows (`ai-comment.yml`, `pr-review.yml`, `issue-opened.yml`) appended `commands-footer.md` into `OPENCODE_PROMPT`. That variable is the **userPrompt** — the instruction fed *into* the model — as the workflow's own comments document (`ai-comment.yml:48-50`: "Build the PROMPT that opencode uses as userPrompt"). The model was never instructed to reproduce the footer in its posted comment, so it never appeared. The footer was silent input context only.

### Fix
- Removed the footer from all three prompt builds.
- Added a new `Post AI command reference (once per thread)` step after `Run OpenCode` in each workflow:
  - Scans the thread's existing comments for a hidden marker (`<!-- ai-commands-footer -->`) via `gh api …/issues/{n}/comments --paginate -q '.[].body' | grep -qF`.
  - Marker absent → posts the footer as its own comment (marker prepended). Present → skips.
  - Thread number resolves across event types (`github.event.issue.number || github.event.pull_request.number`).
- Rewrote `commands-footer.md` as a clean standalone comment (full table of all 10 commands, wording aligned with `help.md`) instead of a bare inline line that only made sense as prompt context.
- Left `context.md` (which lists the commands for the model's own awareness) in the prompt — that part was already correct.

---

## Key Decisions

1. **Separate comment, exactly once per thread** (vs. instructing the model to echo the footer). Chosen because it does not depend on LLM compliance and guarantees exactly-once-per-thread via the marker. Trade-off: adds ~15 lines of bash per workflow (acceptable; GitHub Actions has no native step-sharing and a composite action would be over-engineering for 3 consumers).
2. **Marker-based dedup with `grep -F`** — treats the HTML comment as a fixed string, so the `<!-- -->` is not regex-interpreted. Idempotent across `/ai` re-triggers, `synchronize` re-pushes, and re-reviews.
3. **Footer removed from the prompt entirely** (not just supplemented) — keeping it in the prompt would have been dead weight; `context.md` already carries command awareness for the model.

---

## Assumptions

1. **Assumed:** `OPENCODE_PROMPT` is the model input (userPrompt), not the output template.
   **Validated:** Workflow comment at `ai-comment.yml:48-50`; confirmed by observing that no bot comment on PR #276 contained the footer.
2. **Assumed:** `gh issue comment` works on PR threads.
   **Validated:** PRs share the issues comment API endpoint; established GitHub pattern. Confirmed live on PR #288 itself — the new step posted the footer comment on its own thread on first run.
3. **Assumed:** A hidden HTML comment marker survives GitHub's comment rendering and is returned verbatim by the comments API.
   **Validated:** Standard GitHub markdown behaviour; the live run on #288 confirmed the marker is stored and the dedup `grep` matches it.

---

## Blockers

None.

---

## Tests Run

- `yaml.safe_load` on all three workflow files → OK; each has the new step; footer fully removed from prompt builds (grep confirms no stale references).
- `bash -n` on the new post-run step in all three files → OK.
- Dedup simulation (3 consecutive runs on one thread) → posts exactly once, then skips twice; marker + footer table both present in the posted body. Assertions pass.
- **Live validation:** the `pr-review.yml` workflow ran on PR #288 itself and posted the command-reference comment as its own comment on first run — end-to-end proof.

---

## Next Steps

- Land PR #288 after the automated review approves.
- Follow-up (worklog 0400): add `/design` and `/merge` commands and a `--no-merge` hold flag to the code-change commands.

---

## Files Modified

- `.github/prompts/commands-footer.md` (rewritten as standalone comment)
- `.github/workflows/ai-comment.yml` (removed footer from prompt; added dedup post-run step)
- `.github/workflows/pr-review.yml` (same)
- `.github/workflows/issue-opened.yml` (same)
- `worklogs/0399_2026-06-19_ai-commands-footer-dedup.md` (new — this file)
