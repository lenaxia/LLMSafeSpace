-- Bug 6 (worklog 0094): rename secret type 'llm-provider' to 'api-key'
-- to match user-facing docs, threat model, and SDK examples. The old
-- name predated the threat model and was a regular source of confusion
-- (every new user got 400 on first secret create using the documented
-- 'api-key' type).
--
-- The user_secrets.type column is a plain VARCHAR(50) with no CHECK
-- constraint, so the migration is a simple UPDATE; no schema change.
UPDATE user_secrets SET type = 'api-key' WHERE type = 'llm-provider';
