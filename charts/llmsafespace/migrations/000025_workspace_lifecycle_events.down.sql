BEGIN;

DROP INDEX IF EXISTS idx_wle_owner;
DROP INDEX IF EXISTS idx_wle_workspace;
DROP TABLE IF EXISTS workspace_lifecycle_events;

COMMIT;
