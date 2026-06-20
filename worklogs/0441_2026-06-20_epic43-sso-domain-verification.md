# Worklog 0441 — Epic 43 / D17 Q-S2: DNS verification of claimed SSO domains

**Date:** 2026-06-20
**Epic:** 43 (Organization Management)
**Session:** Implemented DNS verification for per-org OIDC SSO claimed domains (D17 Q-S2), closing the gap where any org admin could claim any email domain and intercept login-page auto-routing.
**Status:** Complete

---

## Objective

The D17 Q-S2 design specified DNS verification of `claimed_domains` to prevent a malicious org admin from claiming `victim.com` and intercepting that org's users on the login page. The design was documented but unimplemented — all claimed domains auto-routed regardless of ownership. This session implemented the verification mechanism: on-demand DNS TXT record check, verified/claimed subset invariant, and login-page gating.

**Operator decisions (locked in before implementation):**
- Existing claimed domains → grandfathered as verified at migration time
- Verification → on-demand (synchronous DNS lookup, not background job)
- Token rotation → org admins can rotate themselves
- No unverify action → verified domains stay verified until removed from claimed

---

## Work Completed

### Schema (migration 000041)

Added two columns to `org_sso_configs`:
- `verified_domains TEXT[] NOT NULL DEFAULT '{}'` — subset of `claimed_domains` that passed DNS verification
- `verification_token TEXT` — per-org random token for the TXT record

Grandfather clause: `UPDATE org_sso_configs SET verified_domains = claimed_domains` preserves existing auto-routing. GIN index on `verified_domains` for the login-page discovery query.

Mirrored to `charts/llmsafespaces/migrations/`.

### Types (`pkg/types/orgs.go`)

Added `VerifiedDomains []string` and `VerificationToken string` to both `OrgSSOConfig` and `OrgSSOConfigResponse`.

### Store (`pg_org_store.go`)

- `scanSSOConfig`: scans 2 new columns (verified_domains via `pq.Array`, verification_token via `sql.NullString`)
- `GetSSOConfig`/`FindSSOConfigByDomain`: SELECT the 2 new columns
- `UpsertSSOConfig`: writes verified_domains; generates a verification token on INSERT if none supplied; ON CONFLICT preserves existing token (rotation is via `RotateVerificationToken`)
- `ListSSODomains`: **load-bearing change** — now selects `verified_domains` instead of `claimed_domains` and filters `WHERE array_length(c.verified_domains, 1) IS NOT NULL`. Unverified domains no longer appear in the login-page discovery endpoint.
- NEW `SetDomainVerified`: atomic `UPDATE ... SET verified_domains = array_append(...)` with WHERE guards ensuring the domain is claimed and not already verified. Idempotent.
- NEW `RotateVerificationToken`: generates a 32-hex-char token via `crypto/rand`, returns it. Errors if no SSO config exists.
- NEW `randomVerificationToken`: helper using `crypto/rand` + `hex.EncodeToString`.

### SSO service (`sso.go`)

- NEW `dnsResolver` interface + `netResolver` production impl (wraps `net.DefaultResolver.LookupTXT`)
- NEW `VerifyDomain(ctx, orgID, domain)`: normalizes domain, loads SSO config, checks domain is claimed, checks token exists, does DNS TXT lookup at `_llmsafespaces-verify.<domain>`, promotes on match. Returns sentinel errors: `ErrDomainNotClaimed`, `ErrNoVerificationToken`, `ErrDNSNotMatching`.
- NEW `RotateToken(ctx, orgID)`: delegates to store
- NEW `SetDNSResolver(r)`: test-injection point for the DNS resolver
- UPDATED `ApplyConfigMutation`: new `existingVerified []string` parameter; computes `intersectDomains(existingVerified, newClaimed)` so verifications are preserved for still-claimed domains and dropped for removed ones (invariant: `verified ⊆ claimed`)
- NEW `intersectDomains` helper

### Handler (`org_sso.go`)

- UPDATED `Put`: extracts `existing.VerifiedDomains` and passes to `ApplyConfigMutation`
- UPDATED `toSSOResponse`: includes `verifiedDomains` + `verificationToken`
- UPDATED `Get` default response: includes empty `verifiedDomains`
- NEW `VerifyDomain`: `POST /orgs/:id/sso/domains/:domain/verify`; maps service errors to HTTP codes (404/400/409/422); audits `sso.domain.verify`
- NEW `RotateToken`: `POST /orgs/:id/sso/verification-token/rotate`; returns new token; audits `sso.token.rotate`
- NEW `respondVerifyError`: error→HTTP mapper

### Router (`router.go`)

Wired 2 new routes behind `OrgAdminGuard`:
- `POST /api/v1/orgs/:id/sso/domains/:domain/verify`
- `POST /api/v1/orgs/:id/sso/verification-token/rotate`

### Frontend

- `api/sso.ts`: added `verifiedDomains`, `verificationToken` to `OrgSSOConfig`; added `verifyDomain` + `rotateToken` methods
- `OrgSSOTab.tsx`: NEW `DomainVerification` component with domain status table (Verified/Unverified + Verify button), DNS TXT record instructions, token generate/rotate button

