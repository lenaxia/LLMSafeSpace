# Epic 33: Observability, Metering, and Billing Infrastructure

**Status:** Planning  
**Created:** 2026-06-06  
**Depends On:** Epic 24 (Self-Healing Lifecycle), Epic 26 (Client-Proxied Inference), Epic 28 (Unified Event Stream)  
**Priority:** High  

---

## Problem Statement

LLMSafeSpace has 51 Prometheus metrics instrumented across three binaries and nothing
consuming them. No scraper. No dashboards. No alerting. No persistent event ledger. The
metrics are emitted into a void.

Beyond the missing stack, there are three structural gaps:

**Gap 1: No observability infrastructure.**
Metrics are instrumented but nothing scrapes, stores, visualizes, or alerts on them.

**Gap 2: Metering routes through the controller.**
CPU, memory, and disk are read by agentd from the pod cgroup, reported to the controller
via 60-second statusz poll, and the controller writes Prometheus counters. The ground
truth is inside the pod. The current design introduces a 60-second lag and a controller
dependency into the metering path. If the controller is unhealthy, metering stops. If the
pod is deleted between polls, the final period is unrecorded.

**Gap 3: No per-customer event ledger.**
Prometheus counters tell you what the fleet consumed in aggregate. They cannot answer
"what did workspace X consume in July" or "which customers were impacted by the outage
between 14:00 and 15:30." Those questions require a queryable event log with customer
identity attached.

---

## Foundational Decisions

Before the architecture, the decisions that shape it.

### Decision 1: agentd is the source of truth for pod state

The controller sets workspace phase to `Active` when Kubernetes resources reach desired
state. This is not the same as "the workspace is billing." The controller's phase is a
reconciliation decision based on readiness gates polled from outside the pod.

agentd runs inside the pod. It knows the actual state: is the pod Ready from the inside?
Is opencode alive? Is the workspace genuinely serving users? The controller's view is
always a lagged, approximate projection of agentd's reality.

**For billing: pod Ready = billing.** Not controller phase = Active. If the pod is Ready,
we are billing. If it is Suspended or Terminated, we are not. This is what users
understand, what every compute platform does, and what agentd can report with
millisecond precision.

### Decision 2: Second-granularity billing from the source

Billing precision is second-granularity. This requires exact timestamps for pod state
transitions — when did billing start, when did it stop. A minute-granularity poll from
the controller cannot provide this. agentd must push state transitions as events with
exact timestamps.

Second-granularity billing precision does not come from push metrics (counters
accumulating every second). It comes from **event timestamps** at state transitions. A
workspace that runs for 2 hours produces 2 events: `pod_ready` at the start and
`pod_terminated` at the end. Duration is `ended_at - started_at`. No counter. No
accumulation error. Millisecond precision.

### Decision 3: One push target for agentd

agentd pushes to one target: the events-gateway. The gateway fans out to VictoriaMetrics
(operational store) and Postgres (billing ledger). agentd has one failure mode, one
configuration point, one network dependency.

Splitting agentd's push between VictoriaMetrics (resource metrics) and the gateway
(billing events) gives agentd two independent failure modes and complicates the WAL
design. Everything goes through the gateway.

### Decision 4: The controller is fallback only

In the happy path, the controller does not write billing data. It manages Kubernetes
state. It emits fleet-level operational counters (workspace creation rate, failure rate,
recovery state machine). It does not touch `compute_periods` or `inference_events`
except as a gap-closer when agentd was unable to push.

### Decision 5: No distinction between operational and billing data

Every metric is an operational metric. Some are also billing inputs.
`workspace_active_seconds` tells the ops team about fleet utilization and the finance
team what to charge. They are the same data viewed through different lenses. Do not
build separate pipelines.

The two consumers have different technical requirements:

| | VictoriaMetrics | Postgres |
|---|---|---|
| Question answered | What is happening right now? | What exactly happened to customer X? |
| Granularity | Second-granularity from agentd push | Exact event timestamps |
| Query style | PromQL, rates, percentiles | SQL, joins to users/plans |
| Loss tolerance | A few seconds acceptable | Billing-critical events must not be lost |
| Retention | 90 days configurable | Indefinite |
| Use | Dashboards, alerting, fleet ops | Invoicing, disputes, incident impact |

Both are necessary. Both receive data from the same event stream via the gateway.

### Decision 6: Postgres for the billing ledger, not a TSDB

This decision is worth stating explicitly because the natural instinct — given that we
are already using VictoriaMetrics — is to ask whether the billing tables could also live
there. They should not, and the reasons are specific.

**`compute_periods` and `inference_events` are not time-series data.** A time-series is
a stream of numeric samples at regular intervals: CPU at T=0, CPU at T=1, CPU at T=2.
A billing period is a record with identity: workspace X was active from T1 to T2,
duration 3600.677 seconds, belonging to user Y on plan Z. These are structurally
different things. TSDBs are optimised for the former.

**The upsert pattern has no TSDB equivalent.** `inference_events` accumulates token
deltas over a session's lifetime via `ON CONFLICT (session_id) DO UPDATE SET
input_tokens = inference_events.input_tokens + EXCLUDED.input_tokens`. VictoriaMetrics
has no concept of row updates. Storing token deltas in a TSDB and summing them at query
time is possible but complex and fragile — the sum changes if the retention window
changes, if any sample is lost, or if the query window is wrong.

**Billing queries require SQL joins.** "What did user X consume this month" is
`JOIN users ON user_id`. "Which customers were impacted by the outage" is
`JOIN users ON user_id WHERE severity = 'critical'`. PromQL has no JOIN. You could
carry `user_id` as a label on every series and filter by it — but you cannot join to
`users.email`, `users.plan_id`, or contract terms without a second application-level
lookup. For a usage dashboard showing customer names this is a minor inconvenience. For
an invoice generation job or an incident compensation process this is a structural
limitation.

**Indefinite exact retention is a billing requirement.** A customer disputes a charge
from 18 months ago. You need to show them the exact record. VictoriaMetrics supports
indefinite retention with `retentionPeriod: -1`, but its storage format is optimised
for recent data. Downsampling rules must be explicitly disabled for billing data or old
records lose second-granularity precision. Postgres retains every row exactly as written,
forever, with no configuration required.

**Dispute resolution requires row-level evidence.** "Your workspace ws-abc was active
from 2026-06-06T14:00:07.441Z to 2026-06-06T16:47:23.118Z for 10035.677 seconds" is
a Postgres row. Showing that to a customer or a lawyer is straightforward. Reconstructing
the same fact from a TSDB requires knowing which series to query, which timestamps to
use, and trusting that no downsampling occurred. Postgres is the correct store for
financial evidence.

**The right split:** VictoriaMetrics owns everything that is a numeric sample over time
(CPU, memory, disk at 1-second granularity; fleet counters; latency histograms; rates).
Postgres owns everything that is a record with identity and financial consequence
(billing periods, inference sessions, workspace lifecycle events). The gateway fans out
to both from the same event stream.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│ workspace pod                                                           │
│                                                                         │
│  ┌──────────────────────────────────────────────────────────────────┐  │
│  │ agentd                                                           │  │
│  │                                                                  │  │
│  │  PodStateTracker          ResourceSampler                        │  │
│  │  - detects Ready/         - reads cgroup every 1s               │  │
│  │    Suspended/Terminated   - cpu_usage_seconds                   │  │
│  │  - fires on transition    - memory_bytes                        │  │
│  │  - WAL-protects Tier 1    - disk_bytes                          │  │
│  │                                                                  │  │
│  │  GatewayClient                                                   │  │
│  │  - single push target                                            │  │
│  │  - WAL for Tier 1 events (emptyDir /var/lib/agentd/wal/)        │  │
│  │  - batches Tier 2 + resource samples (1s flush)                 │  │
│  └──────────────────────────────┬───────────────────────────────────┘  │
│                                 │ POST /ingest (1/second)              │
└─────────────────────────────────┼───────────────────────────────────── ┘
                                  │
┌─────────────────────────────────┼─────────────────────────────────────┐
│ node (kubelet)                  │                                      │
│  ┌────────────────────┐         │                                      │
│  │ cAdvisor           │         │                                      │
│  │ /metrics/cadvisor  │         │                                      │
│  └─────────┬──────────┘         │                                      │
└────────────┼───────────────────────────────────────────────────────────┘
             │ scrape (vmagent, 15s)       │
             │ node-level fleet metrics    │
             ▼                            ▼
      vmagent                    events-gateway (2-3 replicas)
      (relabels, remote_write)   │
             │                   ├──→ VictoriaMetrics remote_write
             │                   │    - pod state gauges
             ▼                   │    - cpu/memory/disk per workspace (1s)
      VictoriaMetrics             │    - inference counters
      (TSDB, 90d retention)      │    - fleet operational counters
             ▲                   │
             └───────────────────┘
                                 │
                                 └──→ Postgres
                                      - compute_periods
                                      - inference_events
                                      - workspace_events
                                      - workspace_events_dlq
```

