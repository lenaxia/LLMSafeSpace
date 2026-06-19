-- Epic 11, US-11.1: Organizations schema.
--
-- All CREATE statements use IF NOT EXISTS. ALTER TABLE ADD COLUMN IF NOT EXISTS
-- requires PostgreSQL 9.6+; our target is PG 14+.
--
-- update_updated_at_column() already exists from migration 000006.

CREATE TABLE IF NOT EXISTS organizations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL,
    created_by  TEXT NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_orgs_slug_active
    ON organizations(slug) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_orgs_created_by ON organizations(created_by);

DROP TRIGGER IF EXISTS trg_organizations_updated_at ON organizations;
CREATE TRIGGER trg_organizations_updated_at
    BEFORE UPDATE ON organizations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TABLE IF NOT EXISTS org_memberships (
    org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role             TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    -- pending_key_wrap: true when role=admin and the admin key handshake (US-11.5
    -- Step 2) has not yet been completed. Members never have this set.
    pending_key_wrap BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_org_memberships_user ON org_memberships(user_id);
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'org_memberships' AND column_name = 'pending_key_wrap'
    ) THEN
        CREATE INDEX IF NOT EXISTS idx_org_memberships_pending
            ON org_memberships(org_id) WHERE pending_key_wrap = TRUE;
    END IF;
END $$;

-- One row per (org, admin). Stores the org DEK wrapped with that admin's KEK.
-- Members do not have rows here; they rely on server-side org DEK cache during injection.
--
-- The composite FK to org_memberships enforces the invariant that only active members
-- can have a key row. Deleting the membership row (via RemoveOrgMember) cascades to
-- delete the key row automatically — this is the structural guarantee that prevents
-- orphaned key rows when application-layer code is bypassed (e.g. DB admin scripts).
CREATE TABLE IF NOT EXISTS org_key_members (
    org_id      UUID NOT NULL,
    user_id     TEXT NOT NULL,
    wrapped_dek BYTEA NOT NULL,
    key_version INTEGER NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id),
    -- Composite FK to org_memberships: membership is a prerequisite for having a key.
    -- ON DELETE CASCADE means removing the membership row removes the key row.
    FOREIGN KEY (org_id, user_id) REFERENCES org_memberships(org_id, user_id) ON DELETE CASCADE,
    -- Individual FKs for referential integrity to parent tables.
    FOREIGN KEY (org_id) REFERENCES organizations(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

DROP TRIGGER IF EXISTS trg_org_key_members_updated_at ON org_key_members;
CREATE TRIGGER trg_org_key_members_updated_at
    BEFORE UPDATE ON org_key_members
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Add org_id to workspaces. Nullable: personal workspaces have org_id = NULL.
ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS org_id UUID REFERENCES organizations(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_workspaces_org ON workspaces(org_id) WHERE org_id IS NOT NULL;
