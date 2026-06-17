# Epic 44 README.md Update Complete

**Status:** ✅ All updates applied  
**Date:** 2026-06-16  
**Version:** 2.0 (up from 1.0)

---

## Changes Applied to README.md

### 1. Solution Section ✅
- Added 8 key solution points (was 5)
- Added architecture decision note about unavoidable restarts
- Clarified request buffering (30s timeout, 10 request limit)
- Changed memory threshold to 85% (was 75%)
- Added "defer indefinitely" clarification

### 2. Architecture Section ✅
- Completely rewrote "New Behavior" flow
- Added DB-backed rematerialization details
- Removed in-memory secret buffering references
- Added request buffering flow (during restart)
- Added 4 critical design notes explaining the architecture

### 3. Stories Section ✅

**Modified Stories:**
- **US-44.2:** Removed forced timeout, added DB-backed rematerialization, added design notes
- **US-44.5:** Changed threshold from 75% to 85%, added "cannot modify opencode" note

**Moved Stories:**
- **US-44.8:** Moved from P2 to P0 (ops monitoring is first-class citizen)

**New Stories Added:**
- **US-44.9:** Aggressive api-key deprecation (3 days)
- **US-44.10:** API proxy request buffering (3 days) 
- **US-44.11:** Admin session recovery tools (3 days)

**Total Stories:** 11 (was 8)

### 4. Configuration Section ✅
- Changed `MEMORY_WARNING_THRESHOLD` from 0.75 to 0.85
- Added `REQUEST_BUFFER_SIZE_PER_WORKSPACE=10`
- Added `REQUEST_BUFFER_TIMEOUT_SECONDS=30`
- **Removed** `RESTART_MAX_DEFER_MINUTES` (no forced timeout)

### 5. Implementation Order Section ✅
- Split US-44.1 into 44.1a and 44.1b (proxy + frontend)
- Added US-44.8 to Phase 1 (was in Phase 3)
- Added US-44.9, US-44.10, US-44.11
- Updated estimates:
  - Phase 1: 13 days (was 8.5 days)
  - Phase 2: 4 days (unchanged)
  - Phase 3: 6 days (was 2 days)
  - **Grand Total: 23 days (was 14.5 days)**

### 6. Success Metrics Section ✅
- Removed "Forced restarts after 30min" metric (no forced timeout)
- Added "User-triggered restarts <10%" metric
- Added "Request buffer timeout rate <0.1%" metric
- Added "Request buffer wait time P95 <5s" metric
- Changed "OOM detected >0" to ">90% of actual OOM kills"

### 7. Integration Tests Section ✅
- Removed "Buffered secret changes merge correctly" test
- Added "DB-backed rematerialization" test
- Added "Request buffering during restart" tests

### 8. Open Questions Section ✅
- Marked all questions as ANSWERED ✅
- Added summary of all 5 answers
- Added references to existing API endpoints
- Updated Epic version to 2.0
- Changed next steps to "Begin Phase 1 implementation"

### 9. Appendix: api-key Status ✅
- Updated to reflect "no current users"
- Added deprecation timeline reference (US-44.9)

---

## Key Architectural Changes Documented

### 1. Process Restarts Are Unavoidable
- Node.js `process.env` is immutable (tested and confirmed)
- Cannot modify opencode (constraint)
- Focus on transparency, not elimination

### 2. DB-Backed Rematerialization
- **OLD (WRONG):** Buffer secrets in-memory in agentd
- **NEW (CORRECT):** Write to PostgreSQL immediately, re-fetch on restart
- Idempotent, handles multiple updates automatically

### 3. Request Buffering Added
- New story US-44.10 (3 days)
- Buffer only `POST /messages` in API proxy
- 30s timeout, 10 request limit
- Transparent user experience

### 4. No Forced Timeout
- **OLD:** Force restart after 30 minutes
- **NEW:** Defer indefinitely, user triggers manually
- Rationale: Multi-hour agentic workflows are common

### 5. Ops Monitoring is P0
- **OLD:** Phase 3 (optional)
- **NEW:** Phase 1 (critical path)
- Prometheus metrics are first-class requirement

---

## Epic 44 Final Stats

| Metric | Before | After | Change |
|--------|--------|-------|--------|
| Total Stories | 8 | 11 | +3 |
| Total Estimate | 14.5 days | 23 days | +8.5 days |
| Phase 1 (P0) | 8.5 days | 13 days | +4.5 days |
| Phase 2 (P1) | 4 days | 4 days | No change |
| Phase 3 (P2) | 2 days | 6 days | +4 days |

---

## Documentation Created During Investigation

1. ✅ `INCIDENT-ANALYSIS.md` (546 lines) - Technical investigation with evidence
2. ✅ `DESIGN-VALIDATION.md` (520 lines) - All 7 assumptions validated
3. ✅ `SUMMARY.md` (207 lines) - Executive summary
4. ✅ `VALIDATION-COMPLETE.md` - Validation report
5. ✅ `QUESTIONS-ANSWERED.md` - User's 5 answers documented
6. ✅ `DESIGN-FINAL.md` - Complete design summary (this session)
7. ✅ `STUCK-SESSIONS-RECOVERY.md` - Recovery guide for current issues
8. ✅ `README.md` - **Updated with all corrections (v2.0)**

---

## Ready for Implementation

**Phase 1 (P0) - 13 days:**
- US-44.1a/b: Terminal SSE events
- US-44.2: Session-aware restart (NO forced timeout, DB-backed)
- US-44.3: Fix api-key bug
- US-44.4: OOM detection + metrics
- US-44.8: Ops monitoring (Prometheus)
- US-44.10: Request buffering

**Phase 2 (P1) - 4 days:**
- US-44.5: Memory warnings (85% threshold, cannot implement)
- US-44.6: Per-session memory (cannot implement)
- US-44.7: Restart reason logging

**Phase 3 (P2) - 6 days:**
- US-44.9: api-key deprecation
- US-44.11: Admin recovery tools

---

## User Requirements Met ✅

1. ✅ **Ops monitoring is P0** - Moved US-44.8 to Phase 1
2. ✅ **NO forced timeout** - Removed from US-44.2, defer indefinitely
3. ✅ **85% memory threshold** - Updated US-44.5 and config
4. ✅ **Request buffering NOT secret buffering** - Clarified architecture, added US-44.10
5. ✅ **Aggressive api-key deprecation** - Added US-44.9 with 6-month sunset
6. ✅ **UI notification with "Restart Now" button** - Documented in US-44.2
7. ✅ **Multi-hour workflows supported** - No forced timeout, noted as common case

---

**Next Step:** Begin Phase 1 implementation (US-44.1a → US-44.10)
