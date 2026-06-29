# Worklog: Comprehensive e2e tests for #422 + #416

**Date:** 2026-06-28
**Session:** Add comprehensive e2e test coverage for the two recently-merged features (managed_process data race fix #422, agent customization #416)
**Status:** Complete

---

## Objective

Identify and fill test coverage gaps for #422 (managed_process data race fix) and #416 (agent customization feature). Both PRs merged with AI-review approval but had gaps in their test suites — the user requested comprehensive e2e coverage.

---

## Work Completed

### Audit findings

- **#422**: Existing tests covered sequential restart/stop/reap but NOT the concurrency scenarios the fix targeted. The SIGKILL fallback timer (child ignoring SIGTERM >5s) was explicitly untested (noted in the #422 review).
- **#416**: `PromptHandler` had **zero** handler-level tests. The prompt service had good unit tests for `ResolveEffective` but `resolveRoleSystemPrompt` cycle detection and depth limit were untested. Agent-role handler tests existed but only covered workspace-role selection, not prompt CRUD.

### #422 — `managed_process_concurrency_test.go` (new file)

- `TestManagedProcess_ConcurrentRestarts`: 8 goroutines call `restart()` simultaneously under `-race`
- `TestManagedProcess_ConcurrentRestartAndPIDReads`: `restart()` racing with 8 reader goroutines reading `p.cmd.Process.Pid` under the mutex
- `TestManagedProcess_RestartWithSlowChild_SIGKILLFallback`: child uses `IGNORE_SIGTERM=1` (discards SIGTERM in a loop; only SIGKILL terminates). Removing the killTimer would cause the child to run forever → test hangs. This properly discriminates the SIGKILL mechanism.
- `TestManagedProcess_StopWithSlowChild_SIGKILLFallback`: same path in `stop()`
- `TestManagedProcess_DoubleStopIsIdempotent`: 3 goroutines call `stop()` concurrently

### #416 — `prompts_test.go` (new file, 30 handler tests)

All 6 registered PromptHandler endpoints covered: `GetPlatform`, `SetPlatform`, `GetOrg`, `SetOrg`, `GetWorkspacePrompt`, `SetWorkspacePrompt`. Plus the shared `userPromptAllowedFromPolicies` helper. Coverage includes: happy paths, DB errors (500), validation failures (400, >10000 chars), policy enforcement (org-locked 403, default-locked 403, standalone workspace), audit emission failures (LogAuditEvent/LogOrgEvent errors → handler still returns 200, logger.Warn called), toggle-only and prompt-only partial updates.

### #416 — `service_test.go` (4 new tests)

`resolveRoleSystemPrompt` cycle detection (A→B→A terminates via visited map), depth limit (15-deep chain respects maxChainDepth=10, exactly 10 GetAgentRole calls), leaf-without-extends terminal, store-error abort.

### Fake child enhancement (`managed_process_test.go`)

Added `IGNORE_SIGTERM=1` mode to the TestHelperProcess fake: catches and discards SIGTERM in a loop, so only SIGKILL (uncatchable) can terminate. This lets the SIGKILL-fallback tests discriminate the mechanism — without this mode, the child exits naturally regardless of whether the killTimer fires.

---

## Key Decisions

1. **IGNORE_SIGTERM=1 vs SIGTERM_DELAY_MS=8000** — Initial implementation used 8s delay, but the first review caught that the child exits naturally at 8s regardless of whether the killTimer fires (both pass the 10s test timeout). Switched to IGNORE_SIGTERM=1 which makes the child ignore SIGTERM entirely, so removing the killTimer causes an infinite hang. This makes the test a true regression guard.

2. **Handler tests use mock store, not real Postgres** — Followed the established pattern from `agent_roles_test.go` (same package, same mock approach). The service-layer integration with the real DB is validated by the `workspace_integration_test.go` suite; handler tests focus on binding, validation, policy enforcement, and error propagation.

3. **No separate e2e/integration test for the full router path** — The existing `workspace_integration_test.go` and `router_*_test.go` files use a different fixture pattern (full gin engine with all middleware). Adding prompt routes to that pattern would require substantial fixture setup. Handler-level tests with mock store provide the same coverage of the handler logic; the router registration was verified by code inspection (router.go:416-419, 1141-1142).

---

## Tests Run

```
go test -race -count=1 ./cmd/workspace-agentd/...                    # 119s all pass
go test -race -count=1 -run "Prompt" ./api/internal/handlers/...     # 1.3s all pass
go test -race -count=1 ./api/internal/services/prompt/...            # 1.0s all pass
gofmt -l .   # clean
go vet ./... # clean
go build ./... # clean
```

---

## Files Modified

- `cmd/workspace-agentd/managed_process_concurrency_test.go` (new — 304 lines)
- `cmd/workspace-agentd/managed_process_test.go` (modified — added IGNORE_SIGTERM mode, updated mode docs)
- `api/internal/handlers/prompts_test.go` (new — 30 handler tests)
- `api/internal/services/prompt/service_test.go` (modified — 4 new resolveRoleSystemPrompt tests)
