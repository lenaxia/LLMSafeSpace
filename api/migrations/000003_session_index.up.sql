CREATE TABLE IF NOT EXISTS session_index (
    workspace_id   TEXT NOT NULL,
    session_id     TEXT NOT NULL,
    title          TEXT,
    last_message_at TIMESTAMPTZ,
    message_count  INTEGER NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, session_id)
);

CREATE INDEX IF NOT EXISTS idx_session_index_workspace ON session_index (workspace_id, last_message_at DESC NULLS LAST);
