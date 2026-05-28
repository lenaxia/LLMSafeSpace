# Epic 12: Usage Metering & Billing

**Status:** Planning
**Created:** 2026-05-28
**Depends On:** Epic 6 (Collapse Sandbox into Workspace), Epic 10 (Multi-Tenant Trust)
**Priority:** High

**Motivation:** LLMSafeSpace needs per-tenant usage tracking for billing, cost attribution, quota enforcement, and abuse detection. The platform must meter compute time, LLM requests/tokens, storage, and API calls — attributable to individual users today and rollable up to organizations in the future (Epic 11).

---

## Problem Statement

### Current State

1. **No per-tenant metering.** Existing Prometheus metrics lack `user_id` labels. Cannot answer "how much did user X consume?"
2. **No billing-grade event log.** Prometheus is operational (lossy, retention-limited). No append-only audit trail suitable for invoicing.
3. **No compute time tracking.** No record of how long each workspace pod runs per user.
4. **No LLM usage attribution.** The proxy forwards requests to opencode but doesn't count messages or tokens per user.
5. **No quota enforcement.** Rate limiting exists (requests/second) but no usage caps (e.g., "1000 LLM requests/month on free tier").
6. **No billing provider integration point.** No interface for exporting usage to Stripe/Lago/Metronome.

### Design Principles

1. **Owner-aware from day one.** Every usage event records `owner_id + owner_type` (user | org). When Epic 11 adds organizations, billing rolls up without schema migration.
2. **Actor ≠ Owner.** The `actor_id` (who performed the action) is always a user, even within an org-owned workspace. Enables per-member cost breakdown.
3. **Metering never blocks requests.** Usage recording is async (buffered channel → batch write). A metering failure must not degrade the user experience.
4. **At-least-once with idempotency.** Events may be delivered more than once (retries, reconciliation). Idempotency keys prevent double-counting.
5. **Billing provider owns pricing, plans, and invoices.** We emit raw usage events. The billing provider (Stripe/Lago) handles rate cards, plan management, proration, invoicing, and payment collection. We do not replicate their functionality.
6. **Reconcilable.** Periodic jobs detect and fill gaps in compute time metering. Every correction is auditable.

### Build vs. Buy Decisions

| Concern | Decision | Rationale |
|---------|----------|-----------|
| Usage event storage | **Build** | Our source of truth for disputes, reconciliation, quota. Must survive provider migration. |
| Async buffered writes | **Build** | ~100 lines of Go. Too simple for a dependency. |
| Idempotent dedup | **Build** | One SQL clause (`ON CONFLICT DO NOTHING`). |
| Dead-letter queue | **Build** | A Postgres table is simpler than adding a message broker. |
| Quota enforcement | **Build** | Real-time, in-path. Can't round-trip to external provider per request. |
| Compute time reconciliation | **Build** | No provider does this. K8s events aren't billing-grade (1h retention). |
| Pricing / rate cards | **Buy** | Billing provider's core job. Don't maintain a parallel rate card. |
| Plan/subscription management | **Buy** | Billing provider handles upgrades, downgrades, proration. |
| Invoice generation | **Buy** | Billing provider handles this entirely. |
| Credits / refunds | **Buy** | Emit credit event to provider. They handle the accounting. |
| Audit log | **Reuse** | Generalize Epic 10's `secret_audit_log` into a shared `audit_log` table. |

---

## Architecture

### Data Flow

```
┌─────────────────────────────────────────────────────────────────────┐
│  Request Path (non-blocking)                                        │
│                                                                     │
│  API Handler ──→ MeteringService.Record(event) ──→ buffered channel │
│       │                                                             │
│       ▼                                                             │
│  Response to client (metering is fire-and-forget)                   │
└─────────────────────────────────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────────────────┐
│  Background Writer (goroutine)                                      │
│                                                                     │
│  channel ──→ batch (up to 100 events or 1s flush) ──→ INSERT        │
│                                                          │          │
│                                              on failure: │          │
│                                                          ▼          │
│                                              Redis retry queue      │
│                                                          │          │
│                                              after 5 retries:       │
│                                                          ▼          │
│                                              usage_events_dlq       │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│  Controller (compute time)                                          │
│                                                                     │
│  Workspace reconciler:                                              │
│    - Every 60s heartbeat: emit compute_seconds for Active pods      │
│    - On phase transition out of Active: emit final compute_seconds  │
│    - On every phase transition: emit workspace_lifecycle_event      │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│  Reconciliation Cron (every 5 min)                                  │
│                                                                     │
│  Compare K8s pod uptime vs recorded compute_seconds                 │
│  Emit catch-up events for gaps > 90s                                │
│  Source = 'reconciliation'                                          │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│  Billing Export (every 5 min)                                       │
│                                                                     │
│  Read usage_events WHERE id > cursor                                │
│  Map owner → billing_accounts.external_customer_id                  │
│  Call BillingProvider.ReportUsage(batch)                             │
│  Advance cursor                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Billing Owner Resolution

```
resolveBillingOwner(ctx, userID, workspaceID):
  1. Get workspace CRD → spec.owner
  2. If workspace has orgID set → return {ID: orgID, Type: "org"}
  3. Else → return {ID: userID, Type: "user"}