---

## Component Responsibilities

### agentd

Gains three new internal components. No new binary, no sidecar.

**PodStateTracker:** Monitors agentd's own health state and detects transitions between
billable states (pod_ready, pod_suspended, pod_resumed, pod_terminated). Fires Tier 1
events on transition. These are the billing period boundaries.

**ResourceSampler:** Reads cgroup data every 1 second (already does this for statusz).
Packages samples for push. These are Tier 2 — best-effort operational data.

**GatewayClient:** agentd's single push target. Handles WAL for Tier 1 events, batching
for Tier 2 events and resource samples, retry with backoff, and graceful drain on
shutdown.

### events-gateway

A new dedicated Go service. 2-3 replicas. Receives all data from agentd (and the
controller for gap events). Fans out to VictoriaMetrics and Postgres. The sole holder
of billing-table write credentials.

Does not hold state between requests. Stateless. Any replica can handle any request.
The WAL lives in agentd, not in the gateway.

### controller

Fleet-level operational counters unchanged. Existing `controller/internal/metrics`
package unchanged. Gains one new responsibility: gap detection and closure. Detects
open `compute_periods` rows where `last_heartbeat_at` has gone stale, verifies the pod
is actually gone, and closes the period.

### vmagent

Scrapes:
- cAdvisor on every node (fleet-level resource data, 15s interval)
- API server `/metrics` (HTTP, auth, session metrics, 15s interval)
- Controller `/metrics` (operational counters, 15s interval)

Does not scrape workspace pods directly. Per-workspace second-granularity data comes
from agentd push via the gateway.

### VictoriaMetrics

Single-node. 90-day retention. Receives data from two sources:
- vmagent remote_write (cAdvisor + API server + controller metrics)
- events-gateway remote_write (agentd pod state + resource samples)

---

## Data Model

### `compute_periods` — billing period ledger

```sql
CREATE TABLE compute_periods (
    id               UUID        DEFAULT gen_random_uuid() PRIMARY KEY,
    workspace_id     TEXT        NOT NULL,
    user_id          TEXT        NOT NULL,
    runtime          TEXT        NOT NULL,
    security_level   TEXT        NOT NULL,
    started_at       TIMESTAMPTZ NOT NULL,
    ended_at         TIMESTAMPTZ,                    -- NULL = period open
    duration_secs    NUMERIC(12,3),                  -- populated on close
    last_heartbeat_at TIMESTAMPTZ,                   -- updated every second by gateway
    source           TEXT        NOT NULL DEFAULT 'agentd',  -- 'agentd' | 'controller_gap_close'
    idempotency_key  TEXT        UNIQUE              -- workspace_id:started_at_unix
);

CREATE INDEX compute_periods_user_idx
    ON compute_periods(user_id, started_at DESC);
CREATE INDEX compute_periods_workspace_idx
    ON compute_periods(workspace_id, started_at DESC);
CREATE INDEX compute_periods_open_idx
    ON compute_periods(last_heartbeat_at)
    WHERE ended_at IS NULL;
```

**Two operations per billing period:**

Open (on pod_ready):
```sql
INSERT INTO compute_periods
    (workspace_id, user_id, runtime, security_level,
     started_at, last_heartbeat_at, idempotency_key)
VALUES ($1, $2, $3, $4, $5, NOW(), $6)
ON CONFLICT (idempotency_key) DO NOTHING;
```

Close (on pod_terminated or pod_suspended):
```sql
UPDATE compute_periods
SET ended_at      = $2,
    duration_secs = EXTRACT(EPOCH FROM $2 - started_at),
    source        = $3
WHERE workspace_id = $1
  AND ended_at IS NULL;
```

Heartbeat update (on every resource sample):
```sql
UPDATE compute_periods
SET last_heartbeat_at = NOW()
WHERE workspace_id = $1
  AND ended_at IS NULL;
```

**Billing query — compute hours per user this month:**
```sql
SELECT
    u.email,
    SUM(cp.duration_secs)         AS total_seconds,
    SUM(cp.duration_secs) / 3600  AS compute_hours
FROM compute_periods cp
JOIN users u ON u.id = cp.user_id::uuid
WHERE cp.started_at >= date_trunc('month', NOW())
  AND cp.ended_at IS NOT NULL
GROUP BY u.email
ORDER BY compute_hours DESC;
```

**Incident query — who had active workspaces between T1 and T2:**
```sql
SELECT DISTINCT cp.user_id, cp.workspace_id, cp.started_at, cp.ended_at
FROM compute_periods cp
WHERE tstzrange(cp.started_at, cp.ended_at) && tstzrange(:t1, :t2);
```

---

### `inference_events` — per-session token ledger

One row per inference session. Upserted as token deltas arrive, closed on session
completion.

