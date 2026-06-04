-- Rollback migration 000014

DROP TABLE IF EXISTS workspace_agent_state;

ALTER TABLE workspaces DROP CONSTRAINT IF EXISTS workspaces_user_id_fkey;
ALTER TABLE workspaces ALTER COLUMN user_id TYPE VARCHAR(255) USING user_id::text;

ALTER TABLE user_secret_bindings DROP CONSTRAINT IF EXISTS user_secret_bindings_workspace_id_fkey;
ALTER TABLE user_secret_bindings ALTER COLUMN workspace_id TYPE VARCHAR(36) USING workspace_id::text;
