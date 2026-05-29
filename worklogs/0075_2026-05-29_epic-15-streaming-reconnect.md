# Worklog: Epic 15 — Streaming State Resilience & Mid-Stream Reconnect

**Date:** 2026-05-29
**Session:** Implement all 6 user stories of Epic 15 (US-15.1 through US-15.6)
**Status:** Complete

---

## Objective

Implement streaming state resilience so that:
1. The streaming indicator is server-driven (survives page refresh)
2. Mid-stream page reload renders accumulated history + resumes live streaming for new parts
3. On idle, authoritative history replaces any streaming artifacts

---

## Work Completed

### US-15.1: Session Status Polling on Mount
- `useChatStream` now accepts `serverBusy: boolean` parameter and exposes `effectiveStreaming = localStreaming || serverBusy`
- `ChatPage` derives `serverBusy` from `useWorkspaceStatus().sessions[].status`
- Added `sseHasDrivenBusy` ref to prevent status poll from overriding SSE-driven state

### US-15.2: SSE-Driven Streaming State
- `useEventStream` now accepts optional `{ onReconnect }` callback, fired on successful reconnection (not first connect)
- `handleSSEEvent` sets `serverBusy` from `session.status` events for the current session
- On SSE reconnect, `workspace-status` query is invalidated to re-poll and catch missed transitions

### US-15.3: History Fetch on Busy Reconnect
- `historyPartIds` ref computed from `useMessageHistory` data — contains all `part.id` values from fetched history
- Existing `useMessageHistory` already fetches on mount regardless of streaming state — no new fetch logic needed

### US-15.4: Fetch-on-Boundary Live Streaming
- Added boundary detection gate at the top of `parseStreamEvent`
- In reconnect mode: `message.part.updated` events with `part.id` in `historyPartIds` are ignored; new parts are tracked in `knownLivePartIds` and rendered live
- `message.part.delta` events for history parts or orphan parts are ignored
- Gate is inactive when `isReconnectMode.current === false` (normal send flow)

### US-15.5: Final History Reconciliation on Idle
- `reconcileOnIdle()` awaits `queryClient.refetchQueries` then clears `sseStreamParts`, `isReconnectMode`, `knownLivePartIds`, and all streaming refs
- Called from SSE `session.status: idle` handler — works for both send flow and reconnect flow
- No flicker: streaming parts cleared only AFTER history arrives in cache

### US-15.6: E2E Tests
- 18 test cases in `ChatPage.reconnect.test.tsx` covering all 5 stories
- Groups: Status-Driven Indicator (7), History Fetch (2), Boundary Detection (6), Idle Reconciliation (2), Full Flow (1)
- All existing 65 ChatPage tests pass without modification (zero regressions)

---

## Key Decisions

1. **`sseHasDrivenBusy` flag**: Prevents the `useEffect` that syncs from status poll from overriding SSE-driven state. Without this, the stale status poll would reset `serverBusy` to false immediately after SSE sets it to true.

2. **Reconnect mode as a ref, not state**: `isReconnectMode` is a ref because it's read synchronously inside `parseStreamEvent` (a callback). Using state would cause stale closure issues.

3. **`reconcileOnIdle` uses `refetchQueries` not `invalidateQueries`**: `refetchQueries` returns a promise that resolves when data arrives, allowing us to clear streaming parts only after history is in cache (no flicker).

4. **`handleSend` exits reconnect mode**: When the user sends a new message, reconnect mode is disabled so the normal streaming flow works without the boundary gate.

---

## Blockers

None.

---

## Tests Run

```
npx vitest run src/pages/ChatPage.reconnect.test.tsx
  → 18 passed

npx vitest run src/pages/ChatPage.sse.test.tsx src/pages/ChatPage.test.tsx src/pages/ChatPage.activate.test.tsx src/pages/ChatPage.autorename.test.tsx src/hooks/ src/api/ src/lib/
  → 237 passed, 0 failed

npx tsc --noEmit
  → Only pre-existing @radix-ui errors (unrelated)
```

---

## Next Steps

- Validate assumption EA5 (does opencode return in-progress messages via GET /session/:id/message before idle?) against a real cluster
- If EA5 is false, the reconnect UX is slightly degraded (history shows messages up to current response, not partial response text) but still acceptable
- Consider adding broker-level sequencing in a future epic if fetch-on-boundary proves insufficient

---

## Files Modified

- `frontend/src/hooks/useEventStream.ts` — added `onReconnect` callback parameter
- `frontend/src/hooks/useChatStream.ts` — added `serverBusy` param, `effectiveStreaming`, `localStreaming` export
- `frontend/src/pages/ChatPage.tsx` — serverBusy state, historyPartIds, isReconnectMode, knownLivePartIds, boundary detection gate, reconcileOnIdle, handleSSEReconnect
- `frontend/src/pages/ChatPage.reconnect.test.tsx` — NEW: 18 test cases for Epic 15
