Post a comment on the issue or PR with the following content (and nothing else):

---

## AI Assistant Commands

The following commands are available in issue and PR comments:

| Command | Description | Custom Text |
|---|---|---|
| `/ai [text]` | General-purpose — context-dependent. On a PR: full re-review. On an issue: analyze and respond. With text: address the specific request. | Optional |
| `/review [text]` | Explicit code review of the current PR. Append text to focus the review on specific areas. | Optional |
| `/fix <description>` | Fix a specific bug or issue. Creates a branch, writes regression tests (TDD), opens a PR, iterates through automated review until approved, then merges. | Required |
| `/implement <description>` | Implement a feature or user story following TDD and the multi-agent workflow. Creates a branch, opens a PR, iterates through review until approved. | Required |
| `/test <target>` | Write or improve tests for specified code. Follows TDD requirements from README-LLM.md. Creates a branch, opens a PR, iterates through review. | Required |
| `/analyze [text]` | Deep read-only analysis. Posts findings as a comment. No code changes. | Optional |
| `/explain <topic>` | Explain code, architecture, or data flow. Posts explanation as a comment. No code changes. | Required |
| `/security [text]` | Security-focused review against design/SECURITY.md. Checks secrets, RBAC, CRD schemas, input validation, redaction. Fixes findings if code changes are warranted. | Optional |
| `/triage [text]` | Triage an issue — categorize, prioritize, assess impact, suggest labels and related items. Posts assessment as a comment. | Optional |
| `/help` | Show this command reference. | — |

**All commands are available to repository owners, members, and collaborators.**

**Code change commands** (`/fix`, `/implement`, `/test`, `/security`) **follow the review-iterate-approve workflow:**
1. Create feature branch
2. Open PR
3. Automated review triggers
4. Fix findings and push (re-review triggers)
5. Repeat until approved
6. Merge with squash