---

## Key Decisions

- **Grandfather existing domains.** Operator decision — preserves today's behavior for live deployments. Safe because org creation is platform-admin-gated (`design/0031` D1) and current org admins are trusted. No migration-time disruption.

- **On-demand verification (not background job).** The spec said "background job" but modern SSO providers (GitHub, Google) use on-demand. Simpler: no worker, no retry logic, immediate feedback to the admin. The DNS lookup is a single `net.LookupTXT` call (~100ms).

- **One token per org (not per domain).** Simplifies UX — the org admin copies one TXT record value to all their domains. The record NAME differs per domain (`_llmsafespaces-verify.acme.com` vs `_llmsafespaces-verify.acme.io`) but the VALUE is the same.

- **Verified ⊆ claimed invariant enforced in service layer.** `ApplyConfigMutation` computes the intersection before upserting. `SetDomainVerified`'s WHERE clause (`$2 = ANY(claimed_domains)`) is defense-in-depth at the DB level. Two layers because single mechanisms fail.

- **Token rotation allowed (not immutable).** If a token leaks (e.g., DNS config committed to a public repo), the org admin can rotate immediately without platform-admin intervention. Old DNS records stop matching until updated.

---

## Assumptions (Rule 7) and validation

- **A-GRANDFATHER:** grandfathering via `UPDATE org_sso_configs SET verified_domains = claimed_domains`. **VALIDATED** — migration runs atomically; existing auto-routing preserved.
- **A-NEW-DOMAINS:** domains added post-migration start unverified. **VALIDATED** by `TestApplyConfigMutation_NewDomainsStartUnverified`.
- **A-SUBSET-INVARIANT:** `verified ⊆ claimed` enforced at two layers. **VALIDATED** by `TestApplyConfigMutation_PreservesVerifiedForStillClaimedDomains` (service) and `TestSetDomainVerified_NotClaimed_NoOp` (store WHERE clause).
- **A-ROUTE-GATE:** `ListSSODomains` filters on `verified_domains`. **VALIDATED** by updated `TestListSSODomains_Multiple` (mock returns `verified_domains` column) and the SQL change at `pg_org_store.go`.
- **A-DNS-LOOKUP:** Go stdlib `net.LookupTXT` sufficient. **VALIDATED** — interface extracted for testability (`dnsResolver`); production uses `net.DefaultResolver`.
- **A-TOKEN-FORMAT:** 32-hex-char token via `crypto/rand`. **VALIDATED** by `TestRotateVerificationToken_GeneratesDifferentTokens` (consecutive tokens differ).

---

## Blockers

None.

---

## Tests Run

- `go test -race ./api/internal/services/database/...` — **PASS** (all existing SSO store tests updated for new columns + 10 new tests for SetDomainVerified/RotateVerificationToken)
- `go test -race ./api/internal/services/sso/...` — **PASS** (13 new tests: VerifyDomain happy/unhappy paths, RotateToken, ApplyConfigMutation intersection)
- `go test -race ./api/internal/handlers/...` — **PASS** (9 new handler integration tests for verify/rotate/get-with-verified)
- `cd frontend && npx tsc --noEmit` — **PASS**
- `go run ./cmd/repolint` — **PASS** (migration mirrored to chart; worklog numbering clean)

---

## Next Steps

- Open PR, monitor CI + AI review, iterate per the multi-agent workflow.
- Out-of-scope known gaps (documented in README §14, not addressed here):
  - Instance-level / platform-global OIDC (every flow is org-scoped today)
  - SAML / SCIM (deferred per Epic 43 D3)

---

## Files Modified

- `api/migrations/000041_org_sso_domain_verification.up.sql` — new
- `api/migrations/000041_org_sso_domain_verification.down.sql` — new
- `charts/llmsafespaces/migrations/000041_org_sso_domain_verification.{up,down}.sql` — mirrored
- `pkg/types/orgs.go` — added VerifiedDomains + VerificationToken
- `api/internal/services/database/pg_org_store.go` — 2 new methods, updated scan/query/upsert, helper
- `api/internal/services/database/pg_org_store_sso_test.go` — updated existing tests, 10 new tests
- `api/internal/services/sso/sso.go` — dnsResolver, VerifyDomain, RotateToken, ApplyConfigMutation intersection
- `api/internal/services/sso/sso_test.go` — fakeDNSResolver, 13 new tests
- `api/internal/handlers/org_sso.go` — VerifyDomain, RotateToken handlers, updated Put/Get/toSSOResponse
- `api/internal/handlers/org_sso_test.go` — fakeVerifyDNSResolver, 9 new handler tests
- `api/internal/server/router.go` — 2 new routes
- `frontend/src/api/sso.ts` — new types + methods
- `frontend/src/components/org-admin/OrgSSOTab.tsx` — DomainVerification component
- `README-LLM.md` — §14 endpoints + known gaps updated, v1.18
- `worklogs/0441_2026-06-20_epic43-sso-domain-verification.md` — this worklog
