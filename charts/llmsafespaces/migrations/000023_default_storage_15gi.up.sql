-- Migration 000023: increase workspace.defaultStorageSize from 10Gi to 15Gi.
--
-- /tmp is now a subPath on the workspace PVC (see pod_builder.go SubPath: "tmp").
-- The PVC now carries three subtrees: workspace/, home/, and tmp/. The extra
-- 5Gi headroom ensures the tmp/ subtree (agent-config.json, secrets-env, any
-- ephemeral agent output) plus workspace/ and home/ can grow without exhausting
-- the default allocation.
--
-- workspace.maxStorageSize is intentionally NOT seeded here. That setting has
-- been removed from the schema — PVC size enforcement is now handled solely by
-- the admission webhook (webhooks.maxWorkspaceStorageGi in values.yaml).
--
-- Idempotent: ON CONFLICT DO UPDATE ensures re-applying is safe and the new
-- value is always applied even if migration 000018 had already seeded 10Gi.

INSERT INTO instance_settings (key, value)
VALUES ('workspace.defaultStorageSize', '"15Gi"')
ON CONFLICT (key) DO UPDATE SET value = '"15Gi"';