```sql
CREATE TABLE inference_events (
    id            UUID        DEFAULT gen_random_uuid() PRIMARY KEY,
    workspace_id  TEXT        NOT NULL,
    user_id       TEXT        NOT NULL,
    session_id    TEXT        NOT NULL UNIQUE,       -- upsert key
    model_id      TEXT        NOT NULL,
    provider_id   TEXT        NOT NULL,
    tier          TEXT        NOT NULL CHECK (tier IN ('free', 'paid')),
    input_tokens  BIGINT      NOT NULL DEFAULT 0,
    output_tokens BIGINT      NOT NULL DEFAULT 0,
    cost_dollars  NUMERIC(10,8) NOT NULL DEFAULT 0,
    started_at    TIMESTAMPTZ NOT NULL,
    ended_at      TIMESTAMPTZ,
    duration_secs NUMERIC(10,2)
);

CREATE INDEX inference_events_user_idx
    ON inference_events(user_id, started_at DESC);
CREATE INDEX inference_events_workspace_idx
    ON inference_events(workspace_id, started_at DESC);
```

Token accumulation (upsert on each session.updated delta):
```sql
INSERT INTO inference_events
    (workspace_id, user_id, session_id, model_id, provider_id,
     tier, input_tokens, output_tokens, cost_dollars, started_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
ON CONFLICT (session_id) DO UPDATE SET
    input_tokens  = inference_events.input_tokens  + EXCLUDED.input_tokens,
    output_tokens = inference_events.output_tokens + EXCLUDED.output_tokens,
    cost_dollars  = inference_events.cost_dollars  + EXCLUDED.cost_dollars;
```

Session close (on session.status = idle):
```sql
UPDATE inference_events
SET ended_at      = NOW(),
    duration_secs = EXTRACT(EPOCH FROM NOW() - started_at)
WHERE session_id = $1;
```

**Inference billing query:**
```sql
SELECT
    u.email,
    SUM(ie.input_tokens + ie.output_tokens) AS total_tokens,
    SUM(ie.cost_dollars)                    AS cost_usd
FROM inference_events ie
JOIN users u ON u.id = ie.user_id::uuid
WHERE ie.started_at >= date_trunc('month', NOW())
GROUP BY u.email
ORDER BY cost_usd DESC;
```

**Combined invoice query:**
```sql
SELECT
    u.email,
    ROUND(SUM(cp.duration_secs) / 3600, 4)  AS compute_hours,
    SUM(ie.cost_dollars)                      AS inference_cost_usd
FROM users u
LEFT JOIN compute_periods cp
    ON cp.user_id = u.id::text
    AND cp.started_at >= date_trunc('month', NOW())
    AND cp.ended_at IS NOT NULL
LEFT JOIN inference_events ie
    ON ie.user_id = u.id::text
    AND ie.started_at >= date_trunc('month', NOW())
GROUP BY u.email
ORDER BY compute_hours DESC;
```

---

### `workspace_events` — operational event log

```sql
CREATE TABLE workspace_events (
    id           UUID        DEFAULT gen_random_uuid() PRIMARY KEY,
    workspace_id TEXT,                               -- NULL for fleet-level events
    user_id      TEXT,                               -- NULL for fleet-level events
    event_type   TEXT        NOT NULL,
    severity     TEXT        NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    source       TEXT        NOT NULL,               -- 'agentd' | 'controller' | 'api'
    detail       JSONB       NOT NULL DEFAULT '{}',
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX workspace_events_workspace_idx
    ON workspace_events(workspace_id, occurred_at DESC)
    WHERE workspace_id IS NOT NULL;
CREATE INDEX workspace_events_user_idx
    ON workspace_events(user_id, occurred_at DESC)
    WHERE user_id IS NOT NULL;
CREATE INDEX workspace_events_type_severity_idx
    ON workspace_events(event_type, severity, occurred_at DESC);
```

**Event catalog:**

| event_type | Source | Tier | Severity | detail |
|---|---|---|---|---|
| `pod_ready` | agentd | 1 | info | `runtime`, `security_level` |
| `pod_suspended` | agentd | 1 | info | `reason` |
| `pod_resumed` | agentd | 1 | info | — |
| `pod_terminated` | agentd | 1 | info | `reason`, `exit_code` |
| `session_completed` | agentd | 1 | info | `session_id`, `duration_secs`, `total_tokens` |
| `session_interrupted` | agentd | 1 | warning | `session_id`, `reason` |
| `workspace_failed` | controller | 1 | critical | `failure_class`, `consecutive_failures`, `last_error` |
| `workspace_recovery_exhausted` | controller | 1 | critical | `failure_class`, `total_attempts` |
| `workspace_safe_mode_entered` | controller | 1 | critical | `consecutive_failures` |
| `workspace_oom_killed` | controller | 1 | warning | `memory_limit_bytes`, `memory_used_bytes` |
| `workspace_created` | controller | 2 | info | `runtime`, `security_level`, `create_duration_secs` |
| `workspace_deleted` | controller | 2 | info | `lifetime_secs` |
| `workspace_recovery_started` | controller | 2 | warning | `failure_class`, `attempt_number` |
| `workspace_recovery_succeeded` | controller | 2 | info | `failure_class`, `attempt_number` |
| `workspace_init_slow` | controller | 2 | warning | `init_duration_secs` |
| `auth_failure` | api | 2 | warning | `reason`, `ip_prefix` |
| `account_locked` | api | 2 | warning | `failure_count` |
| `api_key_created` | api | 2 | info | `key_prefix` |
| `api_key_revoked` | api | 2 | info | `key_prefix` |

**Tier 1** events are billing boundaries or incident-critical records. Loss means
incorrect charges or incomplete post-mortems. WAL-protected in agentd, written
synchronously in the gateway before 202 response.

**Tier 2** events are informational. Loss means incomplete history, no billing
impact. Written async by the gateway, best-effort.

**Incident impact query:**
```sql
SELECT DISTINCT
    u.email,
    we.workspace_id,
    we.event_type,
    we.detail,
    we.occurred_at
FROM workspace_events we
JOIN users u ON u.id = we.user_id::uuid
WHERE we.occurred_at BETWEEN :t1 AND :t2
  AND we.severity IN ('warning', 'critical')
ORDER BY we.occurred_at;
```

**Financial impact of an outage:**
```sql
SELECT
    we.user_id,
    we.workspace_id,
    we.detail->>'session_id'  AS session_id,
    ie.input_tokens,
    ie.output_tokens,
    ie.cost_dollars
FROM workspace_events we
LEFT JOIN inference_events ie
    ON  ie.workspace_id = we.workspace_id
    AND ie.session_id   = we.detail->>'session_id'
WHERE we.event_type  = 'session_interrupted'
  AND we.occurred_at BETWEEN :t1 AND :t2;
```

---

### `workspace_events_dlq` — dead-letter queue

```sql
CREATE TABLE workspace_events_dlq (
    id              UUID        DEFAULT gen_random_uuid() PRIMARY KEY,
    payload         JSONB       NOT NULL,
    error_message   TEXT        NOT NULL,
    retry_count     INTEGER     NOT NULL DEFAULT 0,
    first_failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_retried_at TIMESTAMPTZ,
    resolved_at     TIMESTAMPTZ,
    resolution      TEXT        CHECK (resolution IN ('reprocessed', 'discarded'))
);
```

---

## The `pkg/events` Package

A shared package imported by agentd, controller, API server, and gateway. Typed event
constants, typed detail structs, shared client interface, and shared test doubles.
Schema mismatches are compile-time errors, not runtime surprises.

