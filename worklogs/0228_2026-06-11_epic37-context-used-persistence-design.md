# Worklog: Epic 37 — Context Usage Persistence Design

**Date:** 2026-06-11
**Session:** Design document for durable context_used persistence via session_index
**Status:** In Progress

---

## Objective

Fix the context usage bar regression introduced by PR #91 (Epic 36). The bar disappears for sessions with history because `context_used` is stored only in agentd's ephemeral in-memory SSE tracker, which is wiped on pod restart, is gated on opencode's live session list, and relies on a 30-second background goroutine with `omitempty` zero-ambiguity.

Design and implement a proper, durable solution that:
- Persists `context_used` per session in PostgreSQL (`session_index` table)
- Writes it in the API proxy layer (already intercepts all SSE events from opencode)
- Surfaces it through the `GET /workspaces/:id/sessions` endpoint (already reads `session_index`)
- Delivers real-time updates to the frontend via the existing `opencode.event` SSE stream
- Eliminates all the agentd-side complexity (fillGaps, promptTokens tracker, per-session ContextUsed in CRD path) in a follow-up cleanup PR

---

## Problem Analysis

### Root cause of current regression

1. `ChatPage` reads `contextUsed` from `sessionStatus?.contextUsed` — found by looking up the current `sessionId` in `status?.sessions[]` (the CRD-path sessions list)
2. `status?.sessions[]` comes from `GetWorkspaceStatus` → CRD → agentd statusz → `sessionStatusTracker.promptTokens` (in-memory)
3. The tracker is only populated by `session.next.step.ended` SSE events (real-time) or by `fillGaps` (30s background fetch)
4. For sessions with history but no recent activity, the tracker is empty → `ContextUsed=0` → `omitempty` drops it from JSON → frontend gets `undefined` → bar hides

### Why the existing approach is wrong

- **Ephemeral state**: agentd tracker is wiped on every pod restart
- **Wrong data source**: reads from CRD (controller-polled 60s) not from the durable session store
- **Wrong layer**: context usage is per-session metadata, exactly what `session_index` is for
- **SSE already arrives at the proxy**: the API proxy's `SSETracker` already receives `session.next.step.ended` and forwards it to the browser. The same event needs to be persisted to DB.

---

## Design

### Architecture

```
opencode pod → SSE: session.next.step.ended
  → proxy SSETracker.processEvent (already running)
  → onRawEvent callback (already wired: proxy.go:179)
  → persistContextFromEvent (NEW — same pattern as persistTitleFromEvent)
  → session_index.context_used = input + cache.read + cache.write  (durable)

GET /workspaces/:id/sessions
  → ListSessionIndex (already queries session_index)
  → SessionListItem.ContextUsed  (NEW field — surfaced for free)

Frontend ChatPage / Sidebar:
  → reads contextUsed from SessionListItem (sessions query, not status query)
  → ALSO: handles session.next.step.ended in opencode.event SSE for real-time update
    → stores in contextBySessionRef (useRef map)
    → DiskUsageBar reads from ref first, falls back to SessionListItem value
```

### What does NOT change

- `context_total` path (CRD ← agentd statusz) — untouched
- agentd's existing fillGaps / SSE tracker — untouched (cleanup in separate PR)
- GetWorkspaceStatus — untouched (still reads from CRD)
- Controller deep-status poll — untouched

---

## Validated Assumptions

