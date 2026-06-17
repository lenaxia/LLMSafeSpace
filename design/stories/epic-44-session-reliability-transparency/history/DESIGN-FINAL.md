# Epic 44: Design Summary - FINAL

**Status:** Ready for Implementation  
**All Questions Answered:** 2026-06-16  
**Total Stories:** 11 (up from 8)  
**Total Estimate:** ~23 days (up from 14.5 days)

---

## Core Design Principles (Validated)

### 1. Process Restarts Are Unavoidable for env-secrets
**Reason:** Node.js `process.env` is immutable after startup (core language behavior)  
**Tests Conducted:** Confirmed via live Node.js tests that env vars cannot be hot-reloaded  
**Conclusion:** Accept restarts, make them transparent

### 2. Secrets Written to DB Immediately (No Buffering)
**WRONG Initial Design:** Buffer secret changes in-memory in agentd  
**CORRECT Design:** Secrets written to PostgreSQL immediately when user saves them  
**Rematerialization:** agentd re-fetches ALL secrets from DB when restart occurs (idempotent)

### 3. Request Buffering NOT Secret Buffering
**New Requirement:** Buffer incoming `POST /messages` HTTP requests in API proxy during restart  
**Why:** Provides transparent user experience - user never sees 502 errors  
**How:** Proxy holds HTTP connections open (up to 30s), forwards when opencode healthy

### 4. Operations Monitoring is P0
**Not Optional:** Prometheus metrics are first-class citizen, must be in Phase 1  
**Why:** SRE teams need visibility into restart patterns, buffer timeouts, OOM events

### 5. No Forced Restart Timeout
**Initial Design:** Force restart after 30 minutes  
**Corrected Design:** Defer indefinitely until sessions idle OR user clicks "Restart Now"  
**Rationale:** Multi-hour agentic workflows are common, forced timeout causes data loss

---

## Updated Story List (11 Stories)

### Phase 1: P0 - Critical Path (11.5 days)

| Story | Description | Days | Files |
|-------|-------------|------|-------|
| US-44.1a | Terminal SSE Events (Proxy) | 1.5 | `api/internal/handlers/proxy.go` |
| US-44.1b | Terminal SSE Events (Frontend) | 0.5 | `frontend/src/components/workspace/Terminal.tsx` |
| US-44.2 | Session-Aware Restart | 4 | `cmd/workspace-agentd/secrets.go`, `main.go` |
| US-44.3 | Fix api-key Restart Bug | 0.5 | `cmd/workspace-agentd/secrets.go:437` |
| US-44.4 | OOM Detection + Metrics | 1.5 | `cmd/workspace-agentd/main.go` |
| US-44.8 | Ops Monitoring (Prometheus) | 2 | `cmd/workspace-agentd/main.go` |
| US-44.10 | API Proxy Request Buffering | 3 | `api/internal/handlers/proxy.go` |

**Phase 1 Total:** 13 days (was 8.5 days)

### Phase 2: P1 - Observability (4 days)

| Story | Description | Days | Files |
|-------|-------------|------|-------|
| US-44.5 | Memory Pressure Warnings (85% threshold) | 2 | opencode (cannot modify) |
| US-44.6 | Per-Session Memory Attribution | 1 | opencode (cannot modify) |
| US-44.7 | Restart Reason Logging | 1 | `cmd/workspace-agentd/process.go` |

**Phase 2 Total:** 4 days (unchanged)

### Phase 3: P2 - Deprecation (6 days)

| Story | Description | Days | Files |
|-------|-------------|------|-------|
| US-44.9 | Aggressive api-key Deprecation | 3 | Multiple (UI, API, docs) |
| US-44.11 | Admin Session Recovery Tools | 3 | `api/internal/handlers/admin.go` |

**Phase 3 Total:** 6 days (new)

**Grand Total:** 23 days (~4.5 weeks)

---

## Key Design Corrections Applied

### 1. Architecture: DB-Backed Rematerialization

**Old (WRONG):**
```
User changes secret → agentd buffers in memory → waits for idle →
applies buffered changes → restarts
```