```go
// pkg/events/types.go

type EventType string
type Severity  string
type Source    string
type PodState  string

const (
    // Tier 1 — billing-critical, WAL-protected
    EventPodReady                 EventType = "pod_ready"
    EventPodSuspended             EventType = "pod_suspended"
    EventPodResumed               EventType = "pod_resumed"
    EventPodTerminated            EventType = "pod_terminated"
    EventSessionCompleted         EventType = "session_completed"
    EventSessionInterrupted       EventType = "session_interrupted"
    EventWorkspaceFailed          EventType = "workspace_failed"
    EventWorkspaceRecoveryExhausted EventType = "workspace_recovery_exhausted"
    EventWorkspaceSafeModeEntered EventType = "workspace_safe_mode_entered"
    EventWorkspaceOOMKilled       EventType = "workspace_oom_killed"

    // Tier 2 — informational, best-effort
    EventWorkspaceCreated          EventType = "workspace_created"
    EventWorkspaceDeleted          EventType = "workspace_deleted"
    EventWorkspaceRecoveryStarted  EventType = "workspace_recovery_started"
    EventWorkspaceRecoverySucceeded EventType = "workspace_recovery_succeeded"
    EventWorkspaceInitSlow         EventType = "workspace_init_slow"
    EventAuthFailure               EventType = "auth_failure"
    EventAccountLocked             EventType = "account_locked"
    EventAPIKeyCreated             EventType = "api_key_created"
    EventAPIKeyRevoked             EventType = "api_key_revoked"
)

var tier1Events = map[EventType]bool{
    EventPodReady: true, EventPodSuspended: true,
    EventPodResumed: true, EventPodTerminated: true,
    EventSessionCompleted: true, EventSessionInterrupted: true,
    EventWorkspaceFailed: true, EventWorkspaceRecoveryExhausted: true,
    EventWorkspaceSafeModeEntered: true, EventWorkspaceOOMKilled: true,
}

func IsTier1(t EventType) bool { return tier1Events[t] }

type WorkspaceEvent struct {
    WorkspaceID *string         `json:"workspace_id,omitempty"`
    UserID      *string         `json:"user_id,omitempty"`
    EventType   EventType       `json:"event_type"`
    Severity    Severity        `json:"severity"`
    Source      Source          `json:"source"`
    Detail      json.RawMessage `json:"detail"`
    OccurredAt  time.Time       `json:"occurred_at"`
}

type ResourceSample struct {
    WorkspaceID      string    `json:"workspace_id"`
    UserID           string    `json:"user_id"`
    Timestamp        time.Time `json:"timestamp"`         // cgroup read time, not push time
    CPUUsageSeconds  float64   `json:"cpu_usage_seconds"` // cumulative, from cgroup
    MemoryBytes      int64     `json:"memory_bytes"`
    DiskBytes        int64     `json:"disk_bytes"`
}

type IngestRequest struct {
    StateEvents     []WorkspaceEvent `json:"state_events,omitempty"`
    ResourceSamples []ResourceSample `json:"resource_samples,omitempty"`
    InferenceEvents []InferenceEvent `json:"inference_events,omitempty"`
}
```

### The Writer interface

```go
// pkg/events/writer.go

type Writer interface {
    Write(ctx context.Context, evt WorkspaceEvent) error
    Flush(ctx context.Context) error   // blocks until async queue drained — tests + shutdown
    Start() error
    Stop(ctx context.Context) error
}

// NoopWriter — injected when gateway is not configured. Never nil. Safe by construction.
type NoopWriter struct{}
func (NoopWriter) Write(_ context.Context, _ WorkspaceEvent) error { return nil }
func (NoopWriter) Flush(_ context.Context) error                   { return nil }
func (NoopWriter) Start() error                                    { return nil }
func (NoopWriter) Stop(_ context.Context) error                    { return nil }

// RecordingWriter — for tests. Synchronous. No goroutines. No races.
type RecordingWriter struct {
    mu     sync.Mutex
    events []WorkspaceEvent
}
func (r *RecordingWriter) Write(_ context.Context, evt WorkspaceEvent) error {
    r.mu.Lock(); defer r.mu.Unlock()
    r.events = append(r.events, evt)
    return nil
}
func (r *RecordingWriter) EventsOfType(t EventType) []WorkspaceEvent { ... }
func (r *RecordingWriter) Flush(_ context.Context) error             { return nil }
func (r *RecordingWriter) Start() error                              { return nil }
func (r *RecordingWriter) Stop(_ context.Context) error              { return nil }
```

Every component receives `events.Writer` as an interface. In tests, inject
`RecordingWriter`. In production, inject `GatewayClient`. The null object pattern
(`NoopWriter`) eliminates nil checks everywhere.

---

## The GatewayClient (in agentd)

```go
// cmd/workspace-agentd/events/client.go

type GatewayClient struct {
    gatewayURL  string
    workspaceID string
    userID      string
    httpClient  *http.Client
    wal         *WAL                 // protects Tier 1 events
    ch          chan WorkspaceEvent   // Tier 2 async buffer
    resourceCh  chan ResourceSample   // resource sample buffer
    done        chan struct{}
    stopped     chan struct{}
    log         Logger

    // metrics
    tier1Written  prometheus.Counter
    tier2Written  prometheus.Counter
    dropped       prometheus.Counter
    walSize       prometheus.Gauge
}

func (c *GatewayClient) Write(ctx context.Context, evt WorkspaceEvent) error {
    evt.OccurredAt = time.Now()
    if IsTier1(evt.EventType) {
        return c.writeTier1(ctx, evt)
    }
    c.writeTier2(evt)
    return nil
}

func (c *GatewayClient) writeTier1(ctx context.Context, evt WorkspaceEvent) error {
    // 1. Write to WAL first — durable before any network call
    seq, err := c.wal.Append(evt)
    if err != nil {
        // WAL write failed (disk full, I/O error) — attempt direct push as fallback
        c.log.Error("WAL write failed, attempting direct push", "event_type", evt.EventType, "error", err)
        return c.pushWithRetry(ctx, []WorkspaceEvent{evt}, nil)
    }

    // 2. Push to gateway with retries
    writeCtx, cancel := contextWithMinDeadline(ctx, 500*time.Millisecond)
    defer cancel()
    if err := c.pushWithRetry(writeCtx, []WorkspaceEvent{evt}, nil); err != nil {
        // Push failed — WAL entry remains, will be replayed on next Start()
        // Do NOT return error — caller (reconciler, pod state handler) must continue
        c.log.Error("tier-1 push failed, WAL will replay on recovery",
            "event_type", evt.EventType, "seq", seq, "error", err)
        return nil
    }

    // 3. Confirm WAL entry — safe to compact
    c.wal.Confirm(seq)
    c.tier1Written.Inc()
    return nil
}

func (c *GatewayClient) writeTier2(evt WorkspaceEvent) {
    select {
    case c.ch <- evt:
    default:
        c.dropped.Inc()
    }
}

func (c *GatewayClient) PushResourceSample(sample ResourceSample) {
    select {
    case c.resourceCh <- sample:
    default:
        // resource samples are best-effort — drop silently on buffer full
        c.dropped.Inc()
    }
}
```

