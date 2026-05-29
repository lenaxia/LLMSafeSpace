# Epic 15: Streaming State Resilience & Mid-Stream Reconnect

**Status:** Planning
**Created:** 2026-05-29
**Priority:** High
**Depends on:** Epic 3 (Proxy/Sessions), Epic 6 (Collapse Sandbox), Worklog 0071 (opencode v1.15.12 upgrade)

## Problem Statement

The frontend streaming UX has two defects:

1. **Streaming indicator is browser-state-dependent.** The bouncing "..." indicator is driven by a local React `useState(false)`. When the user refreshes the page or navigates away and back, the state resets to `false` even if the agent is actively processing. The user sees a dead chat with no indication that work is in progress.

2. **Mid-stream page reload loses the live stream.** If the user reloads while the agent is streaming a long response, the SSE connection drops. On reconnect, the frontend has no way to resume rendering the in-progress response. The user must wait for the agent to finish, then manually trigger a history fetch.

## Assumptions (Epic-Level)

| # | Assumption | Status | Validation |
|---|-----------|--------|------------|
| EA1 | opencode v1.15.12 is deployed (events carry `id`, parts carry `id`/`messageID`/`sessionID`) | ✅ | Worklog 0071: version pin updated, tests pass |
| EA2 | The workspace SSE stream (`GET /workspaces/:id/events`) reconnects automatically via `useEventStream` | ✅ | Verified: `useEventStream.ts` has exponential backoff reconnect loop |
| EA3 | `session.status` events with `status: "busy"/"idle"` are emitted by the proxy when a prompt starts/finishes | ✅ | Verified: `proxy.go:841-846` publishes `session.status` busy on prompt_async; session_tracker publishes idle on `session.status` idle from opencode |
| EA4 | `GET /workspaces/:id/status` returns `sessions[]` with per-session `status: "idle" | "busy"` | ✅ | Verified: `WorkspaceStatus.sessions` field in `api/types.ts`, populated by `getWorkspaceStatus` handler |
| EA5 | `messagesApi.getHistory()` returns the full message list including any partially-completed assistant response | ⚠️ | Unvalidated — need to confirm opencode returns in-progress messages via `GET /session/:id/message` before the session goes idle. If not, history is only complete after idle. |
| EA6 | The `message.part.updated` snapshot events contain the full accumulated text (not just the latest delta) | ✅ | Verified: worklog 0046 confirms `message.part.updated` is a snapshot; `message.part.delta` is incremental |
| EA7 | SSE events arrive in causal order per session (no out-of-order delivery within a single connection) | ✅ | SSE is TCP-ordered; the broker publishes sequentially per workspace |
| EA8 | The `useEventStream` hook fires the `onEvent` callback for every event including `session.status` | ✅ | Verified: `useEventStream.ts:55-60` calls `onEventRef.current(JSON.parse(line.slice(6)))` for every `data:` line |

## Solution Design

### Problem A: Session-State-Driven Streaming Indicator

**Current flow:**
```
User sends message → setStreaming(true) → await idle SSE → setStreaming(false)
Page refresh → useState(false) → indicator gone, even if agent is busy
```

**New flow:**
```
Page loads with sessionId → poll GET /workspaces/:id/status → check session.status
  If "busy" → setStreaming(true), subscribe to SSE
  If "idle" → setStreaming(false)
SSE session.status "idle" arrives → setStreaming(false)
SSE session.status "busy" arrives → setStreaming(true)
```

The streaming indicator becomes a **derived state from the server**, not a local side-effect of the send action.

### Problem B: Mid-Stream Reconnect (Fetch-on-Boundary)

**Strategy:** On page load (or SSE reconnect) while session is busy:

1. Fetch history via `getHistory()` — render all messages as static blocks (including any partial assistant response accumulated so far)
2. Subscribe to SSE stream
3. When the first SSE event arrives for a **new conversation part** (new tool call, new text block, new reasoning block) that isn't already in the rendered history — start rendering it live
4. When `session.status` goes `idle` — do a final `getHistory()` fetch to capture the complete last response, replace the streaming parts with the authoritative history

