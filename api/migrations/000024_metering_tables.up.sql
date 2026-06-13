-- US-12.1: Metering tables for usage metering and billing.
--
-- usage_events: append-only core metering table, source of truth for billing.
-- usage_events_dlq: dead-letter queue for failed batch writes.

BEGIN;

CREATE TABLE IF NOT EXISTS usage_events (
    id              BIGSERIAL PRIMARY KEY,
    idempotency_key TEXT UNIQUE,

    owner_id        TEXT NOT NULL,
    owner_type      TEXT NOT NULL DEFAULT 'user' CHECK (owner_type IN ('user', 'org')),

    actor_id        TEXT NOT NULL,

    workspace_id    TEXT,

    event_type      TEXT NOT NULL
        CHECK (event_type IN ('compute_seconds','llm_request','llm_tokens','storage_bytes','api_call')),
    event_subtype   TEXT,

    quantity        BIGINT NOT NULL CHECK (quantity >= 0),

    resource_tier   TEXT,
    region          TEXT,

    metadata        JSONB NOT NULL DEFAULT '{}',

    request_context JSONB,

    source          TEXT NOT NULL DEFAULT 'api'
        CHECK (source IN ('api','controller','cron','reconciliation')),

    event_time      TIMESTAMPTZ NOT NULL,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    period          DATE NOT NULL DEFAULT CURRENT_DATE
);

CREATE INDEX IF NOT EXISTS idx_usage_owner_period  ON usage_events(owner_id, owner_type, period);
CREATE INDEX IF NOT EXISTS idx_usage_actor_period  ON usage_events(actor_id, period);
CREATE INDEX IF NOT EXISTS idx_usage_workspace     ON usage_events(workspace_id, period)
    WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_usage_type_period   ON usage_events(event_type, period);
CREATE INDEX IF NOT EXISTS idx_usage_idempotency   ON usage_events(idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS usage_events_dlq (
    id              BIGSERIAL PRIMARY KEY,
    payload         JSONB NOT NULL,
    error_message   TEXT NOT NULL,
    retry_count     INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    first_failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_retried_at TIMESTAMPTZ,
    resolved_at     TIMESTAMPTZ,
    resolution      TEXT CHECK (resolution IN ('reprocessed','discarded','dead'))
);

CREATE INDEX IF NOT EXISTS idx_dlq_unresolved ON usage_events_dlq(last_retried_at ASC NULLS FIRST)
    WHERE resolved_at IS NULL;

COMMIT;
