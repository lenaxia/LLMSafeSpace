-- Epic 43, US-43.19: User-level suspension (D19) — rollback.

BEGIN;

DROP INDEX IF EXISTS idx_users_status;
ALTER TABLE users DROP COLUMN IF EXISTS status;

COMMIT;
