-- Migration 000036 down: remove single-org enforcement
--
-- Reverses D8. Restores the ability for a user to belong to multiple orgs.
-- This does not restore any data (none existed) but allows future re-enrollment
-- in multiple orgs if the product direction reverses.

DROP INDEX IF EXISTS idx_org_memberships_single_user;
