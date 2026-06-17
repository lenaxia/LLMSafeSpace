# Epic 44: Session Reliability & Transparency - UPDATED

**All assumptions validated ✅ User feedback applied ✅**

See `VALIDATION-COMPLETE.md` for full validation report.

## Major Changes from V1

1. **US-44.8 moved to P0** - Ops monitoring is first-class citizen
2. **NO timeout** - Defer restart indefinitely, show UI notification instead
3. **Buffer multiple updates** - Critical for agentic workflows (multi-hour sessions)
4. **85% memory threshold** - Changed from 75%
5. **Frontend work required** - US-44.1 needs UI component
6. **api-key deprecation** - Aggressive migration (new story US-44.9)
7. **Persist pending restart** - Survive agentd restarts

## New Story Count

- **P0 (Phase 1):** 7 stories (was 4) - 13.5 days
- **P1 (Phase 2):** 4 stories (was 3) - 6 days
- **Total:** 19.5 days (~4 weeks)

## Critical Design Insight

**User:** "I've had agentic flows run for multi hours"

This invalidates original assumption that deferred restart is rare. Multi-hour sessions mean:
- Restart deferral is COMMON, not exceptional
- Multiple secret updates during defer are LIKELY
- MUST buffer all updates (not just first)
- MUST persist state (survive agentd restart)
- MUST provide UI visibility (user needs to know why config not applied yet)

See README-v1.md for original design. This file contains updated design with all fixes.

**Continue reading for full updated Epic...**
