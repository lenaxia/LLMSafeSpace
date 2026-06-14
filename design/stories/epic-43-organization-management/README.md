# Epic 43: Organization Management & Multi-Tenant Product

**Status:** Planning
**Created:** 2026-06-14
**Last Updated:** 2026-06-14
**Depends On:** Epic 11 (Organizations — foundation complete), Epic 12 (Usage Metering & Billing — metering infrastructure complete), Epic 30 (Unified Credential Model — complete, PR #39)
**Priority:** High

**Motivation:** Epic 11 built the org crypto primitives, membership tables, and credential injection. Epic 30 delivered the unified credential model (`owner_type='user'|'admin'|'org'`). Epic 12 built the metering infrastructure. But the product layer is missing: any user can create unlimited orgs with no gating, there's no admin portal, no email invitations, no SSO, no policy engine, and no way to charge for the service. This epic turns the technical foundation into a real multi-tenant product.

> **See also:** [`DECISIONS.md`](./DECISIONS.md) — all decisions confirmed (D1–D20).

---

## Current State (as of 2026-06-14, code-verified)

| Area | Status | Detail |
|------|--------|--------|
| Org schema | Done | `organizations`, `org_memberships`, `org_key_members`, `workspaces.org_id` (migration 000029) |
| Org DEK crypto | Done | Per-admin wrapped DEK, domain-separated HKDF, rotation (`pkg/secrets/org_key_service.go`) |
| Org credential CRUD | Done | Create/list/update/delete org credentials, auto-apply rules (`pkg/secrets/org_credential_store.go`) |
| Org credential injection | Done | `decryptBinding` org branch, `SeedWorkspaceCredentials` org seeding (`pkg/secrets/injection.go:149`) |
| Org member management (API) | Done | Add/remove members, role changes, key handshake, TOCTOU-safe (`pg_org_store.go`) |
| Org guard middleware | Done | `OrgMemberGuard`, `OrgAdminGuard` (`api/internal/middleware/org_guard.go`) |
| Unified credential model | Done | Epic 30 complete (PR #39). `provider_credentials` with `owner_type='user'\|'admin'\|'org'`, `CredentialProvisioner` wired at `workspace_service.go:271`, all three decrypt branches in `injection.go:135-170`. Deployed Helm rev 159, live-validated 2026-06-07. |
| Metering infrastructure | Done | Epic 12. `usage_events` (owner_type='org' supported), `usage_limits`, `billing_accounts`, `billing_export_cursor` tables. `metering.Service` with async batch writer, DLQ, quota enforcement. `BillingProvider` interface + `NoopBillingProvider` (`pkg/billing/provider.go`). |
| Frontend: org list + create | Partial | `OrgSettingsTab.tsx` (238 lines): create, list, delete, accept-key. Only `list`/`create`/`delete`/`acceptKey` API methods invoked; member/credential/workspace methods exist in `orgs.ts` but no component calls them. |
| Frontend: member management | Missing | No UI for listing members, inviting, changing roles |
| Frontend: credential management | Missing | No UI for org credentials |
| Frontend: org workspace listing | Missing | No UI |
| Org creation gating | Missing | Any authenticated user can create unlimited orgs |
| Email invitations | Missing | API takes raw user IDs, not email addresses. Zero email infra (no SMTP, no SES, no email service). |
| Org admin portal | Missing | Org management buried in user settings tab — wrong UX. No `/admin/orgs/:slug` route. |
| SSO/Identity provisioning | Missing | No OIDC/SAML/SCIM. Only `oidcEnabled` / `ssoProviders` placeholder flags in `AuthConfig`. |
| Policy engine | Missing | No org-level model/provider restrictions, workspace quotas, egress rules |
| Billing tiers | Partial | Epic 12 has metering + billing schema. `users.plan_id` exists. No Stripe, no plan enforcement, no payment flow. `NoopBillingProvider` only. |
| Audit logging | Partial | `secret_audit_log` (user-scoped). `audit_log` table exists (migration 028) with `domain CHECK ('billing','secrets','admin')` — no `'org'` value, no `org_id` column. Only one emit site (billing DLQ discard). |
| Deprovisioning workflow | Missing | No defined flow for offboarding members, archiving workspaces |

---

## Phase Overview — Decisions, Interfaces, Protocols

All 5 phases at a glance. All decisions confirmed — see [`DECISIONS.md`](./DECISIONS.md) for full rationale (D1–D20).

### Phase 1: Org Product Foundation — launch MVP

| | |
|---|---|
| **Stories** | US-43.1 (gating), US-43.2 (email invitations), US-43.3 (portal shell), US-43.4 (members tab), US-43.5 (credentials tab), US-43.6 (workspaces tab), US-43.6b (PVC export — fast follow) |
| **Key decisions** | ✅ D1: Stripe Customer Portal. ✅ D2: AWS SES. ✅ D6: Members see only their own workspaces; admins see all (modifies Epic 11 `verifyOwner`). ✅ D7: No workspace transfer. ✅ D8: Soft delete + 30-day retention + full PVC export. ✅ D9: Multi-org; SSO creates separate accounts per email. ✅ D10: Custom endpoints in UI. |
| **Breaking change** | `verifyOwner` changes from `IsOrgMember` to `IsOrgAdmin` for org workspace access (D6). Members can no longer access other members' org workspaces — only their own + what admins can see. |
| **New interfaces** | **DB:** `organizations.status`, `organizations.plan_id`, `organizations.subscription_status` (migration 030); `stripe_events` table for webhook idempotency (migration 030); case-insensitive slug index (migration 030); `org_invitations` table with bounce columns (migration 031); `billing_accounts.owner_type` CHECK constraint (migration 032); `provider_credentials.base_url` column (migration 037, D10). **API:** invitation CRUD, Stripe checkout/portal session endpoints, Stripe webhook (HMAC + idempotency — replaces existing stub), SES bounce webhook, workspace PVC export streaming endpoint, internal org-status endpoint for controller. **Go:** `pkg/email/EmailProvider` interface + `SESProvider`; `StripeProvider` with signature verification; `StripeWebhookHandler` with DI (DB, Stripe client, logger). **Frontend:** `/admin/orgs/:slug/*` routes, `OrgAdminLayout`, org switcher in chat sidebar. |
| **Protocols** | Invitation email flow (admin invites → SES email → recipient clicks → register/login → accept → membership). Stripe Checkout redirect (create org pending → checkout → webhook activates). Stripe Customer Portal redirect (manage billing → portal → webhook syncs plan). PVC export (helper pod mounts PVC read-only → tarball → signed download URL). |

### Phase 2: Policy & Control (enforcement only — no UI)

| | |
|---|---|
| **Stories** | US-43.7 (policy schema + API), US-43.8 (enforcement). ~~US-43.9 (policy UI)~~ — **deferred** (D15). |
| **Key decisions** | ✅ D15: Ship 4 policies (`allowed_models`, `allowed_providers`, `max_workspaces_per_member`, `max_active_workspaces_per_member`). No policy UI — admins configure via API. Defer `egress_policy`, `require_mfa`, `allowed_runtimes`, `max_session_duration_hours`. ✅ D16: Org policy intersects platform policy (`final = org ∩ platform`). |
| **New interfaces** | **DB:** `org_policies` table (migration 034). **API:** `GET/PUT /api/v1/orgs/:id/policies` (admin-only, no UI). **Go:** `PolicyService` interface, injected into `CreateWorkspace`, `ListModels`, credential injection. Redis-cached (5-min TTL). |
| **Protocols** | Policy evaluation at workspace creation (reject if quota exceeded). Policy evaluation at model selection (filter disallowed models). Provider restriction at credential injection (skip disallowed providers). |

### Phase 3: Enterprise (OIDC only — SAML/SCIM deferred)

| | |
|---|---|
| **Stories** | US-43.10 (OIDC SSO), US-43.13 (org-scoped audit log). ~~US-43.11 (SAML)~~ and ~~US-43.12 (SCIM)~~ deferred — see [DECISIONS.md D3](./DECISIONS.md). |
| **Key decisions** | ✅ D17: Auto-provision (creates separate account per IdP email — see D9). Domain mapping with DNS verification. Group claim → role mapping. Server KEK for client secret storage. |
| **New interfaces** | **DB:** `org_sso_configs` table (migration 035); `audit_log` — add `'org'` domain value + `org_id` column (migration 036). **API:** `GET/PUT /api/v1/orgs/:id/sso`, `GET /api/v1/orgs/:id/audit`, OIDC callback `GET /auth/oidc/:orgSlug/callback`. **Go:** OIDC provider in auth flow, async audit writer for org events. **Frontend:** SSO tab, Audit tab. |
| **Protocols** | OIDC Authorization Code Flow with PKCE. Email-domain-to-org matching at login screen. Auto-provisioning: OIDC userinfo → create user with IdP email → create membership. Group claim (`groups[]`) → role mapping (`admin`/`member`). |

### Phase 4: Billing Integration (metered billing is critical path)

| | |
|---|---|
| **Stories** | US-43.14 (Stripe integration), US-43.15 (plan tiers / feature gating), US-43.16 (billing UI — portal redirect), US-43.17 (usage-based pricing via Stripe Metered — **critical path**) |
| **Key decisions** | ✅ D1: Stripe Customer Portal. ✅ D12: **Metered billing for both tiers** — individual: low flat ($2–5) + usage; org: per-seat + usage. US-43.17 is in critical path. ✅ D13: Trials configurable, default off (3 days, 3 members, 1 workspace). ✅ D14: 7-day payment grace period via Stripe Smart Retries. |
| **New interfaces** | **DB:** `organizations.plan_id` + `subscription_status` (migration 030). **API:** `POST /api/v1/orgs/:id/billing/checkout`, `POST /api/v1/orgs/:id/billing/portal`, `POST /api/v1/webhooks/stripe`. **Go:** `StripeProvider` implementing `BillingProvider` + `CheckoutProvider` interface. Stripe Metered: Epic 12 `BillingExporter` reports `llm_tokens` + `compute_seconds` to Stripe. **Config:** plan tier → feature map; `instance_settings` for trial config. **Frontend:** Billing tab (plan status + usage summary + "Manage in Stripe" button). |
| **Protocols** | Stripe Checkout (initial subscription: select plan → pay → webhook activates). Stripe Customer Portal (management: upgrade/downgrade/cancel → webhook syncs). Stripe Webhook → `organizations.plan_id` + `subscription_status` sync. Feature gating reads `plan_id` from DB. Stripe Metered: `BillingExporter` → Stripe aggregates → invoices with usage charges. Payment failure → 7-day Smart Retries → suspend if unpaid. |

### Phase 5: Platform Operations

| | |
|---|---|
| **Stories** | US-43.18 (platform admin dashboard), US-43.19 (org suspension + user suspension), US-43.20 (cross-org audit) |
| **Key decisions** | ✅ D18: Reuse `users.role='admin'`. ✅ D19: **Both** org-level and user-level suspension. ✅ D20: Hard operational suspension (kill all pods), data preserved (PVCs retained), storage metering continues, compute/token metering stops. |
| **New interfaces** | **DB:** `users.status` column (migration 033). **Frontend:** `/admin/platform/*` routes (org list, user list, suspend/unsuspend). **API:** `GET /api/v1/admin/orgs`, `POST /api/v1/admin/orgs/:id/suspend`, `POST /api/v1/admin/orgs/:id/unsuspend`, `GET /api/v1/admin/users`, `POST /api/v1/admin/users/:id/suspend`, `POST /api/v1/admin/users/:id/unsuspend`, `GET /api/v1/admin/audit`. **Internal API:** `GET /api/v1/internal/orgs/:orgID/status` (controller queries org status on reconcile). **Controller:** org-status cache (30s TTL), suspend workspaces when org is suspended. |
| **Protocols** | Org suspension: set status → controller queries org status on reconcile → suspends all pods → OrgMemberGuard rejects (403) → PVCs preserved → storage metering continues. User suspension: set `users.status` → auth middleware rejects (403) → user blocked across all contexts. Cross-org audit: query `audit_log` across all orgs (platform admin only). |

---

## Product Vision

### Three User Types

```
Individual Developer
  ├── Signs up directly, personal account
  ├── Free tier or individual subscription
  ├── Personal credentials, personal workspaces
  └── No org involvement

Team Admin (small/mid team)
  ├── Creates org (gated by payment via Stripe Checkout)
  ├── Invites members by email (SES)
  ├── Manages shared org credentials (LLM keys)
  ├── Members auto-get org credentials in their workspaces
  ├── Per-seat or flat org subscription (Stripe Customer Portal)
  └── Uses org admin portal at /admin/orgs/:slug

Enterprise IT Admin
  ├── Org provisioned by platform operator or self-service enterprise signup
  ├── Configures SSO (OIDC) — users auto-provision on first login
  ├── Sets policies: allowed models, workspace quotas, egress rules
  ├── Views audit logs, usage reports, billing
  ├── Manages department/team structure within org (future — flat for now)
  └── Annual contract billing

Platform Operator (you)
  ├── Platform admin dashboard at /admin/platform
  ├── Creates/suspends orgs, manages individual subscribers
  ├── Sets global policies (rate limits, allowed runtimes)
  ├── Monitors fleet health, usage, incidents
  └── Handles abuse, security, billing exceptions
```

### Application Structure

```
/ (public)
  ├── /login, /register (authentication)
  ├── /invitations/:token (invitation accept/decline — public view, auth to accept)

/chat (authenticated user UI)
  ├── Chat interface, workspace management
  ├── Personal settings (credentials, API keys, secrets)
  └── Org switcher in sidebar (if member of orgs)

/admin/orgs/:slug (org admin portal)
  ├── Overview: member count, usage summary, plan status
  ├── Members: invite by email, roles, deprovision
  ├── Credentials: org LLM keys, provider config
  ├── Workspaces: list, quotas, archive
  ├── Policies: allowed models, egress, workspace limits (Phase 2)
  ├── Billing: plan status, "Manage in Stripe" → Customer Portal (Phase 4)
  ├── Audit: org-scoped activity log (Phase 3)
  └── SSO: OIDC config, group-to-role mapping (Phase 3)

/admin/platform (platform operator only — Phase 5)
  ├── Orgs: create, suspend, configure, billing
  ├── Users: individual subscribers, usage, billing
  ├── Platform credentials: free-tier keys
  ├── System: metrics, incidents, global policies
  └── Audit: cross-org activity, security events
```

---

## Scenarios & Workflows

### W1: Org Creation & Gating

**Decision:** Self-service with payment (Stripe Checkout) for Team/Business; platform-admin provisions Enterprise orgs manually with custom contracts. See [DECISIONS.md D1](./DECISIONS.md).

**Flow (self-service):**
```
1. User clicks "Create Organization" (prominent CTA in sidebar, not in settings)
2. Enters org name, slug → POST /api/v1/orgs
3. Backend creates org with status='pending_activation', creates Stripe Customer
4. Returns { org, checkoutUrl }
5. Frontend redirects to Stripe Checkout (plan selection + payment)
6. User completes payment → Stripe redirects to /admin/orgs/:slug?checkout=success
7. Stripe webhook (checkout.session.completed) → backend sets status='active', plan_id
8. Frontend polls GET /orgs/:id until status='active' (max 10s, then show "setting up...")
9. User redirected to org admin portal (full access)
```

**Abandoned checkout:** Org remains `pending_activation`. Cron deletes pending orgs older than 7 days — but first checks Stripe for the checkout session status. If the checkout was completed (payment succeeded but webhook was lost), the org is activated instead of deleted. Only orgs with expired/canceled Stripe checkout sessions are deleted. The founding admin's membership and key are also deleted (cascade).

**Flow (enterprise):**
```
1. Platform admin creates org via /admin/platform with status='active'
2. Billing marked as 'manual' (contract — no Stripe)
3. Platform admin assigns initial org admin (by email invitation or user ID)
4. Org admin receives invitation, configures SSO (Phase 3)
5. Users auto-provision via SSO
```

### W2: Email-Based Invitations

Current API takes raw user IDs. Need email-based invitations with accept/reject flow.

**Decision:** AWS SES for email delivery. See [DECISIONS.md D2](./DECISIONS.md).

**Schema:**
```sql
CREATE TABLE org_invitations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email       TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    invited_by  TEXT NOT NULL REFERENCES users(id),
    token_hash  TEXT NOT NULL UNIQUE,          -- SHA-256 of opaque token; plaintext never stored. Rotated on resend.
    expires_at  TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    accepted_by TEXT REFERENCES users(id),
    declined_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Pending = not accepted AND not declined AND not expired
CREATE INDEX idx_org_invitations_org ON org_invitations(org_id) WHERE accepted_at IS NULL AND declined_at IS NULL;
CREATE INDEX idx_org_invitations_email ON org_invitations(email) WHERE accepted_at IS NULL AND declined_at IS NULL;
```

**Flow:**
```
1. Org admin enters email address(es) in portal → POST /api/v1/orgs/:id/invitations
2. Backend: generate token (32 bytes crypto/rand, base64url), store SHA-256 hash
3. Backend: send invitation email via SES with link https://app/invitations/:token
4. Recipient clicks link → GET /api/v1/invitations/:token (public — shows org name, inviter, role)
5. If not logged in: "Register or log in to join" → redirect to /login?return_to=/invitations/:token
6. If logged in: Accept / Decline buttons
7. Accept: POST /api/v1/invitations/:token/accept
   a. Validate token (hash match, not expired, not accepted)
   b. Create org_memberships row
   c. If role=admin: set pending_key_wrap=true (key handshake via existing accept-key flow)
   d. Mark invitation accepted
8. Redirect to org admin portal (admin) or chat with org switcher (member)
```

**Security:** Token is 32 bytes of crypto/rand → ~256 bits entropy. Stored as SHA-256 hash (not plaintext) so DB compromise doesn't leak valid tokens. Expiry: 7 days. Rate limited: max 50 invitations per org per hour.

### W3: Org Admin Portal

Dedicated UI for org management. Not a settings tab.

**Route:** `/admin/orgs/:slug`

**Tabs:**
1. **Overview** — member count, workspace count, usage this period, plan status
2. **Members** — table of members with role badges, invite button, remove/promote/demote actions; pending invitations section
3. **Credentials** — org LLM credential cards (same pattern as user credentials but org-scoped)
4. **Workspaces** — all org workspaces with owner, phase, usage; archive/delete actions
5. **Policies** — model allowlist, provider restrictions, workspace quota per member, egress policy (Phase 2)
6. **Billing** — current plan, seats used/available, "Manage in Stripe" → Customer Portal redirect (Phase 4)
7. **Audit** — org-scoped audit log (Phase 3)
8. **SSO** — OIDC discovery URL, client credentials, group-to-role mapping, auto-provision toggle (Phase 3)

### W4: SSO & Identity Provisioning (OIDC only — Phase 3)

**Decision:** OIDC only. SAML and SCIM deferred. See [DECISIONS.md D3](./DECISIONS.md).

**OIDC flow:**
```
1. Org admin enters OIDC discovery URL + client ID + client secret in portal
2. Config encrypted at rest (org DEK) — stored in org_sso_configs table
3. Login page detects user's email domain → shows "Sign in with [Org Name]" button
4. User clicks → OIDC Authorization Code Flow with PKCE
5. Callback: /auth/oidc/:orgSlug/callback
6. Map OIDC sub/email to user:
   a. If user exists: log in
   b. If not exists: auto-provision (create user + membership) — configurable
7. Group claims map to org roles (e.g., "llmsafespace-admins" → admin)
```

### W5: Policy Engine (Phase 2)

Org-level controls that restrict what members can do.

**Policy types:**
| Policy | Description | Default |
|--------|-------------|---------|
| `allowed_models` | Model allowlist — members can only use these models | All allowed |
| `allowed_providers` | Provider restrictions — only these providers' credentials work | All allowed |
| `max_workspaces_per_member` | Workspace creation limit per org member | 5 |
| `max_active_workspaces_per_member` | Concurrent active workspace limit | 3 |
| `allowed_runtimes` | Which runtime environments members can use | All allowed |
| `egress_policy` | Network egress mode (none, allowlist, unrestricted) | Platform default |
| `require_mfa` | Members must have MFA enabled to access org workspaces | false |
| `max_session_duration_hours` | Workspace auto-suspend after N hours of inactivity | Platform default |

**Enforcement points:**
- `allowed_models` / `allowed_providers` — filter in `ListModels`, reject in model selection
- `max_workspaces_per_member` — check in `CreateWorkspace` before creating
- `egress_policy` — set on workspace CRD, enforced by controller network policy
- `require_mfa` — check in auth middleware for org-affiliated requests

**Schema:**
```sql
CREATE TABLE org_policies (
    org_id      UUID NOT NULL REFERENCES organizations(id),
    key         TEXT NOT NULL,
    value       JSONB NOT NULL DEFAULT '{}',
    updated_by  TEXT REFERENCES users(id),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, key)
);
```

### W6: Billing Tiers & Payment

**Decision:** Stripe Customer Portal for all payment management. We store `plan_id` + `subscription_status` in DB and gate features locally. We never touch credit card data. See [DECISIONS.md D1](./DECISIONS.md).

> **Pricing model confirmed (D12):** Both tiers use metered billing — individual = low flat fee ($2–5/mo) + usage charges; org = per-seat fee + usage charges. The specific dollar amounts below are placeholders to be finalized. Usage charges are per-token and per-compute-second via Stripe Metered Billing (US-43.17, critical path).

**Individual plans (placeholder prices — to be finalized):**
| Plan | Base | Usage | Notes |
|------|------|-------|-------|
| Free | $0 | $0 | 1 workspace, shared free-tier models, limited messages |
| Pro | ~$3/mo | Per-token + per-compute-second | All models, multiple workspaces |

**Org plans (placeholder prices — to be finalized):**
| Plan | Base | Per-seat | Usage | Notes |
|------|------|----------|-------|-------|
| Team | TBD | TBD | Per-token + per-compute-second | Shared org credentials, workspace quotas |
| Business | TBD | TBD | Same | + SSO (Phase 3), policies (Phase 2), audit log (Phase 3) |
| Enterprise | Custom | Custom | Custom | + Dedicated support, custom policies, SLA |

**What Stripe handles (we don't build):**
- Credit card entry, storage, PCI compliance
- Invoice generation and delivery
- Payment retries / dunning emails
- Plan upgrades, downgrades, cancellations (via Customer Portal)
- Tax calculation
- Receipts

**What we build:**
- Stripe Checkout Session creation (initial subscription)
- Stripe Customer Portal Session creation (management redirect)
- Stripe Webhook handler (sync `plan_id` + `subscription_status` to DB)
- Feature gating logic (read `plan_id` → enforce limits)
- Plan tier definitions (config: tier → features map)
- Seat counting (count active members)

**Payment failure flow:**
```
Stripe webhook: invoice.payment_failed
  → organizations.subscription_status = 'past_due'
  → Org stays active (members retain access)
  → Stripe Smart Retries attempts payment over 7 days (D14)
  → Stripe handles dunning emails automatically
  → Org portal shows "payment issue" banner during grace period
  → If all retries fail: Stripe marks subscription 'unpaid'
    → Stripe webhook: customer.subscription.updated (status=unpaid)
    → organizations.status = 'suspended'
    → All workspace pods terminated (D20); PVCs preserved
    → OrgMemberGuard rejects all access (403)
    → Storage metering continues; compute/token metering stops
  → Payment recovery at any point: invoice.paid → subscription_status='active', banner cleared
```

### W7: Audit Logging (Org-Scoped) — Phase 3

Enterprise compliance requires "who did what, when" within the org.

**Schema change:** Add `'org'` to `audit_log.domain` CHECK constraint + add `org_id` column:
```sql
ALTER TABLE audit_log ALTER COLUMN domain TYPE TEXT;  -- drop CHECK
ALTER TABLE audit_log ADD CONSTRAINT audit_log_domain_chk
    CHECK (domain IN ('billing', 'secrets', 'admin', 'org'));
ALTER TABLE audit_log ADD COLUMN org_id UUID REFERENCES organizations(id);
CREATE INDEX idx_audit_org ON audit_log(org_id, created_at DESC) WHERE org_id IS NOT NULL;
```

**Events to audit:**
- Member joined / left / role changed
- Credential created / updated / deleted
- Policy created / updated
- Workspace created / archived / deleted
- SSO configuration changed
- Admin promoted / demoted
- Key rotation performed
- Billing plan changed (via webhook)

### W8: Deprovisioning & Offboarding

When a member leaves the org:

1. Org admin removes member via portal
2. Membership row deleted, org key member deleted (FK cascade)
3. Member's org workspaces remain (they were created under the org)
4. Member loses access to those workspaces (verifyOwner no longer grants access)
5. Member's personal workspaces are unaffected
6. Org credentials are NOT rotated (removal is sufficient for offboarding; rotation for security incidents)

**Security incident flow:**
1. Admin removes member
2. Admin triggers `POST /orgs/:id/rotate-key` to invalidate any cached DEK access
3. All org credentials re-encrypted with new DEK
4. Remaining admins must re-accept keys (pending_key_wrap set)

---

## Proposed User Stories

### Phase 1: Org Product Foundation (must-have for launch)

| Story | Title | Effort |
|-------|-------|--------|
| US-43.1 | Org creation gating — payment-first via Stripe Checkout; enterprise via platform admin. Includes `verifyOwner` + `ListWorkspaces` visibility change (D6), webhook from scratch with HMAC + idempotency, pending org cleanup cron with Stripe check | 12h |
| US-43.2 | Email-based invitation system (schema, SES integration, API, accept flow, SES bounce handling, concurrent-accept race protection) | 10h |
| US-43.3 | Org admin portal shell — route, layout, overview tab, org switcher | 8h |
| US-43.4 | Members tab — list, invite, remove, role management | 6h |
| US-43.5 | Credentials tab — org credential CRUD UI + custom endpoint support (D10, requires migration 037 + backend changes) | 6h |
| US-43.6 | Workspaces tab — org workspace listing (admins see all; members see own, requires `ListOrgWorkspaces` query change) | 4h |
| US-43.6b | Full PVC data export — streaming endpoint (no intermediate storage), helper pod, signed download URL (D8 fast follow) | 6h |

### Phase 2: Policy & Control (enforcement only — no UI)

| Story | Title | Effort |
|-------|-------|--------|
| US-43.7 | Policy schema + API — org_policies table, CRUD endpoints (no UI) | 4h |
| US-43.8 | Policy enforcement — 4 policies: allowed_models, allowed_providers, workspace quotas | 6h |
| ~~US-43.9~~ | ~~Policy UI~~ — **Deferred** (D15). Admins configure via API. Future story adds UI. |

### Phase 3: Enterprise (OIDC only)

| Story | Title | Effort |
|-------|-------|--------|
| US-43.10 | OIDC SSO — config, login flow, auto-provisioning, domain mapping, group claims (D17) | 16h |
| US-43.13 | Org-scoped audit log — events, API, UI | 8h |
| ~~US-43.11~~ | ~~SAML SSO~~ — **Deferred** (see [DECISIONS.md D3](./DECISIONS.md)) |
| ~~US-43.12~~ | ~~SCIM provisioning~~ — **Deferred** |

### Phase 4: Billing Integration (metered billing is critical path — D12)

| Story | Title | Effort |
|-------|-------|--------|
| US-43.14 | Stripe integration — Checkout, Customer Portal, webhook handler, 7-day grace period (D14) | 8h |
| US-43.15 | Plan tiers — individual ($2–5 flat + usage) + org (per-seat + usage), feature gating, configurable trials (D13) | 8h |
| US-43.16 | Billing UI — plan status, usage summary, "Manage in Stripe" redirect | 2h |
| US-43.17 | Usage-based pricing — Stripe Metered: llm_tokens + compute_seconds via Epic 12 BillingExporter (**critical path** — D12) | 8h |

### Phase 5: Platform Operations

| Story | Title | Effort |
|-------|-------|--------|
| US-43.18 | Platform admin dashboard — org list, user list, suspend/unsuspend, configure | 10h |
| US-43.19 | Org + user suspension — hard operational (kill pods), data preserved, controller-query based suspension, last-admin deadlock prevention (D19, D20) | 10h |
| US-43.20 | Cross-org audit — platform-level audit view | 4h |

---

## Phase 1 Detailed Design

### US-43.1: Org Creation Gating

**Goal:** Restrict org creation to paid users (self-service via Stripe Checkout) or platform admins (enterprise, manual billing).

**Schema (migration 000030):**
```sql
ALTER TABLE organizations
    ADD COLUMN status TEXT NOT NULL DEFAULT 'pending_activation'
        CHECK (status IN ('pending_activation', 'active', 'suspended')),
    ADD COLUMN plan_id TEXT NOT NULL DEFAULT 'free',
    ADD COLUMN subscription_status TEXT NOT NULL DEFAULT 'inactive'
        CHECK (subscription_status IN ('inactive', 'active', 'trialing', 'past_due', 'canceled', 'unpaid'));

CREATE INDEX idx_orgs_status ON organizations(status);
```

> `subscription_status` tracks the Stripe subscription lifecycle separately from `status`. An org can be `status='active'` (members can access) while `subscription_status='past_due'` (payment issue, in grace period). See D14.

**D6 visibility change (breaking — modifies Epic 11):**

`verifyOwner` in `workspace_service.go` changes the org access check:

```go
// Epic 11 (current): org membership grants access to any org workspace
access := meta.UserID == userID || orgStore.IsOrgMember(meta.OrgID, userID)

// D6 (new): only creator or org admin can access a given org workspace
access := meta.UserID == userID || orgStore.IsOrgAdmin(meta.OrgID, userID)
```

`ListWorkspaces` in `database.go:571-584` currently returns BOTH personal and org workspaces via an `OR` clause with a `LEFT JOIN org_memberships`. To implement D6, **remove the OR clause** — the query becomes `WHERE w.user_id = $1 AND w.deleted_at IS NULL`. Users see only their own workspaces (personal + org-attributed that they created). The change affects direct workspace access (open, operate) by non-creator org members and the workspace list itself.

**Go types:**
```go
// pkg/types/types.go — extend Organization
type OrgStatus string
const (
    OrgStatusPendingActivation OrgStatus = "pending_activation"
    OrgStatusActive            OrgStatus = "active"
    OrgStatusSuspended         OrgStatus = "suspended"
)

type OrgPlan string
const (
    PlanFree       OrgPlan = "free"
    PlanTeam       OrgPlan = "team"
    PlanBusiness   OrgPlan = "business"
    PlanEnterprise OrgPlan = "enterprise"
)
```

**API surface:**
| Endpoint | Method | Auth | Purpose |
|----------|--------|------|---------|
| `/api/v1/orgs` | POST | JWT | Modified: creates org `pending_activation`, creates Stripe customer + `billing_accounts` row atomically, returns `{ org, checkoutUrl }`. Platform admin (`users.role='admin'`) creates `active` with `plan_id='enterprise'` — no checkout. |
| `/api/v1/orgs/:id` | GET | JWT + OrgMember | Modified: response includes `status`, `planId`. Frontend polls until `active`. |
| `/api/v1/orgs/:id/billing/checkout` | POST | JWT + OrgAdmin | Creates Stripe Checkout Session for plan upgrade. Returns `{ url }`. |
| `/api/v1/orgs/:id/billing/portal` | POST | JWT + OrgAdmin | Creates Stripe Billing Portal Session. Returns `{ url }`. |
| `/api/v1/webhooks/stripe` | POST | HMAC signature | **New endpoint** (replaces existing stub at `/api/v1/webhooks/billing`). Stripe webhook. No JWT auth. |

> **Webhook handler — built from scratch.** The existing `POST /api/v1/webhooks/billing` handler (`api/internal/handlers/webhook.go`) is a 20-line stub that returns `200 {"status":"ok"}` unconditionally. No signature verification, no body parsing, no DB writes, no event dedup. The new Stripe webhook handler is a complete replacement, not an extension. It requires dependency injection of: Stripe client (for signature verification), DB connection (for state updates), logger, and a `billing_accounts` store (for customer → org lookup).

**Webhook idempotency (new table, migration 000030):**
```sql
CREATE TABLE IF NOT EXISTS stripe_events (
    event_id    TEXT PRIMARY KEY,   -- Stripe event ID (e.g., 'evt_12345')
    event_type  TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```
On each webhook: `INSERT INTO stripe_events (event_id, event_type) VALUES ($1, $2) ON CONFLICT DO NOTHING`. If `rows affected == 0`, the event was already processed — return 200 immediately. This prevents double-processing when Stripe retries delivery (which it does for up to 3 days).

**Webhook handler signature verification:**
```go
// Verify HMAC-SHA256 signature using Stripe webhook signing secret
func (h *StripeWebhookHandler) HandleWebhook(c *gin.Context) {
    payload, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))  // 1MB max
    if err != nil { c.JSON(400, gin.H{"error": "invalid body"}); return }

    signature := c.GetHeader("Stripe-Signature")
    event, err := webhook.ConstructEventWithOptions(payload, signature, h.signingSecret, ...)
    if err != nil { c.JSON(401, gin.H{"error": "invalid signature"}); return }

    // Idempotency check
    var inserted bool
    _ = h.db.QueryRow(`INSERT INTO stripe_events ... ON CONFLICT DO NOTHING RETURNING true`).Scan(&inserted)
    if !inserted { c.JSON(200, gin.H{"status": "duplicate"}); return }

    // Dispatch by event type
    switch event.Type {
    case "checkout.session.completed": ...
    case "invoice.payment_failed": ...
    case "customer.subscription.updated": ...
    case "customer.subscription.deleted": ...
    case "invoice.paid": ...
    }
}
```

**Webhook → org lookup chain:**
```
Stripe event → event.Data.Object.customer (Stripe customer ID)
  → SELECT owner_id FROM billing_accounts WHERE external_customer_id = $1 AND provider = 'stripe'
  → owner_id = org UUID
  → UPDATE organizations SET ... WHERE id = $orgID
```

**Guard updates:**
- `OrgMemberGuard` and `OrgAdminGuard` must additionally check `organizations.status = 'active'`. The `organizations.status` column is added by migration 000030 (it does NOT exist today — `000029_organizations.up.sql` has only `id, name, slug, created_by, created_at, updated_at, deleted_at`). The existing `IsOrgMember` / `IsOrgAdmin` SQL queries already JOIN organizations — extend the WHERE clause to include `AND o.status = 'active'`.

**Stripe provider (new):**
```go
// pkg/billing/stripe_provider.go
type StripeProvider struct {
    client *stripe.Client
    // ...
}

// Extends BillingProvider interface + new methods
func (s *StripeProvider) CreateCheckoutSession(ctx context.Context, customerID, orgID, planID string) (url string, err error)
func (s *StripeProvider) CreatePortalSession(ctx context.Context, customerID string) (url string, err error)
func (s *StripeProvider) ParseWebhook(payload []byte, sig string) (event stripe.Event, err error)
```

**Pending org cleanup cron:**
- Runs every 1h. Selects orgs where `status='pending_activation' AND created_at < now() - interval '7 days'`.
- **Before deleting:** queries Stripe for the checkout session status. If the checkout session was completed (payment succeeded but webhook didn't arrive), activate the org instead of deleting it. Only delete orgs where the Stripe checkout session is expired/canceled/unpaid.
- **On Stripe API failure (timeout, 5xx, network error):** skip the org entirely — do NOT delete. Retry on the next cron cycle. Treating a Stripe API failure as "checkout expired" would delete orgs where the user actually paid. Log the skip at Warn level with the org ID and error.
- The 7-day window matches Stripe's checkout session expiry (default 24h) + webhook retry window (3 days) + margin (2.5 days). The previous 24h window risked deleting orgs where the user paid but the webhook was delayed.
- Deletion cascades to memberships and key members.

**Acceptance criteria:**
- Non-admin user: `POST /orgs` → 201 with `status='pending_activation'` + `checkoutUrl`
- Platform admin: `POST /orgs` → 201 with `status='active'`, no checkout URL
- `OrgMemberGuard` rejects access to pending orgs (403 "org not yet activated")
- `OrgMemberGuard` rejects access to suspended orgs (403 "org suspended")
- D6: Non-creator org member cannot access another member's workspace via `verifyOwner` (403); org admin can access any org workspace
- Stripe webhook `checkout.session.completed` → `status='active'`, `plan_id` from subscription, `subscription_status='active'`
- Stripe webhook `invoice.payment_failed` → `subscription_status='past_due'` (org stays active during grace period)
- Stripe webhook `customer.subscription.updated` (status=unpaid) → `status='suspended'` (after 7-day grace period exhausts retries — D14)
- Stripe webhook `customer.subscription.deleted` → `status='suspended'`
- Stripe webhook `invoice.paid` → `subscription_status='active'` (payment recovered, clear past_due)
- Invalid webhook signature → 401 (no state mutation)
- Duplicate webhook event (Stripe retry) → 200 `{"status":"duplicate"}` (no state mutation — idempotency via `stripe_events` table)
- Pending org older than 7 days with expired Stripe checkout → deleted by cron
- Pending org older than 7 days with completed Stripe checkout → activated by cron (webhook was lost)
- `invoice.paid` on a suspended org → `status='active'`, `subscription_status='active'` (org unsuspends)
- `invoice.paid` on an active org with `subscription_status='past_due'` → `subscription_status='active'` (clears past_due)
- Integration test: create org → simulate Stripe webhook → verify activation
- Integration test: simulate `invoice.payment_failed` → verify `subscription_status='past_due'`, org stays active → simulate `customer.subscription.updated`(unpaid) → verify `status='suspended'` → simulate `invoice.paid` → verify `status='active'`

**Dependencies:** US-43.3 (portal needs `status` to show activation state)

---

### US-43.2: Email-Based Invitation System

**Goal:** Replace raw user-ID member addition with email-based invitations using AWS SES (D2).

**Schema (migration 000031):**

**Email service abstraction:**
```go
// pkg/email/provider.go
type EmailProvider interface {
    Send(ctx context.Context, msg EmailMessage) error
}

type EmailMessage struct {
    To       string
    Subject  string
    HTMLBody string
    TextBody string
}

// pkg/email/ses_provider.go — AWS SES implementation
type SESProvider struct {
    client *ses.Client
    from   string
}

// pkg/email/noop_provider.go — dev/test: logs to console
type NoopProvider struct{}
```

**Config:**
```yaml
email:
  provider: "ses"        # "ses" | "noop"
  ses:
    region: "us-east-1"
    fromAddress: "noreply@safespaces.dev"
    # AWS credentials from IRSA or env (AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY)
```

**API surface:**
| Endpoint | Method | Auth | Purpose |
|----------|--------|------|---------|
| `/api/v1/orgs/:id/invitations` | POST | JWT + OrgAdmin | Create invitation(s). Body: `{ emails: ["alice@example.com", ...], role: "member"\|"admin" }`. Sends SES email to each. Returns created invitations. |
| `/api/v1/orgs/:id/invitations` | GET | JWT + OrgMember | List pending invitations for org. |
| `/api/v1/orgs/:id/invitations/:invID` | DELETE | JWT + OrgAdmin | Revoke pending invitation. |
| `/api/v1/orgs/:id/invitations/:invID/resend` | POST | JWT + OrgAdmin | Resend invitation email. |
| `/api/v1/invitations/:token` | GET | Public | Get invitation details (org name, inviter, role, expires). No auth — token is the credential. |
| `/api/v1/invitations/:token/accept` | POST | JWT | Accept invitation. Creates membership. If admin role → `pending_key_wrap=true`. |
| `/api/v1/invitations/:token/decline` | POST | JWT | Decline invitation. Marks as declined. |

**Token security:**
- Generate: 32 bytes `crypto/rand` → base64url encode → ~43 chars
- Store: SHA-256 hash of token (plaintext never in DB)
- Lookup: hash incoming token → `WHERE token_hash = $1`
- Expiry: 7 days from creation
- Entropy: 256 bits — immune to brute force even if DB is compromised

**Email template (invitation):**
```
Subject: [Org Name] invitation to join on LLMSafeSpace

You've been invited by [Inviter Name] to join [Org Name] as a [member|admin].

Click here to accept: https://app.safespaces.dev/invitations/[token]

This invitation expires in 7 days.
```

**Go service:**
```go
// api/internal/services/email/invitation_service.go
type InvitationService struct {
    db       *sql.DB
    email    email.EmailProvider
    baseURL  string
}

func (s *InvitationService) CreateInvitations(ctx context.Context, orgID, invitedBy string, emails []string, role OrgRole) ([]Invitation, error)
func (s *InvitationService) GetInvitationByToken(ctx context.Context, token string) (*InvitationDetail, error)  // hash lookup
func (s *InvitationService) AcceptInvitation(ctx context.Context, token, userID string) (*OrgMembership, error)
func (s *InvitationService) DeclineInvitation(ctx context.Context, token, userID string) error
func (s *InvitationService) ResendInvitation(ctx context.Context, invID string) error
```

**Accept flow detail:**
```
1. POST /api/v1/invitations/:token/accept (authenticated — userID in context)
2. Hash token → lookup invitation
3. Validate: not expired, not already accepted, not already declined
4. Check: user not already a member of this org
5. BEGIN TRANSACTION:
   a. SELECT ... FROM org_invitations WHERE id = $5 FOR UPDATE  -- lock row to prevent concurrent accept
   b. Re-check: accepted_at IS NULL AND declined_at IS NULL (double-check under lock)
      → if already taken: ROLLBACK, return 409 Conflict
   c. INSERT INTO org_memberships (org_id, user_id, role, pending_key_wrap)
      VALUES ($1, $2, $3, $4)  -- pending_key_wrap = (role == 'admin')
   d. UPDATE org_invitations SET accepted_at = now(), accepted_by = $2 WHERE id = $5
   e. If role == 'admin': return membership with instructions to call accept-key
6. COMMIT
7. Return membership
```

> The `SELECT ... FOR UPDATE` in step 5a prevents the race condition where two users attempt to accept the same invitation token simultaneously. Without it, both pass the "not already accepted" check in step 3, both INSERT memberships (different PKs, both succeed), and both UPDATE the invitation row (last writer wins) — resulting in duplicate memberships from a single invitation.

**SES bounce/complaint handling:**
AWS SES sends bounce/complaint notifications via SNS. Configure:
1. SES → SNS topic (`ses-bounces`, `ses-complaints`)
2. SNS → HTTP endpoint: `POST /api/v1/webhooks/ses` (HMAC signature verified via SNS signing)
3. Handler marks the invitation as bounced: `UPDATE org_invitations SET bounce_type = $1, bounced_at = now() WHERE email = $2`
4. Invitation list in admin UI shows bounce status; bounced invitations cannot be resent
5. Repeated bounces to the same address suppress future sends to that address globally

Schema addition (migration 000031):
```sql
-- Add to org_invitations table:
ALTER TABLE org_invitations ADD COLUMN bounce_type TEXT CHECK (bounce_type IN ('permanent', 'transient', 'complaint'));
ALTER TABLE org_invitations ADD COLUMN bounced_at TIMESTAMPTZ;
```

This protects SES sender reputation — sending to known-bad addresses degrades deliverability for ALL users.

**Acceptance criteria:**
- Admin can invite 1-N emails in a single request
- Each email gets a unique token; SES email sent
- `NoopProvider` logs email to console (dev mode)
- Expired invitation → accept returns 410 Gone
- Already-accepted invitation → accept returns 409 Conflict
- User already a member → accept returns 409 Conflict
- Admin role invitation → membership with `pending_key_wrap=true` (user must then call accept-key)
- Member role invitation → membership, no key wrap needed
- Invitation page (`GET /invitations/:token`) shows org name, inviter, role, expiry — no auth required
- Admin can revoke pending invitations (DELETE)
- Admin can resend invitations (POST resend) — generates new token, invalidates old token, resets expiry, sends new email
- Bounced invitation: SES bounce webhook marks invitation with `bounce_type`; resend to bounced email returns 400
- Concurrent accept of same token: second request returns 409 (FOR UPDATE prevents duplicate membership)
- Rate limited: max 50 invitation emails per org per hour
- Integration test: invite → accept → verify membership in DB → verify invitation marked accepted

**Dependencies:** US-43.3 (portal Members tab uses invitation API), US-43.4

---

### US-43.3: Org Admin Portal Shell

**Goal:** Dedicated org admin portal at `/admin/orgs/:slug` with layout and Overview tab.

**Frontend routes:**
```typescript
// frontend/src/router.tsx — new routes
{ path: "/admin/orgs/:slug", element: <OrgAdminLayout />, loader: resolveOrgBySlug,
  children: [
    { index: true, redirect: "overview" },
    { path: "overview", element: <OrgOverviewTab /> },
    { path: "members", element: <OrgMembersTab /> },
    { path: "credentials", element: <OrgCredentialsTab /> },
    { path: "workspaces", element: <OrgWorkspacesTab /> },
    // Future phases (routes added when those phases ship):
    // { path: "policies", element: <OrgPoliciesTab /> },   // Phase 2 — enforcement only, no UI (D15)
    // { path: "billing", element: <OrgBillingTab /> },     // Phase 4
    // { path: "audit", element: <OrgAuditTab /> },         // Phase 3
    // { path: "sso", element: <OrgSSOTab /> },             // Phase 3
  ]
}
```

**Org slug resolution:**
- `GET /api/v1/orgs/:slug-or-id` — existing `GetOrg` resolves by slug or ID. The `OrgResponse` includes `userRole` — frontend checks `userRole === 'admin'` to allow portal access; members see read-only subset.
- **Slug normalization:** `GetOrgBySlug` in `pg_org_store.go:112-113` is currently case-sensitive (`WHERE slug = $1`). Two fixes needed:
  1. **Enforce lowercase at creation** — `CreateOrg` lowercases the slug before insert and before the uniqueness check. Reject slugs containing uppercase letters in the request validator.
  2. **Case-insensitive query fallback** — change `GetOrgBySlug` to `WHERE LOWER(slug) = LOWER($1) AND deleted_at IS NULL` as a safety net for any pre-existing mixed-case slugs. Add expression index: `CREATE UNIQUE INDEX idx_orgs_slug_lower ON organizations(LOWER(slug)) WHERE deleted_at IS NULL;` (migration 000030).

**Layout component (Phase 1 — admin view):**
```
┌──────────────────────────────────────────────────────┐
│ [Org Name]  [Plan Badge]  [Billing Status]           │
├──────────┬───────────────────────────────────────────┤
│ Overview │                                           │
│ Members  │           [Active Tab Content]            │
│ Creds    │                                           │
│ Workspaces│                                          │
├──────────┴───────────────────────────────────────────┤
│ ← Back to Chat          [User Avatar]                │
└──────────────────────────────────────────────────────┘

(Future phases add: Policies (Phase 2, enforcement-only — no tab until UI ships),
 Billing (Phase 4), Audit (Phase 3), SSO (Phase 3))
```

**Overview tab:**
- Member count (active + pending invitations)
- Workspace count (active + suspended)
- Usage summary (compute hours, token count this billing period — from Epic 12 `usage_events`)
- Plan status (plan name, seats used/available, next billing date, subscription status)
- Pending key wrap alert (if current user is admin with `pending_key_wrap=true`)

**Org switcher in main chat sidebar:**
- Dropdown showing "Personal" + list of orgs user belongs to
- Switching orgs filters the workspace list
- "Create Organization" button at bottom of switcher

**Access guard:**
- Frontend: `OrgAdminLayout` calls `GET /orgs/:slug` on mount; if `userRole !== 'admin'`, redirect non-admin tabs to Overview (members can see Overview + Workspaces; only admins see Members/Credentials/Billing/Policies/SSO)
- Backend: existing `OrgMemberGuard` / `OrgAdminGuard` enforce server-side

**Acceptance criteria:**
- `/admin/orgs/:slug` loads org admin portal with sidebar navigation
- Org slug resolves to org (case-insensitive)
- Non-members redirected to `/chat` with error toast
- Overview tab shows member count, workspace count, plan status
- Org switcher appears in chat sidebar for users with org memberships
- "Create Organization" in switcher triggers create flow
- Member (non-admin) accessing portal: sees Overview + Workspaces only; other tabs hidden
- Admin accessing portal: sees all tabs

**Dependencies:** US-43.1 (status/plan fields for Overview tab)

---

### US-43.4: Members Tab

**Goal:** Full member management UI — list, invite by email, change roles, remove.

**Component: `OrgMembersTab`**

**Members table:**
| Name | Email | Role | Key Status | Joined | Actions |
|------|-------|------|------------|--------|---------|
| Alice | alice@acme.com | Admin | Active | Jun 1 | — (self) |
| Bob | bob@acme.com | Member | — | Jun 5 | Promote · Remove |
| Carol | carol@acme.com | Admin | Pending key | Jun 10 | Complete key · Demote · Remove |

**Pending invitations section:**
| Email | Role | Sent | Expires | Actions |
|-------|------|------|---------|---------|
| dave@acme.com | Member | Jun 12 | Jun 19 | Resend · Revoke |

**Invite modal:**
- Textarea for email addresses (comma or newline separated)
- Role selector (Member / Admin)
- "Send Invitations" button
- Validates email format client-side
- Shows success/error per email

**Actions:**
- **Promote** (member → admin): calls existing `PUT /orgs/:id/members/:userID` with `role: "admin"`. Sets `pending_key_wrap=true`. UI shows "key handshake required" badge.
- **Demote** (admin → member): calls existing `PUT /orgs/:id/members/:userID` with `role: "member"`. Blocked if last admin.
- **Remove**: calls existing `DELETE /orgs/:id/members/:userID`. Confirm modal. Blocked if removing self as last admin.
- **Complete key** (pending admin): inline password prompt → calls existing `POST /orgs/:id/accept-key`.
- **Resend invitation**: calls `POST /orgs/:id/invitations/:invID/resend`.
- **Revoke invitation**: calls `DELETE /orgs/:id/invitations/:invID`.

**APIs used:**
| Existing (Epic 11) | New (US-43.2) |
|---|---|
| `GET /orgs/:id/members` | `POST /orgs/:id/invitations` |
| `PUT /orgs/:id/members/:userID` | `GET /orgs/:id/invitations` |
| `DELETE /orgs/:id/members/:userID` | `DELETE /orgs/:id/invitations/:invID` |
| `POST /orgs/:id/accept-key` | `POST /orgs/:id/invitations/:invID/resend` |

**Acceptance criteria:**
- Members table lists all active members with role badges and key status
- Pending invitations listed separately with expiry countdown
- Invite modal sends to multiple emails; each gets individual token
- Promote sets pending key wrap; UI shows "Complete Key Handshake" CTA
- Demote blocked for last admin (error toast)
- Remove blocked for self-removal as last admin (error toast)
- Resend generates a NEW token and resets expiry (old token invalidated; per US-43.2 token security model, plaintext is never stored so original token cannot be reused)
- Revoke deletes invitation; recipient's link becomes invalid
- Rate limit: if invitation send fails (429), show "too many invitations, try later"

**Dependencies:** US-43.2 (invitation API), US-43.3 (portal shell)

---

### US-43.5: Credentials Tab

**Goal:** Org credential management UI. All backend APIs exist (Epic 11 `org_credentials.go`). Frontend API client methods exist in `orgs.ts` but no component calls them.

**Component: `OrgCredentialsTab`**

**Credential cards:**
```
┌─────────────────────────────────────────────┐
│ 🟢 OpenAI Production              [Edit] [×] │
│ Provider: openai                              │
│ Models: gpt-4o, gpt-4o-mini (allowlist)      │
│ Auto-apply: All org workspaces        [Manage]│
│ Updated: Jun 10, 2026                        │
└─────────────────────────────────────────────┘
```

**Add credential modal:**
- Name (required)
- Provider dropdown (openai, anthropic, google, custom)
- API key (required, password field)
- Base URL (optional — for custom/self-hosted endpoints)
- Model allowlist (optional — multi-select from provider's models)
- Auto-apply toggle (if on, credential auto-applies to all org workspaces)

**Auto-apply management:**
- Per credential, show auto-apply rules
- Add rule: target type (all org workspaces / specific workspace)
- Remove rule

**APIs used (all existing):**
| Endpoint | Purpose |
|---|---|
| `GET /orgs/:id/credentials` | List org credentials |
| `POST /orgs/:id/credentials` | Create credential |
| `PUT /orgs/:id/credentials/:credID` | Update credential |
| `DELETE /orgs/:id/credentials/:credID` | Delete credential |
| `POST /orgs/:id/credentials/:credID/auto-apply` | Create auto-apply rule |
| `GET /orgs/:id/credentials/:credID/auto-apply` | List auto-apply rules |
| `DELETE /orgs/:id/credentials/:credID/auto-apply` | Delete auto-apply rule |

**Custom endpoint support (BYO inference):**
- When provider = "custom", show Base URL field
- `provider_credentials` does NOT currently have a `base_url` column (verified — `api/migrations/000015_unified_credential_model.up.sql:13-25` has no such column). This requires:
  - Migration `000037`: `ALTER TABLE provider_credentials ADD COLUMN base_url TEXT NOT NULL DEFAULT '';`
  - Handler change: accept `baseURL` in create/update requests
  - Store change: persist and read `base_url`
  - Injection change: `FormatOpenCodeConfig` emits `baseURL` into opencode provider config when non-empty
- Confirmed decision: D10 (yes — but requires migration + backend changes, not just UI)

**Acceptance criteria:**
- Credential cards list all org credentials with provider, model allowlist, auto-apply status
- Create credential: name + provider + API key → credential appears in list
- Edit credential: update name, API key, model allowlist
- Delete credential: confirm modal → credential removed from list
- Auto-apply: toggle creates/removes "all org workspaces" rule
- API key is never displayed after creation (security — only show masked "••••••••last4")
- Custom provider: base URL field visible when provider = "custom"

**Dependencies:** US-43.3 (portal shell)

---

### US-43.6: Workspaces Tab

**Goal:** List org workspaces with filtering and management actions. Visibility per D6: admins see all org workspaces; members see only their own.

**Component: `OrgWorkspacesTab`**

**Workspace table (admin view — sees all):**
| Name | Owner | Phase | Runtime | Last Active | Usage (this period) | Actions |
|------|-------|-------|---------|-------------|---------------------|---------|
| API Dev | Alice | Active | python | 2 min ago | 1.2M tokens | Open · Suspend |
| ML Pipeline | Bob | Suspended | python | 3 days ago | 800K tokens | Open · Resume |

**Workspace table (member view — sees only own per D6):**
| Name | Phase | Runtime | Last Active | Usage (this period) | Actions |
|------|-------|---------|-------------|---------------------|---------|
| API Dev | Active | python | 2 min ago | 1.2M tokens | Open · Suspend |

> Members do not see the "Owner" column (all rows are theirs). The owner filter is hidden for members.

**API behavior:**
- `GET /orgs/:id/workspaces` returns all org workspaces for admins; for members, filters to `user_id = caller` (D6). Auth: JWT + OrgMember.
- **Query change required:** `ListOrgWorkspaces` in `pg_org_store.go:508-547` currently has no `user_id` parameter — it returns ALL org workspaces (`WHERE w.org_id = $1 AND w.deleted_at IS NULL`). Add a `userID *string` parameter:
  - Admin request: `userID = nil` → query unchanged (all org workspaces)
  - Member request: `userID = &callerID` → query adds `AND w.user_id = $userID`
- The handler determines role via `IsOrgAdmin(orgID, userID)` and passes nil or the caller's ID accordingly.

**Filters (admin only):**
- By owner (dropdown of org members)
- By phase (active, suspended, all)

**Filters (member):**
- By phase (active, suspended, all) — owner filter hidden

**Actions:**
- **Open**: navigate to `/chat/:workspaceId`. Members can only open their own workspaces (D6 `verifyOwner` blocks access to others'). Admins can open any org workspace.
- **Suspend/Resume**: calls existing workspace suspend/resume API. Admin-only action (OrgAdminGuard). Members see suspend/resume only for their own workspaces (if we choose to allow self-suspend).
- **Export** (future, US-43.6b): trigger full PVC export for this workspace

**Usage column:**
- Reads from Epic 12 `GET /api/v1/usage/workspaces/:id` — aggregate token count for current billing period

**APIs used:**
| Endpoint | Purpose |
|---|---|
| `GET /orgs/:id/workspaces` | List org workspaces (existing, paginated; role-differentiated per D6) |
| `POST /workspaces/:id/suspend` | Suspend workspace (existing) |
| `POST /workspaces/:id/resume` | Resume workspace (existing) |
| `GET /usage/workspaces/:id` | Workspace usage (existing, Epic 12) |

**Acceptance criteria:**
- Admin: table lists all org workspaces with owner, phase, runtime, last active
- Member: table lists only workspaces the member created (D6), no owner column
- Admin: filter by owner narrows list; member: owner filter hidden
- Filter by phase (active/suspended/all) works for both roles
- Open navigates to chat view; member opening another member's workspace → 403 (D6 `verifyOwner`)
- Admin can open any org workspace
- Suspend/Resume works for org admins (OrgAdminGuard)
- Usage column shows token count for current billing period
- Pagination for large workspace counts

**Dependencies:** US-43.3 (portal shell), D6 (visibility change in US-43.1)

---

### US-43.6b: Full PVC Data Export

**Goal:** Allow an org admin or platform admin to export the complete PVC contents of any workspace — for data portability, org offboarding (D8), or GDPR compliance.

**Scope (D8 fast follow):**
- Export includes the **entire PVC** (all files: workspace files, opencode.db, git repos, `.local` config — everything at `/workspace` and `/home/sandbox`)
- Packaged as a tarball (`.tar.gz`)
- Time-limited signed download URL (expires after 24h)

**Technical approach — streaming endpoint (no intermediate storage):**

The API does not have S3 or object storage integration today. Storing a 10GB tarball in API memory causes OOM; storing on ephemeral disk is lost on restart. The solution is a **streaming download endpoint** — no intermediate file is stored anywhere.

```
1. Admin triggers export: POST /api/v1/orgs/:id/workspaces/:wsID/export
   → Returns { jobId, downloadUrl: "/api/v1/exports/{jobId}/download" }
   → The downloadUrl is signed (HMAC + 24h expiry)

2. When the client requests GET /api/v1/exports/{jobId}/download:
   a. API verifies signed URL (HMAC, not expired)
   b. API spawns a helper pod that mounts the PVC read-only
   c. Helper pod runs: tar czf - /workspace /home/sandbox | nc {api-pod-ip} {port}
      (or: helper pod runs tar to stdout, API streams it to the HTTP response writer)
   d. API sets Content-Disposition: attachment; filename="{orgSlug}-{workspaceName}.tar.gz"
   e. Client receives the tarball as a streaming HTTP response
   f. Helper pod is deleted when the stream completes or the client disconnects
```

**Why streaming, not storage:** Without S3, there's no durable place to put the tarball. Streaming avoids intermediate storage entirely. The tradeoff: the client must stay connected for the entire download (no "download later" link). For org offboarding (D8), the admin triggers the export and waits.

**Future S3 integration:** When S3 is available (future epic), the flow changes to: helper pod → tarball → S3 → presigned URL. The client gets a real download link valid for 24h. Until then, streaming is the only option that doesn't require new infrastructure.

**Connection handling:**
- Set `Content-Length` if possible (helper pod can `du -sb /workspace /home/sandbox` first for approximate size)
- Set generous timeouts: 30min for large PVCs
- If client disconnects mid-stream: helper pod is killed, resources cleaned up
- Rate limiting: max 3 concurrent exports per org (tracked in Redis)

**API surface:**
| Endpoint | Method | Auth | Purpose |
|----------|--------|------|---------|
| `/api/v1/orgs/:id/workspaces/:wsID/export` | POST | JWT + OrgAdmin | Create export job, return signed download URL |
| `/api/v1/workspaces/:wsID/export` | POST | JWT + owner | Self-service export (GDPR) |
| `/api/v1/admin/workspaces/:wsID/export` | POST | JWT + Admin | Platform admin export (any workspace) |
| `/api/v1/exports/:jobId/download` | GET | Signed URL (HMAC) | Stream tarball to client |

**Acceptance criteria:**
- Admin triggers export → signed download URL returned immediately → GET on URL streams tarball
- Suspended workspace can be exported (helper pod mounts PVC without requiring workspace to be Active)
- Download URL expires after 24h (HMAC verification fails)
- Large PVC (5GB+) exports via streaming (no OOM — data flows through, not buffered in memory)
- Read-only mount: no modifications to the PVC during export
- Client disconnect mid-stream → helper pod killed, resources cleaned up
- Member exporting their own workspace: works (self-service)
- Member exporting another member's org workspace: 403 (D6)
- Org admin exporting any org workspace: works
- Integration test: create workspace with files → export → stream download tarball → verify contents match

**Dependencies:** None (can be built independently of other Phase 1 stories)

---

## Open Questions

> **All questions resolved (2026-06-14).** Confirmed decisions are D6–D20 in [`DECISIONS.md`](./DECISIONS.md). The detailed context, options, and tradeoffs for each question are retained below for reference. Each question header notes the confirmed decision.

---

### Q1: Should orgs have sub-groups (teams/departments)? — ✅ Resolved: D6 (defer, but members see only own workspaces)

**Context:** A flat member list means every member is either an admin or a member, org-wide. All members see all org workspaces; all admins manage everything. For a 500-person enterprise, this doesn't scale — you'd want "Engineering" team with their own credentials, "Sales" team with limited workspace visibility, department-level admins who manage their team but not the whole org.

**Concrete example:** Acme Corp has 200 engineers across 5 teams. With flat membership, every engineer sees every other engineer's workspaces. The Backend team's LLM key (which has a high spend limit) is injected into the Marketing intern's workspace. The VP of Engineering wants team leads to manage their own teams without global admin power.

**Option A — Yes (sub-groups now):**

Schema changes:
```sql
CREATE TABLE org_groups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL,
    parent_id   UUID REFERENCES org_groups(id) ON DELETE CASCADE,  -- nesting
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(org_id, slug)
);
CREATE TABLE org_group_members (
    group_id    UUID NOT NULL REFERENCES org_groups(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role        TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('lead', 'member')),
    PRIMARY KEY (group_id, user_id)
);
```

Code changes:
- `org_groups` CRUD API + handlers (~8h)
- Group-scoped credential targeting in `credential_auto_apply` (new `target_type='group'`) (~6h)
- Group-scoped workspace visibility in `verifyOwner` (~6h)
- Group admin role in guards (`OrgGroupLeadGuard`) (~4h)
- Nested RBAC inheritance resolution (~8h)
- Frontend: group management UI, group-scoped views (~12h)
- Total: ~40-60h

Risks:
- Nested RBAC creates combinatorial permission resolution complexity (parent group permissions, child group overrides)
- Credential auto-apply rules gain a 4th target type — the priority merge in `SeedWorkspaceCredentials` becomes more complex
- `verifyOwner` gets an additional branch — org membership OR group membership
- Significant frontend work (group tree UI, per-group credential cards, workspace filtering)

**Option B — Defer (flat for now, add later):**

What flat gives you:
- Two roles: admin (manage everything) and member (use org resources)
- All members see all org workspaces
- All admins manage all credentials, members, policies
- Works well for orgs up to ~50 people

What you cannot do:
- Restrict workspace visibility to a subset of members
- Have team-scoped credentials (different LLM keys for different teams)
- Delegate admin power to team leads

**Platform precedent:**
- **GitHub:** Started with flat "teams" under an org. Added nested teams later. Sub-organizations never added.
- **Slack:** Flat channels under a workspace. Enterprise Grid adds "org level" above workspace. No nesting below channel.
- **Vercel:** Flat teams. No sub-groups.
- **Linear:** Flat teams within a workspace. No nesting.
- **Notion:** Flat. No groups at all in early days.

**Effort:** Option A = ~50h. Option B = 0h (current state).

**Revisit trigger:** An enterprise customer with >100 members requests team-scoped credentials or workspace isolation. Until then, no user has asked for this.

**Recommendation:** Defer. Phase 1 is flat membership. Every successful SaaS platform started flat and added sub-groups only when customer demand forced it. Building it speculatively adds 50h of complexity — including nested RBAC, the #1 source of authorization bugs in every system that has it — for a feature no customer has requested.

---

### Q2: Workspace ownership transfer — can a personal workspace be moved to an org? — ✅ Resolved: D7 (no)

**Context:** Currently, workspaces are created under an org from the start (you pass `orgID` at creation time, or leave it null for a personal workspace). The question: can a user take a workspace they already created as a personal workspace and transfer it to the org later?

**Concrete example:** Alice creates a personal workspace, spends two weeks building a prototype with her personal OpenAI key. She wants to hand it off to her team. Without transfer, she creates a new org workspace and manually copies files — losing session history, opencode.db state, and having to reconfigure everything.

**Option A — Yes (build transfer):**

API: `POST /api/v1/workspaces/:id/transfer` — body: `{ orgID: "..." }`

What happens under the hood:
1. `workspace_metadata.org_id` updated from NULL → target org ID
2. `Workspace CRD spec.owner.orgID` updated
3. All existing `workspace_credential_bindings` for this workspace are re-evaluated:
   - User-scoped explicit bindings: **removed** (personal credentials should not follow the workspace into the org)
   - User-scoped auto-apply bindings: **removed**
   - Org-scoped credentials: **seeded** via `SeedWorkspaceCredentials`
4. On next workspace resume, the pod restarts with new credential context
5. Active sessions are terminated (credential context mismatch)
6. Audit log: `workspace_transferred`, actor, from_owner, to_org

Edge cases:
- What if Alice has personal secrets stored in the workspace's secret store? They're encrypted with Alice's user DEK. After transfer, the workspace is under the org — org admins should have access but Alice's secrets are user-encrypted. Options: (a) delete personal secrets on transfer, (b) leave them encrypted (org admins can't read them), (c) re-encrypt with org DEK (requires both keys in cache).
- What if Alice is the only member of the org? Then transfer is functionally identical to keeping it personal until other members join.
- What if the workspace is Active when transferred? Sessions must be killed (credential swap mid-session would produce garbled state).

Effort: ~8-12h (transfer API + credential rebinding + session kill + edge cases + tests)

**Option B — No (create under org from start):**

Current behavior: you pick "Personal" or org name when creating the workspace. If you want existing work in the org, create a new org workspace and copy files (the PVC-mounted `/workspace` makes this a filesystem copy).

**Platform precedent:**
- **GitHub:** Repos can be transferred between owners. It's a well-used feature. But GitHub repos don't have encrypted secrets tied to a user's key.
- **Vercel:** Projects can be moved between teams.
- **Slack:** Channels cannot be moved between workspaces.

**Effort:** Option A = ~12h. Option B = 0h (current state).

**Revisit trigger:** Users complain about the manual copy workflow. Until then, the cleaner model wins.

**Recommendation:** No for now. Transfer introduces a credential-context-change edge case that's particularly nasty in our system because secrets are zero-knowledge encrypted. The user's personal secrets in the workspace become orphaned — they can't be decrypted by org admins, but they're also not removed. "Create under org from the start" avoids this entirely. If users request transfer, the simplest safe implementation would be "transfer = copy files to new org workspace, delete personal workspace" which avoids credential re-encryption entirely.

---

### Q3: What happens to workspaces when an org is deleted? — ✅ Resolved: D8 (soft delete + 30-day retention + full PVC export)

**Context:** Orgs can be deleted (soft delete via `deleted_at`, per Epic 11). When deleted, what happens to the workspaces created under that org? Members? Credentials? This must be decided before Phase 1 because `SoftDeleteOrg` already exists and its behavior needs to match the product decision.

**Concrete example:** Acme Corp decides to leave the platform. Their org has 30 workspaces with months of work — prototypes, documentation, research notes. The org admin clicks "Delete Organization." What happens?

**Option A — Hard delete immediately:**
- All workspaces, PVCs, data destroyed immediately
- `DELETE FROM organizations WHERE id = ...` cascades to memberships, key members, credentials
- Workspaces' PVCs are deleted (orphaned CRDs garbage-collected)
- Destructive, irreversible
- Risk: accidental deletion destroys irreplaceable work
- Effort: 0h (CASCADE does this already if we remove the soft-delete guard)

**Option B — Block hard delete if workspaces exist:**
- `DELETE /orgs/:id` returns 409 if any workspaces exist
- Admin must delete or archive each workspace first, then delete the org
- Safest — admin must consciously destroy each workspace
- Most tedious for large orgs
- Already partially implemented: Epic 11's `SoftDeleteOrg` checks `OrgHasActiveWorkspaces` and blocks deletion if true
- Effort: 0h (Epic 11 already does this for soft delete)

**Option C — Soft delete with retention:**
- Soft delete: `deleted_at` set, members lose access immediately (guards already filter `deleted_at IS NULL`)
- Workspaces preserved (PVCs retained, CRDs kept)
- After N days (30 default): cron hard-deletes the org, workspaces, PVCs
- Platform admin can force-hard-delete sooner or restore within retention window
- Allows recovery from accidental deletion or "we changed our mind"
- Effort: ~4h (retention cron + force-delete API + restore API)

**Combined recommendation: B + C:**

```
DELETE /orgs/:id (soft delete):
  1. Check: any workspaces? → 409 "delete or archive workspaces first"
  2. Set deleted_at = now()
  3. Members lose access (existing guard behavior)
  4. Org appears in platform admin's "deleted orgs" list with 30-day countdown

Platform admin actions on soft-deleted orgs:
  - POST /admin/orgs/:id/restore — undelete (within 30 days)
  - POST /admin/orgs/:id/force-delete — hard delete now (destroys data)

Retention cron (daily):
  - Find orgs WHERE deleted_at < now() - interval '30 days'
  - For each: verify no workspaces (shouldn't have any since B blocked it)
  - Hard delete: organizations row, cascade memberships, key members, credentials
```

**Platform precedent:**
- **GitHub:** Org deletion is blocked if repos exist. You must transfer or delete repos first. (Option B)
- **Slack:** Workspace deletion has a 30-day grace period. (Option C)
- **Vercel:** Team deletion requires removing all projects first. (Option B)

**Effort:** B is 0h (exists). C adds ~4h (retention cron + admin restore/force-delete APIs).

**Revisit trigger:** If the 30-day retention window causes storage cost concerns (hundreds of deleted orgs with preserved workspaces), shorten or remove retention.

**Recommendation:** B + C. This matches what Epic 11 already built (B) and adds a safety net (C). Block deletion when workspaces exist prevents the catastrophic scenario. Soft-delete with 30-day retention gives a recovery window for regret. Platform admin force-delete provides an escape hatch.

---

### Q4: Can a user belong to multiple orgs? — ✅ Resolved: D9 (yes; SSO creates separate accounts per email)

**Context:** Alice works at Acme Corp (her employer) but also consults for Startup Inc. Can she be a member of both orgs on the same LLMSafeSpace account?

**Technical reality:** The schema already supports this. `org_memberships` has a composite PK `(org_id, user_id)` — Alice can have membership rows in multiple orgs. Epic 11's `ListOrgsForUser` returns all orgs a user belongs to. No schema change is needed either way.

**Option A — Yes (multi-org):**

What this means in practice:
- **Org switcher in chat sidebar:** dropdown showing "Personal" + list of orgs. Switching context filters the workspace list.
- **Workspace creation dialog:** user selects "Personal" or an org name when creating a workspace. This already works — `CreateWorkspaceRequest` has an `OrgID` field.
- **Billing isolation:** each org has its own Stripe subscription. Alice's work on Acme Corp's workspaces bills to Acme Corp. Her work on Startup Inc's workspaces bills to Startup Inc. Her personal workspaces bill to her personal plan.
- **Credential isolation:** Acme Corp's org credentials don't leak into Startup Inc's workspaces. The `orgID` on the workspace determines which org credentials are injected.
- **Notification isolation:** Alice sees separate activity indicators per org.

Frontend changes needed:
- Org switcher dropdown in sidebar (~4h)
- Workspace creation dialog: add org selector (already partially exists) (~2h)
- Settings page: show "Organisations" tab with all orgs (already exists) (~0h)

Backend changes needed:
- `ListWorkspaces` currently returns BOTH personal and org workspaces via an OR clause (D6 removes this). With D6 applied, the query already returns only the user's own workspaces. No additional org-filtering needed — the org switcher filters on the frontend side.
- Workspace creation already accepts `orgID` — no change
- Org switcher API: `GET /orgs` already returns user's orgs — no change

**Option B — No (single org):**

What this means:
- User can only be in one org at a time
- `org_memberships` gets a constraint: a user can have at most one row where `role = 'admin'` or `role = 'member'`
- Must leave one org before joining another
- Simpler UX but highly restrictive — most professionals are part of multiple organizations

**Platform precedent:**
- **GitHub:** Yes — one account, many orgs. This is the universal standard.
- **Slack:** Yes — one account, multiple workspaces. You switch between them.
- **Vercel:** Yes — one account, multiple teams.
- **Linear:** Yes — one account, multiple workspaces.
- **Notion:** Yes — one account, multiple workspaces.

Every modern SaaS platform supports multi-org. Users expect it.

**Effort:** Option A = ~6h (mostly frontend switcher). Option B = ~4h (enforcement constraint + leave-before-join logic). But B is the wrong direction.

**Revisit trigger:** Never. Multi-org is the correct model.

**Recommendation:** Yes. The schema already supports it. Every user expects it. The work is a sidebar dropdown + workspace creation org selector. The alternative (single org) would mean Alice needs two accounts — which is the worst possible UX and defeats the purpose of the org model.

---

### Q5: Should orgs be able to register custom/BYO model endpoints? — ✅ Resolved: D10 (yes)

**Context:** An org might run their own LLM inference server — a self-hosted vLLM instance with a fine-tuned model, an on-prem Ollama, an Azure OpenAI deployment with a custom endpoint, or a LiteLLM proxy. Should they be able to register this as a provider so their workspaces can use it?

**Technical reality — requires migration + backend changes (code-verified):**

The backend does NOT currently support custom endpoints:
- `provider_credentials` table has **no** `base_url` column (verified: `api/migrations/000015_unified_credential_model.up.sql:13-25` — no such column exists in any migration)
- The credential injection path (`FormatOpenCodeConfig`) has no logic to emit a base URL into the opencode provider config
- `decryptBinding` handles any provider name — it doesn't validate against a known list (this part IS true)
- opencode itself supports custom OpenAI-compatible endpoints via the provider config's `baseURL` field (this is true — opencode can consume it, but we don't currently produce it)

This requires a migration (000037), handler/store changes, injection path changes, and UI work. See D10 for details.

**Option A — Yes (expose in UI):**

Frontend changes in `OrgCredentialsTab`:
- Add "Custom" to provider dropdown
- When "Custom" selected, show "Base URL" field (e.g., `http://vllm.internal.acme.com:8000/v1`)
- Optionally show "Provider Name" field (for the opencode config key — defaults to "custom")
- Optionally show "Model IDs" field (comma-separated model names the endpoint serves)

Backend changes (migration 000037 + handler + store + injection — see D10):
- `ALTER TABLE provider_credentials ADD COLUMN base_url TEXT NOT NULL DEFAULT '';`
- Handler: accept `baseURL` in create/update requests
- Store: persist and read `base_url`
- `FormatOpenCodeConfig`: emit `baseURL` into opencode provider config when non-empty

Effort: ~4-6h (migration + handler + store + injection + frontend).

**Option B — No (hide it):**

Orgs can only select from the built-in provider list (openai, anthropic, google). Custom endpoints are configured via direct API calls or database manipulation (undocumented, unsupported).

Consequence: orgs that run their own inference cannot use it through the normal UI. They'd need to `curl POST /orgs/:id/credentials` with a custom provider name. This is a poor experience for a use case that's increasingly common (private/fine-tuned models).

**Platform precedent:**
- **OpenRouter:** Custom endpoints supported
- **LiteLLM:** Built around custom endpoints
- **Continue.dev:** Custom endpoints supported
- **Most AI platforms:** Support OpenAI-compatible custom endpoints as table stakes

**Effort:** Option A = ~4-6h (migration + backend + frontend). Option B = 0h.

**Revisit trigger:** Never. If we're going to do it, do it now while the credential tab is being built.

**Recommendation:** Yes. Requires a migration + backend changes (~4-6h total, not the ~1h originally estimated). Blocking self-hosted inference would be a competitive disadvantage — many enterprise AI teams run private models. Add "Custom" to the provider dropdown, a "Base URL" field in US-43.5, and migration 037 + handler/store/injection changes.

---

### Q6: Data residency — should org workspaces be pinned to specific regions? — ✅ Resolved: D11 (no, future epic)

**Context:** Some enterprises (especially EU-based under GDPR, healthcare under HIPAA, finance under regional regulations) have legal requirements that their data must stay in a specific geographic region. "Data residency" means: this org's workspaces must run in EU-West, their PVC data must be stored in EU-West, their database rows must be in EU-West.

**Concrete example:** Acme Corp GmbH (Munich) wants to use LLMSafeSpace. German law requires their data to stay in the EU. If our cluster is in us-east-1, we cannot serve them without data residency.

**Option A — Yes (multi-region with residency):**

This is a major infrastructure project:

1. **Multi-region Kubernetes:** Deploy clusters in us-east-1, eu-west-1, ap-southeast-1. Either:
   - Multiple clusters with a global API gateway routing to the right region (~weeks of work)
   - Single cluster spanning regions (EKS/GKE multi-AZ doesn't cover multi-region; needs federation or Karmada)

2. **Region-aware storage:**
   - Longhorn PVCs are cluster-scoped — no cross-region replication
   - Each region needs its own storage backend
   - Database: either multi-region PostgreSQL (CockroachDB, Aurora Global, Postgres-BDR) or per-region databases with sync

3. **Region-aware scheduling:**
   - `organizations.region` column
   - Workspace CRD gets `spec.region` or node selectors
   - Kubernetes node affinity / topology spread constraints to pin pods to region
   - `nodeSelector: topology.kubernetes.io/region: eu-west-1`

4. **Compliance auditing:**
   - Prove to auditors that data never left the region
   - Log every data access with region attribution
   - Network policies preventing cross-region egress

5. **Operational overhead:**
   - 3x the infrastructure cost
   - 3x the monitoring surface
   - Cross-region debugging is hard
   - Backup/restore per region

Effort: This is a 3-6 month project. It should be its own epic.

**Option B — Defer (single-region):**

All workspaces run in the single deployment region. All data stays where the cluster is. Customers with residency requirements cannot be served.

**Platform precedent:**
- **GitHub:** Multi-region with residency for Enterprise Cloud
- **Slack:** Multi-region; Enterprise Grid supports data residency
- **AWS:** Built around regions
- **Most SaaS:** Start single-region, add residency for enterprise tier later

**Effort:** Option A = months (its own epic). Option B = 0h.

**Revisit trigger:** An enterprise customer with a contract that requires data residency. Until then, most initial customers (small teams, startups, US-based companies) don't need it.

**Recommendation:** No for now. Single-region. This is a major infrastructure project that should be its own epic. Most initial customers don't have residency requirements. When an enterprise contract requires it, the work is: multi-region cluster deployment + region-aware scheduling + per-region storage. Track as a future epic.

---

## Phase-Specific Decisions

These questions can be settled when their phase is about to start, but preferences now help avoid rework.

### Billing Decisions (Phase 4)

#### Q-B1: Seat-based vs flat vs hybrid org pricing? — ✅ Resolved: D12 (metered for both tiers: individual = low flat + usage; org = per-seat + usage)

**Context:** The README proposes placeholder prices ($100 Team + $20/seat, $500 Business + $30/seat). The pricing model determines the Stripe setup, the seat-counting logic, and the feature gating rules.

**Option A — Flat pricing (simpler):**
- Org pays a fixed monthly fee regardless of member count
- e.g., Team $200/mo (up to 10 members), Business $1000/mo (up to 50 members)
- Stripe: simple subscription, no metered components
- Seat counting: enforced by us (reject new invitations if at seat limit)
- Pros: predictable revenue, simple Stripe setup, easy to explain
- Cons: a 2-person team pays the same as a 10-person team; at large scale, flat pricing leaves money on the table

**Option B — Per-seat pricing (Stripe-native):**
- Org pays base fee + per-seat charge
- e.g., Team $100/mo + $20/seat/mo
- Stripe: subscription with per-seat pricing — Stripe natively handles seat additions/removals and proration
- Seat counting: Stripe tracks this; we sync from webhook events (`customer.subscription.updated` includes quantity)
- We enforce: `org_memberships` count ≤ Stripe subscription quantity (block invitations if exceeded)
- Pros: fair (pay for what you use), scales with org growth, Stripe handles proration
- Cons: members see cost impact of their seat; removing a member triggers proration event

**Option C — Hybrid (base + seats + usage):**
- Base fee covers platform access
- Per-seat covers human members
- Per-token/per-compute covers actual AI usage (metered)
- e.g., $100/mo base + $20/seat/mo + $0.50 per million tokens
- Stripe: subscription (base + seats) + metered billing (usage)
- Most complex but most accurate
- Pros: perfectly aligned cost-to-value
- Cons: users hate unpredictable bills; hard to estimate monthly cost

**Implementation impact per option:**
| Aspect | Flat (A) | Per-seat (B) | Hybrid (C) |
|--------|----------|-------------|-----------|
| Stripe setup | Simple subscription | Subscription with quantity | Subscription + metered |
| Seat enforcement | Manual (we count) | Stripe quantity (sync via webhook) | Stripe quantity |
| Proration | None | Stripe handles | Stripe handles |
| Usage tracking | Not needed | Not needed | Epic 12 `BillingExporter` → Stripe Metered |
| Invoice complexity | Simple | Moderate | Complex |
| Effort | ~2h | ~4h | ~8h (includes US-43.17 metered) |

**Recommendation:** ~~Start with **Option B (per-seat)**.~~ **Confirmed as D12: metered billing for both tiers** (Option C — base + seats + usage). Per-seat pricing (Option B) is part of the model for orgs, but usage metering is required from day one for both individual and org tiers to prevent power-user abuse. US-43.17 (Stripe Metered) is critical path, not deferred.

---

#### Q-B2: Trial periods for org plans? — ✅ Resolved: D13 (configurable, default off: 3 days, 3 members, 1 workspace)

**Context:** Should new orgs get a free trial (e.g., 14 days) before their subscription starts? Trials reduce friction for adoption but can be abused.

**Option A — Yes (14-day trial via Stripe):**
- Stripe natively supports trial periods: `subscription_data: { trial_period_end: <timestamp> }`
- User creates org → Stripe Checkout with trial → no charge for 14 days → card charged on day 15
- Org is `active` during trial (full access)
- Stripe webhook `customer.subscription.updated` fires when trial ends → if card valid, charge succeeds → stays active. If card invalid, `subscription_status = 'past_due'`
- Stripe webhook `invoice.payment_failed` during trial-end → grace period → suspend

Abuse prevention:
- One trial per Stripe customer (we check `billing_accounts` for existing subscriptions)
- Card required upfront (Stripe can validate card during trial)
- Platform admin can disable trials globally via `instance_settings`

Effort: ~2h (Stripe trial config in checkout session + webhook handling already built in US-43.1)

**Option B — No trials:**
- Pay before org is active. No free period.
- Simpler, no abuse surface
- Higher conversion friction — users hesitate to pay before trying

**Option C — Freemium (free tier org):**
- A free "Starter" org tier: max 3 members, 1 workspace, shared free-tier models
- No trial expiry — it's permanently free
- Upgrade to Team/Business when limits are hit
- Requires careful limit enforcement to prevent abuse

**Recommendation:** ~~Option A (14-day trial).~~ **Confirmed as D13: configurable, default off** (3 days, 3 members, 1 workspace when enabled — user chose a more constrained trial than the 14-day proposal). Stripe handles the mechanics. Card-required-upfront prevents casual abuse.

---

#### Q-B3: Payment failure → grace period before suspension? — ✅ Resolved: D14 (7-day grace via Stripe Smart Retries)

**Context:** When a payment fails (expired card, insufficient funds, etc.), should we immediately suspend the org or give a grace period?

**Option A — Immediate suspension:**
- Stripe webhook `invoice.payment_failed` → `organizations.status = 'suspended'`
- All members lose access immediately
- Data preserved (PVCs retained) but inaccessible
- Reactivate by updating payment method in Stripe Portal
- Pros: clean, no free service
- Cons: a single transient payment failure (bank holiday, card renewal) locks out the entire org. Bad UX.

**Option B — Grace period (recommended):**
- Stripe webhook `invoice.payment_failed` → `organizations.subscription_status = 'past_due'` (org stays active)
- Stripe retries payment automatically (Smart Retries — up to 4 attempts over 2 weeks)
- If all retries fail (typically 14 days) → Stripe marks subscription `unpaid` → webhook → `organizations.status = 'suspended'`
- During grace period: org is fully functional, members see a "payment issue" banner in the portal
- Stripe handles dunning emails automatically (we don't build dunning)
- After suspension: data preserved for 30 days (matches Q3 retention). Payment recovery → unsuspend.

Implementation:
```sql
-- organizations table already has subscription_status (added in migration 030)
-- Values: 'active', 'trialing', 'past_due', 'canceled', 'unpaid'
```

Webhook handling:
| Stripe Event | Action |
|---|---|
| `invoice.payment_failed` | `subscription_status = 'past_due'`; show banner |
| `invoice.payment_action_required` | (3DS auth) — email admin via Stripe |
| `customer.subscription.updated` (status=unpaid) | `status = 'suspended'` |
| `customer.subscription.deleted` | `status = 'suspended'` |
| `invoice.paid` | `subscription_status = 'active'`; clear banner |

Effort: ~2h (webhook handler + frontend banner)

**Option C — No grace period, no suspension (infinite free service):**
- Payment fails → org stays active indefinitely
- We eat the cost
- Obviously not viable

**Recommendation:** ~~Option B (14-day grace period via Stripe Smart Retries).~~ **Confirmed as D14: 7-day grace period** (user chose tighter window). Stripe handles retries and dunning emails — we listen for the final `unpaid` status. During the grace period, a non-blocking banner in the org portal reminds the admin to update their payment method. This matches standard SaaS behavior (GitHub, Vercel, Slack all have grace periods).

---

#### Q-B4: Usage-based (metered) billing at launch or flat-only? — ✅ Resolved: D12 (metered at launch — critical path)

**Context:** Beyond seat-based pricing, should we also bill for actual LLM token usage? Epic 12 records `usage_events` (compute_seconds, llm_tokens, storage_bytes) per workspace. Stripe Metered Billing can consume these events and add usage charges to invoices.

**Option A — Yes (metered at launch):**
- Epic 12's `BillingExporter` reports usage to Stripe as metered events
- Stripe aggregates and invoices: "Base: $100, Seats (5 × $20): $100, Tokens (2.5M × $0.50/M): $1.25 → Total: $201.25"
- Requires US-43.17 implementation (~6h)
- Risk: users get unpredictable bills ("why is my bill $500 this month?"). Needs dashboards and alerts.
- Risk: metering gaps (DLQ, reconciliation) mean undercounting → revenue loss

**Option B — No (flat-only at launch, add metered later):**
- Phase 4 ships with base + seats only
- US-43.17 (metered billing) is implemented but not activated
- Usage is metered in the DB (Epic 12) for dashboards and internal analytics
- Customers see usage in the portal but aren't billed for it
- Activate metered billing when pricing model is validated and metering reliability is proven

**Recommendation:** ~~Option B (flat-only at launch).~~ **Confirmed as D12: metered billing at launch** (Option A). Metering reliability concerns are addressed in D12 (synchronous `llm_tokens` writes recommended, ~2ms p99 overhead). US-43.17 is critical path. The metering infrastructure from Epic 12 must be validated as billing-grade before launch — see D12 for the specific fixes needed (token reconciliation path, synchronous write option).

---

### Policy Decisions (Phase 2)

#### Q-P1: Which policies to enforce at launch? — ✅ Resolved: D15 (ship 4: allowed_models, allowed_providers, max_workspaces, max_active_workspaces; no UI)

**Context:** The policy engine (W5) defines 8 policy types. Not all are equally important or easy to implement. Which subset should ship in Phase 2?

| Policy | Complexity | Value | Recommendation |
|--------|-----------|-------|----------------|
| `allowed_models` | Low — filter in `ListModels`, reject in model selection | High — cost control, compliance | **Ship in Phase 2** |
| `allowed_providers` | Low — same pattern as allowed_models | Medium — redundant with allowed_models for most cases | **Ship in Phase 2** |
| `max_workspaces_per_member` | Low — count query in `CreateWorkspace` | High — prevents workspace sprawl | **Ship in Phase 2** |
| `max_active_workspaces_per_member` | Low — count active workspaces in `CreateWorkspace` | Medium — compute cost control | **Ship in Phase 2** |
| `allowed_runtimes` | Low — validate runtime env in `CreateWorkspace` | Low — runtimes are admin-managed already | **Defer** |
| `egress_policy` | High — requires controller NetworkPolicy generation, per-workspace egress rules | High — security compliance | **Defer to Phase 2b** (separate story) |
| `require_mfa` | Medium — MFA infrastructure doesn't exist yet | Medium — security for enterprise | **Defer** (depends on Epic 34 MFA) |
| `max_session_duration_hours` | Medium — requires activity tracking + auto-suspend | Low — nice to have | **Defer** |

**Recommendation:** Ship Phase 2 with the 4 low-complexity, high-value policies: `allowed_models`, `allowed_providers`, `max_workspaces_per_member`, `max_active_workspaces_per_member`. These are all synchronous checks in `CreateWorkspace` or `ListModels` — no controller changes, no infrastructure. Defer `egress_policy` (complex, controller-level), `require_mfa` (depends on Epic 34), and `max_session_duration_hours` (needs activity tracking work).

---

#### Q-P2: Where does policy enforcement live? — ✅ Resolved: D15 (API service for synchronous checks; PolicyService injected into handlers)

**Context:** Policies must be enforced at the right layer. Too early and you block legitimate operations. Too late and the damage is done (workspace created, tokens consumed).

**Enforcement points:**

| Policy | Enforcement Point | How |
|--------|-------------------|-----|
| `allowed_models` | API: `ListModels` | Filter the model catalog based on org policy. Member never sees disallowed models. |
| `allowed_models` | API: workspace proxy | Reject requests to disallowed models. Belt-and-suspenders: even if a member somehow selects a disallowed model, the proxy blocks the request. |
| `allowed_providers` | API: credential injection | Skip injecting credentials from disallowed providers. Workspace silently has no key for that provider. |
| `max_workspaces_per_member` | API: `CreateWorkspace` | Count existing workspaces for this user under this org. Reject if over limit. |
| `max_active_workspaces_per_member` | API: `CreateWorkspace` + `ResumeWorkspace` | Count active (non-suspended) workspaces. Reject creation/resume if over limit. |
| `egress_policy` | Controller: NetworkPolicy generation | Write NetworkPolicy CRD with egress rules. Enforced by kube-network. |

**Architecture decision:** All synchronous policy checks live in the **API service** (workspace_service.go, model handler, proxy handler), not the controller. The controller only enforces what's written to the CRD (egress_policy → NetworkPolicy). This matches the existing architecture: the API service owns validation and business rules; the controller owns Kubernetes state.

A `PolicyService` interface:
```go
type PolicyService interface {
    GetPolicy(ctx context.Context, orgID, key string) (json.RawMessage, error)
    CheckModelAllowed(ctx context.Context, orgID, modelID string) (bool, error)
    CheckWorkspaceQuota(ctx context.Context, orgID, userID string) error
}
```

Injected into `WorkspaceService.CreateWorkspace`, `ModelHandler.ListModels`, and `ProxyHandler`.

**Recommendation:** API service for synchronous checks (model, quota, provider). Controller for egress (CRD → NetworkPolicy). A `PolicyService` in the API layer reads from `org_policies` (cached in Redis, 5-minute TTL) and is injected into the relevant handlers.

---

#### Q-P3: Policy inheritance — org overrides platform default? — ✅ Resolved: D16 (org intersects platform; final = org ∩ platform)

**Context:** There are three levels of policy: platform-wide defaults (set by platform admin), org-level policies (set by org admin), and potentially per-user policies. How do they interact?

**Option A — Org overrides platform default:**
- Platform admin sets: `allowed_models = ["gpt-4o", "gpt-4o-mini", "claude-3.5-sonnet"]`
- Org admin restricts: `allowed_models = ["gpt-4o"]` (subset of platform's list — more restrictive)
- Org admin CANNOT expand: `allowed_models = ["gpt-4o", "o1-preview"]` (o1-preview not in platform's list → rejected)
- Rule: org policy is intersected with platform policy. Final = org ∩ platform.
- This is the standard model: platform sets the ceiling, org sets the floor within that ceiling.

**Option B — Org policy is independent:**
- Org admin can set any policy regardless of platform defaults
- Platform policy is a suggestion, not a constraint
- Risk: org admin allows a model the platform doesn't want to support (e.g., a model with compliance issues)

**Option C — No platform-level policy:**
- Only org-level policies exist
- Platform admin doesn't set model/provider restrictions
- Simpler but no global guardrails

**Recommendation:** Option A. Platform policy sets the ceiling (what's allowed on this instance). Org policy narrows within that ceiling (what this org allows). No per-user policy within org — all members of the org share the same org policy. Implementation: `final_allowed = org_policy ∩ platform_policy` computed at enforcement time. If org policy is absent for a key, platform default applies.

---

### SSO Decisions (Phase 3)

#### Q-S1: Auto-provision users on first SSO login? — ✅ Resolved: D17 (yes — creates separate account per IdP email, see D9)

**Context:** When a user logs in via OIDC SSO for the first time, there's no LLMSafeSpace account for them. Should we auto-create one, or require pre-existing accounts?

**Option A — Yes (auto-provision, recommended):**
- User clicks "Sign in with [Org Name]" on login page
- OIDC flow completes — we get their email, name, sub from the IdP
- No existing user with that email → auto-create user with: username = email, password = random (they'll use SSO, never need password)
- Auto-create `org_memberships` row linking them to the org
- Auto-create `user_keys` row (random DEK — they'll set password later if they want non-SSO login)
- User is logged in and redirected to chat

This replaces the need for SCIM (US-43.12). The IdP controls access — when the IdP removes a user, they can no longer SSO in. Their existing account remains but is orphaned (no org access).

Configurable per org:
- `org_sso_configs.auto_provision = true` (default)
- If `false`: only pre-existing users with matching email can SSO in. Admins must create accounts first.

Effort: ~2h (user creation logic in OIDC callback handler)

**Option B — No (pre-existing accounts only):**
- Admin must create user accounts before SSO works
- User logs in via SSO → if no matching account → "Contact your administrator"
- Requires manual user management — defeats the purpose of SSO
- Would require SCIM for automated provisioning (which we deferred in D3)

**Recommendation:** Option A (auto-provision). This is why we deferred SCIM — OIDC auto-provisioning covers the same need with far less complexity. The IdP is the source of truth for who can access the org. When someone leaves the company, the IdP removes them → they can't SSO → their org membership becomes orphaned (we can deactivate it via a periodic check or webhook).

---

#### Q-S2: Email domain → org auto-join mapping? — ✅ Resolved: D17 (yes, with DNS domain verification)

**Context:** When a user from `@acme.com` visits the login page, should we automatically show "Sign in with Acme Corp" and route them to Acme's OIDC provider?

**Option A — Yes (domain mapping):**
- Org admin claims a domain: `@acme.com`
- Platform verifies domain ownership (DNS TXT record or HTML file)
- When a user enters their email on the login page, we check: does the email domain match a claimed org domain?
- If yes: show "Sign in with Acme Corp" button prominently
- If user clicks: OIDC flow with Acme's IdP
- If user has no account and auto-provision is on: account created + membership created

This is how Google Workspace, GitHub Enterprise, and most enterprise SSO work. The domain is the routing key.

Schema:
```sql
-- org_sso_configs table
CREATE TABLE org_sso_configs (
    org_id              UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    oidc_discovery_url  TEXT NOT NULL,
    oidc_client_id      TEXT NOT NULL,
    oidc_client_secret  BYTEA NOT NULL,  -- encrypted with org DEK
    claimed_domains     TEXT[] NOT NULL DEFAULT '{}',  -- ['acme.com', 'acme.io']
    auto_provision      BOOLEAN NOT NULL DEFAULT true,
    group_role_mapping  JSONB NOT NULL DEFAULT '{}',   -- {"admins": "admin", "developers": "member"}
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Domain verification:
- Admin enters domain → we generate a random token → admin adds `TXT llmsafespace-verify=<token>` to DNS
- Background job checks DNS → if match → domain marked verified
- Unverified domains: SSO works but no auto-routing (user must know the org slug)

Effort: ~4h (domain verification + login page routing logic)

**Option B — No (slug-based routing):**
- User visits `https://app.safespaces.dev/login/acme-corp` or `https://app.safespaces.dev/sso/acme-corp`
- The slug in the URL determines which IdP to use
- No email-domain detection
- Simpler but less polished — users must know their org slug or find a link

**Recommendation:** Option A (domain mapping) with verification. It's the expected enterprise experience. Phase 3 can ship with slug-based routing first (simpler), then add domain detection as a Phase 3b enhancement. Domain verification prevents malicious org admins from intercepting another org's users.

---

#### Q-S3: Group/role claims → org role mapping? — ✅ Resolved: D17 (yes — OIDC groups claim maps to admin/member via configurable JSON rules)

**Context:** OIDC providers return group/role claims in the ID token or userinfo endpoint. For example, Okta might return `groups: ["llmsafespace-admins", "engineering"]`. Should these map to org roles?

**Option A — Yes (claim-based role mapping):**
- Org admin configures mapping in SSO settings:
  ```json
  {
    "admins": "admin",
    "llmsafespace-admins": "admin",
    "developers": "member",
    "*": "member"
  }
  ```
- On SSO login, we read the `groups` claim from the IdP
- Map groups to org role: if any group maps to "admin" → admin; otherwise member
- Role is applied on every login (if IdP removes admin group → next login demotes the user)
- Stored in `org_sso_configs.group_role_mapping` (JSONB)

This means role management lives in the IdP, not in LLMSafeSpace. The org admin's job in LLMSafeSpace becomes read-only for membership — the IdP controls who's a member and what role they have.

Effort: ~3h (claim parsing + role mapping logic + membership sync on each login)

**Option B — No (manual roles):**
- SSO authenticates identity; org admin manually assigns roles in LLMSafeSpace
- First SSO login → auto-provision as member; admin promotes manually
- More work for org admin but more control

**Recommendation:** Option A (claim-based mapping). It's the enterprise expectation — if the IdP says you're an admin, you're an admin. IdP-driven role management is the entire point of enterprise SSO. The `*` wildcard default ensures unknown groups get member role. The mapping is configurable per org — orgs that want manual control can set `"*": "member"` and manage promotions manually.

---

#### Q-S4: OIDC client config storage — encrypted at rest? — ✅ Resolved: D17 (server KEK — always decryptable, no org DEK dependency)

**Context:** The OIDC client secret is a credential that the org admin enters. It must be stored so we can use it during the OIDC token exchange. How should it be stored?

**Option A — Encrypted with org DEK (recommended):**
- `org_sso_configs.oidc_client_secret` is BYTEA
- Encrypted with the org DEK (Epic 11's per-org data encryption key)
- On SSO callback: org DEK fetched from Redis cache → decrypt client secret → use for token exchange
- If org DEK not in cache: SSO callback fails (admin must be logged in to have DEK cached — same constraint as accept-key)

This reuses the existing org DEK infrastructure. The client secret is protected by the same key that protects org LLM credentials.

Risk: if no admin is logged in (org DEK not cached), SSO callback fails. Mitigation: org DEK cache TTL is 7 days after rotation (per Epic 11). In practice, at least one admin logs in weekly.

Effort: ~1h (reuse existing org DEK encrypt/decrypt functions)

**Option B — Encrypted with server KEK (like admin credentials):**
- `org_sso_configs.oidc_client_secret` encrypted with `LLMSAFESPACE_MASTER_SECRET`-derived server KEK (same as `owner_type='admin'` credentials)
- Always decryptable — no org DEK dependency
- SSO callback always works

Risk: server KEK compromise exposes all OIDC client secrets. But server KEK compromise already exposes all admin credentials — this doesn't increase blast radius.

Effort: ~1h (reuse existing admin encryption functions)

**Option C — Plaintext:**
- Store client secret as TEXT
- Never acceptable — DB compromise exposes all IdP client secrets

**Recommendation:** Option B (server KEK). While Option A is theoretically more secure (org-level isolation), the practical risk of SSO callback failures (when no admin has logged in recently) makes it operationally fragile. Option B is always-available and doesn't increase blast radius beyond what admin credentials already have. The server KEK is derived from `LLMSAFESPACE_MASTER_SECRET` which is already the root of trust for admin credentials.

---

### Platform Ops Decisions (Phase 5)

#### Q-O1: Platform admin = existing `users.role='admin'`? — ✅ Resolved: D18 (yes, reuse existing)

**Context:** The platform admin dashboard (`/admin/platform`) needs to be gated. Who is a platform admin?

**Option A — Reuse existing `users.role='admin'` (recommended):**
- The `users` table already has a `role` column: `'user'` or `'admin'`
- The `AdminGuard` middleware (`api/internal/server/admin_guard.go`) already checks this
- Platform admin dashboard routes use `AdminGuard`
- No schema change, no new role, no new middleware
- Platform admins are: you (the operator), any ops team members you promote

How to promote: `UPDATE users SET role = 'admin' WHERE id = ...` (manual, ops-only). Or a CLI tool: `./llmsafespace-admin promote-user <email>`.

Effort: 0h (reuse existing)

**Option B — New `platform_admin` role:**
- Add `users.role = 'platform_admin'` as a third value
- Separate from org admin (which is per-org, in `org_memberships.role`)
- Requires updating AdminGuard, adding new middleware, updating all admin routes
- More explicit but more work

**Recommendation:** Option A. `users.role='admin'` is the platform-wide admin. It already powers the admin settings UI, admin provider credentials, admin usage endpoints. The platform dashboard is just another admin-gated route. No reason to introduce a new role. Org admins are per-org (in `org_memberships`) — there's no collision.

---

#### Q-O2: Suspension granularity — org only, or user too? — ✅ Resolved: D19 (both org-level and user-level)

**Context:** Suspension can be applied at two levels: the org (entire org blocked) or individual users (one member blocked without affecting the org). Which should Phase 5 support?

**Option A — Org-level only (recommended for Phase 5):**
- `organizations.status = 'suspended'` → all members lose access
- Use cases: non-payment, abuse, platform policy violation, enterprise offboarding
- `OrgMemberGuard` / `OrgAdminGuard` check `status = 'active'` (from US-43.1)
- Simple, one status flag, one guard update

**Option B — User-level too:**
- `users.status = 'suspended'` → user blocked across all orgs and personal workspaces
- Use cases: individual abuse, security incident on one user, compliance hold
- Requires a new `UserStatus` check in auth middleware (block login, block API access)
- Requires a new `users.status` column and migration
- More granular but more complex

**Option C — Both:**
- Org-level suspension (Phase 5)
- User-level suspension (Phase 5b or separate epic)
- User-level is primarily a security/abuse tool, not a billing tool

**Recommendation:** ~~Option A (org-level only) for Phase 5.~~ **Confirmed as D19: Option C — both org-level and user-level suspension.** The user wants to suspend individual accounts that aren't tied to an org (personal accounts that abuse the system). User-level suspension adds `users.status` column (migration 000033) and an auth middleware check.

---

#### Q-O3: Suspension behavior — what exactly happens? — ✅ Resolved: D20 (hard operational: kill all pods, preserve PVCs, storage metering continues, compute/token stops)

**Context:** When an org is suspended, what happens to its workspaces, sessions, credentials, and data? This needs precise definition.

**Confirmed behavior (D20 — hard operational, data preserved):**

| Resource | On Suspension | On Unsuspension |
|----------|---------------|-----------------|
| Member access | All members get 403 from org routes (`OrgMemberGuard` rejects) | Access restored immediately |
| Active workspaces | **All pods terminated** — every org workspace Active → Suspended (D20) | User/admin manually resumes each workspace (~3s each) |
| Active sessions | SSE streams terminated (pods killed) | — |
| New workspace creation | Blocked — `CreateWorkspace` checks org status | Allowed |
| Org credentials injection | Stops (no active pods to inject into) | Resumes on workspace resume |
| Pending invitations | Remain pending (recipients can't accept — org not active) | Can accept again |
| Billing | Subscription marked `unpaid`/`canceled` by Stripe webhook | Reactivated when payment recovers |
| Data (PVCs, DB) | **Preserved** — nothing deleted | Intact |
| Compute metering | **Stops** (no active pods) | Resumes when workspaces resumed |
| Storage metering | **Continues** — PVCs still allocated | — |

**Implementation (D20):** Controller-query based. Controller reconciler queries `GET /api/v1/internal/orgs/:orgID/status` on each workspace reconcile (cached 30s). If org is suspended, controller suspends the workspace (Active → Suspended, pod deleted, PVC retained). Avoids O(n) K8s PATCH calls from API service. Atomic — all workspaces see same org status on next reconcile.

**Recommendation:** ~~Soft suspension for Phase 5.~~ **Confirmed as D20: hard operational suspension** — all workspace pods terminated, PVCs preserved. The user chose to stop compute costs during suspension. Storage metering continues (PVCs still allocated). Implementation: controller-query approach (controller reads org status via internal API, not per-workspace labels).

The 30-day retention from Q3 applies: if a suspended org doesn't recover within 30 days, platform admin can hard-delete (after verifying no workspaces remain — blocked by Q3 Option B).

---

## Non-Requirements (Explicitly Out of Scope)

| Item | Rationale |
|------|-----------|
| Org sub-groups / nested teams | Deferred (D6). Flat membership for now, but members see only their own workspaces. Sub-teams designed for as future extension. |
| Workspace ownership transfer | No (D7). Create under org from start. Credential/session migration edge cases too complex. |
| Marketplace for sharing org templates | Future feature after template system exists |
| Cross-org collaboration / federation | Enterprise tier; complex trust model |
| Custom branding / white-label orgs | Enterprise tier; deferred |
| Mobile admin app | Web portal sufficient |
| Org-level resource quotas (CPU/memory) | K8s-level concern; platform-wide quotas first |
| SAML SSO | Declining adoption; OIDC covers modern IdPs including Google/Microsoft/Azure (D3) |
| SCIM provisioning | Auto-provisioning via OIDC covers most needs; SCIM adds sync complexity (D3) |
| Custom billing UI / payment forms | Stripe Customer Portal handles all payment management (D1) |
| Data residency / multi-region | Major infrastructure project; future epic (D11) |
| Policy UI (US-43.9) | Deferred (D15). Enforcement shipped; admins configure via API. UI is a future story. |
| `egress_policy` enforcement | Complex controller-level NetworkPolicy generation; Phase 2b follow-up (D15) |
| `require_mfa` policy | OIDC providers handle MFA at IdP layer; most orgs will use their own OIDC (D15) |
| `max_session_duration_hours` policy | Requires activity tracking work; deferred (D15) |
| `allowed_runtimes` policy | Runtimes are admin-managed already; low value (D15) |
| Per-user policy overrides within org | All org members share the same org policy; no per-user scoping (D16) |
| Account linking (multiple accounts → one identity) | Future feature (D9). SSO creates separate accounts per email for now. |
| PVC-to-S3 cold storage migration | Future epic. Suspended org PVCs remain on Longhorn for now (D20). |
