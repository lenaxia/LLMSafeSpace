-- Drop the DB-side cache of workspace phase/pvc_state.
--
-- The Workspace CRD is the source of truth for both fields. The cache
-- introduced in migration 5 was populated best-effort by the API's syncPhase
-- helper but could diverge from the CRD (notably right after creation, when
-- syncPhase no-oped on an empty phase). The list endpoint now reads phase
-- directly from CRDs via a label-scoped LIST.
--
-- pvc_state was never read by any code path.
DROP INDEX IF EXISTS idx_workspaces_phase;
ALTER TABLE workspaces DROP COLUMN IF EXISTS pvc_state;
ALTER TABLE workspaces DROP COLUMN IF EXISTS phase;
