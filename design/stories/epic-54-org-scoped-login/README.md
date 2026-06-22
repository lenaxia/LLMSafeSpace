# Epic 54: Org-Scoped Login — Email Discovery + Subdomain Routing

**Status:** Planning
**Created:** 2026-06-22
**Depends On:** Epic 43 (Organization Management — org model, SSO, invitations shipped), Epic 49 (Email Foundation — SES/transactional infra, if separate from Epic 43)
**Does NOT depend on:** passkeys (deferred), multi-org membership (deferred — see D54-3)
**Priority:** Medium-High

**Motivation:** The current login UX assumes one of two things about a user: (a) they have a password, or (b) their email's domain matches a DNS-verified org `claimed_domain`. For deployments where users BYO their own email addresses via an external IdP (Authelia, Authentik, Keycloak, etc.) — common for homelab, education, and contractor-heavy orgs — neither assumption holds. Those users get stuck at the login page with no way to reach their org's SSO flow, and support burden scales linearly with org count.

This epic ships a Slack-style login discovery flow: type email → silently routed to your org's subdomain → normal SSO runs. No org list ever rendered (avoids customer list leak), no magic link (D54-1), no enumeration oracle (follows the `password_reset` precedent).

---

## Origin

Conversation 2026-06-22 reviewing `README-LLM.md` §14 (Multi-Tenant OIDC SSO). The current login page (`frontend/src/pages/LoginPage.tsx:42-81`) only renders the "Sign in with {orgName}" button when the typed email's domain matches a verified `claimed_domain`. For BYO-email orgs (where members use `gmail.com`, `proton.me`, personal domains, etc.), that match never fires and the user is left with only the email/password form — which is permanently blocked for SSO-provisioned users (`README-LLM.md:1447`: SSO users get a random unusable bcrypt hash).

The platform is multi-tenant with potentially multiple BYO-email orgs, so per-org direct URLs (`/api/v1/auth/sso/<slug>/start`) don't scale as a UX: phishing-y looking, lost in onboarding, support-per-org.

---

## Current State (as of 2026-06-22, code-verified)

| Area | Status | Detail |
|------|--------|--------|
| Per-org OIDC SSO | ✅ Shipped | Epic 43 US-43.10. PKCE + state-cookie. `services/sso/sso.go`. Documented in `README-LLM.md` §14. |
| `claimed_domains` DNS verification | ✅ Shipped | Migration 000041. `POST /orgs/:id/sso/domains/:domain/verify`. Login-page discovery filters on verified only. |
| Direct SSO start URL | ✅ Shipped | `GET /api/v1/auth/sso/:orgSlug/start` (`org_sso.go`). Bypasses domain match — works for any user with the URL. |
| Login page email→domain match | ✅ Shipped | `LoginPage.tsx:42`: `domains.find((d) => email.endsWith(d.domain))`. Only surfaces button when domain matches. |
| Email-lookup primitive (`GetUserByEmail`) | ✅ Shipped | `database.go:115`, interface `interfaces.go:61`. Used by SSO, auth, register, email-verify, password-reset. |
| Enumeration-safe response pattern | ✅ Shipped | `password_reset.go:119` + test at `password_reset_test.go:425` ("must return 202 (not 500) to avoid enumeration"). Establishes the convention this epic will follow. |
| Invitation system | ✅ Shipped | `InvitationsHandler` (`invitations.go`). Token-based accept flow carries `orgID` — invitation links already serve as first-login onboarding. |
| Single-org invariant (1 user → ≤1 org) | ✅ Enforced (DB + app layer) | DB: `CREATE UNIQUE INDEX idx_org_memberships_single_user ON org_memberships(user_id)` (`migration 000036_single_org_enforcement.up.sql:12-13`). App: pre-check in `orgs.go:125-145` (create) and `:409-420` (add-member) returns a clear 409 before hitting the raw constraint. Dropping 1:1 requires a migration to `DROP INDEX idx_org_memberships_single_user` + app code change + new `users.default_org_id` column (see D54-3). |
| Wildcard subdomain ingress | ❌ Not present | `frontend-ingress.yaml` supports `additionalHosts` (list of explicit hosts) but no wildcard host or wildcard cert. `cert-manager.io/v1` is already installed for the webhook cert (`templates/webhook-cert.yaml`) — reusable for a wildcard Issuer. |
| Cross-subdomain session cookie | ❌ Not configured | `lsp_session` JWT cookie is set without an explicit `Domain=` attribute (`auth.go` cookie path). Subdomain routing requires `Domain=.app.example.com` (or equivalent) so the session survives the redirect. |
| Passkeys / WebAuthn | ❌ Not present | Zero matches for `passkey`, `webauthn`, `WebAuthn` anywhere in the repo. Deferred (see Non-Goals). |

