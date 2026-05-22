# Worklog 0023: Epic 3 — Proxy Sessions Complete

**Date:** 2026-05-22
**Session:** Implement US-3.1 (proxy handler), US-3.2 (route registration), US-3.3 (activity tracking) with full TDD, skeptical review, and gap remediation
**Status:** Complete

---

## Objective

Implement Epic 3: the reverse proxy layer that forwards requests from the API to `opencode serve` running inside sandbox pods. Three user stories: proxy handler core (US-3.1), route registration and app wiring (US-3.2), and activity tracking (US-3.3).

---

## Work Completed

### US-3.1: Implement Proxy Handler

**New files created:**
- `api/internal/handlers/proxy.go` — ProxyHandler: reverse proxy to opencode, Basic Auth injection, connection ceiling (10), active session limit, stale IP retry, cache invalidation
- `api/internal/handlers/session_tracker.go` — SSETracker: subscribes to opencode's `GET /event` SSE stream per sandbox, parses `session.status` events (busy→active, idle→inactive)
- `api/internal/handlers/crd_watcher.go` — SandboxWatcher: single CRD watch loop, fires phase-change callbacks, shared infrastructure for future MCP use
- `api/internal/handlers/activity.go` — ActivityTracker: in-memory batching, 60s flush to Workspace CRD via read-modify-write + RetryOnConflict
- `api/internal/handlers/proxy_test.go` — 97 proxy handler tests
- `api/internal/handlers/crd_watcher_test.go` — 18 CRD watcher tests
- `api/internal/handlers/session_tracker_test.go` — 17 SSE tracker tests
- `api/internal/handlers/activity_test.go` — 15 activity tracker tests

**Key design decisions:**
- SSE tracking is necessary (not over-engineering) because `prompt_async` returns 204 immediately — request-level tracking would make `maxActiveSessions` a no-op for the MCP persona
- CRD watcher is shared infrastructure serving: password cache invalidation, workspace config invalidation, future MCP phase notifications
- Connection ceiling check runs BEFORE session limit check to avoid session leaks on ceiling rejection
- Activity Record() called only on successful proxy (not on connection failure)
- `Stop()` closes `stopCh`; flush goroutine handles final flush on `stopCh` to avoid concurrent Flush() calls

**Skeptical review found 11 gaps, all fixed:**
1. CRITICAL: `EnsureWatching` never called — SSE tracker was dead code
2. CRITICAL: `SetPasswordGetter` never wired — SSE tracker couldn't auth
3. CRITICAL: SSE tracker used sandboxID as hostname instead of pod IP
4. HIGH: `onPhaseChange` stopped SSE for all phases including Running
5. HIGH: `busy→active` SSE tracking not implemented
6. MEDIUM: Session leak on connection ceiling rejection
7. MEDIUM: Session leak when proxy returns 503
8. LOW: `sseIdleTimeout` was dead code — no read deadline
9. HIGH: No integration test for SSE→proxy session lifecycle
10. LOW: Dead code in CRD watcher handleEvent
11. LOW: No exponential backoff on SSE reconnect

### US-3.2: Route Registration + Ownership Middleware + App Wiring

**Files modified:**
- `api/internal/server/router.go` — New `NewRouter` signature accepts `*handlers.ProxyHandler`; `sandboxOwnershipMiddleware` reads `labels["user-id"]`; `registerProxyRoutes` adds 7 proxy routes; sandbox stored in Gin context to avoid double CRD read
- `api/internal/app/app.go` — Creates ProxyHandler, wires `Start()`/`Stop()` in lifecycle, passes to `server.NewRouter()`

**New tests:**
- `api/internal/server/router_proxy_test.go` — 12 route tests including ownership check, auth, endpoint mapping, e2e proxy

**Existing test updated:**
- `api/internal/server/router_workspace_test.go` — Updated `NewRouter` call to pass `nil` proxy handler

