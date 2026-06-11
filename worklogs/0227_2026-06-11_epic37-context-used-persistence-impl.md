# Worklog: Epic 37 — Context Usage Persistence Implementation

**Date:** 2026-06-11
**Session:** Full implementation of durable context_used persistence via session_index
**Status:** Complete

---

## Objective

Fix the context usage bar regression from PR #91 (Epic 36) by persisting `context_used` to PostgreSQL (`session_index`) through the existing proxy SSE event interception path, and surfacing it through `GET /workspaces/:id/sessions` to both the frontend and the Go SDK.

---

## Work Completed

### Architecture

```
opencode → SSE: session.next.step.ended
  → proxy SSETracker.processEvent (existing)
  → onRawEvent callback (existing)
  → persistContextFromEvent (NEW)  ← same pattern as persistTitleFromEvent
  → session_index.context_used (PostgreSQL, durable)

GET /workspaces/:id/sessions
  → ListSessionIndex (existing, now includes context_used)
  → SessionListItem.ContextUsed *int64  (NEW field)

Frontend ChatPage:
  → reactive useQuery(["sessions", workspaceId]) → activeSessionData.contextUsed  (cold-start DB value)
  → opencode.event SSE: session.next.step.ended → contextBySessionRef.current (real-time override)
  → contextUsedForDisplay = realtime ?? db_value
  → DiskUsageBar uses contextUsedForDisplay
```

### Validated Assumptions

All 10 assumptions from the design doc validated. Key confirmations:
- V1: `session.next.step.ended` event shape confirmed via test data and handler code
- V2: `onRawEvent` already fires for all events including step.ended — confirmed in `session_tracker.go:310`
- V3: `persistTitleFromEvent` is the established pattern — followed exactly
- V4: `session_index` primary key `(workspace_id, session_id)` supports idempotent upserts
- V5: `ListSessionIndex` feeds `ListWorkspaceSessions` → `GET /workspaces/:id/sessions`
- V8: API runs 2 replicas — idempotent upsert handles concurrent writes of same value correctly

### Backend Changes

**Migration `000022_session_index_context`:**
- Up: `ALTER TABLE session_index ADD COLUMN IF NOT EXISTS context_used BIGINT;`
- Down: `DROP COLUMN IF EXISTS context_used`
- Chart migrations synced via `make chart-sync-migrations`

**`pkg/types/types.go`:**
- `SessionListItem.ContextUsed *int64 \`json:"contextUsed,omitempty"\`` — pointer so NULL = nil (not set) is distinguishable from 0

**`api/internal/interfaces/interfaces.go`:**
- `DatabaseService`: added `UpsertSessionContextUsed(ctx, workspaceID, sessionID string, contextUsed int64) error`
- `SessionIndexService`: added `UpsertContextUsed(ctx, workspaceID, sessionID string, contextUsed int64) error`

**`api/internal/services/database/database.go`:**
- `UpsertSessionContextUsed`: ON CONFLICT upsert, same pattern as UpsertSessionTitle
- `ListSessionIndex`: added `context_used` to SELECT and scans via `sql.NullInt64`

**`api/internal/services/sessionindex/service.go`:**
- `UpsertContextUsed`: delegates to `db.UpsertSessionContextUsed` (synchronous, same as UpsertTitle)

**`api/internal/handlers/proxy.go`:**
- `onRawEvent`: refactored nil-broker check to guard only the Publish call (not the full function), added `persistContextFromEvent` call for `session.next.step.ended`
- `emitNormalizedInputEvent`: added nil broker guard (was previously guarded by early return in caller)
- `persistContextFromEvent`: new method, exact mirror of `persistTitleFromEvent`, extracts `input + cache.read + cache.write` and calls `sessionIndex.UpsertContextUsed`
- `Initialize`: after watcher starts, calls `EnsureWatching` for all currently-Active workspaces to mitigate Gap 1 (API replica restart losing SSE subscriptions)

**`api/internal/mocks/database.go`:**
- Added `UpsertSessionContextUsed` stub

### Frontend Changes