The run loop batches Tier 2 events and resource samples together and pushes once per
second:

```go
func (c *GatewayClient) run() {
    defer close(c.stopped)
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    var evtBuf   []WorkspaceEvent
    var sampleBuf []ResourceSample

    flush := func() {
        if len(evtBuf) == 0 && len(sampleBuf) == 0 {
            return
        }
        req := IngestRequest{
            StateEvents:     evtBuf,
            ResourceSamples: sampleBuf,
        }
        ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
        defer cancel()
        // one retry on transient error
        for attempt := 0; attempt < 2; attempt++ {
            if err := c.post(ctx, req); err == nil {
                break
            } else if attempt == 0 {
                time.Sleep(200 * time.Millisecond)
            }
        }
        evtBuf   = evtBuf[:0]
        sampleBuf = sampleBuf[:0]
    }

    for {
        select {
        case evt := <-c.ch:
            evtBuf = append(evtBuf, evt)
            if len(evtBuf) >= 100 { flush() }

        case sample := <-c.resourceCh:
            sampleBuf = append(sampleBuf, sample)
            if len(sampleBuf) >= 100 { flush() }

        case <-ticker.C:
            flush()

        case <-c.done:
            for len(c.ch) > 0        { evtBuf   = append(evtBuf, <-c.ch) }
            for len(c.resourceCh) > 0 { sampleBuf = append(sampleBuf, <-c.resourceCh) }
            flush()
            return
        }
    }
}
```

`Stop` blocks until drain completes, preventing event loss on graceful shutdown:

```go
func (c *GatewayClient) Stop(ctx context.Context) error {
    close(c.done)
    select {
    case <-c.stopped: return nil
    case <-ctx.Done(): return fmt.Errorf("gateway client drain timeout: %w", ctx.Err())
    }
}
```

On `Start()`, replay unconfirmed WAL entries before accepting new events:

```go
func (c *GatewayClient) Start() error {
    entries, err := c.wal.UnconfirmedEntries()
    if err != nil {
        c.log.Warn("WAL replay read failed — some tier-1 events may be lost", "error", err)
    }
    for _, entry := range entries {
        c.log.Info("replaying unconfirmed WAL entry", "event_type", entry.Event.EventType, "seq", entry.Seq)
        c.pushWithRetry(context.Background(), []WorkspaceEvent{entry.Event}, nil)
        c.wal.Confirm(entry.Seq)
    }
    go c.run()
    return nil
}
```

---

## The WAL

File-per-entry design. Confirmed entries deleted immediately. WAL size is bounded by
the number of unconfirmed Tier 1 events — under normal operation near zero.

```go
// cmd/workspace-agentd/events/wal.go

type WAL struct {
    dir        string
    maxPending int          // hard cap (default 10000)
    mu         sync.Mutex
    seq        uint64
    pending    int64        // atomic count for fast cap check
    size       prometheus.Gauge
}

func (w *WAL) Append(evt WorkspaceEvent) (uint64, error) {
    if atomic.LoadInt64(&w.pending) >= int64(w.maxPending) {
        return 0, ErrWALFull
    }
    seq := atomic.AddUint64(&w.seq, 1)
    data, _ := json.Marshal(WALEntry{Seq: seq, Event: evt})
    path := filepath.Join(w.dir, fmt.Sprintf("%016d.pending", seq))
    // write to tmp then atomic rename — either file exists complete or not at all
    tmp := path + ".tmp"
    if err := os.WriteFile(tmp, data, 0600); err != nil {
        return 0, err
    }
    if err := os.Rename(tmp, path); err != nil {
        os.Remove(tmp)
        return 0, err
    }
    atomic.AddInt64(&w.pending, 1)
    w.size.Set(float64(atomic.LoadInt64(&w.pending)))
    return seq, nil
}

func (w *WAL) Confirm(seq uint64) error {
    path := filepath.Join(w.dir, fmt.Sprintf("%016d.pending", seq))
    err := os.Remove(path)
    if err == nil {
        atomic.AddInt64(&w.pending, -1)
        w.size.Set(float64(atomic.LoadInt64(&w.pending)))
    }
    return err
}

func (w *WAL) UnconfirmedEntries() ([]WALEntry, error) {
    paths, _ := filepath.Glob(filepath.Join(w.dir, "*.pending"))
    sort.Strings(paths) // replay in sequence order
    var entries []WALEntry
    for _, p := range paths {
        data, err := os.ReadFile(p)
        if err != nil { continue }
        var entry WALEntry
        if err := json.Unmarshal(data, &entry); err != nil { continue }
        entries = append(entries, entry)
    }
    return entries, nil
}
```

**WAL size analysis:**

Under normal operation: Tier 1 events are confirmed within milliseconds of the gateway
POST succeeding. WAL contains 0–2 entries at any instant.

Under gateway outage: One WAL entry per Tier 1 event during the outage. Tier 1 events
are state transitions — rare. A workspace running continuously through a 1-hour outage
produces 0 new WAL entries (it was already in `pod_ready` state). A workspace that
starts during the outage produces 1 entry (`pod_ready`). A workspace that suspends
during the outage produces 1 entry (`pod_suspended`).

The heartbeat (resource samples) is Tier 2 — not WAL-protected. It does not accumulate
in the WAL during an outage.

**maxPending = 10000** entries × ~300 bytes = 3MB maximum WAL size regardless of how
long the pod runs or how long the gateway is down. The emptyDir volume is capped at
50Mi — the WAL cannot fill it.

---

## The events-gateway

### Ingest endpoint

```go
// cmd/events-gateway/main.go

func (g *Gateway) handleIngest(c *gin.Context) {
    var req events.IngestRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }

    // Tier 1 state events — write to Postgres before responding
    // These are billing boundaries. Gateway must durably record them
    // before returning 202.
    if len(req.StateEvents) > 0 {
        tier1, tier2 := splitByTier(req.StateEvents)
        if len(tier1) > 0 {
            if err := g.pg.WriteTier1Events(c.Request.Context(), tier1); err != nil {
                // Postgres write failed — return 503 so agentd retries from WAL
                g.log.Error("tier-1 event write failed", "error", err, "count", len(tier1))
                c.JSON(503, gin.H{"error": "upstream unavailable"})
                return
            }
        }
        // Tier 2 events — async, do not block response
        if len(tier2) > 0 {
            g.asyncWriter.Enqueue(tier2)
        }
    }

    // Resource samples → VictoriaMetrics only (not Postgres)
    // Non-blocking — failure does not affect billing
    if len(req.ResourceSamples) > 0 {
        g.vmWriter.EnqueueSamples(req.ResourceSamples)
        // update last_heartbeat_at for open compute_periods
        g.pg.UpdateHeartbeats(req.ResourceSamples)
    }

    // Inference events — async write to inference_events table
    if len(req.InferenceEvents) > 0 {
        g.asyncWriter.EnqueueInference(req.InferenceEvents)
    }

    c.Status(202)
}
```

