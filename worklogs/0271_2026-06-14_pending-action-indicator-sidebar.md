# Worklog: Pending Action Indicator in Sidebar Nav Panel

**Date:** 2026-06-14
**Session:** Add pulsing HelpCircle icon to sidebar sessions when agent has pending questions or permission requests
**Status:** Complete

---

## Objective

When an agent requests permissions or asks a question, the session in the nav panel should visually alert the user that action is needed. The indicator should pulse and show a HelpCircle icon, and bubble up from subtask sessions to their top-level parent.

---

## Work Completed

### SessionActivityProvider — pending action tracking

Added `pendingActions` state (`Map<sessionId, Set<requestId>>`) to track agent questions and permission requests per session. Multiple concurrent prompts are tracked independently via request IDs.

- `addPendingAction(workspaceId, sessionId, requestId)`: adds a request to the session's set. Also stores reverse mapping in `requestToSessionRef` for resolved events (which carry only `request_id`).
- `removePendingAction(requestId)`: uses the reverse mapping to find the correct session, removes the request. Cleans up the session entry when the set becomes empty.
- `clearWorkspacePendingActions(workspaceId)`: filters by workspace ID using `pendingActionWsRef` so only one workspace's sessions are cleared.
- `isSessionPendingAction(sessionId)`: boolean check.
- `pendingActionSessionIds`: derived `Set<string>` from the pending actions keys via `useMemo`.
- Exported hooks: `useIsSessionPendingAction`, `useAddPendingAction`, `useRemovePendingAction`, `useSessionPendingActions`.

Lifecycle clearing:
- Session idle (both current and non-current branches in SSE handler) clears pending actions for that session.
- Workspace phase non-active clears pending actions scoped to that workspace.

### ChatPage — push events to provider

The `handleSSEEvent` callback now calls `addPendingAction(workspaceId, sessionId, requestId)` on `agent.question` and `agent.permission` events, and `removePendingAction(requestId)` on `agent.question.resolved` and `agent.permission.resolved`.

### Sidebar — pending action indicator

**WorkspaceSessionList:**
- Reads `useSessionPendingActions()` to get the raw set of session IDs with pending actions.
- Computes `pendingIndicatorIds` via `useMemo` + recursive tree walk: any session whose subtree contains a pending action marks itself and all ancestors. This bubbles up from subtasks to the root parent.

**SessionTreeRow:**
- Accepts `pendingIndicatorIds: Set<string>` prop.
- `showPending = depth === 0 && pendingIndicatorIds.has(s.id)` — only top-level sessions show the indicator.
- Icon priority: busy (Loader2) > pending (HelpCircle, amber, pulsing) > unread (MessageSquareText, pulsing) > default (MessageSquare).
- Title span also pulses when `showPending` is true.

**OrphansGroup:** passes `pendingIndicatorIds` through to child `SessionTreeRow` calls.

---

## Key Decisions

1. **Request-to-session reverse mapping** instead of passing sessionId in removed events. Resolved events carry only `request_id`, so we need a lookup to find which session to decrement. Using a `useRef<Map>` avoids state coupling.

2. **Add-only bubble-up via tree walk.** The `pendingIndicatorIds` set is computed as containing ALL ancestors of any session with pending actions. This means the indicator always appears on the root session, regardless of how deep the pending session is. Depth gating restricts visual display to `depth === 0`.

3. **Workspace-scoped clearing.** `clearWorkspacePendingActions` uses `pendingActionWsRef` to track which workspace each session belongs to, so phase transitions don't clear pending actions from other workspaces.

4. **Icon priority: busy > pending > unread.** A session that is busy (processing) should show the spinner, not the pending indicator — the response is coming. Once idle, the pending indicator (if any) takes priority over unread.

---

## Blockers

None.

---

## Tests Run

- Frontend: `npm test` — 1010 tests pass (+5 new regression tests for pending action provider state, +3 sidebar UI tests)
- Frontend: `npm run typecheck` — clean
- Frontend: `npm run lint` — only pre-existing ChatPage issues
- Pre-commit: repolint ✓

### New regression tests
- `addPendingAction marks session as pending`
- `removePendingAction clears pending state`
- `multiple pending requests — session stays pending until last removed`
- `session idle clears pending actions`
- `useSessionPendingActions returns set of pending session IDs`
- Sidebar: `shows HelpCircle with pulse when session has pending action`
- Sidebar: `parent shows indicator when child has pending action (bubble-up)`
- Sidebar: `pending indicator shows even when session is unread`

---

## Next Steps

- Merge PR after AI review approval.
- Open question: verify opencode PATCH /session/:id endpoint (from previous PR) is functional — the rename-in-agent feature may need a different approach if unsupported.

---

## Files Modified

- `frontend/src/providers/SessionActivityProvider.tsx` — added pendingActions state, handlers, context, hooks
- `frontend/src/providers/SessionActivityProvider.test.tsx` — +5 tests, updated 3 for new signatures
- `frontend/src/components/layout/Sidebar.tsx` — HelpCircle icon, pendingIndicatorIds computation, showPending in SessionTreeRow
- `frontend/src/components/layout/Sidebar.test.tsx` — +3 pending indicator UI tests, mock updates
- `frontend/src/pages/ChatPage.tsx` — addPendingAction/removePendingAction calls in SSE handler
- `frontend/src/pages/ChatPage.*.test.tsx` (10 files) — mock updates for new hooks
- `frontend/src/components/layout/AppShell.test.tsx` — mock updates
