# Epic 43: Organization Management & Multi-Tenant Product

**Status:** Planning
**Created:** 2026-06-14
**Depends On:** Epic 11 (Organizations — foundation complete), Epic 12 (Usage Metering & Billing — metering infrastructure exists)
**Priority:** High

**Motivation:** Epic 11 built the org crypto primitives, membership tables, and credential injection. But the product layer is missing: any user can create unlimited orgs with no gating, there's no admin portal, no email invitations, no SSO, no policy engine, and no way to charge for the service. This epic turns the technical foundation into a real multi-tenant product.

---

## Current State (as of Epic 11 completion)

| Area | Status | Detail |
|------|--------|--------|
| Org schema | Done | `organizations`, `org_memberships`, `org_key_members`, `workspaces.org_id` |
| Org DEK crypto | Done | Per-admin wrapped DEK, domain-separated HKDF, rotation |
| Org credential CRUD | Done | Create/list/update/delete org credentials, auto-apply rules |
| Org credential injection | Done | `decryptBinding` org branch, `SeedWorkspaceCredentials` org seeding |
| Org member management (API) | Done | Add/remove members, role changes, key handshake, TOCTOU-safe |
| Org guard middleware | Done | `OrgMemberGuard`, `OrgAdminGuard` |
| Frontend: org list + create | Partial | Settings → Organisations tab (create, list, delete, accept-key) |
| Frontend: member management | Missing | No UI for listing members, inviting, changing roles |
| Frontend: credential management | Missing | No UI for org credentials |
| Frontend: org workspace listing | Missing | No UI |
| Org creation gating | Missing | Any authenticated user can create unlimited orgs |
| Email invitations | Missing | API takes raw user IDs, not email addresses |
| Org admin portal | Missing | Org management buried in user settings — wrong UX |
| SSO/Identity provisioning | Missing | No OIDC/SAML/SCIM, manual invitations only |
| Policy engine | Missing | No org-level model/provider restrictions, workspace quotas, egress rules |
| Billing tiers | Partial | Epic 12 has metering infra + billing schema, but no org-level billing or payment gating |
| Audit logging | Partial | `secret_audit_log` exists but not org-scoped; Epic 12 added `audit_log` table |
| Deprovisioning workflow | Missing | No defined flow for offboarding members, archiving workspaces |

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
  ├── Creates org (gated by payment or approval)
  ├── Invites members by email
  ├── Manages shared org credentials (LLM keys)
  ├── Members auto-get org credentials in their workspaces
  ├── Per-seat or flat org subscription
  └── Uses org admin portal at /admin/orgs/:slug

Enterprise IT Admin
  ├── Org provisioned by platform operator or self-service enterprise signup
  ├── Configures SSO (OIDC/SAML) — users auto-provision on first login
  ├── Sets policies: allowed models, workspace quotas, egress rules
  ├── Views audit logs, usage reports, billing
  ├── Manages department/team structure within org
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
  ├── /login, /register, /sso/:provider (authentication)

/chat (authenticated user UI)
  ├── Chat interface, workspace management
  ├── Personal settings (credentials, API keys, secrets)
  └── Org switcher in sidebar (if member of orgs)

/admin/orgs/:slug (org admin portal)
  ├── Overview: member count, usage summary, plan status
  ├── Members: invite, roles, SSO groups, deprovision
  ├── Credentials: org LLM keys, provider config
  ├── Workspaces: list, quotas, archive
  ├── Policies: allowed models, egress, workspace limits
  ├── Billing: subscription, invoices, seats
  ├── Audit: org-scoped activity log
  └── SSO: OIDC/SAML config, SCIM endpoint

/admin/platform (platform operator only)
  ├── Orgs: create, suspend, configure, billing
  ├── Users: individual subscribers, usage, billing
  ├── Platform credentials: free-tier keys
  ├── System: metrics, incidents, global policies
  └── Audit: cross-org activity, security events
