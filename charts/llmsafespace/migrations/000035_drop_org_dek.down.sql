-- Rollback migration 000035: Restore org DEK infrastructure.
--
-- WARNING: This does NOT restore any previously-encrypted org credentials.
-- Existing org credentials (encrypted with server KEK) cannot be decrypted
-- with the org DEK. This rollback is for schema only, not data.
-- All org credentials would need to be re-created after rollback.

-- Restore the pending_key_wrap column.
ALTER TABLE org_memberships
    ADD COLUMN IF NOT EXISTS pending_key_wrap BOOLEAN NOT NULL DEFAULT FALSE;

-- Restore the partial index.
CREATE INDEX IF NOT EXISTS idx_org_memberships_pending
    ON org_memberships(org_id)
    WHERE pending_key_wrap = TRUE;

-- Restore the org_key_members table.
CREATE TABLE IF NOT EXISTS org_key_members (
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wrapped_dek BYTEA NOT NULL,
    key_version INTEGER NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (org_id, user_id) REFERENCES org_memberships(org_id, user_id) ON DELETE CASCADE,
    PRIMARY KEY (org_id, user_id)
);

CREATE TRIGGER trg_org_key_members_updated_at
    BEFORE UPDATE ON org_key_members
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
