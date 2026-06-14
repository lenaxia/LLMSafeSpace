# Worklog: Grafana Dashboard Redesign — Operational & Billing

**Date:** 2026-06-14
**Session:** Redesign both Grafana dashboards around operator stories/actions, single critical thresholds, top-N bar gauges, corrected availability formula, Postgres datasource for per-user billing attribution
**Status:** Complete

---

## Objective

Redesign the operational and billing Grafana dashboards so that every panel tells a story and enables a specific operator action. Move operational metrics off the billing dashboard (and vice versa). Add thresholds to actionable panels. Add per-user billing attribution via a Postgres datasource.

---

## Work Completed

### Design Analysis (Pre-Implementation)

Identified six operational stories and four billing stories that every dashboard panel must serve:

**Operational:** (A) Platform availability, (B) Workspace lifecycle UX, (C) Resource saturation, (D) Noisy neighbor detection, (E) Controller/recovery health, (F) Security/auth, (G) Metering pipeline integrity.

**Billing:** (H) Burn rate, (I) Per-user attribution, (J) Token economics, (K) Quota enforcement.

Key design decisions validated against user requirements:
- Availability = `1 - 5xx / (total - 4xx)` — 4xx excluded as client errors
- Single critical thresholds only (no warn/crit tiers)
- p95/p99 only (drop p50 except where typical is the story)
- Top-N bar gauges for noisy-neighbor attribution (time-series-by-entity is unreadable at multitenant scale)
- Error type breakdown (401/403/404/429/5xx) retained as a story panel — distinguishes auth failures, authz issues, routing bugs, quota enforcement, server faults
- Metering pipeline health moved from billing to operational (it's about data trustworthiness, not cost attribution)

### Operational Dashboard Rewrite (`charts/llmsafespace/dashboards/operational.json`)

Complete rewrite. 7 rows, 30 panels. Every panel has a description explaining the story and the action.

**Row 1: Platform Availability**
- Availability % stat (formula: `1 - 5xx/(total-4xx)`, critical threshold at 99%)
- Quick stat row: Active Workspaces, Suspended Workspaces, Safe Mode, Legacy API Keys
- Request Composition stacked area (2xx/4xx/5xx)
- Error Status Breakdown by specific HTTP code (401, 403, 404, 429, 500, 502...)
- API Latency p95/p99 (red line at 2s)

**Row 2: Workspace Lifecycle**
- Create Duration p95/p99 (red line at 120s)
- Resume Duration p95/p99 (red line at 10s)
- Workspace Failure & Restart Rate (filtered to Failed/Creating transitions)

**Row 3: Resource Saturation & Dependencies**
- DB Pool Utilization % (red line at 80%)
- DB Query p95 + Errors combined (red line at 50ms)
- Redis p95 + Errors combined (red line at 5ms)
- SSE Dropped Events (red line at 1/s)
- Dependency Status stat (postgres, redis)

**Row 4: Noisy Neighbor Detection** (all top-10 bar gauges)
- Top-10 Workspaces by CPU (red at 4 cores)
- Top-10 Workspaces by Memory (red at 2Gi)
- Top-10 Workspaces by Disk % of PVC (red at 95%)
- Top-10 Users by Active Compute (red at 10 parallel workspaces)

**Row 5: Controller & Recovery**
- Reconciliation Duration p99 (red at 2s)
- Reconciliation Errors (red at >0)
- Recovery Success Ratio by failure class (red below 75%)

**Row 6: Security & Auth**
- Auth Failure Ratio + Reasons (red at 15%)
- Client Error Types: 401/403/404/429 breakdown (story panel, no threshold)
- Agent Reload Failure Ratio (red at 10%)

**Row 7: Metering Pipeline Integrity** (moved from billing dashboard)
- Events Recorded/Failed/Dropped (red at 1/s for dropped)
- DLQ Depth gauge (red at >0)
- DLQ Dead Entries stat (red at >0)
- Billing Export Lag (red at 900s)

**Removed from operational:** Active API Connections (no action), WebSocket Connections (no action), Agent Reload Duration (not actionable), Agentd Startup Gate (contributing factor to create duration, not separately actionable), Bulk Reload Outcomes (not actionable at runtime), Billing at a Glance row (cross-link instead), Session Duration (billing/capacity metric), Inference metrics (billing), Service Startup Duration (only relevant on deploy), Relay Injector Outcomes (moved to billing).

### Billing Dashboard Rewrite (`charts/llmsafespace/dashboards/billing.json`)

Complete rewrite. 4 rows, 16 panels.

**Row 1: Burn Rate**
- Total Cost / Requests / Input Tokens / Output Tokens (period stat row)
- Cost Rate by Model $/h
- Cost Rate by Tier (free vs paid, stacked)

**Row 2: Per-User Attribution** (Postgres datasource)
- Top-10 Users by Token Consumption (SQL query against usage_events)
- Top-10 Users by Compute Hours (SQL query)
- Top-10 Workspaces by Compute Hours (SQL query)
- Top-10 Users by Daily Quota Utilization % (SQL query joining usage_limits)

**Row 3: Token Economics & Model Mix**
- Token Throughput by Model (input vs output)
- Inference Request Rate by Model & Provider
- Model Selection Churn
- Relay Injector Outcomes

**Row 4: Quota Enforcement**
- Quota Exceeded Events by type
- Top-10 Users Hitting Quota Limits (Postgres query)

**Removed from billing:** Metering Events Recorded/Failed/Dropped (moved to ops), DLQ panels (moved to ops), Batch Write Latency (moved to ops), Reconciliation Catch-up (moved to ops), Export Lag (moved to ops), Legacy API Keys (moved to ops), duplicate "LLM Calls Fleet Total" panel, empty Epic 33 panels (Workspace LLM Proxy Latency, Proxy Bytes).

### Postgres Grafana Datasource (`charts/llmsafespace/templates/grafana-datasources.yaml`)

New Helm template deploying a ConfigMap with `grafana_datasource: "1"` label for the Grafana sidecar datasource importer. Configures a Postgres datasource pointing at the existing `postgresql.host:postgresql.port` with `postgresql.database` and `postgresql.user` from values.yaml. Password injected via `${PG_PASSWORD}` environment variable (operator configures Grafana to set this from the existing Secret).

Added `monitoring.datasources` section to `values.yaml` with enable flag, namespace, labels, and postgres UID configuration.

### Prometheus Rules Update (`charts/llmsafespace/templates/prometheus-rules.yaml`)

Replaced the two-tier API error rate alerts (LLMSafeSpaceHighAPIErrorRate warning at 5%, LLMSafeSpaceHighAPIErrorRateCritical at 15%) with a single critical availability alert:

- `LLMSafeSpaceLowAvailability`: fires when `1 - 5xx/(total-4xx) < 0.99` for `3m` (approximates "3 of 5 data points" at 1-minute evaluation interval)
- Severity: critical only (no warning tier)
- 4xx excluded from denominator per the corrected availability formula

---

## Key Decisions

1. **Availability formula excludes 4xx from denominator** — 4xx are client errors (bad auth, not found, quota). Including them would penalize the server for client mistakes. Formula: `1 - 5xx / (total - 4xx)`.

2. **Single critical thresholds, no warning tier** — per user requirement. Every actionable panel has exactly one red line. This simplifies operator response: if the line is red, act.

3. **Top-N bar gauges instead of time-series-by-entity** — at multitenant scale (100+ workspaces/users), time-series-by-entity produces unreadable spaghetti. Bar gauges show the current top-10 consumers, which is exactly what an operator needs to take action (throttle/terminate the specific workspace or user).

4. **Metering pipeline health moved to operational dashboard** — pipeline integrity is about data trustworthiness, not cost attribution. A billing dashboard showing cost numbers is meaningless if the pipeline producing them is broken; the operator needs to know about pipeline issues immediately, not when reviewing monthly bills.

5. **Per-user billing attribution via Postgres datasource** — Prometheus cardinality prevents per-user inference cost metrics (O(workspaces × models × providers) ≈ 30M series at scale). The usage_events table in PostgreSQL has all the raw data. Added a Grafana Postgres datasource ConfigMap to the Helm chart and SQL-backed bar gauge panels querying usage_events directly.

6. **Error type breakdown (401/403/404/429) retained as story panel without threshold** — these are distinct signals (auth failure, authz issue, routing bug, quota enforcement) that don't share a single threshold. The panel tells the operator WHAT kind of client errors are occurring, which guides different actions.

7. **Request Composition as stacked area without threshold** — this is a traffic story panel (what's the mix?), not an action panel. The availability % stat panel is the action panel for this story.

8. **p50 dropped from latency panels** — only p95/p99 are actionable. p50 represents typical experience, which doesn't drive operator response.

---

## Assumptions

1. Grafana is deployed with the sidecar datasource importer (standard in kube-prometheus-stack / Grafana Helm chart) — validated by the existing dashboard ConfigMap pattern using `grafana_dashboard: "1"` label.
2. Postgres is reachable from Grafana's network — validated by existing NetworkPolicy (charts/llmsafespace/templates/datastore-network-policy.yaml) and the fact that API pods connect to the same Postgres.
3. `usage_events` table schema is stable — validated by reading migration `000024_metering_tables.up.sql`.
4. Node-exporter / kube-state-metrics may or may not be deployed — node-level resource panels omitted from this iteration. If deployed, they can be added in a future row.
5. The `${PG_PASSWORD}` env var must be configured in the Grafana pod — this is an operator deployment step, documented in the values.yaml comments.

---

## Blockers

None.

---

## Tests Run

- `python3 -c "import json; json.load(open('operational.json'))"` — valid JSON ✓
- `python3 -c "import json; json.load(open('billing.json'))"` — valid JSON ✓
- Programmatic panel audit: confirmed all 30 operational panels and 16 billing panels have descriptions, actionable panels have red thresholds, non-actionable story panels intentionally omit thresholds ✓
- `yaml.safe_load(values.yaml)` — valid YAML, datasources section present ✓
- Prometheus rules: confirmed availability alert present, old error rate alerts removed ✓
- Helm not available in sandbox for `helm template` — validation deferred to CI/operator

---

## Next Steps

1. Deploy to a test cluster with monitoring enabled and verify dashboards render correctly
2. Configure `${PG_PASSWORD}` in Grafana pod env vars (from the existing Postgres Secret)
3. Consider adding node-level resource panels if node-exporter is deployed
4. Consider adding a Grafana alerting rule for the availability threshold (in addition to the PrometheusRule) if using Grafana's built-in alerting
5. Review the remaining PrometheusRule alerts — several are still `severity: warning`. If the "critical only" policy applies to all alerts, convert or remove warning-tier rules

---

## Files Modified

- `charts/llmsafespace/dashboards/operational.json` — complete rewrite (2484→715 lines, 7 rows, 30 panels)
- `charts/llmsafespace/dashboards/billing.json` — complete rewrite (1974→415 lines, 4 rows, 16 panels)
- `charts/llmsafespace/templates/grafana-datasources.yaml` — new file (Postgres datasource ConfigMap)
- `charts/llmsafespace/templates/prometheus-rules.yaml` — replaced two-tier error rate alerts with single critical availability alert
- `charts/llmsafespace/values.yaml` — added `monitoring.datasources` section
