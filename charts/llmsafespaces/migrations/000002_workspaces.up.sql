CREATE TABLE IF NOT EXISTS workspaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    namespace VARCHAR(255) NOT NULL DEFAULT 'default',
    runtime VARCHAR(255),
    security_level VARCHAR(50) DEFAULT 'standard',
    storage_size VARCHAR(50) DEFAULT '5Gi',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_workspaces_user_id ON workspaces(user_id);

ALTER TABLE sandboxes ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id);
