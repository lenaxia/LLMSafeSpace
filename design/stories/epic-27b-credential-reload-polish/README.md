# Epic 27b: Credential Reload Polish

**Status:** Planning (do not start before Epic 27a is shipped and validated on the live cluster)
**Created:** 2026-06-03
**Priority:** Medium
**Depends on:** Epic 27a (Credential Reload Foundation) — schema, single-workspace reload endpoint, banner, removal of auto-dispose

---

## Problem Statement

Epic 27a delivers the foundation: users can add credentials without interrupting in-flight calls, and an explicit reload endpoint applies them when the user clicks. Three gaps remain:

1. **No politeness for active sessions.** The reload endpoint disposes immediately, aborting any LLM streams currently running. A power user mid-task who wants to apply credentials cleanly has no way to wait for current activity to finish.
2. **No bulk operation.** A user who has added credentials across N workspaces must visit each workspace and click reload, N times. Programmatic callers face the same friction.
3. **Errors don't self-diagnose.** When opencode rejects a chat-prompt request and the workspace has staged credentials, the error response gives no hint that reload might fix it. The user has to correlate timestamps mentally.

Epic 27b closes these gaps and ships the documentation and SDK ergonomics needed for programmatic callers to use the reload feature confidently.

---

## Scope of Epic 27b

**In Epic 27b:**
- Drain mode (`?drain=true` query parameter on the existing reload endpoint), event-driven using a snapshot from `GET /session/status` plus the existing `SSETracker` event stream
- `SSETracker.SubscribeDrain` fan-out method enabling multiple concurrent drain calls per workspace
- `Client.GetSessionStatuses` one-shot snapshot method on the opencode client
- Bulk reload across all of a user's workspaces with pending credentials, streaming NDJSON response
- Error response enrichment on chat-proxy routes, buffering 4xx/5xx and adding `agentNeedsRefresh` / `credentialsPendingSince` / `hint` fields with a correct opencode-field allowlist
- API reference documentation for the full credential-reload contract
- SDK ergonomic helpers: typed exception classes, retry-with-drain wrappers, idiomatic per-language patterns
- Prometheus metrics for reload operations extending `metrics.Service`

**Out of scope:**
- All of Epic 27a's foundation work (assumed shipped)
- Per-session model overrides at prompt time
- DynamoDB-style migration
- Upstream `POST /provider/refresh` PR (when it lands, the reload handler implementation can swap transparently — no API contract change)

---

## Design Principles

1. **Politeness is opt-in for API callers, defaulted-on for UI users.** Programmatic callers default to immediate dispose (script intent). The UI modal defaults the drain checkbox to checked (interactive politeness).
2. **Authoritative sources only.** Drain mode uses opencode's own `GET /session/status` endpoint for the initial snapshot and opencode's SSE event stream for transitions. We do not rely on our proxy's `activeSess` map, which tracks only write-op sessions and is not an authoritative view of opencode's session state.
3. **Subscribe before snapshot.** The drain implementation subscribes to SSE events before taking the snapshot to ensure no idle transitions are missed during the window between the two calls.
4. **Streaming over buffering.** Bulk reload streams per-workspace results as they complete; no slow workspace blocks reporting on others.
5. **Errors point at the action, not the diagnosis.** The hint says "If this is related to credentials you just changed, reload the agent." We do not try to correlate the specific failed model with specific staged credentials.
6. **All new behaviour is metricked.** Operational visibility from day one via `metrics.Service` extension.

---

## Stated Assumptions

Each verified against codebase or `~/personal/opencode` at specific file:line.

| # | Assumption | Verification |
|---|---|---|
| B1 | Epic 27a's `workspace_agent_state` table, `MarkCredentialChanged` / `MarkAgentReloaded` helpers, `agentNeedsRefresh` workspace API field, and `AgentReloadHandler` are shipped and stable. | Pre-condition; verified by Epic 27a's success criteria. |
| B2 | The existing `SSETracker` (`api/internal/handlers/session_tracker.go`) dispatches `session.status` events to `onSessionIdle` / `onSessionActive` callbacks (one callback each). The tracker has no fan-out mechanism today; drain mode requires multiple concurrent subscribers per workspace. The fix is a small `SubscribeDrain` method addition (~20 lines) — NOT a full `SessionEventBus` abstraction. | `session_tracker.go:41-72` (single callback fields). |
| B3 | opencode exposes `GET /session/status` at path `/session/status` (verified: `session.ts:79,120-128`). It returns `Record<string, SessionStatus.Info>` where `Info.type` is `"idle"` | `"busy"` | `"retry"`. The endpoint requires Basic auth. This is the authoritative source for the initial busy-session snapshot in drain mode. | `~/personal/opencode/packages/opencode/src/server/routes/instance/httpapi/groups/session.ts:79,120-128`; `session.ts:47` (`StatusMap = Schema.Record(Schema.String, SessionStatus.Info)`). |
| B4 | opencode's `GET /session/status` resolves the workspace directory via `WorkspaceRoutingMiddleware`. When no `?directory=` param or `x-opencode-directory` header is provided, it falls back to `process.cwd()` (`workspace-routing.ts:87`). In our single-workspace pod, `process.cwd()` is the workspace directory set by the entrypoint. `GetSessionStatuses` does not need to pass a directory param. | `~/personal/opencode/packages/opencode/src/server/routes/instance/httpapi/middleware/workspace-routing.ts:87`. |
| B5 | opencode's `session.status` event fires for EVERY session status transition (idle AND busy), unconditional on client connection state. The transition is emitted by `SessionStatus.set()` which is called by the `Runner`'s `onIdle` and `onBusy` hooks (`run-state.ts:60-66`). The `onIdle` hook fires when the runner fiber completes for any reason (normal exit, cancel, interrupt) — verified in `effect/runner.ts:70-81` (`finishRun`). | `run-state.ts:60-66`; `runner.ts:67-81`. |
| B6 | The proxy handler at `api/internal/handlers/proxy.go` proxies chat requests via `proxyToWorkspace`. Error responses from opencode are passed through verbatim today. Adding error enrichment requires buffering responses with status >= 400 (4KB cap) and re-emitting an enriched JSON body. Successful (2xx) and streaming (SSE) responses are NOT buffered. | `api/internal/handlers/proxy.go` (proxyToWorkspace pathway). |
| B7 | opencode error responses use Effect's `HttpApiError` tagged-error framework. The envelope fields are: `_tag` (discriminator), `message`, and per-error-class structured fields. All known error classes and their fields are defined at `~/personal/opencode/packages/opencode/src/server/routes/instance/httpapi/errors.ts`. The enrichment allowlist is derived from this exhaustive list. | `errors.ts:1-193` (all error class definitions verified). |
| B8 | A user's workspaces are listed by `*workspace.Service.ListWorkspaces` (`workspace_service.go:282-326`). Bulk reload adds a new `ListPendingReloadWorkspaces(ctx, userID)` method that queries workspaces with `pending_refresh = TRUE` in `workspace_agent_state` via the partial index from Epic 27a. Phase filtering (Active only) happens per-workspace at reload time, not in the list query — non-Active workspaces appear in the result with an error entry. `WorkspaceListItem.MaxActiveSessions` is NOT populated by this query (it requires a separate settings lookup). For bulk reload purposes `MaxActiveSessions` is not needed; callers that need it should call `GetWorkspace` per item. | `workspace_service.go:282-326`; Epic 27a migration partial index. |
| B9 | Prometheus metrics for reload are added as new methods on `*metrics.Service` (`api/internal/services/metrics/metrics.go`) following the `RecordRequest` / `RecordWorkspaceCreation` pattern, not as package-level `prometheus.NewCounterVec` vars. This maintains the codebase's single registration point. | `api/internal/services/metrics/metrics.go:111-119` (existing pattern). |
| B10 | No production users; API changes have no backwards-compatibility constraints. | Stated by user. |
| B11 | `api/internal/interfaces/interfaces.go` `DatabaseService` interface was extended in Epic 27a's PR with `MarkCredentialChanged`, `GetLastCredentialChangedAt`, and `MarkAgentReloaded`. Epic 27b adds `ListPendingReloadWorkspaces` to `DatabaseService` in the same PR that adds `workspace.Service.ListPendingReloadWorkspaces`. `api/internal/mocks/database.go` is also updated in that PR. Without these interface additions the workspace service layer cannot call the new database methods and the build fails. | `interfaces.go:47-79`; `workspace_service.go:43` (`dbService apiinterfaces.DatabaseService`). |

