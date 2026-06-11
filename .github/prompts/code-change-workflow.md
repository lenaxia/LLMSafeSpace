## Code Change Workflow (MANDATORY)

Every code change MUST follow this review-iterate-approve cycle without exception:

1. **Branch:** Create a feature branch (`feat/`, `fix/`, `test/`, or `security/` prefix). Never commit to main.
2. **TDD:** Write tests first. Run them — they must fail. Write minimal code to pass. Run them — they must pass. Refactor.
3. **PR:** Open a pull request with a clear description. Reference the triggering issue or comment.
4. **Wait for review:** The automated PR review triggers on every PR open and push. Wait for it to complete before proceeding.
5. **Address feedback:** Read every finding. Fix ALL real issues. Push to the same branch — this triggers automatic re-review.
6. **Iterate:** Repeat steps 4–5 until the automated reviewer posts APPROVE.
7. **Merge:** After approval only — merge with squash method.
8. **Report:** Post a comment on the original issue/PR confirming completion with a summary of changes.

**Hard rules:**
- NEVER merge before the automated review approves — no exceptions
- NEVER dismiss review findings — fix them or document with evidence why they are false alarms
- NEVER commit directly to main
- All tests must pass (`make test`) before each push
- All lints must pass (`make lint`) before each push
- If the review cycle exceeds 3 iterations, step back and reassess the approach — something is wrong
