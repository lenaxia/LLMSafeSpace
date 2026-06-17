# Incident Analysis & Epic Plan: Session Reliability & Transparency

**Date:** 2026-06-16  
**Status:** Investigation Complete - Ready for Epic Planning  
**Goal:** Prevent silent session failures and improve platform transparency

> **⚠️ Outdated File Path References**
>
> This document was written during the initial investigation, before we confirmed that **opencode is a third-party binary that cannot be modified** in this repo. It contains references to file paths that do NOT exist in this codebase:
>
> - `cmd/opencode/main.go` — does not exist
> - `packages/opencode/src/*.ts` — does not exist
>
> Subsequent design iteration moved all opencode-specific work into agentd (`cmd/workspace-agentd/`) which **can** be modified. The current canonical Epic 44 README has the corrected file paths. This file is preserved for the empirical incident evidence (root causes, kubectl observations, cgroup paths) — treat the "Implementation" sections as historical thinking, not as files-to-modify lists.
>
> Cgroup paths in this document also reference cgroup v1 (`memory.limit_in_bytes`) — the actual codebase uses cgroup v2 (`memory.max`). See `cmd/workspace-agentd/main.go:577-594` for the production paths.

---

## Executive Summary

Two production incidents revealed critical gaps in session lifecycle observability and restart safety:

1. **Incident A (OOMKill)**: 16 concurrent sessions exhausted 2 GiB memory limit, pod terminated mid-task. Session stuck "busy" until next restart.
2. **Incident B (Unsafe Restart)**: User changed workspace secrets during active session, triggering immediate opencode SIGTERM and destroying in-flight work.

Both incidents share common failure: **no terminal event notification** when agent dies unexpectedly, and **no session-aware restart mechanism** for configuration changes.

### Key Findings

- ✅ **api-key secret type is LEGACY** (superseded by llm-provider). Code paths still handle the type (~250 references), but **no current users have api-key secrets defined** — confirmed by user (2026-06-16). Distinguish "code support exists" from "actively used by customers": the former is true, the latter is not, which justifies aggressive deprecation.
- ⚠️ **api-key restart bug**: Type writes to env file but `shouldRestart()` only checks `env-secret`
- ✅ **maxActiveSessions already exists** (default 5, range 1-20) - DO NOT reimplement
- ⚠️ **Memory limit is configurable** via Workspace CRD but defaults are aggressive (512Mi base × 4 burst = 2 GiB)
- ⚠️ **No session-aware restart**: Changes requiring restart terminate opencode immediately regardless of active work

---

## Incident Details

### Incident A: OOMKill (Workspace `a847faa5-19b4-463d-a434-1ce473a16f93`)

**Root Cause:** Pod exceeded 2 GiB memory limit with 16 active sessions (~1.6M context tokens)

**Timeline:**
- 08:43:14 UTC - Last healthy memory reading: 1.65 GiB (83% of limit)
- 08:47:44 UTC - Pod OOMKilled (exit 137)
- Session `ses_13076538bffeYtLrhoZ2ccRM1E` stuck `status: "busy"` until next pod restart

**Impact:**
- In-flight assistant turn lost (no recovery)
- No user notification (SSE stream silently closed on `io.EOF`)
- Session remained "busy" in SQLite until `abortStaleSessionsAfterStart` ran

**Evidence:**
```bash
kubectl describe pod opencode-a847faa5-...-5c9dc -n default
# Last State: Terminated (Reason: OOMKilled, Exit Code: 137)
```

**Memory Source:**
- Default: `pkg/controller/internal/workspace/pod_builder.go:249,251`
  - `defaultMemory = "512Mi"`
  - `burstFactor = 4`
  - Effective limit: 2 GiB

**Overridable:** Yes, via Workspace CRD:
```yaml
spec:
  resources:
    memory: "1Gi"        # Request
    memoryLimit: "4Gi"   # Limit
```

