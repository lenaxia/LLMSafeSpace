# Epic 25: API Server Robustness & Correctness

**Status:** Planning
**Created:** 2026-06-01
**Priority:** High
**Depends on:** Epic 22 (shipped), Epic 23 Stories 1+4 (shipped)
**Related epics:**
- **Epic 24** — Self-healing workspace lifecycle (controller-side robustness). Epic 25 is the API-server counterpart.
- **Epic 17** — Security review (some findings overlap, e.g., body size limits on proxy path).

---

## Problem Statement

The API server has accumulated functional bugs and robustness gaps that cause incorrect behavior under normal operation and degrade catastrophically under stress. These issues are distinct from Epic 24 (which focuses on the controller's workspace lifecycle) — they live in the request path between the browser/SDK and the workspace pod.

**Observed failure modes:**

1. Connection counter goes negative after session-limit rejection → unlimited connections per workspace (B1)
2. Streaming responses silently truncated on mid-stream read errors → client receives partial JSON with 200 status (B2)
3. Unbounded request body read on proxy path → API server OOM (G1)
4. SSE streaming to client has no write deadline → goroutine leak on stalled clients (G3)
5. Session creation uses `http.DefaultClient` (no explicit timeout beyond client disconnect) → potential infinite hangs (B3)
6. Activity tracker map grows unbounded → memory leak + unnecessary K8s API calls for deleted workspaces (B5)
7. SSE reconnect backoff never resets (dead code) → 30s delay after normal pod restart (B6)
8. Suspend/Resume don't use retry-on-conflict → spurious 409 errors to users (G6)

**Root cause:** The proxy handler (`proxy.go`, 1175 lines) conflates routing, connection management, SSE streaming, auth caching, session tracking, and activity recording in a single file with interleaved mutex operations. This makes bugs like B1 (double release) nearly invisible during review.

---

## Design Principles

1. **Correct first**: Fix the confirmed bugs before adding new capabilities.
2. **Decompose for testability**: Split `proxy.go` into focused modules that can be unit-tested in isolation.
3. **Fail safe**: Connection limits, body size limits, and timeouts must fail closed (reject request) not open (allow unlimited).
4. **Graceful degradation**: Redis/DB unavailability should degrade features (caching, rate limiting) not crash the request path.
5. **Observable**: Every connection lifecycle event, timeout, and limit enforcement is metricked.

---

## Stated Assumptions

| # | Assumption | Verification |
|---|---|---|
| A1 | `proxy.go:370` sets `defer h.releaseConnection(workspaceID)` and line 374 calls `h.releaseConnection(workspaceID)` explicitly before returning — double release | Verified: `proxy.go:370,374` — defer fires on all return paths including the early return at 380 |
| A2 | `doProxy`'s streaming path (line 512-527) writes response headers then streams body chunks. On mid-stream read error, it `break`s and returns `nil` — the client receives a truncated response with no error indication | Verified: `proxy.go:515` writes headers, loop at 519-527 streams, returns nil on any readErr |
| A3 | `io.ReadAll(c.Request.Body)` at line 392 has no size limit; the middleware body-size limit (Epic 17) is only on auth routes | Verified: `middleware/validation.go` applies `MaxBodySize` only to routes registered with the validation middleware; proxy routes bypass it |
| A4 | `http.DefaultClient` is used at `workspace_service.go:813` for session creation; it has no timeout | Verified: Go's `http.DefaultClient` has `Timeout: 0` (infinite) |
| A5 | `StreamEvents` at line 307 writes to `c.Writer` with no deadline; if the client TCP window fills, `Write` blocks indefinitely | Verified: Gin's `ResponseWriter` wraps `net/http.ResponseWriter` which has no default write deadline |
| A6 | `ActivityTracker.activity` map entries are added by `Record()` but never removed by any code path. `flushOne` errors on deleted workspaces (NotFound) but never removes the entry. | Verified: `grep -n "delete" activity.go` returns no results; `flushOne` returns error on NotFound, `Flush` logs it but doesn't clean up |
| A7 | `activeSess` entries are removed by `onSessionIdle` (wired via SSETracker `session.status` idle events) AND by `removeActiveSession` on proxy error. The cleanup IS functional when the SSE connection is healthy. | Verified: `proxy.go:818` — `onSessionIdle` calls `removeActiveSession`; `session_tracker.go:260` dispatches on `status.type == "idle"` |
| A8 | `SuspendWorkspace` does Get → modify status → UpdateStatus without retry-on-conflict | Verified: `workspace_service.go:400-407` — no `retry.RetryOnConflict` wrapper |
| A9 | The SSETracker's `connectAndRead` ALWAYS returns a non-nil error (either idle timeout or stream ended). The backoff reset (`backoff = 2s` on nil error) is dead code — backoff always grows. | Verified: `session_tracker.go:213-214` — both exit paths return `fmt.Errorf(...)` |
| A10 | No production users; API changes have no backwards-compatibility constraints | Stated by user (Epic 21 A7 / Epic 24 A10 precedent) |

### Hypotheses Considered and Refuted

| # | Hypothesis | Refutation |
|---|---|---|
| R1 | "doProxy can write a response (502 on 401) and then the retry path writes another response (double write)" | Refuted: the 401 path writes 502 and returns nil. Retry only fires on connection errors (before any response is written). The retry path is safe. |
| R2 | "Title fetch fires before the proxy response is committed (B4)" | Refuted: `proxyToWorkspace` is synchronous — it writes the response before returning. `c.Writer.Status()` at line 197 correctly reflects the actual response status. |
| R3 | "Active session tracking never cleans up (G8)" | Partially refuted: `onSessionIdle` IS wired and DOES remove sessions. However, cleanup depends on the SSE connection being healthy. If SSE disconnects, sessions remain in `activeSess` until SSE reconnects and opencode sends idle events. This is a partial gap, not a complete failure. |

---

## Scope

### In Scope

| # | What | Category |
|---|---|---|
| 1 | Fix B1: double connection release | Bug fix |
| 2 | Fix B2: streaming path returns nil on mid-stream read error → client gets truncated response with no error signal | Bug fix |
| 3 | Fix B3: `http.DefaultClient` in session creation → use configured client with timeout | Bug fix |
| 4 | Fix B5: activity tracker unbounded map growth (entries never removed for deleted workspaces) | Bug fix |
| 5 | Fix B6: SSE `connectAndRead` always returns error → backoff never resets → 30s reconnect delay after normal server close | Bug fix |
| 6 | G1: enforce max body size on proxy path (10MB default, configurable) | Robustness |
| 7 | G2: readiness check / retry before session creation in EnsureSession | Robustness |
| 8 | G3: write deadline on SSE streaming to client (prevent goroutine leak on stalled clients) | Robustness |
| 9 | G6: retry-on-conflict for Suspend/Resume status updates | Robustness |
| 10 | G8-partial: flush `activeSess` for a workspace when its SSE connection drops (belt-and-suspenders alongside existing idle callback) | Robustness |
| 11 | Proxy file decomposition into focused modules | Maintainability |
| 12 | Prometheus metrics for connection lifecycle, timeouts, body rejections | Observability |
| 13 | Context propagation: replace `context.TODO()` in `pkg/kubernetes/client_crds.go` | Correctness |

### Out of Scope

| # | What | Why |
|---|---|---|
| 1 | Circuit breaker on proxy → sandbox | Separate concern (resilience pattern); needs design |
| 2 | Redis graceful degradation (D4) | Requires significant refactoring of rate limiter + cache services |
| 3 | DB/CRD state divergence (D1) | Needs a reconciliation design; not a quick fix |
| 4 | EnsureSession idempotency (D2) | Needs distributed locking or CAS semantics |
| 5 | Proactive password cache invalidation (D3) | Needs Secret watch integration; separate story |
| 6 | G4: handleSuspending pod termination wait | Controller-side; belongs in Epic 24 |
| 7 | G7: handleTerminating PVC-in-use | Controller-side; belongs in Epic 24 |

---

## User Stories

| Story | Title | Category | Depends On |
|---|---|---|---|
| US-25.1 | Fix double connection release (B1) | Bug fix | None |
| US-25.2 | Fix silent response truncation on mid-stream read error (B2) | Bug fix | None |
| US-25.3 | Replace `http.DefaultClient` in session creation (B3) | Bug fix | None |
| US-25.4 | Fix activity tracker unbounded growth (B5) | Bug fix | None |
| US-25.5 | Fix SSE reconnect backoff never resetting (B6) | Bug fix | None |
| US-25.6 | Enforce max body size on proxy path (G1) | Robustness | None |
| US-25.7 | Add readiness retry before session creation (G2) | Robustness | None |
| US-25.8 | Add write deadline on SSE client streaming (G3) | Robustness | None |
| US-25.9 | Retry-on-conflict for Suspend/Resume (G6) | Robustness | None |
| US-25.10 | Flush activeSess on SSE disconnect (G8-partial) | Robustness | None |
| US-25.11 | Proxy handler file decomposition | Maintainability | US-25.1, US-25.2 |
| US-25.12 | Connection lifecycle Prometheus metrics | Observability | US-25.1 |
| US-25.13 | Context propagation in `pkg/kubernetes/client_crds.go` | Correctness | None |

### Dependency Graph

```
US-25.1 (double release fix) ──┐
US-25.2 (truncation fix) ──────┼── US-25.11 (proxy decomposition)
                                │
US-25.3 (DefaultClient) ───────── independent
US-25.4 (activity map) ────────── independent
US-25.5 (SSE backoff) ─────────── independent
US-25.6 (body size) ───────────── independent
US-25.7 (readiness retry) ─────── independent
US-25.8 (SSE write deadline) ──── independent
US-25.9 (retry-on-conflict) ───── independent
US-25.10 (activeSess flush) ───── independent
US-25.12 (metrics) ────────────── depends on US-25.1
US-25.13 (context propagation) ── independent
```

### Critical Path

```
US-25.1 → US-25.2 → US-25.11 (decomposition enables safe future changes)
```

All other stories are independent and can ship in any order.

---

## Detailed Bug Analysis

### B1: Double Connection Release

**Location:** `api/internal/handlers/proxy.go:370,374`

**Current code:**
```go
// Line 370
defer h.releaseConnection(workspaceID)

// Line 372-380
if isWriteOp && sessionID != "" {
    if !h.checkAndAddActiveSession(workspaceID, sessionID, maxSessions) {
        h.releaseConnection(workspaceID)  // ← EXPLICIT release
        c.Header("Retry-After", ...)
        c.JSON(http.StatusTooManyRequests, ...)
        return  // ← defer ALSO fires here → double release
    }
}
```

**Impact:** After one session-limit rejection, `connCount[workspaceID]` becomes -1. All subsequent `acquireConnection` calls succeed because `-1 < maxConnectionsPerWorkspace`. The workspace effectively has no connection limit.

**Fix:** Remove the explicit `h.releaseConnection(workspaceID)` at line 374. The `defer` handles it.

---

### B2: Silent Response Truncation on Mid-Stream Read Error

**Location:** `api/internal/handlers/proxy.go:512-530`

**Current code:**
```go
copyResponseHeaders(resp.Header, c.Writer.Header())
c.Writer.Header().Set("X-Accel-Buffering", "no")
c.Writer.WriteHeader(resp.StatusCode)  // ← headers committed

flusher, canFlush := c.Writer.(http.Flusher)
buf := make([]byte, 32*1024)
for {
    n, readErr := resp.Body.Read(buf)
    if n > 0 {
        _, _ = c.Writer.Write(buf[:n])
        if canFlush { flusher.Flush() }
    }
    if readErr != nil {
        break  // ← exits on ANY error (io.EOF is normal, but also network errors)
    }
}
return nil  // ← ALWAYS returns nil, even on non-EOF errors
```

**Impact:** If the connection to the workspace pod drops mid-response (pod restart, network partition), the client receives a truncated JSON body with no error indication. The HTTP status is already 200 (headers committed). The client's JSON parser fails silently or produces garbage. The proxy reports success (returns nil), so no retry fires and no error is logged.

**Fix:** Distinguish `io.EOF` (normal completion) from other read errors. On non-EOF error after headers are committed, the response is already corrupted — log the error, close the connection (set `Connection: close` or use `http.Hijacker`), and return an error so the caller can track it in metrics. For non-streaming JSON responses (Content-Type: application/json without chunked encoding), buffer the full response before writing headers so truncation can be detected before committing.

---

### B3: `http.DefaultClient` in Session Creation

**Location:** `api/internal/services/workspace/workspace_service.go:813`

**Current code:**
```go
resp, err := http.DefaultClient.Do(req)
```

**Impact:** If opencode hangs during session creation, the goroutine blocks forever. The user's HTTP request (which called `EnsureSession`) also blocks. The 60s context from `waitForWorkspaceActive` is consumed by the polling loop, leaving minimal remaining deadline for session creation. In the worst case (workspace was already Active), the full 60s context is available but `http.DefaultClient` ignores it because the context is on the request, not the client timeout — wait, actually `http.NewRequestWithContext` IS used (line 808), so the context deadline IS respected. Let me re-verify...

**Re-verification:** Line 808: `req, err := http.NewRequestWithContext(ctx, ...)`. The `ctx` here is the 60s timeout context from `waitForWorkspaceActive`. So the request WILL be cancelled when the context expires. However, `waitForWorkspaceActive` creates its own 60s context (line 769: `ctx, cancel := context.WithTimeout(ctx, 60*time.Second)`), and `createSessionOnWorkspace` receives the ORIGINAL `ctx` from `EnsureSession`, not the polling context. Let me check...

Actually, looking at the call chain:
1. `EnsureSession(ctx, ...)` — ctx is the HTTP request context
2. `waitForWorkspaceActive(ctx, workspaceID)` — creates its own 60s sub-context internally
3. `createSessionOnWorkspace(ctx, workspaceID, podIP)` — uses the ORIGINAL ctx (HTTP request context)

The HTTP request context has whatever timeout the Gin server sets (typically none for long-polling endpoints). So `createSessionOnWorkspace` uses a context with NO timeout beyond the client's TCP connection lifetime. If the client disconnects, the context cancels. But if the client is patient and opencode hangs, this blocks indefinitely.

**Corrected impact:** The session creation call blocks until the HTTP client disconnects OR the server's read/write timeout fires (if configured). With no explicit timeout on the workspace service's HTTP client, a hanging opencode process blocks the goroutine for the lifetime of the client connection.

**Fix:** Use a dedicated `*http.Client` with a 15s timeout, OR wrap the context with a 15s deadline before calling `createSessionOnWorkspace`.

---

### B5: Activity Tracker Unbounded Map Growth

**Location:** `api/internal/handlers/activity.go`

**Current behavior:**
- `Record(workspaceID)` adds/updates entry in `t.activity` map (line 65)
- `Flush()` iterates all entries and calls `flushOne` for each (line 76)
- `flushOne` calls `k8sClient.Workspaces().Get(workspaceID)` — returns NotFound for deleted workspaces
- On error, `Flush` logs it but does NOT remove the entry from `t.activity` or `t.lastFlush`
- Result: deleted workspaces accumulate forever, generating a failed K8s API call every 60s each

**Impact:** Over time, every flush cycle makes N unnecessary K8s API calls (one per deleted workspace). Memory grows linearly with total workspace count (not active count). On a system that creates/deletes many workspaces, this becomes a significant API server load.

**Fix:** In `Flush()`, if `flushOne` returns a NotFound error, delete the entry from both `t.activity` and `t.lastFlush`. Also add a periodic full-sweep that removes entries for workspaces that haven't had activity in >2× the flush interval (stale entries from workspaces that were deleted without a final flush).

---

### B6: SSE Reconnect Backoff Never Resets

**Location:** `api/internal/handlers/session_tracker.go:120-140`

**Current code:**
```go
func (t *SSETracker) subscribe(ctx context.Context, workspaceID string) {
    backoff := 2 * time.Second
    maxBackoff := 30 * time.Second

    for {
        if err := t.connectAndRead(ctx, workspaceID); err != nil {
            t.logger.Debug("SSE subscription ended", ...)
        } else {
            backoff = 2 * time.Second  // ← DEAD CODE: connectAndRead NEVER returns nil
        }

        select {
        case <-ctx.Done(): return
        case <-time.After(backoff):
            backoff = backoff * 2
            if backoff > maxBackoff { backoff = maxBackoff }
        }
    }
}
```

**Root cause:** `connectAndRead` always returns a non-nil error:
- Line 213: `return fmt.Errorf("SSE idle timeout for workspace %s", ...)` (idle timer fired)
- Line 214: `return fmt.Errorf("SSE stream ended for workspace %s", ...)` (scanner.Scan() returned false)

The `else` branch (backoff reset) is unreachable. After a normal server-side close (pod restart, graceful shutdown), the backoff grows: 2s → 4s → 8s → 16s → 30s. The workspace is unreachable for up to 30s after a pod restart.

**Fix:** Return nil from `connectAndRead` when the stream ends normally (scanner returns false without a scan error and without idle timeout). Alternatively, check the error type in `subscribe`: if it's "stream ended" (not timeout, not connection error), reset backoff to a short value (500ms) for immediate reconnect.

---

### G8-partial: activeSess Stale Entries on SSE Disconnect

**Location:** `api/internal/handlers/proxy.go` — `activeSess` map

**Current behavior (mostly correct):**
- `onSessionIdle` IS wired and DOES call `removeActiveSession` when opencode sends `session.status` idle events
- This works correctly when the SSE connection is healthy

**Gap:** When the SSE connection drops (pod restart, network issue):
1. The idle timer fires after 5 minutes → `connectAndRead` returns error
2. `subscribe` reconnects with backoff
3. During the disconnection window, no `session.status` events are received
4. Sessions that went idle during disconnection remain in `activeSess`
5. If the workspace has `maxActiveSessions = 5` and all 5 sessions went idle during a 30s disconnect, the user cannot create new sessions until the SSE reconnects and opencode sends idle events

**Fix:** When `connectAndRead` returns (SSE connection lost), clear all `activeSess` entries for that workspace. The next write operation will re-add the session. This is safe because `activeSess` is a soft limit (prevents new sessions, doesn't kill existing ones) — clearing it on disconnect is conservative (allows new sessions) rather than restrictive (blocks them).

---

## Proxy Decomposition Plan (US-25.11)

Split `proxy.go` (1175 lines) into:

| File | Responsibility | Lines (est.) |
|------|---------------|------|
| `proxy.go` | Route handlers (CreateSession, SendMessage, etc.) — thin dispatch | ~150 |
| `proxy_forward.go` | `doProxy`, `copyResponseHeaders`, response streaming | ~200 |
| `proxy_connections.go` | Connection counting, session tracking, acquire/release | ~100 |
| `proxy_auth.go` | Password cache, `getPassword`, `invalidateCaches` | ~100 |
| `proxy_sse.go` | `StreamEvents`, broker integration | ~100 |
| `proxy_activity.go` | Activity tracker (already separate in `activity.go`) | existing |
| `proxy_helpers.go` | `validateSessionID`, `isConnectionError`, `stripPatchParts` | ~100 |

Total: same code, 7 focused files instead of 1 monolith. Each file has a single responsibility and can be tested independently.

---

## Test Plan

| Story | Test | What It Proves |
|---|---|---|
| US-25.1 | `TestConnectionRelease_SessionLimitRejection_NoDoubleRelease` | After session-limit rejection, connCount == 0 (not -1) |
| US-25.1 | `TestConnectionRelease_NormalFlow_ReleasesOnce` | Normal request releases exactly once |
| US-25.1 | `TestConnectionRelease_AfterDoubleReject_LimitStillEnforced` | Two rejections → connCount still 0; next acquire at limit still blocked |
| US-25.2 | `TestDoProxy_MidStreamReadError_LogsError` | Read error after headers committed → error logged, connection closed |
| US-25.2 | `TestDoProxy_NormalEOF_NoError` | Normal io.EOF → no error logged, response complete |
| US-25.2 | `TestDoProxy_JSONResponse_BufferedBeforeCommit` | Non-streaming JSON response fully buffered before headers written |
| US-25.3 | `TestCreateSession_Timeout_ReturnsError` | Hanging opencode → error within 15s, not infinite |
| US-25.3 | `TestCreateSession_ContextCancelled_ReturnsError` | Client disconnect → session creation cancelled promptly |
| US-25.4 | `TestActivityTracker_DeletedWorkspace_EntryRemoved` | Flushing a deleted workspace (NotFound) removes it from map |
| US-25.4 | `TestActivityTracker_MapSize_BoundedByActiveWorkspaces` | After 100 workspaces created and 90 deleted, map has ≤10 entries |
| US-25.4 | `TestActivityTracker_FlushError_NonNotFound_Retains` | Non-NotFound error (network) retains entry for next flush |
| US-25.5 | `TestSSETracker_NormalStreamEnd_FastReconnect` | Server closes stream normally → reconnect within 500ms |
| US-25.5 | `TestSSETracker_ConnectionError_BackoffGrows` | Connection refused → backoff doubles each attempt |
| US-25.5 | `TestSSETracker_IdleTimeout_ModerateBackoff` | Idle timeout → backoff grows but resets after successful read |
| US-25.6 | `TestProxy_OversizedBody_Rejected` | 11MB body → 413 Payload Too Large |
| US-25.6 | `TestProxy_NormalBody_Accepted` | 1MB body → proxied successfully |
| US-25.6 | `TestProxy_EmptyBody_Accepted` | Empty body (GET) → proxied successfully |
| US-25.7 | `TestEnsureSession_PodNotReady_RetriesUntilReady` | Pod Active but opencode not listening → retries, eventually succeeds |
| US-25.7 | `TestEnsureSession_PodNeverReady_TimesOut` | Pod never responds → returns error within 15s |
| US-25.8 | `TestStreamEvents_StalledClient_ConnectionClosed` | Client stops reading → connection closed within 30s |
| US-25.8 | `TestStreamEvents_NormalClient_NoTimeout` | Active client receiving events → no premature close |
| US-25.9 | `TestSuspendWorkspace_ConflictRetry_Succeeds` | Concurrent status update → retry succeeds |
| US-25.9 | `TestResumeWorkspace_ConflictRetry_Succeeds` | Concurrent status update → retry succeeds |
| US-25.10 | `TestSSEDisconnect_ClearsActiveSessions` | SSE connection drops → activeSess for workspace cleared |
| US-25.10 | `TestSSEReconnect_SessionReAddedOnWrite` | After clear, next write re-adds session normally |
| US-25.12 | `TestMetrics_ConnectionAcquired_Incremented` | Successful acquire → counter++ |
| US-25.12 | `TestMetrics_BodyRejected_Incremented` | Oversized body → rejection counter++ |
| US-25.13 | `TestClientCRDs_ContextCancelled_ReturnsError` | Cancelled context → operation returns ctx.Err() |
| US-25.13 | `TestClientCRDs_ContextWithTimeout_Respected` | Context with 1s timeout → operation fails within 2s |

---

## Implementation Order

**Phase 1: Bug fixes (can ship immediately, no decomposition needed)**
1. US-25.1 — one-line fix (remove explicit release)
2. US-25.2 — distinguish io.EOF from read errors in streaming path; buffer JSON responses
3. US-25.3 — inject HTTP client with 15s timeout into workspace service (or wrap context with deadline)
4. US-25.4 — remove entries from activity map on NotFound flush error
5. US-25.5 — return nil from `connectAndRead` on normal stream end; reset backoff on nil

**Phase 2: Robustness (independent, any order)**
6. US-25.6 — body size limit middleware on proxy routes (io.LimitReader)
7. US-25.7 — retry loop with 500ms backoff in `createSessionOnWorkspace` (3 attempts)
8. US-25.8 — write deadline via `http.ResponseController.SetWriteDeadline` on SSE connections
9. US-25.9 — wrap Suspend/Resume in `retry.RetryOnConflict`
10. US-25.10 — clear `activeSess[workspaceID]` when SSE `connectAndRead` returns

**Phase 3: Structural (after bug fixes stabilize)**
11. US-25.11 — proxy file split
12. US-25.12 — metrics
13. US-25.13 — context propagation

---

## Metrics (US-25.12)

```go
var (
    proxyConnectionsAcquired = prometheus.NewCounterVec(
        prometheus.CounterOpts{Name: "llmsafespace_proxy_connections_acquired_total"},
        []string{"workspace_id"},
    )
    proxyConnectionsRejected = prometheus.NewCounterVec(
        prometheus.CounterOpts{Name: "llmsafespace_proxy_connections_rejected_total"},
        []string{"workspace_id", "reason"}, // "connection_limit", "session_limit"
    )
    proxyBodyRejected = prometheus.NewCounter(
        prometheus.CounterOpts{Name: "llmsafespace_proxy_body_rejected_total"},
    )
    proxyResponseWriteErrors = prometheus.NewCounter(
        prometheus.CounterOpts{Name: "llmsafespace_proxy_response_write_errors_total"},
    )
    sseClientConnections = prometheus.NewGauge(
        prometheus.GaugeOpts{Name: "llmsafespace_sse_client_connections"},
    )
    sseClientWriteTimeouts = prometheus.NewCounter(
        prometheus.CounterOpts{Name: "llmsafespace_sse_client_write_timeouts_total"},
    )
    activeSessionsGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{Name: "llmsafespace_active_sessions"},
        []string{"workspace_id"},
    )
)
```
