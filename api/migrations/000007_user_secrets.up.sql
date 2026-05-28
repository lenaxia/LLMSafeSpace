-- Encrypted user secrets (zero-knowledge at rest).
CREATE TABLE IF NOT EXISTS user_secrets (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name           VARCHAR(255) NOT NULL,
    type           VARCHAR(50) NOT NULL,
    ciphertext     BYTEA NOT NULL,
    key_version    INTEGER NOT NULL,
    metadata       JSONB NOT NULL DEFAULT '{}',
    created_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, name)
);

CREATE INDEX IF NOT EXISTS idx_user_secrets_user_id ON user_secrets(user_id);

-- Which secrets are attached to which workspaces.
CREATE TABLE IF NOT EXISTS user_secret_bindings (
    secret_id      UUID NOT NULL REFERENCES user_secrets(id) ON DELETE CASCADE,
    workspace_id   VARCHAR(36) NOT NULL,
    created_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (secret_id, workspace_id)
);

CREATE INDEX IF NOT EXISTS idx_secret_bindings_workspace ON user_secret_bindings(workspace_id);

-- Audit log (append-only).
CREATE TABLE IF NOT EXISTS secret_audit_log (
    id             BIGSERIAL PRIMARY KEY,
    user_id        VARCHAR(36) NOT NULL,
    action         VARCHAR(50) NOT NULL,
    secret_id      UUID,
    workspace_id   VARCHAR(36),
    metadata       JSONB DEFAULT '{}',
    timestamp      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_user_time ON secret_audit_log(user_id, timestamp DESC);