---

### Incident B: Unsafe Restart (Workspace `8154ae86-d7b7-4f53-b046-d8d3b462b972`)

**Root Cause:** User changed secret bindings while session was actively running (`status: "busy"`, `contextUsed: 102762`)

**Timeline:**
- 16:03:22 - User POSTed prompt → HTTP 204 accepted
- 16:04:07 - User answered tool permission question
- 16:04:13.953 - User PUT workspace bindings (added `GH_TOKEN` env-secret)
- 16:04:14.129 - workspace-agentd: `"env secrets changed, restarting opencode"`
- 16:04:14.135 - opencode SIGTERMed mid-stream
- **Result:** Assistant turn destroyed, `contextUsed` collapsed to 0

**Bug Confirmed:** 
- `cmd/workspace-agentd/secrets.go:415` - No session state check before restart
- No graceful draining period
- No user notification that operation will cause restart

---

## Secret Type Analysis

Reading `cmd/workspace-agentd/secrets.go` and `pkg/agentd/secrets/secrets.go`:

| Secret Type | Restart Required? | Reason | Current Behavior |
|------------|------------------|--------|------------------|
| `env-secret` | **YES** | Written to env file; Node.js `process.env` snapshot at startup | ✅ Restarts correctly |
| `api-key` | **YES** | Written to env file as `API_KEY_<NAME>`; same snapshot limitation | ⚠️ **BUG: Does NOT restart** |
| `ssh-key` | **NO** | Written to `~/.ssh/`; git/ssh read filesystem at invocation | ✅ No restart needed |
| `secret-file` | **NO** | Written to filesystem; read dynamically | ✅ No restart needed |
| `git-credential` | **NO** | Written to `~/.git-credentials`; git reads at invocation | ✅ No restart needed |
| `llm-provider` | **NO** | Hot-reloadable via opencode `/auth` API | ✅ No restart needed |

**Source Evidence:**

`cmd/workspace-agentd/secrets.go:437-444`:
```go
func shouldRestart(batch []secrets.Secret) bool {
    for _, s := range batch {
        if s.Type == "env-secret" {
            return true
        }
    }
    return false
}
```

**BUG:** Function only checks `env-secret`, missing `api-key` which also writes to env file (line 367, calls `applyAPIKey()` → writes `API_KEY_<NAME>` to `SecretsEnvPath`).

**api-key Status:**
- ⚠️ ~250 code references still handle the type, but **no customers have api-key secrets defined** — confirmed by user (2026-06-16)
- ✅ Marked "legacy" in UI (frontend/src/components/settings/SecretsTab.tsx:14-18)
- ✅ Cannot create new ones from UI (dropdown hidden)
- ✅ Existing secrets still functional
- ✅ Superseded by `llm-provider` type for LLM credentials

---

## Session Limit Configuration - Already Exists

**CRITICAL:** User requirements document requested `maxSessionsPerWorkspace` configuration.  
**FINDING:** This already exists as `maxActiveSessions` with full implementation.

### Current Implementation

**CRD Definition** (`pkg/apis/llmsafespace/v1/workspace_types.go:128`):
```go
// +kubebuilder:validation:Minimum=1
// +kubebuilder:validation:Maximum=20
// +kubebuilder:default=5
MaxActiveSessions int32 `json:"maxActiveSessions,omitempty"`
```

**Settings Schema** (`pkg/settings/schema.go:61`):
```go
{Key: "workspace.defaultMaxActiveSessions", Tier: 2, Type: TypeInt, 
 Default: 5, Min: intPtr(1), Max: intPtr(20), 
 Category: "Workspace", Label: "Max Sessions", 
 Description: "Concurrent sessions per workspace"}
```

**Enforcement** (`api/internal/handlers/proxy_connections.go:39-57`):
- HTTP 429 when limit reached
- Response includes `{"error": "active session limit reached", "maxActiveSessions": 5, "retryAfter": 10}`
- Tracks via SSE `session.status: idle/busy` events

