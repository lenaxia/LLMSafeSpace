-- Migration 000018: seed workspace.defaultStorageSize instance setting.
--
-- Prior to this migration the only default for workspace storage size was
-- hardcoded in the frontend (workspaces.ts). API-direct and SDK callers
-- received a 400 if they omitted storageSize. This migration seeds the
-- authoritative default into instance_settings so the API can supply it
-- without client-side knowledge.
--
-- The value "10Gi" replaces the old frontend default of "5Gi". The increase
-- accounts for /home/sandbox now sharing the PVC (see pod_builder.go subPath
-- migration) — combined workspace + home usage needs more headroom.
--
-- Idempotent: ON CONFLICT DO NOTHING means re-applying is safe.

INSERT INTO instance_settings (key, value)
VALUES ('workspace.defaultStorageSize', '"10Gi"')
ON CONFLICT (key) DO NOTHING;
