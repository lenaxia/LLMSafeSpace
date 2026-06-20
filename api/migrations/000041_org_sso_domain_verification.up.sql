-- Epic 43, US-43.10b: DNS verification of claimed SSO domains (D17 Q-S2).
--
-- Adds per-org tracking of which claimed_domains have been DNS-verified and
-- the shared verification token used to prove ownership. Pre-fix all claimed
-- domains auto-routed on the login page regardless of ownership; this gates
-- auto-routing on verification so a malicious org admin cannot intercept
-- another org's users by claiming their email domain.
--
-- GRANDFATHER (operator decision): existing claimed_domains are copied into
-- verified_domains verbatim. This preserves today's behavior for live
-- deployments — no org loses auto-routing. New domains claimed after this
-- migration start unverified and must complete DNS verification before
-- auto-routing.

ALTER TABLE org_sso_configs
    ADD COLUMN IF NOT EXISTS verified_domains TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS verification_token TEXT;

-- Grandfather: every existing claimed domain is treated as verified.
-- Safe because org creation is platform-admin-gated (design/0031 D1) and
-- current org admins are trusted actors.
UPDATE org_sso_configs
   SET verified_domains = claimed_domains
 WHERE verified_domains = '{}';

-- GIN index for the verified-domain lookup the login page now filters on.
CREATE INDEX IF NOT EXISTS idx_org_sso_verified_domains
    ON org_sso_configs USING GIN (verified_domains);
