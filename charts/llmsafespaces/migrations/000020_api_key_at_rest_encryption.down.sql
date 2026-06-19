BEGIN;

DROP INDEX IF EXISTS idx_api_keys_key_active;

ALTER TABLE api_keys
    DROP COLUMN IF EXISTS allowed_cidrs;

COMMIT;
