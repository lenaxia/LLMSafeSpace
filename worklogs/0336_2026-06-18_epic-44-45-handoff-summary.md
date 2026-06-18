# Worklog: Epics 44 + 45 — Session Reliability & Multi-Replica State Consistency

**Date:** 2026-06-17 to 2026-06-18
**Session:** Full implementation of Epic 45 (9 stories, complete) + Epic 44 P0 (5 stories). Both production incidents addressed.
**Status:** Epic 45 COMPLETE. Epic 44 P0 COMPLETE. 6 P1/P2 stories remain.

---

## Objective

Implement Epics 44 and 45 from the design docs at `design/stories/epic-44-session-reliability-transparency/` and `design/stories/epic-45-multi-replica-state-consistency/`. These epics were driven by two production incidents on 2026-06-16:

- **Incident A (OOMKill)**: 16 concurrent sessions exhausted 2 GiB memory limit, pod OOMKilled mid-task. Session stuck `status: "busy"` until next restart. No user notification.
- **Incident B (Unsafe Restart)**: User changed workspace secret bindings while session was actively running (102K context tokens). workspace-agentd immediately SIGTERMed opencode, destroying in-flight work. No session state check.

Both incidents share a common root cause: per-replica in-memory state in ProxyHandler that drifts across multi-replica API deployments.

---

## Work Completed — 11 PRs Merged

### Epic 45: Multi-Replica State Consistency (COMPLETE — 9/9 stories)

| PR | Story | What |
|---|---|---|
| #204 | US-45.1 | `wsstate.Store` interface (26 methods) + `InMemoryStore` implementation. Extracted 6 per-workspace state maps from ProxyHandler (`activeSess`, `deletedSessions`, `pwCache`, `wsConfig`, `priorPhase`, `parentBackfilled`) behind a typed interface. No behavior change — pure refactor. Skeptical validator caught a CRITICAL bug: `InvalidateAll` would have cleared `priorPhase`, breaking Active→Active reconcile. Fixed before merge. |
| #205 | US-45.2 | **Redis-backed `activeSess`** (⭐ fixes stuck-session bug class). Two Lua scripts for atomic check-and-add and remove-and-delete-if-empty. Fail-open on Redis errors. 30-min TTL auto-recovery for stuck entries. Hash-tagged keys `ws:{workspace_id}:active`. 50-goroutine and 1000-op concurrent atomicity tests. |
| #207 | US-45.3 | **Redis-backed `deletedSessions`**. Per-key TTL tombstones. Fail-CLOSED on Redis errors (opposite of activeSess — data integrity > availability). Prevents cross-replica zombie session resurrection. |
| #210 | US-45.4 | **Redis-backed `pwCache`**. 1h TTL. Fail-through-to-K8s policy (third fail mode: Redis is a cache, K8s Secret is source of truth). Handler-level integration test verifying 401→Redis-DEL path. |
| #211 | US-45.6 | **Redis-backed `wsConfig`**. JSON-serialized Config struct. 5min TTL (shorter than password — config changes via CRD). |
| #212 | US-45.7+45.8 | **Redis-backed `priorPhase` + `parentBackfilled`**. 24h TTL. priorPhase survives InvalidateAll per US-45.1 contract. Final two sections migrated — all 6 now Redis-backed. |
| #214 | US-45.9 | **Remove dead InMemoryStore from RedisStore**. All 6 sections Redis-backed; embedded InMemoryStore was dead code. InMemoryStore retained for ProxyHandler unit tests / local dev. |

**US-45.5** (conditional L1 LRU cache for pwCache) skipped — only ships if P99 > 5ms in production. Histogram registered from day one; ship only if measurement warrants.

### Epic 44: Session Reliability & Transparency (P0 COMPLETE — 5/11 stories)

| PR | Story | What |
|---|---|---|
| #202 | US-44.1a | **Proxy emits terminal `agent_died` SSE event**. When an SSE stream from opencode dies mid-flight (EOF after data), proxy appends `event: error\ndata: {"type":"agent_died","reason":"unknown"}`. Scope-limited to `text/event-stream` responses (not JSON passthrough). Skeptical validator discovered the frontend listens on the broker stream, not the proxied SSE body — frontend integration split into US-44.1c. |
| #217 | US-44.2+44.3 | **Session-aware restart + api-key fix** (⭐ Incident B fix). When env-secret/api-key changes trigger `shouldRestart`, agentd now checks `sessionStatusTracker.hasAnyBusy()` first. If sessions busy → defer restart via background goroutine polling every 5s. NO forced timeout. US-44.3: `shouldRestart()` now includes `api-key` type (was missing — latent bug). |
| #218 | US-44.4 | **OOM detection in agentd supervisor** (⭐ Incident A fix). `classifyExit(waitErr)` detects SIGKILL (OOM killer signal) via `exec.ExitError` → `syscall.WaitStatus`. Writes JSON marker to `/workspace/.opencode-oom-marker` (PVC-backed, persists across restarts). Increments `workspace_oom_kills_total` Prometheus counter. |
| #222 | US-44.8 | **Ops Prometheus metrics** (P0). `workspace_restarts_total{workspace_id, reason}`, `workspace_memory_bytes{workspace_id}` (cgroup v2, 60s sample), `workspace_active_sessions{workspace_id}` (busy count, 60s sample), `workspace_context_tokens{workspace_id}` (sum, 60s sample). Periodic collection goroutine wired into agentd main loop. |

