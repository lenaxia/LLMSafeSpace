## AI Assistant Commands

The following commands are available on this issue/PR thread. Reply with one to trigger the assistant — any text after a command tunes the request (e.g. `/review focus on the auth flow`).

| Command | Description |
|---|---|
| `/ai [text]` | Re-assess this issue/PR in full, or address a specific request (context-dependent). |
| `/review [text]` | Explicit code review of the current PR. Append text to focus on specific areas. |
| `/fix <description>` | Fix a bug: creates a branch, writes TDD regression tests, opens a PR, iterates through review until approved, then merges. |
| `/implement <description>` | Implement a feature/story: TDD, opens a PR, iterates through review until approved, then merges. |
| `/test <target>` | Write or improve tests for the specified code: TDD, opens a PR, iterates through review until approved. |
| `/analyze [text]` | Deep read-only analysis. Posts findings as a comment. No code changes. |
| `/explain <topic>` | Explain code, architecture, or data flow. Posts explanation as a comment. No code changes. |
| `/security [text]` | Security-focused review against `design/SECURITY.md`. |
| `/triage [text]` | Triage this issue — categorize, prioritize, assess impact, suggest labels. |
| `/design [text]` | Iterate on a design doc under `design/` before implementing/fixing. Opens a PR, iterates through review, then **holds** (never auto-merges). |
| `/merge` | Explicitly merge an approved PR (squash). Use after `/design` or a `--no-merge` run. |
| `/help` | Show the full command reference. |

**All commands are available to repository owners, members, and collaborators.** Code-change commands (`/fix`, `/implement`, `/test`, `/security`) auto-merge after approval by default — append `--no-merge` to hold for an explicit `/merge`. `/design` always holds. None of these ever commit to `main` directly.
