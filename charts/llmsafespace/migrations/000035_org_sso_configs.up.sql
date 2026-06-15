-- Epic 43, US-43.10: Org OIDC SSO configuration.
--
-- Stores per-org OIDC provider configuration. The client_secret is encrypted
-- at rest with the org DEK (D17) — stored as a base64 ciphertext blob, never
-- plaintext. group_admin_claim and group_member_claim map OIDC group claims
-- to org roles (e.g., "llmsafespace-admins" → admin).

BEGIN;

CREATE TABLE IF NOT EXISTS org_sso_configs (
    org_id              UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    discovery_url       TEXT NOT NULL,
    client_id           TEXT NOT NULL,
    encrypted_secret    BYTEA NOT NULL,
    group_admin_claim   TEXT NOT NULL DEFAULT 'llmsafespace-admins',
    group_member_claim  TEXT NOT NULL DEFAULT 'llmsafespace-members',
    auto_provision      BOOLEAN NOT NULL DEFAULT TRUE,
    enabled             BOOLEAN NOT NULL DEFAULT FALSE,
    configured_by       TEXT REFERENCES users(id),
    configured_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
