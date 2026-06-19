-- Epic 43, US-43.19: User-level suspension (D19).
--
-- Adds an authoritative users.status column alongside the legacy boolean
-- users.active. The boolean is retained as-is for backward compatibility with
-- existing read sites; status is the new authoritative field consulted by the
-- auth middleware on every authenticated request:
--   status = 'suspended' → blocked across ALL contexts (all orgs + personal)
--   status = 'active'    → permitted
--
-- The backfill derives status from the legacy active flag so the new gate does
-- not lock out existing users: active=true → 'active', active=false →
-- 'suspended'. Migrations are applied exactly once in production, so the
-- one-shot UPDATE is safe.

BEGIN;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'suspended'));

-- Backfill: align status with the pre-existing active flag for every row.
UPDATE users SET status = CASE WHEN active THEN 'active' ELSE 'suspended' END;

-- Partial index: only non-active rows are interesting for suspension lookups.
-- Keeps the index small in the common case where almost every user is active.
CREATE INDEX IF NOT EXISTS idx_users_status ON users(status) WHERE status != 'active';

COMMIT;
