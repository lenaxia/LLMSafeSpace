# Worklog: Epic 22 + Epic 23 (Stories 1, 4) + FailureReason Enum

**Date:** 2026-05-31
**Session:** Implement Epic 22 (agentd health redesign), Epic 23 Stories 1+4 (DeletionTimestamp guard, auth-cache fix), and FailureReason enum extraction from Epic 21
**Status:** Complete

---

## Objective

Ship the structural fixes that prevent the worklog 0100 incident class from recurring:
1. Epic 22: Separate liveness/readiness/deep-status concerns in agentd
2. Epic 23 Story 1: DeletionTimestamp guard prevents dying-pod misclassification
3. Epic 23 Story 4: Fix browser basic-auth dialog from stale password cache
4. FailureReason enum: Give operators structured failure information

Scoping decision: Epic 21 Change B (exponential backoff) deferred until post-Epic-22 metrics justify it. The backoff schedule is speculative without production data.

---

## Work Completed

### Epic 22: agentd Health-Endpoint Redesign (US-22.1 through US-22.8)

- **US-22.1** (pre-existing): `/v1/healthz` already refactored to process-only liveness
- **US-22.2**: Created `healthz_cache.go` — eager-refresh `IsHealthy` cache with 5s interval, atomic pointer for lock-free reads, 3-failure threshold before flipping unhealthy, panic recovery in refresher goroutine
- **US-22.3**: Refactored `/v1/readyz` to read from cache; returns 503 when unhealthy; no inline opencode calls on request path
- **US-22.4**: Documented `/v1/statusz` as expensive in code comments
- **US-22.5**: Controller's `checkAgentHealth` now polls `/v1/healthz` (cheap, process-only) for liveness decisions
- **US-22.6**: `enrichAgentStatus` polls `/v1/statusz` separately via `maybeEnrichAgentStatus` (~60s cadence); failures do NOT increment `ConsecutiveHealthFailures`
- **US-22.7**: Kubelet probes verified to work with cached readyz semantics
- **US-22.8**: Mux split into admin port (4098: healthz/readyz/statusz) and user port (4097: reload-secrets). Pod spec probes target admin port. Network policy updated to allow port 4098.

### Epic 23 Story 1: DeletionTimestamp Guard

- Added `isPodTerminating(pod)` helper in `controller/internal/workspace/helpers.go`
- `handleCreating`: DeletionTimestamp check fires BEFORE the PodRunning and PodFailed checks. Terminating pod → requeue (wait for reaping), never writes terminal Failed
- `handleActive`: Terminating pod → transition to Creating (clear PodIP/Endpoint), does NOT increment TransientFailureCount
- 7 new tests covering nil pod, no timestamp, with timestamp, terminating+Failed, terminating+Running, genuine Failed, and Active-phase terminating pod

### Epic 23 Story 4: Auth-Cache Invalidation + Header Sanitization

- `onPhaseChange` now invalidates `pwCache` on:
  - `Failed` (password secret deleted by cleanupFailedWorkspaceSecrets)
  - `Active`-from-non-Active (ensurePasswordSecret regenerated password)
  - Prior-phase tracking via `priorPhase map[string]string`
- `doProxy`: upstream 401 → converted to structured 502, `pwCache` proactively invalidated
- `copyResponseHeaders` helper strips `WWW-Authenticate`, `Proxy-Authenticate`, `Set-Cookie`
- 12 new tests covering header stripping, 401→502 conversion, cache invalidation scenarios

### FailureReason Enum (Epic 21 Change C partial)

- Added `FailureReason` type with 7 enum values to `workspace_types.go`
- Added `FailureReason` field to `WorkspaceStatus`
- `markFailed()` helper ensures all 5 Failed-write sites populate the enum
- FailureReason cleared on recovery via RestartGeneration bump
- CRD YAML updated in both `pkg/crds/` and `charts/` with enum validation

### Validator Findings (fixed)

