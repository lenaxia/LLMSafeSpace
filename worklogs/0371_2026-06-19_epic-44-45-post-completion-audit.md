# Epic 44 + 45 Post-Completion Audit — Validated Findings

**Date:** 2026-06-19
**Session:** Deep independent audit of all Epic 44 + 45 work via 4 parallel skeptical sub-agents. Each finding was independently verified against actual code on main.
**Status:** Findings documented for fix tracking

---

## Methodology

4 independent audit agents examined: (1) Epic 45 Redis state, (2) Epic 44 agentd-side, (3) Epic 44 API-side, (4) cross-cutting assumptions. Each finding below was re-verified against the actual code on `main` — false findings are documented as such, real findings are tracked for fixes.

---

## CRITICAL (5 findings)

### C1 — US-44.8 metrics not scraped in production (no ServiceMonitor for agentd)
**Status:** REAL. **Verified.**
**Evidence:** `charts/llmsafespace/templates/servicemonitor.yaml` defines 2 ServiceMonitors (API + controller). No agentd ServiceMonitor exists. `charts/llmsafespace/templates/prometheus-rules.yaml:225-228` explicitly documents the gap: "ServiceMonitor can reach the metrics endpoint... the chart only ships ServiceMonitors for api and controller." The ops_metrics.go comment (`:9090`) is stale — actual port is `:4098` (`pkg/agentd/types.go:22`). The US-44.8 acceptance criterion "metrics exposed on `:9090/metrics`" is doubly wrong (wrong port, no scraper).
**Impact:** `workspace_restarts_total`, `workspace_memory_bytes`, `workspace_active_sessions`, `workspace_context_tokens`, `workspace_oom_kills_total` — none appear in production dashboards without manual operator config. The P0 motivation for US-44.8 ("ops monitoring is first-class citizen") is silently unmet.
**Fix:** Add a ServiceMonitor for agentd's admin port (`:4098/metrics`). Fix the stale `:9090` comment.

### C2 — Session-status tracker unreliable (stale-busy + cold-start false-idle)
**Status:** REAL (both sub-claims). **Verified.**
**Evidence:**
- **Stale-busy:** `hasAnyBusy()` (`main.go:265`) reads from `statuses` map, only updated by SSE `session.status` events (`main.go:449-483`). If opencode dies mid-busy without emitting idle, the stale "busy" entry persists until `prune()` runs (called from statusz fetch at `main.go:621`). A secret change during this window defers the restart unnecessarily.
- **Cold-start false-idle:** If agentd itself restarts (OOM, kubelet restart), the tracker map is empty → `hasAnyData()` returns false → the SSE-disconnect fallback (`secrets.go:92-100`) fires → immediate restart on first secret change. This destroys in-flight work — Incident B regression.
**Impact:** The single signal gating the Incident B fix can be wrong in both directions. Cold-start is the worst: agentd restart while opencode is busy → next secret change kills the session.
**Fix:** (a) For stale-busy: call `prune` on deferred-restart poll tick, not just statusz. (b) For cold-start: do NOT use empty-tracker as "all idle" — treat empty tracker as "unknown, defer with warning" or query opencode's `/session` API for live status.

### C3 — 30min activeSess TTL expires during multi-hour agentic turns
**Status:** REAL. **Verified.**
**Evidence:** `checkAndAddScript` (`redis.go:67-92`) refreshes TTL (`EXPIRE`) only on `CheckAndAddActiveSession` calls. This is called on new POST requests (`proxy.go:226`) and on `onSessionActive` SSE callbacks (`proxy_events.go:153`). `session.status=busy` is a state-transition event, not a periodic heartbeat — emitted once when the turn starts. A multi-hour single turn (the canonical AI-coding use case per README line 14) with no new POST has its activeSess entry expire after 30min. A concurrent POST then passes the limit check → two concurrent turns on the same session → corrupts opencode's SQLite session history.
**Impact:** Silent data corruption risk for the exact use case the system is built for.
**Fix:** Refresh TTL on SSE activity (session.status events), not just on CheckAndAdd. OR add a periodic TTL refresh in the SSE tracker's busy-session set.

