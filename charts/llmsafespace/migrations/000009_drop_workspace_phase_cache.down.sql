-- Restore the workspace phase/pvc_state cache.
--
-- Rollback for migration 9. Re-creates columns with the same defaults as
-- migration 5 (NOT NULL, default '' / 'none') and the index on phase. Note
-- that running this rollback alone is insufficient to restore the cache:
-- the API code that wrote to these columns (syncPhase) was also removed
-- alongside this migration. Code rollback is required to repopulate them.
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS phase TEXT NOT NULL DEFAULT '';
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS pvc_state TEXT NOT NULL DEFAULT 'none';
CREATE INDEX IF NOT EXISTS idx_workspaces_phase ON workspaces(phase);
