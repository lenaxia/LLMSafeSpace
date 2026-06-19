-- Epic 43, US-43.7: Org policy schema.
--
-- Stores org-scoped policies as a key-value table. Per D15, Phase 2 ships 4
-- policies: allowed_models, allowed_providers, max_workspaces_per_member,
-- max_active_workspaces_per_member. The value column is JSONB so each policy
-- type controls its own shape. Missing key = default (no restriction).

BEGIN;

CREATE TABLE IF NOT EXISTS org_policies (
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key         TEXT NOT NULL CHECK (key IN (
        'allowed_models',
        'allowed_providers',
        'max_workspaces_per_member',
        'max_active_workspaces_per_member'
    )),
    value       JSONB NOT NULL DEFAULT '{}',
    updated_by  TEXT REFERENCES users(id),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, key)
);

COMMIT;
