# Worklog 0233 — Epic 36: Context Bar Always Visible (omitempty Fix)

**Date:** 2026-06-12
**Session:** Fix three interacting bugs preventing the context usage bar from ever rendering
**Status:** Complete

---

## Objective

The context usage bar graph (Epic 36) never appeared on any workspace — not on new sessions, not on long-running sessions. Diagnose and fix.

---

## Root Cause Analysis

Three bugs, each independently sufficient to hide the bar:

### Bug 1: `omitempty` on `ContextUsed int64`
Go's `omitempty` silently drops zero-valued int64 fields from JSON. When `ContextUsed=0` (new session, no LLM calls yet), the field was absent from JSON. TypeScript read `undefined`. `DiskUsageBar` checked `if (contextUsed != null)` — `undefined != null` is `false` in JS loose equality, so the bar never rendered.

**Files:** `pkg/agentd/types.go:67`, `pkg/apis/llmsafespace/v1/workspace_types.go:230`, `pkg/types/types.go:487`
**Fix:** Removed `omitempty` from all three.

### Bug 2: `if pt > 0` guard in buildStatuszHandler
`cmd/workspace-agentd/main.go:699` only set `sessions[i].ContextUsed` when `tracker.getPromptTokens(s.ID) > 0`. Cold-start sessions with no SSE data never got the field.

**Fix:** Always set unconditionally: `sessions[i].ContextUsed = tracker.getPromptTokens(s.ID)`

### Bug 3: No fallback in ChatPage
`ChatPage.tsx:789-790` passed `sessionStatus?.contextUsed` and `status?.contextTotal` without fallback. Undefined propagated to DiskUsageBar.

**Fix:** Added `?? 0` fallback.

---

## PR Review Feedback (Round 1)

Reviewer: github-actions[bot] — CHANGES_REQUESTED

### Required changes:
1. **JSON wire-format test**: All Go tests unmarshalled via `json.Unmarshal` into structs, which sets absent int64 fields to 0. Tests would pass even if `omitempty` were re-added. Added `assert.Contains(t, w.Body.String(), "\"contextUsed\":0", ...)` to `TestBuildStatuszHandler_NoContextUsed_WhenTrackerEmpty`.
2. **Inaccurate PR body/commit message**: Original claimed "replaced 4 hand-rolled inline handler test copies" and listed phantom files. Corrected to reflect only what the commit actually changes.
3. **Missing worklog**: This entry.

---

## Work Completed

- Removed `omitempty` from `ContextUsed` in 3 Go type files
- Removed `> 0` guard from `buildStatuszHandler`
- Added `?? 0` fallback in ChatPage
- Added JSON wire-format regression test
- Updated ChatPage.context.test.tsx (bar shows for zero contextUsed)
- Fixed commit message and PR body per review feedback
- Rebased onto latest main (resolved 4 merge conflicts)

---

## Tests Run

| Package | Tests | Result |
|---|---|---|
| `cmd/workspace-agentd` | ContextUsage + buildStatuszHandler | ✅ Pass |
| `controller/internal/workspace` | ContextUsed | ✅ Pass |
| `api/internal/services/workspace` | ContextUsed | ✅ Pass |
| Frontend (vitest) | Full suite (847 tests) | ✅ Pass |
| Go build | Full build | ✅ Pass |

---

## Files Modified

- `cmd/workspace-agentd/main.go` — remove `> 0` guard
- `cmd/workspace-agentd/main_test.go` — add JSON wire-format assertion
- `pkg/agentd/types.go` — remove `omitempty`
- `pkg/apis/llmsafespace/v1/workspace_types.go` — remove `omitempty`
- `pkg/types/types.go` — remove `omitempty`
- `frontend/src/pages/ChatPage.tsx` — add `?? 0` fallback
- `frontend/src/pages/ChatPage.context.test.tsx` — update test for zero contextUsed
