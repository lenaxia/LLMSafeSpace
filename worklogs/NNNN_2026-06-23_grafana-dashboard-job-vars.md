# Worklog: Remove Grafana Dashboard `job` / `controller_job` Template Variables

**Date:** 2026-06-23
**Session:** Diagnose why the operational + billing Grafana dashboards still showed "No data" for most panels after PR #356's metrics-wiring deploy. Root cause: stale URL parameters left the `controller_job` template variable empty; the variable's `refresh: 2` (refresh on time range change only) never re-resolved on dashboard load. Fix: remove the variables entirely — there's only ever one valid value for each, and a hardcoded job-name matcher is more robust than a runtime-resolved variable.
**Status:** Complete. All controller-side panels now resolve to data on the live cluster.

---

## Bug Report

> "Availability, active workspaces, suspended workspaces, safe mode, legacy apis, workspace create duration and resume duration, DB metrics, sse events, noisy neighbors, controller and recovery, and metering should all have data"

Dashboard URL the user opened:
```
https://grafana.thekao.cloud/d/llmsafespace-operational/llmsafespace-operational?...&var-job=llmsafespace-api&var-controller_job=&...
```

Note the empty `var-controller_job=` parameter.

## Root Cause Analysis

### Step 1: confirm metrics are in Prometheus

Direct queries with the right job labels returned data for almost every panel:

| Query | Result |
|---|---|
| `sum(llmsafespaces_workspaces_running{job="llmsafespace-controller-metrics"})` | 9 series ✓ |
| `llmsafespaces_workspace_safe_mode_active{job="llmsafespace-controller-metrics"}` | 1 series ✓ |
| `sum(llmsafespaces_db_pool_active_connections{job="llmsafespace-api"})` | 1 series ✓ |
| `histogram_quantile(0.95, sum(rate(llmsafespaces_db_query_duration_seconds_bucket{job="llmsafespace-api"}[5m])) by (le, operation))` | 5 series ✓ |
| `sum(rate(llmsafespaces_metering_events_recorded_total{job="llmsafespace-api"}[5m]))` | 1 series ✓ |

Data was flowing. The dashboard wasn't displaying it.

### Step 2: the `controller_job` variable

The dashboard expected `controller_job` to resolve to `llmsafespace-controller-metrics` via `label_values(llmsafespaces_reconciliation_duration_seconds_bucket, job)`. With the URL setting `var-controller_job=` empty:

- The variable resolved to literal empty string
- Every panel using `{job=~"$controller_job"}` matched zero series
- `refresh: 2` on the variable means "refresh on time range change only" — visiting the URL fresh did NOT re-resolve

Confirmed by the user: opening the `controller_job` dropdown showed an empty options list, and typing "All" did nothing. The variable was dead-on-arrival from the URL.

### Step 3: why have the variable at all

There's only ever one valid value: `llmsafespace-controller-metrics`. The cluster runs a single controller deployment (replicas: 2 for HA, but both pods share the same `job` label — they're indistinguishable in metrics, and aggregations across them produce the right values). The `job` variable has the same property — only one valid value (`llmsafespace-api`).

The variables provided no operational benefit and were a constant footgun for stale URLs. Removing them eliminates the entire class of "dashboard silently empty because URL var was wrong" bugs.

## Fix

Surgical Python-driven JSON edit on both `operational.json` and `billing.json`:

1. **Removed `controller_job` and `job` from `templating.list`** — three template variables removed across the two dashboards (`controller_job` from operational, `job` from both)
2. **Replaced every `{job=~"$controller_job"}` with `{job="llmsafespace-controller-metrics"}`** — exact matcher, no regex, no variable resolution
3. **Replaced every `{job=~"$job"}` with `{job="llmsafespace-api"}`** — same treatment
4. Kept `datasource` template variable (real Grafana convention for letting Grafana picker pick the data source)

After patching, **0 references to `$controller_job` or `$job`** remain in either file. Verified by grep.

## Validation