| # | Assumption | Validation |
|---|---|---|
| A1 | `session.next.step.ended` event shape: `{type, properties: {sessionID, tokens: {input, output, reasoning, cache: {read, write}}}}` | Confirmed: `cmd/workspace-agentd/main.go:432-449` (handleStepEnded), test data in `cmd/workspace-agentd/main_test.go:573` |
| A2 | Proxy SSETracker calls `onRawEvent(workspaceID, eventType, rawData)` for ALL events including step.ended | Confirmed: `session_tracker.go:310-312` — `onRawEvent` called before `dispatchProperties`; every event goes through |
| A3 | `persistTitleFromEvent` is the established pattern for proxy-side DB persistence on SSE events | Confirmed: `proxy.go:1282-1283` and `opencode_upgrade_test.go:63-149` |
| A4 | `session_index` table has `(workspace_id, session_id)` primary key supporting upserts | Confirmed: `000003_session_index.up.sql:7` — `PRIMARY KEY (workspace_id, session_id)` |
| A5 | `ListSessionIndex` query is the DB function called by `ListWorkspaceSessions` which feeds `GET /workspaces/:id/sessions` | Confirmed: `workspace_service.go:914-922`, `database.go:779` |
| A6 | Frontend `SessionListItem` type is what's returned by `getSessions` and used by Sidebar and ChatPage sessions query | Confirmed: `frontend/src/api/types.ts:65-74`, `frontend/src/hooks/useSessions.ts:6-7` |
| A7 | `opencode.event` with `event_type="session.next.step.ended"` already arrives in the browser (forwarded by proxy) | Confirmed: `session_tracker.go:310` fires `onRawEvent` → `proxy.go:1275-1279` publishes `opencode.event` to broker → frontend SSE stream |
| A8 | API server runs 2 replicas (`values.yaml:27`); both will try to upsert — idempotent because both write same value | Confirmed: `charts/llmsafespace/values.yaml:27` replicaCount=2; upsert with ON CONFLICT is idempotent for same value |
| A9 | `UpsertSessionTitle` (synchronous, direct) is the right pattern to follow — not `RecordMessage` (async queue) | Confirmed: step.ended fires at most once per LLM call (very low frequency); sync upsert appropriate |
| A10 | EnsureWatching only fires on browser SSE subscribe or write op; API replica restart loses all subscriptions | Confirmed: `proxy.go:370-371, 515-516` — only on browser subscribe or write op; new replica starts with empty `subscriptions` map |

### Failed / Revised Assumption

| # | Original | Reality | Impact |
|---|---|---|---|
| — | "merge context_used into GetWorkspaceStatus" | Wrong — adds DB call to hot status path, conflates two data sources | **Revised**: read from sessions list (`ListWorkspaceSessions`) not status response |

---

## Implementation Plan

### Story 1: DB migration + DB layer
- Migration `000022_session_index_context.up.sql`: `ALTER TABLE session_index ADD COLUMN IF NOT EXISTS context_used BIGINT;`
- `DatabaseService` interface: `UpsertSessionContextUsed(ctx, workspaceID, sessionID string, contextUsed int64) error`
- `database.go`: implement with ON CONFLICT upsert (same pattern as UpsertSessionTitle)
- `DatabaseService` mock: add method stub
- **Tests first**: `TestUpsertSessionContextUsed_*`

### Story 2: Session index service layer
- `SessionIndexService` interface: `UpsertContextUsed(ctx, workspaceID, sessionID string, contextUsed int64) error`
- `sessionindex/service.go`: delegate to DB (synchronous, same as UpsertTitle)
- Mock in `opencode_upgrade_test.go` and `api/internal/mocks/`: add `UpsertContextUsed`
- **Tests first**: `TestUpsertContextUsed_DelegatesToDB`

### Story 3: ListSessionIndex surfaces context_used
- Add `context_used` to `SELECT` in `ListSessionIndex` query
- Add `ContextUsed *int64` to `types.SessionListItem` (pointer: NULL from DB = nil, distinguishable from 0)
- Scan `context_used` (nullable BIGINT → `sql.NullInt64`) into `item.ContextUsed`
- **Tests first**: `TestListSessionIndex_IncludesContextUsed`

### Story 4: Proxy — persistContextFromEvent
- Add `persistContextFromEvent(workspaceID, rawData string)` method to `ProxyHandler`
- Parses step.ended shape: `{properties: {sessionID, tokens: {input, cache: {read, write}}}}`
- Computes `promptTokens = input + cache.read + cache.write`
- Calls `sessionIndex.UpsertContextUsed`
- Wire into `onRawEvent`: `if eventType == "session.next.step.ended" && h.sessionIndex != nil`
- **Tests first**: `TestPersistContextFromEvent_*` (6 test cases) in `opencode_upgrade_test.go`

### Story 5: Startup EnsureWatching for active workspaces
- In `proxy.go` `Initialize()` or `WorkspaceWatcher` start: after watcher starts, call `EnsureWatching` for all known-Active workspaces
- Mitigation for Gap 1 (SSETracker not watching after API restart)
- **Tests first**: `TestEnsureWatchingOnStartup_ActiveWorkspaces`

