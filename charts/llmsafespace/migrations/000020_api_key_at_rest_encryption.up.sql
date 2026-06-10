-- US-10.13 Part 1: API Key At-Rest Encryption
--
-- Adds allowed_cidrs for IP allowlisting and a unique index on
-- the key column (which stores SHA-256 hash since migration 000017)
-- to enforce one-active-key-per-hash and enable fast lookups.

BEGIN;

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS allowed_cidrs TEXT[];

CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_key_active
    ON api_keys(key) WHERE active = true;

COMMIT;