### Refuted Hypotheses

| # | Hypothesis | Refutation |
|---|---|---|
| BR1 | "Drain should use `activeSess` (proxy's write-tracking map) as the source of busy sessions." | Refuted: `activeSess` only tracks sessions that performed write operations through our proxy. Sessions opened via other paths, resumed from cold storage, or initiated by other clients are not tracked. opencode's own `GET /session/status` is the authoritative source. |
| BR2 | "Drain should use `BusySessions(workspaceID)` on SSETracker." | Refuted by BR1 plus the fact that this method doesn't exist on SSETracker and would require exposing `activeSess` from ProxyHandler — a coupling that crosses ownership boundaries. |
| BR3 | "Drain should do a health check on the SSE connection and fall back to polling if unhealthy." | Refuted: the snapshot (`GET /session/status`) is independent of SSE health. If SSE drops mid-drain, the deadline fires and returns the last-known busy sessions as a `DrainTimeout` error. The caller retries. One deterministic error path is simpler than health-check + fallback-poll machinery. |
| BR4 | "Drain should poll `GET /session/status` at 1Hz." | Refuted: the existing SSETracker already maintains a real-time event subscription. Polling reinvents the mechanism, adds load (N workspaces × 1Hz HTTP), introduces 1s+ latency, and creates a parallel state-tracking path. |
| BR5 | "Bulk reload should buffer all results and return one JSON response when complete." | Refuted: with 50 workspaces in drain mode, a buffered response could hold the HTTP connection for many minutes. Streaming results lets the caller see progress and abandon early. |
| BR6 | "Default drain mode for API callers." | Refuted: scripted callers expect imperative semantics. UI users expect politeness (modal makes the choice explicit, drain defaulted to checked). Different defaults reflect different intents. |
| BR7 | "Apply error enrichment to all proxy routes." | Refuted: file-not-found errors getting "did you reload your agent?" hints is noise. Scope to chat-proxy routes where credential staleness is the most likely cause. |
| BR8 | "Correlate the failed model with specific staged credentials." | Refuted: the credential's `models[]` list is not authoritative. Generic hint is more honest. |

---

## User Stories

| Story | Title | Depends On |
|---|---|---|
| US-27b.1 | `Client.GetSessionStatuses` on opencode client; `SSETracker.SubscribeDrain` fan-out | None |
| US-27b.2 | `WaitUntilIdle` drain primitive in `api/internal/handlers/agent_drain.go` | US-27b.1 |
| US-27b.3 | Drain mode (`?drain=true`) on `POST /workspaces/:id/agent/reload` | US-27b.2 |
| US-27b.4 | Bulk endpoint `POST /users/me/agents/reload` (streaming NDJSON) | US-27b.3 |
| US-27b.5 | Error-enrichment middleware on chat-proxy routes | None |
| US-27b.6 | API reference documentation: reload contract, drain semantics, error envelope | US-27b.3, US-27b.4, US-27b.5 |
| US-27b.7 | SDK ergonomics design (Python, TS, Go): typed exceptions, retry-with-drain helper | US-27b.6 |
| US-27b.8 | `metrics.Service` extension for reload operations | US-27b.3, US-27b.4 |
| US-27b.9 | Worklog: design rationale, drain implementation, retrospective | All |

### Dependency Graph

```
US-27b.1 (primitives) ─→ US-27b.2 (WaitUntilIdle) ─→ US-27b.3 (drain mode) ─┐
                                                                            ├─→ US-27b.6 (docs) ─→ US-27b.7 (SDK)
                                                         US-27b.4 (bulk) ──┤
US-27b.5 (error enrichment) ───────────────────────────────────────────────┘

US-27b.3, US-27b.4 ─→ US-27b.8 (metrics)

All ─→ US-27b.9 (worklog)
```

### Critical Path

```
US-27b.1 → US-27b.2 → US-27b.3 → US-27b.4 → US-27b.6 → US-27b.7 → ship 27b
                                       ↘
                                        US-27b.8 (metrics, parallel with docs)

US-27b.5 (error enrichment) — independent, can ship in parallel with main path
```

---

## Detailed Design

### US-27b.1 — `Client.GetSessionStatuses` and `SSETracker.SubscribeDrain`

**New method on `pkg/agent/opencode/client.go`:**

```go
// GetSessionStatuses calls GET /session/status on opencode and returns the
// current status of all known sessions. The map key is the session ID;
// the value is the status type string: "idle", "busy", or "retry".
//
// In our single-workspace pod, the endpoint resolves the workspace directory
// from process.cwd() when no ?directory= param is provided (B4). No query
// params are needed.
func (c *Client) GetSessionStatuses(ctx context.Context) (map[string]string, error) {
    url := c.baseURL + "/session/status"
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil {
        return nil, fmt.Errorf("build session/status request: %w", err)
    }
    req.SetBasicAuth(agentd.AuthUsername, c.password)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("GET /session/status: %w", err)
    }
    defer resp.Body.Close() //nolint:errcheck
    if resp.StatusCode >= 400 {
        // Read up to 512 bytes of the error body for diagnostics. Drain failures
        // are hard to debug with only a status code; the opencode error envelope
        // (_tag, message, etc.) is included in the returned error string.
        errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
        return nil, fmt.Errorf("GET /session/status returned %d: %s", resp.StatusCode, string(errBody))
    }

    var raw map[string]struct {
        Type string `json:"type"`
    }
    if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&raw); err != nil {
        return nil, fmt.Errorf("decode session/status: %w", err)
    }
    result := make(map[string]string, len(raw))
    for id, info := range raw {
        result[id] = info.Type
    }
    return result, nil
}
```

**New method on `api/internal/handlers/session_tracker.go`:**

```go
// drainSub is a per-workspace drain subscriber record.
type drainSub struct {
    onIdle   func(workspaceID, sessionID string)
    onActive func(workspaceID, sessionID string)
}

// Add two new fields to SSETracker struct:
//   drainMu   sync.Mutex
//   drainSubs map[string]map[uint64]*drainSub  // workspaceID → id → sub

// SubscribeDrain registers onIdle and onActive callbacks for session-status
// events in the given workspace. Multiple concurrent drain subscriptions per
// workspace are supported. Returns a cancel function the caller MUST invoke
// when done (defer it) to avoid goroutine/memory leaks.
//
// Both callbacks are called from the SSETracker's dispatch goroutine under
// no lock — they MUST NOT block.
func (t *SSETracker) SubscribeDrain(
    workspaceID string,
    onIdle func(workspaceID, sessionID string),
    onActive func(workspaceID, sessionID string),
) (cancel func()) {
    t.drainMu.Lock()
    defer t.drainMu.Unlock()

    if t.drainSubs == nil {
        t.drainSubs = make(map[string]map[uint64]*drainSub)
    }
    if t.drainSubs[workspaceID] == nil {
        t.drainSubs[workspaceID] = make(map[uint64]*drainSub)
    }
    id := atomic.AddUint64(&t.drainSubCounter, 1)
    t.drainSubs[workspaceID][id] = &drainSub{onIdle: onIdle, onActive: onActive}

    return func() {
        t.drainMu.Lock()
        defer t.drainMu.Unlock()
        delete(t.drainSubs[workspaceID], id)
        if len(t.drainSubs[workspaceID]) == 0 {
            delete(t.drainSubs, workspaceID)
        }
    }
}
```