```

---

## Scenarios & Workflows

### W1: Org Creation & Gating

**Question:** Who can create orgs and how?

**Options:**
- A) Platform admin approval required — safest, slowest
- B) Self-service with payment (must subscribe before org is active) — best for SaaS
- C) Self-service free with limits (max 1 org, 3 members), upgrade for more — growth model
- D) Platform admin creates org, then hands off to org admin — enterprise model

**Proposed:** B + D. Self-service with payment for small teams; platform admin provisions enterprise orgs manually with custom contracts.

**Flow (self-service):**
1. User clicks "Create Organization" (prominent, not in settings)
2. Enters org name, slug
3. Selects plan (Team $X/mo, Business $Y/mo)
4. Enters payment (Stripe)
5. Org created, user becomes founding admin
6. Redirected to org admin portal

**Flow (enterprise):**
1. Platform admin creates org via /admin/platform
2. Assigns initial org admin (by email or user ID)
3. Org admin receives invitation, configures SSO
4. Users auto-provision via SSO

### W2: Email-Based Invitations

Current API takes raw user IDs. Need email-based invitations with accept/reject flow.

**Schema:**
```sql
CREATE TABLE org_invitations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id),
    email       TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'member',
    invited_by  TEXT NOT NULL REFERENCES users(id),
    token       TEXT NOT NULL,          -- opaque token for accept URL
    expires_at  TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    accepted_by TEXT REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Flow:**
1. Org admin enters email address(es) in portal
2. System sends invitation email with accept link
3. Recipient clicks link → if registered, joins org → if not, register first then join
4. If role=admin, key handshake triggers after join

### W3: Org Admin Portal

Dedicated UI for org management. Not a settings tab.

**Route:** `/admin/orgs/:slug`

**Tabs:**
1. **Overview** — member count, workspace count, usage this period, plan status
2. **Members** — table of members with role badges, invite button, remove/promote/demote actions, SSO status indicator
3. **Credentials** — org LLM credential cards (same pattern as user credentials but org-scoped)
4. **Workspaces** — all org workspaces with owner, phase, usage; archive/delete actions
5. **Policies** — model allowlist, provider restrictions, workspace quota per member, egress policy
6. **Billing** — current plan, seats used/available, invoices, payment method, upgrade/downgrade
7. **Audit** — org-scoped audit log (who created/deleted credentials, invited/removed members, changed policies)
8. **SSO** — OIDC discovery URL, SAML metadata URL, SCIM base URL, group-to-role mapping, auto-provision toggle

### W4: SSO & Identity Provisioning

Enterprise teams require SSO. Users should auto-join org on first SSO login.

**OIDC flow:**
1. Org admin enters OIDC discovery URL + client credentials in portal
2. Login page shows "Sign in with [Org Name]" for users with matching email domain
3. User authenticates via IdP → callback → map OIDC sub/email to user → auto-join org
4. Group/role claims map to org roles (admin/member)

**SAML flow:**
1. Org admin uploads SP metadata or enters IdP metadata URL
2. Login page shows SSO button
3. User authenticates via IdP → ACS callback → map NameID to user → auto-join

**SCIM provisioning:**
1. IdP pushes user/group changes to `/api/v1/scim/v2/Users` and `/Groups`
2. Auto-creates/updates/deactivates users and org memberships
3. Group membership drives org role assignment

### W5: Policy Engine

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

**Individual plans:**
| Plan | Price | Limits |
|------|-------|--------|
| Free | $0 | 1 workspace, shared free-tier models, 100 messages/mo |
| Pro | $20/mo | 5 workspaces, all models, unlimited messages |
| Team (individual seat) | $50/mo | 20 workspaces, priority compute |

**Org plans:**
| Plan | Price | Limits |
|------|-------|--------|
| Team | $100/mo base + $20/seat | Up to 10 seats, shared org credentials, 50 workspaces |
| Business | $500/mo base + $30/seat | Up to 50 seats, SSO, policies, audit log, 200 workspaces |
| Enterprise | Custom | Unlimited, SSO+SCIM, dedicated support, custom policies, SLA |

**Implementation:**
- Stripe for individual + Team/Business self-service
- Manual invoicing for Enterprise
- Epic 12 metering feeds usage events; billing service calculates charges
- Payment gating: org features locked until subscription active

### W7: Audit Logging (Org-Scoped)

Enterprise compliance requires "who did what, when" within the org.