Key invariant: **the gateway returns 503 if a Tier 1 Postgres write fails.** agentd
sees 503, does not confirm the WAL entry, retries. The WAL entry persists until the
write succeeds. Billing period boundaries are never silently lost.

### Fan-out to VictoriaMetrics

The gateway converts state events and resource samples to Prometheus remote_write
format and writes to VictoriaMetrics:

```
# pod_ready event → gauge
llmsafespace_workspace_pod_state{workspace_id="ws-abc", user_id="user-456", state="ready"} 1 @timestamp_ms

# pod_terminated event → gauge
llmsafespace_workspace_pod_state{workspace_id="ws-abc", user_id="user-456", state="ready"} 0 @timestamp_ms

# resource sample → gauges with exact cgroup read timestamp
llmsafespace_workspace_cpu_usage_seconds_total{workspace_id="ws-abc", user_id="user-456"} 847.3 @timestamp_ms
llmsafespace_workspace_memory_bytes{workspace_id="ws-abc", user_id="user-456"} 536870912 @timestamp_ms
llmsafespace_workspace_disk_bytes{workspace_id="ws-abc", user_id="user-456"} 2147483648 @timestamp_ms
```

The timestamp is from `ResourceSample.Timestamp` — the moment agentd read the cgroup,
not the moment the gateway received the push. This is the key property of the design:
exact measurement timestamps, not "whenever it arrived."

### Gateway self-metrics

```
llmsafespace_gateway_events_received_total{event_type, source, tier}
llmsafespace_gateway_events_written_total{store}             -- 'postgres' | 'victoriametrics'
llmsafespace_gateway_events_dropped_total{reason}
llmsafespace_gateway_tier1_write_duration_seconds
llmsafespace_gateway_batch_write_duration_seconds
llmsafespace_gateway_buffer_occupancy                        -- alert at 0.8
llmsafespace_gateway_dlq_size
llmsafespace_gateway_vm_write_errors_total
llmsafespace_gateway_pg_write_errors_total
```

### Gateway gap-closer interaction

The controller calls the gateway's event endpoint (same `POST /ingest`) with
`source = "controller_gap_close"`. The gateway handles it identically to agentd-sourced
events — writes to Postgres and VictoriaMetrics. The controller does not write to
Postgres or VictoriaMetrics directly.

---

## Controller gap detection

One new reconciliation function. Runs every 60 seconds.

```go
func (r *WorkspaceReconciler) reconcileStaleComputePeriods(ctx context.Context) {
    // Find open periods where agentd has gone silent
    stale, err := r.db.GetStaleOpenPeriods(ctx, 90*time.Second)
    if err != nil {
        r.log.Warn("stale period query failed", "error", err)
        return
    }

    for _, period := range stale {
        // Critical: verify the pod is actually gone before closing.
        // A slow gateway causing delayed heartbeats must not close an active period.
        pod, err := r.getPod(ctx, period.WorkspaceID)
        if err == nil && pod.Status.Phase == corev1.PodRunning {
            r.log.Warn("stale heartbeat but pod still running — gateway may be slow",
                "workspace_id", period.WorkspaceID,
                "last_heartbeat", period.LastHeartbeatAt)
            continue  // do not close — false positive
        }

        // Pod is gone or unresolvable — safe to close
        endTime := r.getBestEndTime(ctx, period.WorkspaceID, pod)

        r.eventWriter.Write(ctx, events.WorkspaceEvent{
            WorkspaceID: &period.WorkspaceID,
            UserID:      &period.UserID,
            EventType:   events.EventPodTerminated,
            Severity:    "info",
            Source:      "controller_gap_close",
            Detail:      json.RawMessage(`{"reason":"stale_heartbeat"}`),
            OccurredAt:  endTime,
        })
        // Gateway handles the compute_period close via the pod_terminated event
    }
}

func (r *WorkspaceReconciler) getBestEndTime(
    ctx context.Context,
    workspaceID string,
    pod *corev1.Pod,
) time.Time {
    // Preference order:
    // 1. Pod deletion timestamp (most accurate)
    // 2. Last heartbeat time (best available estimate)
    // 3. Now (conservative fallback — may slightly overcharge)
    if pod != nil && pod.DeletionTimestamp != nil {
        return pod.DeletionTimestamp.Time
    }
    stale, err := r.db.GetLastHeartbeat(ctx, workspaceID)
    if err == nil && !stale.IsZero() {
        return stale
    }
    return time.Now()
}
```

---

## Cardinality fixes (existing metrics)

Before building dashboards on top of existing metrics, fix the anti-patterns:

**`api_active_connections{type, user_id}`** — remove `user_id`. O(users) cardinality.
Fleet-level connection count by type is the useful signal.

**`workspaces_created_total{runtime, user_id}`** in API service — delete. The
controller's `llmsafespace_workspaces_created_total{runtime, security_level}` is
authoritative. Two metrics for the same thing with different label sets is ambiguous.

**`workspace_resource_usage{workspace_id, resource_type}`** — delete. Never populated
with real data. Superseded by agentd push via gateway.

**`RecordRelayInjector`** in API service metrics.go — delete. Unused. The correct
registration is in `cmd/workspace-agentd/relay_injector.go`.

---

## VictoriaMetrics scrape architecture

vmagent scrapes three targets:
1. **cAdvisor** on every node — fleet-level node and pod resource data, 15s interval,
   relabeled with `workspace_id` and `user_id` from pod labels for fleet-level views
2. **API server** `/metrics` — HTTP, auth, session, inference fleet counters, 15s
3. **Controller** `/metrics` — operational counters, 15s
   (controller `metricsAddr` changed from `127.0.0.1:8080` to `0.0.0.0:8081`,
   NetworkPolicy restricts to vmagent only)

Per-workspace second-granularity resource data comes from agentd push via the gateway,
not from cAdvisor scraping. cAdvisor provides fleet-level and node-level aggregates.

---

## Alerting rules

Evaluated by vmagent. Alertmanager routing is operator-configured — out of scope.

