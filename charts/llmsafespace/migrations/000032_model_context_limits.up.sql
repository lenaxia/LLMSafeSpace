-- Add model_context_limits JSONB column to provider_credentials.
-- Stores a map of model_id → context_window_size (tokens) set by the
-- credential owner when configuring the allowlist via the UI fetch-models flow.
-- This is separate from model_allowlist (TEXT[]) to preserve backward
-- compatibility — existing rows get an empty map, no data loss.
ALTER TABLE provider_credentials
    ADD COLUMN IF NOT EXISTS model_context_limits JSONB NOT NULL DEFAULT '{}';
