# Epic 44 Design Questions - ANSWERED

**Date:** 2026-06-16  
**Status:** All questions answered, ready to update README.md

---

## Question 1: Request Buffering Scope

**Question:** When opencode is restarting, which requests should the API proxy buffer?

**Answer:** **A) Only POST /messages**

**Rationale:**
- Most critical user-facing operation
- Prevents "session busy" errors during restart
- Other requests (GET /sessions, GET /history, POST /abort) can safely fail with 502 since they're read-only or idempotent
- Simpler implementation, less memory overhead

**Implementation Note:** Buffer only `POST /api/v1/workspaces/:id/sessions/:sessionId/messages` requests in the proxy.

---

## Question 2: Buffer Timeout

**Question:** How long should we hold buffered requests before giving up?

**Answer:** **A) 30 seconds**

**Rationale:**
- Typical restart takes 2-5 seconds
- 30 seconds provides 6x safety margin
- Conservative enough for slow restarts
- Short enough to prevent indefinite hangs

**Requirements:**
- ✅ Must add Prometheus metrics for monitoring:
  - `workspace_request_buffer_timeout_total{workspace_id}` - Counter
  - `workspace_request_buffer_wait_seconds{workspace_id}` - Histogram
- Clear error message if timeout exceeded: "Workspace is restarting, please try again in a moment"

---

## Question 3: Buffer Size Limit

**Question:** How many requests should we buffer per workspace before rejecting new ones?

**Answer:** **A) 10 requests**

**Rationale:**
- Reasonable for typical usage
- If user sends >10 prompts during a 5-second restart, something is wrong
- Prevents memory exhaustion attacks

**Requirements:**
- ✅ Must add Prometheus metrics for monitoring:
  - `workspace_request_buffer_size{workspace_id}` - Gauge (current size)
  - `workspace_request_buffer_full_total{workspace_id}` - Counter (rejections due to full buffer)
- Error message if buffer full: "Too many requests during restart, please try again"

---

## Question 4: No Forced Timeout for Session Idle Wait

**Question:** Confirm that we defer restart indefinitely (no forced timeout after N minutes)?

**Answer:** **YES - Confirmed**

**Implications:**
- Secret changes might not apply for HOURS if sessions stay busy (multi-hour agentic workflows)
- User can trigger restart manually via:
  - **UI button:** "Restart Now" button in notification banner
  - **API call:** `POST /api/v1/workspaces/:id/agent/reload?drain=true`
- Restart applies automatically when all sessions become idle naturally

**UI Notification Text:**
```
⚠️ Configuration update pending - will apply when sessions are idle
[Restart Now] (will halt active sessions)
```

**Existing API Endpoint:**
- `POST /api/v1/workspaces/:id/agent/reload` - Trigger reload manually
- Query param `?drain=true` - Wait for sessions to become idle (optional)
- Query param `?drainTimeoutSeconds=300` - Timeout for drain mode (default 300s)

**Epic 44 Behavior:**
- When secret bindings change: workspace marked as "needs rematerialization"
- agentd polls sessions every 5s
- When all sessions idle: agentd rematerializes from DB + restarts opencode
- User can click "Restart Now" → calls `POST /agent/reload?drain=false` (immediate restart, halts sessions)
- User can call API directly: `POST /agent/reload?drain=true` (graceful drain, waits for idle)

---

## Question 5: UI Notification Design

**Question:** How should the "pending restart" banner be designed?

**Answer:** **B) Banner with "Restart Now" button**

