# Worklog: Epic 37 — Session Activity & Unread State UX (Backend Complete)

**Date:** 2026-06-10
**Session:** Full implementation of Epic 37 backend stories (US-37.1, US-37.2, US-37.3) with TDD
**Status:** Complete (backend Phase 1)

---

## Objective

Implement Epic 37 Phase 1 backend: publish session status to user-scoped SSE, merge active status into REST, add last_seen_at/hasUnread/mark-seen endpoint.

---

## Work Completed

### US-37.1: Publish session.status to user-scoped SSE stream

**Implementation:** Added user-broker publish calls after workspace-broker publish in both `onSessionIdle` and `onSessionActive` in `proxy.go`. Each block: nil-check `userBroker`, look up workspace owner, publish event with `workspace_id`, `session_id`, `status`.

**Tests (5):** All pass
- `TestOnSessionIdle_PublishesToUserBroker` — idle event delivered with correct fields
- `TestOnSessionActive_PublishesToUserBroker` — busy event delivered
- `TestOnSessionIdle_SkipsUserBrokerWhenOwnerUnknown` — silently skipped
- `TestOnSessionIdle_NoPanicWhenUserBrokerNil` — nil safety
- `TestOnSessionActive_NoPanicWhenUserBrokerNil` — nil safety

### US-37.2: Merge active session status into session list REST

**Implementation:** After `ListWorkspaceSessions` in router.go, if `proxyHandler != nil`, call `GetActiveSessions`, build a set, and stamp `Status = "active"` on matching sessions. Added `SetActiveSessionsForTest` helper on ProxyHandler.

**Tests (3):** All pass
- `TestListWorkspaceSessions_MergesActiveStatus` — 1 of 3 sessions marked active
- `TestListWorkspaceSessions_AllIdleWhenNoProxyHandler` — nil proxy handler safe
- `TestListWorkspaceSessions_EmptyWorkspace_NoCrash` — empty list safe

### US-37.3: Add last_seen_at, hasUnread, mark-seen endpoint

**Implementation:**
- Migration `000020_session_last_seen` adds `last_seen_at TIMESTAMPTZ` to `session_index`
- `ListSessionIndex` SQL now includes `last_seen_at` and computed `has_unread` column
- `UpdateSessionLastSeen` DB method with INSERT ON CONFLICT pattern
- `UpdateLastSeen` on `SessionIndexService`
- `MarkSessionSeen` on `WorkspaceService` (verifyOwner + delegate)
- `PUT /:id/sessions/:sessionId/seen` router endpoint (204 on success, 403 on wrong owner)
- Go types: `LastSeenAt *time.Time`, `HasUnread bool` on `SessionListItem`
- Frontend types: `lastSeenAt?: string`, `hasUnread: boolean`
- Frontend API: `markSessionSeen` method
- Contract fixtures and tests updated

**Tests (9 new):** All pass
- 5 DB-level tests (hasUnread true/false/null, UpdateSessionLastSeen existing/new)
- 3 service-level tests (MarkSessionSeen delegates/wrong owner/nil session index)
- 3 router-level tests (mark-seen 204/403/401)

### Assumptions Validated

| # | Assumption | Evidence |
|---|-----------|----------|
| A1 | `session_index.status` hardcoded to "idle" | `database.go:803` |
| A2 | `session.status` only to workspace broker | `proxy.go:962,1168` |
| A3 | `useUserEventStream` has no callback | `useUserEventStream.ts:40-63` |
| A4 | Sidebar only fetches for expanded workspaces | `Sidebar.tsx:366` |
| A5 | No unread tracking exists | grep confirmed |
| A6 | `UserEventBroker.PublishToUser` has replay buffer | `event_broker_user.go:228` |
| A7 | Router has access to proxyHandler | `router.go:757` |
| A8 | `GetActiveSessions` exists | `proxy.go:1143` |

### Full Regression Run