### C4 — PriorPhase fail-false triggers mass cache wipe on Redis blip
**Status:** REAL but narrow. **Verified.**
**Evidence:** `GetPriorPhase` (`redis.go:686`) returns `("", false)` on Redis error. `onPhaseChange` (`proxy_events.go:101`) treats `!hadPrior` as first-invocation → calls `invalidateCaches` → `InvalidateAll` → clears activeSess + deletedSessions + pwCache + wsConfig. If Redis recovers between the GetPriorPhase error and the InvalidateAll writes, the data IS wiped.
**Impact:** A transient Redis blip during a CRD watcher reconnect can wipe per-workspace state across all replicas — recreating the data-loss class Epic 45 exists to prevent. Narrow probability (requires specific timing), HIGH blast radius.
**Fix:** PriorPhase fail-false should be treated as "assume Active→Active" (the common case), NOT "first invocation." Change the fail mode to return `("Active", true)` on error, or skip the invalidateCaches branch when the store had an error.

### C5 — Request buffer has no global memory cap (DoS vector)
**Status:** REAL. **Verified.**
**Evidence:** `requestBuffer.maxSize` (`proxy_request_buffer.go:140`) is per-workspace (default 10). No global aggregate cap. Each buffered request body can be up to 10MB (`proxy.go:259`). 1000 users × 10 workspaces × 10 buffered × 10MB = ~1TB. A coordinated platform-wide restart (e.g., opencode version rollout) could OOM the API server.
**Impact:** Platform-level DoS during coordinated restarts.
**Fix:** Add a global `atomic.Int64` tracking total buffered bytes; reject new buffers when a global cap (e.g., 500MB) is exceeded.

---

## HIGH (5 findings)

### H1 — Deferred-restart goroutine is uncancellable and unbounded
**Status:** REAL. **Verified.**
**Evidence:** `secrets.go:112` spawns `go func() { ticker := time.NewTicker(pollInterval); for range ticker.C { ... } }()` with no `context.Context`, no `bgWg` tracking, no max-defer timeout (dropped from the design per user requirement). On pod shutdown (`main.go:1091-1106`), `bgCancel()` does not cancel this goroutine. If opencode never goes idle (stuck tool, hung MCP, infinite loop), the goroutine polls every 5s forever. Credentials silently never apply.
**Impact:** Silent non-application of credentials for indefinitely-busy sessions. No user-visible signal that a restart is pending.
**Fix:** (a) Pass `bgCtx` to the goroutine so it's cancelled on shutdown. (b) Add a max-defer timeout (e.g., 2h) with a force-restart + log. (c) Track in `bgWg`.

### H2 — `workspace_restarts_total` missing secret-change restarts
**Status:** REAL. **Verified.**
**Evidence:** `RecordRestart` called only at `main.go:1325` ("crash") and `oom_detection.go:135` ("oom"). The secret-reload path (`secrets.go:486-498`) writes the restart-reason marker but never calls `RecordRestart`. The most common restart type (user-initiated credential change) is invisible in Prometheus.
**Impact:** Ops dashboards undercount restarts. The metric is incomplete.
**Fix:** Call `pkgOpsMetrics.RecordRestart(workspaceIDFromEnv(), reason)` in the secrets path alongside the marker write.

### H3 — Plaintext workspace passwords in Redis
**Status:** REAL but bounded. **Verified.**
**Evidence:** `SetCachedPassword` (`redis.go:551`) stores the password as a plaintext Redis STRING via `client.Set(key, password, ttl)`. K8s Secrets are encrypted at rest; Redis typically is not.
**Impact:** Per-workspace auth credentials exposed in Redis RDB/AOF dumps, memory, backups. Bounded: these are per-workspace generated credentials (not user passwords), on internal network.
**Fix:** Document the tradeoff and require Redis TLS + at-rest encryption in the production runbook. OR hash the password in Redis (store only a hash for comparison, fetch plaintext from K8s on miss).

