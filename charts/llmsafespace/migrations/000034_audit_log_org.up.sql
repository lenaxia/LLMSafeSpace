-- Epic 43, US-43.13: Org-scoped audit log.
--
-- Extends the existing audit_log table (migration 028) with:
-- 1. 'org' as a valid domain value (for org-scoped events)
-- 2. An optional org_id column for filtering org events
-- 3. An index for efficient org-scoped audit queries

BEGIN;

-- Drop any existing domain check constraint (handles both auto-named and
-- explicitly-named from a prior migration run) via ALTER TYPE which implicitly
-- drops inline CHECK constraints, then re-add with IF NOT EXISTS-safe pattern.
ALTER TABLE audit_log ALTER COLUMN domain TYPE TEXT;
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_domain_chk;
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
