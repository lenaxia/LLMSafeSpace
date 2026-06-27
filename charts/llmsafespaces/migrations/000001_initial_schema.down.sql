-- Pre-launch migration collapse — there is no incremental rollback
-- target. The down migration nukes the database to a clean slate.
--
-- Once production data exists this will be unusable; the up migration
-- is a one-way door from that point on.
--
-- Note on golang-migrate: after running this down, the migrate CLI's
-- `migrate down` will report a non-fatal TRUNCATE error because it
-- tries to clear `schema_migrations` (which this down dropped along
-- with `public`). The schema IS correctly nuked. The next `migrate up`
-- works normally because golang-migrate recreates `schema_migrations`
-- on first apply. CI uses raw psql for the round-trip test and is
-- unaffected by this CLI quirk.
DROP SCHEMA IF EXISTS public CASCADE;
CREATE SCHEMA public;
GRANT ALL ON SCHEMA public TO PUBLIC;

-- Restore the default schema comment that Postgres ships with. Without
-- this, pg_dump observes an empty comment on the recreated schema and
-- the round-trip schema-diff gate flags a spurious difference vs. the
-- initial database state.
COMMENT ON SCHEMA public IS 'standard public schema';
