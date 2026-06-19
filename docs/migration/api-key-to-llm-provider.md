# Migrating from api-key to llm-provider / env-secret

The `api-key` secret type is deprecated and will be removed on **December 19, 2026**.

## Why migrate?

`api-key` secrets are materialized as `API_KEY_<NAME>` environment variables and require a process restart to update (though Epic 44 made restarts session-aware). The newer secret types offer better integration:

- **`llm-provider`** — for LLM API credentials (Anthropic, OpenAI, etc.). Hot-reloadable (no restart). Structured provider config with model selection.
- **`env-secret`** — for non-LLM API keys (GitHub, Stripe, etc.). Requires restart, but Epic 44's session-aware restart defers until sessions are idle.

## How to migrate

### For LLM APIs (e.g. Anthropic, OpenAI)
1. Create a new `llm-provider` secret with your API key and provider config.
2. Bind it to your workspace.
3. Remove the old `api-key` secret.

### For other APIs (e.g. GitHub tokens)
1. Create a new `env-secret` with the same variable name.
2. Bind it to your workspace.
3. Remove the old `api-key` secret.

## Existing api-key secrets

Existing `api-key` secrets continue to work until the sunset date. After sunset, new `api-key` secrets cannot be created, but existing ones remain functional.
