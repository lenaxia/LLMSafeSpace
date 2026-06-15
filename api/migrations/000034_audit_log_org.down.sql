BEGIN;

DROP INDEX IF EXISTS idx_audit_org;

ALTER TABLE audit_log DROP COLUMN IF EXISTS org_id;

-- Drop the named constraint and restore the original domain values.
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_domain_chk;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'audit_log_domain_check' AND conrelid = 'audit_log'::regclass
    ) THEN
        ALTER TABLE audit_log ADD CONSTRAINT audit_log_domain_check
            CHECK (domain IN ('billing', 'secrets', 'admin'));
    END IF;
END $$;

COMMIT;
