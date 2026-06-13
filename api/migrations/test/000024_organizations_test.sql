-- api/migrations/test/000024_organizations_test.sql
--
-- Migration-safety test for 000024_organizations.
-- Run against a PostgreSQL instance that has all up-migrations applied.
-- Each block asserts a structural property of the schema.
-- Any failure raises an EXCEPTION which exits with a non-zero code.

DO $$
DECLARE
    col_count INTEGER;
    idx_count INTEGER;
    org_id    UUID;
    user_id   TEXT := 'test-org-migration-user';
    org_id2   UUID;
BEGIN
    -- -------------------------------------------------------------------------
    -- Precondition: insert a test user
    -- -------------------------------------------------------------------------
    INSERT INTO users (id, username, email, password_hash)
    VALUES (user_id, 'org-mig-test', 'org-mig@test.example', 'x')
    ON CONFLICT DO NOTHING;

    -- -------------------------------------------------------------------------
    -- 1. organizations table exists with expected columns
    -- -------------------------------------------------------------------------
    SELECT COUNT(*) INTO col_count
    FROM information_schema.columns
    WHERE table_name = 'organizations'
      AND column_name IN ('id','name','slug','created_by','created_at','updated_at','deleted_at');
    IF col_count <> 7 THEN
        RAISE EXCEPTION 'organizations table missing columns (expected 7, got %)', col_count;
    END IF;

    -- -------------------------------------------------------------------------
    -- 2. org_memberships table exists with expected columns
    -- -------------------------------------------------------------------------
    SELECT COUNT(*) INTO col_count
    FROM information_schema.columns
    WHERE table_name = 'org_memberships'
      AND column_name IN ('org_id','user_id','role','pending_key_wrap','created_at');
    IF col_count <> 5 THEN
        RAISE EXCEPTION 'org_memberships table missing columns (expected 5, got %)', col_count;
    END IF;

    -- -------------------------------------------------------------------------
    -- 3. org_key_members table exists with expected columns
    -- -------------------------------------------------------------------------
    SELECT COUNT(*) INTO col_count
    FROM information_schema.columns
    WHERE table_name = 'org_key_members'
      AND column_name IN ('org_id','user_id','wrapped_dek','key_version','created_at','updated_at');
    IF col_count <> 6 THEN
        RAISE EXCEPTION 'org_key_members table missing columns (expected 6, got %)', col_count;
    END IF;

    -- -------------------------------------------------------------------------
    -- 4. workspaces.org_id column exists and is nullable
    -- -------------------------------------------------------------------------
    SELECT COUNT(*) INTO col_count
    FROM information_schema.columns
    WHERE table_name = 'workspaces'
      AND column_name = 'org_id'
      AND is_nullable = 'YES';
    IF col_count <> 1 THEN
        RAISE EXCEPTION 'workspaces.org_id column missing or not nullable';
    END IF;

    -- -------------------------------------------------------------------------
    -- 5. Insert workspace with NULL org_id succeeds
    -- -------------------------------------------------------------------------
    INSERT INTO workspaces (id, name, user_id)
    VALUES (gen_random_uuid(), 'org-mig-test-ws', user_id);
    -- (cleanup happens at end)

    -- -------------------------------------------------------------------------
    -- 6. Partial unique index: two active orgs with the same slug is rejected
    -- -------------------------------------------------------------------------
    INSERT INTO organizations (id, name, slug, created_by)
    VALUES (gen_random_uuid(), 'Test Org 1', 'test-org-slug-unique', user_id)
    RETURNING id INTO org_id;

    BEGIN
        INSERT INTO organizations (id, name, slug, created_by)
        VALUES (gen_random_uuid(), 'Test Org 2', 'test-org-slug-unique', user_id);
        RAISE EXCEPTION 'Expected unique violation for duplicate active slug but got none';
    EXCEPTION WHEN unique_violation THEN
        -- Expected
        NULL;
    END;

    -- -------------------------------------------------------------------------
    -- 7. Soft-deleted org allows slug reuse
    -- -------------------------------------------------------------------------
    UPDATE organizations SET deleted_at = now() WHERE id = org_id;
    INSERT INTO organizations (id, name, slug, created_by)
    VALUES (gen_random_uuid(), 'Test Org 2', 'test-org-slug-unique', user_id)
    RETURNING id INTO org_id2;

    -- -------------------------------------------------------------------------
    -- 8. FK: insert org_membership with non-existent org_id returns FK violation
    -- -------------------------------------------------------------------------
    BEGIN
        INSERT INTO org_memberships (org_id, user_id, role)
        VALUES (gen_random_uuid(), user_id, 'admin');
        RAISE EXCEPTION 'Expected FK violation for non-existent org_id but got none';
    EXCEPTION WHEN foreign_key_violation THEN
        NULL;
    END;

    -- -------------------------------------------------------------------------
    -- 9. Composite FK cascade: deleting org_memberships row cascades to org_key_members
    -- -------------------------------------------------------------------------
    INSERT INTO org_memberships (org_id, user_id, role, pending_key_wrap)
    VALUES (org_id2, user_id, 'admin', false);

    INSERT INTO org_key_members (org_id, user_id, wrapped_dek, key_version)
    VALUES (org_id2, user_id, E'\\x00', 1);

    -- Delete membership: should cascade to key member row
    DELETE FROM org_memberships WHERE org_id = org_id2 AND user_id = user_id;

    SELECT COUNT(*) INTO col_count
    FROM org_key_members WHERE org_id = org_id2 AND user_id = user_id;
    IF col_count <> 0 THEN
        RAISE EXCEPTION 'org_key_members row should have cascaded on membership delete but still exists';
    END IF;

    -- -------------------------------------------------------------------------
    -- 10. org_key_members insert fails if no matching org_memberships row
    -- -------------------------------------------------------------------------
    BEGIN
        INSERT INTO org_key_members (org_id, user_id, wrapped_dek, key_version)
        VALUES (org_id2, user_id, E'\\x00', 1);
        RAISE EXCEPTION 'Expected FK violation for org_key_members without org_memberships row but got none';
    EXCEPTION WHEN foreign_key_violation THEN
        NULL;
    END;

    -- -------------------------------------------------------------------------
    -- Cleanup
    -- -------------------------------------------------------------------------
    DELETE FROM workspaces WHERE user_id = user_id;
    DELETE FROM organizations WHERE created_by = user_id;
    DELETE FROM users WHERE id = user_id;

    RAISE NOTICE '000024_organizations migration tests: ALL PASSED';
END;
$$;
