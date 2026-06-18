# Epic 39: Session Activity State Integrity

> **Renumbered** from `epic-38` (US-46.1, 2026-06-18): the `epic-38` slot collided
> with `epic-38-architectural-remediation`. This directory moved to `epic-39` to
> give every epic a unique number. Internal story IDs remain `US-38.x` (historical
> labels referenced by append-only worklogs); the directory number is the
> canonical handle going forward.

**Status:** Implemented
**Created:** 2026-06-12
**Priority:** High
**Depends on:** Epic 37 (Session Activity & Unread State UX — provider and hooks exist)
**Related:** Epic 28 (Unified Event Stream — user-scoped SSE)

---

## Problem Statement

The blue spinner in the left sidebar that indicates a busy session intermittently resets to the static `MessageSquare` icon and then comes back, despite the session being busy the entire time. Subtask (child) sessions also fail to show the spinner.

### User impact

When clicking through sessions across multiple workspaces, the spinner on a busy session flickers off and back on. The user interprets this as the session completing and restarting, which is incorrect. Subtask sessions never show activity at all.

---

## Root Cause

`SessionActivityProvider.tsx` used `queryCache.subscribe` to rebuild its entire `busySessions` and `pendingUnread` maps from the React Query cache on **every** cache update. This fired every time any `"sessions"` query was invalidated or refetched — which happens on every `session.status` SSE event (`ChatPage.tsx:547`).

The React Query cache is populated by the REST endpoint `GET /workspaces/:id/sessions`. That endpoint returns sessions from PostgreSQL with `status: "idle"` hardcoded (`database.go:822`), then enriches sessions found in `ProxyHandler.activeSess` — a **per-replica in-memory map** (`proxy.go:74`, documented at `proxy.go:1228-1229` as "per-replica view"). When the enrichment misses (different API replica, timing gap, or the `activeSess` map being empty on a fresh API server start), REST returns `status: "idle"` for a session that SSE correctly tracks as busy.

