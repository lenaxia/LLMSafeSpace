# Epic 44: Session Reliability & Transparency

**Status:** Design Validated + User Feedback Applied - Ready for Implementation  
**Created:** 2026-06-16  
**Validated:** 2026-06-16 (see VALIDATION-COMPLETE.md for all assumptions validated)  
**Depends on:** Epic 3 (Proxy to opencode), Epic 6 (Collapse Sandbox), Epic 27a (Credential Reload Foundation)

**Key User Requirements:**
- **Ops monitoring is P0** (first-class citizen, not optional)
- **NO timeout for deferred restart** (defer indefinitely with UI notification, no forced restart)
- **85% memory warning threshold** (not 75%)
- **Deprecate api-key aggressively** (no current users, provide migration path)
- **Request buffering NOT secret buffering** (secrets written to DB immediately, rematerialization is idempotent)
- **Multi-hour agentic workflows** (deferred restart is VERY common, not edge case)

---

## Problem Statement

Two production incidents revealed critical gaps in session lifecycle observability and restart safety:

1. **Incident A (OOMKill)**: 16 concurrent sessions exhausted 2 GiB memory limit, pod terminated mid-task. Session stuck `status: "busy"` until next restart. No user notification.
   
2. **Incident B (Unsafe Restart)**: User changed workspace secret bindings while session was actively running (`status: "busy"`, 102K context tokens). workspace-agentd immediately SIGTERMed opencode, destroying in-flight work. No session state check, no graceful draining.

**Common failure pattern:** No terminal event notification when agent dies unexpectedly, and no session-aware restart mechanism for configuration changes requiring process restart.

**Evidence:**
- `cmd/workspace-agentd/secrets.go:415-419` - Restart triggered immediately on env-secret changes without checking session state
- `api/internal/handlers/proxy.go:440-447` - SSE proxy silently closes on `io.EOF` without emitting error event
- `cmd/workspace-agentd/secrets.go:437-444` - `shouldRestart()` only checks `env-secret`, missing `api-key` type (latent bug)

---

## Solution

Prevent silent session failures and improve platform transparency through:

1. **Terminal SSE events** when agent dies (OOM, SIGTERM, crash)
2. **Session-aware restart mechanism** that defers restarts until all sessions are idle
3. **Request buffering during restart** - API proxy holds `POST /messages` requests (30s timeout, 10 request limit) so users never see 502 errors
4. **OOM detection & notification** with clear user-facing messages and Prometheus metrics
5. **Memory pressure warnings** at 85% of pod limit (changed from 75% per user requirement)
6. **Ops monitoring (Prometheus)** as first-class citizen in Phase 1 - not optional
7. **Fix api-key restart bug** then deprecate aggressively (no current users, provide migration path)
8. **UI notification for pending restart** - banner with "Restart Now" button, no forced timeout, defer indefinitely

**Key Architecture Decision:** Process restarts are unavoidable for `env-secret` and `api-key` types because Node.js `process.env` is immutable after startup (tested and confirmed). Focus is on making restarts transparent through deferred restart + request buffering.

---

## Goals

### User-Facing
- **Zero unexplained "session stuck busy" reports** - Users know why their session stopped
- **Zero data loss** from configuration changes during active sessions
- **Clear error messages** when agent terminates unexpectedly (OOM, crash, restart)

### Operational
- **Observable restart reasons** - Logs and metrics show why opencode restarted
- **Early warning system** - Memory pressure warnings before OOM kill
- **Graceful degradation** - Restarts happen transparently when safe

---

## Architecture

### Current Behavior (Broken)

```
User changes workspace secret binding (env-secret or api-key)
  ↓
agentd detects change via /workspaces/:id/bindings webhook
  ↓
agentd immediately calls proc.restart()
  ↓
opencode receives SIGTERM mid-task
  ↓
In-flight assistant turn destroyed
  ↓
SSE stream silently closes (io.EOF)
  ↓
Session stuck "busy" in SQLite until next restart
```

### New Behavior (Session-Aware + Request Buffering)

