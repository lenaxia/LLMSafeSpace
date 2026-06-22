# Epic 54 — Decision Log

**Epic:** Org-Scoped Login — Email Discovery + Subdomain Routing
**Created:** 2026-06-22
**Last Updated:** 2026-06-22

All decisions below were confirmed during the 2026-06-22 scoping conversation. They are the load-bearing constraints on the epic; if any is revisited, the epic replans.

---

## D54-1: No magic links — email is for invitations and notifications only

**Status:** Confirmed (2026-06-22)
**Source:** User direction

**Context:** Email-led login discovery could be implemented two ways: (A) Slack-style — type email → silent redirect to org subdomain → normal auth (password or SSO) runs there; (B) Magic-link style — type email → "we emailed you a sign-in link" → user clicks → authenticated. Magic link is fully enumeration-safe (no public response difference) but adds latency and email deliverability as an auth dependency.

**Decision:** Option A only. No magic link, ever. The email infra shipped by Epic 43 D2 (AWS SES) is used for **invitations** and **notifications** only — never as an authentication factor.

**Consequences:**
- The lookup endpoint returns a `redirectUrl` synchronously; no email is sent.
- Enumeration hardening must be done at the response layer (uniform status/body/timing), not via "we emailed you" theater.
- Passkeys, when built, replace the password path entirely. SSO continues to be the IdP-routed path. Email is never an auth factor.
- If a future requirement demands passwordless login, the answer is passkeys (D-future), not magic links.

**Impact on stories:**
- US-54.1: response is `{ redirectUrl }`, never `{ message: "check your email" }`.
- No dependency on Epic 49 (Email Foundation) for the core flow.

---

## D54-2: Single-redirect response shape, never a list — enumeration-safe by construction

**Status:** Confirmed (2026-06-22)
**Source:** User direction + codebase precedent

**Context:** The lookup endpoint has to answer "which org does this email belong to?" Two response shapes were considered: (A) `{ orgs: [...] }` — a list the frontend picks from (the "org picker" UX); (B) `{ redirectUrl: "..." }` — a single redirect target the frontend blindly follows.

**Decision:** Option B. Always. The response is a single `redirectUrl`, never a list. Found and not-found branches return identical status (200), identical body shape, and identical timing (within ±2ms).

**Rationale:**
- **Customer list leak:** Option A renders org names to unauthenticated users. On a multi-tenant platform with sensitive customers (health, finance, stealth startups), that's a non-starter. Even on a casual platform, it's a competitive-intel gift.
- **Enumeration oracle:** Option A's response shape differs by existence (empty list vs. populated list), making user-existence enumeration trivial. Option B's response is uniform; only timing differs, and we pad that.
- **Forward-compatible with multi-org:** When 1→N orgs per user lands, the resolver picks one (default / last-used), but the response shape doesn't change. Frontend, rate limiter, and any caching layer stay untouched.

**Enumeration hardening — required controls (US-54.1 acceptance criteria):**

| Control | Implementation |
|---|---|
| Status code | Always 200. Never 404, never 500. DB errors fall through to the not-found branch. |
| Body shape | Always `{ redirectUrl: string }`. The string differs (real subdomain vs. root-with-query-param) but the JSON shape is identical. |
| Timing | Not-found branch sleeps to match the p99 of the found branch (~5ms baseline). Gated by `auth.lookupTimingPad` config flag so tests can disable. |
| Rate limit | Per-IP and per-email. Recommended: 10/min/IP, 5/hour/email. |
| DB error handling | Follows `password_reset.go:119` precedent exactly — return the not-found branch, never surface the error. Test at `password_reset_test.go:425` ("must return 202 (not 500) to avoid enumeration") is the model. |

**Not-found redirect target (open question — default decided, alternative noted):**

- **Default:** `redirectUrl = "https://<root>/?lookup=not_found"`. Frontend renders "We couldn't find an account for that email" with a "try a different email" + "create an account" CTA.
- **Alternative considered:** redirect to a generic "check your email" page (magic-link theater without sending email). Rejected — feels deceptive and offers no real benefit over the honest not-found page.

**Impact on stories:**
- US-54.1 acceptance criteria enumerate all four branches (found/1-org, found/0-orgs, not-found, db-error) and require uniform response shape.
- US-54.2 frontend renders the not-found page at `?lookup=not_found`, never tries to enumerate further.

---

## D54-3: Keep 1 user → ≤1 org invariant; multi-org later only changes the resolver, not the API

**Status:** Confirmed (2026-06-22)
**Source:** User direction ("We may add multi-org support in the future, but will not do so immediately.")

**Context:** The current schema (`org_memberships`, migration 000029) allows multi-org at the DB level — `PRIMARY KEY (org_id, user_id)`, plain index on `user_id`, no unique constraint. The 1:1 invariant is enforced at the application layer (`orgs.go:125-145` for create, `:405-418` for add-member). Dropping 1:1 is a one-line code change + a new `users.default_org_id` column.

**Decision:** Keep 1:1 for now. The `POST /auth/lookup` contract returns a single `redirectUrl` — under 1:1, the resolver is trivially `ListOrgsForUser(user.ID)[0]` (always ≤1 row). When multi-org lands:

1. **API contract unchanged.** Still `{ redirectUrl: string }`. The resolver picks one org via `ORDER BY last_used_at DESC LIMIT 1` or `users.default_org_id`.
2. **Frontend unchanged.** Root discovery page still submits email, still follows the single redirect.
3. **Schema change** (future epic, not this one):
   ```sql
   ALTER TABLE users ADD COLUMN default_org_id UUID REFERENCES organizations(id);
   ALTER TABLE users ADD COLUMN last_used_org_id UUID REFERENCES organizations(id);
   ALTER TABLE users ADD COLUMN last_used_org_at TIMESTAMPTZ;
   -- Drop the app-layer 1:1 check in orgs.go:125-145 and :405-418.
   ```
