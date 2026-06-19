-- US-12.6: Quota enforcement tables

BEGIN;

CREATE TABLE IF NOT EXISTS usage_limits (
    id           BIGSERIAL PRIMARY KEY,
    owner_id     TEXT NOT NULL,
    owner_type   TEXT NOT NULL DEFAULT 'user',
    event_type   TEXT NOT NULL,
    period_type  TEXT NOT NULL DEFAULT 'monthly' CHECK (period_type IN ('daily','monthly','lifetime')),
    max_quantity BIGINT NOT NULL CHECK (max_quantity > 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(owner_id, owner_type, event_type, period_type)
);

ALTER TABLE users ADD COLUMN IF NOT EXISTS plan_id TEXT NOT NULL DEFAULT 'free';

COMMIT;
