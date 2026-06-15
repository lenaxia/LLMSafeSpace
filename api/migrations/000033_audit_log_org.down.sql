BEGIN;

DROP INDEX IF EXISTS idx_audit_org;

ALTER TABLE audit_log DROP COLUMN IF EXISTS org_id;

-- Drop the named constraint and let ALTER TYPE remove the check, restoring
-- the original schema shape (inline CHECK without explicit name).
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_domain_chk;
ALTER TABLE audit_log ALTER COLUMN domain TYPE TEXT CHECK (domain IN ('billing', 'secrets', 'admin'));

COMMIT;
