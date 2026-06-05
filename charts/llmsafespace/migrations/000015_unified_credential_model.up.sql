-- Epic 30, US-30.1: Unified Credential Model schema.
--
-- All CREATE statements use IF NOT EXISTS for idempotency: applying
-- this migration to a database that already ran it must be a no-op.
-- The migration-safety CI gate enforces this.
--
-- The update_updated_at_column() function already exists from migration 000006.

-- Drop the legacy admin credential sets system entirely.
DROP TABLE IF EXISTS credential_sets;

-- New unified table for all LLM provider credentials.
CREATE TABLE IF NOT EXISTS provider_credentials (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_type      TEXT NOT NULL CHECK (owner_type IN ('user', 'org', 'admin')),
    owner_id        TEXT NOT NULL,
    name            TEXT NOT NULL,
    provider        TEXT NOT NULL,
    ciphertext      BYTEA NOT NULL,
    key_version     INTEGER NOT NULL DEFAULT 1,
    model_allowlist TEXT[] NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(owner_type, owner_id, provider)
);

CREATE INDEX IF NOT EXISTS idx_provider_creds_owner ON provider_credentials(owner_type, owner_id);

DROP TRIGGER IF EXISTS trg_provider_credentials_updated_at ON provider_credentials;
CREATE TRIGGER trg_provider_credentials_updated_at
    BEFORE UPDATE ON provider_credentials
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Workspace credential bindings: source_type + within_priority for two-key priority sort.
CREATE TABLE IF NOT EXISTS workspace_credential_bindings (
    credential_id    UUID NOT NULL REFERENCES provider_credentials(id) ON DELETE CASCADE,
    workspace_id     UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    source_type      TEXT NOT NULL DEFAULT 'explicit'
                         CHECK (source_type IN ('explicit', 'auto')),
    within_priority  INTEGER NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(credential_id, workspace_id)
);

CREATE INDEX IF NOT EXISTS idx_ws_cred_bindings_workspace ON workspace_credential_bindings(workspace_id);
CREATE INDEX IF NOT EXISTS idx_ws_cred_bindings_credential ON workspace_credential_bindings(credential_id);

-- Auto-apply rules: configuration-only table that drives seeding at workspace creation.
CREATE TABLE IF NOT EXISTS credential_auto_apply (
    credential_id   UUID NOT NULL REFERENCES provider_credentials(id) ON DELETE CASCADE,
    target_type     TEXT NOT NULL CHECK (target_type IN ('user', 'org', 'all')),
    target_id       TEXT,
    within_priority INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Partial unique indexes to handle NULL target_id correctly.
CREATE UNIQUE INDEX IF NOT EXISTS idx_cred_auto_apply_unique_targeted
    ON credential_auto_apply(credential_id, target_type, target_id)
    WHERE target_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_cred_auto_apply_unique_all
    ON credential_auto_apply(credential_id, target_type)
    WHERE target_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_cred_auto_apply_all  ON credential_auto_apply(target_type) WHERE target_type = 'all';
CREATE INDEX IF NOT EXISTS idx_cred_auto_apply_user ON credential_auto_apply(target_id)   WHERE target_type = 'user';
CREATE INDEX IF NOT EXISTS idx_cred_auto_apply_org  ON credential_auto_apply(target_id)   WHERE target_type = 'org';

-- Job state for async backfill operations.
CREATE TABLE IF NOT EXISTS credential_backfill_jobs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    credential_id UUID NOT NULL REFERENCES provider_credentials(id) ON DELETE CASCADE,
    status        TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'complete', 'failed')),
    processed     INTEGER NOT NULL DEFAULT 0,
    errors        JSONB NOT NULL DEFAULT '[]',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
