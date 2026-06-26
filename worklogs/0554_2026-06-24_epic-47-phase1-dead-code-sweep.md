# Worklog: Epic 47 Phase 1 — Dead Code Sweep + Silent Failure Removal

**Date:** 2026-06-24
**Session:** Implemented US-47.1, US-47.2, US-47.3 — removed dead frontend code and silent failures as the prerequisite phase for Epic 47 (Frontend Architecture Consolidation).
**Status:** Complete

---

## Objective

Phase 1 of Epic 47: remove dead code and silent failures before touching the busy-state and loading-primitive consolidation in later phases. The dead-code sweep shrinks every later diff by removing misleading parallel implementations.

---

## Work Completed

### US-47.1: Dead-code sweep

Deleted 15 files and several dead API methods/hooks. Every deletion verified against the codebase via grep (Rule 7) before removal — not just trusting the story doc.

**Files deleted:**
- `lib/stream.ts` + tests — HTTP-streaming parser orphaned by the SSE move
- `components/settings/AdminCredentialsTab.tsx` + test — backend table dropped in migration 000015
- `api/events.ts` + test — BroadcastChannel SSE client never wired
- `components/session/SessionItem.tsx`, `SessionList.tsx` + tests — superseded by Sidebar's tree view
- `components/workspace/WorkspaceSessionList.tsx` + test — zero production importers
- `hooks/useSessions.ts` + test — only a stale comment reference remained

**Dead API methods removed:**
- `workspacesApi.getActiveSessions` — zero callers
- `workspacesApi.getWorkspaceSessions` — zero callers, wrong-typed (`WorkspaceListItem[]` for a sessions endpoint)
- `ActiveSessionsResponse` + `WorkspaceSessionItem` types — only used by deleted methods
- `useWorkspaces` (list hook) — zero callers; `useWorkspaceStatus` from the same file preserved