**Design:**
- Banner (not modal - non-blocking)
- No dismiss button (important notification shouldn't be hideable)
- Single action button to force restart

**UI Mockup:**
```
┌──────────────────────────────────────────────────────────────┐
│ ⚠️ Configuration update pending - will apply when sessions   │
│    are idle                                                   │
│                                              [Restart Now] ←  │
│                           (will halt active sessions)         │
└──────────────────────────────────────────────────────────────┘
```

**Button Behavior:**
- On click: `POST /api/v1/workspaces/:id/agent/reload?drain=false`
- Shows loading spinner: "Restarting workspace..."
- On success: Banner disappears
- On error: Shows error message in banner

**Banner Visibility:**
- Show when: `workspace.needsRematerialization == true`
- Hide when: `workspace.needsRematerialization == false` (after successful restart)
- Persist across page reloads (fetch workspace status on mount)

---

## Monitoring Requirements

### New Prometheus Metrics (from Questions 2 & 3)

**Request Buffering Metrics:**
```go
// Counter - Total timeouts waiting for opencode to restart
workspace_request_buffer_timeout_total{workspace_id}

// Histogram - Time spent waiting in buffer (buckets: 0.5s, 1s, 2s, 5s, 10s, 30s)
workspace_request_buffer_wait_seconds{workspace_id}

// Gauge - Current number of buffered requests
workspace_request_buffer_size{workspace_id}

// Counter - Total rejections due to full buffer
workspace_request_buffer_full_total{workspace_id}
```

**Usage:**
- Set up alerts for `workspace_request_buffer_timeout_total` - indicates frequent restarts or slow startup
- Monitor `workspace_request_buffer_wait_seconds` P95/P99 - should be <5s under normal conditions
- Alert if `workspace_request_buffer_full_total` > 0 - indicates user spamming requests or buffer too small

---

## Updated Epic 44 Story List

### New Story: US-44.10 - API Proxy Request Buffering

**Problem:** Users see "server busy" errors when sending prompts during opencode restart  
**Solution:** Buffer `POST /messages` requests in API proxy during restart, forward when healthy  

**Acceptance Criteria:**
- [ ] Proxy detects opencode unhealthy (502/503 from health check)
- [ ] Buffers only `POST /messages` requests (other requests return 502 immediately)
- [ ] Buffer limit: 10 requests per workspace
- [ ] Buffer timeout: 30 seconds
- [ ] Holds HTTP connection open (no 502 response to client)
- [ ] When opencode healthy: forwards buffered requests FIFO
- [ ] Prometheus metrics: `buffer_timeout_total`, `buffer_wait_seconds`, `buffer_size`, `buffer_full_total`
- [ ] Clear error messages on timeout/full buffer
- [ ] Test: simulate restart, verify requests forwarded without client seeing error

**Estimate:** 3 days

**Files:**
- `api/internal/handlers/proxy.go` - Add buffering logic
- `api/internal/metrics/metrics.go` - Add new metrics

---

## Next Steps

1. ✅ All 5 questions answered
2. ⏭️ Update Epic 44 README.md with:
   - Corrected architecture (DB-backed rematerialization, no secret buffer)
   - Add US-44.10 (Request Buffering) with 3-day estimate
   - Add monitoring requirements to US-44.8
   - Update total estimate to ~23 days (was 14.5)
   - Add UI notification mockup
   - Add API reload mechanism documentation
3. ⏭️ Present final design for approval
4. ⏭️ Begin Phase 1 implementation

---

## Summary of Design Changes from Initial Draft

| Item | Initial Design | Corrected Design |
|------|---------------|------------------|
| Secret buffering | In-memory buffer in agentd | NO buffer - DB is source of truth, idempotent rematerialization |
| Request buffering | Not included | NEW US-44.10 - buffer POST /messages in proxy |
| Memory threshold | 75% | 85% |
| Ops monitoring | Phase 3 (optional) | Phase 1 (P0, first-class citizen) |
| Restart timeout | 30 minutes forced | No timeout - defer indefinitely, user can trigger via button/API |
| api-key handling | Fix restart bug | Fix bug + aggressive deprecation story (US-44.9) |
| Buffer scope | N/A | Only POST /messages (not all requests) |
| Buffer timeout | N/A | 30 seconds with Prometheus monitoring |
| Buffer size | N/A | 10 requests per workspace with monitoring |
| UI notification | Not specified | Banner with "Restart Now" button, no dismiss |

**Total stories:** 11 (was 8)  
**Total estimate:** ~23 days (was 14.5 days)  
**Phase 1 (P0):** 11.5 days (includes monitoring now)
