# Worklog — Epic 8: Credential Health & Agent Abstraction

**Date:** 2026-05-26  
**Operator:** opencode  
**Start state:** main at sha-5574072 (Epic 8 design merged)  
**End state:** main at sha-375e4b7  

---

## Summary

Implemented credential health monitoring and agent runtime abstraction for LLMSafeSpace. The system now detects invalid/missing credentials and unhealthy agent processes before they cause silent failures, with self-healing capabilities.

**Stories completed:** 10 of 11 (US-8.9 SSE deferred)  
**Commits:** 3 (e2a0da6, 33862fa, 375e4b7)  
**Test packages:** 30 pass, 0 failures (Go +race)  
**Frontend tests:** 10 new (HealthBanner), 18 ChatPage pass, 1 pre-existing failure (SessionItem unrelated)

---

## Assumptions Stated and Validated

| # | Assumption | Validated How |
|---|-----------|---------------|
| A1 | `mapCredSecretToWorkspaces` prefix-matching logic is correct | Read controller.go, confirmed prefix `workspace-creds-` + workspace name |
| A2 | Fake client supports watch predicate simulation | Tests pass with fake client |
| A3 | opencode serves `/global/health`, `/provider`, `/config/providers` | Read opencode source: `groups/global.ts` (GlobalPaths.health), `groups/provider.ts` (root="/provider"), `groups/config.ts` (`${root}/providers`) |
| A4 | `pkg/agent` can import `pkg/agent/opencode` without cycle | Restructured: interface in agent/, impl in opencode/, consumer registers via `opencode.Register()` |
| A5 | Credential secret data key is `"provider-config"` | Confirmed in controller.go init container script and workspace_service.go |
| A6 | Fake client handles `Status().Update()` with status subresource | `WithStatusSubresource()` used in all test builders |

---

## Implementation Details

### US-8.0: Fix Broken Secret Watch (Blocker)

**Root cause:** `credentialSecretChanged()` stored the credential hash in-memory on first learn but `handleActive()` never called `Status().Update()` when `changed=false`. The hash was always empty on re-read, so credential changes were never detected.

**Fix:** Removed in-memory mutation from `credentialSecretChanged()`. Added `else if` branch in `handleActive()` to persist hash on first learn via `Status().Update()`.

**Files:** `controller/internal/workspace/controller.go`  
**Tests:** 6 new in `secret_watch_test.go`

### US-8.1: workspace-agentd Binary + Dockerfile

**New packages:** `pkg/agentd/types.go` (Healthz/Readyz/Statusz response types), `cmd/workspace-agentd/main.go` (daemon binary with zap structured logging)

**Daemon architecture:** Sidecar in workspace pod that proxies opencode's API endpoints into standardized health check endpoints:
- `/v1/healthz` → calls `GET :4096/global/health` → returns `{healthy, version, uptime_seconds}`
- `/v1/readyz` → calls health + `/provider` → returns `{ready, providers_connected, agent_version}`
- `/v1/statusz` → calls health + provider + config → returns full status

**Files:** `cmd/workspace-agentd/main.go`, `pkg/agentd/types.go`, `pkg/agentd/types_test.go`, `runtimes/base/Dockerfile` (agentd-builder stage), `runtimes/base/tools/entrypoints/entrypoint-opencode.sh`

### US-8.2: HTTP Probes via Daemon

Changed `buildPod()` from TCP probes on :4096 to HTTP probes on :4097:
- Readiness: `GET :4097/v1/readyz` (10s initial, 15s period, 5 failure threshold)
- Liveness: `GET :4097/v1/healthz` (15s initial, 30s period, 6 failure threshold)
- Added `agentd` container port (4097)

**Files:** `controller/internal/workspace/controller.go`

### US-8.3: Agent Runtime Interface

**New packages:**
- `pkg/agent/agent.go` — `AgentRuntime` interface + `AgentType`/`CredentialState` types + thread-safe registry (`sync.RWMutex`)
- `pkg/agent/opencode/opencode.go` — OpenCode implementation (validates JSON, checks non-empty)
- `pkg/agent/opencode/register.go` — `Register()` function for consumer-side registration

