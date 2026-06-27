-- Add model_output_limits JSONB column to provider_credentials.
-- Stores a map of model_id → output_token_limit (max response tokens) set by
-- the credential owner alongside model_context_limits.
--
-- opencode's published JSON Schema (https://opencode.ai/config.json) requires
-- BOTH `context` and `output` keys when the model `limit` object is present
-- (required: ["context", "output"], additionalProperties: false). Without this
-- column, a saved model_context_limits entry produces an invalid limit block
-- in agent-config.json and opencode rejects the config with SchemaError,
-- returning HTTP 500 from every endpoint including POST /session.
--
-- Existing rows default to an empty map. FormatOpenCodeConfig only emits a
-- `limit` block when BOTH limits are non-zero, so rows that have only
-- model_context_limits set will continue to work (the limit block is omitted
-- and opencode falls back to built-in defaults) until the owner sets an
-- output limit too.
ALTER TABLE provider_credentials
    ADD COLUMN IF NOT EXISTS model_output_limits JSONB NOT NULL DEFAULT '{}';
