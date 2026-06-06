-- Rollback: remove the auto-bindings inserted by the forward migration.
-- We only remove rows where source_type = 'auto' that were created for
-- user-owned credentials (rows that existed before this migration ran
-- as explicit bindings are unaffected).
DELETE FROM workspace_credential_bindings wcb
USING provider_credentials pc, workspaces w
WHERE wcb.credential_id = pc.id
  AND wcb.workspace_id  = w.id
  AND pc.owner_type     = 'user'
  AND w.user_id         = pc.owner_id
  AND wcb.source_type   = 'auto';
