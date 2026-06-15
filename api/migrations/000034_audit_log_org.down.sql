BEGIN;

DROP INDEX IF EXISTS idx_audit_org;

ALTER TABLE audit_log DROP COLUMN IF EXISTS org_id;

ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_domain_chk;
ALTER TABLE audit_log ALTER COLUMN domain TYPE TEXT;

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
