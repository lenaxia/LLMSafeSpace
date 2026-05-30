-- Rollback for 000010_rename_llm_provider_to_api_key
--
-- WARNING: this rollback is DESTRUCTIVE in production. After 000010 has
-- run, new clients on the upgraded image will create rows with
-- type='api-key' that were never previously 'llm-provider'. This
-- rollback will rename ALL such rows, breaking the new clients that
-- continue to write 'api-key'. Only run this rollback if you are
-- simultaneously rolling back the API image to a pre-000010 build.
--
-- For a safe one-way rollback, prefer manually identifying which rows
-- existed before the up migration (e.g. via created_at < <deploy ts>).
UPDATE user_secrets SET type = 'llm-provider' WHERE type = 'api-key';

