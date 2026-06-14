-- Epic 43, US-43.1: Organization status, plan, subscription lifecycle + Stripe
-- webhook idempotency.
--
-- Adds the org status/plan/subscription columns that gate workspace access and
-- feature availability, the stripe_events dedup table for safe webhook retries,
-- and a case-insensitive unique index on org slugs (D6 / US-43.3 slug resolution).
--
-- The DDL is idempotent (IF NOT EXISTS). The backfill UPDATE below is ONE-SHOT:
-- it activates pre-existing orgs so the new status filter does not lock out
-- current members. Re-running it would also activate legitimately-pending new
-- orgs — migrations are applied exactly once in production, so this is safe.

BEGIN;

ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'pending_activation'
        CHECK (status IN ('pending_activation', 'active', 'suspended'));

-- Backfill: every org that existed before this migration was created under the
-- pre-gating model (unrestricted access). Mark them 'active' so the new
-- IsOrgMember/IsOrgAdmin status filter does not lock out existing members.
-- Newly-created orgs override this default to 'pending_activation' at the
-- application layer (the DB default remains 'pending_activation' for fresh rows).
UPDATE organizations SET status = 'active'
 WHERE deleted_at IS NULL AND status = 'pending_activation';

ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS plan_id TEXT NOT NULL DEFAULT 'free';

ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS subscription_status TEXT NOT NULL DEFAULT 'inactive'
        CHECK (subscription_status IN ('inactive', 'active', 'trialing', 'past_due', 'canceled', 'unpaid'));

CREATE INDEX IF NOT EXISTS idx_orgs_status ON organizations(status);

-- Case-insensitive unique slug index. The prior case-sensitive index
-- (idx_orgs_slug_active from migration 029) is intentionally retained: it
-- remains correct for exact-match lookups and the new lowercase index enforces
-- the case-insensitive uniqueness guarantee. If pre-existing mixed-case slugs
-- collide case-insensitively, this CREATE UNIQUE INDEX will fail — run the
-- collision check below first and reconcile any duplicates:
--   SELECT LOWER(slug), COUNT(*) FROM organizations
--   WHERE deleted_at IS NULL GROUP BY LOWER(slug) HAVING COUNT(*) > 1;
CREATE UNIQUE INDEX IF NOT EXISTS idx_orgs_slug_lower_active
    ON organizations(LOWER(slug)) WHERE deleted_at IS NULL;

-- stripe_events: idempotency record for Stripe webhook delivery. Stripe retries
-- webhook delivery for up to 3 days; the ON CONFLICT DO NOTHING insert + rows
-- affected check ensures each event is processed exactly once.
CREATE TABLE IF NOT EXISTS stripe_events (
    event_id     TEXT PRIMARY KEY,
    event_type   TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