```
User changes workspace secret binding (env-secret or api-key)
  ↓
API writes secret changes to DATABASE immediately (PostgreSQL)
  ↓
agentd marks workspace as "needs_rematerialization" flag
  ↓
agentd checks opencode /sessions API for busy sessions
  ↓
If any session is status:"busy":
  ├─ agentd sets "pending_restart" flag
  ├─ agentd polls /sessions every 5s (RESTART_IDLE_CHECK_INTERVAL)
  ├─ Frontend UI shows: "⚠️ Configuration update pending - will apply when sessions idle
  │                      [Restart Now] (will halt active sessions)"
  ├─ User can click "Restart Now" → calls POST /agent/reload?drain=false (immediate)
  ├─ User can call API: POST /agent/reload?drain=true (graceful wait)
  └─ When all sessions idle:
      ├─ agentd re-fetches ALL secrets from DATABASE (idempotent)
      ├─ agentd rematerializes secrets to filesystem
      └─ agentd restarts opencode transparently
Else (all idle):
  ├─ agentd immediately rematerializes from DB
  └─ agentd restarts opencode
  
During opencode restart (2-5 seconds):
  ├─ API proxy detects opencode unhealthy (502 from health check)
  ├─ Proxy buffers incoming POST /messages requests
  ├─ Holds HTTP connections open (30s timeout, 10 request limit)
  ├─ When opencode healthy, forwards buffered requests FIFO
  └─ User never sees restart (transparent experience)
  
If subsequent secret changes occur during defer:
  ├─ New secrets written to DB immediately
  ├─ When restart finally occurs, agentd fetches LATEST state from DB
  └─ No need to buffer/merge - DB is source of truth
```

**Critical Design Notes:**
1. **No in-memory secret buffer** - Secrets are written to PostgreSQL immediately when user saves them
2. **Rematerialization is idempotent** - Always fetches ALL secrets from DB, can retry safely
3. **Multiple updates handled automatically** - DB always has latest state
4. **Request buffering NOT secret buffering** - User clarified this distinction (see QUESTIONS-ANSWERED.md)

### Terminal Event Flow (New)

```
opencode process exits unexpectedly (OOM, crash, SIGKILL)
  ↓
agentd detects exit code (137 = OOM, other = crash)
  ↓
agentd writes marker file:
  /home/workspace/.opencode-restart-reason
  {"reason": "oom", "timestamp": "...", "exitCode": 137}
  ↓
API proxy detects non-graceful stream closure
  ↓
proxy emits SSE: event: error
              data: {"type": "agent_died", "reason": "oom"}
  ↓
Frontend shows: "⚠️ Agent was terminated due to memory exhaustion"
  ↓
On restart, opencode reads marker and logs event
```

---

## Component Responsibilities

### agentd (`cmd/workspace-agentd/`)
- Supervises opencode process via `managedProcess`
- Tracks live session busy/idle state via `sessionStatusTracker` (SSE subscription to opencode `/event`)
- Decides when restarts are safe (checks `tracker.hasAnyBusy()` before `proc.restart()`)
- Detects opencode exit codes (137 = OOM, others = crash)
- Writes restart reason marker files to `/home/workspace/.opencode-restart-reason`

### Proxy (`api/internal/handlers/proxy.go`)
- Streams SSE events from opencode to client
- Detects abnormal stream closures (io.EOF after receiving data)
- Emits synthetic error events to client on abnormal closure
- **Has NO access to session state** (lives in agentd)
- **Cannot reliably determine if session was busy** at time of death (timing issue)

### opencode (opencode process — third-party, NOT modifiable in this repo)
- Executes session logic
- We cannot modify opencode itself; the actual memory monitoring happens in **agentd** (`cmd/workspace-agentd/main.go:577-594`)
- agentd reads cgroup v2 paths: `/sys/fs/cgroup/memory.current` (current usage) and `/sys/fs/cgroup/memory.max` (limit)
- agentd emits memory pressure warnings via SSE when usage >threshold
- agentd reads restart reason markers on startup
- agentd emits OOM/crash events if markers present

**Key Architecture Note:** agentd and opencode run in same pod; proxy runs in separate deployment. No direct communication between agentd and proxy (stateless HTTP only). Session state tracking happens in agentd via SSE subscription, NOT accessible to proxy.

---

## Stories

### P0: Critical Fixes (Prevent Data Loss)

#### US-44.1: Terminal SSE Events for Agent Death
**Problem:** SSE proxy silently closes on `io.EOF` when opencode dies  
**Solution:** Emit synthetic `event: error` on abnormal EOF (heuristic: any data received before EOF)  
**Files:** `api/internal/handlers/proxy.go:440-447`  
**Acceptance:**
- [ ] SSE emits error event on abnormal EOF (heuristic: `bytesReceived > 0` before EOF)
- [ ] Frontend displays error message in terminal
- [ ] Error event includes `type: "agent_died"` with `reason: "unknown"` (cannot reliably determine OOM vs crash vs session busy state from proxy)
- [ ] Tests cover EOF detection and error emission
- [ ] False positives (graceful close) are acceptable; false negatives (silent failures) are not