Update `dispatchProperties` to fan out to drain subscribers after the existing `onSessionIdle`/`onSessionActive` callbacks:

```go
func (t *SSETracker) dispatchProperties(workspaceID, eventType string, props json.RawMessage) {
    // ... existing logic ...
    switch p.Status.Type {
    case "idle":
        if t.onSessionIdle != nil {
            t.onSessionIdle(workspaceID, p.SessionID)
        }
        // Fan out to drain subscribers.
        // Copy to slice under the lock to avoid iterating the inner map outside
        // the lock (which would be a concurrent map read/write — data race).
        t.drainMu.Lock()
        var idleCbs []*drainSub
        for _, s := range t.drainSubs[workspaceID] {
            idleCbs = append(idleCbs, s)
        }
        t.drainMu.Unlock()
        for _, s := range idleCbs {
            s.onIdle(workspaceID, p.SessionID)
        }
    case "busy", "retry":
        // "retry" is treated as still-active: a session in retry state is
        // running an LLM operation that has not completed. Drain must wait
        // for it. The "retry" → "idle" transition will be emitted as a
        // separate "idle" event when the session finishes (B3). If the SSE
        // connection drops before the idle event, the drain deadline fires
        // (ErrDrainTimeout) and the caller retries — the retry snapshot will
        // show the session as idle.
        if p.Status.Type == "busy" {
            if t.onSessionActive != nil {
                t.onSessionActive(workspaceID, p.SessionID)
            }
        }
        t.drainMu.Lock()
        var activeCbs []*drainSub
        for _, s := range t.drainSubs[workspaceID] {
            activeCbs = append(activeCbs, s)
        }
        t.drainMu.Unlock()
        for _, s := range activeCbs {
            s.onActive(workspaceID, p.SessionID)
        }
    }
}
```

The drain subscriber callbacks are called with `drainMu` released to avoid deadlock if a callback tries to cancel its own subscription. The copy-to-slice pattern ensures the inner `map[uint64]*drainSub` is never iterated outside the lock.

### US-27b.2 — `WaitUntilIdle` drain primitive

New file `api/internal/handlers/agent_drain.go`:

```go
// ErrDrainTimeout is returned by WaitUntilIdle when the deadline elapses
// before all sessions become idle.
type ErrDrainTimeout struct {
    BusySessions []string
}
func (e *ErrDrainTimeout) Error() string {
    return fmt.Sprintf("drain timeout: sessions still busy: %v", e.BusySessions)
}

// WaitUntilIdle blocks until all sessions in the workspace are idle (none
// with status "busy" or "retry"), the context is cancelled, or the deadline
// fires.
//
// Algorithm (subscribe-before-snapshot ordering eliminates the race between
// checking initial state and subscribing to transitions):
//
//  1. Subscribe to SSETracker for idle/active events for this workspace.
//  2. Call GET /session/status for the authoritative initial snapshot.
//  3. Seed the busy set from the snapshot (type != "idle").
//  4. If already empty, return nil immediately.
//  5. Process incoming events in a select loop:
//     - idle event  → remove session from busy set
//     - active event → add session to busy set
//     - busy set empty → return nil
//     - context deadline → return *ErrDrainTimeout with remaining sessions
//
// The busy set is owned by a single goroutine (this function) — no mutex
// needed. Callbacks push events to a buffered channel; channel capacity 64
// handles burst transitions safely.
func WaitUntilIdle(
    ctx context.Context,
    workspaceID string,
    tracker *SSETracker,
    opencodeClient *opencode.Client,
    timeout time.Duration,
) error {
    drainCtx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()

    type event struct {
        sessionID string
        idle      bool
    }
    events := make(chan event, 64)

    // Step 1: subscribe BEFORE snapshot to avoid missing a transition
    // that occurs between snapshot and subscribe.
    unsub := tracker.SubscribeDrain(workspaceID,
        func(_, sid string) {
            select {
            case events <- event{sid, true}:
            default:
                // Channel full: the select loop will drain it.
                // The busy set may transiently over-count but the next
                // idle event for the same session will correct it.
            }
        },
        func(_, sid string) {
            select {
            case events <- event{sid, false}:
            default:
            }
        },
    )
    defer unsub()

    // Step 2: authoritative snapshot from opencode.
    statuses, err := opencodeClient.GetSessionStatuses(drainCtx)
    if err != nil {
        return fmt.Errorf("WaitUntilIdle: snapshot: %w", err)
    }

    // Step 3: seed the busy set.
    busy := make(map[string]struct{})
    for id, typ := range statuses {
        if typ != "idle" {
            busy[id] = struct{}{}
        }
    }

    // Step 4: fast path.
    if len(busy) == 0 {
        return nil
    }

    // Step 5: event loop.
    for {
        select {
        case e := <-events:
            if e.idle {
                delete(busy, e.sessionID)
            } else {
                busy[e.sessionID] = struct{}{}
            }
            if len(busy) == 0 {
                return nil
            }
        case <-drainCtx.Done():
            remaining := make([]string, 0, len(busy))
            for id := range busy {
                remaining = append(remaining, id)
            }
            sort.Strings(remaining) // deterministic for tests
            if errors.Is(drainCtx.Err(), context.DeadlineExceeded) {
                return &ErrDrainTimeout{BusySessions: remaining}
            }
            return drainCtx.Err()
        }
    }
}
```

`WaitUntilIdle` is a pure function (no struct receiver) to keep the interface minimal. It is called by the drain-mode branch of `AgentReloadHandler.Reload`.

**On the "channel full" case:** When the channel fills (64-event burst), a subsequent idle event for that session will be queued or may also be dropped if the burst is extreme. The busy set may temporarily over-count, which extends the drain wait but does not cause incorrect behaviour — if a session goes idle without its event arriving, the deadline fires and the caller retries. On retry, `GET /session/status` shows the session as idle and drain completes immediately. **This is not a permanent deadlock.** Correctness is preserved; the worst case is a spurious `DrainTimeout` error. The channel capacity of 64 is intentional: typical workspaces have 1-5 sessions; 64 concurrent session transitions in a burst is effectively impossible in practice, and the timeout-and-retry path handles any edge case correctly.

### US-27b.3 — Drain mode on the existing reload endpoint

`POST /api/v1/workspaces/:id/agent/reload?drain=true&drainTimeoutSeconds=300`

Add to `AgentReloadHandler.Reload` (after phase and pod-IP resolution, before capturing `priorChangedAt`):

