-- Migration 000014: workspace_agent_state + Bug 11 fix + Bug 12 fix
-- Epic 27a: Credential Reload Foundation

-- Bug 11 fix: align user_secret_bindings.workspace_id with workspaces.id type
-- and add the FK that should have existed from the start.
-- Existing rows already contain valid 36-char UUID strings; the cast is a no-op.
ALTER TABLE user_secret_bindings
    ALTER COLUMN workspace_id TYPE UUID USING workspace_id::uuid;

ALTER TABLE user_secret_bindings
    ADD CONSTRAINT user_secret_bindings_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;

-- Bug 12 fix: align workspaces.user_id with users.id type (VARCHAR(36))
-- and add the missing FK. ON DELETE RESTRICT (not CASCADE) is intentional:
-- workspaces use soft-delete (deleted_at column); users are hard-deleted.
-- CASCADE would hard-delete workspace rows, bypassing soft-delete, losing audit
-- records, and orphaning live Kubernetes CRD objects.
-- RESTRICT means DeleteUser() fails if workspace rows still reference the user.
ALTER TABLE workspaces
    ALTER COLUMN user_id TYPE VARCHAR(36) USING user_id::varchar(36);

ALTER TABLE workspaces
    ADD CONSTRAINT workspaces_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

-- New per-workspace agent state, separate from workspace identity.
-- One row per workspace, created lazily on first credential mutation.
CREATE TABLE IF NOT EXISTS workspace_agent_state (
    workspace_id                UUID PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    last_credential_changed_at  TIMESTAMP WITH TIME ZONE,
    last_agent_disposed_at      TIMESTAMP WITH TIME ZONE,
    pending_refresh             BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at                  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_workspace_agent_state_pending
    ON workspace_agent_state (pending_refresh)
    WHERE pending_refresh = TRUE;

-- Backfill: any workspace that currently has llm-provider bindings is
-- treated as "credentials changed at migration time, never reloaded."
-- This causes the banner to appear for existing users on first login
-- after migration, prompting them to reload once to establish a clean baseline.
INSERT INTO workspace_agent_state (workspace_id, last_credential_changed_at, pending_refresh)
SELECT DISTINCT b.workspace_id, NOW(), TRUE
FROM user_secret_bindings b
JOIN user_secrets s ON s.id = b.secret_id
WHERE s.type = 'llm-provider'
ON CONFLICT (workspace_id) DO NOTHING;