**Story correction (Rule 7 validation caught a wrong claim):**
The story doc flagged `ensureSession` + `EnsureSessionResponse` as dead duplicates of `sessions.ts:create`. Verified false: `Sidebar.tsx:109` actively calls `workspacesApi.ensureSession`. Both hit `/workspaces/:id/sessions/new` but `ensureSession` is the one in use. Retained it and documented the finding. This is exactly why Rule 7 (validate, don't trust the doc) matters.

**Test cleanup:**
- `useChatStream.test.ts` — removed the `import * as eventsApi from "../api/events"` and the test asserting `registerTabCloseAbort` is undefined (the file is deleted, the test is meaningless)
- `useWorkspaces.test.tsx` — removed the `useWorkspaces` (list) test block; kept `useWorkspaceStatus` tests
- `contract.test.ts` + `contract-fixtures.json` — removed `ActiveSessionsResponse` fixture and test (type deleted)

### US-47.2: Remove per-workspace autoSuspend UI (silent failure)

`WorkspaceSettingsDrawer.tsx` collected an autoSuspend toggle + idle-timeout input, showed a warning about compute costs, and wired `onSave` to **nothing** (`Sidebar.tsx:444` — `onSave={async () => {}}`). The settings were silently discarded.

- Removed the `WorkspaceSettings` interface, autoSuspend/idleMinutes state, the autoSuspend form section, and the `onSave` prop entirely
- Updated `Sidebar.tsx` to remove the `onSave={async () => {}}` wiring
- Preserved the secret-bindings section (the working part of the drawer — persists via `api.put`)
- Rewrote `WorkspaceSettingsDrawer.test.tsx` to reflect the new surface (removed autoSuspend tests, added a test asserting autoSuspend controls are absent, fixed save-error and saving-state tests to toggle a binding first since `api.put` only fires when bindings changed)

### US-47.3: Remove dead AbortController from useChatStream

`useChatStream.ts` created an `AbortController`, stored it in a ref, and exposed `abort()` — but the controller's `signal` was **never passed to `sendAsync`**. The POST completed regardless. The real stop mechanism is `workspacesApi.abortSession` (server-side, called separately in `ChatPage.tsx`).

- Removed `abortRef`, the `new AbortController()` allocation, the `abort` callback, and the `abort` export
- Updated `ChatPage.tsx` to remove `abort()` from the `onAbort` handler (the handler still calls `abortSession` — the real mechanism)
- Removed the "abort() is a no-op when not streaming" test from `useChatStream.test.ts`

---

## Key Decisions

| Decision | Rationale |
|----------|-----------|
| **Retained `ensureSession` despite story flagging it as dead** | Rule 7 validation: `Sidebar.tsx:109` actively uses it. The story was wrong. Removing it would break session creation. |
| **Rewrote `WorkspaceSettingsDrawer.test.tsx` rather than patching** | The old tests were tightly coupled to the autoSuspend UI and `onSave` prop. A clean rewrite matching the new surface (secrets-only) is clearer than surgical edits. |
| **Save-error/saving-state tests now toggle a binding first** | After removing `onSave`, `api.put` is the only save path, and it only fires when `bindingsChanged` is true. Tests must create a change to exercise the save path. |
| **No worklog written before commit** | Corrected in this entry. |

---

## Blockers

None.

---

## Tests Run

| Command | Outcome |
|---------|---------|
| `npx tsc --noEmit` | Clean |
| `npm run lint` | Clean |
| `npx vitest run` | 1178 tests pass (109 files) — down from 1277 because 8 test files were deleted with their dead sources |
| `npm run build` | Clean |

---

## Next Steps

1. **Push this branch** (`fix/epic-47-phase1-dead-code-silent-failures`) + `feat/pending-input-state-facet` (Epic 55) from an authenticated environment.
2. **US-47.5 (Phase 3):** Unify server-busy state source — eliminate the dual `serverBusy`/`sseHasDrivenBusy` in ChatPage, consolidate into `SessionActivityProvider.busySessions`. This continues the ownership-split pattern Epic 55 started. Medium risk (most-tested area).
3. **US-47.8/47.9 (Phase 4):** Gate wsLog + remove per-message `useNow` + fix queryCache hot path. Lower risk, performance-focused.

---

## Files Modified

### Deleted (15 files)
- `frontend/src/lib/stream.ts`, `stream.test.ts`, `stream.edge.test.ts`
- `frontend/src/components/settings/AdminCredentialsTab.tsx`, `AdminCredentialsTab.test.tsx`
- `frontend/src/api/events.ts`, `events.test.ts`
- `frontend/src/components/session/SessionItem.tsx`, `SessionItem.test.tsx`
- `frontend/src/components/session/SessionList.tsx`, `SessionList.test.tsx`
- `frontend/src/components/workspace/WorkspaceSessionList.tsx`, `WorkspaceSessionList.test.tsx`
- `frontend/src/hooks/useSessions.ts`, `useSessions.test.tsx`

### Modified
- `frontend/src/api/workspaces.ts` — removed `getActiveSessions`, `getWorkspaceSessions`, `ActiveSessionsResponse` import
- `frontend/src/api/types.ts` — removed `ActiveSessionsResponse`, `WorkspaceSessionItem` interfaces
- `frontend/src/api/contract.test.ts` — removed `ActiveSessionsResponse` test
- `frontend/src/api/contract-fixtures.json` — removed `ActiveSessionsResponse` fixture
- `frontend/src/hooks/useWorkspaces.ts` — removed dead `useWorkspaces` (list) function
- `frontend/src/hooks/useWorkspaces.test.tsx` — removed `useWorkspaces` test block
- `frontend/src/hooks/useChatStream.ts` — removed `abortRef`, `abort` callback, `abort` export
- `frontend/src/hooks/useChatStream.test.ts` — removed `eventsApi` import, `registerTabCloseAbort` test, `abort()` no-op test
- `frontend/src/components/workspace/WorkspaceSettingsDrawer.tsx` — removed autoSuspend UI, `onSave` prop, `WorkspaceSettings` interface
- `frontend/src/components/workspace/WorkspaceSettingsDrawer.test.tsx` — rewrote for new surface
- `frontend/src/components/layout/Sidebar.tsx` — removed `onSave={async () => {}}` from drawer usage
- `frontend/src/pages/ChatPage.tsx` — removed `abort()` call from onAbort, removed `abort` from useChatStream destructure
