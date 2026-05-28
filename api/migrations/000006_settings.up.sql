-- Settings tables for Epic 9: Configuration & Settings

CREATE TABLE instance_settings (
  key TEXT PRIMARY KEY,
  value JSONB NOT NULL,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

CREATE TABLE user_settings (
  user_id VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  value JSONB NOT NULL,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, key)
);

CREATE TABLE credential_sets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT UNIQUE NOT NULL,
  is_default BOOLEAN NOT NULL DEFAULT false,
  providers_encrypted BYTEA NOT NULL,
  key_version SMALLINT NOT NULL DEFAULT 1,
  model_allowlist TEXT[] NOT NULL DEFAULT '{}',
  assigned_to JSONB NOT NULL DEFAULT '"all"',
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
);

-- Only one default credential set at a time
CREATE UNIQUE INDEX idx_credential_sets_default ON credential_sets (is_default) WHERE is_default = true;

-- Auto-update updated_at on all settings/credential tables
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_instance_settings_updated_at
  BEFORE UPDATE ON instance_settings
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER trg_user_settings_updated_at
  BEFORE UPDATE ON user_settings
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER trg_credential_sets_updated_at
  BEFORE UPDATE ON credential_sets
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
