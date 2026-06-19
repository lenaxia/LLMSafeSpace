-- Epic 43, US-43.13: Org-scoped audit log.
--
-- Extends the existing audit_log table (migration 028) with:
-- 1. 'org' as a valid domain value (for org-scoped events)
-- 2. An optional org_id column for filtering org events
-- 3. An index for efficient org-scoped audit queries

BEGIN;

-- Drop the auto-generated inline CHECK constraint from migration 028.
-- PostgreSQL auto-names it `audit_log_domain_check` (table_column_check).
-- ALTER COLUMN TYPE does NOT drop CHECK constraints, so we must drop explicitly.
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_domain_check;
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_domain_chk;

-- Add the new constraint allowing 'org' as a domain value. Use a DO block for
-- idempotency so the migration can be re-applied without error.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'audit_log_domain_chk' AND conrelid = 'audit_log'::regclass
    ) THEN
        ALTER TABLE audit_log ADD CONSTRAINT audit_log_domain_chk
            CHECK (domain IN ('billing', 'secrets', 'admin', 'org'));
    END IF;
END $$;

-- Nullable org_id: org-scoped events set it; non-org events leave it NULL.
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS org_id UUID REFERENCES organizations(id);

-- Index for org-scoped audit queries (WHERE org_id IS NOT NULL excludes non-org events).
CREATE INDEX IF NOT EXISTS idx_audit_org ON audit_log(org_id, created_at DESC) WHERE org_id IS NOT NULL;

COMMIT;
