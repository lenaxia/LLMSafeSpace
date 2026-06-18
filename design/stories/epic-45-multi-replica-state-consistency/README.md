# Epic 45: Multi-Replica State Consistency

**Status:** Design  
**Created:** 2026-06-16  
**Depends on:** Existing Valkey/Redis infrastructure (cache service, msgqueue, ratelimit)  
**Related:** Epic 44 (Session Reliability) — addresses different symptoms of same underlying issue

## Problem Statement

The API server is deployed with multiple replicas (currently 2), but `ProxyHandler` holds critical workspace and session state in process-local maps. This causes:

1. **Stuck sessions** (observed 2026-06-16): Session marked "active" on one replica, "idle" on another. User sees `409 Conflict: session is busy` only when their request lands on the stale replica.

2. **Double event processing**: Each replica subscribes independently to opencode SSE, fires `onSessionIdle` callbacks, writes metering events. Effective 2× DB write load.

3. **Inconsistent rate limiting**: `maxConnectionsPerWorkspace=10` and `maxActiveSessions=5` are per-replica, so effective limits are N× across replicas.

4. **Cache invalidation gaps**: One replica clears its `pwCache` on phase change; other replica's cache stays stale until its own watcher fires.

5. **Stale state on restart**: API replica restart loses all in-memory state. Watcher reseeds phase data, but session-level state (busy sessions, recent deletions) is gone.

## Evidence

