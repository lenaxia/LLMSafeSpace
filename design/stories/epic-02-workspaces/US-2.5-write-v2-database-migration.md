# US-2.5: Write V2 Database Migration

**Epic:** 2 - Workspaces
**Priority:** High
**Depends on:** US-1.1

## User Story

As a developer, I want the database schema to match V2 needs, so that the API can store workspace metadata.

## Acceptance Criteria

- [ ] `workspaces` table created with correct columns
- [ ] `sandboxes` table updated with workspace_id foreign key
- [ ] No CRD-owned fields in PostgreSQL (phase, pvc_name, pod_ip, lastActivityAt)
- [ ] V1 tables that are no longer needed documented but not dropped (backward compat)
- [ ] Existing migration issues fixed (missing columns in users, sandbox_labels table)

## Technical Details

**New file:** `api/migrations/000002_workspaces.up.sql`

**From design §15:**

```sql
CREATE TABLE workspaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    namespace VARCHAR(255) NOT NULL,
    runtime VARCHAR(255),
    security_level VARCHAR(50) DEFAULT 'standard',
    storage_size VARCHAR(50) DEFAULT '5Gi',
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    deleted_at TIMESTAMP
);

CREATE INDEX idx_workspaces_user_id ON workspaces(user_id);

ALTER TABLE sandboxes ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id);
```

**Also fix `000001_initial_schema.up.sql`** (currently empty) to contain the full V2 schema, or fix `001_initial_schema.sql` issues:
- Add `active BOOLEAN DEFAULT TRUE` and `role VARCHAR(50) DEFAULT 'user'` to `users`
- Add `name`, `status`, `updated_at` columns to `sandboxes`
- Add `sandbox_labels` table
- Remove `warm_pools`, `execution_history`, `file_operations`, `package_installations` tables (V2 doesn't need them)

## Design Reference

Section 5.5a (State Management), Section 15 (Migration Guide)

## Effort

Medium (2-3 hours)