| Alert | Expression | Severity |
|---|---|---|
| `WorkspaceSafeModeActive` | `llmsafespace_workspace_safe_mode_active > 0` | critical |
| `WorkspaceConsecutiveFailuresHigh` | `llmsafespace_workspace_consecutive_failures_max > 5` | warning |
| `WorkspaceFailureRateElevated` | `rate(llmsafespace_workspaces_failed_total[5m]) > 0.1` | warning |
| `WorkspaceRecoveryRateHigh` | `rate(llmsafespace_workspace_recovery_attempts_total[5m]) > 0.5` | warning |
| `ReconciliationErrorRateHigh` | `rate(llmsafespace_reconciliation_errors_total[5m]) > 0.1` | warning |
| `AuthFailureRateSpiked` | `rate(llmsafespace_auth_failures_total[5m]) * 60 > THRESHOLD` | warning |
| `InferenceCostBurnRateHigh` | `rate(llmsafespace_inference_cost_dollars_total[1h]) * 3600 > THRESHOLD` | warning |
| `AgentReloadDrainTimeouts` | `increase(llmsafespace_agent_reload_drain_timeouts_total[10m]) > 0` | warning |
| `WorkspaceCreateP99Slow` | `histogram_quantile(0.99, rate(llmsafespace_workspace_create_duration_seconds_bucket[5m])) > THRESHOLD` | warning |
| `WorkspaceResumeP99Slow` | `histogram_quantile(0.99, rate(llmsafespace_workspace_resume_duration_seconds_bucket[5m])) > THRESHOLD` | warning |
| `APIKeyLegacyKeysRemaining` | `llmsafespace_api_key_legacy_total > 0` | info |
| `ObservabilityBlind` | `rate(vm_rows_inserted_total[5m]) == 0` | critical |
| `GatewayDropping` | `rate(llmsafespace_gateway_events_dropped_total[5m]) > 0` | warning |
| `GatewayBufferPressure` | `llmsafespace_gateway_buffer_occupancy > 0.8` | warning |
| `WALSizeHigh` | `llmsafespace_agentd_wal_pending_entries > 100` | warning |
| `ComputePeriodStale` | `time() - llmsafespace_agentd_last_heartbeat_push > 10` | warning |

All THRESHOLD values configurable via `values.yaml`.

---

## Dashboards

### Operational dashboard

**Fleet Capacity:** `workspaces_running` by runtime/security_level, creation/deletion rates.  
**Reliability:** Failure rate, recovery attempts vs. successes, safe-mode count (red ≥ 1), consecutive failures max.  
**Latency SLOs:** Create P50/P99 with 60s reference line. Resume P50/P99 with 3s reference line.  
**API Health:** Request rate by status class, error rate, P99 latency by endpoint.  
**Auth:** Failure rate by reason, spike vs. baseline.  
**Agent Reload:** Success/failure rate, P99 duration, drain timeout occurrences.  
**Controller Health:** Reconciliation P50/P99 by resource, error rate, status update conflicts.  
**Gateway Health:** Events received/written/dropped, buffer occupancy, DLQ size, VM write errors.  

### Usage dashboard

**Pod State Fleet View:** Active workspace count derived from `workspace_pod_state{state="ready"}` gauge sum.  
**Resource Consumption (second-granularity):** CPU usage rate per workspace (top-10), memory per workspace (top-10), disk per workspace — all from agentd push, exact cgroup timestamps.  
**Inference Traffic:** Request rate by model/provider, free/paid split, model distribution pie, session duration P50/P99.  
**Token Throughput:** Input/output tokens per minute fleet-wide, output/input ratio (runaway session detection).  
**Cost:** Fleet USD burn rate (dollars/hour), cumulative cost today, cost by provider/model.  
**Per-Customer (Postgres datasource):** Top-N workspaces by compute hours this month, top-N users by inference cost, combined invoice preview.  

---

## User Stories

---

### US-33.1 — Fix cardinality bugs in API service metrics

Remove `user_id` from `api_active_connections`. Delete `workspaces_created_total`,
`workspace_resource_usage`, and `RecordRelayInjector` from the API service. Update
interface, mock, and all call sites.

**Definition of done:** `go build ./api/...` and `go test ./api/...` pass. No duplicate
metric registrations. No `user_id` on connection gauge.

---

### US-33.2 — VictoriaMetrics + Grafana deployment

Single-node VictoriaMetrics with 90-day retention, PVC, cluster-internal NetworkPolicy.
Grafana with VictoriaMetrics datasource (default) and Postgres datasource (for per-customer
panels). Admin credentials from existing `secret.yaml` pattern. Grafana ingress at
`grafana.safespaces.dev`.

**New chart templates:** `victoria-metrics-*.yaml`, `grafana-*.yaml`  
**New values sections:** `victoriaMetrics:`, `grafana:`  

**Definition of done:** Both pods reach Ready. VictoriaMetrics `/health` returns 200.
Grafana datasources connected. Credentials from K8s Secret.

---

### US-33.3 — vmagent + cAdvisor scrape config

vmagent deployment scraping cAdvisor (node SD, relabeling `workspace_id`/`user_id`
from pod labels, keeping only billing-relevant metrics), API server, and controller.
Controller `metricsAddr` changed to `0.0.0.0:8081` with NetworkPolicy restricting to
vmagent. remote_write to VictoriaMetrics.

**Definition of done:** All three sources visible in VictoriaMetrics within 30s of
install. `container_cpu_usage_seconds_total{workspace_id!=""}` returns data.

---

### US-33.4 — `pkg/events` shared package

Typed event constants, typed detail structs, `Writer` interface, `NoopWriter`,
`RecordingWriter`, `GatewayClient` (without WAL — see US-33.7). Unit tests for all
types. This package is imported by agentd, controller, API server, and gateway.

**Definition of done:** `go test ./pkg/events/...` passes. All event types defined as
typed constants. Interface has `Write`, `Flush`, `Start`, `Stop`.

---

### US-33.5 — events-gateway service

New `cmd/events-gateway/` binary. HTTP server (Gin). `POST /ingest` endpoint.
Fan-out to VictoriaMetrics (async, best-effort) and Postgres (Tier 1 sync, Tier 2
async). Returns 503 if Tier 1 Postgres write fails. Gateway self-metrics. Helm
templates (2-replica Deployment, Service, NetworkPolicy). Postgres scoped role
`events_gateway`.

**Definition of done:** `POST /ingest` with Tier 1 event writes to Postgres before
returning 202. `POST /ingest` returns 503 on Postgres failure. VictoriaMetrics receives
remote_write data from gateway. `go test ./cmd/events-gateway/...` passes.

---

### US-33.6 — Postgres event table migrations

Migration `000018_compute_periods.up.sql` — `compute_periods` table, indexes,
idempotency constraint.  
Migration `000019_inference_events.up.sql` — `inference_events` table, indexes,
session_id unique constraint.  
Migration `000020_workspace_events.up.sql` — `workspace_events` and
`workspace_events_dlq` tables, indexes, severity constraint.  
All three synced to `charts/llmsafespace/migrations/`.

**Definition of done:** `make migrate-up` and `make migrate-down` clean. All indexes
and constraints present. Billing queries in data model section execute correctly.

---

### US-33.7 — agentd WAL + GatewayClient with Tier 1 durability

`cmd/workspace-agentd/events/wal.go` — file-per-entry WAL in `/var/lib/agentd/wal/`.
Atomic rename on write. Immediate delete on confirm. `maxPending` cap with
`ErrWALFull`. Self-metrics (`llmsafespace_agentd_wal_pending_entries`).

Update `GatewayClient` to WAL-protect Tier 1 events: Append → POST → Confirm.
On gateway 503: WAL entry persists, replayed on next `Start()`. On WAL full: fallback
to direct POST, log ERROR.

`emptyDir` volume mount at `/var/lib/agentd/` added to pod spec by controller
(`pod_builder.go`).

**Definition of done:** Simulated gateway outage → WAL accumulates entries → gateway
recovers → WAL replays → entries confirmed → WAL empty. Crash-and-restart test:
unconfirmed WAL entries replayed on `Start()`. WAL never exceeds `maxPending`.
`go test ./cmd/workspace-agentd/events/...` passes.