**Files:** `pkg/agent/agent.go`, `pkg/agent/opencode/opencode.go`, `pkg/agent/opencode/register.go`  
**Tests:** 11 new (registry thread safety, credential validation, format passthrough)

### US-8.4: Credential Validation on SetCredentials

Added `agent.ValidateCredentials()` and `agent.FormatCredentials()` calls before storing credential secrets. Invalid/empty JSON returns validation error to the user.

**Files:** `api/internal/services/workspace/workspace_service.go`  
**Tests:** 3 new (invalid JSON, empty config, nil config)

### US-8.5: Credential Health Conditions

Added CRD condition types and reason constants:
- `CredentialsAvailable` condition: True/False/Unknown
- Reasons: `CredentialsValid`, `CredentialSecretNotFound`, `CredentialEmpty`, `CredentialInvalid`, `CredentialCheckError`
- `checkCredentialState()` method validates via AgentRuntime on every active reconcile

**Files:** `pkg/apis/llmsafespace/v1/workspace_types.go`, `controller/internal/workspace/controller.go`

### US-8.6: Agent Health Check in Controller

Periodic HTTP health checks from controller to daemon:
- 5-minute check interval, 2-minute grace period after start
- 15-minute backoff after 3 consecutive failures
- Shared `healthHTTPClient` (package-level, 5s timeout)
- Sets `AgentHealthy` condition + `LastHealthCheckAt` + `ConsecutiveHealthFailures`
- **Repair:** When failures >= 3, deletes pod and transitions to Creating phase (restart count incremented)

**Files:** `controller/internal/workspace/controller.go`  
**Tests:** 12 new (healthy, degraded, unhealthy, connection refused, threshold repair, below threshold, grace period, backoff, success reset, empty PodIP)

### US-8.7: Init Container Fix

Removed `echo '{}' > /sandbox-cfg/credentials` else branch. When no credential secret exists, the file is simply not created.

**Files:** `controller/internal/workspace/controller.go`

### US-8.8: API CredentialState + AgentHealth

Extended `WorkspaceStatusResult` with `CredentialStateResult` and `AgentHealthResult` types. API parses controller condition messages via regex to extract connected providers, version, configured count, and last check time. Distinguishes Degraded vs Unhealthy by reason constant.

**Files:** `pkg/types/types.go`, `api/internal/services/workspace/workspace_service.go`  
**Tests:** 9 table-driven tests (5 credential states, 5 agent health states)

### US-8.10: Self-Healing Suspend on Credential Loss

When annotation `llmsafespace.dev/suspend-on-cred-loss=true` is set and `CredentialsAvailable=False`, controller transitions workspace to Suspending phase.

**Files:** `controller/internal/workspace/controller.go`  
**Tests:** 3 new (suspend on loss, no suspend without annotation, no suspend when valid)

### Frontend: Health Rendering

Created `HealthBanner` component showing credential and agent health status in ChatPage. Yellow warning banners appear for credential issues (missing/empty/invalid) and agent issues (degraded/unhealthy/unknown). Hidden when healthy.

**Files:** `frontend/src/components/chat/HealthBanner.tsx`, `frontend/src/pages/ChatPage.tsx`, `frontend/src/api/types.ts`  
**Tests:** 10 new HealthBanner tests, 18 existing ChatPage tests still pass

### Additional Fixes

- **Security:** `GenerateRandomString()` changed from timestamp-based to `crypto/rand`
- **Bug:** Stale PVCs with wrong owner UID were never deleted → controller looped forever on `AlreadyExists`. Now deletes before recreating.
- **Constants:** Annotation key, credential data key, unused `requeueSuspend` removed
- **Logging:** Daemon uses `zap` structured logging instead of `fmt.Fprintf(os.Stderr)`

---

## Validator Findings and Resolutions

### Pass 1 Findings

