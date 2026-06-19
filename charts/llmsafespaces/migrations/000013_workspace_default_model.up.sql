-- Add default_model to workspaces table.
-- Model selection is not sensitive data — no encryption needed.
-- Stored as workspace-level metadata to avoid DEK/session requirements
-- and read-modify-write races on encrypted secrets.
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS default_model VARCHAR(255);
