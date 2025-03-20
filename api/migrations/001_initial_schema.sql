-- Create users table
CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(36) PRIMARY KEY,
    username VARCHAR(255) NOT NULL UNIQUE,
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create API keys table
CREATE TABLE IF NOT EXISTS api_keys (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key VARCHAR(255) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE
);

-- Create sandboxes table
CREATE TABLE IF NOT EXISTS sandboxes (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    runtime VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    terminated_at TIMESTAMP WITH TIME ZONE
);

-- Create warm pools table
CREATE TABLE IF NOT EXISTS warm_pools (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    runtime VARCHAR(255) NOT NULL,
    min_size INTEGER NOT NULL DEFAULT 1,
    max_size INTEGER NOT NULL DEFAULT 5,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create permissions table
CREATE TABLE IF NOT EXISTS permissions (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    resource_type VARCHAR(255) NOT NULL,
    resource_id VARCHAR(255) NOT NULL,
    action VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, resource_type, resource_id, action)
);

-- Create execution history table
CREATE TABLE IF NOT EXISTS execution_history (
    id VARCHAR(36) PRIMARY KEY,
    sandbox_id VARCHAR(36) NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,
    type VARCHAR(50) NOT NULL,
    content TEXT NOT NULL,
    exit_code INTEGER,
    stdout TEXT,
    stderr TEXT,
    started_at TIMESTAMP WITH TIME ZONE NOT NULL,
    completed_at TIMESTAMP WITH TIME ZONE,
    duration_ms INTEGER
);

-- Create file operations table
CREATE TABLE IF NOT EXISTS file_operations (
    id VARCHAR(36) PRIMARY KEY,
    sandbox_id VARCHAR(36) NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,
    operation VARCHAR(50) NOT NULL,
    path VARCHAR(1024) NOT NULL,
    size BIGINT,
    timestamp TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create package installations table
CREATE TABLE IF NOT EXISTS package_installations (
    id VARCHAR(36) PRIMARY KEY,
    sandbox_id VARCHAR(36) NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,
    package_name VARCHAR(255) NOT NULL,
    version VARCHAR(255),
    manager VARCHAR(50) NOT NULL,
    status VARCHAR(50) NOT NULL,
    installed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Create indexes
CREATE INDEX idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX idx_sandboxes_user_id ON sandboxes(user_id);
CREATE INDEX idx_warm_pools_user_id ON warm_pools(user_id);
CREATE INDEX idx_permissions_user_id ON permissions(user_id);
CREATE INDEX idx_permissions_resource ON permissions(resource_type, resource_id);
CREATE INDEX idx_execution_history_sandbox_id ON execution_history(sandbox_id);
CREATE INDEX idx_file_operations_sandbox_id ON file_operations(sandbox_id);
CREATE INDEX idx_package_installations_sandbox_id ON package_installations(sandbox_id);
