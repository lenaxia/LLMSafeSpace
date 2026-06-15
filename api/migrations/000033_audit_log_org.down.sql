BEGIN;

DROP INDEX IF EXISTS idx_audit_org;

ALTER TABLE audit_log DROP COLUMN IF EXISTS org_id;

ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_domain_chk;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_domain_chk
    CHECK (domain IN ('billing', 'secrets', 'admin'));

COMMIT;
