-- Add parent_session_id to support sidebar hierarchy (subagent/subtask sessions
-- under their parent). Nullable: top-level sessions have NULL.
--
-- We do not enforce a foreign key to (workspace_id, session_id) for two
-- reasons:
--   1. opencode emits session.created for the child before the parent in
--      some race conditions, and a strict FK would force ordering.
--   2. If a parent session is deleted, the surviving children become
--      "orphans" — the sidebar shows them under a synthetic group rather
--      than hard-deleting the rows (matches user-requested UX).
--
-- Index added to make "list children of a parent" fast for hierarchy
-- queries even when the table grows large.
ALTER TABLE session_index
    ADD COLUMN IF NOT EXISTS parent_session_id TEXT;

CREATE INDEX IF NOT EXISTS idx_session_index_parent
    ON session_index (workspace_id, parent_session_id)
    WHERE parent_session_id IS NOT NULL;