**New (CORRECT):**
```
User changes secret → API writes to PostgreSQL immediately →
agentd marks "needs rematerialization" → waits for idle →
agentd re-fetches ALL secrets from DB → rematerializes → restarts
```

**Why Better:**
- No state management in agentd (DB is source of truth)
- Idempotent (can retry without side effects)
- Multiple secret updates handled automatically (always get latest from DB)
- Simpler code (no buffer, no merge logic)

---

### 2. New Story: US-44.10 - Request Buffering

**Problem:** Users see 502 errors when sending prompts during restart  
**Solution:** API proxy buffers `POST /messages` requests during restart

**Implementation:**
- Buffer scope: Only `POST /messages` (not GET/DELETE/etc.)
- Buffer size: 10 requests per workspace
- Buffer timeout: 30 seconds
- Behavior: Hold HTTP connection open, forward FIFO when healthy
- Metrics: `buffer_timeout_total`, `buffer_wait_seconds`, `buffer_size`, `buffer_full_total`

**User Experience:**
```
User sends prompt → opencode restarting (5 seconds) → request buffered →
opencode healthy → request forwarded → user gets response

Total time: ~7 seconds (2s restart + 5s LLM call)
User never sees error!
```

---

### 3. Moved US-44.8 from P2 to P0

**Rationale:** Operations monitoring is not optional, it's a first-class requirement

**New Metrics (all in Phase 1):**
```go
// OOM events (critical alerting)
workspace_oom_kills_total{workspace_id}

// Restart tracking
workspace_restarts_total{workspace_id, reason}

// Resource monitoring
workspace_memory_bytes{workspace_id}
workspace_active_sessions{workspace_id}
workspace_context_tokens{workspace_id}

// Request buffering (NEW)
workspace_request_buffer_timeout_total{workspace_id}
workspace_request_buffer_wait_seconds{workspace_id}
workspace_request_buffer_size{workspace_id}
workspace_request_buffer_full_total{workspace_id}
```

---

### 4. Memory Warning Threshold: 85% (Not 75%)

**Changed in:** US-44.5  
**Rationale:** User specified 85% threshold  
**Config:** `MEMORY_WARNING_THRESHOLD=0.85`

---

### 5. No Forced Restart Timeout

**Changed in:** US-44.2  
**Old:** Force restart after 30 minutes  
**New:** Defer indefinitely, user triggers manually

**User Options to Apply Pending Restart:**
1. **Wait for idle:** Restart applies automatically when all sessions become idle
2. **UI button:** Click "Restart Now" in banner (calls `POST /agent/reload?drain=false`)
3. **API call:** `POST /api/v1/workspaces/:id/agent/reload?drain=true` (graceful wait)

**UI Banner:**
```
┌──────────────────────────────────────────────────────────────┐
│ ⚠️ Configuration update pending - will apply when sessions   │
│    are idle                                                   │
│                                              [Restart Now]    │
│                           (will halt active sessions)         │
└──────────────────────────────────────────────────────────────┘
```

---

### 6. New Stories Added

#### US-44.9: Aggressive api-key Deprecation
**Why:** User confirmed no api-key secrets currently in use  
**Approach:**
- Mark api-key type as deprecated in UI (already done)
- Add migration guide: api-key → llm-provider for LLM keys
- Add banner: "api-key secrets are deprecated, please migrate to llm-provider or env-secret"
- Set sunset date: 6 months from Epic 44 ship date
- After sunset: prevent creation of new api-key secrets

#### US-44.10: API Proxy Request Buffering
**Why:** Core requirement for transparent restarts  
**Details:** See section 2 above

#### US-44.11: Admin Session Recovery Tools
**Why:** Incident response - stuck sessions from past incidents  
**Endpoint:** `POST /api/v1/admin/sessions/:sessionId/force-abort`  
**Use Case:** When workspaces are deleted but sessions persist in DB

---

## Configuration Changes

### New Environment Variables

