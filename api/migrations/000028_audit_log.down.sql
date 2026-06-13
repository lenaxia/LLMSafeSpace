BEGIN;

DROP INDEX IF EXISTS idx_audit_actor;
DROP INDEX IF EXISTS idx_audit_domain;
DROP TABLE IF EXISTS audit_log;

COMMIT;
