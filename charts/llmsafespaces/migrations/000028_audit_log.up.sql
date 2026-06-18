-- US-12.10: Generalized audit log

BEGIN;

CREATE TABLE IF NOT EXISTS audit_log (
    id          BIGSERIAL PRIMARY KEY,
    actor_id    TEXT NOT NULL,
    domain      TEXT NOT NULL CHECK (domain IN ('billing','secrets','admin')),
    action      TEXT NOT NULL,
    target_id   TEXT,
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_domain ON audit_log(domain, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_actor  ON audit_log(actor_id, created_at DESC);

COMMIT;