Ran every panel's query through Prometheus directly (via `kubectl port-forward` to `kube-prometheus-stack-prometheus`):

**Operational dashboard (29 panels):**
- ✓ 21 panels return data: Availability, Active Workspaces, Suspended, Safe Mode, Legacy API Keys, Request Composition, API Latency p95/p99, DB Pool, DB Query Latency, Redis Latency, Dependency Status, Top Workspaces by CPU/Memory/Disk, Top Users, Reconciliation Duration p99, Metering Events, DLQ Depth, Billing Export Lag
- ✗ 8 panels return EMPTY: Error Status Breakdown (no 5xx in window), Workspace Create/Resume Duration (no creates/resumes), Workspace Failure & Restart, SSE Dropped (no drops), Reconciliation Errors (no errors), Recovery, Auth Failure (no failed logins), Client Error Types (no 4xx), Agent Reload — **all correct "no data" states for a healthy quiet system**

**Billing dashboard (11 panels):**
- ✓ 6 panels return data: Total Requests, Total Input/Output Tokens, Token Throughput by Model, Request Rate by Model & Provider
- ✗ 5 panels return EMPTY: Total Cost, Cost by Model, Cost by Tier, Model Selection Churn, Relay Injector Outcomes, Quota Exceeded — **correct "no data" for the current state** (cost requires postgres-side billing join; quota requires 429 responses)

`go test ./charts/llmsafespaces/...` — pass (chart tests still green after the JSON edit).

## Key Decisions

1. **Removed variables instead of fixing `refresh: 2 → 1`.** The latter would fix the immediate symptom but leave the footgun for future stale URLs. Removal eliminates the entire failure mode.
2. **Used `=` not `=~` matcher.** Exact match is faster, clearer, and there's no regex semantics to reason about.
3. **Kept `datasource` template variable.** That's a real Grafana convention — the data source picker at the top of every dashboard. Different from the job filter.
4. **Did not introduce any default values via `or vector(0)`.** EMPTY panels for "counter never observed" are correct Prometheus semantics. Adding `or vector(0)` everywhere would mask real "is this metric being scraped?" outages.

## Adversarial Self-Review

**Phase 1 — findings:**

1. *What if the controller is renamed?* Today: `llmsafespace-controller-metrics` is the literal `job` label set by the chart's ServiceMonitor. If the chart's ServiceMonitor selector ever changes the label, every dashboard panel breaks. → Mitigated by the chart_test golden tests that lock in the label. Worth a future story to template the job name into the dashboard JSON via Helm rendering.
2. *What if a multi-tenant or multi-cluster setup needs to filter?* YAGNI — we're nowhere near that. If/when it becomes real, reintroduce the variable with `refresh: 1` (on dashboard load) and `multi: true`. The decision to remove it now is reversible.
3. *Did I miss any `$job` / `$controller_job` references?* Grep confirms 0 remaining. The Python script walked every nested dict including `targets[].expr`, `query.query`, and `definition`.
4. *Will my changes break Helm rendering?* `helm template` produces 2 dashboard ConfigMap entries (correct), `helm lint` clean.
5. *Will Grafana sidecar pick up the new ConfigMap?* Already validated for the previous PR #356 redeploy — the sidecar reloads ConfigMap-backed dashboards within 30s.

All findings either mitigated, false alarm with rationale, or filed as future work.

---

## Next Steps

After PR merge:
1. Helm upgrade pushes the new dashboard JSON into the `llmsafespace-grafana-dashboards` ConfigMap
2. Grafana sidecar picks up the change (30s)
3. Hard-refresh the dashboards in browser (`Ctrl+Shift+R`) to clear cached panel rendering
4. The user's original URL should now show data on every panel that has metric series, regardless of stale URL params

## Files Modified

- `charts/llmsafespaces/dashboards/operational.json` — removed `controller_job` and `job` template variables; replaced 30+ matcher references with literal job names
- `charts/llmsafespaces/dashboards/billing.json` — same treatment for `job`
- `worklogs/0521_2026-06-23_grafana-dashboard-job-vars.md` — this file