All tests pass — zero failures across entire repository:
- 16 API test packages: OK
- 14 pkg test packages: OK (including repolint migration drift)
- Controller, mocks, cmd: OK

---

## Key Decisions

- `hasUnread` computed in SQL (not Go) — eliminates clock skew since both timestamps from same PostgreSQL server
- `hasUnread = false` when `last_seen_at IS NULL` — never-visited sessions are not "unread"
- `SetActiveSessionsForTest` test helper added to ProxyHandler (not exported, used only by tests)
- Migration copied to `charts/llmsafespace/migrations/` to satisfy repolint drift test

---

## Blockers

None — all backend stories complete and passing.

---

## Tests Run

```
GOPROXY=direct GONOSUMCHECK='*' GONOSUMDB='*' go test -timeout 120s ./... -count=1
```
Result: zero FAIL lines across all packages.

---

## Next Steps

1. Frontend Phase 2: US-37.4 SessionActivityProvider (React Context)
2. US-37.5 Activity spinner in sidebar
3. US-37.6 Unread pulsation in sidebar
4. US-37.7 New messages divider
5. US-37.8 Mark-seen on navigate
6. US-37.9 Comprehensive test suite
7. Adversarial self-review before commit

---

## Files Modified

### New Files
- `api/internal/handlers/proxy_session_status_test.go` — US-37.1 tests
- `api/internal/services/database/session_last_seen_test.go` — US-37.3 DB tests
- `api/migrations/000020_session_last_seen.up.sql` — migration
- `api/migrations/000020_session_last_seen.down.sql` — rollback
- `charts/llmsafespace/migrations/000020_session_last_seen.up.sql` — chart migration
- `charts/llmsafespace/migrations/000020_session_last_seen.down.sql` — chart rollback

### Modified Files
- `api/internal/handlers/proxy.go` — US-37.1 user-broker publish + SetActiveSessionsForTest helper
- `api/internal/server/router.go` — US-37.2 active status merge + US-37.3 mark-seen endpoint
- `api/internal/services/database/database.go` — US-37.3 ListSessionIndex SQL + UpdateSessionLastSeen
- `api/internal/services/sessionindex/service.go` — US-37.3 UpdateLastSeen
- `api/internal/services/workspace/workspace_service.go` — US-37.3 MarkSessionSeen
- `api/internal/interfaces/interfaces.go` — US-37.2/37.3 new interface methods
- `api/internal/mocks/database.go` — UpdateSessionLastSeen mock
- `api/internal/mocks/workspace.go` — MarkSessionSeen mock
- `pkg/types/types.go` — LastSeenAt, HasUnread fields on SessionListItem
- `api/internal/services/database/session_index_test.go` — Updated for new SQL columns
- `api/internal/server/router_frontend_workspace_test.go` — US-37.2/37.3 router tests
- `api/internal/server/router_openapi_contract_test.go` — implOnlyAllowlist for mark-seen
- `api/internal/handlers/opencode_upgrade_test.go` — Mock UpdateLastSeen
- `api/internal/handlers/proxy_backfill_test.go` — Mock UpdateLastSeen
- `api/internal/services/auth/auth_e2e_all_test.go` — Mock UpdateSessionLastSeen
- `api/internal/services/auth/auth_e2e_secrets_test.go` — Mock UpdateSessionLastSeen
- `api/internal/services/auth/auth_sessionid_test.go` — Mock UpdateSessionLastSeen
- `api/internal/services/workspace/workspace_session_test.go` — US-37.3 MarkSessionSeen tests + mock UpdateLastSeen
- `frontend/src/api/types.ts` — lastSeenAt, hasUnread fields
- `frontend/src/api/workspaces.ts` — markSessionSeen API call
- `frontend/src/api/contract-fixtures.json` — Updated SessionListItem fixture
- `frontend/src/api/contract.test.ts` — Assert lastSeenAt, hasUnread
- `design/stories/README.md` — Epic 37 status updated
