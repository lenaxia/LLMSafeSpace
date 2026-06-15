-- Migration 000035: Drop org DEK infrastructure (D7 in design 0031)
--
-- Org credentials are now encrypted with a server-side KEK (deriveServerKey
-- label "org-credentials"), not the per-org DEK. The OrgKeyService,
-- org_key_members table, and pending_key_wrap column are eliminated entirely.
-- See design/0031_2026-06-15_org-access-control-portal-architecture.md.
--
-- No data migration needed: no orgs exist in production (confirmed pre-deploy).
-- All new org credentials will be server-KEK-encrypted from the start.

-- Drop the org_key_members table (per-admin wrapped DEK copies).
DROP TABLE IF EXISTS org_key_members;

-- Drop the pending_key_wrap column from org_memberships.
-- With no org DEK, there is nothing to "set up" — admins have immediate access.
ALTER TABLE org_memberships DROP COLUMN IF EXISTS pending_key_wrap;

-- Drop the partial index on pending_key_wrap (if it exists — created by migration 029).
DROP INDEX IF EXISTS idx_org_memberships_pending;
