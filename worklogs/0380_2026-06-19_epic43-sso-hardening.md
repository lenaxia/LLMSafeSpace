# Worklog: Epic 43 — SSO Hardening (F8/F9/F10/F11)

**Date:** 2026-06-19
**Session:** Fix worklog 0372 SSO findings. Part of a 4-PR remediation split by subsystem; this PR owns the OIDC SSO service + handler. Each finding was independently re-verified against source (Rule 11 Phase 2) before fixing.
**Status:** Complete

---

## Objective

- **F8 (MEDIUM)** — OIDC auto-provision/login-match ignored `email_verified`; a permissive IdP letting a user register `victim@example.com` unverified could SSO into the victim's account.
- **F9 (MEDIUM)** — the Azure AD `memberOf` group-claim fallback had zero test coverage.
- **F10 (MEDIUM)** — the fake IdP recorded the PKCE verifier without validating it against the challenge, so a client regression dropping the verifier would not be caught.
- **F11 (LOW)** — the SSO callback URL trusted `X-Forwarded-Proto`/`Host` when `RedirectBaseURL` was unset, with no operator-visible signal.

---

## Work Completed

### F8 — Require `email_verified`
`oidcClaims` now decodes `email_verified`; `HandleCallback` rejects with `ErrEmailUnverified` before any account binding (covers auto-provision AND login-match). Absent claim decodes to `false` (spec-compliant default-deny). `errorReason` maps it to a client-safe `email_unverified` token.

### F9 — Test the `memberOf` fallback
Added `TestCallback_MemberOfGroups_MappedToRole` (Azure-AD-style token using `memberOf` instead of `groups`) + `TestEffectiveGroups_MergesGroupsAndMemberOf`. Production code already worked; this closes the coverage gap.

### F10 — Fake IdP validates PKCE
Fake IdP gained an `/authorize` handler that records the S256 challenge; `/token` validates `codeChallenge(verifier) == challenge` when one was recorded. `TestCallback_PKCEBinding_FullFlow` + `TestCallback_PKCEBinding_WrongVerifierRejected` prove the binding.

### F11 — Warn on forwarded-header fallback
`resolveCallbackURL` logs a warning when it falls back to forwarded headers (so operators see the gap), with an accurate security note. SSO callbacks are infrequent (once per login) so the per-call warning is not noisy.

### Review-driven additions
- `TestOIDCClaims_AbsentEmailVerified_DecodesFalse` — locks the default-deny decoding (F8 regression guard against a future `*bool`/nil-allow change).
- `TestSSOHandler_Callback_UnverifiedEmail_RedirectsWithError` — E2E wiring test for the `email_unverified` redirect reason (mirrors the existing `provisioning_disabled` test).

---

## Key Decisions

1. **`email_verified` is strictly required (absent → reject).** Per OIDC §5.1; org admins configure a compliant IdP. The fake IdP defaults the claim to `true` (well-configured IdP); the rejection test sets it false.
2. **F10 is a test-only fix** — production PKCE was already correct; the gap was the fake IdP not enforcing the binding.
3. **F11 is observability-only** — the header-trust gap remains (mitigated by IdP redirect-URI registration); the warning makes it visible. No startup guard/metric (would be disproportionate for a LOW).

---

## Blockers

None.

---

## Tests Run

- `go test ./api/internal/services/sso/... ./api/internal/handlers/...` — green.
- New: F8 (unverified rejected, verified accepted, absent-claim decodes false, handler redirect wiring), F9 (memberOf mapping + unit), F10 (PKCE full flow + wrong-verifier rejected), F11 (warning on fallback).

---

## Next Steps

Companion PRs: chart+endpoint (#266), auth (#267); the dependent db/handler PR (F6/F7) follows. All independent of this PR.

---

## Files Modified

- `api/internal/services/sso/sso.go` — F8 `EmailVerified` claim + `ErrEmailUnverified`.
- `api/internal/services/sso/sso_test.go` — F8/F9/F10 + fake IdP `/authorize`+PKCE validation + email_verified default.
- `api/internal/handlers/org_sso.go` — F8 `errorReason` mapping + F11 forwarded-header warning.
- `api/internal/handlers/org_sso_idp_helpers_test.go` — email_verified default in `signRS256`.
- `pkg/types/auth.go` — pre-existing gofmt drift fix per Rule 5.
- `charts/llmsafespaces/templates/prometheus-rules.yaml` — pre-existing rename-miss fix: 3 alerts (`SSEBrokerDroppingEvents`, `SafeModeActive`, `StatusUpdateConflicts`) left singular `LLMSafeSpace*` by the module-rename PR, failing `TestMonitoring_PrometheusRule_ContainsAllAlerts` on main. Completed the rename to `LLMSafeSpaces*` (Rule 5). Bundled here because it blocked this PR's full-test-suite CI gate; also applied to companion PRs #266/#267.
