-- Epic 30, US-30.1: Rollback unified credential model.
-- WARNING: DESTRUCTIVE. Does NOT restore credential data.

DROP TABLE IF EXISTS credential_backfill_jobs;
DROP TABLE IF EXISTS credential_auto_apply;
DROP TABLE IF EXISTS workspace_credential_bindings;
DROP TABLE IF EXISTS provider_credentials;

-- Recreate credential_sets shell (empty — no data restored).
CREATE TABLE credential_sets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT UNIQUE NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT false,
    providers_encrypted BYTEA NOT NULL,
    key_version SMALLINT NOT NULL DEFAULT 1,
    model_allowlist TEXT[] NOT NULL DEFAULT '{}',
    assigned_to JSONB NOT NULL DEFAULT '"all"',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_credential_sets_default ON credential_sets (is_default) WHERE is_default = true;

DROP TRIGGER IF EXISTS trg_credential_sets_updated_at ON credential_sets;
CREATE TRIGGER trg_credential_sets_updated_at
    BEFORE UPDATE ON credential_sets
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
