ALTER TABLE sandboxes DROP COLUMN IF EXISTS workspace_id;
DROP INDEX IF EXISTS idx_workspaces_user_id;
DROP TABLE IF EXISTS workspaces;