```go
drain := c.Query("drain") == "true"
drainTimeout := h.config.DrainDefaultTimeout // default 5 minutes, configurable
if v := c.Query("drainTimeoutSeconds"); v != "" {
    if t, _ := strconv.Atoi(v); t > 0 && t <= h.config.DrainMaxTimeoutSeconds {
        drainTimeout = time.Duration(t) * time.Second
    }
    // If t <= 0 or t > DrainMaxTimeoutSeconds, the default is used silently.
    // DrainMaxTimeoutSeconds = 0 (misconfiguration) would reject every valid
    // value. NewAgentReloadHandler validates: if DrainMaxTimeoutSeconds == 0
    // it panics with a clear message at construction time rather than
    // silently ignoring all caller-supplied timeouts at request time.
}

drainStart := time.Now()
var drainElapsedMs int64
if drain {
    password, err := h.getPassword(c.Request.Context(), workspaceID)
    if err != nil {
        RespondWithError(c, apierrors.NewInternalError("get_opencode_password_failed", err))
        return
    }
    opencodeCl := opencode.NewClient(
        fmt.Sprintf("http://%s:%d", podIP, agentd.AgentPort),
        password,
    )
    if err := WaitUntilIdle(
        c.Request.Context(),
        workspaceID,
        h.sseTracker,
        opencodeCl,
        drainTimeout,
    ); err != nil {
        var drainErr *ErrDrainTimeout
        if errors.As(err, &drainErr) {
            drainElapsedMs = time.Since(drainStart).Milliseconds()
            c.JSON(http.StatusRequestTimeout, gin.H{
                "error": gin.H{
                    "code":           "drain_timeout",
                    "message":        fmt.Sprintf("workspace did not become idle within %s", drainTimeout),
                    "busySessionIDs": drainErr.BusySessions,
                    "drainElapsedMs": drainElapsedMs,
                },
            })
            h.metricsService.RecordAgentReloadDrainTimeout(drainElapsedMs)
            return
        }
        RespondWithError(c, apierrors.NewInternalError("drain_failed", err))
        return
    }
    drainElapsedMs = time.Since(drainStart).Milliseconds()
}

// ... continue with priorChangedAt capture, agentd call, MarkAgentReloaded ...

c.JSON(http.StatusOK, gin.H{
    "disposed":       true,
    "drained":        drain,
    "drainElapsedMs": drainElapsedMs,
    "lastDisposedAt": disposedAt.Format(time.RFC3339),
})
```

**Password threading:** `AgentReloadHandler` needs `getPassword(ctx, workspaceID) (string, error)` — the same function used by `proxy.go:685`. This is injected as a function dependency:

```go
type AgentReloadHandler struct {
    // ... existing fields ...
    sseTracker    *SSETracker
    getPassword   func(ctx context.Context, workspaceID string) (string, error)
    config        AgentReloadConfig
}

type AgentReloadConfig struct {
    DrainDefaultTimeout    time.Duration // default: 5 * time.Minute
    DrainMaxTimeoutSeconds int           // default: 600 (10 minutes)
}
```

**Injection wiring in `app.go` — Option A (invert construction, recommended):**

`ProxyHandler.getPassword` is an unexported method and `ProxyHandler.sseTracker` is an
unexported field. Neither can be accessed from `app.go` via the `*handlers.ProxyHandler`
value. The correct solution is to invert the construction order so `app.go` holds the
primary references and injects them into both `ProxyHandler` and `AgentReloadHandler`:

1. Extract `*SSETracker` construction out of `NewProxyHandler`. Add an `InjectSSETracker(t *SSETracker)` setter to `ProxyHandler` (or accept it as a constructor parameter).
2. Extract the password-getter closure out of `NewProxyHandler`. The closure reads from the `k8sClient` and the `pwCache` map — it can be a standalone function or a struct that both handlers share.
3. In `app.go`, construct `sseTracker` and `passwordGetter` before constructing `ProxyHandler` and `AgentReloadHandler`. Pass both to each.

Concrete `app.go` wiring:

```go
// app.go — construct shared dependencies first
passwordGetter := handlers.NewWorkspacePasswordGetter(k8sClient, cfg.Kubernetes.Namespace)
sseTracker := handlers.NewSSETracker(httpClient, log, nil /* onSessionIdle set below */)

proxyHandler, err := handlers.NewProxyHandler(k8sClient, log, cfg.Kubernetes.Namespace, httpClient, &agentoc.Dialect{})
proxyHandler.InjectSSETracker(sseTracker)      // setter added in US-27b.1
proxyHandler.InjectPasswordGetter(passwordGetter) // setter added in US-27b.1

agentReloadHandler := handlers.NewAgentReloadHandler(
    wsSvc, dbSvc,
    newSecretsPodIPResolver(...),
    // 15s timeout: larger than agentd's own 10s dispose timeout so agentd's
    // deadline always fires first, returning a structured JSON error body.
    // Matches the value set in the 27a wiring — both wiring sites must stay in sync.
    &http.Client{Timeout: 15 * time.Second},
    passwordGetter,  // same instance as ProxyHandler
    sseTracker,      // same instance as ProxyHandler
    log, metricsService,
    handlers.AgentReloadConfig{
        DrainDefaultTimeout:    5 * time.Minute,
        DrainMaxTimeoutSeconds: 600,
    },
)
```

`handlers.NewWorkspacePasswordGetter` is a new small constructor that encapsulates the
k8s-secret-reading + in-memory-cache logic currently in `ProxyHandler.getPassword`
(`proxy.go:685-710`). It is extracted into a standalone type in `api/internal/handlers/password_getter.go`
(new file) so both `ProxyHandler` and `AgentReloadHandler` share the same implementation
without either owning it.

`WorkspacePasswordGetter` exposes three methods:

```go
type WorkspacePasswordGetter struct { /* k8sClient, namespace, cache, mu */ }

// Get returns the password for the given workspace, reading from the
// Kubernetes secret workspace-pw-{workspaceID} and caching the result
// in-memory. Equivalent to the current ProxyHandler.getPassword logic.
func (g *WorkspacePasswordGetter) Get(ctx context.Context, workspaceID string) (string, error)

// Invalidate removes the cached password for a workspace, forcing the
// next Get call to re-read from Kubernetes. Called by ProxyHandler.invalidateCaches
// on 401 responses (proxy.go:504) and on workspace phase transitions
// (proxy.go:815). Previously this called delete(h.pwCache, workspaceID)
// directly; after extraction it calls g.Invalidate(workspaceID).
func (g *WorkspacePasswordGetter) Invalidate(workspaceID string)

// InvalidateAll clears the entire cache. Called from tests and on full
// proxy reset.
func (g *WorkspacePasswordGetter) InvalidateAll()
```

`ProxyHandler.invalidateCaches` is updated to call `h.passwordGetter.Invalidate(workspaceID)` instead of `delete(h.pwCache, workspaceID)`. The `pwCache` / `pwCacheMu` fields on `ProxyHandler` are removed (moved into `WorkspacePasswordGetter`). This is the only structural change to `ProxyHandler` from cache ownership perspective — the observable behaviour (password caching and invalidation-on-401) is identical.

**Files added/changed by this wiring refactor (US-27b.1 scope):**
- `api/internal/handlers/password_getter.go` — NEW: `WorkspacePasswordGetter` type with `Get(ctx, workspaceID) (string, error)`
- `api/internal/handlers/proxy.go` — remove inline `getPassword` method; add `InjectPasswordGetter` and `InjectSSETracker` setters; update `NewProxyHandler` to not construct `sseTracker` internally if one is injected
- `api/internal/app/app.go` — construct `passwordGetter` and `sseTracker` before `NewProxyHandler`; inject into both handlers

UI elapsed counter: the frontend modal, when drain is checked, computes elapsed time client-side from request start and displays "Waiting for sessions to finish... 1:23 elapsed" until the response arrives. When the response arrives, the displayed elapsed time switches to the server-returned `drainElapsedMs` value (the authoritative figure). The client-side counter is a live estimate only; do not display both values simultaneously.

### US-27b.4 — Bulk endpoint `POST /api/v1/users/me/agents/reload`

