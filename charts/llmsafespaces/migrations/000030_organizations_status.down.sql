-- Rollback for US-43.1: organizations status/plan/subscription + stripe_events.

BEGIN;

DROP TABLE IF EXISTS stripe_events;

DROP INDEX IF EXISTS idx_orgs_slug_lower_active;

ALTER TABLE organizations
    DROP COLUMN IF EXISTS subscription_status;

ALTER TABLE organizations
    DROP COLUMN IF EXISTS plan_id;

ALTER TABLE organizations
    DROP COLUMN IF EXISTS status;

DROP INDEX IF EXISTS idx_orgs_status;

COMMIT;
