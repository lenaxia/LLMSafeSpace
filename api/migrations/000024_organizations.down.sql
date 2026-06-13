-- Epic 11, US-11.1: Organizations schema rollback.
ALTER TABLE workspaces DROP COLUMN IF EXISTS org_id;
DROP TABLE IF EXISTS org_key_members;
DROP TABLE IF EXISTS org_memberships;
DROP TABLE IF EXISTS organizations;
