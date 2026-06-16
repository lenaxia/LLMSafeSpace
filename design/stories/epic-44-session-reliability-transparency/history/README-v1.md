# Epic 44: Session Reliability & Transparency

**Status:** Design Validated + User Feedback Applied - Ready for Implementation  
**Created:** 2026-06-16  
**Validated:** 2026-06-16 (see VALIDATION-COMPLETE.md for all assumptions validated)  
**Depends on:** Epic 3 (Proxy to opencode), Epic 6 (Collapse Sandbox), Epic 27a (Credential Reload Foundation)

**Key User Requirements:**
- Ops monitoring is P0 (first-class citizen)
- NO timeout for deferred restart (defer indefinitely with UI notification)
- 85% memory warning threshold
- Deprecate api-key aggressively (no current users)
- Buffer multiple secret updates (agentic workflows run for hours)

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
3. **OOM detection & notification** with clear user-facing messages
4. **Memory pressure warnings** at 75% of pod limit
5. **Fix api-key restart bug** (writes to env file but doesn't trigger restart)

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

### New Behavior (Session-Aware)

```
User changes workspace secret binding (env-secret or api-key)
  ↓
agentd detects change, checks opencode /sessions API
  ↓
If any session is status:"busy":
  ├─ agentd buffers secret changes
  ├─ agentd polls /sessions every 5s
  └─ When all sessions idle:
      ├─ agentd applies buffered changes
      └─ agentd restarts opencode transparently
Else (all idle):
  └─ agentd immediately applies changes and restarts
```

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

### opencode (opencode process)
- Executes session logic
- Monitors own memory usage via Node.js `process.memoryUsage()`
- Reads cgroup memory limit from `/sys/fs/cgroup/memory/memory.limit_in_bytes`
- Emits memory pressure warnings via SSE when >threshold
- Reads restart reason markers on startup
- Emits OOM/crash events if markers present

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
**Solution:** Defer restarts until all sessions idle using existing `sessionStatusTracker`  
**Files:** `cmd/workspace-agentd/secrets.go`, `cmd/workspace-agentd/main.go`  
**Acceptance:**
- [ ] `checkSessionsIdleAndRestart()` function checks `sessionStatusTracker.hasAnyBusy()`
- [ ] Add `sessionStatusTracker.hasAnyBusy()` method (returns `len(busySessions) > 0`)
- [ ] Add `sessionStatusTracker.listBusy()` method (returns `[]string` of session IDs)
- [ ] Pass `sessionStatusTracker` reference to `reloadSecretsHandler()`
- [ ] If any sessions busy: spawn background goroutine to poll tracker every 5s
- [ ] Background goroutine applies restart when `!tracker.hasAnyBusy()`
- [ ] Subsequent secret updates during defer are ignored (user must re-apply after restart)
- [ ] Config: `RESTART_IDLE_CHECK_INTERVAL=5s`, `RESTART_MAX_DEFER_MINUTES=30`
- [ ] After 30min deferred, force restart with logged warning
- [ ] If `sessionStatusTracker` has no data (SSE disconnected), treat as "all idle" and restart immediately with logged warning

**Design Note:** Uses existing SSE-based session tracking. The `/session` REST API returns `Status: "idle"` hardcoded for all sessions, so cannot be used for live busy/idle detection.

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
**Files:** `cmd/workspace-agentd/main.go` (managedProcess supervisor), `cmd/opencode/main.go`, `api/internal/handlers/history.go`  
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
**Solution:** opencode monitors memory usage, emits warning at 75% of limit  
**Files:** `packages/opencode/src/metrics/memory-monitor.ts` (new), `packages/opencode/src/server.ts`  
**Acceptance:**
- [ ] opencode checks `process.memoryUsage()` every 60s
- [ ] When >75% of cgroup limit: emit warning SSE event
- [ ] User sees: "⚠️ Memory usage high (1.5 GiB / 2 GiB). Consider reducing concurrent sessions or increasing workspace memory limit."
- [ ] Read cgroup limit from `/sys/fs/cgroup/memory/memory.limit_in_bytes`
- [ ] Config: `MEMORY_WARNING_THRESHOLD=0.75`, `MEMORY_CHECK_INTERVAL_MS=60000`

---

#### US-44.6: Per-Session Memory Attribution
**Problem:** No visibility into which sessions are memory-heavy  
**Solution:** Track estimated memory per session based on context tokens  
**Files:** `packages/opencode/src/session-manager.ts`, `api/internal/handlers/history.go`  
**Acceptance:**
- [ ] `estimatedMemoryMB` tracked per session: `(contextTokens × 2 bytes) + (historyTurns × 10KB overhead)`
- [ ] Included in `GET /sessions/:id` response
- [ ] Terminal UI shows memory usage in session list
- [ ] Helps users identify memory-heavy sessions

---

#### US-44.7: Restart Reason Logging
**Problem:** No record of WHY opencode restarted  
**Solution:** agentd writes restart reason to marker file, opencode logs on startup  
**Files:** `cmd/workspace-agentd/process.go`, `cmd/opencode/main.go`  
**Acceptance:**
- [ ] agentd writes `/home/workspace/.opencode-restart-reason` before restart
- [ ] Format: `{"reason": "env_secrets_changed", "timestamp": "2026-06-16T16:04:14Z", "secretNames": ["GH_TOKEN"]}`
- [ ] opencode reads on startup and logs to console
- [ ] Reasons: `env_secrets_changed`, `api_key_changed`, `crash`, `oom`, `user_requested`
- [ ] API includes restart events in workspace event stream (optional)

---

### P2: Operational Metrics (Backend Only)

#### US-44.8: Additional Observability Metrics
**Problem:** No operational visibility into workspace performance beyond OOM events (covered in US-44.4)  
**Solution:** Expose additional Prometheus metrics for SRE dashboards  
**Files:** `cmd/workspace-agentd/main.go` (extend existing metrics)  
**Acceptance:**
- [ ] `workspace_restarts_total{workspace_id, reason}` - Counter (reasons: env_secrets, api_key, crash, user_requested)
- [ ] `workspace_memory_bytes{workspace_id}` - Gauge (current memory usage)
- [ ] `workspace_active_sessions{workspace_id}` - Gauge (from sessionStatusTracker)
- [ ] `workspace_context_tokens{workspace_id}` - Gauge (sum across all sessions)
- [ ] Metrics exposed on agentd `:9090/metrics` endpoint (already exists for gate timings)
- [ ] NOT user-facing (SRE/admin dashboards only)

**Note:** `workspace_oom_kills_total` moved to US-44.4 (P0) since it's critical for alerting.

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

### Phase 1: Critical Path (Week 1)
1. **US-44.1** - Terminal SSE events (2 days)
   - Emit error on abnormal EOF in proxy
   - Add component responsibility documentation
   
2. **US-44.3** - api-key restart bug fix (1 hour)
   - One-line change to `shouldRestart()`
   
3. **US-44.2** - Session-aware restart (4 days)
   - Add `sessionStatusTracker.hasAnyBusy()` and `listBusy()` methods
   - Wire tracker to `reloadSecretsHandler()`
   - Implement deferred restart goroutine
   - Add SSE disconnect fallback handling
   
4. **US-44.4** - OOM detection + metrics (1.5 days)
   - Detect exit 137 in `managedProcess.supervise()`
   - Write marker files
   - Add `workspace_oom_kills_total` Prometheus counter
   - Wire opencode startup marker reading

**Total Phase 1:** 8.5 days

### Phase 2: Observability (Week 2)
5. **US-44.5** - Memory pressure warnings (2 days)
6. **US-44.6** - Per-session memory attribution (1 day)
7. **US-44.7** - Restart reason logging (1 day)

**Total Phase 2:** 4 days

### Phase 3: Operational (Optional)
8. **US-44.8** - Additional Prometheus metrics (2 days)
   - `restarts_total`, `memory_bytes`, `active_sessions`, `context_tokens`
   - Extend existing agentd metrics endpoint

**Total Phase 3:** 2 days

**Grand Total:** 14.5 days (~3 weeks)

---

## Testing Strategy

### Unit Tests
- `cmd/workspace-agentd/secrets_test.go` - api-key restart behavior, session-aware deferral
- `cmd/workspace-agentd/process_test.go` - OOM marker creation, exit code detection
- `api/internal/handlers/proxy_test.go` - Terminal SSE event emission on EOF

### Integration Tests
- `api/internal/handlers/secrets_integration_test.go` - Session-aware restart flow with busy sessions
- `cmd/workspace-agentd/secrets_test.go` - Buffered secret changes merge correctly

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
- **OOM events detected:** >0 (confirms detection works)
- **Deferred restarts:** >0 (confirms session-aware restart works)
- **Average defer time:** <2 minutes (confirms sessions don't stay idle for long)
- **Forced restarts after 30min:** <1% of total deferred restarts (confirms timeout is appropriate)
- **Time to resolution:** Average time from OOM to user recovery <5 minutes (down from "requires kubectl + log diving")

---

## Configuration

### New Environment Variables (agentd)
```bash
# Session-aware restart timing
RESTART_IDLE_CHECK_INTERVAL=5s          # How often to poll opencode /sessions
RESTART_MAX_DEFER_MINUTES=30            # Force restart after this duration

# Memory monitoring (opencode)
MEMORY_WARNING_THRESHOLD=0.75           # Warn at 75% of cgroup limit
MEMORY_CHECK_INTERVAL_MS=60000          # Check every 60s
```

### Existing Configuration (No Changes)
- `maxActiveSessions` - Already exists, default 5, range 1-20
- Pod memory limits - Already configurable via Workspace CRD `spec.resources.memory` and `spec.resources.memoryLimit`

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

**api-key Status:**
- ✅ Still actively used (250+ code references)
- ✅ Marked "legacy" in UI (cannot create new ones)
- ✅ Superseded by `llm-provider` for LLM credentials
- ✅ Still useful for generic API keys (e.g., `API_KEY_GITHUB`, `API_KEY_SLACK`)

---

## Open Questions

1. **Phase 3 (Prometheus Metrics):** Is operational monitoring a priority, or defer this phase?
2. **Restart Max Defer Time:** 30 minutes reasonable, or prefer shorter/longer timeout?
3. **Memory Warning Threshold:** 75% of limit appropriate, or prefer 80%/85%?
4. **api-key Migration Path:** Should we actively migrate users from api-key to llm-provider, or leave as-is?

---

**Epic Version:** 1.0  
**Last Updated:** 2026-06-16  
**Author:** OpenCode Investigation  
**Next Steps:** Review with team, prioritize stories, begin Phase 1 implementation