4. **Resolver change** (future epic):
   ```go
   // Today (1:1):
   orgIDs := orgs.ListOrgsForUser(ctx, user.ID)
   if len(orgIDs) == 0 { return notFoundBranch() }
   return redirectFor(orgIDs[0])

   // Future (multi-org):
   if user.DefaultOrgID != nil { return redirectFor(*user.DefaultOrgID) }
   if user.LastUsedOrgID != nil && time.Since(user.LastUsedOrgAt) < 30*24*time.Hour {
       return redirectFor(*user.LastUsedOrgID)
   }
   orgIDs := orgs.ListOrgsForUser(ctx, user.ID)
   if len(orgIDs) == 0 { return notFoundBranch() }
   return redirectFor(orgIDs[0])  // or surface a "pick an org" UI on the subdomain — but that's a subdomain-scoped decision, not a public-surface leak
   ```

**Why this matters for the contract:** The whole point of D54-2 is that the public surface never reveals org membership. A future "user is in 5 orgs, pick one" UI must live on the **subdomain** (authenticated context), never on the **root** (unauthenticated). The single-redirect contract enforces that boundary at the API layer.

**Impact on stories:**
- US-54.1 resolver today: `ListOrgsForUser` → first (only) row → redirect. Documented as "under 1:1 invariant" so a future reader knows the simplification is intentional.
- No migration in this epic. Multi-org migration is the future epic's responsibility.

---

## D54-4: Spike first — wildcard subdomain routing viability gates the epic

**Status:** Confirmed (2026-06-22)
**Source:** Conversation — "the subdomain routing is the unknown"

**Context:** The epic's UX depends on `acme.app.example.com` routing cleanly to the frontend service, with a TLS cert that covers it and a session cookie that survives the root→subdomain redirect. Whether the operator's cluster handles this is genuinely unknown — the chart today supports only explicit `additionalHosts`, not wildcards.

**Decision:** Time-boxed 1-day spike (S54-0) before any epic work starts. The spike proves:
1. Wildcard DNS resolves.
2. cert-manager issues a `*.<base>` cert.
3. Ingress controller routes `acme.<base>` to frontend.
4. Session cookie with `Domain=.<base>` survives the redirect.
5. NetworkPolicies don't block subdomain traffic.

**Outcome criteria:**
- All 5 green → epic proceeds as scoped.
- Any red → replan. The fallback is the org picker (#2 from the discussion): a frontend-only modal listing orgs from `/auth/sso/domains` (already shipped). It leaks the customer list, which is why we preferred subdomains — but if infra blocks subdomains, the picker is the defensible fallback. Replan cost: ~2 days frontend-only.

**Time-box:** 1 day. If the spike runs over, the answer is "no" — don't let it become an open-ended infra project. Escalate and replan.

**Why this is a spike and not just story 1 of the epic:** The lookup endpoint (US-54.1) is low-risk and could start in parallel, but its value proposition is gated on the routing working. If the spike fails, the endpoint is still shippable (operators can distribute direct SSO start URLs manually as a degraded mode) but the epic's UX story collapses. Sequencing the spike first avoids spending 5 days on a UX that can't ship.

---

## D54-5: `orgSubdomainRouting.enabled=false` by default — opt-in for operators with wildcard DNS

**Status:** Confirmed (2026-06-22)
**Source:** Convention — backward compatibility

**Context:** The Helm chart today supports single-host deploys (`frontend.ingress.host: "safespace.example.com"`). Many operators (homelab, single-tenant) don't have wildcard DNS and don't need org-scoped login. Forcing subdomain routing on would break their deploy.

**Decision:** The new `auth.orgSubdomainRouting` block defaults to `enabled: false`. The new `frontend.ingress.wildcard` block auto-sets `enabled: true` when `orgSubdomainRouting.enabled`, but only renders if the operator explicitly opts in.

**Consequences:**
- `helm upgrade` with no value changes is a no-op — no new resources rendered.
- Operators who want org-scoped login must:
  1. Configure wildcard DNS (`*.app.example.com` → ingress LB).
  2. Set `auth.orgSubdomainRouting.enabled=true`, `baseDomain="app.example.com"`, `cookieDomain=".app.example.com"`.
  3. Configure `wildcardCert.issuerRef` (or supply a pre-issued TLS secret via `tlsSecret`).
- The `POST /auth/lookup` endpoint works regardless of the chart setting — it always returns a `redirectUrl`. If subdomain routing isn't enabled, the URL falls back to the root login with a query param (degraded but functional). This lets the backend ship without coordinating with operator infra changes.

**Impact on stories:**
- US-54.3 (chart): all new resources gated on `orgSubdomainRouting.enabled`. Default-off.
- US-54.1 (endpoint): reads `auth.orgSubdomainRouting.baseDomain` from config; if empty, falls back to root-redirect-with-query-param behavior. Documented in the resolver.

---

## Summary — what this epic is and is not

**Is:**
- A Slack-style email → silent redirect → org-scoped login flow.
- Enumeration-safe by construction (uniform response, timing-padded, rate-limited).
- Backward compatible (default-off, single-host deploys unaffected).
- Forward-compatible with multi-org (resolver change only, API contract stable).
- A 1-day spike + ~4 days of stories, conditional on spike success.

**Is not:**
- Magic links (D54-1).
- An org picker (rejected for customer-list leak; fallback only if spike fails).
- Multi-org membership (deferred — D54-3).
- Passkeys (deferred — future epic, consumes this epic's contract).
- A change to SSO, invitations, or any Epic 43 code path.
