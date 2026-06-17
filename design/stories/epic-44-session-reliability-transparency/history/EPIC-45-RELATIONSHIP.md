# How Epic 45 Ties Into Epic 44

**Date:** 2026-06-16  
**Status:** Analysis Complete

---

## TL;DR

**Epic 44 and Epic 45 solve DIFFERENT symptoms of the SAME root cause:**

- **Epic 44:** Prevents sessions getting stuck "busy" when **opencode dies** (OOM, crash, restart)
- **Epic 45:** Prevents sessions getting stuck "busy" when **API replicas have inconsistent state**

Both Epics are **independent** and **complementary** - neither blocks the other, both improve reliability.

---

## The Root Problem: Stuck Sessions

### Incident Categories

**Type 1: opencode Dies (Epic 44 Scope)**
- OOMKill (Incident A)
- SIGTERM during active session (Incident B)
- Process crash
- **Symptom:** Session stuck "busy" in SQLite until next opencode restart
- **Solution (Epic 44):** 
  - Auto-abort stale sessions on restart (already implemented in `stale_sessions.go`)
  - Terminal SSE events notify user
  - Session-aware restart prevents data loss

**Type 2: Multi-Replica Inconsistency (Epic 45 Scope)**
- API replica has stale `activeSess` map
- User request lands on wrong replica
- **Symptom:** Session shows "busy" on one replica, "idle" on another
- **Solution (Epic 45):**
  - Move session state to Redis (shared across replicas)
  - Atomic operations prevent race conditions

---

## How They Relate

### Shared Context

Both Epics reference the **same incident investigation**:
- Workspace: `a847faa5-19b4-463d-a434-1ce473a16f93`
- Session: `ses_13076538bffeYtLrhoZ2ccRM1E`
- Date: 2026-06-16

Epic 45 README line 24-30:
```
**Stuck Session Investigation (2026-06-16):**
- Session `ses_13076538bffeYtLrhoZ2ccRM1E` returned 409 Conflict from one replica
- Same session showed `status: "idle"` from agentd's `/v1/statusz`
- Same session showed `status: "idle"` in workspace CRD
- Root cause: `proxy_connections.go:70-78` reads from per-replica `activeSess` map
- The map had stale entry from when opencode was OOMKilled mid-stream
```

**This reveals Epic 45's perspective:** The stuck session had TWO problems:
1. ✅ **Epic 44 problem:** opencode OOMKilled, session stuck in SQLite
2. ✅ **Epic 45 problem:** Stale entry in API replica's `activeSess` map

---

## Dependency Analysis

### Does Epic 45 Depend on Epic 44?

**Answer: No.** Epic 45 says:

```
**Depends on:** Existing Valkey/Redis infrastructure
**Related:** Epic 44 (Session Reliability) — addresses different symptoms of same underlying issue
```

They are **related** but **independent**.

### Does Epic 44 Depend on Epic 45?

**Answer: No.** Epic 44 doesn't mention Epic 45 at all.

Epic 44 scope:
- opencode lifecycle (OOM, restart, crash)
- workspace-agentd supervision
- SSE streaming to client

Epic 45 scope:
- API server multi-replica state
- Redis-backed session tracking
- Cache consistency

**Different layers of the stack.**

---

## Which Should Ship First?

### Option A: Ship Epic 44 First (Recommended)

**Pros:**
- ✅ Addresses more critical user pain (data loss from unsafe restarts)
- ✅ Smaller scope (11 stories vs 9 stories)
- ✅ Less risky (no Redis migration, no multi-replica coordination)
- ✅ Immediate user value (terminal events, OOM detection, transparent restarts)

**Cons:**
- ⚠️ Multi-replica stuck sessions still possible (but rate is low since OOM is main cause)

### Option B: Ship Epic 45 First

**Pros:**
- ✅ Fixes multi-replica inconsistency permanently
- ✅ Enables scaling to 3+ API replicas safely

**Cons:**
- ⚠️ Higher risk (Redis migration, Lua scripts, atomic operations)
- ⚠️ No immediate user-facing value (stuck sessions are rare)
- ⚠️ Doesn't fix the main cause (opencode deaths)

### Option C: Ship Both in Parallel

**Pros:**
- ✅ Fastest time to full reliability

**Cons:**
- ⚠️ Resource intensive (2 engineering teams)
- ⚠️ If both have issues, harder to debug which Epic caused it

---

## Recommended Strategy

### Phase 1: Epic 44 First (4-5 weeks)
- **Week 1-2:** Epic 44 Phase 1 (P0 stories) - 13 days
- **Week 3:** Epic 44 Phase 2 (P1 stories) - 4 days
- **Week 4:** Stabilization, monitor metrics
- **Week 5:** Epic 44 Phase 3 (P2 stories) - 6 days