```
POST /api/v1/users/me/agents/reload
Query params:
  - drain (boolean, default false)
  - drainTimeoutSeconds (integer, optional, applied per-workspace; capped server-side)
  - parallelism (integer, default 5, max 20)

Response:
  Content-Type: application/x-ndjson
  (streaming; each line is a JSON object terminated by newline)

  {"workspaceId":"abc-123","disposed":true,"drained":false,"lastDisposedAt":"..."}
  {"workspaceId":"def-456","disposed":true,"drained":true,"drainElapsedMs":8021,"lastDisposedAt":"..."}
  {"workspaceId":"ghi-789","disposed":false,"error":{"code":"drain_timeout","busySessionIDs":["sess-x"]}}
  {"summary":{"total":3,"succeeded":2,"failed":1,"durationMs":12345}}

  Empty-result case: Content-Type: application/x-ndjson, single summary line:
  {"summary":{"total":0,"succeeded":0,"failed":0,"durationMs":0}}

  HTTP status: always 200 (results are in the stream).
  The summary line is ALWAYS the last line.
```

The empty-result case uses the same NDJSON format (single summary line) for a consistent content-type across all responses. SDK callers read lines; one-line response works naturally.

Implementation uses a **channel-based fan-in** to avoid mutex contention under slow clients (Issue E.3 from audit round 3):

```go
func (h *AgentReloadHandler) BulkReload(c *gin.Context) {
    // extractAuth is the package-level helper in package handlers (secrets.go:795).
    userID, _ := extractAuth(c)
    if userID == "" {
        c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
        return
    }
    drain := c.Query("drain") == "true"
    parallelism := parseIntClamped(c.Query("parallelism"), 5, 1, 20)
    drainTimeout := h.parseDrainTimeout(c.Query("drainTimeoutSeconds"))
    start := time.Now()

    pending, err := h.workspaceSvc.ListPendingReloadWorkspaces(c.Request.Context(), userID)
    if err != nil {
        RespondWithError(c, apierrors.NewInternalError("list_pending_failed", err))
        return
    }

    c.Writer.Header().Set("Content-Type", "application/x-ndjson")
    c.Writer.Header().Set("X-Accel-Buffering", "no")
    c.Writer.WriteHeader(http.StatusOK)

    if len(pending) == 0 {
        _ = json.NewEncoder(c.Writer).Encode(map[string]any{
            "summary": map[string]any{"total": 0, "succeeded": 0, "failed": 0, "durationMs": 0},
        })
        c.Writer.(http.Flusher).Flush()
        return
    }

    // Channel-based fan-in: workers never touch c.Writer directly.
    // A single writer goroutine owns the response stream, eliminating
    // mutex contention under slow clients.
    type result struct {
        data      map[string]any
        succeeded bool
    }
    results := make(chan result, parallelism)

    sem := make(chan struct{}, parallelism)
    var wg sync.WaitGroup
    for _, ws := range pending {
        wg.Add(1)
        ws := ws
        go func() {
            defer wg.Done()
            sem <- struct{}{}
            defer func() { <-sem }()
            r := h.reloadOne(c.Request.Context(), userID, ws.ID, drain, drainTimeout)
            results <- r
        }()
    }
    // Close results when all workers done.
    // Workers exit promptly when ctx is cancelled; wg.Wait() unblocks within one reloadOne timeout.
    go func() { wg.Wait(); close(results) }()
    // Single writer goroutine.
    flusher, _ := c.Writer.(http.Flusher)
    succeeded, failed := 0, 0
    for r := range results {
        _ = json.NewEncoder(c.Writer).Encode(r.data)
        if flusher != nil { flusher.Flush() }
        if r.succeeded { succeeded++ } else { failed++ }
    }
    _ = json.NewEncoder(c.Writer).Encode(map[string]any{
        "summary": map[string]any{
            "total": len(pending), "succeeded": succeeded, "failed": failed,
            "durationMs": time.Since(start).Milliseconds(),
        },
    })
    if flusher != nil { flusher.Flush() }
    h.metricsService.RecordAgentReloadBulk(len(pending), succeeded, failed)
}
```

The `c.Request.Context()` cancellation propagates to the workers' `reloadOne` calls. When the client disconnects, in-flight dispose calls continue (dispose is fast and idempotent) but results are discarded after the context is cancelled.

**`reloadOne` helper — reuses the single-workspace reload logic:**

```go
// reloadOne performs a single-workspace reload and returns a result struct
// suitable for streaming as a NDJSON line. It follows the same logic as
// AgentReloadHandler.Reload but writes to a struct instead of an HTTP response.
// The warning path (dispose OK, DB commit failed) is preserved: the result
// carries disposed=true plus a warning string so the client sees the workspace
// was reloaded even if state tracking failed.
func (h *AgentReloadHandler) reloadOne(
    ctx context.Context,
    userID, workspaceID string,
    drain bool,
    drainTimeout time.Duration,
) result {
    fail := func(code, msg string) result {
        return result{
            data:      map[string]any{"workspaceId": workspaceID, "disposed": false, "error": map[string]any{"code": code, "message": msg}},
            succeeded: false,
        }
    }

    ws, err := h.workspaceSvc.GetWorkspace(ctx, userID, workspaceID)
    if err != nil {
        return fail("workspace_error", err.Error())
    }
    if ws.Phase != string(v1.WorkspacePhaseActive) {
        return fail("phase_not_active", fmt.Sprintf("workspace is in phase %q", ws.Phase))
    }

    podIP, err := h.podResolver.GetWorkspacePodIP(ctx, userID, workspaceID)
    if err != nil || podIP == "" {
        return fail("pod_not_reachable", "workspace pod is not reachable")
    }

    if drain {
        pw, err := h.getPassword(ctx, workspaceID)
        if err != nil {
            return fail("get_opencode_password_failed", err.Error())
        }
        opencodeCl := opencode.NewClient(
            fmt.Sprintf("http://%s:%d", podIP, agentd.AgentPort),
            pw,
        )
        if err := WaitUntilIdle(ctx, workspaceID, h.sseTracker, opencodeCl, drainTimeout); err != nil {
            var drainErr *ErrDrainTimeout
            if errors.As(err, &drainErr) {
                return result{
                    data: map[string]any{
                        "workspaceId": workspaceID, "disposed": false,
                        "error": map[string]any{
                            "code": "drain_timeout", "busySessionIDs": drainErr.BusySessions,
                        },
                    },
                    succeeded: false,
                }
            }
            return fail("drain_failed", err.Error())
        }
    }

    priorChangedAt, err := h.db.GetLastCredentialChangedAt(ctx, workspaceID)
    if err != nil {
        return fail("agent_state_read_failed", err.Error())
    }

    agentdURL := fmt.Sprintf("http://%s:%d/v1/agent/reload", podIP, agentd.AgentdPort)
    req, _ := http.NewRequestWithContext(ctx, http.MethodPost, agentdURL, nil)
    resp, err := h.httpClient.Do(req)
    if err != nil {
        return fail("agent_unreachable", err.Error())
    }
    defer resp.Body.Close() //nolint:errcheck
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
        return fail("dispose_failed", fmt.Sprintf("agentd returned %d: %s", resp.StatusCode, string(body)))
    }

    // Dispose succeeded. Update state.
    tx, err := h.db.BeginTx(ctx, nil)
    if err != nil {
        h.logger.Warn("bulk reload: tx begin failed", zap.String("workspaceID", workspaceID), zap.Error(err))
        return result{
            data: map[string]any{
                "workspaceId": workspaceID, "disposed": true,
                "warning": "Agent was reloaded but state could not be updated. Reload again to clear the banner.",
            },
            succeeded: true, // dispose succeeded; warning is non-fatal
        }
    }
    defer tx.Rollback() //nolint:errcheck

    disposedAt, err := h.db.MarkAgentReloaded(ctx, tx, workspaceID, priorChangedAt)
    if err != nil {
        h.logger.Warn("bulk reload: MarkAgentReloaded failed", zap.String("workspaceID", workspaceID), zap.Error(err))
        return result{
            data: map[string]any{
                "workspaceId": workspaceID, "disposed": true,
                "warning": "Agent was reloaded but state could not be updated. Reload again to clear the banner.",
            },
            succeeded: true,
        }
    }
    if err := tx.Commit(); err != nil {
        h.logger.Warn("bulk reload: tx commit failed", zap.String("workspaceID", workspaceID), zap.Error(err))
        return result{
            data: map[string]any{
                "workspaceId": workspaceID, "disposed": true,
                "warning": "Agent was reloaded but state could not be updated. Reload again to clear the banner.",
            },
            succeeded: true,
        }
    }

    return result{
        data: map[string]any{
            "workspaceId": workspaceID, "disposed": true,
            "drained": drain, "lastDisposedAt": disposedAt.Format(time.RFC3339),
        },
        succeeded: true,
    }
}
```

