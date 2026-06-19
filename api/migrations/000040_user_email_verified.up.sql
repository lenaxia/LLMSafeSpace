-- 000040: Add email_verified column (Epic 49).
--
-- Used by US-49.5 (password reset requires verified email) and US-49.6
-- (email verification on signup). Backfill existing users to true — they
-- authenticated with their email at signup before this feature existed;
-- locking them out of any gate would be hostile.

ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT false;
UPDATE users SET email_verified = true WHERE email_verified = false;