---

## Scope

### Spike S54-0: Wildcard subdomain routing viability (1 day, blocking)

> Ship first. The epic's UX depends on the infra working; if it doesn't, replan.

Prove end-to-end on staging:

1. **DNS:** wildcard `*.app.<staging-domain>` A record → ingress controller LB.
2. **TLS:** cert-manager `Certificate` for `*.app.<staging-domain>` using an existing or new `Issuer` (the chart already has cert-manager wired for the webhook cert — `templates/webhook-cert.yaml`). Validate issuance succeeds and renewal is set up.
3. **Ingress:** a single `Ingress` rule with `host: "*.app.<staging-domain>"` (or per-organization template that the chart renders for each verified org). Confirm ingress-nginx (or the operator's controller) routes `acme.app.<staging-domain>` to the frontend service.
4. **Cookie scoping:** set `Domain=.app.<staging-domain>` on the `lsp_session` cookie in a throwaway branch — confirm a session established on the root domain survives a redirect to a subdomain.
5. **NetworkPolicy:** verify `workspace-network-policy.yaml` and `api-network-policy.yaml` don't block subdomain traffic to the API. (Workspace NetPol is for sandbox pods, not frontend → API; should be a non-issue, but verify.)
6. **Cookie SecurityContext:** confirm SameSite=Lax (current) doesn't break top-level cross-subdomain redirects. SameSite=None + Secure would be required if subdomains are treated as cross-site by some browsers (Safari ITP). Verify in Chrome + Firefox + Safari.

**Outcome:** "yes, ship the epic as scoped" OR "infra gap X blocks; here's the infra story that has to land first."

**Time-boxed to 1 day.** If the spike runs over, escalate — the answer is "no" and we replan toward org picker (#2 from the discussion) instead.

---

### Phase 1 — Backend: Email → org lookup endpoint

**Story US-54.1 — `POST /api/v1/auth/lookup`**

| | |
|---|---|
| **Effort** | ~4h |
| **Depends on** | Spike S54-0 (confirm subdomain routing will work — endpoint shape doesn't change, but the epic's value proposition does) |
| **Blocked by** | None at the API layer |

**Endpoint shape:**

```
POST /api/v1/auth/lookup
Content-Type: application/json

{ "email": "alice@example.com" }
```

**Response (200, always — see enumeration hardening):**

```json
{ "redirectUrl": "https://acme.app.example.com" }
```

**Resolver logic** (server-side, in a new `services/authlookup`):

```
1. Normalize email (lowercase, trim) — same path as GetUserByEmail callers.
2. GetUserByEmail(ctx, email)
     - found → user, err
     - not found → user == nil, err == nil
     - db error → user == nil, err != nil
3. If user != nil:
     a. GetUserOrgID(ctx, user.ID) — `pg_org_store.go:801` returns the single
        org_id via `SELECT org_id FROM org_memberships WHERE user_id = $1`.
        Under 1:1 invariant (DB unique index from migration 000036, D54-3),
        the query returns 0 or 1 row.
     b. If orgID != "": fetch org slug → return { redirectUrl: subdomainFor(slug, base) }
     c. If orgID == "": fall through to "not found" branch (user has no org;
        e.g. personal-only user). Return redirectUrl to the root login with
        ?lookup=not_found so the response shape is uniform.
4. If user == nil (not found OR db error):
     Return the same redirectUrl as 3c: root login with ?lookup=not_found.
     Identical status code, identical body shape (see hardening).
```

**`subdomainFor(slug, base)` helper:**

```go
// subdomainFor constructs the org's subdomain URL. The base domain comes from
// config (auth.orgSubdomainRouting.baseDomain, e.g. "app.example.com").
// If baseDomain is empty (subdomain routing disabled), the helper falls back
// to the direct SSO start URL — which works today regardless of chart config,
// so the lookup endpoint is useful even before an operator enables subdomain
// routing (email → direct SSO start URL, skipping the broken domain-match step
// on the current LoginPage). The user is never told "not found" when they ARE
// found.
func subdomainFor(slug, base string) string {
    if base == "" {
        return fmt.Sprintf("/api/v1/auth/sso/%s/start", slug)
    }
    return fmt.Sprintf("https://%s.%s", slug, strings.TrimPrefix(base, "."))
}
```

**Enumeration hardening** (mandatory — matches `password_reset.go:119` precedent exactly: uniform response, no timing pad):

| Control | Implementation |
|---|---|
| Uniform status code | Always 200 OK. Never 404, never 500 (DB errors return the not-found redirect). |
| Uniform body shape | Always `{ redirectUrl: string }`. Never `{ error: ... }`. The string differs (real subdomain vs. root-with-`?lookup=not_found`) but the JSON shape is identical. |
| Rate limit | Per-IP and per-email. Recommended: 10 lookups/min/IP, 5 lookups/hour/email. Use existing rate-limit middleware. |
| No timing pad (matches precedent) | `password_reset.go:119` does NOT sleep — it returns 202 uniformly. This endpoint does the same: no `time.Sleep`, no config flag. **The branches do asymmetric DB work** (not-found returns after `GetUserByEmail`; found adds `GetUserOrgID` + slug fetch) — mirroring password_reset's own asymmetry. The decision to skip a pad rests on the added lookups being indexed and fast relative to network jitter; this is an empirical claim that **must be measured at implementation time** (see acceptance criterion below). If measurement shows the asymmetry is observable, a pad is a trivial addition. The precedent (shipped, audited) does not pad and has not been an enumeration vector in practice. |

**Acceptance criteria:**

- `POST /auth/lookup {email}` returns 200 with `{ redirectUrl }` for a known user → URL is `https://<orgSlug>.<base>`.
- Same request for an unknown email returns 200 with `{ redirectUrl: "https://<base>/?lookup=not_found" }` — same status code, same body shape.
- DB error on `GetUserByEmail` returns the not-found branch (never 500), per `password_reset.go:119` precedent.
- `GetUserOrgID` returns `""` (no membership) → not-found branch (covers personal-only users).
- Rate limit triggers after threshold → 429.
- Handler unit test covers all four branches (found/1-org, found/0-orgs, not-found, db-error).
- **Timing measurement (load-bearing for the no-pad rationale):** record p50/p99 of found-branch (with org) vs. not-found-branch over ≥1000 iterations each. Document the result in the implementation worklog. If found-branch p99 exceeds not-found-branch p99 by a margin that would be observable through network jitter (rough threshold: >5ms divergence), add a timing pad at that point and note it in the worklog. The default is no pad (matches `password_reset.go`).
- Integration test: full lookup → 302 → SSO start → callback → session cookie set on subdomain.

**Not-found redirect target:** decided in D54-2 — root login with `?lookup=not_found`. Frontend renders "We couldn't find an account for that email" + "try a different email" + "create an account" CTA.

---

### Phase 2 — Frontend: Root-domain email discovery

**Story US-54.2 — Root-domain login form**

| | |
|---|---|
| **Effort** | ~4h |
| **Depends on** | US-54.1, S54-0 |

**Behavior:**

1. Root domain (`app.example.com`, no subdomain) renders a minimal login page: **email input + "Continue" button.** No password field, no org picker, no SSO button.
2. On submit: `POST /api/v1/auth/lookup { email }`.
3. On 200: `window.location.href = response.redirectUrl` (302 to org subdomain or root login with `?lookup=not_found`).
4. On 429 (rate limit): show "Too many attempts, try again in a minute."
5. On network error: show "Something went wrong, try again."

**Routing:**

- The current `LoginPage.tsx` is reused **on org subdomains** (where the user has been routed). The subdomain-aware page shows password + SSO button as today.
- The **root-domain page** is a new component (`RootDiscoveryPage.tsx`) — email-only. It does not need to know which orgs exist.

**Detection of "root vs. subdomain":**

```typescript
// In router.tsx — choose page based on window.location.hostname
const isRootDomain = window.location.hostname === ROOT_DOMAIN; // from config
// root → <RootDiscoveryPage />, subdomain → <LoginPage />
```

`ROOT_DOMAIN` comes from a new frontend config value (build-time or runtime via `/api/v1/auth/config`).

**Acceptance criteria:**

- Root domain renders `RootDiscoveryPage` (email-only).
- Subdomain renders `LoginPage` (current behavior preserved).
- Submit on root → 302 to subdomain → user lands on the org-scoped `LoginPage` with SSO button visible.
- Submit on root for unknown email → 302 back to root with `?lookup=not_found` → show "We couldn't find an account for that email. Try a different email, or [create an account]."
- Rate-limit error renders cleanly.
- E2E test: fresh browser session, root domain, type email → redirected → SSO button present → click → IdP → callback → session cookie set → redirected into app.

---

### Phase 3 — Infra: Wildcard subdomain chart support

**Story US-54.3 — Helm chart wildcard ingress + cert**

| | |
|---|---|
| **Effort** | ~6h |
| **Depends on** | S54-0 |

**Helm values (new):**

```yaml
# values.yaml
auth:
  orgSubdomainRouting:
    enabled: false              # opt-in; default off until operators have wildcard DNS
    baseDomain: ""              # e.g. "app.example.com" — subdomains are <slug>.<baseDomain>
    cookieDomain: ""            # e.g. ".app.example.com" — set on lsp_session cookie
    wildcardCert:
      issuerRef:
        name: ""                # e.g. "letsencrypt-prod"
        kind: "ClusterIssuer"   # or "Issuer"

frontend:
  ingress:
    # existing host + additionalHosts unchanged.
    # when auth.orgSubdomainRouting.enabled, additionally render a wildcard Ingress:
    wildcard:
      enabled: false            # auto-set true if orgSubdomainRouting.enabled
      host: "*.app.example.com" # rendered from auth.orgSubdomainRouting.baseDomain
```

**Templates:**

1. **`templates/frontend-wildcard-ingress.yaml`** (new): renders when `orgSubdomainRouting.enabled`. Single `Ingress` with `host: "*.<baseDomain>"`, routes to frontend service. Reuses `frontend.ingress.className` + annotations.
2. **`templates/frontend-wildcard-cert.yaml`** (new): cert-manager `Certificate` for `*.<baseDomain>` using the configured `issuerRef`. Only rendered if `tls: true` (default).
3. **Modify `templates/configmap-api.yaml`**: emit `auth.orgSubdomainRouting.baseDomain` + `cookieDomain` into API config so `subdomainFor()` (US-54.1) and the cookie setter read it.
4. **Modify `auth.go` cookie setter**: when `cookieDomain` is configured, set `Domain=<cookieDomain>` on the `lsp_session` cookie. When not configured, behavior unchanged (host-only cookie — current default).

**Acceptance criteria:**

- `helm template` renders the wildcard Ingress + Certificate when `orgSubdomainRouting.enabled=true`.
- With `enabled=false` (default), zero new resources rendered — backward compatible.
- Staging deploy with `enabled=true`: `curl https://acme.app.<staging>` returns the frontend.
- TLS cert for `*.<staging>` issues successfully via cert-manager.
- Session cookie set on root domain carries to subdomain (verified via browser devtools — `Domain=.app.<staging>` present on `Set-Cookie`).
- Existing single-host deploys (no subdomain routing) continue to work unchanged.

---

## Future / Non-Goals

| Item | Status | Why deferred |
|------|--------|-------------|
| **Passkeys / WebAuthn** | Future epic | User explicitly deferred ("passkeys can wait"). No existing primitives in the codebase — building it is a real project (new table, handlers, frontend). When prioritized, it replaces the password path entirely and pairs naturally with SSO (SSO users currently have an unusable bcrypt hash — passkey enrollment gives them a non-SSO login option). |
| **Multi-org membership** | Future epic | User: "We may add multi-org support in the future, but will not do so immediately." The `POST /auth/lookup` contract returns a single `redirectUrl` (not a list), so multi-org later only changes the resolver (pick by `last_used_org_id` or `default_org_id`), not the API contract. See D54-3. |
| **Magic-link email login** | Rejected | User: "absolutely no magic link shit." Email-link-as-auth is explicitly out. The transactional email infra (SES, Epic 43 D2) is used only for invitations and notifications, never for auth. |
| **Org picker dropdown** | Rejected | Leaks customer list. The whole point of email-led discovery is to avoid rendering any org list to unauthenticated users. |
| **Email→org membership lookup as the primary discovery** | Subsumed | Replaced by email-led discovery with response masking (this epic). The raw `GetUserByEmail` → `GetUserOrgID` query is the implementation, but the public surface is a single `redirectUrl` — never a list. |
| **Custom IdP per user (not per org)** | Out of scope | IdP is org-scoped today (`org_sso_configs`). A user without an org has no SSO path — they use password (or passkeys, when built). |
| **SAML / SCIM** | Out of scope | Epic 43 D3 — deferred. OIDC covers modern IdPs including Authelia, Authentik, Keycloak. |

---

## Decisions (see [DECISIONS.md](./DECISIONS.md))

- **D54-1:** No magic links. Email is for invitations + notifications only, never auth.
- **D54-2:** `POST /auth/lookup` returns a single `redirectUrl`, never a list. Found and not-found responses match the `password_reset.go:119` precedent: uniform status (200) + uniform body shape, no timing pad (see hardening table in US-54.1).
- **D54-3:** Keep 1 user → ≤1 org invariant (DB-enforced via `UNIQUE INDEX idx_org_memberships_single_user` in migration 000036, plus app-layer pre-check in `orgs.go:125-145, 409-420`). The `redirectUrl` contract is forward-compatible with multi-org: a future epic only changes the resolver (pick `default_org_id` / `last_used_org_id`), not the API surface.
- **D54-4:** Spike first (S54-0). The epic is gated on wildcard subdomain routing being viable on the operator's cluster. If the spike fails, replan toward org picker (rejected here for customer-list-leak reasons but defensible if infra blocks subdomains).
- **D54-5:** `orgSubdomainRouting.enabled=false` by default. Operators must opt in (requires wildcard DNS + cert). Single-host deploys continue to work unchanged.

---

## Migration Numbering

Current highest migration: `000041` (org SSO domain verification). Next available: `000042`.

**This epic adds no migrations.** The lookup endpoint reads existing `users` + `org_memberships` tables; subdomain routing is config + chart changes only.

When multi-org lands (future epic), the migration will be:
- `DROP INDEX IF EXISTS idx_org_memberships_single_user;` (removes the DB 1:1 constraint from migration 000036)
- `ALTER TABLE users ADD COLUMN default_org_id UUID REFERENCES organizations(id);`
- `ALTER TABLE users ADD COLUMN last_used_org_id UUID REFERENCES organizations(id);`
- Drop the app-layer 1:1 pre-check in `orgs.go:125-145, 409-420`.

---

## File Reference

| Concern | Location |
|---------|----------|
| Email lookup primitive | `api/internal/services/database/database.go:115` (`GetUserByEmail`) |
| User → org query (1:1) | `api/internal/services/database/pg_org_store.go:801` (`GetUserOrgID` — returns `(string, error)`, single org_id via `SELECT org_id FROM org_memberships WHERE user_id = $1`) |
| Enumeration-safe precedent | `api/internal/handlers/password_reset.go:119` (+ test `password_reset_test.go:425`) |
| SSO start (target of redirect) | `api/internal/services/sso/sso.go:419` (`StartLogin`), route `GET /auth/sso/:orgSlug/start` |
| SSO callback (sets session cookie) | `api/internal/services/sso/sso.go` (`HandleCallback`), route `GET /auth/sso/:orgSlug/callback` |
| Session cookie setter | `api/internal/services/auth/auth.go` (cookie path; needs `Domain=` when subdomain routing enabled) |
| Single-org enforcement (app layer pre-check) | `api/internal/handlers/orgs.go:125-145` (create), `:409-420` (add member) |
| DB single-org constraint | `api/migrations/000036_single_org_enforcement.up.sql:12-13` (`UNIQUE INDEX ... ON org_memberships(user_id)`) |
| Login page (subdomain, current) | `frontend/src/pages/LoginPage.tsx` |
| Frontend ingress (current) | `charts/llmsafespaces/templates/frontend-ingress.yaml` |
| Frontend ingress values | `charts/llmsafespaces/values.yaml:794-829` (`frontend.ingress` block, has `additionalHosts`) |
| cert-manager Issuer (existing, for webhook) | `charts/llmsafespaces/templates/webhook-cert.yaml` (reusable pattern for wildcard cert) |
| API config | `api/internal/config/config.go:129-139` (OIDC struct at `:134-139`; new `auth.orgSubdomainRouting` block alongside) |
| Chart configmap | `charts/llmsafespaces/templates/configmap-api.yaml` (emit new config keys) |

---

## Sequencing

```
Day 1:   S54-0 (spike)              ← blocking; proves wildcard routing works
            │
            ▼
Day 2-3: US-54.1 (lookup endpoint)  ← can start in parallel with spike if
            │                          confidence is high; lands after spike passes
            ▼
Day 3-4: US-54.2 (frontend)         ← depends on US-54.1
            │
            ▼
Day 4-5: US-54.3 (chart)            ← depends on S54-0 + US-54.1 (config keys)
```

Total: ~5 working days assuming spike passes clean. If spike fails, replan toward org picker — estimate 2 days (frontend-only, no backend changes since `/auth/sso/domains` already returns the data).

---

## Relationship to Existing Epics

- **Epic 43 (Organization Management):** Builds on the SSO + invitation work shipped there. Does not modify any Epic 43 code paths — only consumes the existing `org_sso_configs`, `org_memberships`, and `GetUserByEmail` primitives.
- **Epic 49 (Email Foundation — SES):** Independent. This epic does not send any email (no magic links per D54-1). Invitation emails continue to flow through whatever Epic 49 / Epic 43 D2 ships.
- **Epic 51 (Tenant Isolation — gVisor + quotas):** Independent. Tenant isolation is about workload sandboxing; this epic is about login routing. No code overlap.
- **Future Passkeys epic:** Will consume the same `POST /auth/lookup` endpoint (passkey challenge is also scoped to a user → org). The redirect URL contract is passkey-compatible.