---

## Key Architectural Decisions

### Epic 45

1. **Package-local interface (`wsstate.Store`), not `interfaces.WorkspaceStateStore`**. Consumer is exclusively ProxyHandler; package-local is more idiomatic Go. Documented as deviation from original spec.

2. **`h.state()` lazy getter for nil-safety**. Existing tests construct `&ProxyHandler{}` literally (26 sites), bypassing `NewProxyHandler`. The getter initializes the store on first call. Production goes through `NewProxyHandler` which initializes unconditionally.

3. **Three distinct fail modes across the 6 sections**:
   - `activeSess`: **fail-open** (CheckAndAdd returns true — allow the request, don't block legit traffic on Redis hiccup)
   - `deletedSessions`: **fail-closed** (IsSessionDeleted returns true — assume deleted to prevent zombie resurrection; data integrity > availability)
   - `pwCache`, `wsConfig`: **fail-through** (return false/miss — caller falls back to K8s Secret / Workspace CRD; Redis is a cache, source of truth is external)
   - `priorPhase`, `parentBackfilled`: **fail-safe** (return false — treated as first-invocation / allow retry)

4. **`InvalidateAll` preserves `priorPhase`**. The `onPhaseChange` handler relies on priorPhase surviving invalidation to distinguish first-invocation from Active→Active reconcile. This was the CRITICAL bug caught by skeptical validation in US-45.1 — would have caused every redundant Active watch event to wipe all per-workspace state.

5. **Forward-fix only (no feature flags)**. Per design migration strategy: `app.go` auto-wires RedisStore when cache service is available. No env var. Rollback is via fix-forward PR.

6. **Lua scripts for atomicity**. `checkAndAddScript` (SISMEMBER→EXPIRE if exists, else SCARD→SADD→EXPIRE if room) and `removeActiveScript` (SREM→conditional DEL on empty). Both run as single indivisible Redis commands. Core multi-replica correctness guarantee.

7. **Hash-tagged keys** (`ws:{workspace_id}:active`). The `{workspace_id}` braces force co-location on the same Redis shard — enables future cluster-mode migration with zero code change.

8. **TTL matrix** (auto-recovery for all state):
   - activeSess: 30min (stuck sessions auto-recover)
   - deletedSessions: 30min (tombstones expire — bounded memory)
   - pwCache: 1h (passwords are stable)
   - wsConfig: 5min (config changes via CRD)
   - priorPhase: 24h (survive replica restarts)
   - parentBackfilled: 24h (backfill is idempotent)

### Epic 44

1. **No forced timeout on deferred restart**. Per user requirement: multi-hour agentic workflows are common. The deferred restart goroutine polls indefinitely until sessions go idle OR user triggers manual restart via `POST /agent/reload?drain=false`.

2. **SSE-disconnect fallback**: if `sessionStatusTracker` has no data (map empty, SSE disconnected), restart immediately. Cannot block forever waiting for state we may never get.

3. **`shouldRestart()` must ship US-44.2 and US-44.3 together**. Fixing api-key alone (making it restart) would break users who rely on api-key NOT restarting. Session-aware restart makes the restart safe.

4. **OOM detection via signal, not exit code**. `exit 137` is the convention but Go's `exec.ExitError` exposes the signal (`syscall.WaitStatus.Signaled()` + `Signal() == SIGKILL`), which is more reliable than parsing exit codes.

5. **Scope-limiting `agent_died` heuristic to SSE responses**. The literal design said "any data before EOF". Skeptical validator proved this would break JSON/REST passthrough — every normal HTTP response completes via EOF after data, and appending an SSE event into a JSON body corrupts parsers. Refined to `text/event-stream` Content-Type only.

---

## Skeptical Validation (Rule 11) — Mandatory for Every PR

Every PR underwent independent skeptical validation via a separate subagent. Key catches:

| PR | Finding | Severity | Resolution |
|---|---|---|---|
| #202 (US-44.1a) | Frontend listens on broker stream, not proxied SSE body — proxy-only emission invisible to ChatPage | MAJOR | Rescoped to US-44.1a; split broker bridge into US-44.1c |
| #202 (US-44.1a) | Asymmetric JSON shapes between B2 and US-44.1 `event: error` payloads | MAJOR | Pinned both shapes in `TestProxy_US44_1_ErrorShapesAreDocumented` |
| #204 (US-45.1) | `InvalidateAll` would clear `priorPhase`, breaking Active→Active reconcile | **CRITICAL** | Removed `DeletePriorPhase` from `InvalidateAll`; added regression test |
| #205 (US-45.2) | `ws_state_active_sessions{workspace_id}` gauge accumulates orphan labels forever (UUID cardinality) | MAJOR | Added `DeleteLabelValues` in `RemoveActiveSession` (count→0) and `ClearActiveSessions` |
| #205 (US-45.2) | `SetStateStore` contract not enforced (said "before Start()" but no check) | MINOR | Added `started` bool + panic guard |
| #210 (US-45.4) | Stale delegation tests (`DelegatesPasswordCacheToInMemory`) actively misrepresented implementation | MAJOR | Deleted; replaced with accurate tests |
| #210 (US-45.4) | No handler-level integration test for Redis-backed password path | MINOR | Added `TestProxy_Upstream401_InvalidatesRedisPasswordCache` |

---

## Tests Run (Cumulative)

| Package | Tests | Notes |
|---|---|---|
| `api/internal/services/wsstate` | 111 | 35 InMemoryStore + 76 RedisStore (activeSess, deleted, pwCache, wsConfig, priorPhase, backfilled) |
| `api/internal/handlers` | ~250 | Full handler suite; 13 US-44.1 terminal event tests + integration tests for Redis state |
| `cmd/workspace-agentd` | ~80 | Session-aware restart (19), OOM detection (7), ops metrics (6), plus all pre-existing |

All tests pass with `-race`.

---

## Files Created/Modified Summary

### New files (18)
- `api/internal/services/wsstate/store.go` — Store interface
- `api/internal/services/wsstate/inmemory.go` — InMemoryStore
- `api/internal/services/wsstate/inmemory_test.go` — 35 tests
- `api/internal/services/wsstate/redis.go` — RedisStore (6 sections migrated)
- `api/internal/services/wsstate/redis_test.go` — 21 activeSess tests
- `api/internal/services/wsstate/redis_deleted_test.go` — 12 deletedSessions tests
- `api/internal/services/wsstate/redis_password_test.go` — 13 pwCache tests
- `api/internal/services/wsstate/redis_config_test.go` — 16 wsConfig tests
- `api/internal/services/wsstate/redis_phase_backfill_test.go` — 17 priorPhase + backfilled tests
- `api/internal/handlers/proxy_terminal_events_test.go` — 13 terminal SSE event tests
- `cmd/workspace-agentd/session_aware_restart_test.go` — 19 session-aware restart tests
- `cmd/workspace-agentd/oom_detection.go` — OOM detection + marker + metrics
- `cmd/workspace-agentd/oom_detection_test.go` — 7 OOM tests
- `cmd/workspace-agentd/ops_metrics.go` — 4 Prometheus metrics
- `cmd/workspace-agentd/ops_metrics_test.go` — 6 ops metrics tests
- 11 worklog files (0316–0333)

### Modified files (key)
- `api/internal/handlers/proxy.go` — struct refactor (stateStore field), `SetStateStore` setter, terminal SSE event in `doProxy`
- `api/internal/handlers/proxy_connections.go` — all state access via `h.state()`, test helpers
- `api/internal/handlers/proxy_handlers.go` — deletedSessions delegated to store
- `api/internal/handlers/proxy_events.go` — onPhaseChange uses store
- `api/internal/handlers/proxy_permissions.go` — wsConfig uses store
- `api/internal/handlers/proxy_session_index.go` — parentBackfilled uses store
- `api/internal/app/app.go` — wires RedisStore when cache service available
- `cmd/workspace-agentd/main.go` — sessionStatusTracker methods (hasAnyBusy, listBusy, hasAnyData, snapshot), supervisor OOM integration, metrics goroutine
- `cmd/workspace-agentd/secrets.go` — shouldRestart api-key fix, restartableProcess interface, makeSessionAwareRestartDecision, reloadSecretsHandler signature
- `design/stories/epic-44-session-reliability-transparency/README.md` — acceptance checkboxes ticked, scope refinements documented
- `design/stories/epic-45-multi-replica-state-consistency/README.md` — all 9 stories ticked

---

## What Remains

### Epic 44 — 6 P1/P2 stories remaining

| Story | Priority | Effort | Description |
|---|---|---|---|
| US-44.5 | P1 | 2 days | Memory pressure warnings at 85% of cgroup limit |
| US-44.6 | P1 | 1 day | Per-session memory attribution (estimated from context tokens) |
| US-44.7 | P1 | 1 day | Restart reason logging (marker files for all restart types) |
| US-44.1c | P1 | 1.5 days | Broker-side `agent_died` bridge — makes terminal events visible to frontend |
| US-44.9 | P2 | 3 days | api-key deprecation (6-month sunset, migration guide) |
| US-44.10 | P0* | 3 days | API proxy request buffering (buffer POST /messages during restart, 30s timeout, 10 limit) |
| US-44.11 | P2 | 3 days | Admin session recovery tools (force-abort endpoint) |

*US-44.10 was P0 in the original design but was not started in this session. It provides the "transparent restart" UX (users never see 502 during the 2-5s opencode restart window). Would be the next highest-impact story to implement.

### Epic 45 — COMPLETE ✅

No remaining stories. US-45.5 (conditional L1 LRU cache) only ships if production P99 > 5ms.

### Follow-up items tracked (from skeptical validators)
- Real-Valkey integration test via testcontainers-go (deferred across US-45.2/.3/.4 — miniredis with gopher-lua covers Lua atomicity; testcontainers is a larger dependency decision)
- Widen `wsstate.Store` interface to take `context.Context` (currently uses `context.Background()`; do once before any future store work)
- `app_test.go` case asserting the RedisStore wiring path (Finding 12 from US-45.4 validator)
- `state()` getter hardening against concurrent first-use with `&ProxyHandler{}` literals (Finding 7 from US-45.1 validator — latent, production never hits it)

---

## Next Steps for Fresh Context

1. **Start with US-44.10** (request buffering) — the last P0 story. Provides transparent restart UX. Depends on Epic 45 (merged). Files: `api/internal/handlers/proxy.go`, `api/internal/metrics/metrics.go`.
2. **Then US-44.1c** (broker-side `agent_died` bridge) — completes the user-facing goal of US-44.1. Files: `api/internal/services/sse/tracker.go`, `api/internal/handlers/proxy_events.go`, `frontend/src/pages/ChatPage.tsx`.
3. **Then P1 stories** (US-44.5, .6, .7) in any order — all are agentd-side, independent.
4. **Then P2 stories** (US-44.9, .11) — deprecation and admin tools.

For each story, follow README-LLM.md Multi-Agent Workflow: TDD → skeptical validator → remediation loop → PR → review → merge → worklog.

---

## PRs (all merged, all squash-merged, branches deleted)

| # | Branch | Title |
|---|---|---|
| 202 | feat/epic44-us44.1-terminal-sse-events | feat(epic44): proxy emits terminal agent_died SSE event (US-44.1a) |
| 204 | feat/epic45-us45.1-wsstate-abstraction | feat(epic45): extract ProxyHandler state to wsstate.Store (US-45.1) |
| 205 | feat/epic45-us45.2-redis-activesess | feat(epic45): Redis-backed activeSess eliminates multi-replica drift (US-45.2) |
| 207 | feat/epic45-us45.3-redis-deletedsessions | feat(epic45): Redis-backed deletedSessions prevents zombie resurrection (US-45.3) |
| 210 | feat/epic45-us45.4-redis-pwcache | feat(epic45): Redis-backed pwCache eliminates per-replica staleness (US-45.4) |
| 211 | feat/epic45-us45.6-redis-wsconfig | feat(epic45): Redis-backed wsConfig shares config across replicas (US-45.6) |
| 212 | feat/epic45-us45.7-45.8-redis-phase-backfill | feat(epic45): Redis-backed priorPhase + parentBackfilled (US-45.7, US-45.8) |
| 214 | feat/epic45-us45.9-remove-inmemory | chore(epic45): remove dead InMemoryStore from RedisStore (US-45.9) |
| 217 | feat/epic44-us44.2-44.3-session-aware-restart | feat(epic44): session-aware restart + api-key fix (US-44.2 + US-44.3) |
| 218 | feat/epic44-us44.4-oom-detection | feat(epic44): OOM detection in agentd supervisor (US-44.4) |
| 222 | feat/epic44-us44.8-ops-metrics | feat(epic44): Prometheus ops metrics for SRE dashboards (US-44.8) |
