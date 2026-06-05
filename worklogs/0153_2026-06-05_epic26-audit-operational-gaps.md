# Worklog: Epic 26 Audit — Identified Operational Gaps

**Date:** 2026-06-05
**Session:** Code audit of all Epic 26 files on main post-merge
**Status:** Complete (findings documented, story created)

---

## Objective

Critically audit the merged Epic 26 implementation for weak points, gaps, and failure modes that would prevent the relay from functioning in production.

---

## Findings

### Critical

**1. `isFreeTierModel` and `pushRelayBaseURL` don't use Basic Auth**

- `models.go:450` — `isFreeTierModel` calls `GET /api/model` without `SetBasicAuth`
- `models.go:480` — `pushRelayBaseURL` calls `PUT /auth/opencode` without `SetBasicAuth`
- Compare with `modelExistsInCatalog` (line 393) which correctly uses basic auth
- Per worklog 0127: opencode v1.15.12 requires basic auth on ALL endpoints
- Impact: Both calls will 401 → relay baseURL never pushed → relay never activates via model selection

### High

**2. No relay activation on workspace boot**

- Workspace boots with `OPENCODE_AUTH_CONTENT={"opencode":{"type":"api","key":"public"}}` (free tier default)
- opencode uses this at boot → free models available immediately
- But `pushRelayBaseURL` is only called from `SetModel` handler
- Fresh workspace with free tier active sends requests directly to opencode.ai, bypassing relay
- Relay is structurally active (env vars injected, agentd connected) but opencode doesn't know to route to it

**3. `useRelayClient` hook never integrated into the UI**

- `frontend/src/hooks/useRelayClient.ts` exists but zero imports in any page/component
- Without a connected browser client, all relay proxy requests time out (5s) → 504 to opencode
- The relay system is end-to-end non-functional for browser users

### Medium (documented but not in immediate story)

- `patchAgentModel` also missing basic auth (same pattern)
- No error propagation when relay client disconnects mid-stream
- No per-minute rate limiting (only concurrency cap of 60)
- Mutable `AllowedProxyHosts` slice (test race potential)

### Low

- Trailing slash in APIServiceURL produces double-slash URL
- No Prometheus metrics in relay handler
- `useRelayClient` reconnect loop not phase-aware

---

## Key Decisions

These three issues (#1, #2, #3) are the minimum set needed to make the relay system actually functional in production. They form a single follow-up story.

---

## Blockers

None — all fixes are straightforward and don't require architectural changes.

---

## Tests Run

No tests run in this session (audit only).

---

## Next Steps

Implement US-26.7 (created in `design/stories/epic-26-client-proxied-inference/`).

---

## Files Modified

- `worklogs/0153_2026-06-05_epic26-audit-operational-gaps.md` (this file)
- `design/stories/epic-26-client-proxied-inference/README.md` (new story appended)
