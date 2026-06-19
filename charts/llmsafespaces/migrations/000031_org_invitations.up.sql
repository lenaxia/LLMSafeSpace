-- Epic 43, US-43.2: Email-based org invitation system.
--
-- Stores one row per invitation. The token is 32 bytes of crypto/rand base64url-
-- encoded (~43 chars); only the SHA-256 hash is persisted so a DB compromise
-- does not leak valid tokens. Expired/accepted/declined invitations are retained
-- for audit. The bounce columns capture SES delivery feedback (permanent,
-- transient, complaint) so future sends to known-bad addresses are suppressed.

BEGIN;

CREATE TABLE IF NOT EXISTS org_invitations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email       TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    invited_by  TEXT NOT NULL REFERENCES users(id),
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    accepted_by TEXT REFERENCES users(id),
    declined_at TIMESTAMPTZ,
    bounce_type TEXT CHECK (bounce_type IN ('permanent', 'transient', 'complaint')),
    bounced_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Pending = not accepted, not declined, not expired.
CREATE INDEX IF NOT EXISTS idx_org_invitations_org
    ON org_invitations(org_id)
    WHERE accepted_at IS NULL AND declined_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_org_invitations_email
    ON org_invitations(email)
    WHERE accepted_at IS NULL AND declined_at IS NULL;

DROP TRIGGER IF EXISTS trg_org_invitations_updated_at ON org_invitations;
CREATE TRIGGER trg_org_invitations_updated_at
    BEFORE UPDATE ON org_invitations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

COMMIT;
