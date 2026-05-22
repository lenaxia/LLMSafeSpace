# Worklog 0024: Epic 3 Skeptical Validation — Two Passes, 17 Bugs Fixed

**Date:** 2026-05-22
**Session:** Post-implementation skeptical validation of Epic 3 proxy layer
**Status:** Complete — 573 tests, 0 failures, race detector clean

---

## Objective

Two rounds of skeptical validation of Epic 3 (proxy handler, route registration, activity tracking). Each round: state all assumptions up front, validate each one against actual code with concrete reproduction, fix only confirmed real issues.

---

## Methodology

1. State every assumption required to form an opinion
2. Validate each assumption against the code — no assumed correctness
3. Reproduce each suspected bug with a standalone Go program where possible
4. Fix only confirmed real issues
5. Re-run full test suite + race detector after each fix

---

## Pass 1: 13 Issues Stated, 10 Real, 3 Not Real

### Real issues fixed

| ID | Severity | Issue | Fix |
|----|----------|-------|-----|
| B1 | CRITICAL | `require.NoError(nil, err)` in `setupPassword()` — nil TB panics on any setup failure | Changed to ignore-error form (helper is best-effort) |
| B2 | CRITICAL | `ProxyHandler.Stop()` calls `close(h.stopCh)` unprotected — double-call panics | Wrapped in `sync.Once` |
| B3 | CRITICAL | `ProxyHandler.Start()` no guard — double-call spawns duplicate goroutines | Wrapped in `sync.Once` |
| B4 | HIGH | Default `http.Client{Timeout: 30s}` killed streaming and SSE responses at 30s | Replaced with transport-level timeouts (dial:10s, TLS:10s, response-header:30s) — response body unbounded |
| B5 | HIGH | `wsConfig` not cleared on Running phase transition after resume — workspace reconfiguration invisible | Added Running phase handling in `onPhaseChange` |
| B7 | MEDIUM | Test helpers (`makeSandboxCRD`, `makePasswordSecret`, `makeWorkspaceCRD`) in `proxy.go` — pulled `corev1` into production binary | Moved all three to `proxy_test.go` |
| B8 | MEDIUM | SSE idle timer was dead code — timer reset then checked in non-blocking select, could never fire | Replaced with `time.AfterFunc` that cancels a derived context |
| B10 | MEDIUM | `ProxyHandler.stopCh` field allocated, closed in `Stop()`, never read anywhere | Removed field entirely |
| B11+B12 | LOW | Dead test helpers `setupSandbox/Password/Workspace()` (non-T variants) and `handlerT *testing.T` package var | Removed |
| B13 | LOW | `gin.SetMode` called twice on startup — `app.go` set it, then `NewRouter` always set it to Release, overriding debug mode | Removed call from `app.go`; `app.go` now passes `Debug` through `RouterConfig` |

### Not real (validated by reproduction)

| ID | Issue claimed | Why not real |
|----|--------------|--------------|
| B6 | Double `releaseConnection` corrupts count | Guard `if h.connCount[id] > 0` prevents underflow; explicit call is redundant but safe |
| B9 | Error path not cached in `getWorkspaceConfig` | Intentional — caching transient K8s errors would hide workspace re-creation |

---

## Pass 2: 13 More Assumptions, 4 Real, 9 Not Real

### Real issues fixed

| ID | Severity | Issue | Fix |
|----|----------|-------|-----|
| A1 | CRITICAL | `SandboxWatcher.Stop()` double-close panic — no protection on `close(w.stopCh)` | Added `sync.Once` to `SandboxWatcher` |
| A2 | CRITICAL | `ActivityTracker.Stop()` double-close panic — `ProxyHandler.Start()` error paths call `Stop()`, then `ProxyHandler.Stop()` calls it again | Added `sync.Once` to `ActivityTracker` |
| A9 | HIGH | SSE idle timeout still broken — HTTP request used outer `ctx` (Background), cancelling `idleCtx` had no effect on blocking `scanner.Scan()` | Created `idleCtx` before the HTTP request; passed `idleCtx` to `NewRequestWithContext` so timeout cancels the connection. Updated test server to block on `r.Context().Done()` |
| A11 | LOW | Unnecessary `[]byte → string → strings.NewReader` allocation per proxied request | Replaced with `bytes.NewReader(body)` |

