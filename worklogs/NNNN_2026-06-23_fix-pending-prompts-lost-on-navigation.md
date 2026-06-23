# Worklog: Fix #346 — pending prompts lost on within-tab session navigation

**Date:** 2026-06-23
**Session:** Drive backlog item #346 (frontend bug); picked as the highest-value unblocked issue
**Status:** Complete

---

## Objective

Fix #346: pending agent question/permission prompts vanished from the chat view
when navigating between a parent session and its subtask within one browser tab
(changing `:sessionId` via the sidebar). The sidebar pulse survived (global
layer) but the prompt content did not, leaving a confusing orphaned indicator.

---

## Work Completed

### Root cause

Pending prompt **content** (question/permission bodies) lived in `ChatPage`
session-local state (`useState`), while the **indicator** (which session has a
pending prompt) already lived globally in `SessionActivityProvider`. The
`:sessionId` `useEffect` cleared the local state on every session switch — so
navigating away and back lost the content until a page refresh / SSE reconnect.

### Fix — single source of truth

Lifted prompt **content** into `SessionActivityProvider`, keyed by `requestId`,
filtered by session at read time. This matches the existing indicator layer and
the SSE replay mechanism (`emitPendingInputRequests`).

**`frontend/src/providers/SessionActivityProvider.tsx`**
- New content maps: `pendingQuestionContent`, `pendingPermissionContent`
  (keyed by requestId).
- New context methods + hooks: `addPendingQuestion`, `addPendingPermission`,
  `pendingQuestionsForSession`, `pendingPermissionsForSession`,
  `clearSessionPendingPrompts`.
- `removePendingAction` now also clears content (resolved events drop the
  in-chat prompt). `clearWorkspacePendingActions` prunes content for the
  workspace's sessions in lockstep with the indicator.
- Read-time filter matches both the owning session and the root session
  (`root_session_id`), so subtask prompts still bubble to the parent view —
  the rule ChatPage previously applied at write time, now applied at read time
  so content is stored regardless of the viewed session.
- Added a `pruneMany` helper to avoid needless re-renders.

**`frontend/src/pages/ChatPage.tsx`**
- Removed session-local `pendingQuestions` / `pendingPermissions` state.
- Removed the clearing in the `:sessionId` effect (the bug source).
- SSE handlers now call `addPendingQuestion` / `addPendingPermission`
  (unconditional — store regardless of viewed session); resolved →
  `removePendingAction`.
- US-16.12 clears (session idle / session error) now call
  `clearSessionPendingPrompts(sessionId)` (global, session-scoped).
- Render reads from `usePendingQuestionsForSession` / `usePendingPermissionsForSession`;
  `onResolved` → `removePendingAction(id)`.
- The auto-abort-stuck-tool effect now keys on `pendingPromptCount` (a number)
  instead of the array identities, avoiding excess re-runs.

### Tests

- 6 new provider tests (`SessionActivityProvider.test.tsx`): store + filter by
  session; **per-session isolation with no clear-on-navigation** (the #346 fix
  load-bearing assertion); subtask→parent bubble via `root_session_id`;
  `removePendingAction` clears content; `clearSessionPendingPrompts` clears one
  session only; permission content path. TDD: written first, confirmed red,
  then implemented.
- `ChatPage.input.test.tsx`: switched to the **real** `SessionActivityProvider`
  (a stub mock can't drive the SSE → state → render chain). Verifies
  question/permission render + resolve end-to-end.
- `ChatPage.reconnect.test.tsx`: stateful prompt-store mock so the auto-abort
  guard (`pendingPromptCount > 0`) reflects questions delivered via SSE; reset
  between tests.
- 11 other ChatPage-test mocks: added no-op stubs for the 5 new hooks.

### Validation

- `tsc --noEmit` — pass.
- `vitest run` — **1259/1259 pass** (117 files).
- `eslint` (changed files) — clean.

---

## Key Decisions

- **Lift content, don't poll.** A REST `useQuery` for pending prompts would
  introduce a second source of truth racing with SSE (query returns pending
  moments after SSE already delivered `resolved` → stale prompt). The backend
  already replays pending prompts on SSE connect (`emitPendingInputRequests`),
  so lifting into the SSE-driven global layer keeps one source of truth.
- **Store unconditionally, filter at read.** Previously content was stored only
  if it matched the *current* session — so navigating away lost it. Now every
  prompt is stored (matching the indicator, which was already unconditional)
  and filtered by `root_session_id`/`session_id` at render.
- **Real provider in input test, stateful-store mock in reconnect test.** The
  input test verifies the full render chain (needs real state→re-render); the
  reconnect test only needs the auto-abort guard to see a non-zero count (a
  synchronous mutable store suffices and avoids wrapping ~6 inline renders).

---

## Blockers

None.

---

## Next Steps

- Optional: explicit `?sso=config_error` handling in `LoginPage.tsx` (carried
  from PR #383) — unrelated to this fix.
- The e2e (Playwright) suite runs in CI and will exercise the real navigation
  flow end-to-end; unit tests cover the provider logic and ChatPage wiring.

---

## Files Modified

- `frontend/src/providers/SessionActivityProvider.tsx` — content maps, new
  methods/hooks, `removePendingAction`/`clearWorkspacePendingActions` extended.
- `frontend/src/providers/SessionActivityProvider.test.tsx` — 6 new tests.
- `frontend/src/pages/ChatPage.tsx` — dropped local state + nav-clear; reads
  from provider; handlers + US-16.12 clears repointed.
- `frontend/src/pages/ChatPage.input.test.tsx` — real provider.
- `frontend/src/pages/ChatPage.reconnect.test.tsx` — stateful prompt-store mock.
- 11 other `ChatPage.*.test.tsx` / layout test mocks — added new-hook stubs.
- `worklogs/NNNN_2026-06-23_fix-pending-prompts-lost-on-navigation.md` — this file.