### H4 — cgroup v2 assumption silently false on v1 hosts
**Status:** REAL but edge case in 2026. **Verified.**
**Evidence:** `memory_pressure.go:129`, `oom_detection.go:147,178` read `/sys/fs/cgroup/memory.*` directly. No v1 fallback. `getMemoryUsage` (`main.go:637`) has a `/proc/meminfo` fallback for statusz only, but the pressure monitor and ops metrics do not. On cgroup v1 hosts: no memory warnings, no `workspace_memory_bytes` gauge, no OOM limit logging — silently dead.
**Impact:** US-44.5, US-44.4 OOM limit, US-44.8 memory gauge all silently produce zero on cgroup v1 hosts. No error signal.
**Fix:** Add cgroup v1 detection + fallback paths (`/sys/fs/cgroup/memory/memory.limit_in_bytes` etc.) OR document cgroup v2 as a hard requirement in the Helm chart.

### H5 — OOM marker is dead data
**Status:** REAL. **Verified.**
**Evidence:** Grep for `OOMMarkerPath` across the entire repo shows only WRITE-side references (`main.go:1318`, `oom_detection.go:26,104-118`). Zero READ-side consumers — not in controller, API, or frontend. The restart-reason marker (reason=`oom`) subsumes the useful information.
**Impact:** `writeOOMMarker` and the `memoryLimit`/`exitCode` fields are dead code (~60% of the OOM marker's schema).
**Fix:** Remove `writeOOMMarker` and the `OOMMarkerPath` constant. The restart-reason marker already records reason=`oom`. OR wire a reader if the OOM-specific details are actually needed.

---

## MEDIUM (4 findings)

### M1 — `deletedSessions` fail-closed silently halts all session-event processing during Redis outage
**Status:** REAL. **Verified.**
**Evidence:** `IsSessionDeleted` (`redis.go:457`) returns true on Redis error (fail-closed). Consumed at `proxy_events.go:138` (sessionIndex.RecordMessage), `:142` (queueSvc drain), `:307` (title persistence), `:340` (context token recording). A Redis outage makes ALL sessions look deleted → ALL session-event processing stops.
**Impact:** During a Redis outage, session activity, titles, context tokens, and queued messages are silently dropped for every session on every workspace. No alert for the silent drop.
**Fix:** Log a metric/counter when `isSessionDeleted` returns true due to Redis error (vs actual tombstone). Consider fail-through for session-event processing (process events optimistically; worst case: a deleted session gets one extra event).

### M2 — `agent_died` not replayed on frontend reconnect
**Status:** REAL. **Verified.**
**Evidence:** `onAgentDied` (`proxy_events.go:199-207`) calls only `publishWorkspaceEvent` → `PublishToWorkspace`. `PublishToWorkspace` (`user_broker.go:148-158`) has NO replay buffer. Contrast: `onSessionIdle` publishes to BOTH workspace and user channels (user channel has replay). A frontend reconnecting after agent death misses the event permanently.
**Impact:** User who reconnects after an agent death sees no warning. Inconsistent with session.status events which survive reconnect.
**Fix:** Also publish `agent_died` via `PublishToUser` (the replay-capable channel), mirroring `onSessionIdle`'s dual-publish pattern.

### M3 — Deferred-restart marker accuracy for restarts that never fire
**Status:** REAL but documented. **Verified.**
**Evidence:** The restart-reason marker is written at decision time (`secrets.go:487`) BEFORE `makeSessionAwareRestartDecision`. If the deferred restart never fires (pod dies first), the marker records a restart that didn't happen. On next boot, the stale-marker detection (10min threshold) partially mitigates this.
**Impact:** Misleading log lines for deferred restarts that didn't complete. Bounded by the 10min stale threshold.
**Fix:** Document this as a known limitation. The marker accurately records "a restart was REQUESTED" which is operationally useful even if it didn't fire.

### M4 — RedisStore silently falls back to InMemoryStore with no warning
**Status:** REAL. **Verified.**
**Evidence:** `app.go:110-124`: RedisStore is wired only if `svc.Cache.(*cache.Service)` type-asserts successfully. If it fails, ProxyHandler uses `InMemoryStore` (`proxy.go:130`) with no warning log. A future refactor that wraps the cache service silently reintroduces multi-replica drift.
**Impact:** Silent degradation to single-replica behavior on misconfiguration.
**Fix:** Log a warning when Redis is unavailable and the system falls back to InMemoryStore.

---

## FALSE ALARMS (findings from the audit that are NOT real)

### FA1 — SIGKILL false-positive OOM classification
**Status:** FALSE. **Verified against code.**
**Audit claim:** "Every graceful `restart()` that escalates to SIGKILL after 5s will be recorded as `workspace_oom_kills_total`."
**Refutation:** The supervisor checks `restartRequested` (`main.go:1306`) BEFORE the crash path (`main.go:1315+`). `restart()` sets `restartRequested=true` (`main.go:1346`) before sending any signal. When the kill timer's SIGKILL causes `cmd.Wait()` to return, the supervisor sees `restartRequested=true` and loops WITHOUT calling `classifyExit` or `handleOOMExit`. The crash path (which calls `classifyExit`) is ONLY reached when neither `stopRequested` nor `restartRequested` is set — i.e., an unsolicited crash.

### FA2 — Drainer-exit / tryEnqueue race causes two concurrent drainers
**Status:** REAL but negligible impact. **Verified.**
**Audit claim:** Two drainers can run concurrently for the same workspace, breaking serial-FIFO.
**Refutation:** The race IS technically possible (orphaned queue ref), but: (a) the orphaned queue has exactly ONE request (the one that won the race), (b) both drainers process their own queues correctly, (c) no requests are lost, (d) FIFO ordering within each queue is preserved. The only impact is non-deterministic ordering between the orphaned queue's single request and the new queue's requests — which is acceptable since opencode processes them in arrival order anyway. Severity: NIT, not HIGH.

### FA3 — gin.Context cross-goroutine access is unsafe
**Status:** REAL concern but practically safe. **Verified.**
**Audit claim:** gin documents Context as not goroutine-safe; the buffer's drainer writes to `c.Writer` from a different goroutine.
**Refutation:** While gin's documentation says Context is not goroutine-safe, the buffer's pattern is sequentially safe: the handler goroutine parks in a `select` doing nothing with `c`; the drainer is the sole writer; the channel handoff establishes happens-before. `-race` passes. The concern is about gin UPGRADES changing this invariant, which is a maintenance risk, not a current bug. Should be documented but is not a correctness defect today.

---

## Architectural Observations (not bugs — design tradeoffs to consider)

### A1 — Request buffering may be at the wrong layer
The existing `msgqueue` (Redis-backed, durable, survives replica restart) could handle the restart-window case. The in-process buffer doesn't survive API replica restart. However, the msgqueue is session-scoped (for queued messages to idle sessions), not request-scoped, so it doesn't cleanly fit. The buffer is a reasonable solution but treats the symptom (restarts) rather than the cause.

### A2 — Incident A root cause under-diagnosed
`maxActiveSessions=5` is a count limit, not a memory limit. 16 sessions in 2GB = admission without memory awareness. US-44.4 detects the resulting OOM; US-44.5 warns at 85%; but nothing prevents the 16th session from being admitted. Memory-based admission control would be a root-cause fix.

### A3 — Session-aware restart should be controller-visible
The deferred restart lives in a free-floating agentd goroutine invisible to the controller, statusz, and frontend. A "pending restart" statusz field would give the controller/frontend visibility to surface it to the user and potentially trigger the restart when sessions drain.

### A4 — Redis-cached in-process maps vs shared SQL table
The design picked Redis because cache infrastructure exists. Postgres (alongside session_index) would have eliminated 6 distinct fail-mode behaviors. For O(1-10) writes per session lifecycle, SQL would have been simpler.

### A5 — "No forced timeout" doesn't account for stuck sessions
If opencode hangs (infinite loop, stuck MCP), `hasAnyBusy()` returns true forever. The design's 30min forced timeout was removed per user requirement. A compromise (force-restart if ALL sessions busy for >N hours with no status events) was never considered.

---

## Process Lesson (Rule 7)

The skeptical-validator process caught real bugs **within** each PR. But it did not catch **cross-cutting** assumptions spanning the Helm chart, runtime image, and production deployment:
- Several "validations" were tautological (verified the code does X, not that X is correct in production).
- Production-deployment assumptions (ServiceMonitor, LB timeouts, cgroup version) were never checked against the Helm chart.
- The P0 motivation for US-44.8 (ops observability) is silently unmet because metrics aren't scraped.

Rule 7 was followed **per-PR** but not **per-Epic**. Future epics should include a cross-cutting deployment-validation pass before declaring complete.
