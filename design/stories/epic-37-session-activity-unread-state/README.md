# Epic 37: Session Activity & Unread State UX

**Status:** Planning
**Issue:** #37
**Depends On:** Epic 15 (streaming state — SSE-driven session status), Epic 28 (unified event stream — UserEventBroker)
**Estimated Effort:** ~18 hours

---

## Problem Statement

The sidebar treats every session as equivalent regardless of state. Users have three blind spots:

1. **No activity awareness across workspaces.** A session processing in workspace B gives no visual signal while the user is in workspace A. The user must manually check each workspace.

2. **No unread notification.** When a session finishes processing, there is no indication that a response is waiting. The user has to remember which sessions they kicked off.

3. **No "new since last visit" marker.** When returning to a session with fresh messages, there is no visual separator showing what changed since the last visit. The user must scroll and mentally diff.

All three must survive page refresh and browser restart — a user may kick off a workflow, close the tab, and return hours later.

---

## Scale Constraints

These are enforced by the platform settings (`pkg/settings/schema.go`) and determine the performance profile:

| Constraint | Value | Source |
|---|---|---|
| Max workspaces per user | ~20 (default list limit) | `workspace_service.go:324` |
| Max active workspaces per user | 10 (configurable 1–50) | `settings/schema.go:61` — `maxActiveWorkspacesPerUser` |
| Max sessions per workspace | 10 (stated requirement) | — |
| Max sessions per user | ~100 (10 active ws × 10 sessions) | Derived |
| Session index SQL LIMIT | 100 | `database.go:685` |

**Design implication:** With at most ~100 sessions across at most 10 active workspaces, performance optimization (granular subscriptions, external stores) is unnecessary. A plain `useState` Map in a React Context is more than sufficient — re-rendering 100 items on state change is trivial.

---

## Validated Assumptions

Every claim verified against live code.

| # | Assumption | Verified At | Result |
|---|---|---|---|
| A1 | `session_index.status` is hardcoded to `"idle"` in `ListSessionIndex` | `database.go:710` | **Confirmed.** Active/busy is tracked in-memory in `ProxyHandler.activeSess` only. |
| A2 | `session.status` SSE events are published only to the workspace-scoped broker, not the user-scoped broker | `proxy.go:963`, `proxy.go:1169` | **Confirmed.** Both `onSessionIdle` and `onSessionActive` call `h.broker.Publish` only. |
| A3 | `useUserEventStream` processes events internally with no callback to consumers | `useUserEventStream.ts:40-63` | **Confirmed.** Events are consumed inline. Must add an outlet for session status events. |
| A4 | The sidebar only fetches sessions for expanded workspaces | `Sidebar.tsx:366` | **Confirmed.** `WorkspaceSessionList` is conditionally rendered. |
| A5 | No unread/new-message tracking exists anywhere in the codebase | grep for `unread`, `lastSeen`, `last_seen` | **Confirmed.** |
| A6 | `UserEventBroker.PublishToUser` has replay buffer (128 events), gap detection, and workspace→user ownership tracking | `event_broker_user.go:226-245, 275-289` | **Confirmed.** |
| A7 | The router `GET /:id/sessions` handler has access to `proxyHandler` | `router.go:755` | **Confirmed.** Already used for `BackfillSessionParents`. |
| A8 | `ProxyHandler.GetActiveSessions(workspaceID)` already exists and returns `[]string` of active session IDs | `proxy.go:1143` | **Confirmed.** No new method needed. |
| A9 | `activeMu` is `sync.Mutex` (not `RWMutex`) — existing `GetActiveSessions` takes a full lock | `proxy.go:70` | **Confirmed.** Acceptable — lock held briefly, router handler is concurrent-safe. |
| A10 | `RecordWorkspaceOwner` is called in `onPhaseChange` for every workspace phase transition | `proxy.go:901-902` | **Confirmed.** `WorkspaceOwner(workspaceID)` returns the userID for active workspaces. |
| A11 | `invalidateCaches` deletes `activeSess[workspaceID]` on workspace suspend/terminate | `proxy.go:878` | **Confirmed.** Frontend must clear busy state when receiving `workspace.phase` events for non-Active phases. |
| A12 | Messages have `createdAt` as ISO string, sorted chronologically | `api/types.ts:152`, `messages.ts` | **Confirmed.** |
| A13 | `useSyncExternalStore` is not used anywhere in the codebase | grep for `useSyncExternalStore` | **Confirmed.** Codebase uses React Context + useState for app state. Introducing it would be a new paradigm for no benefit at this scale. |
| A14 | `AppShell` mounts `useUserEventStream` and is inside the router (has access to `useParams`) | `AppShell.tsx:22`, `router.tsx:37` | **Confirmed.** Provider should mount in `AppShell`, not `App`. |
| A15 | `contract-fixtures.json` and `contract.test.ts` must be updated when Go types change | `api/contract-fixtures.json`, `api/contract.test.ts` | **Confirmed.** These are the Go↔TS contract tests. |
| A16 | `RecordMessage` in session index is async (bounded channel) with ~5s eventual consistency | `sessionindex/service.go:60, 98` | **Confirmed.** `last_message_at` may lag real time by seconds. |

