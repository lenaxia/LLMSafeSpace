-- Restore the sandbox tables that 000004.up dropped.
--
-- These tables represented per-execution sandbox metadata in the V1
-- architecture; V2 replaces this with the Workspace CRD plus
-- workspace metadata in the workspaces table. The down migration is
-- here to satisfy the up→down→up round-trip invariant (worklog 0101 /
-- migration-safety CI gate); after a real down + up cycle the schema
-- is identical to the post-up state.
--
-- Note: since 000004.up uses DROP TABLE (not data preservation), this
-- down migration recreates the schema only. Any rows that existed
-- before 000004.up are gone — the up migration accepted that data
-- loss because the project was not yet live.
--
-- The CREATE order follows FK dependency: sandboxes (parent) before
-- sandbox_labels (child). The columns + FK match 000001.up + 000002.up
-- (which added workspace_id) so the round-trip schema diff is empty.

CREATE TABLE IF NOT EXISTS sandboxes (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    runtime VARCHAR(255) NOT NULL,
    name VARCHAR(255),
    status VARCHAR(50) DEFAULT 'active',
    labels JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    terminated_at TIMESTAMP WITH TIME ZONE,
    workspace_id UUID REFERENCES workspaces(id)
);

CREATE TABLE IF NOT EXISTS sandbox_labels (
    sandbox_id VARCHAR(36) NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,
    key VARCHAR(255) NOT NULL,
    value VARCHAR(255) NOT NULL,
    PRIMARY KEY (sandbox_id, key)
);

CREATE INDEX IF NOT EXISTS idx_sandboxes_user_id ON sandboxes(user_id);

-- The execution_history / file_operations / package_installations
-- tables that 000004.up also drops were never created by any
-- migration in this repo (legacy schema from before the project
-- moved to its current migration tooling). 000004.up uses
-- DROP TABLE IF EXISTS to be safe; we don't recreate them here for
-- the same reason — the post-up state these correspond to is "tables
-- absent", which we already have. If a future contributor adds
-- migrations that create these tables, they MUST add the symmetric
-- DROP TABLE here.
