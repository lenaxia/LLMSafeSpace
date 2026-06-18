-- US-12.3: Workspace lifecycle events for billing dispute resolution
-- and compute time gap detection.

BEGIN;

CREATE TABLE IF NOT EXISTS workspace_lifecycle_events (
    id              BIGSERIAL PRIMARY KEY,
    workspace_id    TEXT NOT NULL,
    owner_id        TEXT NOT NULL,
    owner_type      TEXT NOT NULL DEFAULT 'user',
    from_phase      TEXT,
    to_phase        TEXT NOT NULL,
    resource_tier   TEXT,
    event_time      TIMESTAMPTZ NOT NULL,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_wle_workspace ON workspace_lifecycle_events(workspace_id, event_time);
CREATE INDEX IF NOT EXISTS idx_wle_owner     ON workspace_lifecycle_events(owner_id, owner_type, event_time);

COMMIT;
