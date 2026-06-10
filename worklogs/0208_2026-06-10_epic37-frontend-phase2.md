# Worklog: Epic 37 — Session Activity & Unread State UX (Frontend Phase 2 In Progress)

**Date:** 2026-06-10
**Session:** Frontend implementation — US-37.4 (SessionActivityProvider), US-37.5 (spinner), US-37.6 (pulsation), US-37.7 (divider), US-37.8 (mark-seen) — partial, in progress
**Status:** In Progress

---

## Objective

Implement Epic 37 Phase 2 frontend stories: real-time session activity context, spinner, unread pulsation, new-messages divider, mark-seen on navigate.

---

## Work Completed

### US-37.4: SessionActivityProvider (React Context)

**File created:** `frontend/src/providers/SessionActivityProvider.tsx`

- `SessionActivityProvider` wraps AppShell content; mounts `useUserEventStream` with an `onEvent` callback
- Tracks `busySessions: Map<sessionId, workspaceId>` and `pendingUnread: Set<sessionId>` in React state
- On `session.status busy` event: adds to `busySessions`, updates `["sessions", wsId]` query cache if it exists (D7 guard — no orphan entries for collapsed workspaces)
- On `session.status idle` event: removes from `busySessions`, adds to `pendingUnread` if not the currently-viewed session, updates query cache
- On `workspace.phase` event with non-Active phase: clears all `busySessions` + `pendingUnread` for that workspace (D8)
- Exported hooks: `useIsSessionBusy`, `useIsSessionUnread`, `useWorkspaceBusyCount`, `useClearPendingUnread`

**`useUserEventStream` modified:**
- Added optional `options?: { onEvent?: (event: unknown) => void }` parameter
- Stores callback in a ref (stable across renders)
- Invokes `onEventRef.current?.(data)` after all existing workspace.phase/resync processing
- No behaviour change when called without options (zero-risk to existing callers)

**AppShell modified:**
- Removed standalone `useUserEventStream()` call (now managed by SessionActivityProvider)
- Added `<SessionActivityProvider>` wrapper around content
- Added import for SessionActivityProvider

### US-37.5: Activity Spinner

**File modified:** `frontend/src/components/layout/Sidebar.tsx`

- `SessionTreeRow` now calls `useIsSessionBusy(s.id)` and `useIsSessionUnread(s.id)`
- Dead blue dot (`s.status === "active" && <span ...>`) replaced with `{isBusy && <Loader2 className="h-3 w-3 animate-spin text-blue-500 flex-shrink-0" />}`
- `WorkspaceGroup` now calls `useWorkspaceBusyCount(workspace.id)`
- Collapsed workspace shows spinner when `!expanded && busyCount > 0`

### US-37.6: Unread Pulsation

**File modified:** `frontend/src/styles/index.css`

Added:
```css
@keyframes unread-pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.55; } }
.animate-unread-pulse { animation: unread-pulse 2s ease-in-out infinite; }
@media (prefers-reduced-motion: reduce) { .animate-unread-pulse { animation: none; } }
```

**Sidebar:** `showPulse = isUnread && !isSelected && !isBusy` — applied to `MessageSquare` icon and title `<span>` via `cn(..., showPulse && "animate-unread-pulse")`

### US-37.7: New Messages Divider

**File modified:** `frontend/src/components/chat/MessageList.tsx`

- Added `lastSeenAt?: string` prop
- Added `CLOCK_SKEW_BUFFER_MS = 1000` constant
- `dividerIndex` computed via `useMemo`: finds first message where `createdAt > lastSeenAt - 1s`; returns -1 when `lastSeenAt` is null/undefined
- Render loop changed to use `<Fragment key={msg.id}>` — divider rendered before the first unseen message as `role="separator"` with "New messages" label

### US-37.8: Mark-seen on Navigate (in progress)

- `ChatPage.tsx` modifications to add `markSessionSeen` calls not yet applied — interrupted to write worklog

---

## Assumptions Validated

| # | Assumption | Evidence |
|---|-----------|----------|
| A13 | `useSyncExternalStore` not used in codebase | grep confirmed — plain `useState` Map/Set is fine at ~100 sessions max |
| A14 | `AppShell` mounts inside router, has access to `useParams` | `router.tsx:37`, `AppShell.tsx:22` |
| A2 (Sidebar) | Blue dot at `Sidebar.tsx:696-698` — dead because `database.go:803` hardcodes "idle" | Confirmed — now replaced |

---

## Adversarial Pre-Review Notes

- **D7 guard**: `queryClient.getQueryData(sessionsKey)` checked before `setQueryData` — no orphan cache entries for collapsed workspaces ✅
- **D8**: `workspace.phase` non-Active events clear both `busySessions` and `pendingUnread` for the workspace ✅
- **Stable ref for onEvent**: Uses `useRef` updated in `useEffect` — avoids re-creating the SSE connection on every render ✅
- **SessionActivityProvider uses `useParams`** — must be inside router. Mounted in AppShell which is inside router at `router.tsx:37` ✅
- **Divider clock skew**: 1-second buffer handles intra-cluster NTP drift ✅
- **`Fragment` key**: `key={msg.id}` on Fragment preserves React reconciliation ✅

---

## Outstanding

1. US-37.8: Complete `ChatPage.tsx` mark-seen on navigate implementation
2. Pass `lastSeenAt` through `ChatView` → `MessageList`
3. Write frontend tests (US-37.9)
4. Run `npm test` and verify all existing tests still pass
5. Adversarial self-review
6. Commit and push

---

## Blockers

None — build environment stable with `GOCACHE=/workspace/.gocache`.

---

## Tests Run

Backend (all pass, committed at `ad8e53e6`):
- `go test -timeout 120s ./... -count=1` — zero failures

Frontend: not yet run — pending US-37.8 completion.

---

## Next Steps

1. Complete `ChatPage.tsx` mark-seen effects (navigate-to immediate + navigate-away debounced)
2. Pass `lastSeenAt` from session query data through `ChatView` to `MessageList`
3. Write all frontend Vitest unit tests for US-37.4–37.8
4. Run `npm test` — fix any regressions
5. Adversarial self-review (Rule 11)
6. Commit everything

---

## Files Modified Since Last Commit (`ad8e53e6`)

### New Files
- `frontend/src/providers/SessionActivityProvider.tsx` — US-37.4

### Modified Files
- `frontend/src/hooks/useUserEventStream.ts` — onEvent callback
- `frontend/src/components/layout/AppShell.tsx` — SessionActivityProvider mount
- `frontend/src/components/layout/Sidebar.tsx` — spinner + pulsation
- `frontend/src/styles/index.css` — unread-pulse keyframes
- `frontend/src/components/chat/MessageList.tsx` — lastSeenAt prop + divider