`ListPendingReloadWorkspaces` is a new method on `*workspace.Service`:

```go
func (s *Service) ListPendingReloadWorkspaces(ctx context.Context, userID string) ([]*types.WorkspaceListItem, error) {
    return s.dbService.ListPendingReloadWorkspaces(ctx, userID)
}
```

And on `*database.Service`:

```go
func (s *Service) ListPendingReloadWorkspaces(ctx context.Context, userID string) ([]*types.WorkspaceListItem, error) {
    rows, err := s.DB.QueryContext(ctx, `
        SELECT w.id, w.user_id, w.name, w.runtime, w.storage_size, w.image_tag, w.agent_version,
               w.created_at, w.updated_at,
               TRUE AS agent_needs_refresh,
               s.last_credential_changed_at AS credentials_pending_since
        FROM workspaces w
        JOIN workspace_agent_state s ON s.workspace_id = w.id
        WHERE w.user_id = $1
          AND w.deleted_at IS NULL
          AND s.pending_refresh = TRUE
        ORDER BY w.created_at DESC
    `, userID)
    if err != nil {
        return nil, fmt.Errorf("list pending reload workspaces: %w", err)
    }
    defer func() { _ = rows.Close() }()
    var items []*types.WorkspaceListItem
    for rows.Next() {
        var item types.WorkspaceListItem
        if err := rows.Scan(
            &item.ID, &item.UserID, &item.Name, &item.Runtime,
            &item.StorageSize, &item.ImageTag, &item.AgentVersion,
            &item.CreatedAt, &item.UpdatedAt,
            &item.AgentNeedsRefresh, &item.CredentialsPendingSince,
        ); err != nil {
            return nil, fmt.Errorf("scan pending reload workspace: %w", err)
        }
        // MaxActiveSessions is not populated here (requires a separate settings query).
        // Callers that need it should call workspace_service.GetWorkspace per item.
        // For bulk reload purposes MaxActiveSessions is not needed.
        items = append(items, &item)
    }
    if err := rows.Err(); err != nil {
        return nil, fmt.Errorf("iterate pending reload workspaces: %w", err)
    }
    return items, nil
}
```

Note: this query uses a regular JOIN (not LEFT JOIN) because we only want workspaces that have a `workspace_agent_state` row with `pending_refresh = TRUE`. The partial index `idx_workspace_agent_state_pending` (created in Epic 27a migration) makes this efficient.

### US-27b.5 — Error-enrichment middleware

New file `api/internal/handlers/proxy_chat_enrichment.go`:

```go
const chatErrorBodyMax = 4 * 1024

// opencode error field allowlist: derived exhaustively from
// ~/personal/opencode/packages/opencode/src/server/routes/instance/httpapi/errors.ts
// Fields included:
//   _tag        — discriminator (e.g. "ModelNotFoundError"), safe to expose
//   message     — human-readable error message
//   kind        — error sub-kind (InvalidRequestError)
//   field       — field name for validation errors (InvalidRequestError)
//   resource    — resource identifier (ConflictError)
//   service     — upstream service name (UpstreamError, ServiceUnavailableError)
//   status      — upstream HTTP status (UpstreamError)
//   operation   — timed-out operation (TimeoutError)
//   ref         — opaque reference ID (UnknownError)
//   providerID  — LLM provider ID (ProviderNotFoundError, ModelNotFoundError)
//   modelID     — model ID (ModelNotFoundError)
//   suggestions — fuzzy-match suggestions (ModelNotFoundError)
//   sessionID   — session identifier (SessionNotFoundError, SessionBusyError)
// Fields explicitly NOT included: none from opencode's schema leak sensitive
// internal data. All fields above are safe to surface to authenticated workspace owners.
var opencodeErrorAllowlist = []string{
    "_tag", "message", "kind", "field", "resource",
    "service", "status", "operation", "ref",
    "providerID", "modelID", "suggestions", "sessionID",
}

func enrichChatErrorBody(
    body []byte,
    truncated bool,
    needsRefresh bool,
    since time.Time,
    workspaceID string,
) []byte {
    out := map[string]any{}
    if len(body) > 0 {
        var orig map[string]any
        if json.Unmarshal(body, &orig) == nil {
            for _, k := range opencodeErrorAllowlist {
                if v, ok := orig[k]; ok {
                    out[k] = v
                }
            }
        } else {
            // Non-JSON upstream (e.g. plain text from a proxy layer).
            text := string(body)
            if len(text) > 1024 {
                text = text[:1024] + "..."
            }
            out["message"] = text
        }
    }
    if truncated {
        out["bodyTruncated"] = true
    }
    if needsRefresh {
        out["agentNeedsRefresh"] = true
        out["credentialsPendingSince"] = since.Format(time.RFC3339)
        out["hint"] = fmt.Sprintf(
            "You added or modified llm-provider credentials at %s but have not reloaded "+
                "the agent yet. If this error is related to a provider or model you just changed, "+
                "call POST /api/v1/workspaces/%s/agent/reload to apply the new credentials.",
            since.Format(time.RFC3339), workspaceID,
        )
    }
    result, _ := json.Marshal(out)
    return result
}
```

The `chatErrorEnrichmentWriter` wraps `gin.ResponseWriter`, buffers error responses up to 4KB, and calls `Finalize` after the proxy completes:

```go
type chatErrorEnrichmentWriter struct {
    gin.ResponseWriter
    statusCode   int
    body         bytes.Buffer
    bodyOverflow bool
    workspaceID  string
    getState     func(ctx context.Context, workspaceID string) (needsRefresh bool, since time.Time)
}

// WriteHeader defers actual header write until Finalize for error responses.
// Guard: if code is 0 (caller did not set a status before writing), treat as 200.
func (w *chatErrorEnrichmentWriter) WriteHeader(code int) {
    if code == 0 {
        code = http.StatusOK
    }
    w.statusCode = code
    if code < 400 {
        w.ResponseWriter.WriteHeader(code)
    }
}

// Written returns true as soon as any status code has been observed by this
// wrapper. Overrides the embedded gin.ResponseWriter so gin middleware that
// calls c.Writer.Written() before adding headers sees the correct state even
// when we have deferred the actual WriteHeader call until Finalize.
func (w *chatErrorEnrichmentWriter) Written() bool {
    return w.statusCode != 0
}

// Status returns the HTTP status code this wrapper has captured. Overrides
// the embedded gin.ResponseWriter for the same reason as Written().
func (w *chatErrorEnrichmentWriter) Status() int {
    if w.statusCode == 0 {
        return http.StatusOK
    }
    return w.statusCode
}

// Write buffers error bodies; passes 2xx through immediately.
func (w *chatErrorEnrichmentWriter) Write(b []byte) (int, error) {
    if w.statusCode == 0 { w.statusCode = http.StatusOK }
    if w.statusCode < 400 {
        return w.ResponseWriter.Write(b)
    }
    remaining := chatErrorBodyMax - w.body.Len()
    if remaining <= 0 {
        w.bodyOverflow = true
        return len(b), nil
    }
    if len(b) > remaining {
        w.body.Write(b[:remaining])
        w.bodyOverflow = true
        return len(b), nil
    }
    return w.body.Write(b)
}

// Finalize emits the enriched error body. Called after the proxy completes.
func (w *chatErrorEnrichmentWriter) Finalize(ctx context.Context) {
    if w.statusCode < 400 {
        return // already passed through
    }
    needsRefresh, since := w.getState(ctx, w.workspaceID)
    enriched := enrichChatErrorBody(w.body.Bytes(), w.bodyOverflow, needsRefresh, since, w.workspaceID)
    w.ResponseWriter.Header().Set("Content-Type", "application/json")
    w.ResponseWriter.WriteHeader(w.statusCode)
    _, _ = w.ResponseWriter.Write(enriched)
}
```