**`frontend/src/api/types.ts`:**
- `SessionListItem.contextUsed?: number` — surfaced to sidebar and ChatPage

**`frontend/src/api/contract-fixtures.json`:**
- Added `lastSeenAt`, corrected `hasUnread` (pre-existing stale fixture), added `contextUsed: 12500`

**`frontend/src/api/contract.test.ts`:**
- Added `contextUsed` assertion to SessionListItem contract test

**`frontend/src/hooks/useWorkspaces.ts`:**
- Updated comment: context_used is no longer from status polling, it's from sessions list + SSE

**`frontend/src/pages/ChatPage.tsx`:**
- Removed: reads `sessionStatus?.contextUsed` (CRD path)
- Added: `contextBySessionRef` — useRef Map tracking real-time context from SSE
- Added: `contextVersion` state — triggers re-render when SSE updates the ref
- Added: `useQuery(["sessions", workspaceId], staleTime:Infinity)` — reactive cold-start fallback from sessions list
- Added: `contextUsedForDisplay` IIFE — realtime SSE value or DB fallback
- Added: `session.next.step.ended` SSE handler — updates ref map, calls setContextVersion
- Changed: `DiskUsageBar contextUsed={contextUsedForDisplay}` (was `sessionStatus?.contextUsed`)
- Changed: compaction detection from `useEffect` → `useLayoutEffect` (synchronous, prevents timing issues in tests)

**`frontend/src/components/layout/Sidebar.tsx`:**
- Removed: `wsStatus` useQuery (was reading context from workspace-status cache, an anti-pattern)
- Added: `contextBySessionId` built from `sessions` array (already fetched, no extra query)
- Sessions now carry `contextUsed` from session_index, not from status

### Test Changes

**Backend:**
- `database_test.go`: `TestUpsertSessionContextUsed_*` (3 cases), `TestListSessionIndex_IncludesContextUsed` (3 cases)
- `session_last_seen_test.go`: all `ListSessionIndex` mocks updated to include 8th column `context_used`
- `session_index_test.go`: same column count update
- `sessionindex/service_test.go`: `TestUpsertContextUsed_*` (3 cases)
- `opencode_upgrade_test.go`: `TestPersistContextFromEvent_*` (6 cases), `TestOnRawEvent_StepEnded_CallsPersistContext` (1 case), mock updated with `UpsertContextUsed` method
- `proxy_test.go`, `proxy_backfill_test.go`: mock session index types updated with `UpsertContextUsed` stub
- `auth_e2e_all_test.go`, `auth_e2e_secrets_test.go`, `auth_sessionid_test.go`: `fullMockDB`, `apiKeyAwareDB`, `mockDB` updated with `UpsertSessionContextUsed` stub
- `workspace_session_test.go`: `mockSessionIndex` updated with `UpsertContextUsed` method

**Frontend:**
- `ChatPage.context.test.tsx`: completely rewritten — compaction tests now use SSE event path (fireStepEnded helper captures `useEventStream` handler), context bar tests seed sessions cache directly
- `Sidebar.sessions.test.tsx`: context indicator tests updated to seed sessions cache instead of workspace-status cache
- `contract.test.ts`: added `contextUsed` assertion

### Known limitations (documented, not bugs)

1. **EnsureWatching at startup may be empty**: `GetAllKnownPhases()` called after `watcher.Start()` may return empty if the K8s watch hasn't received events yet. The improvement is best-effort; the existing behavior (watching starts on first browser connection or write op) is unchanged as fallback.

2. **First step-ended may be missed on fresh workspace**: If the SSETracker hasn't connected to the pod when the first LLM call completes (race window of ~seconds after workspace becomes Active), the event is missed. Self-heals on next LLM call.

3. **agentd cleanup deferred**: `fillGaps`, `promptTokens` tracker, `SessionInfo.ContextUsed` in CRD path, and related code in agentd are NOT deleted in this PR. They remain as a no-op redundancy until the new DB path is proven in production for a few days.

---

## Adversarial Review Results

