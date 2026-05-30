-- Rollback for 000010_rename_llm_provider_to_api_key
UPDATE user_secrets SET type = 'llm-provider' WHERE type = 'api-key';
