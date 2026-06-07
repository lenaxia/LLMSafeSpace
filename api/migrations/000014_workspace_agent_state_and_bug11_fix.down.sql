-- Rollback migration 000014

DROP TABLE IF EXISTS workspace_agent_state;

ALTER TABLE workspaces DROP CONSTRAINT IF EXISTS workspaces_user_id_fkey;
DO $$ BEGIN
  ALTER TABLE workspaces ALTER COLUMN user_id TYPE VARCHAR(255) USING user_id::text;
EXCEPTION WHEN others THEN NULL;
END $$;

ALTER TABLE user_secret_bindings DROP CONSTRAINT IF EXISTS user_secret_bindings_workspace_id_fkey;
DO $$ BEGIN
  ALTER TABLE user_secret_bindings ALTER COLUMN workspace_id TYPE VARCHAR(36) USING workspace_id::text;
EXCEPTION WHEN others THEN NULL;
END $$;
