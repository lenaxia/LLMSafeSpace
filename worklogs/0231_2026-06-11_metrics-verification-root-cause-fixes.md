# Worklog 0231 — Metrics Verification: Root Cause Analysis and Fixes

**Date:** 2026-06-11
**Session:** Scrape /metrics endpoint, identify all gaps, fix root causes
**Status:** Complete

---

## Objective

User reported billing/inference metrics were empty despite sending inference messages.
Trace every missing metric to its root cause using the actual codebase and live pod
observation — no assumptions accepted without verification against source.

---

## Work Completed

### Methodology

All diagnoses were validated by:
1. Scraping both `/metrics` endpoints directly
2. Port-forwarding to live workspace pod and capturing the raw opencode SSE stream
3. Reading the actual Go source for every call site
4. Cross-checking structs against live wire-format JSON

### Root cause 1 — `handleSessionUpdated` wrong field path (CRITICAL)

**Bug:** `handleSessionUpdated` in `session_tracker.go` parsed `properties.{id, model, tokens, cost}` but opencode 1.15.12 actual wire format nests these under `properties.info.{id, model, tokens, cost}`. The guard `p.ID == ""` was always true. `onInference` callback never fired. All inference metrics were permanently zero.

**Validated against live pod:** Port-forwarded to workspace pod `1aa87aec`, subscribed to `/event` stream, captured 78 SSE lines during an actual inference. Confirmed real structure:
```json
{"type":"session.updated","properties":{
  "sessionID":"ses_...",
  "info":{"id":"ses_...","cost":0,"tokens":{"input":509911,"output":20861,...},"model":{"id":"glm-5.1","providerID":"thekao cloud"}}
}}
```

**Also confirmed:** `session.next.step.ended` does NOT appear in the global `/event` stream — it is agentd-internal only. `cost` is `0` for the `thekao cloud` provider (custom endpoint does not populate cost). This is correct behavior — the metric records what opencode reports.

**Fix:** Updated `handleSessionUpdated` struct to parse `properties.info.*`. Delta tracking now uses cumulative output only — input varies due to cache reads and is not delta-trackable across events.

**Tests:** 6 new TDD tests in `session_tracker_test.go` with real wire-format fixtures.

### Root cause 2 — `auth_failures_total` missing login failures

**Bug:** `Login()` in `auth.go` called `recordFailedAttempt()` for wrong password / user not found / inactive account but never called `metrics.RecordAuthFailure()`. The metric was only wired for invalid bearer token requests (middleware path), not username/password login failures.

**Fix:** Added `metrics.RecordAuthFailure("wrong_password")`, `("user_not_found")`, `("account_inactive")` at the three failure return paths in `Login()`.

**Note:** Metric cannot be verified via port-forward — `RequireHTTPS` middleware blocks plain HTTP login requests. Will populate from actual browser traffic.

**Tests:** 3 new tests in `auth_test.go`.

### Root cause 3 — `workspace_phase_transitions_total` unwired

**Bug:** `handleEvent` in `crd_watcher.go` detected phase changes (`existed && oldPhase != newPhase`) but never called `RecordWorkspacePhaseTransition()`. The function was defined, zero call sites.

**Fix:** Added `metrics.RecordWorkspacePhaseTransition(oldPhase, newPhase)` at the transition point in `handleEvent`.

**Tests:** 2 new tests in `crd_watcher_test.go`.

### Root cause 4 — `agent_reload_total` only fires on success

**Bug:** `Reload()` handler called `RecordAgentReload("success", ...)` only at the success path. Every early return (pod unreachable, agentd 503, DB failure, drain timeout) recorded nothing.

**Fix:** Replaced point-of-success call with `defer`+`succeeded` flag pattern. Every return path now records `result="success"` or `result="error"`.

### Root cause 5 — `relay_injector_total` duplicate across processes

**Finding:** `relayInjectorTotal` in `api/internal/services/metrics/metrics.go` is a duplicate of `relayInjectorOutcomes` in `cmd/workspace-agentd/relay_injector.go`. These live in different processes with different Prometheus registries. The API-side copy can never observe anything from agentd.

**Fix:** Removed `relayInjectorTotal` and `RecordRelayInjector` from the API metrics package.

### Previously confirmed working (zero = correct behavior)

The following metrics were confirmed wired correctly — they just need events to occur:
- `inference_requests_total`, `inference_cost_dollars_total`, `model_selections_total`, `session_duration_seconds` — require actual LLM inference completing
- `workspaces_created_total`, `workspace_create_duration_seconds` — require workspace creation
- `workspace_recovery_*` — require recovery state machine to fire
- `auth_failures_total` (bearer token path) — requires invalid token on protected routes

---

## Deployments

| Tag | Contents |
|-----|----------|
| `ts-1781210644` | Controller: 9 metric call sites wired (reconcile duration, deletions, recovery, active seconds, storage), 4 dead metrics removed |
| `ts-1781211486` | Controller: WorkspaceRecoverySuccessTotal + WorkspacesFailedTotal wired |
| `ts-1781212420` | API: inference parsing fixed, auth failure metrics, phase transitions, agent reload error path |

---

## Tests Run

```
go test -timeout 60s -race ./controller/...     → all pass
go test -timeout 60s -race ./api/internal/handlers/... → all pass (including 6 new inference tests)
go test -timeout 30s -race ./api/internal/services/metrics/... → pass
go test -timeout 180s -race ./api/internal/services/auth/ → pass (140s due to bcrypt E2E tests)
```

---

## Next Steps

Send inference messages from the browser after the new pod has received actual traffic.
The inference metrics (`inference_requests_total`, `inference_input_tokens_total`,
`inference_output_tokens_total`) should populate on the next `session.updated` event with
`info.tokens.output > 0`. Grafana billing dashboard will show data once observations
accumulate.

---

## Files Modified

- `api/internal/handlers/session_tracker.go` — fix handleSessionUpdated field path
- `api/internal/handlers/session_tracker_test.go` — 6 new inference tests
- `api/internal/handlers/crd_watcher.go` — wire phase transition metric
- `api/internal/handlers/crd_watcher_test.go` — 2 new phase transition tests
- `api/internal/handlers/agent_reload.go` — defer-based reload metric
- `api/internal/services/auth/auth.go` — wire auth failure metrics in Login()
- `api/internal/services/auth/auth_test.go` — 3 new auth failure metric tests
- `api/internal/services/metrics/metrics.go` — remove dead relay_injector_total
- `controller/internal/workspace/metrics_wiring.go` — injectable metric helpers
- `controller/internal/workspace/metrics_wiring_test.go` — 17 new controller metric tests
- `controller/internal/workspace/reconciler.go` — reconciliation duration/errors
- `controller/internal/workspace/phase_active.go` — active seconds + storage bytes
- `controller/internal/workspace/phase_creating.go` — recovery success metric
- `controller/internal/workspace/phase_terminating.go` — workspaces deleted
- `controller/internal/workspace/recovery_policy.go` — recovery attempts/backoff/safe mode
- `controller/internal/metrics/metrics.go` — remove 5 dead metrics (LLM/proxy/user LLM)