**Boundary detection:** A "new part" is identified by receiving a `message.part.updated` event where `part.id` does not match any part ID already rendered from the history fetch.

**Why not full dedup with `seq`:** The workspace-level SSE broker (`event_broker.go`) does not propagate `seq` numbers — it re-publishes events without sequencing. Adding broker-level sequencing is a larger change that can be done later if needed. Fetch-on-boundary is sufficient and simpler.

## Stories

| Story | Title | Priority | Depends On |
|-------|-------|----------|------------|
| US-15.1 | Session status polling on mount | Critical | — |
| US-15.2 | SSE-driven streaming state | Critical | US-15.1 |
| US-15.3 | History fetch on busy reconnect | High | US-15.1, US-15.2 |
| US-15.4 | Fetch-on-boundary live streaming | High | US-15.3 |
| US-15.5 | Final history reconciliation on idle | High | US-15.4 |
| US-15.6 | E2E tests | High | US-15.1–15.5 |

## Architecture

### State Machine

```
                    ┌─────────────────────────────────────────────┐
                    │                                             │
                    ▼                                             │
┌──────────┐  mount+poll  ┌──────────┐  SSE idle   ┌──────────┐ │
│   IDLE   │◄────────────│  BUSY    │────────────►│RECONCILE │─┘
│          │  status=idle  │          │             │          │
│ show     │               │ show     │             │ fetch    │
│ history  │  SSE busy     │ dots +   │             │ history  │
│ only     │──────────────►│ stream   │             │ replace  │
└──────────┘               │ parts    │             │ stream   │
                           └──────────┘             └──────────┘
                                ▲                        │
                                │    SSE new part        │
                                │    (boundary)          │
                                └────────────────────────┘
```

### Data Flow (Reconnect Scenario)

```
1. Page loads → ChatPage mounts
2. useWorkspaceStatus() → GET /workspaces/:id/status → sessions[].status
3. If session is "busy":
   a. setStreaming(true) — show bouncing dots
   b. getHistory() → render all messages as static
   c. useEventStream reconnects → SSE events start flowing
4. SSE message.part.updated arrives:
   a. Check: is part.id already in rendered history?
   b. If YES → ignore (already rendered from history fetch)
   c. If NO → new part, start rendering live via sseStreamParts
5. SSE session.status "idle" arrives:
   a. getHistory() → replace all messages with authoritative history
   b. Clear sseStreamParts
   c. setStreaming(false)
```

### Files Impacted

| File | Change |
|------|--------|
| `frontend/src/hooks/useChatStream.ts` | Add `externalBusy` state, expose `setBusy` for SSE-driven updates |
| `frontend/src/pages/ChatPage.tsx` | Poll status on mount, drive streaming from session status, implement boundary detection |
| `frontend/src/hooks/useEventStream.ts` | No changes needed (already reconnects) |
| `frontend/src/hooks/useMessageHistory.ts` | Add `refetch()` capability for on-demand history refresh |
| `frontend/src/api/types.ts` | Ensure `MessagePart.id` is typed (already present but unused) |
| `frontend/src/pages/ChatPage.sse.test.tsx` | New test cases for reconnect scenarios |

## Non-Goals

- **Seamless mid-sentence streaming on reconnect.** If the user reloads mid-sentence, they see the accumulated text as a static block. Live character-by-character streaming resumes only for the *next* part. This is acceptable UX.
- **Broker-level sequencing.** The backend broker does not gain `seq` numbers in this epic. That's a future optimization if fetch-on-boundary proves insufficient.
- **Multi-tab coordination.** The existing `BroadcastChannel` leader election in `events.ts` is unchanged. Only the leader tab connects to SSE; followers receive events via the channel.
- **Offline/PWA support.** No service worker caching of streaming state.

## Success Criteria

1. User refreshes page while agent is processing → bouncing dots appear immediately (within 1 network round-trip)
2. User refreshes page mid-stream → accumulated response renders as static text, new parts stream live
3. Agent finishes while page is loading → final history replaces any partial rendering
4. No duplicate messages or parts rendered at any point
5. No regressions: existing send → stream → idle flow works identically to today
