BEGIN;

ALTER TABLE api_keys
    DROP COLUMN IF EXISTS decrypt_access,
    DROP COLUMN IF EXISTS kek_salt,
    DROP COLUMN IF EXISTS wrapped_dek,
    DROP COLUMN IF EXISTS dek_synced,
    DROP COLUMN IF EXISTS key_ciphertext;

COMMIT;
