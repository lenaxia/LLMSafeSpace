DROP INDEX IF EXISTS idx_workspaces_deleted;
DROP INDEX IF EXISTS idx_workspaces_phase;
ALTER TABLE workspaces DROP COLUMN IF EXISTS pvc_state;
ALTER TABLE workspaces DROP COLUMN IF EXISTS phase;
