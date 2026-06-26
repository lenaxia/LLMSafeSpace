-- 000045: Durable DEK for JWT sessions (Epic 56).
--
-- Closes Invariant 2 (DEK availability matches JWT validity). Today
-- dek:<jti> lives only in Redis. Production Valkey runs without
-- persistence so every Valkey restart drops every cached DEK while
-- JWTs remain valid for up to 30 days (rememberMeDuration=720h).
-- Multiple users have currently-valid JWTs they can no longer use to
-- decrypt their user-DEK content (ssh-keys, env-secrets, user provider
-- creds).
--
-- This migration creates the durable parallel to the existing Redis
-- cache: jwt_sessions(jti, user_id, wrapped_dek, kek_salt, expires_at).
-- The wrapping KEK is derived at issue time from
-- HKDF(matched_signing_key || jti.String(), kek_salt, "...-dek-kek")
-- which requires the JWT to be presented to reconstruct. Stored
-- columns alone are useless: an attacker with full DB access cannot
-- unwrap without the JWT signing key in API process memory.
--
-- Mirrors the existing api_keys.WrappedDEK design (where the KEK is
-- derived from the API key plaintext at use time). The shape is
-- intentional: both auth modes get the same durability guarantee.
--
-- jti is the canonical UUID string that auth.go:426 generates via
-- uuid.New().String() and stores in JWT claims. Type UUID matches.
--
-- expires_at enables the janitor goroutine (Epic 56 DoD) to prune
-- rows past JWT expiration so the table stays bounded.
--
-- ON DELETE CASCADE on user_id keeps the table consistent with user
-- deletion (no orphaned wrapped DEKs that can never be unwrapped).
--
-- Design doc: design/stories/epic-56-durable-dek-session/README.md
--
-- Refs: PR #411 (design merged 2026-06-26), PR #228 (multi-key
-- rotation infrastructure reused).

CREATE TABLE jwt_sessions (
    jti         UUID PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wrapped_dek BYTEA NOT NULL,
    kek_salt    BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_jwt_sessions_user_id    ON jwt_sessions(user_id);
CREATE INDEX idx_jwt_sessions_expires_at ON jwt_sessions(expires_at);