The rebuild from REST clobbered the SSE-tracked state, causing the spinner to vanish. The next SSE event (or the SSE handler's `setQueryData` call) would restore it, producing the flicker.

---

## Validated Assumptions

Every assumption was verified against source code before being used in this design.

| # | Assumption | Status | Evidence |
|---|-----------|--------|----------|
| V1 | SSE tracker (`session_tracker.go`) processes `session.status` for ALL sessions including subtasks — no `parentID` filtering | ✅ Verified | `session_tracker.go:319-379` — reads `sessionID` only, no parent check |
| V2 | `onSessionActive` publishes child session's own `sessionID` to user broker (not root) | ✅ Verified | `proxy.go:1271-1279` — `SessionID: sessionID` passed through, no `resolveRootSessionID` |
| V3 | User broker delivers all event types including `session.status` — no filtering | ✅ Verified | `event_broker_user.go:235-252` — zero filtering by `evt.Type` |
| V4 | User SSE does NOT snapshot active sessions on connect — only `workspace.phase` events are snapshotted | ✅ Verified | `stream_user_events.go:186-252` — no `GetActiveSessions` call |
| V5 | Last-Event-ID replay buffer is 128 events per shard | ✅ Verified | `event_broker_user.go:16` — `replayBufferSize = 128` |
| V6 | `useUserEventStream` did NOT expose `onReconnect` before this epic — added by US-38.3 | ✅ Verified | `useUserEventStream.ts:19` — `onReconnect` added alongside existing `onEvent` option |
| V7 | ChatPage calls `invalidateQueries(["sessions", wsId])` on **every** `session.status` SSE event, unconditionally | ✅ Verified | `ChatPage.tsx:546-547` — fires before any status/session check |
| V8 | REST returns `status:"active"` ONLY from per-replica enrichment; DB hardcodes `"idle"` | ✅ Verified | `database.go:822` + `router.go:759-769` |
| V9 | `GetActiveSessions` is per-replica in-memory — not Redis, not cross-replica | ✅ Verified | `proxy.go:74` — plain `map[string]map[string]bool`, mutex-guarded |
| V10 | SSE tracker `StopWatching` IS called for individual workspaces on suspend/terminate — NOT process-lifetime only | ✅ Verified | `proxy.go:982-984` — `StopWatching` on non-Active phases; `proxy.go:1021-1024` — stop+restart on Active transition |
| V11 | `queryCache.subscribe` fires `"updated"` event when a query transitions to fetching (before new data arrives), with old data still in `query.state.data` | ✅ Verified | TanStack Query `query.js:407` — `#dispatch` calls `this.#cache.notify({ type: "updated" })` for `"fetch"` action |
| V12 | `DeleteSession` publishes `session.status: "deleted"` to workspace broker only — NOT to user broker (`PublishToUser`) | ✅ Verified | `proxy.go:328` — only `h.broker.Publish`, no `h.userBroker.PublishToUser` |
| V13 | `activateMutation.onSuccess` fires before the workspace pod is running (milliseconds vs seconds) | ✅ Verified | `Sidebar.tsx:75-82` — fires on HTTP response; K8s reconciliation takes seconds |
| V14 | opencode will NOT re-emit `session.status: busy` for a session that is already busy — events fire on transitions only | ✅ Verified | opencode session state machine: busy→busy is not a transition |
| V15 | `SessionTreeRow` renders `<Loader2>` for any session where `useIsSessionBusy(s.id)` returns true — no depth or parent filtering | ✅ Verified | `Sidebar.tsx:732-733` — unconditional `isBusy ? <Loader2> : ...`, recursive at `Sidebar.tsx:760-776` |

---

## Architecture Decision

### Level of abstraction

The fix lives entirely within the **existing** `SessionActivityProvider` — a React Context provider that already owns busy/unread state. No new providers, no new hooks, no new components, no new backend endpoints.

**Justification:**

- The provider already has the right responsibility boundary: "track ephemeral session signals (busy, unread) from SSE". The bug was in HOW it reconciled REST data, not in WHAT it tracks.
- Busy/unread are real-time signals, not REST resources. They belong in a reactive map, not in the TanStack Query cache. The existing architecture is correct.
- Adding a new abstraction (e.g., a dedicated SSE state store, a Zustand slice, a `useSyncExternalStore` wrapper) would introduce a new paradigm for zero functional benefit. The codebase uses React Context + useState for all app state (verified: `grep useSyncExternalStore` returns zero results). Introducing a new state pattern would violate consistency.

### Alternatives considered

| Alternative | Pros | Why discarded |
|------------|------|---------------|
| **Full replacement: Zustand store for busy state** | Decoupled from React lifecycle; `subscribe` without `useEffect`; easier to test | Introduces a new state management paradigm. Codebase uses Context+useState everywhere. Two patterns is worse than one. No performance benefit at current scale (~100 sessions). |
| **Backend fix: Share `activeSess` via Redis** | REST becomes globally consistent across replicas; eliminates the root cause | Requires backend infra change (Redis for session state), new failure mode (Redis down), and latency addition. The frontend should not wait for a backend fix to stop flickering. The backend fix is a separate epic (mentioned in `README-LLM.md` line ~459 as future cleanup). |
| **Backend fix: Snapshot active sessions in user SSE on connect** | Frontend gets authoritative state on connect without REST | Requires backend change to `stream_user_events.go` (snapshot function). More complex — need to enumerate all active workspaces' sessions. Current approach (seed from REST cache) achieves the same result using data the frontend already has. |
| **Merge-based reconciliation (REST entries + SSE entries)** | Simpler than seed-once — just merge both sources on every update | REST `"active"` overrides SSE `"idle"` (REST entries go first in map, SSE gaps get filled). SSE `"idle"` overrides REST `"idle"` (no conflict). But REST `"active"` for a session SSE already idled re-adds it — spinner shows after session completes. Incorrect. |
| **Debounce `seedFromCache` — only rebuild after 2s quiet period** | Reduces flicker frequency | Does not eliminate the root cause. Adds timing complexity. The debounce window itself could mask real state changes. |

---

## Design

### State ownership model

```
                          ┌─────────────────────────────────────┐
                          │  SessionActivityProvider             │
                          │                                     │
  REST (one-time seed) ──►│  busySessions: Map<sessionId, wsId> │──► Sidebar spinner
  GET /sessions (active)  │  pendingUnread: Map<sessionId, wsId>│──► Sidebar pulse
                          │  seededRef: Set<worspaceId>         │
  SSE (continuous) ──────►│                                     │
  session.status busy/idle│  SSE is sole authority after seed   │
                          │                                     │
  Lifecycle events ──────►│  workspace.phase: clear + reset    │
  workspace.phase         │  SSE reconnect: clear all + reseed  │
                          └─────────────────────────────────────┘
```

**Rule: SSE is the sole authority for busy/unread after initial seed. REST is only used for seeding before SSE connects, and re-seeding after lifecycle transitions.**

### Seeding lifecycle

Each workspace goes through exactly these states:

```
[unseeded] ──REST data arrives──► [seeded, SSE authority]
     ▲                                  │
     │              workspace.phase     │
     │              (non-Active)        │
     │            ◄─────────────────────┘
     │                                  │
     │              workspace.phase     │
     │              (Active)            │
     │            ◄─────────────────────┘
     │                                  
     └──── SSE reconnect ────────────────┘
          (clears ALL workspaces)
```

1. **Initial seed.** When a `"sessions"` query resolves for a workspace not in `seededRef`, read sessions with `status: "active"` and `hasUnread: true` from the REST data and add them to the maps. Mark workspace as seeded.

2. **SSE authority.** After seeding, `session.status` SSE events are the only source that modifies `busySessions`. SSE `busy` adds; SSE `idle` removes. REST refetches (from `invalidateQueries`) are ignored for seeded workspaces.

3. **Lifecycle reset.** When `workspace.phase` arrives with a non-Active phase (`Suspending`, `Suspended`, `Terminating`, `Terminated`, `Failed`), remove the workspace from `seededRef`, and clear all busy/unread entries for that workspace. When `workspace.phase` arrives with `"Active"`, also remove from `seededRef` (without clearing state — the workspace might have sessions that were busy before suspend and are still busy after resume).

4. **Reconnect reset.** When the SSE connection reconnects, clear ALL entries from `seededRef` and clear the `busySessions` map. The next REST cache update re-seeds all workspaces. This handles the case where sessions completed during the disconnect gap (idle events lost to replay buffer overflow).

### Implementation: `SessionActivityProvider.tsx`

#### Change 1: Replace `seedFromCache` with `seedNewWorkspaces`

**Before (buggy):**
```typescript
function seedFromCache() {
  const busy = new Map<string, string>();
  const unread = new Map<string, string>();
  for (const query of queryCache.getAll()) { ... }
  setBusySessions(busy);        // FULL REPLACEMENT — clobbers SSE state
  setPendingUnread(unread);     // FULL REPLACEMENT — clobbers SSE state
}
```

**After (fixed):**
```typescript
function seedNewWorkspaces() {
  let busyDelta: Map<string, string> | null = null;
  let unreadDelta: Map<string, string> | null = null;
  for (const query of queryCache.getAll()) {
    const key = query.queryKey;
    if (!Array.isArray(key) || key[0] !== "sessions" || typeof key[1] !== "string") continue;
    const wsId = key[1];
    if (seeded.has(wsId)) continue;          // SKIP already-seeded workspaces
    const data = query.state.data;
    if (!Array.isArray(data)) continue;
    seeded.add(wsId);
    for (const session of data as Array<{ id: string; status?: string; hasUnread?: boolean }>) {
      if (session.status === "active") { busyDelta.set(session.id, wsId); }
      if (session.hasUnread) { unreadDelta.set(session.id, wsId); }
    }
  }
  // Additive merge — preserves SSE-tracked entries
  if (busyDelta && busyDelta.size > 0) {
    setBusySessions((prev) => { const next = new Map(prev); /* merge */ return next; });
  }
  // Same for unreadDelta
}
```

**Key difference:** `seeded.has(wsId) → continue` means REST refetches for already-seeded workspaces are no-ops. SSE state is never clobbered.

#### Change 2: Reset seeding on workspace lifecycle events

**In the `onEvent` handler:**

```typescript
if (evt.type === "workspace.phase" && evt.workspace_id && evt.phase) {
  const wsId = evt.workspace_id;
  if (NON_ACTIVE_PHASES.has(evt.phase)) {
    seededRef.current.delete(wsId);   // Allow re-seed on resume
    // Clear busy/unread for this workspace
  } else if (evt.phase === "Active") {
    seededRef.current.delete(wsId);   // Allow re-seed from fresh REST data
  }
}
```

**Why `Active` also resets:** When a workspace resumes, the SSE tracker reconnects to the pod. But opencode won't re-emit `session.status: busy` for sessions that were already busy before suspend — events fire on transitions only (V14). The REST enrichment (which reads from `activeSess`) knows about these sessions because `onSessionActive` is called during the SSE tracker's reconnection backfill. So removing from `seededRef` on `Active` allows the next REST data update to re-seed these pre-existing busy sessions.

#### Change 3: Clear state on SSE reconnect

**In the `useUserEventStream` options:**

```typescript
useUserEventStream({
  onReconnect: () => {
    seededRef.current.clear();
    setBusySessions(new Map());
  },
  onEvent: (data) => { ... },
});
```

**Why clear the busy map:** If the SSE connection was down for >128 events, the replay buffer overflowed and idle events were lost. Sessions that completed during the gap would show as permanently busy because no future SSE event will correct them (opencode doesn't re-emit idle for already-idle sessions). The brief flash of cleared state is better than stale busy that never self-corrects.

**Why NOT clear `pendingUnread`:** Unread state is not ephemeral in the same way — it represents "a session completed that you haven't looked at". If we clear it on reconnect, users lose notifications for sessions that completed during the gap. The `hasUnread` field in the REST data will re-populate unread on re-seed.

### Implementation: `useUserEventStream.ts`

#### Change: Expose `onReconnect` option

**Before:**
```typescript
export function useUserEventStream(options?: { onEvent?: (event: unknown) => void }) {
```

**After:**
```typescript
export function useUserEventStream(options?: { onEvent?: (event: unknown) => void; onReconnect?: () => void }) {
  const onReconnectRef = useRef(options?.onReconnect);
  useEffect(() => { onReconnectRef.current = options?.onReconnect; });
  // ... in onConnect handler:
  if (lastEventIDRef.current !== null) {
    // ... existing reconnect logic ...
    onReconnectRef.current?.();
  }
```

**Why ref-based callback:** The `onConnect` callback is captured once at connection creation. Using a ref ensures the callback always reads the latest function from the parent, even if the parent re-renders between connections. This matches the existing `onEventRef` pattern.

---

## Failure Modes

### FM1 — Busy sessions cleared on reconnect, REST seed is stale (MEDIUM)

**Scenario:** SSE reconnects. `onReconnect` clears busy map. The REST cache data is from before the reconnect and shows `status: "idle"` for all sessions (because the per-replica enrichment hasn't run yet). The `seedNewWorkspaces` re-seeds nothing. Sessions that are actually busy show no spinner until the next SSE event arrives.

**Mitigation:** The SSE reconnect fires `invalidateQueries` for `workspaces` and `workspace-status` (in `useUserEventStream`'s `onConnect`). This triggers the sidebar to refetch workspace data, which eventually triggers the sessions query to refetch. The refetched REST data has the enrichment from the current replica. The `queryCache.subscribe` callback then fires `seedNewWorkspaces` with the fresh data.

**Residual risk:** Gap of 1-5 seconds between reconnect and re-seed. During this gap, all busy spinners are gone. The next SSE event (busy/idle transition) corrects it immediately. Acceptable — the gap is brief and self-correcting.

### FM2 — activateMutation.onSuccess fires before workspace is Active (MEDIUM)

**Scenario:** User clicks resume. `activateMutation.onSuccess` (`Sidebar.tsx:77-81`) fires immediately and invalidates `["sessions", wsId]`. The REST refetch hits the backend before the pod is running — returns 503. The query enters error state with `data: undefined`. When `workspace.phase: "Active"` SSE event arrives seconds later, `seededRef` is cleared, but there's no data to seed from.

**Mitigation:** The workspace SSE stream (`useEventStream` in ChatPage, `ChatPage.tsx:701`) reconnects when the workspace goes Active. The first `session.status` SSE event triggers `ChatPage.tsx:547`'s `invalidateQueries`, which refetches the sessions query with valid data. The `queryCache.subscribe` callback then seeds.

**Residual risk:** Collapsed workspaces don't mount `WorkspaceSessionList`, so their sessions query isn't active. The workspace-level spinner (`busyCount`) shows 0 until the user expands the workspace. Acceptable — the spinner only matters for expanded workspaces where the user can see individual sessions.

### FM3 — Deleted sessions remain in `busySessions` map (LOW)

**Scenario:** User deletes a busy session via the sidebar. `DeleteSession` (`proxy.go:328`) publishes `session.status: "deleted"` to the workspace broker only — NOT to the user broker (`PublishToUser`). The provider's `onEvent` handler (via `useUserEventStream`, which receives events from the user broker) never receives the "deleted" event. The entry persists in `busySessions`, inflating `workspaceBusyCount`.

**Mitigation:** The sidebar's `onDeleteSession` handler (`Sidebar.tsx:202`) calls `invalidateQueries(["sessions", ws.id])`. This triggers a REST refetch that no longer includes the deleted session. But since the workspace is already seeded, `seedNewWorkspaces` skips it. The entry persists until workspace phase change or SSE reconnect.

**Why not fix here:** Requires backend change — `DeleteSession` must call `PublishToUser` (like `onSessionIdle` and `onSessionActive` already do). This is a one-line backend fix but is out of scope for a frontend epic. **Tracked as follow-up item US-38.3.**

**Residual risk:** Workspace-level spinner shows on collapsed workspace after session deletion, until next lifecycle event. Individual session spinner is invisible because the session row is removed from the tree. Acceptable.

### FM4 — First seed after reset reads stale cache data (LOW)

**Scenario:** After a lifecycle reset clears `seededRef`, `seedNewWorkspaces` is triggered by `queryCache.subscribe`. But `queryCache.subscribe` fires `"updated"` on the fetch transition (V11), before new data arrives. `query.state.data` holds the old data. If the old data was written by SSE handlers (stamping `status: "active"`), it's correct. If the old data is from a previous REST refetch with `status: "idle"`, it's stale.

**Mitigation:** For the `workspace.phase: Active` case, the old data was typically written by the SSE busy handler (which stamps `status: "active"` into the cache). For the SSE reconnect case, the old data is the most recent REST response. Both are acceptable approximations — the next SSE event or REST refetch corrects within seconds.

**Residual risk:** Brief period of incorrect busy state after reset. Self-correcting. Acceptable.

---

## Design Critique

### W1 — `seededRef` is implicit global state (LOW) — ACKNOWLEDGED

`seededRef` coordinates between two `useEffect` hooks (the query cache subscriber and the SSE event handler) and one `useUserEventStream` callback. It's mutable shared state within a single component. This is the standard React pattern for cross-hook coordination (refs are designed for this).

**Alternative:** Use a state variable instead of a ref. This would cause unnecessary re-renders (the seeded set changes but is never read during render — only in callbacks). Ref is correct here.

### W2 — Unread state is NOT reset on reconnect (LOW) — ACKNOWLEDGED

`onReconnect` clears `busySessions` but not `pendingUnread`. Rationale: unread represents "a session completed that you haven't looked at" — this is durable information that survives reconnection. If the user had unread sessions before disconnect, they should still see the pulse after reconnect. The REST re-seed will add any new unread sessions from the `hasUnread` field.

### W3 — `clearPendingUnread` reads `wsId` from closure, not state (LOW) — ACKNOWLEDGED

Line 220 reads `pendingUnread.get(sessionId)` from the closure value. The `useCallback` depends on `[pendingUnread, queryClient]`, so the closure is at most one render behind. The consumer (ChatPage `useEffect` at line 82-90) captures the callback from the render and calls it in the same commit phase, so the closure is current. Not a bug in practice.

---

## User Stories

### US-38.1: One-time seed with SSE authority (frontend)

**Goal:** `SessionActivityProvider` seeds from REST once per workspace, then relies exclusively on SSE events for state changes.

**Implementation:**

1. Add `const seededRef = useRef(new Set<string>())` to `SessionActivityProvider`.

2. Replace `seedFromCache` with `seedNewWorkspaces` that:
   - Iterates `queryCache.getAll()` for `"sessions"` queries
   - Skips workspaces already in `seededRef`
   - For new workspaces, reads `status === "active"` and `hasUnread === true` from REST data
   - Adds to `seededRef`, then additively merges into `busySessions` and `pendingUnread`

3. The `queryCache.subscribe` callback calls `seedNewWorkspaces` (unchanged).

**Tests required:**

- SSE busy state survives cache refetch returning `status: "idle"` (regression)
- SSE busy state in ws-1 survives cache update for ws-2 (cross-workspace)
- Initial seed picks up `status: "active"` from REST (existing test #17)
- Initial seed picks up `hasUnread: true` from REST (existing test #23)
- Seeded workspace is not re-seeded on subsequent cache updates

**Files:** `frontend/src/providers/SessionActivityProvider.tsx`, `frontend/src/providers/SessionActivityProvider.test.tsx`

---

### US-38.2: Lifecycle-aware seeding reset (frontend)

**Goal:** Workspace suspend/resume cycles correctly reset and re-seed busy state.

**Implementation:**

1. In the `onEvent` handler, when `workspace.phase` is non-Active: `seededRef.current.delete(wsId)` before clearing busy/unread.

2. When `workspace.phase` is `"Active"`: `seededRef.current.delete(wsId)` without clearing state (the workspace might have pre-existing busy sessions from before suspend).

**Tests required:**

- Workspace suspend/resume re-seeds busy state from REST
- `workspace.phase: Active` resets seeding for re-seed
- Workspace suspend clears busy and unread for that workspace (existing test)

**Files:** `frontend/src/providers/SessionActivityProvider.tsx`, `frontend/src/providers/SessionActivityProvider.test.tsx`

---

### US-38.3: SSE reconnect state reset (frontend + hook)

**Goal:** Stale busy state is cleared on SSE reconnect to prevent permanently-incorrect spinners.

**Implementation:**

1. Add `onReconnect` option to `useUserEventStream`:
   - Signature: `options?: { onEvent?: (event: unknown) => void; onReconnect?: () => void }`
   - Store in `onReconnectRef` with `useEffect` sync (matches `onEventRef` pattern)
   - Call `onReconnectRef.current?.()` in the `onConnect` handler, only when `lastEventIDRef.current !== null` (reconnect, not initial connect)

2. In `SessionActivityProvider`, pass `onReconnect` to `useUserEventStream`:
   - `seededRef.current.clear()` — allows all workspaces to re-seed
   - `setBusySessions(new Map())` — clears stale entries that may never be corrected by SSE

**Tests required:**

- SSE reconnect re-seeds from REST
- SSE reconnect clears stale busy state
- `onReconnect` is not called on initial connect (existing `useUserEventStream` test: `lastEventIDRef.current === null` on first connect)
- `onReconnect` is called on subsequent connects (existing test pattern with `lastEventIDRef`)

**Files:** `frontend/src/providers/SessionActivityProvider.tsx`, `frontend/src/providers/SessionActivityProvider.test.tsx`, `frontend/src/hooks/useUserEventStream.ts`, `frontend/src/hooks/useUserEventStream.test.tsx`

---

### US-38.4: Publish session deletion to user broker (backend — follow-up)

**Goal:** Deleted sessions are removed from `busySessions` and `pendingUnread` in real-time.

**Implementation:**

1. In `proxy.go:DeleteSession` (line 322-334), add `h.userBroker.PublishToUser` call after the existing `h.broker.Publish`, matching the pattern in `onSessionIdle` (`proxy.go:1045-1054`) and `onSessionActive` (`proxy.go:1271-1279`).

2. In `SessionActivityProvider.onEvent`, add handling for `status === "deleted"`:
   - Remove from `busySessions`
   - Remove from `pendingUnread`
   - Remove from cache data

**Tests required:**

- Backend: `TestProxy_DeleteSession_PublishesSSEEventToUserBroker`
- Frontend: SSE "deleted" event removes session from busy and unread maps

**Note:** This is a follow-up story. The frontend implementation in US-38.1 through US-38.3 is complete without it. The residual risk is documented in FM3.

**Files:** `api/internal/handlers/proxy.go`, `api/internal/handlers/proxy_test.go`, `frontend/src/providers/SessionActivityProvider.tsx`, `frontend/src/providers/SessionActivityProvider.test.tsx`

---

## Scale Analysis

The provider tracks at most ~100 sessions across ~10 active workspaces (constraints from Epic 37 scale table). Each state update iterates the `busySessions` Map, which is O(n) where n ≤ 100. This is negligible — no optimization needed.

The `queryCache.subscribe` callback fires on every sessions query update. With 10 workspaces and staleTime: 0, this could fire ~10 times on page load (one per workspace query resolving) and then on each `invalidateQueries`. Each call iterates all query cache entries but skips seeded workspaces in O(1). Total cost per call: O(queries_in_cache) which is bounded by the number of workspaces.

---

## Test Plan

All tests are in `frontend/src/providers/SessionActivityProvider.test.tsx` unless noted.

| # | Test | Story | Type |
|---|------|-------|------|
| 1 | SSE busy state survives cache refetch returning `status: "idle"` | US-38.1 | Regression |
| 2 | SSE busy state in ws-1 survives cache update for ws-2 | US-38.1 | Regression |
| 3 | Workspace suspend/resume re-seeds busy state from REST | US-38.2 | Regression |
| 4 | `workspace.phase: Active` resets seeding for re-seed | US-38.2 | Regression |
| 5 | SSE reconnect re-seeds from REST | US-38.3 | Regression |
| 6 | SSE reconnect clears stale busy state | US-38.3 | Regression |
| 7 | Initial seed from `status: "active"` (existing) | US-38.1 | Existing |
| 8 | Initial seed from `hasUnread: true` (existing) | US-38.1 | Existing |
| 9 | `workspace.phase` non-Active clears busy/unread (existing) | US-38.2 | Existing |
| 10 | `workspace.phase` only clears that workspace's sessions (existing) | US-38.2 | Existing |

Run command: `cd frontend && npx vitest run src/providers/SessionActivityProvider.test.tsx`

Full regression: `cd frontend && npx vitest run` (922 tests)

---

## Files Modified

| File | Stories | Change |
|------|---------|--------|
| `frontend/src/providers/SessionActivityProvider.tsx` | US-38.1, 38.2, 38.3 | Replace `seedFromCache` with `seedNewWorkspaces`; add `seededRef`; reset on lifecycle; clear on reconnect |
| `frontend/src/providers/SessionActivityProvider.test.tsx` | US-38.1, 38.2, 38.3 | 6 new regression tests |
| `frontend/src/hooks/useUserEventStream.ts` | US-38.3 | Add `onReconnect` option |
| `frontend/src/hooks/useUserEventStream.test.tsx` | US-38.3 | Verify `onReconnect` not called on initial connect |
| `api/internal/handlers/proxy.go` | US-38.4 | Add `PublishToUser` to `DeleteSession` (follow-up) |
| `api/internal/handlers/proxy_test.go` | US-38.4 | Test for `PublishToUser` in `DeleteSession` (follow-up) |
