# Worklog: Epic 28 — Unified User-Scoped Event Stream (S28.1–S28.7)

**Date:** 2026-06-03
**Session:** Initial implementation of epic 28 — user-scoped SSE event stream
**Status:** In Progress

---

## Objective

Implement the unified user-scoped event stream (epic 28) to solve the sidebar staleness problem: workspace phase events for background workspaces were lost because SSE was workspace-scoped. The new architecture splits events into a user-scoped stream (workspace.phase for all workspaces) and a workspace-scoped session stream (in-session events only).

---

## Work Completed

### S28.1 — Broker Redesign (`event_broker_user.go`)
- New `UserEventBroker` with 16-shard FNV-32 keying for lock contention reduction (C1)
- `subscriber` struct with `closed` atomic flag and `missedEvent` overflow tracking
- `send()` uses `closed.Load()` guard + `recover()` fallback — race-free under `-race` (FP1)
- `SubscribeUser` / `UnsubscribeUser` with 20-connection-per-user limit returning `ErrTooManySubscribers` (FM8)
- `SubscribeWorkspace` / `UnsubscribeWorkspace` for session streams
- `PublishToUser` — assigns `event_id`, appends to replay buffer under shard lock, fans out outside lock (F6)
- `PublishToWorkspace` — no replay, direct fan-out
- `replayBuffer` ring buffer (128 entries) with `appendLocked()` / `sinceLocked()` (FM2, FM5)
- `Replay(userID, lastID)` returns `(entries, gapDetected)` for ring-wrap detection (F3)
- `RecordWorkspaceOwner` / `WorkspaceOwner` / `CleanupWorkspace` / `CleanupUser` (FM7, C5)
- `GetAllWorkspaceOwners()` for snapshot enumeration
- Added `EventID uint64` and `WorkspaceID string` fields to `WorkspaceSSEEvent` (F2)

### S28.2 — Watcher Fixes (`crd_watcher.go`)
- `seedResourceVersion` now iterates `list.Items`: populates `knownPhases` AND calls `broker.RecordWorkspaceOwner` (FM3, FM7)
- `handleEvent` handles `watch.Deleted`: removes from `knownPhases`, calls `broker.CleanupWorkspace` (C5)
- New `GetAllKnownPhases()` — one RLock, full map copy (G8)
- `SetUserBroker(broker)` setter, called from `ProxyHandler.Start()`

### S28.3 — User Stream Endpoint (`stream_user_events.go`)
- `StreamUserEvents` handler at `GET /api/v1/events`
- Auth guard: returns 401 if userID empty; 429 on `ErrTooManySubscribers`
- SSE headers with `X-Accel-Buffering: no`
- Write deadline via `http.NewResponseController` (30s window)
- Replay phase: reads `Last-Event-ID`, replays from buffer, emits `resync` on gap (F3, F5)
- Snapshot goroutine: k8s List with 5s timeout, filters empty phases (F4, G5)
- Heartbeat goroutine: sends `_heartbeat` sentinel into `s.ch` every 25s (FM1)
- Single-writer live loop: drains `s.ch`, writes to `c.Writer` with flush + deadline extension (F1)

### S28.4 — Phase Publish to User Stream (`proxy.go`)
- `onPhaseChange` now calls `broker.PublishToUser(userID, ...)` in addition to legacy `broker.Publish`
- Records workspace ownership on every phase change
- Events include `WorkspaceID` field

### S28.5 — Workspace Session Stream Route Rename
- Route changed from `/workspaces/:id/events` to `/workspaces/:id/session-events`
- Updated OpenAPI spec (`sdks/openapi.yaml`)
- Handler function unchanged (`StreamEvents`)

### S28.6 — Frontend `useUserEventStream` Hook
- Connects to `/api/v1/events` from root layout on mount
- `lastEventIDRef` initialized to `null`; `Last-Event-ID` header only sent on reconnect (F5)
- Tracks `event_id` from received events for replay
- On `workspace.phase`: invalidates `["workspaces"]` and `["workspace-status", workspace_id]`
- On `resync`: invalidates all workspace caches
- On reconnect: invalidates all workspace caches (FM9)
- Exponential backoff reconnection

### S28.7 — Frontend ChatPage Cleanup
- `useEventStream.ts` URL updated to `/workspaces/:id/session-events`
- Test updated to match