### Phase 2: Epic 45 After Epic 44 Stabilizes (4 weeks)
- **Week 6-7:** Epic 45 P0 stories (US-45.1, 45.2, 45.3) - 6 days
- **Week 8:** Epic 45 P1 stories (US-45.4, 45.6) - 3 days
- **Week 9:** Epic 45 P2 cleanup (US-45.7, 45.8, 45.9) - 3 days

**Total Timeline:** ~9 weeks for both Epics

**Rationale:**
1. Epic 44 fixes the **most common** stuck session cause (opencode deaths)
2. Epic 44 is **lower risk** (no Redis dependency)
3. Epic 45 benefits from Epic 44's `stale_sessions.go` cleanup
4. Shipping sequentially reduces debugging complexity

---

## How They Work Together (After Both Ship)

### Scenario: opencode OOMKill with Multi-Replica API

**Before Epic 44 + 45:**
1. opencode OOMKilled mid-session
2. Session stuck "busy" in SQLite
3. API Replica 1 has stale `activeSess` entry
4. API Replica 2 doesn't (hadn't seen the session yet)
5. User sees 409 error on 50% of requests (load balancer round-robin)
6. **Manual recovery required:** kubectl restart workspace

**After Epic 44 Only:**
1. opencode OOMKilled mid-session
2. agentd writes OOM marker, restarts opencode
3. opencode reads marker, auto-aborts stale session (✅ Epic 44)
4. Terminal shows: "⚠️ Agent was terminated due to memory exhaustion"
5. API Replica 1 still has stale `activeSess` entry (⚠️ still a problem)
6. User sees 409 error on 50% of requests until API replica restarts

**After Epic 44 + 45:**
1. opencode OOMKilled mid-session
2. agentd writes OOM marker, restarts opencode
3. opencode reads marker, auto-aborts stale session (✅ Epic 44)
4. Terminal shows: "⚠️ Agent was terminated due to memory exhaustion"
5. All API replicas read from Redis (✅ Epic 45)
6. Session state consistent across replicas
7. **User never sees 409 error** - fully recovered

---

## Epic Interaction Matrix

| Scenario | Epic 44 Only | Epic 45 Only | Both Epics |
|----------|--------------|--------------|------------|
| opencode OOMKill | ✅ Auto-aborts session | ⚠️ Replica inconsistency remains | ✅ Full recovery |
| opencode crash | ✅ Terminal event + auto-abort | ⚠️ Replica inconsistency remains | ✅ Full recovery |
| Unsafe restart | ✅ Session-aware restart | N/A | ✅ Full transparency |
| Multi-replica drift | N/A | ✅ Redis-backed state | ✅ Consistent state |
| API replica restart | ⚠️ Loses in-memory state | ✅ Reloads from Redis | ✅ No state loss |
| Late SSE events | ✅ Terminal error event | ✅ Tombstones in Redis | ✅ Fully suppressed |

---

## Testing Strategy After Both Epics

### Integration Tests Spanning Both Epics

**Test: OOMKill with Multi-Replica API**
1. Setup: 2 API replicas, 1 workspace with active session
2. Trigger: OOMKill opencode via `kill -9`
3. Verify:
   - ✅ (Epic 44) agentd writes OOM marker
   - ✅ (Epic 44) opencode auto-aborts session on restart
   - ✅ (Epic 45) Both API replicas see consistent state
   - ✅ (Epic 44) Terminal shows OOM error event
   - ✅ User can immediately send new prompt (no 409)

**Test: Unsafe Restart with Multi-Replica API**
1. Setup: 2 API replicas, 1 workspace with busy session
2. Action: Change secret binding (trigger restart)
3. Verify:
   - ✅ (Epic 44) Restart deferred until session idle
   - ✅ (Epic 45) Both replicas see "pending restart" state
   - ✅ (Epic 44) User sees banner on both replicas
   - ✅ (Epic 45) Click "Restart Now" works from either replica

---

## Summary

| Aspect | Epic 44 | Epic 45 |
|--------|---------|---------|
| **Problem** | opencode dies (OOM, crash, restart) | Multi-replica state inconsistency |
| **Scope** | workspace-agentd, opencode, proxy | API server, Redis |
| **Priority** | P0 (data loss risk) | P1 (reliability improvement) |
| **Risk** | Low | Medium (Redis migration) |
| **User Value** | High (immediate) | Medium (incremental) |
| **Dependencies** | None | Valkey/Redis |
| **Stories** | 11 | 9 |
| **Estimate** | 23 days (~5 weeks) | 15 days (~3 weeks) |
| **Ship Order** | **1st** | **2nd** |

**Recommendation:** Ship Epic 44 first, Epic 45 after Epic 44 stabilizes (4 weeks). Total timeline: ~9 weeks for complete session reliability.

---

**Files:**
- Epic 44: `design/stories/epic-44-session-reliability-transparency/README.md`
- Epic 45: `design/stories/epic-45-multi-replica-state-consistency/README.md`
- This analysis: `design/stories/epic-44-session-reliability-transparency/EPIC-45-RELATIONSHIP.md`