```bash
# agentd - Session-aware restart
RESTART_IDLE_CHECK_INTERVAL=5s          # How often to poll session status

# agentd - Request buffering
REQUEST_BUFFER_SIZE_PER_WORKSPACE=10    # Max buffered requests
REQUEST_BUFFER_TIMEOUT_SECONDS=30       # Give up after this long

# opencode - Memory monitoring (UPDATED)
MEMORY_WARNING_THRESHOLD=0.85           # Was 0.75, now 85%
MEMORY_CHECK_INTERVAL_MS=60000          # Check every 60s
```

### Removed Configuration

```bash
# REMOVED - No forced timeout
# RESTART_MAX_DEFER_MINUTES=30
```

---

## Testing Strategy Updates

### New Integration Tests

**Request Buffering:**
- Test: Send prompt during restart, verify no 502 error
- Test: Send 11 prompts during restart, verify 11th rejected (buffer full)
- Test: Restart takes >30s, verify timeout error message

**Session-Aware Restart:**
- Test: Change secret during busy session, verify deferred
- Test: Sessions become idle, verify automatic restart
- Test: User clicks "Restart Now", verify immediate restart
- Test: Multiple secret changes during defer, verify latest applied

**Monitoring:**
- Test: Trigger OOM, verify `workspace_oom_kills_total` incremented
- Test: Buffer request, verify `workspace_request_buffer_wait_seconds` histogram updated

---

## Success Metrics (Updated)

### Week 1 Post-Rollout

- **Zero 502 errors** during restarts (request buffering working)
- **Zero data loss** from secret changes during busy sessions
- **Zero unexplained "session stuck busy"** reports

### First Month

- **OOM detection rate:** >90% of OOM events detected and logged
- **Average request buffer wait:** <5 seconds (P95)
- **Buffer timeout rate:** <0.1% (indicates healthy restart performance)
- **Deferred restart duration:** Median <2 minutes (sessions don't stay busy long)
- **User-triggered restarts:** <10% of total restarts (indicates good automatic behavior)

---

## Dependencies

### Existing Features (Consumed)
- Epic 3: Proxy to opencode (SSE streaming)
- Epic 6: Collapse Sandbox (workspace-agentd secret reconciliation)
- Epic 27a: Credential Reload Foundation (hot-reload for llm-provider)
- Epic 27b: Bulk Agent Reload (`POST /users/me/agents/reload`)

### API Endpoints (Existing)
- `POST /api/v1/workspaces/:id/agent/reload` - Manual reload trigger
- `POST /api/v1/workspaces/:id/reload-secrets` - Secret rematerialization
- `POST /api/v1/users/me/agents/reload` - Bulk reload across workspaces

---

## Risk Mitigation

### High Risk Areas

1. **Request buffering memory exhaustion**
   - **Mitigation:** 10 request limit per workspace, 30s timeout
   - **Monitoring:** `workspace_request_buffer_size` gauge, alert if >8

2. **Session-aware restart deadlock**
   - **Mitigation:** SSE disconnect fallback (restart immediately if tracker offline)
   - **Testing:** Kill SSE stream, verify restart proceeds

3. **Deferred restart never applies**
   - **Mitigation:** UI notification with manual trigger
   - **Monitoring:** Track time between secret change and restart application

4. **process.env assumption wrong**
   - **Mitigation:** Already tested - confirmed Node.js behavior
   - **Fallback:** None needed - restarts are architecturally required

---

## Rollout Plan

### Phase 1 Only (Critical Path)

**Week 1:** Dev cluster - Force OOM, test restarts, verify request buffering  
**Week 2:** Canary (10%) - Monitor metrics, watch for regressions  
**Week 3:** Gradual (50%) - Expand if canary successful  
**Week 4:** Full (100%) - Complete rollout

**Rollback:** Keep Phase 1 stories atomic - each can rollback independently

### Phases 2 & 3 (Optional)

Ship after Phase 1 is stable (2+ weeks in production)

---

## Open Questions (ALL ANSWERED)

1. ✅ Request buffering scope: Only POST /messages
2. ✅ Buffer timeout: 30 seconds with monitoring
3. ✅ Buffer size: 10 requests with monitoring
4. ✅ Restart timeout: No forced timeout, defer indefinitely
5. ✅ UI notification: Banner with "Restart Now" button, no dismiss

---

**Next Step:** Update README.md with this finalized design, then begin Phase 1 implementation.
