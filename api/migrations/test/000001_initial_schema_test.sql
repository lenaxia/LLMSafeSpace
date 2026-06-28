-- api/migrations/test/000001_initial_schema_test.sql
--
-- Migration-safety test for 000001_initial_schema after the pre-launch
-- migration collapse (Epic 55).
--
-- Run against a PostgreSQL instance with the up migration applied. Each
-- block asserts a structural property of the post-collapse schema. Any
-- failure raises an EXCEPTION which exits with a non-zero code.
--
-- Coverage:
--   1. provider_credentials has kind + slug, NO legacy `provider` column.
--   2. provider_credentials kind CHECK constraint enforces the SDK enum.
--   3. provider_credentials slug CHECK constraint enforces the slug regex.
--   4. provider_credentials UNIQUE constraint is on (owner_type, owner_id, slug).
--   5. slug CHECK rejects invalid shapes (space, slash, uppercase, leading hyphen,
--      trailing hyphen, empty).
--   6. slug CHECK accepts valid 1–64 char slugs.
--   7. kind CHECK rejects unknown values.
--   8. UNIQUE blocks duplicate (owner, slug) pairs.

DO $$
DECLARE
    col_count       INTEGER;
    test_cred_id    UUID;
BEGIN
    -- -------------------------------------------------------------------------
    -- 1. kind column exists, TEXT NOT NULL
    -- -------------------------------------------------------------------------
    SELECT COUNT(*) INTO col_count
    FROM information_schema.columns
    WHERE table_name = 'provider_credentials'
      AND column_name = 'kind'
      AND is_nullable = 'NO'
      AND data_type = 'text';
    IF col_count <> 1 THEN
        RAISE EXCEPTION 'kind column missing/wrong-shape (got %)', col_count;
    END IF;

    -- -------------------------------------------------------------------------
    -- 2. slug column exists, TEXT NOT NULL
    -- -------------------------------------------------------------------------
    SELECT COUNT(*) INTO col_count
    FROM information_schema.columns
    WHERE table_name = 'provider_credentials'
      AND column_name = 'slug'
      AND is_nullable = 'NO'
      AND data_type = 'text';
    IF col_count <> 1 THEN
        RAISE EXCEPTION 'slug column missing/wrong-shape (got %)', col_count;
    END IF;

    -- -------------------------------------------------------------------------
    -- 3. legacy `provider` column is GONE
    -- -------------------------------------------------------------------------
    SELECT COUNT(*) INTO col_count
    FROM information_schema.columns
    WHERE table_name = 'provider_credentials'
      AND column_name = 'provider';
    IF col_count <> 0 THEN
        RAISE EXCEPTION 'legacy provider column still exists (got %)', col_count;
    END IF;

    -- -------------------------------------------------------------------------
    -- 4. kind CHECK constraint present
    -- -------------------------------------------------------------------------
    SELECT COUNT(*) INTO col_count
    FROM information_schema.check_constraints cc
    JOIN information_schema.constraint_column_usage ccu
      ON cc.constraint_name = ccu.constraint_name
    WHERE ccu.table_name = 'provider_credentials'
      AND ccu.column_name = 'kind';
    IF col_count = 0 THEN
        RAISE EXCEPTION 'kind CHECK constraint missing';
    END IF;

    -- -------------------------------------------------------------------------
    -- 5. slug CHECK constraint present
    -- -------------------------------------------------------------------------
    SELECT COUNT(*) INTO col_count
    FROM information_schema.check_constraints cc
    JOIN information_schema.constraint_column_usage ccu
      ON cc.constraint_name = ccu.constraint_name
    WHERE ccu.table_name = 'provider_credentials'
      AND ccu.column_name = 'slug';
    IF col_count = 0 THEN
        RAISE EXCEPTION 'slug CHECK constraint missing';
    END IF;

    -- -------------------------------------------------------------------------
    -- 6. UNIQUE constraint is on (owner_type, owner_id, slug)
    -- -------------------------------------------------------------------------
    SELECT COUNT(*) INTO col_count
    FROM pg_indexes
    WHERE tablename = 'provider_credentials'
      AND indexdef ILIKE '%UNIQUE%'
      AND indexdef ILIKE '%slug%'
      AND indexdef ILIKE '%owner_type%'
      AND indexdef ILIKE '%owner_id%';
    IF col_count <> 1 THEN
        RAISE EXCEPTION 'UNIQUE(owner_type, owner_id, slug) index missing (got %)', col_count;
    END IF;

    -- -------------------------------------------------------------------------
    -- 7. slug CHECK rejects invalid values
    -- -------------------------------------------------------------------------
    BEGIN
        INSERT INTO provider_credentials (owner_type, owner_id, name, kind, slug, ciphertext)
        VALUES ('user', 'test-slug', 'n', 'openai', 'has space', E'\\x00');
        RAISE EXCEPTION 'slug CHECK accepted value with space';
    EXCEPTION WHEN check_violation THEN END;

    BEGIN
        INSERT INTO provider_credentials (owner_type, owner_id, name, kind, slug, ciphertext)
        VALUES ('user', 'test-slug', 'n', 'openai', 'UPPER', E'\\x00');
        RAISE EXCEPTION 'slug CHECK accepted uppercase';
    EXCEPTION WHEN check_violation THEN END;

    BEGIN
        INSERT INTO provider_credentials (owner_type, owner_id, name, kind, slug, ciphertext)
        VALUES ('user', 'test-slug', 'n', 'openai', '-leading', E'\\x00');
        RAISE EXCEPTION 'slug CHECK accepted leading hyphen';
    EXCEPTION WHEN check_violation THEN END;

    BEGIN
        INSERT INTO provider_credentials (owner_type, owner_id, name, kind, slug, ciphertext)
        VALUES ('user', 'test-slug', 'n', 'openai', 'trailing-', E'\\x00');
        RAISE EXCEPTION 'slug CHECK accepted trailing hyphen';
    EXCEPTION WHEN check_violation THEN END;

    BEGIN
        INSERT INTO provider_credentials (owner_type, owner_id, name, kind, slug, ciphertext)
        VALUES ('user', 'test-slug', 'n', 'openai', '', E'\\x00');
        RAISE EXCEPTION 'slug CHECK accepted empty';
    EXCEPTION WHEN check_violation THEN END;

    BEGIN
        INSERT INTO provider_credentials (owner_type, owner_id, name, kind, slug, ciphertext)
        VALUES ('user', 'test-slug', 'n', 'openai', 'has/slash', E'\\x00');
        RAISE EXCEPTION 'slug CHECK accepted slash';
    EXCEPTION WHEN check_violation THEN END;

    -- -------------------------------------------------------------------------
    -- 8. slug CHECK accepts valid values (1 char, multi-word with hyphens, ...)
    -- -------------------------------------------------------------------------
    INSERT INTO provider_credentials (owner_type, owner_id, name, kind, slug, ciphertext)
    VALUES ('user', 'test-slug', 'one-char', 'openai', 'a', E'\\x00')
    RETURNING id INTO test_cred_id;
    DELETE FROM provider_credentials WHERE id = test_cred_id;

    INSERT INTO provider_credentials (owner_type, owner_id, name, kind, slug, ciphertext)
    VALUES ('user', 'test-slug', 'multi-word', 'openai_compatible', 'litellm-prod-us-west', E'\\x00')
    RETURNING id INTO test_cred_id;
    DELETE FROM provider_credentials WHERE id = test_cred_id;

    -- -------------------------------------------------------------------------
    -- 9. kind CHECK rejects unknown values
    -- -------------------------------------------------------------------------
    BEGIN
        INSERT INTO provider_credentials (owner_type, owner_id, name, kind, slug, ciphertext)
        VALUES ('user', 'test-kind', 'n', 'not-real', 'slug-x', E'\\x00');
        RAISE EXCEPTION 'kind CHECK accepted unknown value';
    EXCEPTION WHEN check_violation THEN END;

    -- -------------------------------------------------------------------------
    -- 10. UNIQUE blocks duplicate (owner_type, owner_id, slug)
    -- -------------------------------------------------------------------------
    INSERT INTO provider_credentials (owner_type, owner_id, name, kind, slug, ciphertext)
    VALUES ('user', 'test-uniq', 'first', 'openai', 'mycred', E'\\x00');

    BEGIN
        INSERT INTO provider_credentials (owner_type, owner_id, name, kind, slug, ciphertext)
        VALUES ('user', 'test-uniq', 'second', 'anthropic', 'mycred', E'\\x00');
        RAISE EXCEPTION 'UNIQUE(owner_type, owner_id, slug) did not block duplicate';
    EXCEPTION WHEN unique_violation THEN END;

    -- Cleanup
    DELETE FROM provider_credentials WHERE owner_id IN ('test-slug', 'test-kind', 'test-uniq');

    RAISE NOTICE 'Initial schema (Epic 55) — all assertions passed';
END;
$$;
