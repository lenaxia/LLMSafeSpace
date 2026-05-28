DROP TRIGGER IF EXISTS trg_credential_sets_updated_at ON credential_sets;
DROP TRIGGER IF EXISTS trg_user_settings_updated_at ON user_settings;
DROP TRIGGER IF EXISTS trg_instance_settings_updated_at ON instance_settings;
DROP FUNCTION IF EXISTS update_updated_at_column();
DROP TABLE IF EXISTS credential_sets;
DROP TABLE IF EXISTS user_settings;
DROP TABLE IF EXISTS instance_settings;