**Design Limitation:** Proxy cannot determine if session was busy at time of death because session state lives in agentd and SSE stream is already closed. Solution: Emit error on any abnormal EOF.

---

#### US-44.2: Session-Aware Restart Mechanism
**Problem:** Config changes immediately SIGTERM opencode regardless of active work  
**Solution:** Defer restarts until all sessions idle using existing `sessionStatusTracker`, no forced timeout  
**Files:** `cmd/workspace-agentd/secrets.go`, `cmd/workspace-agentd/main.go`  
**Acceptance:**
- [ ] When secret bindings change: secrets written to DB immediately, workspace marked "needs_rematerialization"
- [ ] `checkSessionsIdleAndRestart()` function checks `sessionStatusTracker.hasAnyBusy()`
- [ ] Add `sessionStatusTracker.hasAnyBusy()` method (returns `len(busySessions) > 0`)
- [ ] Add `sessionStatusTracker.listBusy()` method (returns `[]string` of session IDs)
- [ ] Pass `sessionStatusTracker` reference to `reloadSecretsHandler()`
- [ ] If any sessions busy: spawn background goroutine to poll tracker every 5s (RESTART_IDLE_CHECK_INTERVAL)
- [ ] Background goroutine applies restart when `!tracker.hasAnyBusy()`
- [ ] When restart occurs: re-fetch ALL secrets from DB (idempotent), rematerialize, restart opencode
- [ ] **NO forced timeout** - defer indefinitely until sessions idle OR user triggers restart manually
- [ ] User can trigger restart via: UI "Restart Now" button OR API `POST /agent/reload?drain=false`
- [ ] If `sessionStatusTracker` has no data (SSE disconnected), treat as "all idle" and restart immediately with logged warning
- [ ] Subsequent secret changes during defer: written to DB immediately, latest fetched when restart occurs

**Design Notes:**
- Uses existing SSE-based session tracking (cannot use `/session` REST API - returns hardcoded "idle")
- Multi-hour agentic workflows are common - deferred restart is NOT an edge case
- DB is source of truth - no secret buffering in agentd
- User requirement: No forced timeout (see QUESTIONS-ANSWERED.md Q4)

---

#### US-44.3: Fix api-key Restart Bug
**Problem:** `api-key` writes to env file but `shouldRestart()` doesn't check it  
**Solution:** Update `shouldRestart()` to include `api-key` type  
**Files:** `cmd/workspace-agentd/secrets.go:437-444`  
**Acceptance:**
- [ ] `shouldRestart()` returns true for both `env-secret` AND `api-key`
- [ ] Test case covers api-key restart behavior
- [ ] MUST ship with US-44.2 (session-aware restart) to avoid breaking existing users

**Note:** This is a one-line fix but cannot ship independently - would break users who rely on api-key NOT restarting.

---

#### US-44.4: OOM Detection & User Notification
**Problem:** Pod OOMKill leaves session "busy" with no indication  
**Solution:** Detect exit 137, write marker file, surface in UI, emit metrics  
**Files:** `cmd/workspace-agentd/main.go` (managedProcess supervisor), `api/internal/handlers/history.go`  
*(opencode is third-party and cannot be modified — all detection is in agentd)*
**Acceptance:**
- [ ] agentd `managedProcess.supervise()` detects opencode exit code 137 (SIGKILL from OOM) via `cmd.Wait()`
- [ ] agentd writes `/home/workspace/.opencode-oom-marker` with timestamp + memory limit
- [ ] agentd increments `workspace_oom_kills_total{workspace_id}` Prometheus counter
- [ ] agentd exposes counter on `:9090/metrics` endpoint (requires basic Prometheus setup)
- [ ] opencode checks marker on startup, emits OOM event via SSE
- [ ] API proxy includes OOM notice in session history
- [ ] Terminal shows: "⚠️ Agent was terminated due to memory exhaustion. Previous work may be lost."
- [ ] Session history includes: `{"event": "oom_detected", "timestamp": "...", "memoryLimit": "2Gi"}`

**Note:** `managedProcess` supervisor already has access to exit code via `cmd.Wait()` (main.go:1132-1220). Add OOM detection logic to existing supervisor loop.

---

### P1: Improved Observability (Non-Blocking)