| # | Finding | Severity | Resolution |
|---|---------|----------|------------|
| 1 | `agentHealthFromConditions` conflated Degraded/Unhealthy | Bug | Now checks Reason to distinguish |
| 2 | Missing error-path tests for SetCredentials | Test gap | 3 tests added |
| 3 | No test for init container script | Test gap | Test added asserting no `echo '{}'` |
| 4 | No tests for credState/agentHealth helpers | Test gap | 9 table-driven tests |
| 5 | Dead `opencode.Register()` in daemon binary | Dead code | Removed |
| 6 | ConnectionRefused test used invalid PodIP | Test bug | Fixed with port override |

### Pass 2 Findings

| # | Finding | Severity | Resolution |
|---|---------|----------|------------|
| 1 | Password generation timestamp-based | HIGH | Changed to `crypto/rand` + hex |
| 2 | AgentHealth fields never populated | MEDIUM | Regex parsing from condition message + lastCheckedAt from CRD |
| 3 | HTTP client leak per health check | MEDIUM | Shared `healthHTTPClient` package var |
| 4 | Annotation key as raw string | LOW | `AnnotationSuspendOnCredLoss` constant |
| 5 | Dead `requeueSuspend` constant | LOW | Removed |
| 6 | `provider-config` as raw string | LOW | `CredentialSecretDataKey` constant |

### Pass 3 Findings (Integration Review)

| # | Finding | Severity | Resolution |
|---|---------|----------|------------|
| 1 | Daemon endpoints didn't match opencode API | False alarm | Verified in opencode source: `/global/health`, `/provider`, `/config/providers` all exist |
| 2 | Frontend types missing CredentialState/AgentHealth | Gap | Added interfaces to `types.ts` |
| 3 | Controller doesn't repair unhealthy pods | Bug | Added pod deletion + phase transition when failures >= threshold |

---

## Test Results

```
$ go test -timeout 120s -short -race ./...
30 packages, 0 failures

$ npx vitest run (frontend)
54 test files, 53 passed, 1 pre-existing failure (SessionItem.test.tsx - unrelated)
277 tests, 276 passed
```

---

## Files Changed

### New Files
- `cmd/workspace-agentd/main.go`
- `pkg/agentd/types.go`, `pkg/agentd/types_test.go`
- `pkg/agent/agent.go`, `pkg/agent/agent_test.go`
- `pkg/agent/opencode/opencode.go`, `pkg/agent/opencode/opencode_test.go`, `pkg/agent/opencode/register.go`
- `controller/internal/workspace/health_test.go`
- `controller/internal/workspace/secret_watch_test.go`
- `frontend/src/components/chat/HealthBanner.tsx`
- `frontend/src/components/chat/HealthBanner.test.tsx`

### Modified Files
- `controller/internal/workspace/controller.go` — probes, health checks, credential state, pod repair, init container
- `controller/internal/workspace/constants.go` — new constants, removed dead code
- `controller/internal/workspace/stale_pvc_test.go` — enhanced tests with PVC recreation verification
- `controller/internal/common/utils.go` — crypto/rand password generation
- `controller/internal/controller/controller.go` — opencode.Register() call
- `pkg/apis/llmsafespace/v1/workspace_types.go` — new conditions, status fields, reason constants
- `pkg/types/types.go` — CredentialStateResult, AgentHealthResult
- `api/internal/services/workspace/workspace_service.go` — validation, health parsing
- `api/internal/services/workspace/workspace_service_test.go` — new tests
- `runtimes/base/Dockerfile` — agentd-builder stage
- `runtimes/base/tools/entrypoints/entrypoint-opencode.sh` — daemon start
- `frontend/src/api/types.ts` — WorkspaceStatus extended
- `frontend/src/pages/ChatPage.tsx` — HealthBanner integration

---

## Deferred

- **US-8.9: SSE workspace.health Events** — Requires extending the SSE broker infrastructure in the API layer. Warrants its own dedicated session.

---

## Next Steps

- Deploy to cluster with `helm upgrade`
- Verify workspace-agentd sidecar starts in workspace pods
- Confirm health probes work against real opencode API
- Monitor controller health check conditions on active workspaces
- Consider US-8.9 SSE integration as follow-up
