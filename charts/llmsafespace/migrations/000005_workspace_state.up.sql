ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS phase TEXT NOT NULL DEFAULT '';
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS pvc_state TEXT NOT NULL DEFAULT 'none';
CREATE INDEX IF NOT EXISTS idx_workspaces_phase ON workspaces(phase);
CREATE INDEX IF NOT EXISTS idx_workspaces_deleted ON workspaces(deleted_at) WHERE deleted_at IS NOT NULL;
