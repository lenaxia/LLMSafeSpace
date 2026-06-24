## fix(epic-47): phase 1 — dead code sweep + silent failure removal

### Problem

The frontend accumulated dead code, silent failures, and parallel implementations across LLM-assisted development sessions. This PR is Phase 1 of Epic 47 (Frontend Architecture Consolidation) — zero-risk removals that shrink every later diff.

### Changes

**US-47.1 — Dead-code sweep (-2428 lines):**
Deleted 15 files with zero production importers (verified via grep before deletion):
- `lib/stream.ts` — HTTP-streaming parser orphaned by the SSE move
- `components/settings/AdminCredentialsTab.tsx` — backend table dropped in migration 000015
- `api/events.ts` — BroadcastChannel SSE client never wired
- `components/session/SessionItem.tsx` + `SessionList.tsx` — superseded by Sidebar tree
- `components/workspace/WorkspaceSessionList.tsx` — zero importers
- `hooks/useSessions.ts` — only a stale comment reference remained

Dead API methods removed: `getActiveSessions`, `getWorkspaceSessions`, `useWorkspaces` (list hook). Types `ActiveSessionsResponse` and `WorkspaceSessionItem` removed.

**Story correction:** The story flagged `ensureSession` as dead, but Rule 7 validation caught it's actively used by `Sidebar.tsx:109`. Retained.

**US-47.2 — Remove silent-failure autoSuspend UI:**
`WorkspaceSettingsDrawer` collected autoSuspend settings and wired `onSave` to `onSave={async () => {}}` — settings silently discarded. Removed the autoSuspend UI, `onSave` prop, and `WorkspaceSettings` interface. Secret bindings (the working part) preserved.

**US-47.3 — Remove dead AbortController:**
`useChatStream` created an `AbortController` whose `signal` was never passed to `sendAsync`. The real stop mechanism is `workspacesApi.abortSession` (server-side). Removed the dead controller, ref, and `abort` export.

### Verification

- 1178 tests pass (down from 1277 — 8 dead test files deleted with their sources)
- Typecheck, lint, build all clean

### Checklist

- [x] Every deletion verified via grep (zero production importers)
- [x] Worklog created (`worklogs/NNNN_2026-06-24_epic-47-phase1-dead-code-sweep.md`)
- [x] No regression in existing tests
- [ ] CI green (pending push)
