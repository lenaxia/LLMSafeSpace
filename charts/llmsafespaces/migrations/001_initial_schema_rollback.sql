-- Drop tables in reverse order of creation to respect foreign key constraints
DROP TABLE IF EXISTS package_installations;
DROP TABLE IF EXISTS file_operations;
DROP TABLE IF EXISTS execution_history;
DROP TABLE IF EXISTS permissions;
DROP TABLE IF EXISTS warm_pools;
DROP TABLE IF EXISTS sandboxes;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS users;
