# Worklog: Epic 44 + 45 Audit Remediation (Worklog 371 Findings)

**Date:** 2026-06-19
**Session:** Fix all 14 validated findings (5 CRITICAL, 5 HIGH, 4 MEDIUM) from the Epic 44 + 45 post-completion audit (worklog 0371). TDD throughout; adversarial self-review per Rule 11.
**Status:** Complete

---

## Objective

Worklog 0371 documented 14 real findings (C1–C5, H1–H5, M1–M4) plus 3 false alarms (FA1–FA3, no action needed) and 5 architectural observations (A1–A5, design tradeoffs, not bugs). The objective was to fix all 14 real findings, validate they are resolved, and confirm no new issues were introduced — per the user's instruction "Fix all the issues and validate they are resolved and no other issues exist."

The false alarms and architectural observations are explicitly out of scope (they were verified as non-issues or design tradeoffs by the audit).

---

## Work Completed

### C1 — US-44.8 metrics not scraped in production (no ServiceMonitor for agentd)

**Root cause:** The chart shipped ServiceMonitors for the API and controller but nothing for workspace pods' agentd admin port (`:4098/metrics`). The US-44.8 gauges (`workspace_restarts_total`, `workspace_memory_bytes`, `workspace_active_sessions`, `workspace_context_tokens`, `workspace_oom_kills_total`) were never scraped. The `ops_metrics.go` comment also referenced a stale `:9090` port.

**Fix:**
- Added `charts/llmsafespace/templates/podmonitor-agentd.yaml` — a PodMonitor (not ServiceMonitor, because workspace pods are dynamic and addressed by PodIP with no long-lived Service) targeting the `agentd-admin` container port on pods labeled `app=llmsafespace, component=workspace`. The `job` relabeling targets `<release>-agentd` so existing alert expressions matching `job=~".*agentd.*"` fire.
- Added a NetworkPolicy ingress rule (gated by `monitoring.serviceMonitors.agentdPodMonitor.enabled`, default true) permitting Prometheus pods (`networkPolicy.prometheusPodLabelSelector`, default `app.kubernetes.io/name: prometheus`) to reach workspace pods on port 4098. The `/metrics` endpoint is unauthenticated; the NetworkPolicy is the sole network-layer control.
- Added `monitoring.serviceMonitors.agentdPodMonitor.enabled` and `networkPolicy.prometheusPodLabelSelector` / `networkPolicy.prometheusNamespace` values.
- Fixed the stale `:9090` comment in `cmd/workspace-agentd/ops_metrics.go` to `:4098`.
- Updated the stale "operators must configure pod-level scraping" comments in `charts/llmsafespace/templates/prometheus-rules.yaml` to point at the chart-shipped PodMonitor.

**Validation:** `helm template` renders the PodMonitor and NetworkPolicy rule correctly when enabled, and omits both when disabled. `helm lint` passes.

### C2 — Session-status tracker unreliable (stale-busy + cold-start false-idle)

**Root cause:** (a) A stale "busy" entry persisted after opencode died mid-busy and was respawned — `prune()` only ran from the statusz fetch path. (b) An empty tracker (agentd restart, SSE not yet reconnected) caused `makeSessionAwareRestartDecision` to immediately restart, destroying in-flight agentic work — an Incident B regression.

**Fix:** Rewrote `makeSessionAwareRestartDecision` (`cmd/workspace-agentd/secrets.go`) to:
- (C2a) Accept a `sessionLister func(ctx) []string` that probes opencode's `/session` endpoint. The deferred-restart goroutine prunes stale busy entries on every poll tick via the live session list.
- (C2b) Treat an empty tracker as "unknown" rather than "all idle": probe opencode. If opencode is reachable with sessions, DEFER (sessions might be busy but invisible). If opencode is unreachable, restart immediately (nothing to lose). This eliminates the cold-start false-idle regression.
- Added `pruneFromLister` and `trackerHasBusyOrUnknown` helpers. `pruneFromLister` skips the probe when the tracker is empty (nothing to prune) to avoid a redundant HTTP call alongside `trackerHasBusyOrUnknown`.

