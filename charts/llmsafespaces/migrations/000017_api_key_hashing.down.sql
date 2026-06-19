BEGIN;

ALTER TABLE api_keys
    DROP COLUMN IF EXISTS key_prefix,
    DROP COLUMN IF EXISTS key_legacy;

COMMIT;
