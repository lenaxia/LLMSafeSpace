-- Epic 43, US-43.10: OIDC SSO configuration per organization.
--
-- Implements D17: one row per org holding the OIDC provider wiring used by
-- the SSO login flow. The client secret is encrypted at rest with the server
-- KEK derived from LLMSAFESPACE_MASTER_SECRET (D17-S4) — always decryptable,
-- no org DEK cache dependency. claimed_domains drives login-page domain
-- discovery (D17-S2). group_role_mapping maps OIDC `groups` claims to org
-- roles applied on every login (D17-S3).

CREATE TABLE IF NOT EXISTS org_sso_configs (
    org_id              UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    oidc_discovery_url  TEXT NOT NULL,
    oidc_client_id      TEXT NOT NULL,
    oidc_client_secret  BYTEA NOT NULL,
    claimed_domains     TEXT[] NOT NULL DEFAULT '{}',
    auto_provision      BOOLEAN NOT NULL DEFAULT TRUE,
    group_role_mapping  JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- GIN index for domain-to-org discovery on the login page.
CREATE INDEX IF NOT EXISTS idx_org_sso_domains
    ON org_sso_configs USING GIN (claimed_domains);

DROP TRIGGER IF EXISTS trg_org_sso_configs_updated_at ON org_sso_configs;
CREATE TRIGGER trg_org_sso_configs_updated_at
    BEFORE UPDATE ON org_sso_configs
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