**Configuration Levels:**
1. Instance-level default via settings API (mutable by admins)
2. Per-workspace override via CRD `spec.maxActiveSessions`
3. NOT exposed in Helm chart values (intentional - runtime configurable)

**Testing:**
- E2E tests exist: `sdks/canary/go/scenarios/d-session-limit.*`
- Full coverage of limit enforcement and rejection behavior

---

## Epic Plan: Transparency & Reliability Improvements

### P0: Critical Fixes (Prevent Data Loss)

#### 1. **Terminal SSE Events for Agent Death**

**Problem:** When opencode dies (OOM, SIGTERM, SIGKILL), SSE proxy silently closes stream. User sees spinner forever.

**Solution:** Emit synthetic `event: error` in proxy before closing stream.

**Implementation:**
- Modify `api/internal/handlers/proxy.go:440-447`
- Current behavior: `if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) { return }`
- New behavior: Detect abnormal closure (non-idle status) and emit error event

**Edge Case:** If no streaming connection is open (user not viewing terminal), session stays "busy" until restart. This is acceptable - addressed by stale session cleanup in opencode.

**Files:**
- `api/internal/handlers/proxy.go`
- Testing: `api/internal/handlers/proxy_test.go`

---

#### 2. **Session-Aware Restart Mechanism**

**Problem:** Binding changes requiring restart (env-secret, api-key) immediately SIGTERM opencode, destroying in-flight work.

**Current Behavior:**
```go
// cmd/workspace-agentd/secrets.go:415-419
if proc != nil && shouldRestart(batch) {
    log.Info("env secrets changed, restarting opencode")
    proc.restart()
    restarted = true
```

**Required Behavior:**
1. Check if any sessions are `status: "busy"` via opencode `/sessions` API
2. If busy: defer restart, track pending changes, buffer incoming secret updates
3. When all sessions become idle: apply buffered changes and restart transparently
4. If idle for >5 minutes with deferred restart: warn user in UI (optional - most restarts complete in <30s)

**User Experience:**
- Transparent: Changes applied automatically when safe
- No interruptions to active work
- No user action required

**Implementation:**
- New function: `checkSessionsIdleAndRestart(proc *processManager, batch []secrets.Secret) error`
- State: Track `pendingRestartSecrets []secrets.Secret` in agentd
- Goroutine: Poll opencode `/sessions` every 5s when restart pending
- Buffer: Merge subsequent secret updates into pending batch
- Apply: When all idle, apply buffered batch and restart

**Files:**
- `cmd/workspace-agentd/secrets.go` - Add session-aware restart logic
- `cmd/workspace-agentd/opencode/client.go` - Add `ListSessions()` API call
- Testing: `cmd/workspace-agentd/secrets_test.go`

**Configuration:**
- `RESTART_IDLE_CHECK_INTERVAL` (default: 5s)
- `RESTART_MAX_DEFER_MINUTES` (default: 30 minutes, then force restart with user warning)

---

#### 3. **Fix api-key Restart Bug**

**Problem:** `api-key` type writes to env file but `shouldRestart()` doesn't check for it.

**Solution:** Update `shouldRestart()` to include `api-key` type.

**Implementation:**
```go
func shouldRestart(batch []secrets.Secret) bool {
    for _, s := range batch {
        if s.Type == "env-secret" || s.Type == "api-key" {
            return true
        }
    }
    return false
}
```

**Files:**
- `cmd/workspace-agentd/secrets.go:437-444`
- Testing: `cmd/workspace-agentd/secrets_test.go` (add api-key restart test case)

**Note:** This is a one-line fix but MUST be shipped with session-aware restart (P0-2) to avoid breaking existing users who rely on api-key NOT restarting.

---

#### 4. **OOM Detection & User Notification**