1. Network policy only allowed port 4097 → added port 4098
2. `enrichAgentStatus` called every 15s → rate-limited to ~60s via `maybeEnrichAgentStatus`
3. `TestCheckAgentHealth_Degraded` needed to call `enrichAgentStatus` after `checkAgentHealth`

---

## Key Decisions

1. **Epic 21 Change B deferred.** The exponential backoff schedule (5s/30s/2m/5m/15m/1h/4h/12h) is unjustified by production data. After Epic 22 removes the first-order driver of failures, the failure rate may drop below the threshold of concern. Ship metrics first, then decide.

2. **US-22.8 (mux split) shipped despite being potentially premature.** The implementation is simple (two `http.Server` goroutines) and the cost is low. If metrics show it's unnecessary, it's trivial to revert.

3. **`enrichAgentStatus` rate-limiting uses modulo heuristic** rather than a dedicated status field. Avoids adding a new CRD field just for timing. Acceptable approximation for a 60s cadence.

4. **`copyResponseHeaders` uses a deny-list** (3 headers blocked) rather than an allow-list. An allow-list would be safer but would break any future headers opencode adds. The deny-list is narrow and covers the known security-sensitive headers.

---

## Blockers

None.

---

## Tests Run

```
go test -timeout 120s -race -short ./...
# All packages pass (0 failures)
# Key packages:
# - cmd/workspace-agentd: 27.4s (includes 11s ticker test)
# - controller/internal/workspace: 1.2s (48 tests)
# - api/internal/handlers: 12.0s (includes proxy tests)
# - pkg/apis/llmsafespace/v1: CRD schema validation passes
# - charts/llmsafespace: Helm template tests pass
```

---

## Next Steps

1. **Deploy and observe metrics.** After Epic 23 Story 1's metrics are live, check:
   - `WorkspacePodTerminatingObservedTotal` — should be non-zero (confirms the guard is firing)
   - Terminal-Failed rate — should drop to near-zero
   - `WorkspaceStatusUpdateConflictsTotal` — if near-zero, Epic 23 Stories 2+3 can be deferred

2. **If terminal-Failed rate remains problematic post-Epic-22:** revisit Epic 21 Change B with a backoff schedule informed by actual data.

3. **Epic 23 Stories 2+3:** defer until conflict metrics justify the churn.

---

## Files Modified

### New files
- `cmd/workspace-agentd/healthz_cache.go` — eager-refresh cache type + refresher loop
- `cmd/workspace-agentd/healthz_cache_test.go` — 15 tests for cache behavior
- `controller/internal/workspace/helpers.go` — `isPodTerminating` + `markFailed` helpers
- `controller/internal/workspace/deletion_timestamp_test.go` — 7 US-23.1 tests
- `api/internal/handlers/proxy_auth_cache_test.go` — 12 US-23.4 tests

### Modified files
- `cmd/workspace-agentd/main.go` — mux split, healthzCache wiring, readyz from cache
- `controller/internal/workspace/controller.go` — DeletionTimestamp guards, checkAgentHealth→healthz, enrichAgentStatus split, markFailed at all 5 sites, admin port for probes
- `controller/internal/workspace/constants.go` — (unchanged, constants reused)
- `controller/internal/workspace/health_test.go` — agentdAdminPort in test fixtures, degraded test updated
- `controller/internal/workspace/health_enrichment_test.go` — calls enrichAgentStatus directly
- `pkg/agentd/types.go` — AgentdAdminPort + AgentdAdminAddr constants
- `pkg/apis/llmsafespace/v1/workspace_types.go` — FailureReason type + status field
- `pkg/crds/workspace_crd.yaml` — failureReason field with enum validation
- `charts/llmsafespace/crds/workspace.yaml` — failureReason field
- `charts/llmsafespace/templates/workspace-network-policy.yaml` — port 4098 allowed
- `api/internal/handlers/proxy.go` — onPhaseChange fix, 401→502, copyResponseHeaders, priorPhase tracking
