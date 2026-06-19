-- Epic 43, US-43.10: OIDC SSO configuration per organization (revert).

BEGIN;

DROP TRIGGER IF EXISTS trg_org_sso_configs_updated_at ON org_sso_configs;
DROP INDEX IF EXISTS idx_org_sso_domains;
DROP TABLE IF EXISTS org_sso_configs;

COMMIT;
