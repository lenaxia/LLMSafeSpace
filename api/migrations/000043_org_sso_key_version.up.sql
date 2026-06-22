-- US-50.3: add key_version to org_sso_configs so the rotation CLI (US-50.5)
-- can identify which rows need re-wrapping after a KEK rotation.
ALTER TABLE org_sso_configs ADD COLUMN IF NOT EXISTS key_version INTEGER NOT NULL DEFAULT 1;