Applied **only** to chat-proxy routes (B8). The middleware wraps the response writer before the proxy call and calls `Finalize` after:

```go
// In proxy.go, for SendMessage and related chat routes only:
ew := &chatErrorEnrichmentWriter{
    ResponseWriter: c.Writer,
    workspaceID:    workspaceID,
    getState:       h.workspaceAgentState, // reads from DB, cached for request
}
c.Writer = ew
defer ew.Finalize(c.Request.Context())
// ... existing proxy logic ...
```

The enrichment does NOT apply when the API server itself rejects the request before it reaches the proxy (e.g., auth failure, rate limit). These produce a response via `respondWithError` before the enrichment writer is in place.

### US-27b.6 — API Reference Documentation

`docs/api/credentials.md` (new) documents:
- Credential lifecycle: stage vs activate
- `agentNeedsRefresh` + `credentialsPendingSince` on workspace responses
- Single-workspace reload: endpoint, query params, success response shape, drain-timeout 408 shape
- Bulk reload: endpoint, NDJSON stream format, summary line
- Error responses on chat-proxy routes: field shape, `agentNeedsRefresh` hint
- SDK recovery patterns

### US-27b.7 — SDK Ergonomics Design

(Implementation in Epic 14; design captured here.)

The typed exception class surfaces structured fields from the enriched error envelope:

```python
# Python
class AgentNeedsRefreshError(LLMSafeSpaceError):
    workspace_id: str
    credentials_pending_since: datetime
    hint: str

try:
    session.send("do X")
except AgentNeedsRefreshError as e:
    client.workspaces.reload_agent(e.workspace_id, drain=True)
    session.send("do X")  # retry once
```

Auto-retry-with-drain is opt-in at client construction, not implicit:

```python
client = LLMSafeSpace(api_key=..., auto_reload_on_stale_creds=True)
```

The retry is bounded to one attempt; if the retry also fails, the original `AgentNeedsRefreshError` is re-raised.

### US-27b.8 — `metrics.Service` Extension

New methods on `*metrics.Service` following the `RecordRequest` / `RecordWorkspaceCreation` pattern:

```go
func (s *Service) RecordAgentReload(result string, durationMs int64, drained bool) {
    // counter: llmsafespace_agent_reload_total{result, drained}
    // histogram: llmsafespace_agent_reload_duration_ms{drained}
}

func (s *Service) RecordAgentReloadDrainTimeout(elapsedMs int64) {
    // counter: llmsafespace_agent_reload_drain_timeouts_total
    // histogram: llmsafespace_agent_reload_drain_elapsed_ms (on timeout path)
}

func (s *Service) RecordAgentReloadBulk(total, succeeded, failed int) {
    // counter: llmsafespace_agent_reload_bulk_total{outcome}
    // outcome: "all_success" | "partial" | "all_failed"
}
```

A periodic goroutine (30s cadence, deadline-guarded) updates a gauge:

```go
// llmsafespace_agent_reload_pending_workspaces
// Updated by SELECT COUNT(*) FROM workspace_agent_state WHERE pending_refresh = TRUE
```

---

## Test Plan

| Story | Test | What It Proves |
|---|---|---|
| US-27b.1 | `TestGetSessionStatuses_HappyPath` | Returns map of sessionID → type from mock opencode |
| US-27b.1 | `TestGetSessionStatuses_RequiresBasicAuth` | Mock returns 401 without auth → error returned |
| US-27b.1 | `TestGetSessionStatuses_EmptyMap` | No sessions → empty map, no error |
| US-27b.1 | `TestSubscribeDrain_MultipleSubscribers_AllReceiveEvents` | N subscribers all get dispatched onIdle/onActive |
| US-27b.1 | `TestSubscribeDrain_Unsubscribe_StopsCallbacks` | Cancel function works; no leak after cancel |
| US-27b.1 | `TestSubscribeDrain_PerWorkspace_NoCrossTalk` | Workspace A subscribers don't see Workspace B events |
| US-27b.2 | `TestWaitUntilIdle_AlreadyIdle_ReturnsImmediately` | Snapshot shows all idle → returns nil without subscribing |
| US-27b.2 | `TestWaitUntilIdle_BusyThenIdle_ReturnsAfterEvent` | Snapshot shows busy → SSE idle event → returns nil |
| US-27b.2 | `TestWaitUntilIdle_NeverIdle_TimeoutReturnsDrainError` | Returns *ErrDrainTimeout with busy session list |
| US-27b.2 | `TestWaitUntilIdle_ContextCancelled_ReturnsErr` | Caller disconnect → returns ctx.Err() |
| US-27b.2 | `TestWaitUntilIdle_NewBusyDuringWait_HoldsTillIdle` | Session goes busy then idle → waits until idle |
| US-27b.2 | `TestWaitUntilIdle_SubscribeBeforeSnapshot_NoMissedTransition` | Session goes idle between subscribe and snapshot → correctly detected idle |
| US-27b.2 | `TestWaitUntilIdle_SnapshotFailure_ReturnsErr` | GET /session/status fails → error returned, unsub called |
| US-27b.3 | `TestReload_Drain_TriggersWaitUntilIdle` | drain=true → WaitUntilIdle invoked with correct workspace and timeout |
| US-27b.3 | `TestReload_Drain_Timeout_Returns408` | WaitUntilIdle returns ErrDrainTimeout → 408 with busySessionIDs |
| US-27b.3 | `TestReload_Drain_TimeoutCappedByConfig` | drainTimeoutSeconds=999999 → capped to DrainMaxTimeoutSeconds |
| US-27b.3 | `TestReload_NoDrain_ImmediateDisposeAsBefore` | drain=false → WaitUntilIdle NOT called; 27a behaviour preserved |
| US-27b.4 | `TestBulkReload_StreamsResultsAsNDJSON` | Each result emitted as separate NDJSON line before summary |
| US-27b.4 | `TestBulkReload_RespectsParallelism` | parallelism=2 → at most 2 concurrent reloads |
| US-27b.4 | `TestBulkReload_OnlyDirtyWorkspaces` | Workspaces with pending_refresh=false are skipped |
| US-27b.4 | `TestBulkReload_PartialFailure_StreamReflects` | One workspace fails → stream shows mixed; succeeded + failed match |
| US-27b.4 | `TestBulkReload_SummaryLineLast` | Last NDJSON line contains "summary" key |
| US-27b.4 | `TestBulkReload_NoPendingWorkspaces_SingleSummaryLine` | Empty pending → single NDJSON summary line, no result lines |
| US-27b.4 | `TestBulkReload_ParallelismCappedAt20` | parallelism=999 → server clamps to 20 |
| US-27b.4 | `TestBulkReload_ChannelFanIn_NoMutexContention` | 20 workers all emit results; no goroutine blocks on another |
| US-27b.5 | `TestProxyChatError_AgentNeedsRefresh_AddsHint` | 4xx + needsRefresh=true → response has hint, agentNeedsRefresh, credentialsPendingSince |
| US-27b.5 | `TestProxyChatError_FlagFalse_FieldsPassedThrough` | 4xx + needsRefresh=false → allowlisted fields present, no hint added |
| US-27b.5 | `TestProxyChatError_ModelNotFound_StructuredFieldsPreserved` | opencode ModelNotFoundError → providerID, modelID, suggestions all in response |
| US-27b.5 | `TestProxyChatError_NonJSON_WrappedSafely` | Plain text error → wrapped in {message: ...}, no extra fields |
| US-27b.5 | `TestProxyChatError_OversizedBody_TruncatedAndFlagged` | >4KB body → truncated, bodyTruncated: true in response |
| US-27b.5 | `TestProxyChatError_AllowlistBlocksUnknownFields` | Upstream returns unknown field → dropped from response |
| US-27b.5 | `TestProxyChatError_5xxAlsoEnriched` | 5xx with needsRefresh=true → also gets hint |
| US-27b.5 | `TestProxyChatSuccess_NotBuffered` | 200 streaming response passes through immediately; no buffer allocation |
| US-27b.5 | `TestProxyChatError_PreProxy4xx_NotEnriched` | Auth failure (before proxy) → no enrichment middleware in path |
| US-27b.8 | `TestMetricsService_RecordAgentReload_IncrementsCounters` | Counter and histogram updated correctly |
| US-27b.8 | `TestMetricsService_PendingGauge_PeriodicScan` | Gauge updated from DB count; goroutine respects deadline |

