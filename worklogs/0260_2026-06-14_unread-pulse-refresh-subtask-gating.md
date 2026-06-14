# Worklog: Reliable Unread Pulse on Refresh, Gate Subtask Pulse Noise

**Date:** 2026-06-14
**Session:** Fix sidebar unread pulse disappearance on page refresh (unread state lost due to seed-once timing race) and gating subtask pulse to depth-0 only (noise reduction)
**Status:** Complete

---

## Objective

1. Make the sidebar session pulse always accurate across page refreshes. The existing design seeded `pendingUnread` from REST `hasUnread` ONCE per workspace — if the first cache read was stale (e.g. `last_message_at` hadn't persisted yet), the session never pulsed and no SSE idle event was replayed to correct it.

2. Gate unread pulse to top-level (parent) sessions only. Subtasks (depth > 0) were also pulsing, causing noise — every completed subtask doesn't need a pulsing indicator.

---

## Work Completed

### SessionActivityProvider.tsx — reconcile-on-every-update + clearedRef

**Replaced seed-once unread logic** with add-only reconciliation (`reconcileUnread`) that re-reads the durable REST `hasUnread` field on every sessions query cache update.

- `seedBusy()` (renamed from old `seedNewWorkspaces`): busy-only seeding gated by `seededRef` — unchanged behavior, SSE remains authoritative for busy state after initial seed.
- `reconcileUnread()`: add-only reconciliation. `hasUnread:true` and not in `cleared` → add to `pendingUnread`. Never removes on `hasUnread:false` — preserves SSE-set unread through stale refetches where REST hasn't caught up.
- Added `clearedRef` (Map<sessionId, workspaceId>): set on `clearPendingUnread` → suppresses re-adding a session from a stale REST refetch (markSessionSeen PUT racing the GET). Released when REST confirms `hasUnread:false` (reconcile releaseCleared), new SSE busy/idle events, or workspace.phase non-active.
- `clearPendingUnread`: rewritten to use clearedRef instead of cache hasUnread write. Resolves wsId from pendingUnread or cache scan for workspace.phase cleanup targeting.
- busy/idle SSE handlers: add `clearedRef.current.delete(evt.session_id)` to release suppression on new activity.
- workspace.phase non-active: also clears `clearedRef` entries for that workspace.

**Key design decisions:**
- Add-only preserves SSE-set unread (a response that just arrived) through refetches where `last_message_at` hasn't persisted yet — existing seed-once had this property, must be preserved.
- clearedRef bridges the clear-on-view race window without the cache write workaround. The local cache write was problematic because it looked like a REST confirmation, potentially releasing suppression prematurely.
- Despite being add-only, sessions don't leak forever: `clearPendingUnread` (user viewed) removes them, workspace.phase non-active clears them, and they're only added when REST says unread or SSE sets it.

### Sidebar.tsx — depth-gated pulse

Changed `showPulse` in `SessionTreeRow` from:
```
isUnread && !isSelected && !isBusy
```
to:
```
isUnread && !isSelected && !isBusy && depth === 0
```

Subtasks still show the blue spinner (`Loader2`) while busy — `isBusy` path unchanged. When done with an unread response, they show the normal `MessageSquare` icon without pulse animation.

### Pre-existing fixes per zero-debt rule (Rule 5)

- **Contract fixture**: `contract-fixtures.json` was missing `lastSeenAt`/`hasUnread`/`contextUsed` on `SessionListItem`. Updated `contract_test.go` to populate them and regenerated the fixture.
- **gofmt**: 5 Go files had pre-existing formatting issues (proxy_input_test.go, rate_limit_integration_test.go, broker.go, ratelimit_test.go, tracker.go). Formatted.
- **Unused dead code** (CI golangci-lint): removed `workspaceConfig.workspaceID` field (never read), `stripPatchParts()` function, `filterOutPatch()` helper, and `messageEnvelope` type — all unused after proxy handler decomposition.

---

## Key Decisions

1. **Add-only reconciliation** over full reconciliation (add+remove): full reconciliation removed SSE-set unread on stale refetches returning `hasUnread:false` — a regression. Add-only preserves the existing behavior where SSE is authoritative for unread while making the REST baseline re-readable for refresh correctness.

2. **clearedRef vs cache write for clear-on-view race**: the old cache `hasUnread:false` write could release suppression prematurely (the local write looks like REST confirmation). clearedRef decouples the suppression from the cache — suppression is released only by real refetch confirmation or new SSE activity.

3. **Map-based clearedRef** (sessionId→workspaceId) vs Set-based: workspace.phase cleanup needs wsId filtering. Falls back to cache scan for wsId when the session isn't in `pendingUnread`.

---

## Blockers

None.

---

## Tests Run

- Frontend: `npm test` — 1000 tests pass (was 999, +5 new regression tests, -1 fixed contract test)
- Frontend: `npm run typecheck` — clean
- Frontend: `npx eslint` on changed files — clean
- Go: `go build ./api/...` — clean
- Go: `golangci-lint run ./api/internal/handlers/...` — 0 issues
- Go: `go test ./pkg/types/` — PASS
- Pre-commit: repolint ✓, gofmt ✓, goimports ✓, golangci-lint ✓

### New regression tests (5)
- `refresh: stale first read self-heals on subsequent refetch`
- `refresh: reconcile adds unread across multiple workspaces`
- `clearPendingUnread suppresses stale refetch and releases on REST confirm`
- `new SSE idle after clear releases suppression and re-pulses`
- `SSE-set unread survives stale refetch returning hasUnread:false`
- Sidebar: `only top-level sessions pulse; subtasks do not pulse when unread`
- Sidebar: `subtask still shows blue spinner when busy`

---

## Next Steps

- Monitor PR CI (Go tests: `Test -short` and `Test full suite race detector` still running)
- Merge after all CI passes (squash merge per workflow)

---

## Files Modified

- `frontend/src/providers/SessionActivityProvider.tsx` — reconcile+clearedRef refactor
- `frontend/src/providers/SessionActivityProvider.test.tsx` — updated/added 7 tests
- `frontend/src/components/layout/Sidebar.tsx` — depth===0 gate on showPulse
- `frontend/src/components/layout/Sidebar.test.tsx` — depth-gating + busy subtask tests
- `frontend/src/api/contract-fixtures.json` — regenerated (stale fields)
- `pkg/types/contract_test.go` — populate LastSeenAt/HasUnread/ContextUsed
- `api/internal/handlers/proxy_helpers.go` — remove dead code (stripPatchParts, filterOutPatch, messageEnvelope)
- `api/internal/handlers/proxy.go` — remove unused workspaceConfig.workspaceID
- `api/internal/handlers/proxy_input_test.go` — gofmt
- `api/internal/services/eventbroker/broker.go` — gofmt
- `api/internal/services/ratelimit/ratelimit_test.go` — gofmt
- `api/internal/services/sse/tracker.go` — gofmt
- `api/internal/middleware/tests/rate_limit_integration_test.go` — gofmt