---

## Solution Overview

Three capabilities, 9 stories (US-37.4 merged into US-37.3 as it's the same SQL change):

### 1. Activity Indicator (Busy Spinner)

**Current:** Dead blue dot — `status` is always `"idle"` from the API.

**New:** Animated spinner on each session row when the session is actively processing. Collapsed workspaces show a spinner if any child session is busy.

**Mechanism:**
- **Backend (US-37.1):** Publish `session.status` to user-scoped SSE stream in addition to workspace-scoped. ~4 lines per callback.
- **Backend (US-37.2):** Merge active session IDs from `ProxyHandler.GetActiveSessions()` into session list REST response. ~8 lines in router.
- **Frontend (US-37.5):** Simple React Context tracks `busySessions: Map<sessionId, workspaceId>` from SSE events and REST data. Updates `queryClient.setQueryData` on SSE events for real-time cache updates.

### 2. Unread Pulsation

**Current:** No concept of "unread".

**New:** Session name and icon pulsate when the session has unseen responses. Persists across refreshes.

**Mechanism:**
- **Backend (US-37.3):** New `last_seen_at TIMESTAMPTZ` column + `PUT /sessions/:id/seen` endpoint. `has_unread` computed in SQL.
- **Frontend (US-37.6):** Sidebar reads `hasUnread` from session query data + real-time SSE-driven `pendingUnread` set.

### 3. "New Messages" Divider

**Current:** No visual separator.

**New:** Divider at the boundary of seen/unseen messages. Gone on next visit after viewing.

**Mechanism:**
- **Frontend (US-37.7):** `MessageList` receives `lastSeenAt` prop, renders divider before first message with `createdAt > lastSeenAt`.
- **Frontend (US-37.8):** Mark-seen API calls on navigate-to (immediate) and navigate-away (debounced).

---

## Architecture

### Data Flow — Initial Page Load

```
Browser loads /chat
  │
  ├── GET /api/v1/workspaces → WorkspaceListItem[]
  │
  ├── For each expanded workspace: GET /workspaces/:id/sessions → SessionListItem[]
  │     (status: "active"|"idle" from activeSess map — US-37.2)
  │     (hasUnread computed from last_seen_at vs last_message_at — US-37.3)
  │
  ├── GET /api/v1/events (user-scoped SSE) → receives session.status events — US-37.1
  │
  └── SessionActivityProvider (in AppShell) initializes
        ├── busySessions from REST data (status === "active")
        └── Sidebar renders spinners/pulsation from context + query data
```

### Data Flow — Real-Time Session Status

```
opencode agent emits session.status {type: "busy"} for session S in workspace W
  │
  ├── SSETracker.onSessionActive("W", "S")
  │     └── proxyHandler.onSessionActive("W", "S")
  │           ├── h.broker.Publish("W", {..., status: "busy"})           — existing, unchanged
  │           └── h.userBroker.PublishToUser(userID, {..., status: "busy", workspace_id: "W"})  — NEW (US-37.1)
  │
  └── User-scoped SSE → useUserEventStream.onEvent → SessionActivityProvider
        ├── setBusy("S", "W")
        └── If query ["sessions", "W"] exists in cache: setQueryData to update S.status = "active"
```

### Data Flow — Workspace Suspend Cleanup

```
Workspace W transitions to Suspending
  │
  ├── onPhaseChange → h.userBroker.PublishToUser(userID, {type: "workspace.phase", phase: "Suspending"})
  │     └── invalidateCaches("W") clears activeSess["W"]
  │
  └── User-scoped SSE → useUserEventStream → SessionActivityProvider
        └── clearBusyForWorkspace("W")   — clears all busy entries for W
        └── clearPendingUnreadForWorkspace("W")
```

### Data Flow — Mark as Seen

```
User navigates to session S in workspace W
  │
  ├── PUT /workspaces/W/sessions/S/seen → last_seen_at = NOW()
  ├── clearPendingUnread("S")
  └── MessageList renders with lastSeenAt from session query data
        └── Divider before first message where createdAt > lastSeenAt

User navigates away from session S (1s debounced)
  │
  └── PUT /workspaces/W/sessions/S/seen → last_seen_at = NOW()
        └── Captures any messages that arrived while viewing
```

---

## Stories

| Story | Title | Priority | Depends On |
|-------|-------|----------|------------|
| [US-37.1](US-37.1-publish-session-status-to-user-sse.md) | Publish session.status to user-scoped SSE stream | Critical | — |
| [US-37.2](US-37.2-merge-active-status-into-rest.md) | Merge active session status into session list REST | Critical | — |
| [US-37.3](US-37.3-last-seen-at-has-unread-and-mark-seen.md) | Add `last_seen_at`, compute `hasUnread`, mark-seen endpoint | Critical | — |
| [US-37.4](US-37.4-session-activity-provider.md) | SessionActivityProvider (React Context) | Critical | US-37.1, US-37.2 |
| [US-37.5](US-37.5-activity-spinner.md) | Activity spinner in sidebar | Critical | US-37.4 |
| [US-37.6](US-37.6-unread-pulsation.md) | Unread pulsation in sidebar | High | US-37.4, US-37.3 |
| [US-37.7](US-37.7-new-messages-divider.md) | "New messages" divider in MessageList | High | US-37.3 |
| [US-37.8](US-37.8-mark-seen-on-navigate.md) | Mark-seen on navigate | High | US-37.3, US-37.7 |
| [US-37.9](US-37.9-tests.md) | Tests: unit, integration, E2E, regression | Critical | US-37.1–37.8 |

### Dependency Graph

```
US-37.1 (SSE publish) ──────────┐
US-37.2 (REST merge) ────────────┤
US-37.3 (last_seen_at + seen) ───┤
       │                         ├── US-37.4 (Provider) ──────┐
       │                         │        ├── US-37.5 (Spinner)│
       │                         │        └── US-37.6 (Pulse)  │
       ├── US-37.7 (Divider) ────┤                            │
       │       └── US-37.8 (Nav)─┤                            │
       │                         │                            │
       └───────────────────── US-37.9 (Tests) ───────────────┘
```

### Implementation Order

```
Phase 1 (backend, independent, can parallel):
  US-37.1  ← 4 lines × 2 methods in proxy.go
  US-37.2  ← 8 lines in router.go
  US-37.3  ← migration + DB method + endpoint (~1.5h)

Phase 2 (frontend foundation):
  US-37.4  ← provider + useUserEventStream changes (~3h)

Phase 3 (frontend features):
  US-37.5  ← spinner (simple, ~1h)
  US-37.6  ← pulsation (simple, ~1h)
  US-37.7  ← divider (~1.5h)
  US-37.8  ← mark-seen navigate (~1.5h)

Phase 4 (tests):
  US-37.9  ← all tests (~6h)
```

---

## Design Decisions

### D1: `hasUnread` computed in SQL, not Go

Computed inline in the `SELECT` statement. Reason: both timestamps come from the same PostgreSQL server, eliminating clock skew in the comparison. Post-query Go comparison would be correct too (both values come from the same row), but SQL computation is idiomatic and avoids a Go loop.

### D2: `hasUnread = false` when `last_seen_at IS NULL`

A null `last_seen_at` means the user has never visited the session. Showing it as "unread" would be wrong — the user may have created it and sent the initial message, already seeing the responses. Only sessions the user has visited at least once can be "unread."

### D3: Clock skew buffer = 1 second

`last_seen_at` is set by the API server clock. `createdAt` on messages is set by the opencode agent (potentially different pod, same cluster). Intra-cluster NTP drift is typically <100ms. A 1-second buffer is generous. Implemented as a constant in `MessageList`:

```typescript
const CLOCK_SKEW_BUFFER_MS = 1000;
```

### D4: Provider mounts in `AppShell`, not `App`

`App` is outside the router — `useParams` is unavailable. `AppShell` is inside the router at `/chat/:workspaceId/:sessionId` (`router.tsx:37`), already mounts `useUserEventStream`, and renders the `Sidebar`. The provider goes here.

### D5: Use existing `GetActiveSessions`, not a new method

`ProxyHandler.GetActiveSessions(workspaceID string) []string` exists at `proxy.go:1143`. It uses `activeMu` (sync.Mutex), which is sufficient — the lock is held briefly to copy the map keys. No new method needed.

### D6: `useUserEventStream` gains an `onEvent` callback

The hook currently consumes events internally. To avoid duplicating the SSE connection, add an optional `onEvent?: (event: unknown) => void` parameter. The provider passes its handler via this callback. This follows the same pattern as `useEventStream` which already accepts `onEvent`.

### D7: `queryClient.setQueryData` guarded by existence check

When a workspace is collapsed, no `["sessions", wsId]` query exists. Calling `setQueryData` would create an orphan cache entry. Guard with `getQueryData` first — only update if the query already exists.

### D8: `workspace.phase` events clear busy state for that workspace

`invalidateCaches` (`proxy.go:878`) clears `activeSess[workspaceID]` on suspend/terminate, but no per-session `idle` events are emitted. The provider must listen for `workspace.phase` events with non-Active phases and clear all busy/unread entries for that workspace.

---

## Risks, Failure Modes, and Mitigations

### R1: `last_seen_at` clock skew with `createdAt`
**Risk:** `last_seen_at` is API server clock, `createdAt` is agent pod clock. Skewed clocks could misclassify messages.
**Mitigation:** 1-second buffer in divider computation (D3). Both pods run in the same K8s cluster with NTP.

### R2: Workspace owner not yet recorded
**Risk:** `WorkspaceOwner(workspaceID)` returns `""` if no phase change event has been processed (e.g., API server restarted after workspace was already Active).
**Mitigation:** `session.status` event is silently skipped. REST API returns correct state on page load. Owner is recorded on next phase change.

### R3: Stale busy state after SSE disconnect during workspace suspend
**Risk:** Workspace suspends while SSE is disconnected. `invalidateCaches` clears `activeSess` but no `idle` events reach the frontend. Reconnect replays may not include these events.
**Mitigation:** On SSE reconnect, `useUserEventStream` invalidates all workspace caches (existing behavior at line 68). Session list refetch returns correct `status: "idle"`. Additionally, D8 ensures `workspace.phase` events clear busy state.

### R4: Race between mark-seen and new message
**Risk:** User navigates to session → mark-seen fires → but a message was being processed → `last_seen_at` is before the new message → divider shows for a message the user is about to see stream in.
**Mitigation:** This is actually correct behavior. The divider appears before the in-progress message, which is where new content starts. When the session is busy, the user sees streaming content below the divider. On navigate-away, mark-seen fires again with a later timestamp.

### R5: `last_seen_at` null for subtask sessions
**Risk:** Subtask sessions (nested under parent via `parentId`) are never directly visited. `last_seen_at` remains null. `hasUnread` is false. No pulsation for subtask completion.
**Mitigation:** This is correct — subtask results are visible in the parent session's chat. The parent session's unread state covers subtask output.

### R6: Multiple rapid mark-seen calls during fast navigation
**Risk:** User rapidly clicks through sessions A→B→C. Three mark-seen calls fire in quick succession.
**Mitigation:** Navigate-away is debounced (1s). Navigate-to is immediate but idempotent. The backend is a simple `UPDATE SET last_seen_at = NOW()` — concurrent calls are safe (last writer wins, and all write ~NOW()).

---

## Test Plan

### Unit Tests — Backend (Go)

| # | Test | File |
|---|------|------|
| 1 | `onSessionIdle` publishes to user broker with correct fields | `proxy_test.go` |
| 2 | `onSessionActive` publishes to user broker with correct fields | `proxy_test.go` |
| 3 | `onSessionIdle` skips user broker when owner unknown | `proxy_test.go` |
| 4 | `onSessionIdle` no panic when `userBroker` is nil | `proxy_test.go` |
| 5 | `UpdateSessionLastSeen` updates existing row | `session_index_test.go` |
| 6 | `UpdateSessionLastSeen` creates row if missing | `session_index_test.go` |
| 7 | `ListSessionIndex` returns `last_seen_at` and `has_unread` | `session_index_test.go` |
| 8 | `has_unread` true when `last_message_at > last_seen_at` | `session_index_test.go` |
| 9 | `has_unread` false when `last_seen_at IS NULL` | `session_index_test.go` |
| 10 | `has_unread` false when caught up | `session_index_test.go` |
| 11 | `MarkSessionSeen` verifies ownership | `workspace_session_test.go` |
| 12 | Router merges active session IDs into response | `router_frontend_workspace_test.go` |
| 13 | Router handles nil proxyHandler (all idle) | `router_frontend_workspace_test.go` |
| 14 | `PUT /sessions/:id/seen` returns 204 | `router_frontend_workspace_test.go` |
| 15 | `PUT /sessions/:id/seen` wrong user → 403 | `router_frontend_workspace_test.go` |
| 16 | User-scoped SSE delivers `session.status` event | `stream_user_events_test.go` |

### Unit Tests — Frontend (Vitest)

| # | Test | File |
|---|------|------|
| 17 | Provider initializes busy from REST `status: "active"` | `SessionActivityProvider.test.tsx` |
| 18 | Provider updates busy on SSE `session.status: busy` | `SessionActivityProvider.test.tsx` |
| 19 | Provider marks pending unread on SSE idle (non-viewed session) | `SessionActivityProvider.test.tsx` |
| 20 | Provider does NOT mark pending unread on SSE idle (viewed session) | `SessionActivityProvider.test.tsx` |
| 21 | Provider clears pending unread on navigate-to | `SessionActivityProvider.test.tsx` |
| 22 | Provider clears busy/unread on `workspace.phase: Suspending` | `SessionActivityProvider.test.tsx` |
| 23 | Provider respects REST `hasUnread` on load | `SessionActivityProvider.test.tsx` |
| 24 | `workspaceBusyCount` returns correct count | `SessionActivityProvider.test.tsx` |
| 25 | MessageList renders divider at correct position | `MessageList.test.tsx` |
| 26 | MessageList no divider when `lastSeenAt` is null | `MessageList.test.tsx` |
| 27 | MessageList no divider when caught up | `MessageList.test.tsx` |
| 28 | MessageList no crash when messages lack `createdAt` | `MessageList.test.tsx` |
| 29 | mark-seen called on navigate-to | `ChatPage.test.tsx` |
| 30 | mark-seen called on navigate-away (debounced) | `ChatPage.test.tsx` |
| 31 | mark-seen failure is silent | `ChatPage.test.tsx` |
| 32 | Sidebar shows spinner for busy session | `Sidebar.test.tsx` |
| 33 | Sidebar shows pulsation for unread session | `Sidebar.test.tsx` |
| 34 | Sidebar collapsed workspace shows spinner | `Sidebar.test.tsx` |
| 35 | Contract: `SessionListItem` has `lastSeenAt`, `hasUnread` | `contract.test.ts` |

### Integration Tests

| # | Test | Scope |
|---|------|--------|
| 36 | SSE busy → provider → sidebar spinner → idle → pulsation | Backend SSE + Frontend |
| 37 | Navigate to unread → divider → away → back → no divider | Backend API + Frontend |
| 38 | Page refresh → REST returns `status: "active"` → spinner on load | Backend API + Frontend |
| 39 | Page refresh → REST returns `hasUnread: true` → pulsation on load | Backend API + Frontend |

### E2E Tests (Playwright)

| # | Test |
|---|------|
| 40 | Activity spinner across workspaces |
| 41 | Unread pulsation after completion |
| 42 | Pulsation clears on navigate |
| 43 | New messages divider |
| 44 | Divider gone on revisit |
| 45 | Persistence across page refresh |
| 46 | Collapsed workspace spinner |

### Regression Tests

| # | Concern |
|---|---------|
| 47 | Existing streaming UX (send → stream → reconcile) |
| 48 | Session list ordering, titles, timestamps |
| 49 | SSE reconnection |
| 50 | Workspace phase indicators (green/yellow/gray) |
| 51 | Message pagination with divider present |
| 52 | Session tree hierarchy (parent/child, orphans) |
| 53 | Mobile swipeable sidebar |
