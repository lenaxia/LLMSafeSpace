# Worklog: SSE-driven Workspace Phase Events

**Date:** 2026-05-25
**Session:** Replace polling-based workspace status updates with SSE-driven phase change events so the sidebar icon and ChatPage banner stay in sync with actual workspace state.
**Status:** Complete

---

## Objective

The workspace status icon in the sidebar and the transitioning/suspended banners in `ChatPage` could get permanently out of sync with the real workspace phase because:

1. `useWorkspaceStatus` polled `GET /workspaces/:id/status` every 2 s only during transitional phases (`Pending`, `Resuming`, `Suspending`, `Creating`) — polling stopped completely once the workspace reached `Active` or `Suspended`.
2. The `WorkspaceWatcher` (K8s watch loop) detected phase changes server-side but its `onPhaseChange` callback only managed internal server caches — nothing reached the browser.
3. `StreamEvents` was a transparent proxy to the pod's `/event` endpoint. The pod only emits `session.status` events. The pod has no knowledge of its own workspace lifecycle phase.
4. The existing `handleSSEEvent` in `ChatPage` checked `event.session` — a shape mismatch against the actual pod event format `{ type, session_id, status }` — so session invalidation was also silently broken.

---

## Assumptions Validated Before Implementation

1. **`StreamEvents` is a transparent proxy** — confirmed at `proxy.go:178`: `h.proxyToWorkspace(c, "/event", false, "", false)`. No event injection possible without replacing this.
2. **Pod SSE stream only emits `session.status`** — confirmed at `session_tracker.go:209`: `if evt.Type != "session.status" ...`. No workspace phase events come from the pod.
3. **`WorkspaceWatcher` fires `onPhaseChange` on every phase transition** — confirmed at `crd_watcher.go:217–219`. This is the authoritative source of truth for phase changes.
4. **`SSETracker` callbacks are the authoritative source of session idle/busy** — confirmed at `session_tracker.go:213–220`. Already wired into `onSessionIdle` / `onSessionActive` in `proxy.go`.
5. **`handleSSEEvent` shape mismatch** — confirmed: `ChatPage.tsx:54` checks `event.session` but pod sends `{ type: "session.status", session_id, status }`. Session invalidation was broken.
6. **Frontend tests were broken before this session** due to vitest 4.x + rolldown requiring Node 20+, environment has Node 18. Fixed as part of this work by pinning vite@5 + vitest@2 + jsdom@20.

---

## Work Completed

### Backend

**New file: `api/internal/handlers/event_broker.go`**
- `WorkspaceSSEEvent` — canonical typed struct for all SSE events; fields: `type`, `phase`, `session_id`, `status`; `omitempty` ensures only relevant fields appear in JSON
- `WorkspaceEventBroker` — thread-safe pub/sub fan-out keyed by workspace ID; buffered channels (`brokerChannelBuffer = 16`) per subscriber; slow consumers get events dropped (non-blocking publish) rather than blocking publishers; `Unsubscribe` closes the subscriber channel so `range` loops and `select` cases detect done correctly

**Modified: `api/internal/handlers/proxy.go`**
- `ProxyHandler` gains `broker *WorkspaceEventBroker` field
- `Start()` initialises the broker before all other components
- `StreamEvents` — **fully replaced**: was a one-liner transparent proxy (`proxyToWorkspace(c, "/event", ...)`); now a proper SSE handler that:
  - Verifies the workspace exists (404 if not), does not require Active phase (client may connect while resuming)
  - Sets `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`, `X-Accel-Buffering: no`
  - Subscribes to the broker for the workspace ID
  - Loops on `select { ctx.Done() | event from channel }`, writing `data: <json>\n\n` and flushing
  - Defers `broker.Unsubscribe` so disconnected clients are cleaned up regardless of exit path
- `onPhaseChange` — publishes `{type:"workspace.phase", phase:"..."}` to the broker **before** existing cache invalidation logic (all phases published, including Active, Suspending, Suspended, Terminating, Terminated)
- `onSessionIdle` — publishes `{type:"session.status", session_id, status:"idle"}` to the broker after removing the active session
- `onSessionActive` — publishes `{type:"session.status", session_id, status:"busy"}` to the broker after registering the session

**New test files (TDD — written before implementation):**
- `event_broker_test.go` — 10 unit tests: subscribe/receive, multi-subscriber fan-out, cross-workspace isolation, unsubscribe stops delivery, unsubscribe closes channel, noop on missing subscriber, non-blocking with no subscribers, slow subscriber doesn't block others, concurrent publish/subscribe safety, session status event delivery
- `stream_events_test.go` — 8 integration tests: workspace not found returns 404, SSE headers set correctly, phase event delivered to client, session status event delivered to client, client disconnect unsubscribes from broker, `onPhaseChange` publishes to broker for all phases, `onSessionIdle` publishes idle event, `onSessionActive` publishes busy event