**Skeptical review found 5 gaps, all fixed:**
1. CRITICAL: `a.cancel()` before `context.WithTimeout(a.ctx,...)` → immediate deadline on shutdown
2. HIGH: `/metrics` and `/health` registered twice (panic risk)
3. MEDIUM: No explicit guard for missing/nil `user-id` label in ownership check
4. MEDIUM: `TestProxyRoutes_Exist` only asserted `!= 404`, not that handler was reached
5. LOW: Double CRD read per request (ownership middleware + proxy handler) — fixed by storing sandbox in Gin context

### US-3.3: Activity Tracking

ActivityTracker was implemented as part of US-3.1 (`activity.go`). Cross-story review confirmed correct integration.

**Cross-story review found 3 gaps, all fixed:**
1. MEDIUM: `Record()` called before proxy attempt, not after success — activity recorded even for failed requests
2. MEDIUM: Silent skip when `workspaceID == ""` — added debug log
3. MEDIUM: Double-close race in `Stop()`/`Flush()` — moved final flush into goroutine on `stopCh`

**Additional tests added to proxy_test.go:**
- `TestProxy_ActivityNotRecordedOnProxyFailure` — Gap 5 coverage
- `TestProxy_ActivityRecordedOnSuccess` — Gap 5 coverage
- `TestProxy_OnSessionIdle_ActivitySkippedWhenCacheEvicted` — Gap 6 coverage

---

## Test Results

| Before Epic 3 | After Epic 3 | Delta |
|--------------|--------------|-------|
| 463 tests | 573 tests | +110 tests |
| 0 failures | 0 failures | — |

All tests pass with `-race` detector enabled.

---

## Key Decisions

1. **SSE session tracking retained** (not simplified to request-level) because `prompt_async` returns 204 immediately — `maxActiveSessions` would be meaningless without SSE tracking
2. **CRD watcher as shared infrastructure** — feeds password cache, workspace config cache, future MCP phase change notifications
3. **Activity tracked only on success** — prevents dead sandboxes from keeping workspaces alive via failed proxy calls
4. **Final flush in goroutine, not in Stop()** — prevents concurrent Flush() calls between the goroutine ticker and Stop()'s direct call
5. **Sandbox stored in Gin context after ownership check** — avoids two REST calls to K8s per request

---

## Files Modified

| File | Change |
|------|--------|
| `api/internal/handlers/proxy.go` | NEW — ProxyHandler, GetSandboxCRD, onPhaseChange, onSessionIdle, onSessionActive, getPodIPForSSE |
| `api/internal/handlers/session_tracker.go` | NEW — SSETracker with podIPResolver, busy→active support, exponential backoff, idle timeout |
| `api/internal/handlers/crd_watcher.go` | NEW — SandboxWatcher |
| `api/internal/handlers/activity.go` | NEW — ActivityTracker |
| `api/internal/handlers/proxy_test.go` | NEW — 97 tests |
| `api/internal/handlers/crd_watcher_test.go` | NEW — 18 tests |
| `api/internal/handlers/session_tracker_test.go` | NEW — 17 tests |
| `api/internal/handlers/activity_test.go` | NEW — 15 tests |
| `api/internal/server/router.go` | Updated `NewRouter` signature, added proxy routes, ownership middleware |
| `api/internal/server/router_workspace_test.go` | Updated `NewRouter` call |
| `api/internal/server/router_proxy_test.go` | NEW — 12 route tests |
| `api/internal/app/app.go` | Rewritten — ProxyHandler creation, lifecycle, shutdown context fix |

---

## Blockers

None.

---

## Next Steps

Begin Epic 4 (MCP Server):
- Read `design/stories/epic-4-mcp-server/` story files
- Validate stories against codebase (same approach as Epic 3 review)
- The SSE subscription infrastructure from `session_tracker.go` is the foundation for the MCP server's `prompt_async` result collection