**Problem:** Pod OOMKill leaves session "busy" with no indication of what happened.

**Solution:** 
1. workspace-agentd detects opencode process exit code 137 (SIGKILL from OOM)
2. Writes marker file `/home/workspace/.opencode-oom-marker` with timestamp
3. On restart, opencode checks for marker and logs OOM event
4. API proxy checks marker file and includes OOM notice in session history

**User-Facing:**
- Terminal shows: "⚠️ Agent was terminated due to memory exhaustion. Previous work may be lost."
- Session history includes: `{"event": "oom_detected", "timestamp": "...", "memoryLimit": "2Gi"}`

**Implementation:**
- `cmd/workspace-agentd/process.go` - Detect exit 137, write marker
- `cmd/opencode/main.go` - Check marker on startup, emit event
- `api/internal/handlers/history.go` - Include OOM events in response

**Files:**
- `cmd/workspace-agentd/process.go`
- `cmd/opencode/main.go`
- `api/internal/handlers/history.go`
- Testing: Unit tests for marker creation/detection

---

### P1: Improved Observability (Non-Blocking)

#### 5. **Memory Pressure Warnings**

**Problem:** No early warning before OOM kill.

**Solution:** 
- opencode periodically checks `process.memoryUsage()` (every 60s)
- When >75% of cgroup limit: emit warning SSE event
- User sees: "⚠️ Memory usage high (1.5 GiB / 2 GiB). Consider reducing concurrent sessions or increasing workspace memory limit."

**Implementation:**
- New file: `packages/opencode/src/metrics/memory-monitor.ts`
- Integrate with existing SSE broadcaster
- Read cgroup limit from `/sys/fs/cgroup/memory/memory.limit_in_bytes`

**Configuration:**
- `MEMORY_WARNING_THRESHOLD` (default: 0.75)
- `MEMORY_CHECK_INTERVAL_MS` (default: 60000)

**Files:**
- `packages/opencode/src/metrics/memory-monitor.ts` (new)
- `packages/opencode/src/server.ts` (integrate monitor)

---

#### 6. **Per-Session Memory Attribution**

**Problem:** 16 sessions shared 1.65 GiB, but no visibility into which sessions are memory-heavy.

**Solution:**
- Track `estimatedMemoryMB` per session based on `contextUsed` tokens
- Rough formula: `memory ≈ (contextTokens × 2 bytes) + (historyTurns × 10KB overhead)`
- Include in `GET /sessions/:id` response
- Terminal UI shows memory usage in session list

**Implementation:**
- `packages/opencode/src/session-manager.ts` - Add memory estimation
- `api/internal/handlers/history.go` - Include memory field in session metadata

**Files:**
- `packages/opencode/src/session-manager.ts`
- `api/internal/handlers/history.go`

---

#### 7. **Restart Reason Logging**

**Problem:** No record of WHY opencode restarted (user action, OOM, crash, config change).

**Solution:**
- workspace-agentd writes restart reason to `/home/workspace/.opencode-restart-reason`
- Format: `{"reason": "env_secrets_changed", "timestamp": "2026-06-16T16:04:14Z", "secretNames": ["GH_TOKEN"]}`
- opencode reads on startup and logs to console
- API includes in workspace events

**Implementation:**
- `cmd/workspace-agentd/process.go` - Write reason file before restart
- `cmd/opencode/main.go` - Read and log reason on startup

**Files:**
- `cmd/workspace-agentd/process.go`
- `cmd/opencode/main.go`

---

### P2: Operational Metrics (Backend Only)

#### 8. **Prometheus Metrics**

**Metrics to Add:**
- `workspace_oom_kills_total{workspace_id}` - Counter
- `workspace_restarts_total{workspace_id, reason}` - Counter (reasons: env_secrets, api_key, crash, oom, user_requested)
- `workspace_memory_bytes{workspace_id}` - Gauge
- `workspace_active_sessions{workspace_id}` - Gauge
- `workspace_context_tokens{workspace_id}` - Gauge