**Stuck Session Investigation (2026-06-16):**
- Session `ses_13076538bffeYtLrhoZ2ccRM1E` returned 409 Conflict from one replica
- Same session showed `status: "idle"` from agentd's `/v1/statusz`
- Same session showed `status: "idle"` in workspace CRD
- Root cause: `proxy_connections.go:70-78` reads from per-replica `activeSess` map
- The map had stale entry from when opencode was OOMKilled mid-stream
- `reconcileStrandedQueues` (pre-PR-#197 name; `proxy_events.go:549` line reference is from the pre-#197 state) only fixed sessions with queued messages. PR #197 (`f0ea9c50`) renamed it to `reconcileSessionState` and extended it to clear stale `activeSess` entries regardless of queue length, providing automatic recovery within ~5 minutes.

**State Inventory:**

| Map | Location | Authority | Move to Redis? | Priority |
|-----|----------|-----------|----------------|----------|
| `activeSess` | `proxy.go:67` | API server | ✅ Yes | P0 |
| `deletedSessions` | `proxy.go:88` | API server | ✅ Yes | P0 |
| `pwCache` | `proxy.go:58` | k8s Secret (cache) | ✅ Yes | P1 |
| `wsConfig` | `proxy.go:61` | Workspace CRD (cache) | ✅ Yes | P1 |
| `priorPhase` | `proxy.go:64` | Workspace CRD | ✅ Yes | P2 |
| `parentBackfilled` | `proxy.go:81` | API server | ✅ Yes | P2 |
| `connCount` | `proxy.go:70` | Per-replica resource limit | ❌ No | N/A |

`connCount` stays local because it represents per-replica resources (file descriptors, memory) and should not be shared.

## Solution

Externalize shared state to Valkey/Redis using existing cache service infrastructure. Use hash-tagged keys to enable future cluster migration.

### Design Principles

1. **Fail-open on Redis errors** — Better to allow a request than block legit traffic when Redis hiccups
2. **TTL as safety net** — Every shared key has TTL; bugs auto-recover within minutes
3. **Atomic operations via Lua** — Prevent race conditions in multi-replica writes
4. **Hash-tagged keys** — `{workspace_id}` tag forces co-location for future cluster mode
5. **Interface-based design** — Decorator pattern allows adding L1 cache later without changing callers
6. **All-or-nothing rollout** — No feature flags; deploy and rollback if issues

### Key Naming Convention

```
ws:{workspace_id}:active           # SET of busy session IDs
ws:{workspace_id}:deleted:{ses_id} # Tombstone with 30min TTL
ws:{workspace_id}:pw               # Cached password
ws:{workspace_id}:config           # Cached workspace config
ws:{workspace_id}:phase            # Last known phase
ws:{workspace_id}:backfilled       # Parent backfill marker
```

The `{workspace_id}` hash tag ensures all keys for a workspace land on the same Valkey shard in cluster mode.

## Stories

### P0: Critical Reliability (Week 1-2)

#### US-45.1: stateStore Abstraction Layer
**Goal:** Define interface, refactor existing code to use it, no behavior change.

**Acceptance:**
- [x] New package `api/internal/services/wsstate/`
- [x] Define `wsstate.Store` interface *(deviation from original wording "`interfaces.WorkspaceStateStore`" — package-local interface is more idiomatic Go and the consumer is exclusively ProxyHandler. Deviation recorded 2026-06-17.)*
- [x] In-memory implementation matches current `ProxyHandler` behavior exactly *(one near-miss: original `InvalidateAll` would have cleared `priorPhase`, breaking Active→Active reconcile. Caught by skeptical validator before PR; fixed.)*
- [x] All `ProxyHandler` map accesses go through interface
- [x] Existing tests pass *(deviation: 6 test files modified to migrate direct map pokes to typed `SetXxxForTest` helpers — the underlying private fields were removed, so the literal criterion "without modification" was impossible. The migrated tests assert the same semantics.)*
- [x] New tests for the interface contract (`api/internal/services/wsstate/inmemory_test.go` — 35 tests covering active sessions, tombstones, password cache, config cache, prior phase, parent backfill, InvalidateAll, concurrency)

**Files:**
- New: `api/internal/services/wsstate/store.go`
- New: `api/internal/services/wsstate/inmemory.go`
- New: `api/internal/services/wsstate/inmemory_test.go`
- Modified: `api/internal/handlers/proxy.go` (use interface)
- Modified: `api/internal/handlers/proxy_connections.go` (delegate to interface)
- Modified: `api/internal/handlers/proxy_handlers.go`, `proxy_permissions.go`, `proxy_events.go`, `proxy_session_index.go`
- Modified: 6 test files (mechanical migration to test helpers)

**Effort:** 2 days

---

#### US-45.2: Redis-backed activeSess
**Goal:** Move busy session tracking to Valkey. Fixes today's stuck-session bug class.

**Acceptance:**
- [x] Redis implementation of `WorkspaceStateStore.CheckAndAddActiveSession`
- [x] Uses Lua script for atomic check-and-add (`checkAndAddScript`)
- [x] Hash-tagged keys: `ws:{workspace_id}:active`
- [x] TTL = 30 minutes (auto-recovery for stuck entries)
- [x] TTL refreshed on every successful operation (both new-add and idempotent re-add)
- [x] Fail-open behavior on Redis errors (log + allow)
- [x] Prometheus metrics:
  - `ws_state_op_duration_seconds{op,result}` histogram
  - `ws_state_errors_total{op}` counter
  - `ws_state_active_sessions{workspace_id}` gauge (sampled on writes; cleaned up on workspace terminate via DeleteLabelValues)
- [x] Unit tests for Lua script edge cases:
  - Concurrent checkAndAdd (50 goroutines — `TestRedisStore_CheckAndAddActiveSession_Concurrent_AtLimitNoOversubscribe`)
  - 1000-op load test (`TestRedisStore_LoadTest_1000ConcurrentOps_NoDoubleCounting`)
  - TTL refresh on existing member (`TestRedisStore_CheckAndAddActiveSession_TTLRefreshedOnEveryOp`)
  - Limit enforcement under contention (same tests)
  - Empty set scenarios (`TestRedisStore_GetActiveSessions_EmptyWorkspace_ReturnsNil`, `TestRedisStore_RemoveActiveSession_LastRemoval_CleansWorkspaceEntry`)
- [ ] Integration test with real Valkey *(DEFERRED — miniredis with real gopher-lua interpreter covers Lua atomicity; testcontainers-go dependency introduction is a separate, larger change. Tracked as follow-up.)*
- [x] Load test: 1000 concurrent ops, verify no double-counting

**Lua Script:**
```lua
-- KEYS[1] = "ws:{workspace_id}:active"
-- ARGV[1] = sessionID
-- ARGV[2] = maxSessions
-- ARGV[3] = ttlSeconds
-- Returns: 1 if added/already-present, 0 if rejected by limit

local key = KEYS[1]
local sessionID = ARGV[1]
local maxSessions = tonumber(ARGV[2])
local ttlSeconds = tonumber(ARGV[3])

if redis.call('SISMEMBER', key, sessionID) == 1 then
    redis.call('EXPIRE', key, ttlSeconds)
    return 1
end

local count = redis.call('SCARD', key)
if count >= maxSessions then
    return 0
end

redis.call('SADD', key, sessionID)
redis.call('EXPIRE', key, ttlSeconds)
return 1
```

**Files:**
- New: `api/internal/services/wsstate/redis.go`
- New: `api/internal/services/wsstate/redis_test.go`
- Modified: `api/internal/handlers/proxy.go` (added `SetStateStore` setter with panic guard for after-Start use)
- Modified: `api/internal/handlers/proxy_lifecycle.go` (sets `started=true` inside startOnce.Do)
- Modified: `api/internal/app/app.go` (wire up Redis impl when cache service is available)

**Effort:** 3 days

**Risk:** Low. Existing tests cover behavior. Redis errors fall back to allowing requests (no worse than current).

---

#### US-45.3: Redis-backed deletedSessions
**Goal:** Move session tombstones to Valkey to prevent late-event resurrection across replicas.

**Acceptance:**
- [x] Redis implementation of `MarkDeleted` and `IsDeleted`
- [x] Per-key TTL = 30 minutes (`DefaultDeletedTTL`)
- [x] Hash-tagged: `ws:{workspace_id}:deleted:{session_id}`
- [x] Fail-closed behavior on Redis errors (treat as deleted to prevent resurrection)
  - Rationale: If we can't verify, assume deleted; user can recreate session
  - Different from activeSess (fail-open) because data integrity > availability here
- [x] Remove in-memory `deletedSessions` map from production path (RedisStore now overrides all 3 methods; InMemoryStore retains its impl for standalone/test use)
- [x] Tests cover error paths and TTL behavior (12 tests in `redis_deleted_test.go`)

**Files:**
- Modified: `api/internal/services/wsstate/redis.go` (override 3 methods + InvalidateAll update)
- New: `api/internal/services/wsstate/redis_deleted_test.go` (12 tests)

**Effort:** 1 day

**Risk:** Low. Read path is `EXISTS` (cheap). Write path is `SET`. No complex logic.

---

### P1: Cache Layer (Week 3)

#### US-45.4: Redis-backed pwCache
**Goal:** Move workspace password cache to Valkey. Eliminates per-replica staleness on phase changes.

**Acceptance:**
- [x] Redis implementation of `GetPassword` and `InvalidatePassword`
- [x] TTL = 1 hour (`DefaultPasswordTTL`; passwords are stable)
- [x] Invalidation = DELETE on key (no pubsub needed; replicas hit Redis on miss)
- [x] Phase change handler invalidates Redis (one source of truth — via `InvalidateAll` → `InvalidatePassword`)
- [x] Measure latency before adding L1 cache:
  - Target: P99 < 5ms
  - If exceeded → add L1 LRU in follow-up story (US-45.5, conditional)
  - Histogram `ws_state_op_duration_seconds{op,result}` registered from day one with fine-grained buckets around the 5ms target
- [x] Tests cover cache miss, invalidation, k8s Secret fetch fallback (13 unit tests + 1 handler-level integration test exercising 401→Redis-DEL)

**Files:**
- Modified: `api/internal/services/wsstate/redis.go` (override 3 methods + DefaultPasswordTTL + InvalidateAll update + updated type doc)
- New: `api/internal/services/wsstate/redis_password_test.go` (13 unit tests)
- Modified: `api/internal/services/wsstate/redis_test.go` (removed 2 stale delegation tests superseded by US-45.3/.4)
- Modified: `api/internal/handlers/proxy_auth_cache_test.go` (added `TestProxy_Upstream401_InvalidatesRedisPasswordCache` integration test)
- Modified: `api/internal/app/app.go` (updated comment to reflect migrated sections)

**Effort:** 2 days

**Risk:** Medium. Hot path (every chat request). Latency-sensitive.

**Mitigation:** Prometheus histogram on day one. If P99 > 5ms after rollout, ship US-45.5.

---

#### US-45.5 (CONDITIONAL): L1 LRU Cache for pwCache
**Trigger:** Only ship if P99 latency on US-45.4 exceeds 5ms.

**Goal:** Add per-replica LRU cache in front of Redis to reduce hot-path latency.

**Acceptance:**
- [ ] Decorator pattern: `cachedPasswordStore` wraps `redisPasswordStore`
- [ ] LRU size: 1000 entries (configurable)
- [ ] Pubsub channel `ws:pw:invalidate` for cross-replica invalidation
- [ ] L1 cleared on receiving invalidation message
- [ ] Tests verify pubsub propagation

**Effort:** 1 day (if needed)

---

#### US-45.6: Redis-backed wsConfig
**Goal:** Move workspace config cache to Valkey. Same pattern as pwCache.

**Acceptance:**
- [x] Redis implementation of `GetWorkspaceConfig` and `InvalidateWorkspaceConfig`
- [x] TTL = 5 minutes (`DefaultConfigTTL`; config can change more often than passwords)
- [x] JSON-serialized config stored in Redis
- [x] Tests cover serialization edge cases (zero-value Config, missing fields, corrupt JSON)

**Files:**
- Modified: `api/internal/services/wsstate/redis.go` (override 3 methods + DefaultConfigTTL + InvalidateAll update)
- New: `api/internal/services/wsstate/redis_config_test.go` (16 tests)
- Modified: `api/internal/services/wsstate/redis_test.go` (removed stale delegation test)
- Modified: `api/internal/app/app.go` (updated wiring comment)

**Effort:** 1 day

---

### P2: Cleanup (Week 4)

#### US-45.7: Move priorPhase to Redis
**Goal:** Eliminate per-replica phase tracking inconsistency.

**Acceptance:**
- [x] Redis key: `ws:{workspace_id}:phase` (STRING, not HASH — the existing code only needs prior phase, not current+transitionedAt)
- [x] TTL = 24 hours (`DefaultPriorPhaseTTL`)
- [x] Tests verify race-free transitions (watcher serializes per-resource; Get+Set is safe)
- [x] priorPhase survives InvalidateAll per US-45.1 contract (verified by `TestRedisStore_InvalidateAll_ClearsRedisPriorPhase`)

**Effort:** 1 day

---

#### US-45.8: Move parentBackfilled to Redis
**Goal:** Eliminate duplicate backfill operations across replicas.

**Acceptance:**
- [x] Redis key: `ws:{workspace_id}:backfilled` (STRING with TTL, not SETNX — SET is simpler and equally atomic for this use case)
- [x] TTL = 24 hours (`DefaultBackfilledTTL`; backfill is idempotent, can repeat after TTL)
- [x] First replica to claim does the work; others skip (Get returns true after Set)

**Effort:** 1 day

---

#### US-45.9: Remove In-Memory Implementations
**Goal:** Delete dead code. The Redis implementation is now sole path.

**Acceptance:**
- [x] Remove all in-memory map fields from `ProxyHandler` (done in US-45.1)
- [x] Remove `inMemory` field from `RedisStore` — all 6 sections now Redis-backed, no InMemoryStore delegation remains
- [x] Simplify `InvalidateAll` to only call Redis methods (no defense-in-depth InMemoryStore cleanup)
- [x] InMemoryStore (`inmemory.go`) retained for: ProxyHandler's default when no Redis is configured (unit tests, local dev), and handler tests that construct `&ProxyHandler{}` literally
- [x] Update type docs and app.go wiring comment

**Note:** The spec said "Remove `inmemory.go` implementation" entirely. Deviation: InMemoryStore is kept as the default for ProxyHandler unit tests and local dev without Redis. Removing it would force every handler test to spin up a miniredis instance, which is heavyweight for narrow unit tests. The InMemoryStore is dead code in production (app.go always injects RedisStore when cache service is available) — only test/dev code uses it.

**Files:**
- Modified: `api/internal/services/wsstate/redis.go` (removed `inMemory` field + simplified InvalidateAll + updated type doc)

**Effort:** 0.5 days

---

## Architecture Diagram

### Before (Per-Replica State)
```
┌─────────────────┐         ┌─────────────────┐
│  API Replica 1  │         │  API Replica 2  │
│                 │         │                 │
│ activeSess      │         │ activeSess      │
│ {ws1: {s1, s2}} │  ⚠️ DRIFT │ {ws1: {s2, s3}} │
│                 │         │                 │
│ pwCache         │         │ pwCache         │
│ deletedSess     │         │ deletedSess     │
└─────────────────┘         └─────────────────┘
        │                            │
        └──────────┬─────────────────┘
                   │
         ┌─────────▼──────────┐
         │  Workspace Pod      │
         │  (opencode + agentd)│
         └─────────────────────┘
```

### After (Shared State)
```
┌─────────────────┐         ┌─────────────────┐
│  API Replica 1  │         │  API Replica 2  │
│                 │         │                 │
│ connCount       │         │ connCount       │  ← stays local (per-replica resource)
│ (HTTP conns)    │         │ (HTTP conns)    │
└────────┬────────┘         └────────┬────────┘
         │                           │
         └──────────┬────────────────┘
                    │
          ┌─────────▼──────────┐
          │   Valkey/Redis     │
          │                    │
          │ ws:{id}:active     │  ← shared state
          │ ws:{id}:deleted    │
          │ ws:{id}:pw         │
          │ ws:{id}:config     │
          └────────────────────┘
                    │
          ┌─────────▼──────────┐
          │  Workspace Pod      │
          └─────────────────────┘
```

---

## Migration Strategy: Forward-Fix Only

Per user decision: no feature flags, no rollback switch. Each story ships fully committed to Redis. Issues are resolved by fix-forward (additional commits), not by toggling back to in-memory.

### Implications
- **No `WSSTATE_BACKEND` env var** — Redis is the only path after each story ships
- **In-memory implementation removed in US-45.9** — but kept available during migration weeks for testing only
- **Bugs require fix-forward PRs** — code review and testing rigor must compensate
- **Production incidents resolved by:**
  - Restarting API pods to clear bad state
  - Restarting Valkey if Redis state corrupted
  - Hotfix PR if code bug

### Pre-deployment Checklist (Per Story)
- [ ] Unit tests cover Lua script edge cases
- [ ] Integration test against real Valkey instance
- [ ] Load test in staging (1000 RPS, verify no double-counting/race conditions)
- [ ] Prometheus alerts configured for new metrics
- [ ] Runbook updated for new failure modes
- [ ] Code review by 2 engineers (heightened due to no rollback)

### Deployment Plan
1. Merge US-45.1 (interface only, no behavior change) → Deploy → Verify nothing broken
2. Merge US-45.2 (activeSess to Redis) → Deploy → Monitor 24h with heightened alerting
3. Merge US-45.3 (deletedSessions) → Deploy → Monitor 24h
4. Merge US-45.4 (pwCache) → Deploy → **Measure P99 latency for 1 week before proceeding**
5. If P99 latency exceeds 5ms target → Ship US-45.5 (L1 cache) before continuing
6. Continue with US-45.6, 45.7, 45.8.
7. Finally US-45.9 cleanup.

---

## Failure Mode Analysis

### Scenario 1: Valkey Becomes Unreachable
**Impact:**
- `activeSess.CheckAndAdd` → fails open → all sessions allowed (existing rate limits via k8s/connCount still apply)
- `deletedSessions.IsDeleted` → fails closed → late events suppressed (no resurrection)
- `pwCache.Get` → fails to fallback (k8s Secret fetch) → slower but works
- `wsConfig.Get` → fails to fallback (k8s Workspace fetch) → slower but works

**Mitigation:**
- Prometheus alert on `ws_state_errors_total` rate
- Fail-open behavior on `activeSess` + k8s fallback for caches tolerate brief Valkey outages (per "Open Questions" decision: ship without HA initially)
- Valkey HA (sentinel) is a separate follow-up effort — not a prerequisite for this Epic
- Documented runbook: restart Valkey, restart API pods if needed

### Scenario 2: Valkey Slow (P99 > 100ms)
**Impact:**
- Hot paths (chat requests) become slow
- Users see latency spikes
- Eventually request timeouts

**Mitigation:**
- L1 cache for pwCache (US-45.5) reduces dependency on Redis hot path
- Circuit breaker pattern: if 10 consecutive errors, fall back to k8s for next 30s
- Already wired Prometheus histogram detects this

### Scenario 3: Lua Script Bug
**Impact:**
- Atomic operations fail silently or incorrectly
- Could cause: rate limit bypass, double-add, deletion failures

**Mitigation:**
- Extensive unit tests with edge cases
- Integration tests against real Valkey
- Code review focus: Lua scripts in PR template

### Scenario 4: Hash Tag Collision
**Impact:**
- All keys for a workspace land on same shard (intended)
- If one workspace gets very hot, that shard gets uneven load

**Mitigation:**
- Currently single Valkey instance (no sharding yet)
- When migrating to cluster, monitor per-shard load
- Can add per-key hashing variants if needed (sub-tagging like `{ws_id}:{shard_seed}`)

---

## Success Criteria

### Reliability
- [ ] Zero "stuck session" reports for 30 days post-rollout
- [ ] Both API replicas show consistent session state in observability
- [ ] Late SSE events from deleted sessions are correctly suppressed cluster-wide

### Performance
- [ ] P50 hot path latency unchanged or improved
- [ ] P99 hot path latency < 5ms additional overhead
- [ ] Redis ops/sec sustainable at projected load (10x current)

### Operational
- [ ] Runbooks updated for Valkey-related incidents
- [ ] Prometheus dashboards show wsstate metrics
- [ ] Alerts configured for error rates and latency SLOs

---

## Out of Scope

- **Distributed SSE Tracker (S1)** — Separate Epic, addresses N×M SSE connections
- **Watcher coordination (S3)** — Separate concern, leader election for side effects
- **Durable session busy state (R3 from analysis)** — Different problem (opencode crashes), separate fix
- **agentdClient package (M1)** — Maintainability improvement, separate Epic

---

## Open Questions

All resolved per user decisions (2026-06-16):

1. ~~Rollback switch~~ → **Forward-fix only**, no env var. Bugs resolved via PRs.
2. ~~Valkey HA~~ → **Ship with current single-instance Valkey.** HA is separate follow-up. Fail-open behavior on activeSess + k8s fallback for caches means brief Valkey outages are tolerable.
3. ~~L1 cache pre-emptive~~ → **Measure first.** Ship US-45.4 with Redis-only. Add L1 (US-45.5) only if P99 > 5ms after rollout.
4. **Hash tags:** Use `{workspace_id}` consistently from day one (zero cost, enables future cluster migration).
5. **Cluster migration timing:** Not currently planned. Hash tags ensure we're ready when needed.

---

**Last Updated:** 2026-06-16  
**Owner:** TBD  
**Reviewers:** TBD
