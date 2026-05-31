-- Drop sandbox-related tables in FK dependency order
DROP TABLE IF EXISTS execution_history;
DROP TABLE IF EXISTS file_operations;
DROP TABLE IF EXISTS package_installations;
DROP TABLE IF EXISTS sandbox_labels;
DROP TABLE IF EXISTS sandboxes;
