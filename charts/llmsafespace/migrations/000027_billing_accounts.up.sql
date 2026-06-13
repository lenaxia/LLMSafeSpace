-- US-12.8: Billing accounts and export cursor

BEGIN;

CREATE TABLE IF NOT EXISTS billing_accounts (
    id                       BIGSERIAL PRIMARY KEY,
    owner_id                 TEXT NOT NULL,
    owner_type               TEXT NOT NULL DEFAULT 'user',
    provider                 TEXT NOT NULL,
    external_customer_id     TEXT NOT NULL,
    external_subscription_id TEXT,
    status                   TEXT NOT NULL DEFAULT 'active',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(owner_id, owner_type, provider)
);

CREATE TABLE IF NOT EXISTS billing_export_cursor (
    provider            TEXT PRIMARY KEY,
    last_exported_id    BIGINT NOT NULL DEFAULT 0,
    last_exported_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMIT;
