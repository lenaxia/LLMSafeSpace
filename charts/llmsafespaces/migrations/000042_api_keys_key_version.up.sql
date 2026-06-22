-- US-50.3: add key_version to api_keys so the rotation CLI (US-50.5) can
-- identify which rows need re-wrapping after a KEK rotation.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_version INTEGER NOT NULL DEFAULT 1;
