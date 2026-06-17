# Epic 45 Quick Summary

## What
Move workspace state from per-replica in-memory maps to shared Valkey/Redis.

## Why
Today's stuck session (`ses_13076538bffeYtLrhoZ2ccRM1E`) was caused by stale `activeSess` map on one API replica. Multi-replica deployments fundamentally cannot work correctly with per-replica state.

## Scope
9 stories across 4 weeks:

**Week 1-2 (Critical):**
- US-45.1: stateStore abstraction (interface + in-memory impl) — 2 days
- US-45.2: activeSess to Redis ⭐ **fixes today's bug** — 3 days  
- US-45.3: deletedSessions to Redis — 1 day

**Week 3 (Caching):**
- US-45.4: pwCache to Redis — 2 days
- US-45.5: L1 LRU (conditional on latency) — 1 day
- US-45.6: wsConfig to Redis — 1 day

**Week 4 (Cleanup):**
- US-45.7: priorPhase to Redis — 1 day
- US-45.8: parentBackfilled to Redis — 1 day
- US-45.9: Remove in-memory code — 1 day

**Total:** ~12 days

## Key Design Decisions

✅ **Hash-tagged keys** (`ws:{workspace_id}:active`) — enables Valkey cluster migration later  
✅ **Fail-open for activeSess** — Redis errors allow requests (don't block legit traffic)  
✅ **Fail-closed for deletedSessions** — Redis errors suppress events (data integrity)  
✅ **TTL on everything** — Auto-recovery from bugs we haven't found yet  
✅ **Lua scripts for atomicity** — Prevent race conditions  
✅ **Decorator pattern** — Easy to add L1 cache later if measurements show need  
✅ **Interface-based** — Existing tests work with in-memory impl during migration  

## Resolved Decisions (2026-06-16)

1. **Rollback strategy:** Forward-fix only. No env var switches. Bugs resolved via PRs.
2. **Valkey HA:** Ship with current single-instance Valkey. HA deferred to separate work.
3. **L1 cache:** Measure first. Add only if P99 latency > 5ms after US-45.4 ships.

See `README.md` for full design.