#### US-44.5: Memory Pressure Warnings
**Problem:** No early warning before OOM kill  
**Solution:** agentd monitors pod memory usage (cgroup v2), emits warning at 85% of limit  
**Files:** `cmd/workspace-agentd/main.go` (extend existing memory reading at lines 577-594)  
*(opencode is third-party and cannot be modified — agentd handles all monitoring; the file paths under `packages/opencode/src/` referenced in earlier drafts are NOT in this repo)*  
**Acceptance:**
- [ ] agentd checks `/sys/fs/cgroup/memory.current` against `/sys/fs/cgroup/memory.max` every 60s
- [ ] When >85% of cgroup limit: emit warning via existing SSE channel (changed from 75% per user requirement)
- [ ] User sees: "⚠️ Memory usage high (1.7 GiB / 2 GiB). Consider reducing concurrent sessions or increasing workspace memory limit."
- [ ] Use cgroup v2 paths exclusively (this codebase uses cgroup v2; never references v1 `memory.limit_in_bytes`)
- [ ] Config: `MEMORY_WARNING_THRESHOLD=0.85`, `MEMORY_CHECK_INTERVAL_MS=60000`

**Note:** Implemented in agentd since opencode cannot be modified. agentd has full visibility into pod-level cgroup metrics and can emit warnings via the existing SSE channel.

---

#### US-44.6: Per-Session Memory Attribution
**Problem:** No visibility into which sessions are memory-heavy  
**Solution:** Estimate memory per session based on context tokens, computed in agentd from data observable via opencode's `/session` API  
**Files:** `cmd/workspace-agentd/main.go` (extend statusz with per-session memory estimate), `api/internal/handlers/history.go`  
*(opencode is third-party and cannot be modified — `packages/opencode/src/session-manager.ts` referenced in earlier drafts is NOT in this repo. agentd computes the estimate from contextTokens already available via the `/session` endpoint)*  
**Acceptance:**
- [ ] `estimatedMemoryMB` computed per session in agentd: `(contextTokens × 2 bytes) + (historyTurns × 10KB overhead)`
- [ ] Included in agentd `/v1/statusz` response (extends existing SessionInfo)
- [ ] API `GET /sessions/:id` exposes the estimate
- [ ] Terminal UI shows memory usage in session list
- [ ] Helps users identify memory-heavy sessions

---

