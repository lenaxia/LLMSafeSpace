-- Revert: DNS verification of claimed SSO domains.

DROP INDEX IF EXISTS idx_org_sso_verified_domains;

ALTER TABLE org_sso_configs
    DROP COLUMN IF EXISTS verification_token,
    DROP COLUMN IF EXISTS verified_domains;