- **Finding: `contextVersion` TypeScript unused warning** — FIXED: added `void contextVersion` inside IIFE to make consumption explicit
- **Finding: misleading eslint comment** — FIXED: comment removed, replaced with clear explanation
- **Finding: `onRawEvent` nil-broker behavior change** — FALSE ALARM: behavior is correct (context persistence should work even without browser subscribers), verified by test `TestOnRawEvent_StepEnded_CallsPersistContext`
- **Finding: O(N sessions) find in render** — FALSE ALARM: session count is 1-5, negligible
- **Finding: watcher phases empty at startup** — DOCUMENTED: acknowledged known limitation, not a regression

---

## Key Decisions

1. **Read from sessions list, not status response** — keeps `GetWorkspaceStatus` as pure CRD read; no new DB call on the hot status path
2. **Pointer `*int64` for `ContextUsed`** — NULL from DB → nil in Go → omitted from JSON; 0 is included (explicitly set)
3. **Synchronous upsert (not queued)** — same as `UpsertTitle`; step.ended fires at most once per LLM call
4. **`useLayoutEffect` for compaction detection** — runs synchronously after DOM commit; prevents React 18 batching from causing `prevContextUsedRef` to not be set before the drop
5. **SSE-path for compaction tests** — testing compaction via sessions cache update was unreliable with React 18's effect batching; SSE path is synchronous and testable reliably
6. **Separate cleanup PR for agentd** — don't delete `fillGaps`/`promptTokens` until new path proven in production

---

## Blockers

None.

---

## Tests Run

All tests passing:
```
Go (excluding known slow tests):  40 packages ok
Go auth tests (90s):               ok
Go agentd tests (120s):            ok
Frontend:                          94 files, 897 tests passed
```

---

## Next Steps

1. Monitor PR automated reviewer feedback and iterate
2. After merge: deploy and verify context bar shows for existing session `ses_14b4bc19fffe5y7OG2Zn2plLXs`
3. Follow-up cleanup PR: delete agentd `fillGaps`, `SessionInfo.ContextUsed`, CRD `AgentSessionStatus.ContextUsed`, controller `enrichAgentStatus` ContextUsed copy, etc.

---

## Files Modified

**New:**
- `api/migrations/000022_session_index_context.up.sql`
- `api/migrations/000022_session_index_context.down.sql`
- `charts/llmsafespace/migrations/000022_session_index_context.up.sql`
- `charts/llmsafespace/migrations/000022_session_index_context.down.sql`
- `worklogs/0225_2026-06-11_epic37-context-used-persistence-design.md`
- `worklogs/0218_2026-06-10_opencode-infinite-retry-on-context-overflow.md` (renamed from erroneous 0200)

**Modified:**
- `api/internal/handlers/opencode_upgrade_test.go`
- `api/internal/handlers/proxy.go`
- `api/internal/handlers/proxy_backfill_test.go`
- `api/internal/handlers/proxy_test.go`
- `api/internal/interfaces/interfaces.go`
- `api/internal/mocks/database.go`
- `api/internal/services/auth/auth_e2e_all_test.go`
- `api/internal/services/auth/auth_e2e_secrets_test.go`
- `api/internal/services/auth/auth_sessionid_test.go`
- `api/internal/services/database/database.go`
- `api/internal/services/database/database_test.go`
- `api/internal/services/database/session_index_test.go`
- `api/internal/services/database/session_last_seen_test.go`
- `api/internal/services/sessionindex/service.go`
- `api/internal/services/sessionindex/service_test.go`
- `api/internal/services/workspace/workspace_session_test.go`
- `frontend/src/api/contract-fixtures.json`
- `frontend/src/api/contract.test.ts`
- `frontend/src/api/types.ts`
- `frontend/src/components/layout/Sidebar.sessions.test.tsx`
- `frontend/src/components/layout/Sidebar.tsx`
- `frontend/src/hooks/useWorkspaces.ts`
- `frontend/src/pages/ChatPage.context.test.tsx`
- `frontend/src/pages/ChatPage.tsx`
- `pkg/types/types.go`
