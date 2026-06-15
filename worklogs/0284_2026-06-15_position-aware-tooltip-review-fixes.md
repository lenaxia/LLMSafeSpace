# Worklog: Position-Aware Tooltip Component — Address Review Feedback

**Date:** 2026-06-15
**Session:** Address 3 blocking review items on PR #128, rebase on latest main, verify all tests pass
**Status:** Complete

---

## Objective

PR #128 ("fix(ui): replace all ad-hoc tooltips with shared position-aware Tooltip component") had
three blocking review items from the AI reviewer that had not been addressed across three review
passes. The goal was to fix all three, rebase on current main, and prepare for re-review.

---

## Work Completed

### Blocking Item 1: Missing Tooltip.test.tsx (TDD violation)

Created `frontend/src/components/ui/Tooltip.test.tsx` with 10 tests:
- Renders children (string and JSX content)
- Tooltip hidden before hover
- Tooltip visible on hover (userEvent + waitFor, Radix async delay)
- Tooltip hidden on Escape key
- JSX content rendering
- Disabled state renders children bare, no tooltip on hover
- Button and span children rendering
- Tooltip visible on focus

### Blocking Item 2: DiskUsageBar.test.tsx tests broken by Radix async behavior

Updated `frontend/src/components/workspace/DiskUsageBar.test.tsx`:
- Switched from `fireEvent.mouseEnter/mouseLeave` to `userEvent.hover()` + `waitFor`
- Removed the click-toggle test (Radix tooltips are hover/focus-based, no click-toggle)
- Used `getAllByText` instead of `getByText` (Radix renders content twice: visible + hidden aria-describedby)
- Used Escape key to test tooltip dismissal (unhover unreliable in JSDOM with Radix pointer events)

### Blocking Item 3: Undocumented content change

The tooltip text changed from "LiteLLM model config" to "provider model config". This is
intentional: the project's V2 architecture uses a provider model configuration system
(`agent-config.json`), not LiteLLM directly. The term "provider model config" is accurate
and aligns with the relay config subsystem documented in README-LLM.md.

### Supporting fix: TooltipProvider in test wrappers

Added `TooltipProvider` to:
- `frontend/src/test/utils.tsx` AllProviders wrapper (delayDuration=0 for tests)
- 10 ChatPage test files that use custom render wrappers importing from `@testing-library/react`
  directly (not from test/utils)

### Rebase on main

Rebased on latest main (2ef8d1cf). Discarded obsolete CI-fix commits (worklog renumbering,
gitleaks allowlist) that are no longer needed — main has its own worklog numbering and gitleaks
config.

---

## Key Decisions

1. **delayDuration=0 in tests** — Radix's default 300ms delay makes tests slow and unreliable.
   Setting delayDuration=0 in the test TooltipProvider makes hover interactions synchronous
   enough for waitFor-based assertions.

2. **Escape key for dismissal tests** — `userEvent.unhover()` doesn't reliably close Radix
   tooltips in JSDOM because Radix uses pointer events with internal timing. Escape key
   closes the tooltip immediately and deterministically.

3. **getAllByText for Radix content** — Radix renders tooltip content twice: once in the
   visible portal `Content` and once in a visually-hidden `<span role="tooltip">` for
   aria-describedby. `getByText` fails on duplicate matches.

---

## Blockers

None.

---

## Tests Run

- `npx vitest run src/components/ui/Tooltip.test.tsx src/components/workspace/DiskUsageBar.test.tsx`
  — 22 passed
- `npx vitest run` (full frontend suite) — 999 passed, 96 test files
- `npx tsc --noEmit` — clean

---

## Next Steps

- Push and trigger `/review` on PR #128
- Address any new feedback from the reviewer
- Merge once approved

---

## Files Modified

- `frontend/src/components/ui/Tooltip.tsx` — (from original commit, unchanged in this session)
- `frontend/src/components/ui/Tooltip.test.tsx` — new, 10 tests
- `frontend/src/components/workspace/DiskUsageBar.test.tsx` — updated for Radix async behavior
- `frontend/src/test/utils.tsx` — added TooltipProvider to AllProviders
- `frontend/src/pages/ChatPage.activate.test.tsx` — added TooltipProvider to wrapper
- `frontend/src/pages/ChatPage.autorename.test.tsx` — added TooltipProvider to wrapper
- `frontend/src/pages/ChatPage.context.test.tsx` — added TooltipProvider to wrapper
- `frontend/src/pages/ChatPage.navigate.test.tsx` — added TooltipProvider to wrapper
- `frontend/src/pages/ChatPage.queue.test.tsx` — added TooltipProvider to wrapper
- `frontend/src/pages/ChatPage.test.tsx` — added TooltipProvider to wrapper
- `frontend/src/pages/ChatPage.hookcount.test.tsx` — added TooltipProvider to wrapper
- `frontend/src/pages/ChatPage.input.test.tsx` — added TooltipProvider to wrapper
- `frontend/src/pages/ChatPage.reconnect.test.tsx` — added TooltipProvider to wrapper
- `frontend/src/pages/ChatPage.sse.test.tsx` — added TooltipProvider to wrapper
- `worklogs/0284_2026-06-15_position-aware-tooltip-review-fixes.md` — this worklog