### Story 6: Frontend — SessionListItem + sessions-list read path
- `frontend/src/api/types.ts`: add `contextUsed?: number` to `SessionListItem`
- `frontend/src/pages/ChatPage.tsx`: read `contextUsed` from sessions query data (not `sessionStatus?.contextUsed`)
  - `const sessionsData = queryClient.getQueryData<SessionListItem[]>(["sessions", workspaceId])`
  - `const currentSession = sessionsData?.find(s => s.id === sessionId)`
  - `contextUsed = contextBySessionRef.current.get(sessionId) ?? currentSession?.contextUsed`
- Compaction detection: watch the same derived value
- Sidebar: already reads from sessions list — gets `contextUsed` for free once type is updated

### Story 7: Frontend — real-time SSE update
- In ChatPage `opencode.event` handler: add `session.next.step.ended` case
- Extract `properties.sessionID` and compute `input + cache.read + cache.write`
- Store in `contextBySessionRef = useRef<Map<string, number>>(new Map())`
- Trigger re-render with a state counter: `const [contextVersion, setContextVersion] = useState(0)`
- `DiskUsageBar contextUsed={contextBySessionRef.current.get(sessionId) ?? currentSession?.contextUsed}`

---

## Failure Modes and Mitigations

| Failure | Mitigation |
|---|---|
| API replica restarts, loses SSE subscriptions | Story 5: EnsureWatching on startup for all Active workspaces |
| First step-ended event missed (SSE connection race on first write) | Self-heals on second LLM call; DB value from prior sessions shown until then |
| Dual-write from 2 replicas | Idempotent upsert (same value); benign |
| context_used NULL for brand-new sessions | Frontend treats `undefined` as "not yet known" — DiskUsageBar hides until first step-end; correct behavior |
| opencode pod restart, sessions not in opencode memory | Irrelevant — proxy persists to DB on every event regardless of opencode state |

---

## Files to Create/Modify

**New:**
- `api/migrations/000022_session_index_context.up.sql`
- `api/migrations/000022_session_index_context.down.sql`

**Modified:**
- `api/internal/interfaces/interfaces.go` — `DatabaseService`, `SessionIndexService`
- `api/internal/services/database/database.go` — `UpsertSessionContextUsed`, `ListSessionIndex`
- `api/internal/services/sessionindex/service.go` — `UpsertContextUsed`
- `api/internal/handlers/proxy.go` — `onRawEvent` wiring + `persistContextFromEvent`
- `api/internal/handlers/opencode_upgrade_test.go` — tests for `persistContextFromEvent`, mock update
- `api/internal/services/database/database_test.go` — tests for `UpsertSessionContextUsed`, `ListSessionIndex`
- `api/internal/services/sessionindex/service_test.go` — tests for `UpsertContextUsed`
- `api/internal/mocks/database.go` — mock stub
- `pkg/types/types.go` — `SessionListItem.ContextUsed *int64`
- `frontend/src/api/types.ts` — `SessionListItem.contextUsed?: number`
- `frontend/src/pages/ChatPage.tsx` — SSE handler, sessions-list read path, ref map
- `frontend/src/components/workspace/DiskUsageBar.tsx` — (no change needed if ChatPage wires correctly)

---

## Key Decisions

1. **Read from sessions list, not status response** — keeps `GetWorkspaceStatus` as a pure CRD read; no new DB call on status hot path
2. **Pointer `*int64` for `ContextUsed`** — NULL from DB maps to nil, distinguishable from 0; `omitempty` on pointer means nil is omitted, 0 is included
3. **Synchronous upsert (not queued)** — same as UpsertTitle; step.ended fires at most once per LLM call, not high frequency
4. **Separate cleanup PR for agentd** — don't delete fillGaps/promptTokens until new path is proven in production

---

## Blockers

None.

---

## Tests Run

None yet — this is the design doc. Implementation begins next.

---

## Next Steps

Implement in story order: DB migration → DB layer → session index service → ListSessionIndex → proxy handler → startup EnsureWatching → frontend.

---

## Files Modified

- `worklogs/0228_2026-06-11_epic37-context-used-persistence-design.md` (this file)