Today: always returns user (orgs don't exist yet).
Future (Epic 11): resolves org ownership from workspace spec.
```

---

## Data Model

### usage_events (Core metering table — append-only, our source of truth)

```sql
CREATE TABLE usage_events (
    id              BIGSERIAL PRIMARY KEY,
    idempotency_key TEXT UNIQUE,              -- dedup for periodic/retried emitters

    -- Billing target
    owner_id        TEXT NOT NULL,            -- user_id or org_id
    owner_type      TEXT NOT NULL DEFAULT 'user',  -- 'user' | 'org'

    -- Actor (always a user, even within an org)
    actor_id        TEXT NOT NULL,

    -- Resource
    workspace_id    TEXT,

    -- Classification
    event_type      TEXT NOT NULL,            -- compute_seconds | llm_request | llm_tokens | storage_bytes | api_call
    event_subtype   TEXT,                     -- input_tokens | output_tokens | active | suspended | read | write

    -- Quantitative
    quantity        BIGINT NOT NULL,

    -- Pricing context (immutable at write time — used by billing provider for tiered pricing)
    resource_tier   TEXT,                     -- small | medium | large | gpu
    region          TEXT,                     -- us-west-2 | eu-west-1

    -- Dimensional metadata
    metadata        JSONB NOT NULL DEFAULT '{}',  -- provider, model, method, path, etc.

    -- Request context (for dispute resolution)
    request_context JSONB,                    -- ip, api_key_id, session_id, user_agent, request_id

    -- Source
    source          TEXT NOT NULL DEFAULT 'api',  -- api | controller | cron | reconciliation

    -- Timing
    event_time      TIMESTAMPTZ NOT NULL,     -- when usage actually occurred
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    period          DATE NOT NULL DEFAULT CURRENT_DATE
);

CREATE INDEX idx_usage_owner_period ON usage_events(owner_id, owner_type, period);
CREATE INDEX idx_usage_actor_period ON usage_events(actor_id, period);
CREATE INDEX idx_usage_workspace ON usage_events(workspace_id, period);
CREATE INDEX idx_usage_type_period ON usage_events(event_type, period);
CREATE INDEX idx_usage_idempotency ON usage_events(idempotency_key) WHERE idempotency_key IS NOT NULL;
```

### workspace_lifecycle_events (Phase transition history — immutable)

```sql
CREATE TABLE workspace_lifecycle_events (
    id              BIGSERIAL PRIMARY KEY,
    workspace_id    TEXT NOT NULL,
    owner_id        TEXT NOT NULL,
    owner_type      TEXT NOT NULL DEFAULT 'user',
    from_phase      TEXT,                     -- NULL for creation
    to_phase        TEXT NOT NULL,
    resource_tier   TEXT,
    region          TEXT,
    event_time      TIMESTAMPTZ NOT NULL,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_wle_workspace ON workspace_lifecycle_events(workspace_id, event_time);
CREATE INDEX idx_wle_owner ON workspace_lifecycle_events(owner_id, owner_type, event_time);
```

### usage_limits (Quota configuration — our responsibility, real-time enforcement)

```sql
CREATE TABLE usage_limits (
    id           BIGSERIAL PRIMARY KEY,
    owner_id     TEXT NOT NULL,
    owner_type   TEXT NOT NULL DEFAULT 'user',
    scope        TEXT NOT NULL DEFAULT 'owner',  -- 'owner' | 'actor'
    scope_id     TEXT,                           -- NULL for owner-level, actor_id for per-member
    event_type   TEXT NOT NULL,
    period_type  TEXT NOT NULL DEFAULT 'monthly', -- daily | monthly | lifetime
    max_quantity BIGINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(owner_id, owner_type, scope, scope_id, event_type, period_type)
);
```

### billing_accounts (Maps our owners to external billing provider)

```sql
CREATE TABLE billing_accounts (
    id                       BIGSERIAL PRIMARY KEY,
    owner_id                 TEXT NOT NULL,
    owner_type               TEXT NOT NULL DEFAULT 'user',
    provider                 TEXT NOT NULL,           -- stripe | lago | internal
    external_customer_id     TEXT NOT NULL,
    external_subscription_id TEXT,
    status                   TEXT NOT NULL DEFAULT 'active',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(owner_id, owner_type, provider)
);
```

### billing_export_cursor (Incremental sync high-water mark)

```sql
CREATE TABLE billing_export_cursor (
    id                  BIGSERIAL PRIMARY KEY,
    provider            TEXT NOT NULL UNIQUE,
    last_exported_id    BIGINT NOT NULL DEFAULT 0,
    last_exported_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### usage_events_dlq (Dead-letter queue for failed writes)

```sql
CREATE TABLE usage_events_dlq (
    id              BIGSERIAL PRIMARY KEY,
    payload         JSONB NOT NULL,
    error_message   TEXT NOT NULL,
    retry_count     INTEGER NOT NULL DEFAULT 0,
    first_failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_retried_at TIMESTAMPTZ,
    resolved_at     TIMESTAMPTZ,
    resolution      TEXT                     -- reprocessed | discarded
);
```

### Plan cache (column on existing users table)

```sql
-- No new table. Add column to existing users table:
ALTER TABLE users ADD COLUMN plan_id TEXT NOT NULL DEFAULT 'free';
-- Updated via webhook from billing provider when plan changes.
-- Used by quota enforcement to look up default limits per plan.
```

### Audit log (generalized from Epic 10's secret_audit_log)

```sql
-- Reuse/generalize Epic 10's audit_log pattern for all admin actions:
CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    actor_id    TEXT NOT NULL,              -- admin who performed the action
    domain      TEXT NOT NULL,              -- 'billing' | 'secrets' | 'admin'
    action      TEXT NOT NULL,              -- 'credit_issued' | 'limit_override' | 'dlq_discard' | etc.
    target_id   TEXT,                       -- owner_id, secret_id, etc.
    metadata    JSONB NOT NULL DEFAULT '{}', -- action-specific details
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_audit_domain ON audit_log(domain, created_at DESC);
CREATE INDEX idx_audit_actor ON audit_log(actor_id, created_at DESC);
```

### What the billing provider owns (NOT in our DB)

- Pricing / rate cards (Stripe Prices, Lago charges)
- Plan definitions and features (Stripe Products, Lago plans)
- Subscription lifecycle (upgrades, downgrades, proration)
- Invoice generation and delivery
- Payment collection and dunning
- Credit notes and refunds
- Tax calculation

---

## Event Types & Metering Points

| event_type | event_subtype | quantity unit | source | Emission trigger |
|-----------|---------------|--------------|--------|-----------------|
| `compute_seconds` | `active` | seconds | controller | Every 60s heartbeat while pod is Active + on phase transition out of Active |
| `compute_seconds` | `suspended` | seconds | controller | On resume — records PVC retention time (if billed) |
| `llm_request` | `message` | 1 | api (proxy) | Every `SendMessage` / `SendPromptAsync` returning 2xx |
| `llm_tokens` | `input` | token count | api (proxy) | Extracted from opencode response headers/body |
| `llm_tokens` | `output` | token count | api (proxy) | Extracted from opencode response headers/body |
| `storage_bytes` | `pvc` | bytes | cron (daily) | Periodic PVC usage scrape |
| `storage_bytes` | `shared_s3` | bytes | cron (daily) | S3 prefix size (Epic 10 US-10.7) |
| `api_call` | `read` | 1 | api middleware | Every authenticated GET returning 2xx |
| `api_call` | `write` | 1 | api middleware | Every authenticated POST/PUT/DELETE returning 2xx |

### Metering Instrumentation Points

| Component | File | Where to Instrument |
|-----------|------|-------------------|
| LLM request counting | `api/internal/handlers/proxy.go` | After successful `doProxy()` in `SendMessage` / `SendPromptAsync` |
| Compute time heartbeat | `controller/internal/workspace/controller.go` | In reconcile loop when workspace is Active |
| Compute time on transition | `controller/internal/workspace/controller.go` | On phase change from Active → any other phase |
| API call counting | `api/internal/middleware/` | New metering middleware (batches per-user counts) |
| Storage scrape | New cron job | Queries kubelet metrics or PVC capacity |
| Lifecycle events | `controller/internal/workspace/controller.go` | On every phase transition |

---

## Go Interfaces

### MeteringService (API-side)

```go
// api/internal/services/metering/metering.go

type OwnerType string
const (
    OwnerTypeUser OwnerType = "user"
    OwnerTypeOrg  OwnerType = "org"
)

type BillingOwner struct {
    ID   string
    Type OwnerType
}

type UsageEvent struct {
    IdempotencyKey string
    Owner          BillingOwner
    ActorID        string
    WorkspaceID    string
    EventType      string
    EventSubtype   string
    Quantity       int64
    ResourceTier   string
    Region         string
    Metadata       map[string]string
    RequestContext *RequestContext
    Source         string
    EventTime      time.Time
}

type RequestContext struct {
    IP        string `json:"ip,omitempty"`
    APIKeyID  string `json:"api_key_id,omitempty"`
    SessionID string `json:"session_id,omitempty"`
    UserAgent string `json:"user_agent,omitempty"`
    RequestID string `json:"request_id,omitempty"`
}

type UsageReport struct {
    OwnerID     string
    OwnerType   OwnerType
    PeriodFrom  time.Time
    PeriodTo    time.Time
    Totals      map[string]int64             // event_type → total quantity
    ByWorkspace map[string]map[string]int64  // workspace_id → event_type → quantity
}

type MeteringService interface {
    Record(ctx context.Context, event UsageEvent) error
    RecordBatch(ctx context.Context, events []UsageEvent) error
    CheckQuota(ctx context.Context, owner BillingOwner, eventType string) (allowed bool, remaining int64, err error)
    GetUsage(ctx context.Context, owner BillingOwner, from, to time.Time) (*UsageReport, error)
    Start() error
    Stop() error
}
```

### BillingProvider (Export to external system)

```go
// pkg/billing/provider.go

type BillingProvider interface {
    ReportUsage(ctx context.Context, events []UsageExportEvent) (reportedIDs []int64, err error)
    CreateCustomer(ctx context.Context, owner BillingOwner, email string) (externalID string, err error)
    SuspendCustomer(ctx context.Context, externalID string) error
}

type UsageExportEvent struct {
    ExternalCustomerID string
    EventType          string
    Quantity           int64
    Timestamp          time.Time
    IdempotencyKey     string
    Properties         map[string]string
}

// NoopBillingProvider — ships first. Metering works, export is a no-op.
type NoopBillingProvider struct{}
```

---

## API Surface

### User Endpoints

| Endpoint | Method | Auth | Purpose |
|----------|--------|------|---------|
| `/api/v1/usage` | GET | JWT/API Key | Own usage summary (current period, or `?from=&to=`) |
| `/api/v1/usage/workspaces/:id` | GET | JWT/API Key | Usage for a specific workspace |
| `/api/v1/usage/quota` | GET | JWT/API Key | Current quota status (limits, used, remaining, resets_at) |

### Admin Endpoints

| Endpoint | Method | Auth | Purpose |
|----------|--------|------|---------|
| `/api/v1/admin/usage/:ownerId` | GET | JWT (admin) | View any owner's usage |
| `/api/v1/admin/limits/:ownerId` | PUT | JWT (admin) | Set/update quota limits |
| `/api/v1/admin/limits/:ownerId` | GET | JWT (admin) | Get quota limits |
| `/api/v1/admin/billing/status` | GET | JWT (admin) | Export cursor status, DLQ count |
| `/api/v1/admin/billing/dlq` | GET | JWT (admin) | View dead-letter queue |
| `/api/v1/admin/billing/dlq/:id/retry` | POST | JWT (admin) | Retry a DLQ entry |
| `/api/v1/admin/billing/dlq/:id/discard` | POST | JWT (admin) | Discard a DLQ entry |

### Webhook Endpoint (from billing provider)

| Endpoint | Method | Auth | Purpose |
|----------|--------|------|---------|
| `/api/v1/webhooks/billing` | POST | Provider signature | Plan changes, payment events → update `users.plan_id` |

---

## Failure Modes & Recovery

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Metering event lost (API crash) | Reconciliation cron detects gap in compute_seconds | Catch-up event with `source='reconciliation'` |
| Double-counted event | `idempotency_key` UNIQUE constraint | INSERT silently skipped |
| Metering DB down | Channel buffer fills → Redis retry queue | Retry goroutine drains on recovery; DLQ after 5 failures |
| Wrong token count (extraction bug) | User dispute vs provider dashboard | Admin issues credit via billing provider API |
| Platform outage (workspace unavailable) | `workspace_lifecycle_events` shows unexpected transition | Admin issues credit via billing provider |
| Stolen API key | Anomalous usage + `request_context.api_key_id` | Revoke key; credit via billing provider |
| Billing export fails | `billing_export_cursor` stale + Prometheus alert | Retry from cursor (events are idempotent to provider) |
| Controller restart (missed heartbeats) | Reconciliation cron detects gap | Catch-up events |
| User disputes charge | Cross-reference `usage_events` + `workspace_lifecycle_events` + `request_context` | Evidence-based resolution; credit if warranted |

---

## User Stories

### US-12.1: Metering Service Foundation

**Goal:** Implement the core `MeteringService` with async buffered writes, idempotency, and DLQ.

**Scope:**
- `usage_events` + `usage_events_dlq` tables (migration)
- `MeteringService` interface + implementation
- Buffered channel writer with batch INSERT (100 events or 1s flush)
- Redis retry queue for failed writes (reuses existing Redis infra)
- DLQ insertion after 5 retries
- `BillingOwner` resolution function (returns user today, org-ready)
- Prometheus counters: `metering_events_recorded_total`, `metering_events_failed_total`, `metering_dlq_size`

**Acceptance Criteria:**
- `Record()` never blocks the caller
- Events written to DB within 2s of `Record()` call
- Duplicate `idempotency_key` silently deduplicated
- DB unavailability → Redis buffer → drain on recovery → DLQ after 5 failures
- Integration test: Record 1000 events → verify all in DB within 5s

---

### US-12.2: LLM Request & Token Metering

**Goal:** Instrument the proxy handler to record LLM usage per request AND expose operational metrics for LLM health monitoring.

**Scope:**
- Instrument `SendMessage` and `SendPromptAsync` in `proxy.go`
- After successful proxy (2xx): emit `llm_request` event
- Parse opencode response for token counts (if available): emit `llm_tokens` events
- Populate `request_context` (IP, API key ID, session ID, request ID)
- Populate `metadata` (provider, model — from workspace credentials config)
- **Operational Prometheus metrics (same instrumentation point, zero marginal cost):**
  - `llmsafespace_llm_request_duration_seconds{provider, model, status}` — Histogram (total round-trip time through proxy to opencode)
  - `llmsafespace_llm_errors_total{provider, model, error_type}` — Counter (non-2xx from opencode, timeouts, connection failures)
  - `llmsafespace_llm_time_to_first_byte_seconds{provider, model}` — Histogram (for streaming/SSE responses only — measures user-perceived responsiveness)
  - `llmsafespace_llm_requests_in_flight{provider}` — Gauge (concurrent LLM requests being proxied)

**Acceptance Criteria:**
- Every successful `SendMessage` produces exactly one `llm_request` event
- Token events emitted only when data available (no zeros)
- Metering failure does not affect proxy response
- Prometheus duration histogram recorded for every LLM proxy call (success and failure)
- Error counter incremented on non-2xx, timeout, or connection failure
- TTFB histogram recorded for streaming responses
- Integration test: send message → verify event in DB with correct metadata
- Integration test: simulate LLM timeout → verify error counter incremented

---

### US-12.3: Compute Time Metering

**Goal:** Track workspace pod uptime per user with heartbeat + transition events AND expose operational lifecycle metrics.

**Scope:**
- `workspace_lifecycle_events` table (migration)
- Controller emits lifecycle event on every phase transition
- Controller emits `compute_seconds` heartbeat every 60s for Active workspaces
- On transition out of Active: emit final `compute_seconds` for elapsed since last heartbeat
- Idempotency key: `compute:{workspace_id}:{timestamp_bucket_60s}`
- **Operational Prometheus metrics (same instrumentation point, zero marginal cost):**
  - `llmsafespace_workspace_phase_transition_duration_seconds{from_phase, to_phase}` — Histogram (how long each transition takes)
  - `llmsafespace_workspace_resume_duration_seconds` — Histogram (validates the "~3s resume" claim)
  - `llmsafespace_workspaces_active_total` — Gauge (currently Active workspace count)
  - `llmsafespace_workspaces_suspended_total` — Gauge (currently Suspended workspace count)
  - `llmsafespace_workspace_age_seconds{phase}` — Summary (how long workspaces live before termination)

**Acceptance Criteria:**
- Active workspace accumulates ~60 events/hour of compute_seconds
- Phase transition produces lifecycle event + final compute_seconds
- Controller restart does not produce duplicates (idempotency key)
- Phase transition duration histogram recorded on every transition
- Active/Suspended gauges reflect actual cluster state
- Resume duration histogram validates <5s p99
- Integration test: create workspace → wait 2 min → suspend → verify sum ≈ 120s
- Integration test: suspend → resume → verify resume duration histogram updated

---

### US-12.4: Compute Time Reconciliation

**Goal:** Detect and fill gaps in compute time metering caused by controller failures.

**Scope:**
- Reconciliation cron (every 5 min)
- Lists Active workspace pods from K8s
- Queries last `compute_seconds` event per workspace
- Emits catch-up events for gaps > 90s (60s interval + 30s tolerance)
- `source = 'reconciliation'` on catch-up events
- Prometheus counter: `metering_reconciliation_catchup_total`

**Acceptance Criteria:**
- Simulated 3-min heartbeat gap → reconciliation fills it
- No duplicates (idempotency key covers same time buckets)
- Alert fires when gap > 5 minutes

---

### US-12.5: API Call Metering Middleware

**Goal:** Count authenticated API calls per user for billing.

**Scope:**
- New middleware that batches per-user call counts (10s window)
- Emits aggregated `api_call` event per user per batch window
- `event_subtype`: `read` (GET) or `write` (POST/PUT/DELETE)
- Skips unauthenticated paths (health, metrics)

**Acceptance Criteria:**
- Authenticated requests produce batched `api_call` events
- Under load (1000 req/s): metering adds <1ms p99 latency
- Integration test: 50 API calls → verify aggregated event quantity ≈ 50

---

### US-12.6: Quota Enforcement

**Goal:** Enforce usage limits that block requests when exceeded.

**Scope:**
- `usage_limits` table (migration)
- `CheckQuota()` on `MeteringService` — Redis-cached running totals
- Quota check before LLM proxy (returns 429 when exceeded)
- Response includes: `quota_limit`, `quota_used`, `quota_resets_at`
- Default limits derived from `users.plan_id` (configurable per plan)
- Admin override via `usage_limits` table

**Acceptance Criteria:**
- User with limit of 10 `llm_request`/day: 11th request returns 429
- No quota configured → unlimited (default behavior preserved)
- Quota check adds <5ms p99 (Redis-backed)
- Integration test: set limit → exhaust → verify 429 → verify reset

---

### US-12.7: Usage API Endpoints

**Goal:** Expose usage data to users and admins via REST API.

**Scope:**
- `GET /api/v1/usage` — own usage (totals by event_type, by workspace, by day)
- `GET /api/v1/usage/quota` — current limits, used, remaining, reset time
- `GET /api/v1/admin/usage/:ownerId` — admin view of any owner
- `PUT /api/v1/admin/limits/:ownerId` — admin set/update limits
- Pagination for large date ranges

**Acceptance Criteria:**
- User can only see own usage
- Admin can see any user's usage
- Quota endpoint returns accurate remaining count
- Integration test: record events → query API → verify totals match

---

### US-12.8: Billing Provider Integration & Export

**Goal:** Implement the export pipeline and billing provider interface.

**Scope:**
- `billing_accounts` + `billing_export_cursor` tables (migration)
- `BillingProvider` interface + `NoopBillingProvider`
- Export goroutine (every 5 min): read events > cursor → map to provider format → call `ReportUsage()` → advance cursor
- Billing account creation on user registration (if provider configured)
- `users.plan_id` column (migration) — updated via billing provider webhook

**Acceptance Criteria:**
- With `NoopBillingProvider`: export runs, advances cursor, no-ops
- Cursor correctly tracks high-water mark
- Export failure does not advance cursor
- Integration test: record events → run export → verify cursor advanced

---

### US-12.9: Billing Webhook & Plan Sync

**Goal:** Receive plan change events from billing provider and update local state.

**Scope:**
- `POST /api/v1/webhooks/billing` endpoint
- Signature verification (provider-specific)
- On plan change event: update `users.plan_id`
- On payment failure: optionally suspend workspaces (configurable)
- Audit log entry for every webhook-triggered change

**Acceptance Criteria:**
- Valid webhook updates `users.plan_id`
- Invalid signature returns 401
- Audit log records the change with provider event ID
- Integration test: simulate webhook → verify plan_id updated

---

### US-12.10: Admin DLQ & Audit Operations

**Goal:** Admin tools for DLQ management and billing audit trail.

**Scope:**
- Generalized `audit_log` table (migration) — reuses Epic 10 pattern
- Admin endpoints: DLQ view/retry/discard, export status
- Every admin action creates `audit_log` entry with `domain = 'billing'`
- Credits/refunds: admin calls billing provider API directly (we don't replicate)

**Acceptance Criteria:**
- DLQ entries visible, retryable, discardable via API
- All admin actions audited
- Audit log queryable by domain, actor, date range
- Integration test: DLQ entry → retry → verify reprocessed + audit logged

---

### US-12.11: Storage Metering (Cron)

**Goal:** Periodically measure and record storage usage per workspace.

**Scope:**
- Daily cron job
- For each active/suspended workspace: query PVC usage
- For each user with S3 shared folder: query prefix size
- Emit `storage_bytes` events
- Idempotency key: `storage:{workspace_id}:{date}`

**Acceptance Criteria:**
- Daily storage event per workspace with retained PVC
- Idempotency prevents double-counting if cron runs twice
- Integration test: workspace with files → run cron → verify event

---

### US-12.12: Dependency & Service Health Metrics

**Goal:** Expose operational health metrics for all critical dependencies (Postgres, Redis, K8s API) and internal services (auth, proxy, SSE).

**Scope:**
- **HTTP error metrics** (instrument error handler middleware or extend metrics middleware):
  - `llmsafespace_http_5xx_total{method, path, owner_id, owner_type}` — Counter (server errors — our fault, primary alert signal; owner labels enable per-customer error tracking)
  - `llmsafespace_http_4xx_total{method, path, reason, owner_id, owner_type}` — Counter (client errors with classified reason: auth_failed, not_found, validation, rate_limited, quota_exceeded)
  - Note: `owner_id`/`owner_type` populated from auth context (same `resolveBillingOwner` as metering). Unauthenticated requests use `owner_id="anonymous"`. High-cardinality concern mitigated by only tracking errors, not all requests.
- **Database metrics** (instrument `database.go`):
  - `llmsafespace_db_query_duration_seconds{operation}` — Histogram (operation = select, insert, update, delete)
  - `llmsafespace_db_errors_total{operation, error_type}` — Counter (timeout, connection_refused, deadlock)
  - `llmsafespace_db_pool_active_connections` — Gauge (from pgx `pool.Stat()`)
  - `llmsafespace_db_pool_idle_connections` — Gauge
  - `llmsafespace_db_pool_max_connections` — Gauge
- **Redis metrics** (instrument `cache.go`):
  - `llmsafespace_redis_command_duration_seconds{command}` — Histogram
  - `llmsafespace_redis_errors_total{command}` — Counter
  - `llmsafespace_redis_pool_active_connections` — Gauge (from go-redis `PoolStats()`)
- **Auth metrics** (instrument `auth.go`):
  - `llmsafespace_auth_attempts_total{method, result}` — Counter (method=jwt|apikey, result=success|invalid|expired|locked)
  - `llmsafespace_auth_lockouts_total` — Counter
- **Proxy metrics** (instrument `proxy.go`):
  - `llmsafespace_proxy_rejections_total{reason}` — Counter (reason=connection_limit|session_limit|workspace_not_ready)
  - `llmsafespace_proxy_retries_total` — Counter (stale pod IP → retry with fresh IP)
- **SSE metrics** (instrument SSETracker):
  - `llmsafespace_sse_connections_active` — Gauge
- **Controller workqueue** (controller-runtime built-in — verify registration):
  - `workqueue_depth{name}` — already exposed by controller-runtime if metrics registered
  - `workqueue_queue_duration_seconds{name}` — time in queue before processing
- **Service startup** (instrument `services.go`):
  - `llmsafespace_service_startup_duration_seconds{service}` — Histogram
- **Dependency health** (extend `/readyz`):
  - `llmsafespace_dependency_up{dependency}` — Gauge (1=healthy, 0=unhealthy; dependency=postgres|redis|kubernetes)

**Acceptance Criteria:**
- DB pool exhaustion visible before it causes request failures
- Redis connection drops visible in metrics before user impact
- Auth brute force attempts visible via lockout counter
- Proxy rejection reasons distinguishable (capacity vs. workspace state)
- Controller workqueue backlog visible (items waiting > 10 = alert-worthy)
- All dependency health checks reflected in both `/readyz` response and Prometheus gauge
- Integration test: simulate DB connection failure → verify error counter + health gauge flips

---

### US-12.13: Synthetic Canary

**Goal:** Run a continuous synthetic probe that exercises the full request path (auth → workspace → LLM proxy → response) and alerts on failure.

**Scope:**
- Dedicated canary binary/goroutine (runs as a separate pod or sidecar)
- Canary owns a dedicated workspace (tagged `canary=true`, excluded from billing)
- Probe loop (configurable interval, default 60s):
  1. `POST /api/v1/auth/login` — verify auth works
  2. `GET /api/v1/workspaces/:id` — verify workspace API reachable
  3. `GET /api/v1/workspaces/:id/status` — verify workspace is Active (auto-resume if suspended)
  4. `POST /api/v1/workspaces/:id/sessions/:sid/message` — send a trivial prompt, verify 2xx response
  5. Record success/failure + latency for each step
- **Prometheus metrics:**
  - `llmsafespace_canary_probe_success_total{step}` — Counter
  - `llmsafespace_canary_probe_failure_total{step, error}` — Counter
  - `llmsafespace_canary_probe_duration_seconds{step}` — Histogram
  - `llmsafespace_canary_up` — Gauge (1 if last full probe succeeded, 0 if any step failed)
- **Alerting signal:** `llmsafespace_canary_up == 0` for > 2 consecutive probes = page-worthy
- Canary workspace excluded from billing: `metadata.labels["llmsafespace.dev/canary"] = "true"` → metering service skips events with this label
- Configurable LLM prompt (default: "respond with OK" — minimal token usage)
- Canary credentials stored as K8s Secret (same pattern as user credentials)

**Acceptance Criteria:**
- Canary detects API outage within 2 probe intervals (default: 2 min)
- Canary detects LLM proxy failure (opencode down, credentials invalid) within 2 min
- Canary detects workspace stuck in non-Active phase within 2 min
- Canary detects latency degradation: `canary_degraded` flips to 1 when e2e > configurable SLO threshold (default 10s)
- `canary_e2e_duration_seconds` histogram enables p50/p95/p99 latency SLO tracking over time
- `canary_consecutive_failures` enables escalating alert severity (2=warn, 5=critical)
- Canary workspace usage excluded from billing events
- Canary probe does not interfere with real user traffic (dedicated workspace, minimal LLM usage)
- `llmsafespace_canary_up` gauge usable as SLI for availability SLO
- `llmsafespace_canary_e2e_duration_seconds` usable as SLI for latency SLO
- Integration test: kill opencode in canary workspace → verify `canary_up` flips to 0 + failure counter increments
- Integration test: inject 5s delay in proxy → verify `canary_degraded` flips to 1

---

### US-12.14: Structured Request Logging & Correlation

**Goal:** Ensure every request produces one structured log line with correlation keys, and all downstream components propagate context for end-to-end traceability.

**Scope:**
- Enhance logging middleware to emit one JSON log line per request with: `request_id`, `user_id`, `owner_id`, `owner_type`, `workspace_id`, `method`, `path`, `status`, `duration_ms`, `client_ip`, `request_size`, `response_size`, `slow` (bool, >5s threshold)
- On 5xx: log at ERROR with error message
- On slow (>5s): log at WARN with `"slow": true`
- Create enriched logger per request (`logger.With(request_id, user_id, workspace_id)`) and propagate via Gin context — all downstream service calls use this logger, not a fresh one
- Forward `X-Request-ID` header through proxy to opencode pod
- Include `request_id` in metering `request_context` JSONB (already planned in US-12.2)
- Add structured log points for: auth events, workspace transitions, proxy rejections/retries, metering failures, quota events, canary probes (see Logging Strategy section)
- Ensure no request/response bodies, auth headers, or tokens are ever logged

**Acceptance Criteria:**
- Every request produces exactly one INFO log line with all correlation fields
- Given a `request_id`, all related log lines (auth, proxy, metering, errors) are findable via grep
- 5xx requests logged at ERROR; slow requests logged at WARN
- `X-Request-ID` present in opencode pod access logs for proxied requests
- No secrets in any log line (verify with `pkg/redact` patterns)
- Log output is valid JSON (parseable by fluentbit/Loki)
- Performance: logging adds <0.5ms p99 to request latency (Zap buffered writer)
- Integration test: send request → verify structured log line with all fields → verify request_id in metering event

---

## Dependency Graph

```
US-12.1 (Metering Foundation) ───────────────────────────────────────────┐
    │                                                                     │
    ├──→ US-12.2 (LLM Metering)              ─┐                          │
    ├──→ US-12.3 (Compute Time) → US-12.4     │ parallel                 │
    ├──→ US-12.5 (API Call Metering)          ─┘                          │
    ├──→ US-12.11 (Storage Metering)                                      │
    │                                                                     │
    ├──→ US-12.6 (Quota Enforcement)                                      │
    ├──→ US-12.7 (Usage API)                                              │
    │                                                                     │
    └──→ US-12.8 (Billing Export) → US-12.9 (Webhook/Plan Sync)           │
                                                                          │
US-12.10 (Admin/DLQ) ── requires US-12.1 ────────────────────────────────┘

US-12.12 (Dependency Health Metrics) ── independent, no dependencies
US-12.13 (Synthetic Canary) ── requires working API + workspace (Epic 6)
US-12.14 (Structured Logging) ── independent, no dependencies
```

**Critical path:** US-12.1 → US-12.2 + US-12.3 (parallel) → US-12.6 → US-12.7

**Parallelizable after US-12.1:** US-12.2, US-12.3, US-12.5, US-12.11

**Fully independent (can start anytime):** US-12.12, US-12.13, US-12.14

**Deferred until provider chosen:** US-12.8 real implementation (beyond Noop), US-12.9

---

## Organization Extension Point (Epic 11 Compatibility)

When Epic 11 adds organizations:

**What changes:**
- `resolveBillingOwner()` gains org lookup
- Org admin usage endpoints become active
- `usage_limits` gains org-level and per-member limits
- Export maps org → single external customer

**What does NOT change:**
- `usage_events` schema (already has `owner_type = 'org'`)
- `MeteringService` interface
- `BillingProvider` interface
- Reconciliation, DLQ, quota enforcement logic

---


---

## Structured Logging Strategy

### Principles

1. **Log decisions, not data.** Log *why* something happened (auth rejected, quota exceeded, retry triggered), not request/response bodies.
2. **Structured always.** Every log line is JSON. No `fmt.Sprintf` messages.
3. **Correlation keys on every line.** `request_id`, `user_id`, `workspace_id` — grep one request's entire journey.
4. **Levels mean something.** Production runs at INFO. DEBUG is off unless actively troubleshooting.
5. **Never log secrets.** Use `pkg/redact` for any user-provided content that might contain credentials.
6. **One log line per request.** The request summary is the primary INFO log. Additional lines only for errors/warnings.

### Log Levels

| Level | What | Production? |
|-------|------|-------------|
| **ERROR** | Something broke that requires investigation | Yes — alerts fire on these |
| **WARN** | Degraded but functional; may need attention if sustained | Yes |
| **INFO** | Significant state changes + one-line request summaries | Yes |
| **DEBUG** | Detailed flow for troubleshooting | No — enable per-pod via config |

### Request Log Format (one line per request at INFO)

```json
{
  "level": "info",
  "msg": "request",
  "request_id": "req-abc123",
  "method": "POST",
  "path": "/api/v1/workspaces/ws-1/sessions/s-1/message",
  "status": 200,
  "duration_ms": 1523,
  "user_id": "user-456",
  "owner_id": "user-456",
  "owner_type": "user",
  "workspace_id": "ws-1",
  "client_ip": "10.0.1.5",
  "user_agent": "llmsafespace-sdk/1.0",
  "request_size": 245,
  "response_size": 8192,
  "slow": false,
  "error": ""
}
```

Conditional behavior:
- On 5xx: log at ERROR with error message (no stack trace unless panic)
- On slow requests (>5s): add `"slow": true`, log at WARN
- On 4xx: log at INFO (normal client errors)
- Never log: request/response bodies, auth headers, tokens

### Key Logging Points

| Component | Event | Level | Key Fields |
|-----------|-------|-------|------------|
| Auth | Login success/failure | INFO | `user_id`, `method`, `result`, `client_ip` |
| Auth | Account lockout | WARN | `user_id`, `attempts`, `lockout_duration` |
| Workspace | Phase transition | INFO | `workspace_id`, `owner_id`, `from_phase`, `to_phase`, `duration_ms` |
| Workspace | Stuck in transition >60s | WARN | `workspace_id`, `phase`, `stuck_seconds` |
| Proxy | Connection rejected | WARN | `workspace_id`, `user_id`, `reason` |
| Proxy | Retry with fresh pod IP | WARN | `workspace_id`, `old_ip`, `new_ip` |
| Proxy | LLM error from opencode | WARN | `workspace_id`, `status`, `error_type`, `provider` |
| Metering | Batch write failed (retrying) | WARN | `batch_size`, `error` |
| Metering | Event moved to DLQ | ERROR | `event_type`, `error`, `retry_count` |
| Metering | Reconciliation gap detected | WARN | `workspace_id`, `gap_seconds` |
| Quota | Quota exceeded | INFO | `owner_id`, `event_type`, `limit`, `used` |
| Quota | Quota >90% consumed | WARN | `owner_id`, `event_type`, `percent_used` |
| Billing export | Batch completed | INFO | `events_exported`, `cursor_position` |
| Billing export | Export failed | ERROR | `error`, `last_cursor` |
| Controller | Reconciliation error | ERROR | `resource`, `name`, `error` |
| Controller | Workqueue depth >50 | WARN | `controller`, `depth` |
| Startup | Service started | INFO | `service`, `duration_ms` |
| Startup | Service failed to start | ERROR | `service`, `error` |
| Canary | Probe failed | WARN | `step`, `error`, `consecutive_failures` |

### Correlation Chain

```
Request arrives
  → request_id middleware assigns ID
  → logging middleware creates logger with {request_id, user_id, workspace_id}
  → all downstream code uses this enriched logger
  → metering events include request_id in request_context JSONB
  → proxy forwards X-Request-ID header to opencode pod

Given a user complaint:
  1. Find request by user_id + timestamp in logs
  2. Get request_id
  3. Grep all log lines with that request_id (auth, proxy, metering, errors)
  4. Find metering event with request_id in request_context
  5. Find opencode-side logs by forwarded X-Request-ID
```

### Performance

| Concern | Mitigation |
|---------|-----------|
| Log volume | INFO only in prod. One line per request. |
| Allocation | Zap `With()` pre-allocates. Create enriched logger once per request, reuse. |
| Disk I/O | Zap buffered async writer. Flush on graceful shutdown. |
| Shipping | JSON → fluentbit sidecar → Loki/ELK. Not app-level concern. |
| Body logging | Never. Use request_id to correlate with metering events. |

## Non-Requirements (Explicitly Out of Scope)

- **Billing provider client code** (Stripe/Lago SDK) — ship with `NoopBillingProvider`, implement when provider chosen
- **Invoice generation** — billing provider's job
- **Payment collection / dunning** — billing provider's job
- **Tax calculation** — billing provider's job (region column enables it)
- **Pricing rate cards in our DB** — billing provider owns pricing
- **Plan/subscription management UI** — billing provider's dashboard or future frontend work
- **Per-request cost estimation** ("this will cost $0.03") — nice-to-have, not MVP
- **Multi-currency** — USD only; schema is forward-compatible
- **Org-level billing endpoints** — schema supports it, endpoints deferred to Epic 11
- **Real-time WebSocket usage updates** — polling is sufficient for MVP

---

## Security Considerations

1. **Usage data is PII-adjacent.** Access restricted to: own data (user), all data (admin).
2. **Admin actions audited.** Every DLQ operation, limit override logged to `audit_log`.
3. **No secrets in metering data.** `request_context` stores API key ID (not the key). `metadata` stores provider name (not credentials).
4. **DLQ contains user IDs.** DLQ access restricted to admin role.
5. **Webhook signature verification.** Billing provider webhooks validated before processing.
6. **Export to billing provider.** Only owner_id + usage quantities sent. No workspace content or credentials.

---

## Prometheus Metrics (Monitoring the metering system itself)

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `metering_events_recorded_total` | Counter | `event_type`, `source` | Events successfully written |
| `metering_events_failed_total` | Counter | `event_type`, `error_type` | Events that failed to write |
| `metering_buffer_size` | Gauge | — | Current channel buffer occupancy |
| `metering_batch_write_duration_seconds` | Histogram | — | Batch flush latency |
| `metering_dlq_size` | Gauge | — | Unresolved DLQ entries |
| `metering_reconciliation_catchup_total` | Counter | — | Gap-fill events emitted |
| `metering_export_lag_seconds` | Gauge | `provider` | Time since last successful export |
| `metering_quota_exceeded_total` | Counter | `event_type` | Quota enforcement triggers |

## Prometheus Metrics (Operational — LLM & Workspace Health)

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `llmsafespace_llm_request_duration_seconds` | Histogram | `provider`, `model`, `status` | LLM round-trip latency |
| `llmsafespace_llm_errors_total` | Counter | `provider`, `model`, `error_type` | LLM failures (non-2xx, timeout, conn error) |
| `llmsafespace_llm_time_to_first_byte_seconds` | Histogram | `provider`, `model` | Streaming responsiveness |
| `llmsafespace_llm_requests_in_flight` | Gauge | `provider` | Concurrent LLM proxy requests |
| `llmsafespace_workspace_phase_transition_duration_seconds` | Histogram | `from_phase`, `to_phase` | Transition latency |
| `llmsafespace_workspace_resume_duration_seconds` | Histogram | — | Resume SLA validation |
| `llmsafespace_workspaces_active_total` | Gauge | — | Currently Active workspaces |
| `llmsafespace_workspaces_suspended_total` | Gauge | — | Currently Suspended workspaces |
| `llmsafespace_workspace_age_seconds` | Summary | `phase` | Workspace lifespan distribution |

## Prometheus Metrics (Dependency & Service Health)

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `llmsafespace_http_errors_total` | Counter | `status_class`, `method`, `path`, `owner_id`, `owner_type` | 4xx/5xx counts per customer/org |
| `llmsafespace_http_5xx_total` | Counter | `method`, `path`, `owner_id`, `owner_type` | Server errors per customer — alerting signal |
| `llmsafespace_http_4xx_total` | Counter | `method`, `path`, `reason`, `owner_id`, `owner_type` | Client errors per customer with reason |
| `llmsafespace_db_query_duration_seconds` | Histogram | `operation` | DB query latency |
| `llmsafespace_db_errors_total` | Counter | `operation`, `error_type` | DB failures |
| `llmsafespace_db_pool_active_connections` | Gauge | — | Pool pressure |
| `llmsafespace_db_pool_idle_connections` | Gauge | — | Pool headroom |
| `llmsafespace_redis_command_duration_seconds` | Histogram | `command` | Cache latency |
| `llmsafespace_redis_errors_total` | Counter | `command` | Cache failures |
| `llmsafespace_redis_pool_active_connections` | Gauge | — | Redis pool pressure |
| `llmsafespace_auth_attempts_total` | Counter | `method`, `result` | Auth success/failure rates |
| `llmsafespace_auth_lockouts_total` | Counter | — | Brute force indicator |
| `llmsafespace_proxy_rejections_total` | Counter | `reason` | Capacity vs. state rejections |
| `llmsafespace_proxy_retries_total` | Counter | — | Pod IP staleness indicator |
| `llmsafespace_sse_connections_active` | Gauge | — | Long-lived connection count |
| `llmsafespace_service_startup_duration_seconds` | Histogram | `service` | Boot time per component |
| `llmsafespace_dependency_up` | Gauge | `dependency` | Health check status (1=up, 0=down) |

## Prometheus Metrics (Synthetic Canary)

| Metric | Type | Labels | Purpose |
|--------|------|--------|---------|
| `llmsafespace_canary_probe_success_total` | Counter | `step` | Successful probe steps |
| `llmsafespace_canary_probe_failure_total` | Counter | `step`, `error` | Failed probe steps |
| `llmsafespace_canary_probe_duration_seconds` | Histogram | `step` | Per-step latency (auth, workspace_get, status, llm_message) |
| `llmsafespace_canary_e2e_duration_seconds` | Histogram | — | Full probe end-to-end latency (all steps combined) |
| `llmsafespace_canary_llm_ttfb_seconds` | Histogram | — | Time-to-first-byte from canary's perspective (client-side TTFB) |
| `llmsafespace_canary_up` | Gauge | — | SLI: 1=healthy, 0=broken (alerting signal) |
| `llmsafespace_canary_degraded` | Gauge | — | 1 if e2e latency > SLO threshold (e.g., >10s), 0 otherwise |
| `llmsafespace_canary_consecutive_failures` | Gauge | — | Rolling count of consecutive failed probes (resets on success) |
