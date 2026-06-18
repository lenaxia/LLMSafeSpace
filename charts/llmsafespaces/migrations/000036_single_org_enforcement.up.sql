-- Migration 000036: Single-org enforcement (D8 in design 0031)
--
-- One membership per user, enforced at the schema level. A user belongs to at
-- most one org. This is the foundation for: workspace auto-attribution (D4),
-- membership-gated access (D5), and the sidebar org button (D12).
--
-- No data migration needed: no orgs exist in production (confirmed pre-deploy).
-- The pre-existing non-unique index idx_org_memberships_user is superseded by
-- this unique index; it is left in place (harmless, slightly redundant) to keep
-- the migration idempotent and avoid a DROP that could fail on partial deploys.

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_memberships_single_user
    ON org_memberships(user_id);
