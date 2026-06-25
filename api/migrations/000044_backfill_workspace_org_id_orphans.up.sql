-- 000044: Backfill workspaces.org_id and workspace_credential_bindings
-- for org-credential auto-binding. Closes deployment-timing orphans
-- from PR #209 (auto-attribution at workspace-create) and PR #228
-- (D4 owner-migration in CreateOrgWithAdmin).
--
-- Why this exists
-- ===============
-- Two earlier fixes added org_id population to two distinct code paths:
--   * PR #209 (merged 2026-06-18): auto-attribute new workspaces to
--     their owner's org when CreateWorkspace is called.
--   * PR #228 (merged 2026-06-18): in CreateOrgWithAdmin, migrate the
--     owner's existing personal workspaces (org_id IS NULL) to the org.
--
-- Both fixes are correct going forward but neither backfilled rows
-- created during their pre-fix windows:
--
--   * Workspaces created before the org-creation moment carry
--     org_id=NULL forever (the D4 migration only runs at the moment
--     of org creation, and orgs created before PR #228 deployed never
--     ran the migration block).
--   * Workspaces created in the deployment lag between PR #209's
--     merge and its prod rollout went through the pre-fix CreateWorkspace
--     path and stored org_id=NULL even though the user was already in
--     an org.
--
-- Production observation (2026-06-25): user 4382f558 had 11 active
-- workspaces; 5 of them carried org_id=NULL despite the user being
-- a member of org 514fdb19. Those 5 workspaces also missed the
-- auto-binding for the org's `custom` credential because
-- BindCredentialToAllOrgWorkspaces filters w.org_id = $orgID and
-- silently skipped NULL-org_id rows. Result: chat sessions on those
-- workspaces could not resolve `custom/glm-5.2` because the org
-- credential was never bound to the workspace.
--
-- What this migration does
-- ========================
-- 1. UPDATE workspaces SET org_id = m.org_id WHERE org_id IS NULL
--    AND the user has an org_membership. Strict join — only touches
--    rows where the user-to-org link is unambiguous (org_memberships
--    has UNIQUE (user_id), so each user maps to at most one org).
-- 2. INSERT INTO workspace_credential_bindings for every (org cred,
--    workspace) pair that is now consistent but lacks the binding.
--    ON CONFLICT DO NOTHING so re-running is a no-op.
--
-- Both statements are idempotent. Running this on a healthy DB is
-- a no-op. Running it twice in a row is the same as running it once.
--
-- Soft-deleted workspaces are NOT touched.
-- Soft-deleted credentials would be filtered by the JOIN; the schema
-- enforces that `provider_credentials` has no soft-delete column, so
-- every org credential that exists is eligible for auto-binding (this
-- matches the post-fix BindCredentialToAllOrgWorkspaces behaviour).
--
-- Bug class invariants this migration enforces:
--   * post-migration, no workspace where the owner is in an org has
--     org_id IS NULL (modulo soft-deleted rows).
--   * post-migration, every (org_credential, workspace_in_same_org)
--     pair has a workspace_credential_bindings row.

BEGIN;

-- Step 1: Backfill workspaces.org_id from org_memberships.
-- The org_memberships index `idx_org_memberships_single_user` enforces
-- UNIQUE (user_id), so the join produces at most one row per workspace
-- and there is no risk of attributing a workspace to multiple orgs.
--
-- Soft-deleted orgs are excluded: org_memberships is not cleaned up
-- when an organization is soft-deleted, so without the deleted_at IS
-- NULL filter on `organizations`, we would attribute orphan workspaces
-- to a dead org.
UPDATE workspaces w
   SET org_id = m.org_id,
       updated_at = NOW()
  FROM org_memberships m
  JOIN organizations o ON o.id = m.org_id AND o.deleted_at IS NULL
 WHERE w.user_id = m.user_id
   AND w.org_id IS NULL
   AND w.deleted_at IS NULL;

-- Step 2: Backfill workspace_credential_bindings for org credentials.
-- Mirrors BindCredentialToAllOrgWorkspaces' INSERT exactly so the
-- application path and the migration path produce identical rows.
--
-- Cast pc.owner_id::uuid because owner_id is TEXT (it stores either a
-- UUID for owner_type='org' / owner_type='user' or '_platform' for
-- owner_type='admin'). The owner_type='org' filter guarantees the
-- value is a valid UUID; the cast lets the join on workspaces.org_id
-- (uuid) succeed without an "operator does not exist: uuid = text"
-- error. The application code (BindCredentialToAllOrgWorkspaces in
-- pkg/secrets/org_credential_store.go) compares w.org_id to a Go
-- string parameter; pgx auto-casts at the boundary, masking the same
-- shape mismatch.
INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
SELECT pc.id, w.id, 'auto', 5
  FROM provider_credentials pc
  JOIN workspaces w ON w.org_id = pc.owner_id::uuid
 WHERE pc.owner_type = 'org'
   AND w.deleted_at IS NULL
ON CONFLICT (credential_id, workspace_id) DO NOTHING;

COMMIT;
