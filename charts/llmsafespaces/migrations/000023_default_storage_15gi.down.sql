-- Migration 000023 rollback: restore workspace.defaultStorageSize to 10Gi.
--
-- Does not restore workspace.maxStorageSize or
-- workspace.defaultResources.ephemeralStorage — those settings have been
-- removed from the schema and should not be re-seeded on rollback.

UPDATE instance_settings SET value = '"10Gi"' WHERE key = 'workspace.defaultStorageSize';
