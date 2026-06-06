# Worklog: Metrics Instrumentation (Billing, Metering, Operations)

**Date:** 2026-06-06
**Agent:** agent-relay-jun06
**Status:** Complete — postgres billing_events table pending (next session)

---

## Objective

Implement Prometheus metrics for billing, metering, and operations. Verify correct cardinality. Identify what belongs in Prometheus vs postgres.

---

## Architecture Decision: Prometheus vs Postgres

**Prometheus** is for fleet-level aggregates and operational alerting. Labels must be bounded in cardinality. `workspace_id` on Prometheus counters creates O(workspaces × models × providers × tiers) series — fine at today's scale (18 workspaces = ~54k series), but wrong architectural choice because the right place for per-customer billing data is already postgres.

**Postgres billing_events** is for:
- Per-customer invoicing (exact token counts per session)
- Incident impact analysis ("which customers were affected during outage X")
- Customer-facing usage dashboards

The correct split: Prometheus for "what is the fleet consuming right now" (aggregates, rates, P50/P99), postgres for "what did customer X consume in July" (immutable event log).

---

## What Was Implemented

### Prometheus Metrics (fleet aggregates, bounded cardinality)

| Metric | Labels | Purpose |
|--------|--------|---------|
| `llmsafespace_inference_requests_total` | model_id, provider_id, tier | Inference call rate by model/provider |
| `llmsafespace_inference_input_tokens_total` | model_id, provider_id, tier | Fleet token throughput |
| `llmsafespace_inference_output_tokens_total` | model_id, provider_id, tier | Fleet token throughput |
| `llmsafespace_inference_cost_dollars_total` | model_id, provider_id, tier | Fleet USD cost rate |
| `llmsafespace_model_selections_total` | model_id, provider_id, tier | Model preference distribution |
| `llmsafespace_relay_injector_total` | outcome | Relay health per pod boot |
| `llmsafespace_workspace_phase_transitions_total` | from_phase, to_phase | Workspace lifecycle |
| `llmsafespace_workspaces_running` | runtime, security_level | Capacity gauge |
| `llmsafespace_workspaces_created_total` | runtime, security_level | Creation rate |
| `llmsafespace_session_duration_seconds` | (none) | Fleet P50/P99 session length |
| `llmsafespace_auth_failures_total` | reason | Security signal |

**Data source:** SSETracker fires on `session.updated` events when output tokens increase (delta-based, no double-counting).

### Cardinality Notes

Current scale (18 workspaces): all metrics are ~100s of series.
At 1,000 workspaces: still fine for Prometheus.
At 10,000+ workspaces: move to Prometheus remote write or dedicated TSDB; per-user billing events should be in postgres by then anyway.

---

## What Is Pending (Next Session)

### postgres billing_events table

Migration `000018_billing_events` needs to be created:

```sql
CREATE TABLE billing_events (
    id           UUID DEFAULT gen_random_uuid() PRIMARY KEY,
    workspace_id UUID NOT NULL,
    user_id      UUID NOT NULL,
    session_id   TEXT NOT NULL,
    model_id     TEXT NOT NULL,
    provider_id  TEXT NOT NULL,
    tier         TEXT NOT NULL CHECK (tier IN ('free', 'paid')),
    input_tokens  BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cost_dollars  NUMERIC(10,8) NOT NULL DEFAULT 0,
    duration_secs NUMERIC(10,2),
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX billing_events_user_id_idx ON billing_events(user_id, occurred_at);
CREATE INDEX billing_events_workspace_id_idx ON billing_events(workspace_id, occurred_at);
```

`RecordBillingEvent(ctx, event BillingEvent)` needs to be added to:
- `api/internal/interfaces/interfaces.go` (DatabaseService interface)
- `api/internal/services/database/database.go` (implementation)
- `api/internal/handlers/session_tracker.go` (InferenceCallback fires it alongside Prometheus)
- `api/internal/app/app.go` (wire db service into tracker callback)

The SSE tracker's `InferenceCallback` will need the `workspace_id` and `session_id` in scope — the tracker already has `workspaceID` from the subscription context. `user_id` needs to be looked up from the workspace CRD label `user-id`, which the tracker can get via a resolver function (same pattern as `podIPResolver`).

---

## Commits

| Commit | What |
|--------|------|
| b77b9c0 | Initial billing/metering metrics + relay model surfacing |
| 11aa839 | Active workspace gauge + session duration + auth failure counter |
| 5df27b0 | Fix cardinality: remove workspace_id from inference counters (correct per architecture decision above) |
| f34c798 | Fix Item #1: resolveModelIDFromCatalog relay providerID remap + RecordModelSelection call |

Deployed: ts-1780777762 (rev 157)

---

## Files Modified

- `api/internal/services/metrics/metrics.go` — all new metrics
- `api/internal/services/metrics/metrics_test.go` — 7 new TDD tests
- `api/internal/handlers/session_tracker.go` — InferenceCallback + handleSessionUpdated + session duration tracking
- `api/internal/middleware/auth.go` — auth failure counter
- `api/internal/app/app.go` — wiring all metrics callbacks
- `controller/internal/workspace/phase_creating.go` — WorkspacesRunning.Inc() + WorkspacesCreatedTotal
- `controller/internal/workspace/phase_suspend.go` — WorkspacesRunning.Dec()
- `cmd/workspace-agentd/relay_injector.go` — relay injector outcome counters
