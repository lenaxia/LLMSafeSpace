-- 000044 down: revert the backfill.
--
-- Cannot determine which org_id values and which credential bindings
-- this migration created vs which were created by application code,
-- so the down is intentionally a no-op rather than data-destructive.
-- The orphan rows can be re-created in the original NULL state if
-- truly necessary, but doing so silently would risk breaking active
-- workspaces.
--
-- If you need to re-run the migration after manually clearing rows,
-- the up migration is itself idempotent (ON CONFLICT DO NOTHING for
-- bindings, and the UPDATE only touches NULL rows).

SELECT 1;