---

### US-33.8 — agentd PodStateTracker + ResourceSampler

`cmd/workspace-agentd/events/pod_state.go` — `PodStateTracker` monitors agentd's own
health state. Detects transitions: agentd startup → `pod_ready`, suspend signal →
`pod_suspended`, resume signal → `pod_resumed`, shutdown → `pod_terminated`. Fires
`GatewayClient.Write` with appropriate Tier 1 event on each transition.

`cmd/workspace-agentd/events/resource_sampler.go` — `ResourceSampler` reads cgroup
data every 1 second (reuses existing `getCPUUsage`, `getDiskUsage`, `getMemoryUsage`
functions from `cmd/workspace-agentd/main.go`). Calls
`GatewayClient.PushResourceSample`. Timestamp is the cgroup read time.

`EVENTS_GATEWAY_URL` environment variable injected into pod spec by controller
(same pattern as `RELAY_CONFIG_PATH` in `relay_injection.go`).

**Definition of done:** After workspace reaches Active: `pod_ready` event in
`workspace_events`, open row in `compute_periods`. After workspace suspended:
`pod_suspended` event, row closed in `compute_periods` with correct `duration_secs`.
`compute_periods.last_heartbeat_at` updated every second while pod is running.
`go test ./cmd/workspace-agentd/...` passes with `RecordingWriter`.

---

### US-33.9 — agentd inference event emission

Extend SSE tracker `InferenceCallback` to push `InferenceEvent` to the gateway on each
session.updated token delta. Push session_completed event (Tier 1) on
`session.status = idle`. Push session_interrupted event (Tier 1) on SSE disconnect
while session was busy.

Gateway routes `InferenceEvent` to `inference_events` upsert (accumulates tokens)
rather than `workspace_events` insert.

**Definition of done:** After any inference session: row in `inference_events` with
correct token counts and `duration_secs`. Multiple token deltas accumulate correctly.
Session interruption produces `session_interrupted` row in `workspace_events`.

---

### US-33.10 — Controller event emission + gap detection

Controller workspace reconciler gains `events.Writer` (injected, `NoopWriter` default).
Emits `workspace_failed`, `workspace_recovery_exhausted`, `workspace_safe_mode_entered`,
`workspace_oom_killed` as Tier 1 events. Emits `workspace_created`, `workspace_deleted`,
`workspace_recovery_started`, `workspace_recovery_succeeded`, `workspace_init_slow` as
Tier 2 events. `user_id` resolved from `metadata.labels["llmsafespace.dev/user-id"]`
— no database lookup.

`reconcileStaleComputePeriods` runs every 60s. Detects open `compute_periods` rows with
stale `last_heartbeat_at`. Verifies pod is actually gone before closing (prevents false
positives when gateway is slow). Emits `pod_terminated` event via gateway with best
available end time.

**Definition of done:** Simulated workspace failure → `workspace_failed` in
`workspace_events`. Simulated agentd crash (kill -9) followed by workspace deletion →
open `compute_periods` row closed by controller with `source = 'controller_gap_close'`.
Pod still running with stale heartbeat → controller does NOT close the period.
`go test ./controller/...` passes with `RecordingWriter`.

---

### US-33.11 — API server event emission

Auth service emits `auth_failure`, `account_locked` (Tier 2) via `events.Writer`.
API key service emits `api_key_created`, `api_key_revoked` (Tier 2). Writer injected
in `app.go`, `NoopWriter` default.

**Definition of done:** Failed authentication → `auth_failure` row in
`workspace_events`. Account lockout → `account_locked` row.

---

### US-33.12 — Operational and usage dashboards + alerting

Operational dashboard provisioned as Grafana ConfigMap — all panels described in
dashboard section above. Usage dashboard provisioned as Grafana ConfigMap.

Alert rules ConfigMap with all 16 alerts defined above. All THRESHOLD values from
`values.yaml`.

**Definition of done:** Both dashboards render with live data within 15 minutes of
cluster activity. `WorkspaceSafeModeActive` alert fires when metric set to 1 in test.
All configurable thresholds read from `values.yaml`.

---

## Dependency Graph

```
US-33.1  (cardinality fixes)       — independent, do first
US-33.2  (VictoriaMetrics+Grafana) — independent
US-33.3  (vmagent+cAdvisor)        — requires US-33.2
US-33.4  (pkg/events)              — independent
US-33.5  (events-gateway)          — requires US-33.4, US-33.6
US-33.6  (Postgres migrations)     — independent
US-33.7  (agentd WAL+client)       — requires US-33.4, US-33.5
US-33.8  (agentd pod state+sampler)— requires US-33.7
US-33.9  (agentd inference)        — requires US-33.7
US-33.10 (controller events+gaps)  — requires US-33.4, US-33.5
US-33.11 (API server events)       — requires US-33.4, US-33.5
US-33.12 (dashboards+alerts)       — requires US-33.3, US-33.5
```

**Recommended phases:**

```
Phase 1 (parallel): US-33.1, US-33.2, US-33.4, US-33.6
Phase 2 (parallel): US-33.3, US-33.5
Phase 3 (parallel): US-33.7, US-33.10, US-33.11
Phase 4 (parallel): US-33.8, US-33.9, US-33.12
```

---

## Known Weaknesses and Mitigations

| Weakness | Severity | Mitigation | Status |
|---|---|---|---|
| WAL on emptyDir lost on pod deletion | Low | Controller gap-closer closes open periods; best end time from pod DeletionTimestamp | Designed in US-33.10 |
| Controller gap-closer race (slow gateway → false stale) | Medium | Verify pod is actually gone before closing period | Designed in US-33.10 |
| Gateway SPOF for per-workspace VM metrics | Low | 2-3 replicas; cAdvisor covers fleet-level fallback | Architectural |
| Dual-write inconsistency (VM vs. Postgres) | Low | Idempotent operations on both sides; replay-safe | Architectural |
| Network partition between agentd and gateway | Low | Gap-closer race mitigation covers this; billing period stays open | Architectural |

---

## Relationship to Epic 12

Epic 12 (Usage Metering & Billing) designs a generalised `usage_events` table and
billing provider integration. Epic 33 is the foundation: correct ground-truth metering,
exact billing period boundaries, per-session inference records. Epic 12 builds on top.

`compute_periods` maps to Epic 12's `compute_seconds` event type.
`inference_events` maps to Epic 12's `llm_tokens` event type.
The transition is additive. Epic 33 does not need to be replaced when Epic 12 ships.

---

## Non-Requirements (explicitly out of scope)

- Alertmanager routing and notification channels — operator-configured
- VictoriaMetrics cluster mode — single-node sufficient to ~100k workspaces
- Distributed tracing — future epic
- Log aggregation (Loki/ELK) — separate concern
- Billing provider integration — Epic 12
- Quota enforcement — Epic 12
- Customer-facing usage API endpoints — Epic 12
- Per-second cAdvisor scraping — agentd push covers per-workspace second-granularity;
  cAdvisor at 15s is sufficient for fleet-level operational views
