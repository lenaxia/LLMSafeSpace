-- Epic 10 US-10.13 Part 1: Hash API keys at rest
--
-- Previously api_keys.key stored the raw token (e.g. "lss_deadbeef...").
-- After this migration it stores the hex-encoded SHA-256 of the token.
--
-- Migration strategy:
--   1. Add key_prefix column to preserve the first 8 chars for display.
--   2. Add key_legacy boolean: existing rows have their raw key retained
--      temporarily so callers can rotate. New rows always store the hash.
--   3. Existing rows are marked key_legacy=true. The application will
--      attempt hash comparison first; if it fails it falls back to a
--      plaintext comparison (for legacy rows only) and logs a warning so
--      operators can prompt users to rotate their keys.
--   4. On the next ListAPIKeys call the Key field is masked to show only
--      the prefix; legacy rows are indicated with a warning in the response.
--
-- Long-term: Once all legacy keys have been rotated (observable via the
-- llmsafespace_api_key_legacy_total gauge dropping to 0), run
-- 000016_api_key_drop_legacy.up.sql to remove key_legacy and enforce
-- hash-only storage.

BEGIN;

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS key_prefix  VARCHAR(12) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS key_legacy  BOOLEAN     NOT NULL DEFAULT FALSE;

-- Backfill key_prefix from the first chars of the existing plaintext key.
UPDATE api_keys
    SET key_prefix = LEFT(key, 8),
        key_legacy = TRUE
    WHERE key_prefix = '';

COMMIT;