**NOT User-Facing:** These are for SRE/admin dashboards only. Do not surface in UI.

**Implementation:**
- `cmd/workspace-agentd/metrics.go` - Expose Prometheus endpoint on `:9090/metrics`
- `pkg/controller/metrics.go` - Aggregate metrics from all workspaces

**Files:**
- `cmd/workspace-agentd/metrics.go` (new)
- `pkg/controller/metrics.go` (extend existing)

---

### Explicitly Rejected Items

Per user requirements:

❌ **maxTotalContextTokens** - User rejected this feature. Do NOT implement.

❌ **maxSessionsPerWorkspace** - Already exists as `maxActiveSessions`. Do NOT reimplement.

❌ **New Helm Chart Values** - `maxActiveSessions` is intentionally runtime-configurable via settings API. Do NOT expose in Helm.

❌ **Run History Persistence** - NOT in scope for this Epic. Would require:
  - Database schema for event log
  - Retention policy (auto-prune after N days)
  - API endpoints for querying history
  - Considerable storage overhead
  - Defer to separate Epic if user requests

---

## Critical Evaluation of Recommendations

### What Will Actually Help?

✅ **P0 Items (1-4)** - Directly address data loss and user confusion:
- Terminal events: Users know when agent died
- Session-aware restart: Prevents data loss from config changes
- api-key restart bug fix: Prevents silent env inconsistency
- OOM detection: Users understand what happened

✅ **P1 Items (5-7)** - Improve user experience without adding complexity:
- Memory warnings: Early warning before OOM
- Per-session memory: Helps users identify memory-heavy sessions
- Restart reason logging: Debugging aid for both users and support

⚠️ **P2 Items (8)** - Only implement if operational monitoring is priority:
- Prometheus metrics: Useful for SREs but no user-facing value
- Consider deferring until operational pain points are identified

### Alternative Approaches Considered

#### Alternative: UI Warning Before Restart-Causing Operations

**Rejected Reason:** Places burden on user to schedule config changes during idle periods. Session-aware restart is strictly better - fully transparent to user.

#### Alternative: Queue Secret Changes Until Idle

**Accepted:** This IS the session-aware restart mechanism (P0-2). Not an alternative, it's the solution.

#### Alternative: Increase Default Memory Limits

**Rejected Reason:** Wastes resources on typical workloads (most users have 1-3 sessions). Better solution: Memory pressure warnings (P1-5) + user education about `maxActiveSessions` and workspace memory overrides.

#### Alternative: Automatic Session Cleanup After Timeout

**Out of Scope:** Sessions are already cleaned up by `abortStaleSessionsAfterStart`. This Epic focuses on preventing silent failures, not session lifecycle management.

---

## Implementation Order

### Phase 1: Critical Path (Week 1)
1. Terminal SSE events (P0-1) - 2 days
2. api-key restart bug fix (P0-3) - 1 hour
3. Session-aware restart (P0-2) - 3 days
4. OOM detection (P0-4) - 1 day

### Phase 2: Observability (Week 2)
5. Memory pressure warnings (P1-5) - 2 days
6. Per-session memory attribution (P1-6) - 1 day
7. Restart reason logging (P1-7) - 1 day

### Phase 3: Operational (Optional)
8. Prometheus metrics (P2-8) - 2 days

---

## Testing Strategy

### Unit Tests
- `cmd/workspace-agentd/secrets_test.go` - api-key restart behavior
- `cmd/workspace-agentd/process_test.go` - OOM marker creation
- `api/internal/handlers/proxy_test.go` - Terminal SSE event emission

### Integration Tests
- `api/internal/handlers/secrets_integration_test.go` - Session-aware restart flow
- `cmd/workspace-agentd/secrets_test.go` - Buffered secret changes