**Validation:** New tests `TestSessionAwareRestartDecision_C2a_PruneClearsStaleBusy`, `..._C2b_EmptyTracker_OpencodeAliveWithSessions_Defers`, `..._C2b_EmptyTracker_OpencodeUnreachable_RestartsImmediately`, `..._C2b_EmptyTracker_OpencodeAliveNoSessions_RestartsImmediately`, `..._NilTracker_NilLister_RestartsImmediately`.

### C3 — 30min activeSess TTL expires during multi-hour agentic turns

**Root cause:** `checkAndAddScript` refreshed the Redis SET TTL only on `CheckAndAddActiveSession` calls (POST requests + `onSessionActive`). A multi-hour single turn emitted `session.status=busy` once and then no further session.status events, so the 30-minute TTL expired mid-turn. A concurrent POST then passed the limit check → two concurrent turns → corrupt opencode SQLite history.

**Fix:** Added `TouchActiveSessions(workspaceID)` to the `wsstate.Store` interface. RedisStore implements it as `EXPIRE` on the active-set key (no-op on a non-existent key). InMemoryStore implements it as a no-op (no TTL). `onRawEvent` (`api/internal/handlers/proxy_events.go`) calls `TouchActiveSessions` on every SSE event, keeping the TTL fresh during any active turn that emits step/raw events.

**Validation:** `TestRedisStore_TouchActiveSessions_RefreshesTTL` (miniredis fast-forward proves TTL refresh), `..._NonExistentKey_NoOp`, `..._RedisDown_NoPanic`.

### C4 — PriorPhase fail-false triggers mass cache wipe on Redis blip

**Root cause:** `GetPriorPhase` returned `("", false)` on Redis error. `onPhaseChange` treated `!hadPrior` as first-invocation → `invalidateCaches` → `InvalidateAll` → wiped activeSess + deletedSessions + pwCache + wsConfig across all replicas. A transient Redis blip during a CRD watcher reconnect recreated the data-loss class Epic 45 exists to prevent.

**Fix:** `RedisStore.GetPriorPhase` now returns `("Active", true)` on Redis error — assuming the steady-state common case (Active→Active). This limits the damage to `InvalidateWorkspaceConfig` (the else branch) instead of a full cache wipe. The Creating→Active edge case (rare: requires Redis outage exactly at the transition) misses the SSE subscription restart, but the proxy path's `EnsureWatching` on the next request and the controller's periodic reconcile recover it.

**Validation:** Updated `TestRedisStore_GetPriorPhase_RedisDown_AssumesActiveToAvoidCacheWipe` (was `..._ReturnsFalse`).

### C5 — Request buffer has no global memory cap (DoS vector)

**Root cause:** `requestBuffer.maxSize` was per-workspace (10). Each body could be up to 10MB. 1000 users × 10 workspaces × 10 buffered × 10MB ≈ 1TB. A coordinated platform-wide restart could OOM the API server.

