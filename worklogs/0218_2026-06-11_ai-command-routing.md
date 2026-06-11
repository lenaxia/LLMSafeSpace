# Worklog: AI Multi-Command Workflow Routing

**Date:** 2026-06-11
**Session:** Expand single /ai command into 10-command routing system with core rules
**Status:** Complete

---

## Objective

Expand the AI assistant GitHub workflow from a single `/ai` command to a full routing system with purpose-specific commands, core rules enforced on every prompt, and a consistent review-iterate-approve-merge pattern for code changes.

---

## Work Completed

### New Commands (10 total)
- `/review [text]` — explicit PR code review
- `/fix <desc>` — bug fix with TDD and iterate-until-approve workflow
- `/implement <desc>` — feature/story implementation
- `/test <target>` — test writing/improvement
- `/security [text]` — security-focused review
- `/analyze [text]` — deep read-only analysis
- `/explain <topic>` — code/architecture explanation
- `/triage [text]` — issue triage
- `/help` — command reference
- `/ai` — preserved original behavior (backward compatible)

### New Files (11)
- `core-rules.md` — TDD, assumption validation, red flag words, SOLID, 8 quality dimensions, type safety, zero debt
- `code-change-workflow.md` — branch→PR→auto-review→fix→push→re-review→approve→merge
- `commands-footer.md` — appended to every AI response
- `help.md` — full command reference for /help
- 7 command templates: fix.md, implement.md, test.md, security.md, analyze.md, explain.md, triage.md

### Modified Files (4)
- `ai-comment.yml` — rewired with 10-command case routing and custom text extraction
- `pr-review.yml` — added core-rules.md + commands-footer.md
- `issue-opened.yml` — added core-rules.md + commands-footer.md
- `context.md` — updated command reference

### Prompt Structure (every response)
```
context.md → core-rules.md → <command>.md → [code-change-workflow.md] → [custom text] → commands-footer.md
```

---

## Assumptions

1. **Assumed:** The opencode GitHub action appends PR/issue context automatically.
   **Validated:** Confirmed from existing ai-comment.yml comments (line 48-50) and opencode documentation.

2. **Assumed:** `/ai` backward compatibility is sufficient (no migration needed).
   **Validated:** The `if` condition in ai-comment.yml now matches all commands, but `/ai` routing is identical to original.

3. **Assumed:** Read-only commands should not include code-change-workflow.md.
   **Validated:** `/review`, `/analyze`, `/explain`, `/triage`, `/help` produce comments only — no branch/PR creation.

---

## Tests Run

- 267-assertion shell test harness validating:
  - Correct routing for all 10 commands
  - Custom text extraction and preservation
  - Inline command detection
  - Code-change-workflow only on code commands
  - Core rules present in every prompt
  - Correct prompt ordering (context < core-rules < command)
  - Footer on every response
  - Context header on every response
- `make repolint` passes locally (216 worklogs, all contiguous)
- All 3 workflow YAML files validated with Python yaml.safe_load

---

## Next Steps

- Merge PR #100 after review approval
- Monitor first live usage of new commands
- Consider extracting routing logic into standalone testable script (bats/shunit2)

---

## Files Modified

- .github/prompts/analyze.md (new)
- .github/prompts/code-change-workflow.md (new)
- .github/prompts/commands-footer.md (new)
- .github/prompts/core-rules.md (new)
- .github/prompts/explain.md (new)
- .github/prompts/commands-footer.md (new)
- .github/prompts/fix.md (new)
- .github/prompts/help.md (new)
- .github/prompts/implement.md (new)
- .github/prompts/security.md (new)
- .github/prompts/test.md (new)
- .github/prompts/triage.md (new)
- .github/prompts/context.md (modified)
- .github/workflows/ai-comment.yml (modified)
- .github/workflows/issue-opened.yml (modified)
- .github/workflows/pr-review.yml (modified)
- worklogs/0218_2026-06-11_ai-command-routing.md (new)
