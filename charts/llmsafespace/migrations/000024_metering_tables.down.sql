BEGIN;

DROP INDEX IF EXISTS idx_dlq_unresolved;
DROP TABLE IF EXISTS usage_events_dlq;

DROP INDEX IF EXISTS idx_usage_idempotency;
DROP INDEX IF EXISTS idx_usage_type_period;
DROP INDEX IF EXISTS idx_usage_workspace;
DROP INDEX IF EXISTS idx_usage_actor_period;
DROP INDEX IF EXISTS idx_usage_owner_period;
DROP TABLE IF EXISTS usage_events;

COMMIT;
