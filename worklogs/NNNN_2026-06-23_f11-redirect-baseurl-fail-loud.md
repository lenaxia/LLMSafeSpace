# Worklog: F11 — fail-loud when oidc.redirectBaseUrl unset (close header-trust gap)

**Date:** 2026-06-23
**Session:** Implement the F11 fix chosen after closing PR #256 and merging PR #382
**Status:** Complete

---

## Objective

Close the F11 header-trust gap: when `oidc.redirectBaseUrl` is unset, the SSO
handler must not derive the callback URL from `X-Forwarded-Proto` / `Host`
headers (attacker-influenceable at a misconfigured reverse proxy). Fail loud
instead — make the unsafe default impossible.

Chosen fix shape (vs alternatives): **fail-loud**, scoped to the SSO
redirect-generation paths. Rejected "derive a default from the ingress host"
because it re-introduces silent trust in a different shape. No dev-mode
special-casing — if header trust is genuinely needed for local dev, the
operator sets the value explicitly (keeps the control unconditional).

---

## Work Completed

### Code

- **`api/internal/services/sso/sso.go`**: added sentinel
  `ErrRedirectBaseURLNotSet` alongside the existing SSO errors
  (`ErrSSONotConfigured`, `ErrEmailUnverified`, …).
- **`api/internal/handlers/org_sso.go`**:
  - `resolveCallbackURL` signature changed `string` → `(string, error)`.
    When `RedirectBaseURL()` is empty, returns `("", ErrRedirectBaseURLNotSet)`
    + a Warn log explaining the missing config. Removed the `X-Forwarded-Proto` /
    `Host` derivation entirely.
  - `Start`: handles the error → HTTP 500 with
    `"SSO is not fully configured: set oidc.redirectBaseUrl"`.
  - `Callback`: handles the error → 302 to the frontend with
    `?sso=config_error` (browser is mid-flow; a JSON body is not usable).
  - `errorReason`: maps `ErrRedirectBaseURLNotSet` → `"config_error"`.
  - Rewrote the doc comment on `resolveCallbackURL` to describe fail-loud.

### Tests (TDD: written first, confirmed red, then implemented)

- `TestE2E_SSO_ResolveCallbackURL_FailsWhenRedirectBaseURLUnset` — unset
  returns empty URL + `ErrRedirectBaseURLNotSet`, ignores forwarded headers,
  fires one Warn.
- `TestE2E_SSO_ResolveCallbackURL_NoWarnWhenRedirectBaseURLSet` — set returns
  the canonical URL, no error, no warning.
- `TestE2E_SSO_Start_UnsetRedirectBaseURL_Returns500` — handler returns 500 +
  config hint.
- `TestE2E_SSO_Callback_UnsetRedirectBaseURL_RedirectsToFrontend` — 302 to
  frontend with `sso=config_error`.
- Replaced the prior `TestE2E_SSO_ResolveCallbackURL_WarnsOnForwardedHeaderFallback`
  (which asserted the old derive-and-warn behaviour).

### Docs

- `README-LLM.md`: bumped to v1.20; added version-history entry; config table
  now states `redirectBaseUrl` is **required for SSO**; security-controls table
  updated (IdP-registered URI is now defense-in-depth, not the primary
  mitigation); corrected the stale `org_sso.go:245` file ref to `:319`.

---

## Key Decisions

- **Fail-loud over derived default.** Deriving from ingress host re-creates the
  silent-trust failure mode in a different shape. Fail-loud is the only option
  that makes the unsafe default impossible. Validated against the F11 threat
  model: the callback URL is where the IdP redirects with the auth code, so it
  must be the operator's canonical URL.
- **500 for Start, 302-to-frontend for Callback.** Start is an XHR/redirect
  initiator that can return JSON; a misconfiguration is honestly a 500.
  Callback is a browser mid-flow returning from the IdP — only a redirect to the
  frontend with an error token is usable UX.
- **`config_error` as a new frontend token.** The frontend's `?sso=` handling
  has a default case that surfaces a generic error; `config_error` adds
  traceability without breaking the contract. Not a frontend change.
- **No dev-mode escape hatch.** Keeping the control unconditional avoids a
  "dev mode" concept that doesn't otherwise exist and would weaken the
  guarantee.

---

## Blockers

None.

---

## Tests Run

- `go build ./...` — pass.
- `go vet ./api/internal/handlers/... ./api/internal/services/sso/...` — pass.
- `make fmt-check` — pass.
- `golangci-lint run --new-from-rev=origin/main` (handlers + sso) — 0 issues.
- `go test -race -run "SSO|Sso|ResolveCallback|OIDC" ./api/internal/handlers/` — pass.
- `go test -race ./api/internal/services/sso/...` — pass.
- The 4 new F11 tests pass (red→green TDD cycle verified).

---

## Next Steps

- Frontend (optional, low priority): explicitly handle `?sso=config_error` in
  `LoginPage.tsx` with a message like "Single sign-on is not configured on this
  instance" instead of relying on the generic error fallback. Not blocking —
  the default case already shows an error.

---

## Files Modified

- `api/internal/services/sso/sso.go` — added `ErrRedirectBaseURLNotSet`.
- `api/internal/handlers/org_sso.go` — `resolveCallbackURL` fail-loud;
  `Start`/`Callback`/`errorReason` updated; doc comment rewritten.
- `api/internal/handlers/org_sso_test.go` — 4 F11 tests (1 replaced, 3 new).
- `README-LLM.md` — v1.20 entry, config/security tables, file ref.
- `worklogs/NNNN_2026-06-23_f11-redirect-baseurl-fail-loud.md` — this file.
