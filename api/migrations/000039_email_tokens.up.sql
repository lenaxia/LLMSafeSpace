-- 000039: Email tokens for password reset and email verification (Epic 49).
--
-- Single table parameterised by kind. Both token types have identical shape
-- and lifecycle (generate → store hash → verify on use → consume single-use).
-- Splitting into two tables would duplicate columns for no benefit.

CREATE TABLE IF NOT EXISTS email_tokens (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('password_reset', 'email_verify')),
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_email_tokens_user_kind ON email_tokens(user_id, kind);
