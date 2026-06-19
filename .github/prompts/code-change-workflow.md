## Code Change Workflow (MANDATORY)

Every code change MUST follow this review-iterate-approve cycle without exception:

1. **Branch:** Create a feature branch (`feat/`, `fix/`, `test/`, or `security/` prefix). Never commit to main.
2. **TDD:** Write tests first. Run them — they must fail. Write minimal code to pass. Run them — they must pass. Refactor.
3. **PR:** Open a pull request with a clear description. Reference the triggering issue or comment.
4. **Wait for review:** The automated PR review triggers on every PR open and push. Wait for it to complete before proceeding.
5. **Address feedback:** Read every finding. Fix ALL real issues. Push to the same branch — this triggers automatic re-review.
6. **Iterate:** Repeat steps 4–5 until the automated reviewer posts APPROVE.
7. **Merge:** After approval only — merge with squash method, **unless this run was invoked with `--no-merge`** (see Hold below) or it is a `/design` run (which always holds). In a held run, skip merging and post a comment stating the PR is approved and awaiting an explicit `/merge`.
8. **Report:** Post a comment on the original issue/PR confirming completion with a summary of changes.

**Merge control (`--no-merge` and `/merge`):**
- By default `/fix`, `/implement`, `/test`, and `/security` auto-merge after approval (step 7).
- Append `--no-merge` to any of those commands to hold the merge: the run iterates to approval but does NOT merge — it stops and waits for an explicit `/merge`. Use this when you want to review the approved result before landing it.
- `/design` **always** holds — design docs never auto-merge. Iterate to approval, then await `/merge` (further `/design` invocations can refine the doc first).
- `/merge` is the explicit finalize command: it verifies the latest review is APPROVE (and required CI is green), then squash-merges and deletes the branch. It makes no code changes.

**Hard rules:**
- NEVER merge before the automated review approves — no exceptions
- NEVER dismiss review findings — fix them or document with evidence why they are false alarms
- NEVER commit directly to main
- All tests must pass (`make test`) before each push
- All lints must pass (`make lint`) before each push
- If the review cycle exceeds 3 iterations, step back and reassess the approach — something is wrong
