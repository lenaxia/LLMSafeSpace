# Worklog: Small Bug Fixes — Epic 13/25 Proxy Fixes

**Date:** 2026-06-12
**Session:** Audit and fix small live bugs identified in design/stories/README.md recommended implementation order
**Status:** Complete

---

## Objective

Investigate and fix all small independent live bugs from the recommended implementation order: Epic 25 B2 (proxy truncation), Epic 25 G1 (body size limit), Epic 27a (drain injection gap), Epic 06 US-6.7 (local/test.sh), Epic 16 US-16.6 (MCP question tools), Epic 13 US-13.3 (MaxActiveSessions CRD).

---

## Work Completed

### Epic 25 B2: Proxy truncation on pod restart
**Status:** ✅ Already fixed (lines 690-718 of proxy.go). The streaming loop correctly distinguishes EOF/ErrUnexpectedEOF (normal) from other read errors (mid-stream failure), writing an SSE error event and returning an error for the latter. No changes needed.

### Epic 25 G1: io.ReadAll without LimitReader
**Status:** ✅ Fixed. `proxy_input.go:149` had `io.ReadAll(resp.Body)` without a body size limit in the `fetchFromPod` function. Added `io.LimitReader(resp.Body, 1<<20)` — caps response at 1 MiB. Other LoC already had LimitReader applied (models.go:183, proxy.go:541, 664).
- Test: `TestEpic25G1_fetchFromPodLimitReader` — verifies LimitReader caps at 1 MiB.

### Epic 27a: Drain injection gap
**Status:** ✅ Already fixed. `app.go` correctly sequences initialization: `proxyHandler.Start()` at line 434, `proxyHandler.GetSSETracker()` at line 443. Comment at line 440-441 confirms this was an intentional fix: "Wire drain mode dependencies now that proxyHandler.Start() has initialized the SSETracker."

### Epic 06 US-6.7: local/test.sh
**Status:** ✅ Already fixed. All three occurrences corrected:
- Line 222: `sandbox-pw-*` → `workspace-pw-*`
- Line 227: `-c sandbox` → `-c workspace`
- Line 236: `-c sandbox` → `-c workspace`

### Epic 16 US-16.6: MCP question tools
**Status:** ✅ Already fixed. `session_question_reply`, `session_question_reject`, and `session_permission_reply` tools are registered at `pkg/mcp/server.go:34-36`. Integration test at `pkg/mcp/integration_test.go:70-72` verifies all three tool names are present.

### Epic 13 US-13.3: MaxActiveSessions CRD
**Status (main fix):** ✅ Already fixed. `applyWorkspaceDefaults` at `workspace_service.go:723-730` reads `workspace.defaultMaxActiveSessions` from instance settings and sets `crd.Spec.MaxActiveSessions`.

**Status (SSE path):** ✅ Fixed. The `onSessionActive` SSE callback at `proxy.go:1259` read `cfg.maxActiveSessions` from wsConfig, but `shouldAutoApprovePermissions` at `proxy.go:1424-1428` only populated `autoApprovePermissions` — never `maxActiveSessions`. Added `cfg.maxActiveSessions = int(workspace.Spec.MaxActiveSessions)` to the wsConfig population code.
- Test: `TestEpic13_wsConfigPopulatesMaxActiveSessions` — verifies maxActiveSessions is set and used.

---

## Key Decisions

1. **G1 LimitReader size: 1 MiB.** Question/permission lists from the agent pod are always small (<1KB). 1 MiB is generous for edge cases while preventing OOM from a misbehaving pod.
2. **SSE path fix: populate from CRD, not use default.** Rather than using the hardcoded `defaultMaxActiveSessions` (5) in the SSE path, we now read the workspace CRD's `MaxActiveSessions` field (which is already populated by `applyWorkspaceDefaults` from the admin setting). This makes the SSE path consistent with the main proxy request path.

---

## Blockers

None.

---

## Tests Run

```bash
cd api && GOTMPDIR=/workspace/tmp go test -timeout 60s -short ./api/internal/handlers/ -count=1
# ok   handlers    21.179s  (all pass)

cd api && GOTMPDIR=/workspace/tmp go vet ./api/...
# (clean)
```

---

## Next Steps

- **Epic 30 — Unified Credential Model**: Next critical path item per design/stories/README.md. Update Epic 17 threat model before US-30.1.
- Or continue with remaining small fixes from the recommended list if any remain unfixed.

---

## Files Modified

| File | Change |
|---|---|
| `api/internal/handlers/proxy_input.go:149` | Add `io.LimitReader(resp.Body, 1<<20)` to `fetchFromPod` |
| `api/internal/handlers/proxy.go:1427` | Set `cfg.maxActiveSessions` from workspace CRD in `shouldAutoApprovePermissions` |
| `api/internal/handlers/proxy_input_test.go` | Add `TestEpic25G1_fetchFromPodLimitReader` and `TestEpic13_wsConfigPopulatesMaxActiveSessions` |
