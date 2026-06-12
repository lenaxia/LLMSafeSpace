# Worklog 0233 — Epic 33 Observability: Metric Wiring, Inference Parsing Fix, Grafana Datasource

**Date:** 2026-06-11 / 2026-06-12
**Session:** Deploy monitoring helm upgrade, diagnose and fix all zero metrics, fix Grafana dashboards
**Status:** Complete — PR #118 open, addressing review findings

---

## Objective

The Epic 33 observability stack (Helm monitoring PR #108) was deployed but all
Prometheus metrics were reporting zero. Diagnose every root cause from first principles,
validate all findings against live cluster and source code, fix, deploy, and verify.

---

## Work Completed

### Phase 1 — Helm upgrade and deploy

Picked up an interrupted helm upgrade. Issues encountered and resolved:
- `make helm-deploy` defaulted to `RELEASE_NS=llmsafespace`; actual release is in `default` → `RELEASE_NS=default`
- `IMAGE_TAG` not passed → pods pulled chart default `0.1.0` → `ImagePullBackOff` → fixed
- `values.local.yaml` had lost monitoring, webhooks, inferenceRelayURL settings → restored from `helm get values --revision 213`
- CI ts-manifest-merge failed for `ce1650e4` → deployed `sha-ce1650e` (verified == `dev` tag via manifest digest)

### Phase 2 — Controller metric call sites

Registered metrics with zero call sites anywhere in the codebase:

| Fix | File |
|-----|------|
| `reconciliation_duration_seconds` + `reconciliation_errors_total` | `reconciler.go` — wrap `Reconcile()` with timing |
| `workspaces_deleted_total` | `phase_terminating.go` |
| `workspace_recovery_attempts_total`, `recovery_backoff_duration_seconds`, `safe_mode_active` | `recovery_policy.go` after status update |
| `workspace_active_seconds_total`, `user_active_seconds_total` | `phase_active.go` per reconcile tick |
| `workspace_storage_bytes` | `phase_active.go` idempotent gauge set |
| `workspace_recovery_success_total` | `phase_creating.go` when ConsecutiveFailures>0 on Creating→Active |
| `workspaces_failed_total` | `metrics_wiring.go` when SafeMode entered |

Removed 5 dead metrics with zero call sites and no viable instrumentation path:
`WorkspaceLLMRequestsTotal`, `WorkspaceLLMRequestDurationSeconds`, `WorkspaceProxyBytesTotal`,
`UserLLMCallsTotal` (all require events-gateway, Epic 33), `relayInjectorTotal` (agentd-only metric).

New injectable helpers in `metrics_wiring.go` + 17 TDD tests in `metrics_wiring_test.go`.

### Phase 3 — API metric call sites

4 missing call sites identified via source read:
1. **`session_tracker.go`** — `handleSessionUpdated` parsed `properties.{id,model,tokens}` but
   opencode 1.15.12 wire format nests these under `properties.info.{...}`. Validated by connecting
   directly to live pod (`1aa87aec`) and capturing `/event` SSE stream. Fix: corrected struct.
   Delta tracking changed from `(input+output)` total to `output` only.
2. **`auth.go`** — `Login()` called `recordFailedAttempt()` but never `RecordAuthFailure()`.
3. **`crd_watcher.go`** — `handleEvent` detected phase changes but never called `RecordWorkspacePhaseTransition()`.
4. **`agent_reload.go`** — `RecordAgentReload()` only on success path. Fixed with defer+flag.

Removed `relayInjectorTotal` from API metrics (duplicate of agentd metric, different process).

### Phase 4 — Inference wiring decoupling (root cause of inference metrics being zero)

`SetOnInference` was nested inside `if a.agentReloadHandler != nil { ... }`. If the workspace
service type assertion failed, the entire block was skipped and `t.onInference` stayed nil.
Verified by reading `app.go` wiring code — the guard had no relation to inference.
Fix: moved `SetOnInference`/`SetSessionMetrics` into a standalone block gated only on
`GetSSETracker() != nil`.

### Phase 5 — Grafana datasource fix

Both billing and operational dashboards had `"current": {}` for the `$datasource` template
variable. All panel queries use `uid: "${datasource}"` — with the variable unset in Grafana 13,
the UID is unexpanded and queries silently return no data. Fixed by setting `current.value = "default"`.

Verified: queries return correct data through Grafana datasource proxy API.

### Phase 6 — Dashboard descriptions

Added per-panel descriptions to all 26 billing and 32 operational dashboard panels.
Section rows: purpose and when to use. Stat panels: metric source, caveats, target values.
Timeseries panels: legend interpretation, normal/abnormal ranges, cross-panel correlation.

### Phase 7 — PR #118 review findings addressed

1. **Cost double-counting**: `p.Info.Cost` is cumulative from opencode but was passed directly
   to `.Add()` on a Counter. For paid providers with `cost > 0` this inflates the metric.
   Fix: added `sessionCostSeen map[string]float64` alongside `sessionTokenSeen`, emits delta.

2. **Missing worklog**: this file.

3. **Test gaps**: added cost delta tests (2), agent reload defer error-path tests (2), and
   `TestReconcile_Creating_WithPriorFailures_RecoverySuccessMetricFires`.

---

## Deployments

| Tag | Contents |
|-----|----------|
| `sha-ce1650e` | Controller: seed WorkspacesRunning gauge at startup |
| `ts-1781210644` | Controller: 9 metric call sites wired, 5 dead metrics removed |
| `ts-1781211486` | Controller: WorkspaceRecoverySuccessTotal + WorkspacesFailedTotal |
| `ts-1781212420` | API: inference parsing fix, auth failures, phase transitions, agent reload defer |
| `ts-1781222079` | API: inference wiring decoupled from agentReloadHandler guard |

---

## Tests Added

| File | New Tests |
|------|-----------|
| `controller/internal/workspace/metrics_wiring_test.go` | 17 |
| `api/internal/handlers/session_tracker_test.go` | 8 (inference parsing + cost delta) |
| `api/internal/handlers/crd_watcher_test.go` | 2 (phase transition metric) |
| `api/internal/handlers/agent_reload_test.go` | 2 (defer metric recording) |
| `api/internal/services/auth/auth_test.go` | 3 (auth failure metric) |
| `controller/internal/workspace/controller_test.go` | 1 (recovery success metric) |

---

## Files Modified (substantive)

- `api/internal/handlers/session_tracker.go` — inference parsing fix + cost delta
- `api/internal/handlers/crd_watcher.go` — phase transition metric wiring
- `api/internal/handlers/agent_reload.go` — defer-based metric recording
- `api/internal/services/auth/auth.go` — auth failure metric wiring
- `api/internal/services/metrics/metrics.go` — remove dead relay injector metric
- `api/internal/app/app.go` — decouple SetOnInference from agentReloadHandler guard
- `controller/internal/workspace/metrics_wiring.go` — injectable metric helpers
- `controller/internal/workspace/reconciler.go` — reconciliation duration/errors
- `controller/internal/workspace/phase_active.go` — active seconds + storage bytes
- `controller/internal/workspace/phase_creating.go` — recovery success metric
- `controller/internal/workspace/phase_terminating.go` — workspaces deleted
- `controller/internal/workspace/recovery_policy.go` — recovery attempts/backoff/safe mode
- `controller/internal/metrics/metrics.go` — remove 5 dead metrics
- `charts/llmsafespace/dashboards/billing.json` — datasource fix + 26 descriptions
- `charts/llmsafespace/dashboards/operational.json` — datasource fix + 32 descriptions