#### US-44.7: Restart Reason Logging
**Problem:** No record of WHY opencode restarted  
**Solution:** agentd writes restart reason to marker file, then logs the reason itself when reading the marker on next start (opencode is not modifiable; the reason is observable via agentd logs and the marker file's persistence on the PVC)  
**Files:** `cmd/workspace-agentd/process.go`, `cmd/workspace-agentd/main.go`  
*(opencode is third-party and cannot be modified — all reason recording is in agentd)*  
**Acceptance:**
- [ ] agentd writes `/home/workspace/.opencode-restart-reason` before restart
- [ ] Format: `{"reason": "env_secrets_changed", "timestamp": "2026-06-16T16:04:14Z", "secretNames": ["GH_TOKEN"]}`
- [ ] opencode reads on startup and logs to console
- [ ] Reasons: `env_secrets_changed`, `api_key_changed`, `crash`, `oom`, `user_requested`
- [ ] API includes restart events in workspace event stream (optional)

---

#### US-44.8: Operational Monitoring (Prometheus) - MOVED TO P0
**Problem:** No operational visibility into workspace performance (restarts, OOM, memory, sessions)  
**Solution:** Expose comprehensive Prometheus metrics for SRE dashboards  
**Files:** `cmd/workspace-agentd/main.go` (extend existing `:9090/metrics` endpoint)  
**Acceptance:**
- [ ] `workspace_restarts_total{workspace_id, reason}` - Counter (reasons: env_secrets, api_key, crash, user_requested)
- [ ] `workspace_memory_bytes{workspace_id}` - Gauge (current memory usage from cgroup)
- [ ] `workspace_active_sessions{workspace_id}` - Gauge (from sessionStatusTracker)
- [ ] `workspace_context_tokens{workspace_id}` - Gauge (sum across all sessions)
- [ ] Metrics exposed on agentd `:9090/metrics` endpoint (already exists for gate timings)
- [ ] NOT user-facing (SRE/admin dashboards only)

**Note:** `workspace_oom_kills_total` is in US-44.4. This story moved from P2 to P0 per user requirement: "Ops monitoring is P0 (first-class citizen)".

---

#### US-44.9: Aggressive api-key Deprecation
**Problem:** `api-key` secret type is legacy, superseded by `llm-provider` (hot-reloadable) and `env-secret`  
**Solution:** Deprecate aggressively with 6-month sunset timeline, provide migration guide  
**Files:** `frontend/src/`, `api/internal/handlers/secrets.go`, `docs/` (multiple files)  
**Acceptance:**
- [ ] UI banner on api-key secrets: "⚠️ api-key secrets are deprecated. Migrate to llm-provider (for LLM APIs) or env-secret (for other APIs). api-key will be removed on [SUNSET_DATE]."
- [ ] Migration guide published: `/docs/migration/api-key-to-llm-provider.md`
  - For LLM APIs: Use `llm-provider` secret type (hot-reloadable, no restart)
  - For other APIs: Use `env-secret` type (requires restart, but session-aware in Epic 44)
- [ ] Set sunset date: 6 months from Epic 44 ship date
- [ ] After sunset: Prevent creation of new api-key secrets (return 400 error with migration guide link)
- [ ] Existing api-key secrets continue to work (no forced migration, just deprecated)
- [ ] Update secret type dropdown: Remove api-key from list, show only if user has existing api-key secrets

**Context:** User confirmed "no api-key secrets currently in use" - can deprecate aggressively. See QUESTIONS-ANSWERED.md.

**Estimate:** 3 days (UI changes, docs, API validation, testing)

---

#### US-44.10: API Proxy Request Buffering
**Problem:** Users see "502 Bad Gateway" errors when sending prompts during opencode restart  
**Solution:** Buffer `POST /messages` requests in API proxy during restart, forward when healthy  
**Files:** `api/internal/handlers/proxy.go`, `api/internal/metrics/metrics.go`  
**Acceptance:**
- [ ] Proxy detects opencode unhealthy (502/503 from health check)
- [ ] Buffers only `POST /api/v1/workspaces/:id/sessions/:sessionId/messages` requests
- [ ] Other requests (GET, DELETE, POST /abort) return 502 immediately (safe to fail)
- [ ] Buffer limit: 10 requests per workspace (configurable via `REQUEST_BUFFER_SIZE_PER_WORKSPACE`)
- [ ] Buffer timeout: 30 seconds (configurable via `REQUEST_BUFFER_TIMEOUT_SECONDS`)
- [ ] Holds HTTP connection open (no response sent to client until forwarded or timeout)
- [ ] When opencode healthy: forwards buffered requests FIFO (first-in-first-out)
- [ ] On timeout: return 503 with message "Workspace is restarting, please try again in a moment"
- [ ] On buffer full: return 429 with message "Too many requests during restart, please try again"
- [ ] Prometheus metrics:
  - `workspace_request_buffer_timeout_total{workspace_id}` - Counter
  - `workspace_request_buffer_wait_seconds{workspace_id}` - Histogram (buckets: 0.5s, 1s, 2s, 5s, 10s, 30s)
  - `workspace_request_buffer_size{workspace_id}` - Gauge (current buffered count)
  - `workspace_request_buffer_full_total{workspace_id}` - Counter (rejections due to full buffer)
- [ ] Test: Simulate restart, send prompt during restart, verify no 502 error
- [ ] Test: Send 11 prompts during restart, verify 11th rejected (buffer full)
- [ ] Test: Restart takes >30s, verify timeout error message

**Design Note:** This provides transparent restart experience - user never sees errors during normal restarts (2-5 seconds). Per user requirement (QUESTIONS-ANSWERED.md Q1-Q3).

**Estimate:** 3 days

---

### P2: Operational Tools

#### US-44.11: Admin Session Recovery Tools
**Problem:** Sessions can get stuck in "busy" state if workspaces are deleted before cleanup (incident recovery)  
**Solution:** Add admin API endpoint to force-abort sessions without requiring running workspace  
**Files:** `api/internal/handlers/admin.go` (new), `api/internal/server/router.go`  
**Acceptance:**
- [ ] New endpoint: `POST /api/v1/admin/sessions/:sessionId/force-abort`
- [ ] Requires admin role (not accessible to regular users)
- [ ] Marks session as aborted in database (if session tracking exists in DB)
- [ ] OR deletes session record entirely (if sessions are purely in-memory/SQLite)
- [ ] Returns 200 with `{"aborted": true, "sessionId": "..."}` on success
- [ ] Returns 404 if session not found
- [ ] Logs admin action for audit trail
- [ ] Documentation: When to use (workspace deleted, session stuck)

**Use Case:** Recovery from incidents like Incident A & B where workspaces were deleted but sessions persist in stuck state (see STUCK-SESSIONS-RECOVERY.md).

**Estimate:** 3 days

---

## Explicitly Rejected Items

Per requirements analysis and user feedback:

❌ **maxTotalContextTokens** - User rejected this feature. Do NOT implement.

❌ **maxSessionsPerWorkspace** - Already exists as `maxActiveSessions` (default 5, range 1-20, fully implemented in Epic 3). Do NOT reimplement.

❌ **New Helm Chart Values** - `maxActiveSessions` is intentionally runtime-configurable via settings API. Do NOT expose in Helm.

❌ **Run History Persistence** - NOT in scope for this Epic. Would require:
  - Database schema for event log
  - Retention policy (auto-prune after N days)
  - API endpoints for querying history
  - Considerable storage overhead
  - Defer to separate Epic if requested

❌ **UI Warning Before Restart-Causing Operations** - Rejected. Places burden on user to schedule config changes during idle periods. Session-aware restart (US-44.2) is strictly better - fully transparent to user.

❌ **Increase Default Memory Limits** - Rejected. Wastes resources on typical workloads (most users have 1-3 sessions). Better solution: Memory pressure warnings (US-44.5) + user education about `maxActiveSessions` and workspace memory overrides.

---

## Implementation Order

### Phase 1: Critical Path (P0) - Week 1-2

1. **US-44.1a** - Terminal SSE events - proxy (1.5 days)
   - Emit error on abnormal EOF in proxy
   
2. **US-44.1b** - Terminal SSE events - frontend (0.5 days)
   - Display error messages in terminal UI
   
3. **US-44.3** - api-key restart bug fix (0.5 days)
   - One-line change to `shouldRestart()`
   
4. **US-44.2** - Session-aware restart (4 days)
   - Add `sessionStatusTracker.hasAnyBusy()` and `listBusy()` methods
   - Wire tracker to `reloadSecretsHandler()`
   - Implement deferred restart goroutine (NO forced timeout)
   - DB-backed rematerialization (idempotent)
   - Add SSE disconnect fallback handling
   
5. **US-44.4** - OOM detection + metrics (1.5 days)
   - Detect exit 137 in `managedProcess.supervise()`
   - Write marker files
   - Add `workspace_oom_kills_total` Prometheus counter
   - Wire opencode startup marker reading

6. **US-44.8** - Ops monitoring (Prometheus) - MOVED TO P0 (2 days)
   - `workspace_restarts_total{workspace_id, reason}`
   - `workspace_memory_bytes`, `workspace_active_sessions`, `workspace_context_tokens`
   - Extend existing agentd `:9090/metrics` endpoint

7. **US-44.10** - API proxy request buffering (3 days)
   - Buffer only `POST /messages` during restart
   - 30s timeout, 10 request limit
   - Prometheus metrics for monitoring
   - Test with simulated restarts

**Total Phase 1:** 13 days (~2.5 weeks)

### Phase 2: Observability (P1) - Week 3

8. **US-44.5** - Memory pressure warnings (2 days)
   - Implemented in agentd (opencode cannot be modified); reads pod cgroup v2 metrics
   - 85% threshold (changed from 75%)
   
9. **US-44.6** - Per-session memory attribution (1 day)
   - Implemented in agentd; estimate computed from contextTokens already exposed via opencode `/session` API
   
10. **US-44.7** - Restart reason logging (1 day)
    - Write `/home/workspace/.opencode-restart-reason` marker files
    - Log reasons: env_secrets, api_key, crash, oom, user_requested

**Total Phase 2:** 4 days

### Phase 3: Deprecation & Tools (P2) - Week 4+

11. **US-44.9** - Aggressive api-key deprecation (3 days)
    - UI banner with sunset date
    - Migration guide documentation
    - Prevent new api-key creation after sunset
    
12. **US-44.11** - Admin session recovery tools (3 days)
    - `POST /admin/sessions/:id/force-abort` endpoint
    - For incident recovery when workspaces deleted

**Total Phase 3:** 6 days

**Grand Total:** 23 days (~4.5 weeks, up from 14.5 days)

---

**Notes:**
- Phase 1 is P0 (critical path), must ship together
- Phases 2 & 3 can ship independently after Phase 1 stabilizes
- US-44.8 moved from Phase 3 to Phase 1 per user requirement: "Ops monitoring is P0"
- US-44.5 and US-44.6 are implemented in agentd (opencode cannot be modified). agentd has pod-level cgroup visibility and can compute per-session memory estimates from data already exposed via opencode's `/session` API.

---

## Testing Strategy

### Unit Tests
- `cmd/workspace-agentd/secrets_test.go` - api-key restart behavior, session-aware deferral
- `cmd/workspace-agentd/process_test.go` - OOM marker creation, exit code detection
- `api/internal/handlers/proxy_test.go` - Terminal SSE event emission on EOF

### Integration Tests
- `api/internal/handlers/secrets_integration_test.go` - Session-aware restart flow with busy sessions
- `api/internal/handlers/proxy_test.go` - Request buffering during restart, timeout handling, buffer full scenarios
- `cmd/workspace-agentd/secrets_test.go` - DB-backed rematerialization (fetch latest from DB, idempotent)

### E2E Tests (Canary Suite)
- **New scenario:** `e-session-lifecycle` - Test OOM detection, restart transparency, session-aware restart
- **Extend:** `d-session-limit` - Test memory pressure warnings

### Manual Testing Scenarios
1. **Trigger OOM:** Create 20 sessions, each loading 100K context tokens → verify OOM marker written, terminal shows error
2. **Trigger unsafe restart:** Add env-secret during active prompt → verify restart deferred, applied when idle
3. **Verify terminal events:** Kill opencode pod → verify SSE error event emitted, frontend shows message

---

## Rollout Plan

### Prerequisites
- [ ] Code review with focus on restart safety (no deadlocks, no race conditions)
- [ ] Load testing with session-aware restart (stress test with 50 concurrent secret updates)
- [ ] Documentation update: "Why did my session stop?" troubleshooting guide

### Rollout Stages
1. **Dev cluster** - Full testing with forced OOM and restarts
2. **Canary (10% of users)** - P0 items only, monitor for regressions (1 week)
3. **Gradual rollout (50%)** - Monitor restart rates, OOM events (1 week)
4. **Full rollout (100%)** - Complete rollout if no issues

### Rollback Plan
- **US-44.1** (Terminal events): Safe to rollback, no state changes
- **US-44.2** (Session-aware restart): Requires care - ensure no pending restarts during rollback, check goroutine cleanup
- **US-44.3** (api-key fix): Safe to rollback, reverts to buggy behavior (no worse than before)
- **US-44.4** (OOM detection): Safe to rollback, marker files ignored if feature disabled

---

## Success Metrics

### User-Facing (Week 1 post-rollout)
- **Zero unexplained "session stuck busy" reports** after rollout
- **Zero data loss reports** from config changes during active sessions
- **User surveys:** "Why did my session stop?" - Users should be able to answer confidently

### Operational (First month)
- **OOM events detected:** >90% of actual OOM kills (confirms detection works)
- **Deferred restarts:** >0 (confirms session-aware restart works)
- **Average defer time:** Median <2 minutes (confirms sessions don't stay busy long)
- **User-triggered restarts:** <10% of total restarts (indicates good automatic idle detection)
- **Request buffer timeout rate:** <0.1% (indicates healthy restart performance <30s)
- **Request buffer wait time:** P95 <5 seconds (typical restart duration)
- **Time to resolution:** Average time from OOM to user recovery <5 minutes (down from "requires kubectl + log diving")

---

## Configuration

### New Environment Variables

**agentd:**
```bash
# Session-aware restart timing
RESTART_IDLE_CHECK_INTERVAL=5s          # How often to poll opencode /sessions
# RESTART_MAX_DEFER_MINUTES - REMOVED (no forced timeout per user requirement)

# Request buffering
REQUEST_BUFFER_SIZE_PER_WORKSPACE=10    # Max buffered requests per workspace
REQUEST_BUFFER_TIMEOUT_SECONDS=30       # Timeout for buffered requests
```

**agentd:** (opencode is third-party and cannot be modified; agentd implements all monitoring)
```bash
# Memory monitoring
MEMORY_WARNING_THRESHOLD=0.85           # Warn at 85% of cgroup limit (changed from 0.75)
MEMORY_CHECK_INTERVAL_MS=60000          # Check every 60s
```

### Existing Configuration (No Changes)
- `maxActiveSessions` - Already exists, default 5, range 1-20
- Pod memory limits - Already configurable via Workspace CRD `spec.resources.memory` and `spec.resources.memoryLimit`

### Removed Configuration
- `RESTART_MAX_DEFER_MINUTES` - Removed per user requirement: no forced timeout, defer indefinitely

---

## Related Incidents

### Incident A: OOMKill
- **Workspace:** `a847faa5-19b4-463d-a434-1ce473a16f93`
- **Session:** `ses_13076538bffeYtLrhoZ2ccRM1E`
- **Date:** 2026-06-16 08:47:44 UTC
- **Root Cause:** 16 concurrent sessions exhausted 2 GiB memory limit
- **Resolution:** US-44.4 (OOM detection), US-44.5 (memory warnings)

### Incident B: Unsafe Restart
- **Workspace:** `8154ae86-d7b7-4f53-b046-d8d3b462b972`
- **Session:** `ses_130c14344ffeVF52UQ6QGPmB0P`
- **Date:** 2026-06-16 16:04:14 UTC
- **Root Cause:** Secret binding change during active session triggered immediate restart
- **Resolution:** US-44.2 (session-aware restart), US-44.3 (api-key bug fix)

---

## Dependencies

### Consumes
- Epic 3 (Proxy to opencode) - SSE streaming infrastructure
- Epic 6 (Collapse Sandbox) - workspace-agentd secret reconciliation
- Epic 27a (Credential Reload Foundation) - Secret materialization and restart logic

### Enables
- Epic 41 (Message Queue Reliability) - Session state management improvements
- Epic 37 (Session Activity & Unread State) - Reliable session lifecycle events

---

## Appendix: Secret Type Restart Matrix

| Secret Type | Restart Required? | Reason | Current Behavior | Bug? |
|------------|------------------|--------|------------------|------|
| `env-secret` | **YES** | Written to env file; Node.js `process.env` snapshot at startup | ✅ Restarts correctly | No |
| `api-key` | **YES** | Written to env file as `API_KEY_<NAME>`; same snapshot limitation | ❌ Does NOT restart | **YES** (US-44.3) |
| `ssh-key` | **NO** | Written to `~/.ssh/`; git/ssh read filesystem at invocation | ✅ No restart needed | No |
| `secret-file` | **NO** | Written to filesystem; read dynamically | ✅ No restart needed | No |
| `git-credential` | **NO** | Written to `~/.git-credentials`; git reads at invocation | ✅ No restart needed | No |
| `llm-provider` | **NO** | Hot-reloadable via opencode `/auth` API | ✅ No restart needed | No |

**Source:** `cmd/workspace-agentd/secrets.go:437-444`, `pkg/agentd/secrets/secrets.go:477-489`

**api-key Status (per user feedback):**
- ❌ **No current users** - User confirmed "no api-key secrets currently in use"
- ✅ Marked "legacy" in UI (cannot create new ones)
- ✅ Superseded by `llm-provider` for LLM credentials (hot-reloadable, no restart)
- ✅ Superseded by `env-secret` for other API keys (requires restart, but session-aware in Epic 44)
- 📅 **Deprecation timeline (US-44.9):** 6-month sunset from Epic 44 ship date, then prevent new creation

---

## Open Questions - ALL ANSWERED ✅

See `QUESTIONS-ANSWERED.md` for detailed responses. Summary:

1. ✅ **Request buffering scope:** Only `POST /messages` (not all requests)
2. ✅ **Buffer timeout:** 30 seconds with Prometheus monitoring
3. ✅ **Buffer size limit:** 10 requests per workspace with monitoring
4. ✅ **Restart timeout:** NO forced timeout - defer indefinitely, user can trigger via button/API
5. ✅ **UI notification:** Banner with "Restart Now" button, no dismiss button

**Additional answered questions:**
- ✅ **Ops monitoring priority:** YES - moved US-44.8 from P2 to P0 (first-class citizen)
- ✅ **Memory threshold:** 85% (changed from 75%)
- ✅ **api-key migration:** Aggressive deprecation with 6-month sunset (US-44.9)

**API endpoints for manual reload (already exist):**
- `POST /api/v1/workspaces/:id/agent/reload` - Immediate restart (halts sessions)
- `POST /api/v1/workspaces/:id/agent/reload?drain=true` - Graceful restart (waits for idle)
- `POST /api/v1/users/me/agents/reload?drain=true` - Bulk reload across all workspaces

---

**Epic Version:** 2.0 (Updated with all design corrections)  
**Last Updated:** 2026-06-16  
**Author:** OpenCode Investigation  
**Next Steps:** Begin Phase 1 implementation (~13 days)