### Housekeeping
- Fixed pre-existing repolint duplicate worklog numbering (0134 → 0135)
- Rate limiter exempt paths updated: `["/events", "/session-events"]` (G1)
- OpenAPI contract test allowlist updated for new `/api/v1/events` route (G9)

---

## Key Decisions

1. **`closed` atomic flag instead of channel close in Unsubscribe**: Go's race detector catches concurrent `close(ch)` and `select { case ch <- evt }` even with `recover()`. Using an atomic flag eliminates the race entirely while maintaining the same semantics. The channel is never closed; the live loop exits via context cancellation.

2. **Kept legacy `WorkspaceEventBroker` alongside new `UserEventBroker`**: The existing per-workspace broker (`broker.Publish`) is retained for session-scoped events. The new user broker handles user-scoped events. Both coexist during this transition.

3. **Snapshot goroutine sends into `s.ch`**: Maintains single-writer invariant (F1). The live loop is the sole writer to `c.Writer`; snapshot and heartbeat goroutines communicate via the subscriber channel.

4. **k8s List timeout via goroutine+timer**: The `WorkspaceInterface.List` doesn't accept a context. Implemented a 5s timeout wrapper using a goroutine and timer instead.

---

## Blockers

None.

---

## Tests Run

```
go test -timeout 120s -race ./api/internal/handlers/ — PASS (13s)
go test -timeout 60s -race ./api/internal/server/ — PASS (1.5s)
go test -timeout 30s ./pkg/repolint/ — PASS
go test -timeout 30s ./pkg/types/ — PASS
go build ./... — PASS
```

Key test categories:
- 22 new `TestUserBroker_*` tests (sharding, replay, gap detection, overflow, concurrency, FP1)
- 8 new `TestStreamUserEvents_*` tests (auth, headers, live delivery, id lines, replay, heartbeat)
- 3 new `TestWorkspaceWatcher_*` tests (seed phases, deleted handler, GetAllKnownPhases)
- All existing handler/server tests pass (no regressions)

---

## Next Steps

1. **S28.8 — Full test coverage**: Add remaining integration tests per acceptance criteria:
   - Snapshot emits events before first live event
   - Snapshot skip + resync on k8s list failure
   - Write deadline behavior test
   - Goroutine leak test on write error
   - Update `TestStreamEvents_OnPhaseChange_PublishesToBroker` for new broker
   - Frontend tests for `useUserEventStream`

2. **Wire `useUserEventStream` into root layout**: Import and call from `App.tsx` or root layout component

3. **Remove `workspace.phase` handling from `useEventStream`**: ChatPage should no longer react to phase events from the session stream (they won't be published there after full cutover)

4. **Validator pass**: Run skeptical validation per orchestrator workflow step 3

---

## Files Modified

### New Files
- `api/internal/handlers/event_broker_user.go` — UserEventBroker implementation
- `api/internal/handlers/event_broker_user_test.go` — 22 tests
- `api/internal/handlers/stream_user_events.go` — StreamUserEvents handler
- `api/internal/handlers/stream_user_events_test.go` — 8 tests
- `frontend/src/hooks/useUserEventStream.ts` — User-scoped SSE hook
- `worklogs/0136_2026-06-03_epic-28-implementation-s28.1-s28.7.md` — This worklog

### Modified Files
- `api/internal/handlers/event_broker.go` — Added EventID, WorkspaceID fields to WorkspaceSSEEvent
- `api/internal/handlers/crd_watcher.go` — seedResourceVersion populates knownPhases, Deleted handler, GetAllKnownPhases, SetUserBroker
- `api/internal/handlers/crd_watcher_test.go` — 3 new tests
- `api/internal/handlers/proxy.go` — Added userBroker field, Start() creates it, onPhaseChange publishes to user stream
- `api/internal/server/router.go` — New /api/v1/events route, /session-events rename, ExemptPaths update
- `api/internal/server/router_openapi_contract_test.go` — Allowlist for /api/v1/events
- `frontend/src/hooks/useEventStream.ts` — URL → /session-events
- `frontend/src/hooks/useEventStream.test.ts` — URL assertion updated
- `sdks/openapi.yaml` — Path renamed to /session-events

### Renamed Files
- `worklogs/0134_2026-06-03_epic-28-unified-event-stream-design.md` → `worklogs/0135_2026-06-03_epic-28-unified-event-stream-design.md`