**Fix:** Added a global byte cap (`defaultGlobalBufferBytesCap = 500MB`) to `requestBuffer`, tracked via `atomic.Int64` with a CAS loop in `reserveGlobalBytes` (no oversubscription under concurrency) and released in `popHead`. Added `bodySize int` to `bufferedRequest` (set from `len(bodyBytes)` in `proxy.go`). 0-byte requests bypass the cap (they don't consume budget). Added `workspace_request_buffer_global_bytes` gauge and `workspace_request_buffer_global_full_total` counter metrics. `popHead` now takes the `*requestBuffer` to release bytes; all call sites updated.

**Validation:** `TestRequestBuffer_C5_GlobalByteCap_RejectsOversizedRequest`, `..._ReleasedOnPop`, `..._ZeroBodySizeAlwaysAdmitted`, `..._ConcurrentNoOversubscribe` (50 goroutines competing for a 1000-byte cap with 30-byte requests — asserts no oversubscription via CAS).

### H1 — Deferred-restart goroutine is uncancellable and unbounded

**Root cause:** The deferred-restart goroutine polled every 5s with no `context.Context`, no `bgWg` tracking, no max-defer timeout. On shutdown, `bgCancel()` did not cancel it. If sessions stayed busy forever (stuck tool, infinite loop), credentials silently never applied.

**Fix:** `makeSessionAwareRestartDecision` now takes `ctx context.Context` and `bgWg *sync.WaitGroup`. The goroutine selects on `ctx.Done()` (cancellation at shutdown — H1a), force-restarts after `maxDefer` (default 2h — H1b), and registers with `bgWg` (H1c) so shutdown waits for it before `proc.stop()`. `reloadSecretsHandler` receives `bgCtx` and `bgWg` via the new `reloadSecretsDeps` struct.

**Validation:** `TestSessionAwareRestartDecision_H1a_ContextCancel_StopsGoroutine`, `..._H1b_MaxDefer_ForceRestarts`, `..._H1c_WaitGroupTracked`.

### H2 — `workspace_restarts_total` missing secret-change restarts

**Root cause:** `RecordRestart` was called only from the crash (`main.go`) and oom (`oom_detection.go`) paths. The secret-reload path wrote the restart-reason marker but never called `RecordRestart`. The most common restart type (user-initiated credential change) was invisible in Prometheus.

**Fix:** `reloadSecretsHandler` now calls `pkgOpsMetrics.RecordRestart(workspaceIDFromEnv(), metricRestartReason(reason))` alongside the marker write. Added `metricRestartReason` helper mapping marker reasons (`env_secrets_changed`, `api_key_changed`) to the short metric labels (`env_secrets`, `api_key`) matching the help text and crash/oom reasons.

**Validation:** `TestReloadSecretsHandler_H2_EnvSecretRecordsRestartMetric`, `..._H2_APIKeyRecordsRestartMetric`, `TestMetricRestartReason_MapsMarkerReasonToMetricLabel`.

### H3 — Plaintext workspace passwords in Redis

**Root cause:** `SetCachedPassword` stored the password as a plaintext Redis STRING. K8s Secrets are encrypted at rest; Redis typically is not. Hashing is not viable (the proxy needs the plaintext for Basic-Auth on every forwarded request).

**Fix:** Documented the tradeoff and the production requirement (Redis TLS in-transit, at-rest encryption, NetworkPolicy restricting ingress to API pods) in three places: `SetCachedPassword` code comment (`wsstate/redis.go`), the Redis section of `charts/llmsafespace/values.yaml`, and this worklog. This is a deployment responsibility, not a code-level control — the source of truth remains the K8s Secret (encrypted at rest), the Redis cache is bounded by a 1h TTL, and the passwords are per-workspace generated credentials (not user passwords).

### H4 — cgroup v2 assumption silently false on v1 hosts

**Root cause:** `memory_pressure.go` and `oom_detection.go` read `/sys/fs/cgroup/memory.*` directly with no v1 fallback and no error signal. On cgroup v1 hosts: no memory warnings, no `workspace_memory_bytes` gauge, no OOM-limit logging — silently dead.

**Fix:** Documented cgroup v2 as a hard requirement in `charts/llmsafespace/values.yaml` (new `workspace.cgroupV2Required: true` section with rationale and the list of affected features). Added a one-shot `sync.Once` warning log in `memoryPressureMonitor.check()` when the cgroup v2 read fails, so the silent degradation is observable. The warning lists exactly which features are unavailable (pressure warnings, memory gauge, OOM-limit detection) and points at the values.yaml documentation. cgroup v2 is the default on all supported runtime images (Debian bookworm-slim) and all modern node OSes.

### H5 — OOM marker is dead data

**Root cause:** `OOMMarkerPath` + `writeOOMMarker` + `getMemoryLimit` + `formatBytes` had ZERO read-side consumers across the controller, API, and frontend. The restart-reason marker (`reason="oom"`) subsumed the useful information; the `exitCode`/`memoryLimit` fields were dead data.

**Fix:** Removed `OOMMarkerPath`, `writeOOMMarker`, `getMemoryLimit`, and `formatBytes` from `cmd/workspace-agentd/oom_detection.go`. Updated `handleOOMExit` to drop the `oomMarkerPath` parameter (it now writes only the restart-reason marker). Updated `main.go` supervisor to use the new 2-arg signature. Updated `restart_reason.go` doc comment. Updated `oom_detection_test.go` (removed `writeOOMMarker` tests; new `TestHandleOOMExit_DoesNotWriteOOMSpecificMarker` proves the dead marker is not created) and `restart_reason_test.go` (`TestRestartReasonMarker_IndependentOfOOMMarker` → `..._LogDoesNotTouchSiblingFiles` using a generic sibling file).

**Validation:** `TestHandleOOMExit_WritesRestartReasonMarker`, `..._DoesNotWriteOOMSpecificMarker`, `..._EmptyWorkspaceID_DefaultsToUnknown`, `TestRestartReasonMarker_LogDoesNotTouchSiblingFiles`.

### M1 — `deletedSessions` fail-closed silently halts session-event processing

**Root cause:** `IsSessionDeleted` returned true on Redis error (fail-closed). Consumed at 4 call sites gating session-event processing. A Redis outage made ALL sessions look deleted → ALL session-event processing stopped with no alert.

**Fix:** Added a dedicated Prometheus counter `ws_state_is_session_deleted_fail_closed_total` incremented on every fail-closed return in `RedisStore.IsSessionDeleted`. The existing `ws_state_errors_total{op="is_session_deleted"}` counter tracked errors but not the operational impact; the new counter is explicitly named for ops dashboards and alerting. The fail-closed behavior itself is unchanged (data integrity > availability — per the original design rationale).

**Validation:** `TestRedisStore_IsSessionDeleted_RedisDown_IncrementsFailClosedCounter`, `..._RealTombstone_DoesNotIncrementCounter`.

### M2 — `agent_died` not replayed on frontend reconnect

**Root cause:** `onAgentDied` published only via `PublishToWorkspace` (no replay buffer). `onSessionIdle`/`onSessionActive` dual-published to both workspace and user channels (user has replay). A frontend reconnecting after agent death missed the event permanently.

**Fix:** `onAgentDied` now also calls `PublishToUser` when the workspace owner is known, mirroring the dual-publish pattern of `onSessionIdle`/`onSessionActive`.

**Validation:** `TestProxy_OnAgentDied_AlsoPublishedToUserChannel`, `TestProxy_OnAgentDied_UserChannelReplaySurvivesReconnect` (subscribes AFTER the event fires and verifies the replay buffer contains it).

### M3 — Deferred-restart marker accuracy for restarts that never fire

**Root cause:** The restart-reason marker is written at decision time (before `makeSessionAwareRestartDecision`). If the deferred restart never fires (pod dies first), the marker records a restart that didn't happen.

**Fix:** Documented as a known limitation in `writeRestartReasonMarker`'s doc comment. The marker accurately records "a restart was REQUESTED" which is operationally useful even if it didn't fire. The 10-minute stale threshold (`restartReasonStaleThreshold`) partially mitigates. Moving the write to restart-completion time would lose attribution entirely if the pod died mid-restart (the worst time to lose it). No code change — documentation only.

### M4 — RedisStore silently falls back to InMemoryStore with no warning

**Root cause:** `app.go` wired RedisStore only if `svc.Cache.(*cache.Service)` type-asserted successfully. On failure, ProxyHandler used InMemoryStore with no warning. A future refactor wrapping the cache service would silently reintroduce multi-replica drift.

**Fix:** Added an `else` branch in `api/internal/app/app.go` that logs a warning when the Redis cache service is unavailable, explicitly naming the consequence (multi-replica state will not be shared) and the expected context (single-replica dev/test is fine; investigate in production).

---

## Key Decisions

1. **PodMonitor over ServiceMonitor for C1.** Workspace pods are dynamic (one per Workspace CRD) and addressed by PodIP with no long-lived Service. A PodMonitor is the technically correct Prometheus Operator resource for this topology; a ServiceMonitor requires a Service. The worklog said "ServiceMonitor" loosely; the chart comment and prometheus-rules updates document the choice.

2. **C2 cold-start: defer when opencode is reachable, restart when unreachable.** The empty-tracker case is ambiguous (sessions might be busy but invisible). Probing opencode's `/session` endpoint resolves the ambiguity: reachable = alive (defer), unreachable = down (restart). This preserves the Incident B fix's intent (don't destroy in-flight work) while eliminating the cold-start false-idle regression.

3. **H1 maxDefer = 2h.** The US-44.2 design explicitly removed the forced timeout per user requirement. The audit (H1) identified the consequence: stuck sessions defer forever. 2h is long enough that legitimate agentic turns (tens of minutes) are not interrupted, bounded enough that stuck sessions eventually get the credential applied. The force-restart logs a warning so the operator can correlate.

4. **C4 return `("Active", true)` on Redis error.** Rather than changing the Store interface to return errors (which would ripple through all callers), the worklog's first option was chosen: assume the steady-state common case. The Creating→Active edge case is documented and recovered by the proxy path and controller reconcile.

5. **H5 removed `getMemoryLimit`/`formatBytes` too.** They were used only by `writeOOMMarker`. Per Rule 5 (zero tech debt), removing dead code includes its dead dependencies. `readCgroupMemoryCurrent` stays (used by the metrics loop and pressure monitor).

6. **C5 atomic.Int64 with CAS loop.** A mutex would serialize all enqueues globally (hot path). The CAS loop is lock-free and prevents oversubscription under concurrency. The defensive negative-reset in `releaseGlobalBytes` masks double-release bugs but prevents false admission.

---

## Assumptions (Rule 7)

| Assumption | Validation |
|---|---|
| Workspace pods have no long-lived Service (only PodIP) | Verified: `controller/internal/workspace/pod_builder.go` defines container ports but no Service is created for workspace pods in `charts/llmsafespace/templates/`. |
| The `/metrics` endpoint on the agentd admin port is unauthenticated | Verified: `cmd/workspace-agentd/main.go:1032` `adminMux.Handle("/metrics", promhttp.Handler())` — not wrapped in `requireBearerToken`. The NetworkPolicy is the control. |
| opencode's `/session` endpoint returns session IDs but not busy/idle status | Verified: `OpenCodeClient.ListSessions` hardcodes `Status: "idle"` — the SSE tracker is the source of busy/idle. This is why C2 probes reachability, not status. |
| `PublishToUser` has a replay buffer; `PublishToWorkspace` does not | Verified: `eventbroker/user_broker.go:129-146` (`PublishToUser` appends to `sh.replay[userID]`); `:148-158` (`PublishToWorkspace` does not touch replay). |
| cgroup v2 is the default on all supported runtime images | Verified: runtime images are Debian bookworm-slim (README-LLM.md tech stack); bookworm uses cgroup v2 by default. |
| `handleOOMExit`'s `oomMarkerPath` parameter was only used to call `writeOOMMarker` | Verified via grep: only `main.go:1318` and `oom_detection.go` referenced it; all were write-side. Zero read-side consumers. |
| The Store interface has no mock (tests use real InMemoryStore/miniredis) | Verified via grep: no `MockStore` or `fakeStore` in the codebase; `router_admin_session_test.go` uses real implementations. |

---

## Adversarial Self-Review (Rule 11)

### Phase 1 — Weaknesses identified

1. **C2 handler latency**: `makeSessionAwareRestartDecision` now probes opencode (up to 3s) in the reload handler path.
2. **C2 redundant probe**: `pruneFromLister` + `trackerHasBusyOrUnknown` could both probe opencode for an empty tracker.
3. **C5 releaseGlobalBytes negative**: a bookkeeping bug could drive the counter negative.
4. **H1 maxDefer vs US-44.2 "no forced timeout"**: the design explicitly removed the timeout; re-adding it is a behavior change.
5. **C4 Creating→Active edge case**: wrong prior during Redis outage misses SSE subscription restart.

### Phase 2 — Validation

1. **C2 handler latency — ACCEPTABLE.** The probe runs AFTER `reloadMu.Unlock()` (no mutex held). The reload handler is an internal endpoint (POST `/v1/reload-secrets`), not user-facing. 3s additional latency on a credential reload is acceptable. Fixed #2 by making `pruneFromLister` skip the probe when the tracker is empty (nothing to prune), so worst case is one 3s probe, not two.

2. **C5 negative counter — DEFENDED.** `releaseGlobalBytes` clamps at 0 and resets. The `workspace_request_buffer_global_bytes` gauge would show 0 if this happens (observable). A double-release would indicate a real bug worth investigating, but the clamp prevents false admission.

3. **H1 maxDefer — INTENTIONAL per worklog.** The worklog H1 finding explicitly recommends the timeout. The US-44.2 "no forced timeout" was the original design; the post-completion audit identified the consequence (silent non-application of credentials). 2h is a generous safety net that doesn't conflict with normal operation. Documented in the code comment.

4. **C4 edge case — DOCUMENTED.** The Creating→Active-during-Redis-outage case is rare (requires Redis outage exactly at the transition) and recoverable (proxy `EnsureWatching` on next request + controller reconcile). Documented in the `GetPriorPhase` code comment.

### Phase 3 — Result

Zero unresolved real findings. All identified weaknesses are either fixed (#2), defended (#3), intentional per the worklog (#4), or documented edge cases (#5).

---

## Blockers

None.

---

## Tests Run

| Command | Outcome |
|---|---|
| `go build ./...` | PASS |
| `go vet ./cmd/workspace-agentd/ ./api/internal/handlers/ ./api/internal/services/wsstate/ ./api/internal/app/ ./api/internal/services/metrics/ ./api/internal/services/eventbroker/` | PASS (no output) |
| `gofmt -l` on all changed files | PASS (no output after formatting) |
| `go test -timeout 120s -count=1 -short -race ./api/internal/services/wsstate/` | PASS (3.8s) |
| `go test -timeout 120s -count=1 -short -race ./api/internal/handlers/` | PASS (65.8s) |
| `go test -timeout 120s -count=1 -short -race ./cmd/workspace-agentd/` | PASS (63.1s) |
| `go test -timeout 60s -count=1 -short -race ./api/internal/app/ ./api/internal/services/eventbroker/ ./api/internal/services/metrics/` | PASS |
| `go test -timeout 120s -count=1 -short ./controller/internal/workspace/ ./api/internal/server/` | PASS |
| `go test -timeout 120s -count=1 -short ./controller/... ./pkg/agentd/... ./pkg/apis/...` | PASS |
| `helm lint charts/llmsafespace/` | PASS (1 chart linted, 0 failed) |
| `helm template` with monitoring + networkPolicy enabled | PASS (PodMonitor + NetworkPolicy rule render correctly) |
| `helm template` with defaults (monitoring disabled) | PASS (PodMonitor + NetworkPolicy rule omitted) |

`golangci-lint` is not installed in this environment; `go vet` + `gofmt` cover the static-analysis gate. Full `make lint` should be run in CI.

---

## Next Steps

1. **Run `make lint` (golangci-lint) in CI** — not available in this environment. The changes pass `go vet` and `gofmt`.
2. **Deploy and verify PodMonitor scrapes metrics** — the PodMonitor + NetworkPolicy are the C1 fix; production verification requires a cluster with Prometheus Operator.
3. **Consider A1–A5 architectural observations** from worklog 0371 in a future planning session — they are design tradeoffs (e.g. request buffering layer, memory-based admission control), not bugs.

---

## Files Modified

**Go source (production):**
- `cmd/workspace-agentd/main.go` — new `liveSessions` lister; updated `reloadSecretsHandler` call site with `reloadSecretsDeps`; updated `handleOOMExit` call (H5 signature change).
- `cmd/workspace-agentd/secrets.go` — rewrote `makeSessionAwareRestartDecision` (C2/H1); added `sessionLister`, `pruneFromLister`, `trackerHasBusyOrUnknown`, `reloadSecretsDeps`, `metricRestartReason`; added H2 `RecordRestart` call in `reloadSecretsHandler`.
- `cmd/workspace-agentd/oom_detection.go` — removed `OOMMarkerPath`, `writeOOMMarker`, `getMemoryLimit`, `formatBytes` (H5); updated `handleOOMExit` signature; H4 comment on `readCgroupMemoryCurrent`.
- `cmd/workspace-agentd/memory_pressure.go` — added `warnOnce sync.Once` field; H4 warning log in `check()`.
- `cmd/workspace-agentd/ops_metrics.go` — fixed stale `:9090` → `:4098` comment (C1).
- `cmd/workspace-agentd/restart_reason.go` — updated `RestartReasonMarkerPath` doc (H5); M3 known-limitation doc on `writeRestartReasonMarker`.
- `api/internal/handlers/proxy.go` — added `bodySize` to `bufferedRequest` construction (C5).
- `api/internal/handlers/proxy_events.go` — C3 `TouchActiveSessions` call in `onRawEvent`; M2 dual-publish in `onAgentDied`.
- `api/internal/handlers/proxy_request_buffer.go` — C5 global byte cap (`globalBytes`, `globalBytesCap`, `reserveGlobalBytes`, `releaseGlobalBytes`, `newRequestBufferWithGlobalCap`); `popHead` signature change; `bodySize` field.
- `api/internal/services/wsstate/store.go` — added `TouchActiveSessions` to the Store interface (C3).
- `api/internal/services/wsstate/redis.go` — C3 `TouchActiveSessions`; C4 `GetPriorPhase` returns Active on error; M1 fail-closed counter in `IsSessionDeleted`; H3 doc on `SetCachedPassword`; new `pkgIsSessionDeletedFailClosedTotal` metric.
- `api/internal/services/wsstate/inmemory.go` — C3 `TouchActiveSessions` no-op.
- `api/internal/app/app.go` — M4 warning log on InMemoryStore fallback.
- `api/internal/services/metrics/metrics.go` — C5 `SetRequestBufferGlobalBytes`, `RecordRequestBufferGlobalFull`, new gauges/counters.

**Go tests:**
- `cmd/workspace-agentd/session_aware_restart_test.go` — rewrote for C2/H1; new C2a/C2b/H1a/H1b/H1c tests.
- `cmd/workspace-agentd/secrets_test.go` — updated all `reloadSecretsHandler` call sites to `reloadSecretsDeps{}`; new H2 tests; `metricRestartReason` test.
- `cmd/workspace-agentd/oom_detection_test.go` — removed `writeOOMMarker` tests; new H5 tests.
- `cmd/workspace-agentd/restart_reason_test.go` — renamed marker-independence test.
- `api/internal/handlers/proxy_request_buffer_test.go` — new C5 tests.
- `api/internal/handlers/proxy_broker_agentdied_test.go` — new M2 tests.
- `api/internal/services/wsstate/redis_test.go` — new C3/M1 tests.
- `api/internal/services/wsstate/redis_phase_backfill_test.go` — updated C4 test.

**Helm chart:**
- `charts/llmsafespace/templates/podmonitor-agentd.yaml` — NEW (C1).
- `charts/llmsafespace/templates/workspace-network-policy.yaml` — Prometheus ingress rule (C1).
- `charts/llmsafespace/templates/prometheus-rules.yaml` — updated stale scraping comments (C1).
- `charts/llmsafespace/values.yaml` — `agentdPodMonitor`, `prometheusPodLabelSelector`, `prometheusNamespace`, `workspace.cgroupV2Required`, H3 Redis docs.
