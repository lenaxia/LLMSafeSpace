# Worklog 0227 — Grafana Dashboards, Prometheus Alerts, and ServiceMonitors

**Date:** 2026-06-11
**PR:** #108
**Branch:** `feat/grafana-dashboards-alerting-monitoring`

## Summary

Add comprehensive observability infrastructure to the Helm chart covering all 61 published Prometheus metrics (42 operational, 19 billing/metering). Three deliverables: Grafana dashboards, PrometheusRule alerts, and ServiceMonitor resources, all gated by a `monitoring.enabled` toggle.

## What was done

### Grafana Dashboards (2 JSON files)
- `dashboards/operational.json` — 30+ panels: API request overview, connections/WebSocket, auth failures, workspace lifecycle, reconciliation, recovery, agent operations, SSE/relay, billing at a glance
- `dashboards/billing.json` — 25+ panels: inference cost/token breakdown by model/tier, per-user metering (active seconds, CPU, LLM calls), per-workspace resource consumption (storage, memory, CPU, proxy bytes)

### PrometheusRule Alerts (19 rules across 4 groups)
- `llmsafespace.api` (5): graduated error rate (5% warning / 15% critical with distinct names), latency >5s, auth failures, SSE broker drops
- `llmsafespace.controller` (8): reconciliation errors, workspace failures, slow creation, recovery backoff, safe mode, consecutive failures >3, status conflicts, init container slow
- `llmsafespace.agentd` (3): reload failures >10%, slow startup >60s, relay injector failures
- `llmsafespace.billing` (3): high cost rate >$10/hr, disk usage >90%, legacy API keys

### Helm Chart Integration
- `monitoring.enabled` master toggle (off by default) with independent sub-toggles for `dashboards`, `prometheusRules`, `serviceMonitors`
- ServiceMonitors for API (`/metrics`) and controller (metrics port)
- Dashboard ConfigMap auto-discovers JSON from `dashboards/` directory with `grafana_dashboard: "1"` label
- Optional `bearerTokenSecret` for API ServiceMonitor when `LLMSAFESPACE_METRICS_TOKEN` is set
- Controller `metricsAddr` auto-overridden to `0.0.0.0:8080` when ServiceMonitors enabled (containerPort and Service port derived from same variable)

### Tests (14 new)
- Master toggle disabled by default (no resources rendered)
- All resources render when enabled
- Independent sub-toggle gating (dashboards only, rules only, monitors only)
- Controller metricsAddr override when monitoring on vs default loopback when off
- Dashboard ConfigMap contains both JSON files and grafana_dashboard label
- PrometheusRule contains all 19 expected alert names
- Namespace override for all monitoring resources

### Documentation
- Chart README updated with full monitoring section, prerequisites, configuration reference
- Security note about controller metrics exposure
- Fixed pre-existing `rbac.scope` default inaccuracy (`"namespace"` not `"cluster"`)
- Fixed pre-existing double-backtick typo

## Review iterations

### Round 1 — REQUEST CHANGES
- **F1**: Controller ServiceMonitor broken with default loopback binding. Fixed: conditional `$metricsAddr` override.
- **F2**: API ServiceMonitor broken when metrics auth enabled. Fixed: optional `bearerTokenSecret`.
- **F3**: Alert group count wrong (5 vs 4). Fixed: merged `general` into `controller`.
- **R2**: Alert job matchers too broad. Fixed: scoped to release name via fullname helper.
- Missing tests, chart README not updated. Fixed.

### Round 2 — REQUEST CHANGES
- **Controller containerPort**: Variable `$metricsAddr` not used for containerPort/Service port. Fixed: moved variable to container scope, derived port from it.
- **AgentReloadFailures wrong job**: Metric emitted by API service, not agentd. Fixed: changed to `$apiJob`.
- **Agentd scraping gap**: No ServiceMonitor for agentd pods. Documented in comments and README.
- Missing worklog, pre-existing README inaccuracies. Fixed.

### Round 3 — APPROVE

## Assumptions validated

1. Grafana sidecar imports dashboards via `grafana_dashboard: "1"` label — confirmed by convention in grafana/grafana helm chart
2. Prometheus Operator watches for `PrometheusRule` and `ServiceMonitor` CRDs — confirmed by kube-prometheus-stack
3. `llmsafespace_agent_reload_total` emitted only by API service — verified via `grep` in `api/internal/services/metrics/metrics.go:147`, no matches in `cmd/workspace-agentd/`
4. `llmsafespace_agentd_gate_duration_seconds` emitted only by workspace-agentd — verified in `cmd/workspace-agentd/gate_recorder.go:74`
5. Controller `containerPort` must match `--metrics-addr` port — verified by Kubernetes Service `targetPort` name matching
6. Default `rbac.scope` is `"namespace"` — verified by `values.yaml:460` and `TestG5_DefaultIsNamespaceScope`