**Modified: `api/internal/handlers/proxy_test.go`**
- `TestProxy_StreamingResponse` — updated to verify SSE headers via broker-based handler (no longer tests transparent proxy)
- `TestProxy_SSEStreamPassthrough` — replaced with explanatory comment (transparent proxy behaviour no longer exists)
- `TestProxy_EndpointMapping` — removed `events` entry (StreamEvents no longer proxies to pod)
- `TestProxy_E2E_FullFlow` — removed events step and `GET /event` from expected backend request list

### Frontend

**Modified: `frontend/src/api/types.ts`**
- Added `WorkspacePhaseEvent`, `SessionStatusEvent`, `WorkspaceStreamEvent` discriminated union — typed against the backend `WorkspaceSSEEvent` wire format; `session_id` snake_case matches Go JSON output

**Modified: `frontend/src/hooks/useWorkspaces.ts`**
- `useWorkspaceStatus` — removed `refetchInterval` entirely; now a plain `useQuery` with `staleTime: 0`; updates driven by SSE invalidation only; `staleTime: 0` ensures `invalidateQueries` always triggers a re-fetch

**Modified: `frontend/src/pages/ChatPage.tsx`**
- `handleSSEEvent` — narrows on `event.type` (discriminated union):
  - `workspace.phase` → invalidates `["workspaces"]` and `["workspace-status", workspaceId]` (sidebar icon + ChatPage banner both update)
  - `session.status` → invalidates `["sessions", workspaceId]`
  - Unknown types → silently ignored (no invalidation)
- Fixes the pre-existing shape mismatch (`event.session` → `event.type`)
- Imports `WorkspaceStreamEvent` type for type safety

**Modified: `frontend/src/hooks/useWorkspaces.test.tsx`**
- Added 4 new tests: no polling for Active phase, no polling for Suspended phase, no polling for any transitional phase, re-fetch triggered by cache invalidation

**New file: `frontend/src/pages/ChatPage.sse.test.tsx`**
- 7 tests: `workspace.phase` invalidates workspace-status query, `workspace.phase` invalidates workspaces list query, `session.status` invalidates sessions query, `session.status` does not invalidate workspace-status, `workspace.phase` does not invalidate sessions, unknown type is ignored, old `event.session` shape does not trigger invalidation (regression test)

**Modified: `frontend/package.json` + `frontend/package-lock.json`**
- Downgraded `vite` to v5 and `vitest` to v2, added `jsdom@20`, `@testing-library/dom` — required to run frontend tests on Node 18 (pre-existing breakage, not introduced by this session)

---

## Key Decisions

**SSE terminates at the API server, not the pod.** The browser SSE connection no longer reaches the pod. The pod disappearing (suspend, terminate, restart) does not kill the browser's event stream. The API server is the authority for workspace lifecycle events (via K8s watch) and proxies session events from the pod via the `SSETracker`.

**All phases published to broker.** `onPhaseChange` publishes for every phase transition including `Active` — this is correct because a workspace becoming Active after Resuming should immediately update the sidebar without waiting for polling.

**Broker channel closed on unsubscribe.** `Unsubscribe` closes the channel rather than just removing the reference. This means `StreamEvents` handler's `select` on the channel will receive a zero value with `open=false` and return cleanly — no goroutine leak.

**No polling removed from `useWorkspaceStatus` for transitional states.** Transitional state updates (Pending → Creating → Active during workspace boot) are now driven by SSE, not polling. This eliminates all polling — the hook is now purely reactive.

---

## Blockers

None.

---

## Tests Run

**Backend:**
- All `api/internal/handlers/` tests: 54 tests pass (18 new)
- `api/internal/services/workspace/TestDeleteWorkspace_HappyPath` — pre-existing failure, unrelated to this change (confirmed by git stash verification)

**Frontend:**
- All 54 test files, 277 tests pass (14 new)

---

## Files Modified

- `api/internal/handlers/event_broker.go` (new)
- `api/internal/handlers/event_broker_test.go` (new)
- `api/internal/handlers/stream_events_test.go` (new)
- `api/internal/handlers/proxy.go`
- `api/internal/handlers/proxy_test.go`
- `frontend/src/api/types.ts`
- `frontend/src/hooks/useWorkspaces.ts`
- `frontend/src/hooks/useWorkspaces.test.tsx`
- `frontend/src/pages/ChatPage.tsx`
- `frontend/src/pages/ChatPage.sse.test.tsx` (new)
- `frontend/package.json`
- `frontend/package-lock.json`
- `worklogs/0042_2026-05-25_sse-workspace-phase-events.md` (this file)
