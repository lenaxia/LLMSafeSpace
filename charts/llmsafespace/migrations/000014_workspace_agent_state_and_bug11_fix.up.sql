-- Migration 000014: workspace_agent_state + Bug 11 fix + Bug 12 fix
-- Epic 27a: Credential Reload Foundation

-- Bug 11 fix: align user_secret_bindings.workspace_id with workspaces.id type
-- and add the FK that should have existed from the start.
-- Step 1: purge rows with non-UUID workspace_id values (test data, validation
-- stubs, etc.) that would prevent the type cast. These rows reference workspaces
-- that do not exist in the workspaces table and have no K8s CRD behind them.
-- Guard: skip when column is already UUID (re-apply after successful first run).
DO $$ BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'user_secret_bindings'
      AND column_name = 'workspace_id'
      AND data_type = 'character varying'
  ) THEN
    DELETE FROM user_secret_bindings
    WHERE workspace_id !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';
  END IF;
END $$;

-- Step 2: widen column to 36 chars (no-op if already VARCHAR(36)) then convert to UUID.
DO $$ BEGIN
  ALTER TABLE user_secret_bindings
      ALTER COLUMN workspace_id TYPE UUID USING workspace_id::uuid;
EXCEPTION
  WHEN others THEN
    RAISE NOTICE 'user_secret_bindings.workspace_id already UUID or conversion skipped: %', SQLERRM;
END $$;

DO $$ BEGIN
  ALTER TABLE user_secret_bindings
      ADD CONSTRAINT user_secret_bindings_workspace_id_fkey
          FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;
EXCEPTION
  WHEN duplicate_object THEN NULL;
  WHEN foreign_key_violation THEN
    RAISE NOTICE 'user_secret_bindings_workspace_id_fkey skipped: existing rows violate referential integrity: %', SQLERRM;
END $$;

-- Bug 12 fix: align workspaces.user_id with users.id type (VARCHAR(36))
-- and add the missing FK. ON DELETE RESTRICT (not CASCADE) is intentional:
-- workspaces use soft-delete (deleted_at column); users are hard-deleted.
-- CASCADE would hard-delete workspace rows, bypassing soft-delete, losing audit
-- records, and orphaning live Kubernetes CRD objects.
-- RESTRICT means DeleteUser() fails if workspace rows still reference the user.
--
-- Step 1: soft-delete orphan workspaces whose user_id references a user that no
-- longer exists in the users table. This marks them for cleanup but does NOT
-- remove the FK violation: PostgreSQL validates ALL rows (including soft-deleted)
-- when adding a FK constraint. The FK addition below catches
-- foreign_key_violation to handle this gracefully — on databases with orphan
-- data the constraint will not be created but the migration proceeds.
UPDATE workspaces
SET deleted_at = NOW(), updated_at = NOW()
WHERE deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM users WHERE users.id = workspaces.user_id);

-- Step 2: narrow column from VARCHAR(255) to VARCHAR(36) (UUID string length).
DO $$ BEGIN
  ALTER TABLE workspaces
      ALTER COLUMN user_id TYPE VARCHAR(36) USING user_id::varchar(36);
EXCEPTION
  WHEN others THEN
    RAISE NOTICE 'workspaces.user_id already VARCHAR(36) or conversion skipped: %', SQLERRM;
END $$;

DO $$ BEGIN
  ALTER TABLE workspaces
      ADD CONSTRAINT workspaces_user_id_fkey
          FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;
EXCEPTION
  WHEN duplicate_object THEN NULL;
  WHEN foreign_key_violation THEN
    RAISE NOTICE 'workspaces_user_id_fkey skipped: existing rows violate referential integrity: %', SQLERRM;
END $$;

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
-- workspace_id is now UUID (Bug 11 fix above), so no cast is needed.
DO $$ BEGIN
  INSERT INTO workspace_agent_state (workspace_id, last_credential_changed_at, pending_refresh)
  SELECT DISTINCT b.workspace_id, NOW(), TRUE
  FROM user_secret_bindings b
  JOIN user_secrets s ON s.id = b.secret_id
  WHERE s.type = 'llm-provider'
  ON CONFLICT (workspace_id) DO NOTHING;
EXCEPTION
  WHEN datatype_mismatch THEN
    -- Fallback: column is still VARCHAR if Bug 11 ALTER was skipped; cast explicitly.
    INSERT INTO workspace_agent_state (workspace_id, last_credential_changed_at, pending_refresh)
    SELECT DISTINCT b.workspace_id::uuid, NOW(), TRUE
    FROM user_secret_bindings b
    JOIN user_secrets s ON s.id = b.secret_id
    WHERE s.type = 'llm-provider'
    ON CONFLICT (workspace_id) DO NOTHING;
END $$;
