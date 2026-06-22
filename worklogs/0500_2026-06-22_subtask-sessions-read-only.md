# Worklog: Subtask sessions are read-only in chat UI

**Date:** 2026-06-22
**Session:** Make subagent/subtask sessions view-only in the frontend to help enforce max active sessions
**Status:** Complete

---

## Objective

In the frontend, subagent/subtask sessions (spawned by the opencode `task` tool) could be chatted with directly. This circumvents the workspace's max-active-sessions limit because chatting in a subtask drives an additional active session. The goal: make subtask sessions read-only — disable/hide the chat text box and replace it with a "view only" message.

---

## Work Completed

### New `ReadOnlyBanner` component
- `frontend/src/components/chat/ReadOnlyBanner.tsx` — renders a `role="status"` banner with a lock icon and a message. Default message: "Subtasks are view-only. Continue the conversation in the parent session." Accepts an optional `message` prop. No interactive controls.
- `frontend/src/components/chat/ReadOnlyBanner.test.tsx` — 4 tests: default message, custom message, status landmark, no interactive controls.

### `ChatView` gains a `viewOnly` mode
- Added optional `viewOnly` and `viewOnlyMessage` props.
- When `viewOnly` is true, the `<Composer>` and `<QueueSection>` are not rendered; `<ReadOnlyBanner>` is rendered in their place instead. The message history (`MessageList`) still renders so the subtask transcript remains fully viewable.
- `frontend/src/components/chat/ChatView.test.tsx` — 6 new tests covering: composer hidden, default message shown, custom message shown, queue section suppressed, messages still visible, and composer present when `viewOnly=false`.

### `ChatPage` derives subtask state and passes it through
- `frontend/src/pages/ChatPage.tsx` — added `const isSubtask = !!activeSessionData?.parentId;` (`activeSessionData` is the `SessionListItem` for the current session, already derived from the `["sessions", workspaceId]` query). Passed `viewOnly={isSubtask}` to `<ChatView>`.
- `frontend/src/pages/ChatPage.subtask.test.tsx` — 2 integration tests: composer hidden + banner shown for a subtask (`parentId` set); composer present for a top-level session (no `parentId`).

---

## Key Decisions

1. **Subtask classification via truthy `parentId`.** A session is a subtask iff it has a non-empty `parentId`. This is **exactly consistent** with the Sidebar's `buildSessionTree` (`frontend/src/lib/sessionTree.ts:54` classifies roots via `!s.parentId`). Orphaned subtasks (parentId points at a deleted/missing parent) are also treated as read-only, which is correct — they are still subtasks, just orphaned.

2. **Hide, not disable.** The user asked to "disable/hide the text box and replace with a message." Replacing the composer wholesale (rather than disabling the textarea) is clearer UX and also removes the message queue, which has no purpose in a read-only view.

3. **Scope: frontend-only.** The user explicitly scoped this to the frontend ("Currently in the frontend, sub tasks can be chatted with as well"). The phrase "help better enforce max sessions" indicates this is a UX guardrail that complements (not replaces) backend enforcement. Backend max-active-sessions enforcement (the queue / at-cap mechanism) is unchanged and remains the authoritative gate.

---

## Assumptions (stated and validated — Rule 7)

1. **A session with a non-empty `parentId` is a subtask.** Validated: `SessionListItem.parentId` (`frontend/src/api/types.ts:68`); `Sidebar.hierarchy.test.tsx` nests children by `parentId`; `QuestionRequest.root_session_id` comment (`types.ts:230-231`) confirms subagent/subtask sessions have a parent ancestor.
2. **`activeSessionData` reliably reflects the current session's `parentId`.** Validated: `ChatPage.tsx:138` derives it from `sessionsListData?.find((s) => s.id === sessionId)`, sourced from `workspacesApi.getSessions` → `SessionListItem[]` (`frontend/src/api/workspaces.ts:54`).
3. **The Composer is the only chat text input.** Validated: `<Composer>` is used only in `ChatView.tsx:97`; `<ChatView>` is used only in `ChatPage.tsx:995`. Exactly one chat entry point.

---

## Blockers

None.

---

## Adversarial Self-Review (Rule 11)

- **Race window before sessions list loads** — FALSE ALARM. `<ChatView>` is only rendered after the `!historyLoading && !createSessionMutation.isPending` gate (`ChatPage.tsx:969`). The sessions query fires on the same mount, so by the time the composer would render, `activeSessionData` is populated. Additionally a session's `parentId` is immutable, so staleness (`staleTime: Infinity` on the sessions query) cannot affect the classification.
- **Backend bypass** — REAL but out of scope. A determined client could still POST to the session send API directly. This is a frontend UX guardrail per the explicit request; backend max-sessions enforcement is unchanged.
- **Question/Permission prompts in subtask view** — POTENTIAL follow-up. Agent-driven prompts for a subtask's `session_id` would still render in the subtask view (and also bubble up to the parent via `root_session_id`, where they are answerable). This is technically interactive but is agent-driven, not user-initiated chatting, and is beyond the explicit "text box" scope. **Decision: surface prompts in both views** (subtask + parent); robust cross-view dismissal already works via the `agent.question.resolved` / `agent.permission.resolved` SSE events (cleared by `request_id`, no session gate). Tracked separately: a pre-existing within-tab navigation state-loss bug for prompt **content** is filed as #346.

---

## Tests Run

- `npx vitest run src/components/chat/ReadOnlyBanner.test.tsx src/components/chat/ChatView.test.tsx src/pages/ChatPage.subtask.test.tsx` — 24 passed.
- `npm test` (full suite) — 1197 passed across 114 files.
- `npm run typecheck` (`tsc --noEmit`) — clean.
- `npm run lint` (`eslint .`) — clean, zero warnings.

---

## Next Steps

- **Decision needed:** whether to also suppress agent question/permission prompts in the subtask view (they currently render but are answerable from the parent). Recommend suppressing them in view-only mode for a fully consistent read-only experience.
- Backend hardening (optional, defence-in-depth): reject send/queue requests whose target session has a `parentId`, so the max-sessions guarantee does not rely solely on the frontend.

---

## Files Modified

- `frontend/src/components/chat/ReadOnlyBanner.tsx` (new)
- `frontend/src/components/chat/ReadOnlyBanner.test.tsx` (new)
- `frontend/src/components/chat/ChatView.tsx` (added `viewOnly` / `viewOnlyMessage` props; conditional render)
- `frontend/src/components/chat/ChatView.test.tsx` (6 new tests)
- `frontend/src/pages/ChatPage.tsx` (derive `isSubtask`, pass `viewOnly`)
- `frontend/src/pages/ChatPage.subtask.test.tsx` (new — 2 integration tests)
- `worklogs/0500_2026-06-22_subtask-sessions-read-only.md` (new)