**Events to audit:**
- Member joined / left / role changed
- Credential created / updated / deleted
- Policy created / updated
- Workspace created / archived / deleted
- SSO configuration changed
- Admin promoted / demoted
- Key rotation performed

**Schema:** Use Epic 12's `audit_log` table, add `org_id` column for org-scoped queries.

**UI:** `/admin/orgs/:slug/audit` — filterable table (by actor, action, date range, resource type).

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
| US-43.1 | Org creation gating — restrict to platform admins or paid users | 4h |
| US-43.2 | Email-based invitation system (schema, API, email send, accept flow) | 8h |
| US-43.3 | Org admin portal shell — route, layout, overview tab | 6h |
| US-43.4 | Members tab — list, invite, remove, role management | 8h |
| US-43.5 | Credentials tab — org credential CRUD UI | 6h |
| US-43.6 | Workspaces tab — org workspace listing | 4h |

### Phase 2: Policy & Control (differentiator)

| Story | Title | Effort |
|-------|-------|--------|
| US-43.7 | Policy schema + API — org_policies table, CRUD endpoints | 6h |
| US-43.8 | Policy enforcement — model allowlist, workspace quotas | 8h |
| US-43.9 | Policy UI — settings panel in org portal | 4h |

### Phase 3: Enterprise (enterprise tier enabler)

| Story | Title | Effort |
|-------|-------|--------|
| US-43.10 | OIDC SSO — config, login flow, auto-provisioning | 12h |
| US-43.11 | SAML SSO — config, login flow | 12h |
| US-43.12 | SCIM provisioning — user/group sync endpoints | 10h |
| US-43.13 | Org-scoped audit log — events, API, UI | 8h |

### Phase 4: Billing Integration (monetization)

| Story | Title | Effort |
|-------|-------|--------|
| US-43.14 | Stripe integration — customer, subscription, payment method | 10h |
| US-43.15 | Plan tiers — individual + org plans, feature gating | 8h |
| US-43.16 | Billing UI — plan selection, invoices, seats management | 6h |
| US-43.17 | Usage-based pricing — metering → billing calculation | 8h |

### Phase 5: Platform Operations (operator tooling)

| Story | Title | Effort |
|-------|-------|--------|
| US-43.18 | Platform admin dashboard — org list, suspend, configure | 8h |
| US-43.19 | Org suspension — disable org, block access, preserve data | 4h |
| US-43.20 | Cross-org audit — platform-level audit view | 4h |

---

## Open Questions

1. **Should orgs have sub-groups (teams/departments)?** Enterprise orgs have 500+ people. Flat member lists won't work. But this adds significant complexity (nested RBAC, group-scoped credentials). Proposal: defer to a future epic; Phase 1 is flat org membership.

2. **Workspace ownership transfer.** Can an org admin transfer a personal workspace to the org? Or is it "create under org from the start"? Proposal: create under org from the start (current behavior). Transfer is a future feature.

3. **Org deletion.** What happens to workspaces when an org is deleted? Proposal: hard delete blocked if workspaces exist. Admin must archive/delete workspaces first. Soft delete preserves data for 30 days.

4. **Multi-org membership.** Can a user belong to multiple orgs? Current schema supports it (composite PK on org_memberships). Proposal: yes — user sees a workspace switcher to navigate between personal and org workspaces.

5. **Custom models / BYO inference endpoints.** Should orgs be able to register custom model endpoints (e.g., a self-hosted vLLM)? Proposal: yes, via org credentials with a custom baseURL. Already supported by the provider_credentials schema.

6. **Data residency.** Enterprise may require workspaces in specific regions. Proposal: future epic. Phase 1 is single-region.

---

## Non-Requirements (Explicitly Out of Scope)

| Item | Rationale |
|------|-----------|
| Org sub-groups / nested teams | Adds nested RBAC complexity; flat membership sufficient for launch |
| Marketplace for sharing org templates | Future feature after template system exists |
| Cross-org collaboration / federation | Enterprise tier; complex trust model |
| Custom branding / white-label orgs | Enterprise tier; deferred |
| Mobile admin app | Web portal sufficient |
| Org-level resource quotas (CPU/memory) | K8s-level concern; platform-wide quotas first |