### Not real (validated by reproduction or analysis)

| ID | Issue claimed | Why not real |
|----|--------------|--------------|
| A3 | `watchRestartMu` blocks `Stop()` | Lock is inside `watchOnce`; `stopCh` select unblocks cleanly |
| A4 | Double `releaseConnection` (same as B6) | Guard prevents underflow |
| A5 | `ContentLength != 0` drops chunked bodies (ContentLength = -1) | `-1 != 0` is true, body is read |
| A6 | Stale-IP retry after headers written panics | `http.Do` error precedes any response; retry only fires pre-header |
| A7/A12 | `ctx` unused in `getWorkspaceConfig`, K8s calls ignore request context | K8s interface has no context parameter — pre-existing design constraint |
| A8 | SSE uses `context.Background()` — subscriptions outlive sandboxes | Intentional — stopped via `StopWatching` on phase change |
| A10 | Client `Authorization` header forwarded to opencode | `SetBasicAuth` overwrites it — correct proxy behavior |
| A13 | Phase constants duplicated across packages | String values match; noted for Epic 4 where a shared constant in `pkg/apis` would be appropriate |

---

## Key Learnings

1. `sync.Once` must be applied at the level where the protected resource is owned. `ProxyHandler` had `sync.Once` but its children (`ActivityTracker`, `SandboxWatcher`) did not — the children were the ones double-stopped.

2. `time.AfterFunc` idle timeout must be wired to the HTTP request's context, not a derived child context. The HTTP client only unblocks body reads when the request's own context is cancelled, not a descendant.

3. Test servers must block on `r.Context().Done()` when testing SSE — otherwise the HTTP/1.1 keepalive connection stays open and `scanner.Scan()` blocks indefinitely waiting for more data.

4. The `http.Client.Timeout` field and `context.WithTimeout` affect different things. `Timeout` wraps the entire round-trip at the client level; passing a context to `NewRequestWithContext` gives per-request control. When the test previously used `Timeout: 5s`, it cut the connection — masking the bug. The correct architecture is explicit context cancellation.

---

## Files Changed (Pass 1)

| File | Change |
|------|--------|
| `api/internal/handlers/proxy.go` | `sync.Once` for Start/Stop; removed `stopCh`; transport-level timeouts; moved test helpers out; Running phase cache invalidation; `bytes.NewReader` body; `time.AfterFunc` idle timer |
| `api/internal/handlers/proxy_test.go` | Added `makeSandboxCRD/makePasswordSecret/makeWorkspaceCRD`; fixed `require.NoError(nil)`; removed dead helpers and `handlerT` |
| `api/internal/handlers/session_tracker.go` | `time.AfterFunc` idle timer replacing dead non-blocking select |
| `api/internal/app/app.go` | Removed duplicate `gin.SetMode`; passes `Debug` through `RouterConfig` |

## Files Changed (Pass 2)

| File | Change |
|------|--------|
| `api/internal/handlers/crd_watcher.go` | `sync.Once` for `Stop()` |
| `api/internal/handlers/activity.go` | `sync.Once` for `Stop()` |
| `api/internal/handlers/session_tracker.go` | `idleCtx` created before HTTP request; passed to `NewRequestWithContext`; idle-timeout vs stream-end distinguished in return error |
| `api/internal/handlers/session_tracker_test.go` | Test server blocks on `r.Context().Done()` |
| `api/internal/handlers/proxy.go` | `bytes.NewReader` body (A11) |

---

## Test Results

| Metric | Value |
|--------|-------|
| Total tests | 573 |
| Failures | 0 |
| Race detector | Clean |
| Packages | 25/25 passing |

---

## Commits

- `997c0a1` — Fix 9 validated bugs in Epic 3 proxy layer (Pass 1)
- `e986001` — Fix 4 more validated bugs in second Epic 3 skeptical review (Pass 2)

---

## Next Steps

Begin Epic 4 (MCP Server):
- Read `design/stories/epic-4-mcp-server/` story files
- Validate stories against current codebase using same assumption-validation methodology
- The SSE subscription infrastructure from `session_tracker.go` is the foundation for the MCP server's `session_message` tool (collects `prompt_async` results via `session.status` idle events)
- The `SandboxWatcher` is reusable for Epic 4's sandbox phase notifications
