-- Epic 30 audit fixes (migration 000016).
--
-- 1. Backfill user-owned credentials to all existing workspaces (H-5).
--    Pre-existing workspaces were created before Epic 30, so
--    SeedWorkspaceCredentials never ran for them.  This one-time INSERT
--    brings them in line with the invariant.
--
-- 2. Add source_type column to GetCredentialBindings result set so the
--    UI can distinguish auto-bindings from explicit ones (M-1).
--    No schema change needed — source_type already exists in
--    workspace_credential_bindings.  The store query is updated to
--    SELECT it.  This migration is a no-op schema-wise.
--
-- Both operations are idempotent (ON CONFLICT DO NOTHING).

INSERT INTO workspace_credential_bindings
    (credential_id, workspace_id, source_type, within_priority)
SELECT pc.id, w.id, 'auto', 10
FROM provider_credentials pc
JOIN workspaces w ON w.user_id = pc.owner_id
WHERE pc.owner_type = 'user'
  AND NOT EXISTS (
      SELECT 1 FROM workspace_credential_bindings wcb
      WHERE wcb.credential_id = pc.id AND wcb.workspace_id = w.id
  )
ON CONFLICT (credential_id, workspace_id) DO NOTHING;