### E2E Tests (Canary Suite)
- New scenario: `e-session-lifecycle` - Test OOM detection, restart transparency
- Extend: `d-session-limit` - Test memory pressure warnings

### Manual Testing
1. Trigger OOM: Create 20 sessions, each loading 100K context tokens
2. Trigger unsafe restart: Add env-secret during active prompt
3. Verify: Terminal shows error events, session doesn't stay "busy"

---

## Rollout Plan

### Prerequisites
- [ ] Code review with focus on restart safety
- [ ] Load testing with session-aware restart (ensure no deadlocks)
- [ ] Documentation update: "Why did my session stop?" troubleshooting guide

### Rollout Stages
1. **Canary (10% of users)** - P0 items only, monitor for regressions
2. **Gradual rollout (50% → 100%)** - Over 1 week
3. **Post-rollout monitoring** - Track OOM events, restart reasons, user feedback

### Rollback Plan
- P0-1 (Terminal events): Safe to rollback, no state changes
- P0-2 (Session-aware restart): Requires care - ensure no pending restarts during rollback
- P0-3 (api-key fix): Safe to rollback, reverts to buggy behavior (no worse than before)
- P0-4 (OOM detection): Safe to rollback, marker files ignored if feature disabled

---

## Success Metrics

### User-Facing
- **Zero unexplained "session stuck busy" reports** after rollout
- **Zero data loss reports** from config changes during active sessions
- **User surveys:** "Why did my session stop?" - Users should be able to answer confidently

### Operational
- **OOM events detected:** >0 (confirms detection works)
- **Deferred restarts:** >0 (confirms session-aware restart works)
- **Time to resolution:** Average time from OOM to user recovery <5 minutes (down from "requires kubectl + log diving")

---

## Related Documentation

### Source Files Referenced
- `cmd/workspace-agentd/secrets.go` - Secret reconciliation and restart logic
- `cmd/workspace-agentd/stale_sessions.go` - Session recovery after crashes
- `api/internal/handlers/proxy.go` - SSE proxy and connection management
- `pkg/apis/llmsafespace/v1/workspace_types.go` - Workspace CRD including maxActiveSessions
- `pkg/settings/schema.go` - Platform-wide configuration schema
- `controller/internal/workspace/pod_builder.go` - Pod resource limits

### Investigation Logs
- `kubectl logs` for both incidents (API, agentd, opencode)
- `kubectl describe pod` for OOM evidence
- Session SQLite database queries for stuck "busy" status

---

## Questions for User

1. **Phase 3 (Prometheus Metrics):** Is operational monitoring a priority, or defer this phase?
2. **Restart Max Defer Time:** 30 minutes reasonable, or prefer shorter/longer timeout?
3. **Memory Warning Threshold:** 75% of limit appropriate, or prefer 80%/85%?
4. **api-key Migration Path:** Should we actively migrate users from api-key to llm-provider, or leave as-is?

---

## Appendix: api-key Secret Type Status

**Current State:**
- ⚠️ Functional in code (~250 references handle the type) but **no customers have api-key secrets defined**
- ✅ Marked "legacy" in UI (cannot create new ones)
- ✅ Superseded by `llm-provider` for LLM credentials
- ✅ Still useful for non-LLM API keys (generic `API_KEY_<NAME>` env vars)

**Migration History:**
- `000010_rename_llm_provider_to_api_key.up.sql` - Consolidated types in 2025
- Frontend shows migration banner: "Consider recreating as LLM Providers or Environment Variables"

**Recommendation:** Keep api-key type for backward compatibility. Existing users depend on it for non-LLM use cases (example: `API_KEY_GITHUB`, `API_KEY_SLACK`). For new users, env-secret or llm-provider are preferred.

---

**Document Version:** 1.0  
**Last Updated:** 2026-06-16  
**Author:** OpenCode Investigation  
**Next Steps:** Review with user, prioritize phases, create Epic user stories