---

## Failure-Prone Areas

| # | Area | Mitigation |
|---|---|---|
| BF.1 | SSE drops mid-drain after snapshot but before all idle events arrive | Deadline fires; returns ErrDrainTimeout with last-known busy sessions; caller retries |
| BF.2 | Channel full during SSE burst (>64 concurrent transitions) | Dropped `idle` event → session stays in busy set → drain deadline fires → ErrDrainTimeout → caller retries. On retry, `GET /session/status` snapshot shows session as idle → drain completes immediately. Correctness is preserved; the worst case is a spurious timeout on extreme burst. 64 is a sufficient capacity: typical workspaces have 1-5 sessions and 64 concurrent transitions in a burst is effectively impossible in practice. This was raised as a potential permanent-deadlock concern and assessed as **invalid** — the retry path correctly resolves it. |
| BF.3 | Bulk request client disconnects mid-stream | `c.Request.Context()` cancels; worker goroutines exit via context cancellation inside `reloadOne`; `wg.Wait()` unblocks promptly; `results` channel is closed; writer loop exits. Dispose calls already started continue (fast and idempotent). |
| BF.4 | Parallel drain calls for same workspace | Each call has its own SubscribeDrain subscription and busy set; fully independent; no shared mutable state |
| BF.5 | Large opencode error body (>>4KB) | Body cap with truncation flag; `bodyOverflow` surfaced as `bodyTruncated: true` in response |
| BF.6 | Error enrichment applied to non-chat route | Middleware is applied explicitly per route (not as a blanket middleware); accidental application caught by `TestProxyChatError_PreProxy4xx_NotEnriched` |
| BF.7 | Metrics scan goroutine blocked on slow DB | 30s cadence; each scan has its own context with a 5s deadline; failure logs but does not affect request path |

---

## Files Likely Affected

| Path | Change |
|---|---|
| `api/internal/handlers/password_getter.go` | NEW: `WorkspacePasswordGetter` — extracted from `ProxyHandler.getPassword`; shared by ProxyHandler and AgentReloadHandler |
| `pkg/agent/opencode/client.go` | Add `GetSessionStatuses` |
| `pkg/agent/opencode/client_test.go` | Tests for new method |
| `api/internal/handlers/session_tracker.go` | Add `SubscribeDrain`, `drainSubs`, `drainSubCounter`; update `dispatchProperties`; add `InjectSSETracker` / `InjectPasswordGetter` setters on ProxyHandler |
| `api/internal/handlers/session_tracker_test.go` | Tests for fan-out |
| `api/internal/handlers/agent_drain.go` | NEW: `WaitUntilIdle`, `ErrDrainTimeout` |
| `api/internal/handlers/agent_drain_test.go` | NEW |
| `api/internal/handlers/agent_reload.go` | Update `Reload` for drain; add `BulkReload`; add `sseTracker`, `getPassword`, `config` fields |
| `api/internal/handlers/agent_reload_test.go` | Add drain and bulk tests |
| `api/internal/handlers/proxy_chat_enrichment.go` | NEW: `chatErrorEnrichmentWriter`, `enrichChatErrorBody`, allowlist |
| `api/internal/handlers/proxy_chat_enrichment_test.go` | NEW |
| `api/internal/handlers/proxy.go` | Apply enrichment writer to SendMessage and related chat-proxy routes; remove inline `getPassword` method; add `InjectPasswordGetter` and `InjectSSETracker` setters |
| `api/internal/services/workspace/workspace_service.go` | Add `ListPendingReloadWorkspaces` |
| `api/internal/interfaces/interfaces.go` | Add `ListPendingReloadWorkspaces` to `DatabaseService` interface (the three 27a methods were added in 27a's PR) |
| `api/internal/mocks/database.go` | Add stub implementation for `ListPendingReloadWorkspaces` |
| `api/internal/services/database/database.go` | Add `ListPendingReloadWorkspaces` |
| `api/internal/services/metrics/metrics.go` | Add `RecordAgentReload`, `RecordAgentReloadDrainTimeout`, `RecordAgentReloadBulk`; add pending gauge |
| `api/internal/server/router.go` | Register `/users/me/agents/reload` |
| `api/internal/app/app.go` | Wire `sseTracker`, `getPassword`, `config` into `AgentReloadHandler` |
| `api/openapi.yaml` | Document drain mode, bulk endpoint, error envelope |
| `docs/api/credentials.md` | NEW |
| `frontend/src/lib/components/AgentReloadModal.svelte` | Add drain checkbox (default checked); client-side elapsed counter |
| `frontend/src/lib/components/WorkspaceListAgentReloadBanner.svelte` | "Reload all" action calls bulk endpoint; NDJSON stream reader |
| `worklogs/01XX_YYYY-MM-DD_epic-27b-credential-reload-polish.md` | NEW |

---

## Success Criteria

1. Drain mode waits for all sessions to be idle before disposing. Initial state comes from `GET /session/status`; transitions come from the SSETracker event stream. Verified by US-27b.2 / US-27b.3 tests.
2. `TestWaitUntilIdle_SubscribeBeforeSnapshot_NoMissedTransition` specifically proves the subscribe-first ordering is correct.
3. Bulk reload across N workspaces streams per-workspace results as they complete; never buffers all results before writing.
4. Chat-proxy errors include `agentNeedsRefresh` hint when relevant; allowlist preserves all opencode structured error fields; non-chat routes are unchanged.
5. SDK callers can typed-catch `AgentNeedsRefreshError` and recover idiomatically.
6. `metrics.Service` extension follows the existing registration pattern; no package-level `prometheus.NewCounterVec` vars in handler code.
7. No regression of Epic 27a's foundation behaviour; US-27a.9 integration test still passes.
