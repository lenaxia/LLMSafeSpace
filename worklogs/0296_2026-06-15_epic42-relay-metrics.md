# Worklog 0297 — Epic 42: Relay Prometheus Metrics

**Date:** 2026-06-15
**Epic:** 42
**Scope:** 6 controller-emitted Prometheus metrics for InferenceRelay fleet

## Summary

Added the 6 metrics prescribed in the Epic 42 design doc (lines 898–904)
to power Grafana alerting rules and fleet observability.

## Metrics added

| Metric | Type | Labels |
|--------|------|--------|
| `llmsafespace_relay_healthy_replicas` | gauge | — |
| `llmsafespace_relay_provisioning_failed` | gauge | provider |
| `llmsafespace_relay_draining` | gauge | provider |
| `llmsafespace_relay_quota_exhausted` | gauge | provider |
| `llmsafespace_relay_provision_duration_seconds` | histogram | provider |
| `llmsafespace_relay_rotation_total` | counter | provider, reason |

## Files

- `controller/internal/metrics/metrics.go`: 6 new definitions + collectors()
- `controller/internal/relay/metrics_wiring.go`: helper functions
- `controller/internal/relay/reconciler.go`: emit metrics during reconcile
