DROP INDEX IF EXISTS idx_session_index_parent;
ALTER TABLE session_index DROP COLUMN IF EXISTS parent_session_id;
