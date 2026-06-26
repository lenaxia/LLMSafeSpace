-- 000045 down: drop the durable JWT session DEK table.
--
-- Reverting this migration means all in-flight sessions lose their
-- durable DEK row. The Redis cache (dek:<jti>) is unaffected — sessions
-- whose DEK is still cached remain functional. Sessions whose Redis
-- DEK was evicted will fall back to soft-unlock (if Epic 56 endpoint
-- code is still present) or to full logout (if reverting both schema
-- and code together).
--
-- Data loss is acceptable because the wrapped DEK in this table can
-- be re-derived by the user supplying their password (existing
-- UnlockDEK code path). No user data is destroyed by this rollback.

DROP TABLE IF EXISTS jwt_sessions;
