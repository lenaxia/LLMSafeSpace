-- User key material for zero-knowledge secret encryption.
-- Never contains plaintext secrets or DEKs.
CREATE TABLE IF NOT EXISTS user_keys (
    user_id        VARCHAR(36) PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    key_version    INTEGER NOT NULL DEFAULT 1,
    wrapped_dek    BYTEA NOT NULL,
    wrapped_dek_recovery BYTEA,
    salt           BYTEA NOT NULL,
    recovery_salt  BYTEA,
    created_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    rotated_at     TIMESTAMP WITH TIME ZONE
);
