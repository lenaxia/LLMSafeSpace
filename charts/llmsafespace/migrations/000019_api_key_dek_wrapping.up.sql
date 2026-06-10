-- Epic 10 US-10.13 Part 2: API Key DEK Wrapping
--
-- Adds columns for wrapped DEK storage on api_keys so API key sessions
-- can decrypt secrets. When decrypt_access=true, the user's DEK is
-- wrapped with a KEK derived from the raw API key at creation time.
-- key_ciphertext stores the raw API key encrypted under the server
-- master key, enabling DEK re-wrap on password/DEK rotation without
-- requiring the user to re-create their API keys.

BEGIN;

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS decrypt_access  BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS kek_salt        BYTEA,
    ADD COLUMN IF NOT EXISTS wrapped_dek     BYTEA,
    ADD COLUMN IF NOT EXISTS dek_synced      BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS key_ciphertext  BYTEA;

COMMIT;
