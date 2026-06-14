# Epic 43 — Decision Log

**Epic:** Organization Management & Multi-Tenant Product
**Created:** 2026-06-14
**Last Updated:** 2026-06-14

All 20 open questions resolved. Decisions D1–D20 are confirmed. Detailed context for each is in the [Open Questions](./README.md#open-questions) section of the epic README (question headers note the confirmed decision ID).

---

## Confirmed Decisions

### D1: Stripe Customer Portal for all billing UI

**Status:** Confirmed (2026-06-14)
**Source:** User direction

**Context:** Epic 43 needs payment collection for org subscriptions. Options were: (A) build custom billing UI with credit card forms, plan selection, invoice rendering; (B) use Stripe's hosted Customer Portal for all payment management and store only the subscription tier in our DB.

**Decision:** Use Stripe Customer Portal. We build:
- Stripe Checkout Session creation (initial subscription redirect)
- Stripe Customer Portal Session creation (management redirect)
- Stripe Webhook handler (syncs `plan_id` + `subscription_status` to `organizations` table)
- Feature gating logic (reads `plan_id` from DB — no per-request Stripe API call)
- Plan tier definitions (config: tier → features map)

We do NOT build: credit card forms, invoice rendering, payment method CRUD, dunning, plan change UI, tax calculation. Stripe handles all of these.

**Consequences:**
- Zero PCI compliance scope (we never touch card numbers)
- US-43.16 (Billing UI) reduced from 6h to 2h (just a "Manage Billing" button + plan status display)
- Users leave our app to manage billing (Stripe-hosted portal) — standard SaaS pattern
- `BillingProvider` interface (`pkg/billing/provider.go`) already abstracts the provider, so we can swap to a different provider later
- `organizations.plan_id` and `organizations.subscription_status` are the local source of truth for feature gating
- Stripe webhook is the sync mechanism — webhook downtime means stale plan data (acceptable; webhooks are reliable and we reconcile on portal session creation)

**Impact on stories:**
- US-43.14: implement `StripeProvider` (Checkout + Portal + webhook) — ~6h
- US-43.15: plan tier config + feature gating middleware — ~6h
- US-43.16: billing tab (plan status + redirect button) — ~2h
- US-43.17: Stripe Metered Billing via Epic 12 `BillingExporter` — ~6h

---

### D2: AWS SES for email delivery

**Status:** Confirmed (2026-06-14)
**Source:** User direction

**Context:** Email-based invitations (US-43.2) require an email sending service. The codebase has zero email infrastructure today.

**Decision:** Use AWS SES. Implement an `EmailProvider` interface in `pkg/email/` with `SESProvider` (production) and `NoopProvider` (dev/test, logs to console). Config-driven selection.

**Consequences:**
- New infra dependency: AWS SES (requires verified sender domain, IAM/IRSA credentials)
- `pkg/email/provider.go` — `EmailProvider` interface (Send method)
- `pkg/email/ses_provider.go` — SES implementation using AWS SDK for Go v2
- Config: `email.provider`, `email.ses.region`, `email.ses.fromAddress`
- Email templates needed: org invitation, (future: welcome, billing alerts, suspension notice)
- In dev/test: `NoopProvider` logs email content to console — no AWS dependency
- SES sender domain must be verified in AWS (DKIM/SPF setup — ops task, not code)

**Impact on stories:**
- US-43.2 depends on this — invitation emails sent via SES
- Phase 3 audit alerts (future) would also use this

---

### D3: OIDC only for SSO — defer SAML and SCIM

**Status:** Confirmed (2026-06-14)
**Source:** User direction

**Context:** Enterprise SSO has three protocols: OIDC (modern, used by Google/Microsoft/Azure/Okta), SAML (legacy, still common in enterprise), SCIM (user lifecycle provisioning sync). The original epic proposed all three (~34h combined).

**Decision:** Implement OIDC only (US-43.10). Defer SAML (US-43.11) and SCIM (US-43.12). OIDC covers all modern identity providers including Google Workspace, Microsoft Entra ID (Azure AD), Okta, Auth0, and Keycloak. Auto-provisioning on first OIDC login replaces most SCIM use cases.

**Consequences:**
- Phase 3 scope: US-43.10 (OIDC, 12h) + US-43.13 (org audit, 8h) = 20h (down from 34h)
- SAML-only IdPs cannot be served (rare — most enterprises support OIDC alongside SAML)
- No SCIM means no automated user de-provisioning from IdP (admins manually remove members; or future epic adds SCIM)
- When an enterprise customer requires SAML, it can be added as a follow-up story without redesigning the OIDC path

**Impact on stories:**
- US-43.11 (SAML) — removed from Phase 3 scope
- US-43.12 (SCIM) — removed from Phase 3 scope
- US-43.10 (OIDC) — remains, may include auto-provisioning to cover SCIM's use case

---

### D4: Epic 30 (Unified Credential Model) is complete — no sequencing dependency

**Status:** Confirmed (2026-06-14)
**Source:** Code verification

**Context:** The `stories/README.md` audit (last audited 2026-06-05) marked Epic 30 as "❌ Not Started" and "next priority." This appeared to block Epic 43's credential stories (US-43.5) since Epic 30 rewrites the credential model. The user questioned whether Epic 30 was actually done.

**Finding:** Epic 30 is **complete, merged, and deployed**. The audit is stale:
- PR #39 (`feat: Epic 30 — Unified Credential Model`) merged
- Deployed as Helm revision 159, tag `ts-1780783237`
- Live-validated 2026-06-07 (worklog 0180)
- All 14 stories (US-30.1 through US-30.14) implemented
- `CredentialProvisioner` wired at `workspace_service.go:271-276`
- `decryptBinding` handles all three owner types: user, admin, org (`injection.go:135-170`)
- Epic 11 (Organizations) was built on top of Epic 30's `owner_type='org'` and merged in PR #137
- The audit's "Last audited: 2026-06-05" timestamp predates the same-day implementation (worklogs 0159-0168)

**Decision:** No sequencing constraint. Epic 43 can proceed immediately — the credential model it builds on (`provider_credentials` with `owner_type='org'`) is fully operational. The `stories/README.md` audit should be corrected to mark Epic 30 as "✅ Complete."

**Impact:** Epic 43's US-43.5 (Credentials tab UI) can call existing, tested org credential APIs with no backend work needed.

---

### D5: No worklogs for design phase

**Status:** Confirmed (2026-06-14)
**Source:** User direction

**Context:** The `README-LLM.md` mandates worklogs for every session. The user explicitly directed "no worklogs" for this design exploration session.

**Decision:** Skip worklog for this session. Worklogs will resume when implementation begins.

---

## Confirmed Decisions — Open Questions Resolved

All 20 open questions resolved on 2026-06-14. Detailed context for each is in the [Open Questions](./README.md#open-questions) section of the epic README.

---

### D6: No sub-groups — but workspace visibility is creator-scoped, not org-wide

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q1, modified)

**Context:** The original Q1 proposed deferring sub-groups with flat visibility (all members see all org workspaces). The user chose a different model: defer sub-groups, but **members see only their own workspaces** within the org. Admins see all org workspaces via the control panel.

**Decision:** Defer sub-groups. Change workspace visibility:

- **Members** see only workspaces they created (regardless of whether the workspace is personal or org-attributed)
- **Org admins** see all org workspaces via `/admin/orgs/:slug/workspaces`
- `verifyOwner` changes: org membership alone no longer grants workspace access — only the creator or an org admin can access a given org workspace

**Code change from Epic 11:**
```go
// Epic 11 (current): org membership grants access
access := meta.UserID == userID || orgStore.IsOrgMember(meta.OrgID, userID)

// New: only creator or org admin
access := meta.UserID == userID || orgStore.IsOrgAdmin(meta.OrgID, userID)
```

**Future sub-team extension point:** When sub-teams are added, the visibility rule extends to: creator + group members + admins. The current model (creator + admin) is a natural subset that extends cleanly. Design all related code with this extension in mind — no hardcoding that assumes org-wide visibility.

**Impact:**
- `verifyOwner` in `workspace_service.go:696` must change the org access check from `IsOrgMember` to `IsOrgAdmin`
- `ListWorkspaces` in `database.go:571-584` currently returns BOTH personal and org workspaces via an `OR` clause with a `LEFT JOIN org_memberships`. To implement D6, **remove the OR clause** — the query becomes `WHERE w.user_id = $1 AND w.deleted_at IS NULL`. Users see only their own workspaces (personal + org-attributed that they created).
- `ListOrgWorkspaces` in `pg_org_store.go:508-547` currently has no `user_id` parameter. For member-scoped queries (US-43.6), add a `userID *string` parameter; when non-nil (member request), add `AND w.user_id = $userID`. Admin requests pass nil (see all).
- `GET /orgs/:id/workspaces` returns all org workspaces for admins; members see only their own within the org (filter by `user_id` for member requests)
- Org credential injection is unchanged — all org workspaces still get org credentials regardless of who sees them
- `max_workspaces_per_member` policy counts per-user within org (cleaner than org-wide)

---

### D7: No workspace ownership transfer

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q2)

**Context:** Workspaces are created under an org from the start. Personal workspaces cannot be moved to an org later.

**Decision:** No transfer API. Users create workspaces under the desired context (personal or org) at creation time. If existing work needs to move to an org, the user creates a new org workspace and copies files manually (PVC-mounted filesystem makes this straightforward).

**Impact:** No new code. Current Epic 11 behavior is correct.

---

### D8: Org deletion — soft delete + 30-day retention + full PVC export

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q3)

**Context:** Orgs can be deleted. The question was what happens to workspaces, data, and members.

**Decision:** Three-layer safety model:

1. **Block hard delete if workspaces exist** (already implemented in Epic 11's `SoftDeleteOrg`). Admin must delete each workspace first, or the org cannot be deleted.

2. **Soft delete with 30-day retention.** Soft-deleted orgs (`deleted_at` set) are preserved for 30 days. Platform admin can restore within the window or force-delete after it.

3. **Full PVC export as fast follow (US-43.6b).** Before deletion (or during the retention window), an org admin or platform admin can trigger a bulk export of all workspace data. The export includes the **entire PVC** (all files: workspace files, opencode.db, git repos, `.local` config — everything). Packaged as a tarball with a signed download URL.

**Export implementation:**
- Helper pod mounts each workspace PVC read-only
- Creates a tarball of the PVC contents
- Streams to API → generates time-limited signed download URL
- Also useful beyond org deletion: GDPR right to portability, user-initiated data export
- Scoped as US-43.6b (Phase 1 fast follow)

---

### D9: Multi-org membership — yes; SSO creates separate accounts per email

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q4)

**Context:** Users can belong to multiple orgs. The user raised a concern about account separation: a user's personal account should be isolated from their work account.

**Decision:** Multi-org membership is supported (schema already handles it). SSO auto-provision (D-S1) creates a **separate account per IdP-provided email**. This naturally produces the desired isolation:

- Alice has `alice@gmail.com` (personal, registered manually)
- Alice joins Acme Corp → SSO with Okta returns `alice@acme.com` → auto-creates separate account
- Two accounts, fully isolated. Acme Corp admins manage `alice@acme.com`; cannot see `alice@gmail.com`
- When Alice leaves Acme Corp: `alice@acme.com` deactivated or membership removed. Personal account unaffected.

**Account linking (future feature, NOT in scope):** A future enhancement may allow linking multiple accounts to one identity for unified billing, unified notifications, and quick switching. Not needed for Phase 1 or Phase 3 — SSO-creates-separate-account gives the isolation the user wants out of the box.

**Impact:**
- Org switcher in chat sidebar (users with multiple org memberships) — ~4h frontend
- Workspace creation already supports `orgID` selection — no backend change
- No special SSO email-handling logic needed — auto-provision with the IdP email is the correct behavior

---

### D10: Custom/BYO model endpoints — yes

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q5)

**Decision:** Expose "Custom" provider option in the org credentials UI with a "Base URL" field.

**Code-verified reality:** The `provider_credentials` table does **NOT** have a `base_url` column (`api/migrations/000015_unified_credential_model.up.sql:13-25`). The column does not exist in any migration. Custom endpoint support requires:

- **Migration (000036):** `ALTER TABLE provider_credentials ADD COLUMN base_url TEXT NOT NULL DEFAULT '';`
- **Handler change:** `CreateOrgCredential` / `UpdateOrgCredential` accept `baseURL` in request body
- **Store change:** `pg_secret_store` writes/reads `base_url`
- **Injection change:** `FormatOpenCodeConfig` emits `baseURL` into the opencode provider config when non-empty
- **Frontend:** dropdown option + conditional field in US-43.5

**Impact:** ~4-6h (migration + handler + store + injection + frontend). Not the "~1h UI only" originally estimated.

**Migration:** `000036_provider_credentials_base_url` — `ALTER TABLE provider_credentials ADD COLUMN base_url TEXT NOT NULL DEFAULT '';`

---

### D11: No data residency — future epic

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q6)

**Decision:** Single-region for now. Multi-region data residency is a major infrastructure project tracked as a future epic. Activate when an enterprise contract requires it.

---

### D12: Metered billing for both tiers — usage is required at launch

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q-B1 + Q-B4)

**Context:** The user wants usage metering to prevent power-user abuse. Both individual and org tiers include usage-based charges.

**Decision:**

| Tier | Base | Usage |
|------|------|-------|
| Individual | Low flat fee ($2–5/mo — exact price TBD) | Per-token + per-compute-second charges |
| Org | Per-seat fee + base | Per-token + per-compute-second charges (same rates) |

**Stripe setup:** Subscription (base + seats for orgs) + Stripe Metered Billing (usage). US-43.17 (metered billing) is in the **critical path for launch**, not deferred.

**Metered units:**
- `llm_tokens` (input + output — rates may differ per direction)
- `compute_seconds` (workspace active time)
- NOT `storage_bytes` (too small per-user to bill meaningfully)
- NOT `api_calls` (not a meaningful cost for the user)

**Impact:**
- US-43.17 moves from Phase 4b to Phase 4 critical path
- Epic 12 metering must be validated as billing-grade before launch. Current state (verified against code):
  - **Buffer full → silent drop** (`metering.go:185-194`): when the 4096-slot channel is full, events are dropped with only a Prometheus counter. No DLQ, no retry.
  - **Double failure (batch INSERT + DLQ write both fail) → permanent loss** (`metering.go:536-564`): `dropAll()` increments a counter and events are gone.
  - **Reconciliation exists ONLY for `compute_seconds`** (`metering.go:795-797`): there is NO reconciliation path for `llm_tokens`, `llm_request`, or `api_call`. Dropped token events are gone forever.
- **Required fix for billing-grade token metering:** Add a token reconciliation path. Options (decide during Phase 4 implementation):
  - **Option A — Synchronous token writes:** For billing-critical token events, write directly to DB (bypass async buffer) with a short timeout. Slower (~2ms p99) but durable. Apply only to `llm_tokens` events; keep `api_call` and `storage_bytes` on the async path.
  - **Option B — Token reconciliation from opencode.db:** A periodic job reads `usage` entries from `opencode.db` (on the PVC) and reconciles against `usage_events`. Fills gaps. Requires workspace to be Active or briefly resumed.
  - **Option C — Accepted loss with documentation:** Accept that under extreme load, some token events are lost. Bill based on what was recorded. Add a `metering_accuracy` Prometheus gauge and an alert when drop rate exceeds 0.1%.
- **Recommendation:** Option A (synchronous writes for `llm_tokens`) for Phase 4. The 2ms p99 overhead is acceptable on the SSE callback path (which is already async from the user's perspective). Reconciliation (Option B) as a defense-in-depth backup.
- Risk: metering gaps mean undercounting → revenue loss. The fixes above close this gap.

---

### D13: Trials configurable, default off — 3 days, 3 members, 1 workspace

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q-B2)

**Decision:** Trial periods exist as a configurable feature, disabled by default. When enabled:

| Setting | Default |
|---------|---------|
| `billing.trial.enabled` | `false` |
| `billing.trial.days` | `3` |
| `billing.trial.max_members` | `3` |
| `billing.trial.max_workspaces_per_member` | `1` |

Configured via `instance_settings`. When enabled, Stripe Checkout session includes `subscription_data.trial_period_end`. Trial is constrained: enough to evaluate, not enough to abuse. Card required upfront (Stripe validates during trial).

---

### D14: 7-day payment grace period via Stripe Smart Retries

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q-B3)

**Decision:** When a payment fails:
1. Stripe webhook `invoice.payment_failed` → `subscription_status = 'past_due'` (org stays active)
2. Stripe Smart Retries attempts payment over **7 days** (configured in Stripe dashboard)
3. Stripe handles dunning emails automatically
4. Org portal shows non-blocking "payment issue" banner during grace period
5. If all retries fail → Stripe marks subscription `unpaid` → webhook → `organizations.status = 'suspended'`
6. Payment recovery at any point → `subscription_status = 'active'`, banner cleared

Webhook handling:

| Stripe Event | Action |
|---|---|
| `invoice.payment_failed` | `subscription_status = 'past_due'`; show banner |
| `customer.subscription.updated` (status=unpaid) | `status = 'suspended'` |
| `customer.subscription.deleted` | `status = 'suspended'` |
| `invoice.paid` | `subscription_status = 'active'`; if `status = 'suspended'` then `status = 'active'` (unsuspend); clear banner |
| `checkout.session.completed` | `status = 'active'`; `subscription_status = 'active'`; `plan_id` from subscription |

---

### D15: Policy enforcement — 4 policies shipped, no policy UI

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q-P1 + Q-P2)

**Context:** The user wants minimal enforcement (no policy UI). Policies are configured via API; the UI is deferred.

**Decision:**

**Ship (US-43.7 schema + API, US-43.8 enforcement):**
- `allowed_models` — filter in `ListModels`, reject in model selection
- `allowed_providers` — skip credential injection for disallowed providers
- `max_workspaces_per_member` — count in `CreateWorkspace`
- `max_active_workspaces_per_member` — count active in `CreateWorkspace` + `ResumeWorkspace`

**Defer:**
- `allowed_runtimes` — runtimes are admin-managed already
- `egress_policy` — requires controller NetworkPolicy generation (Phase 2b follow-up)
- `require_mfa` — OIDC providers handle MFA at the IdP layer; most orgs will use their own OIDC (Phase 3)
- `max_session_duration_hours` — requires activity tracking work

**Defer policy UI (US-43.9):** Admins configure policies via API call (`PUT /api/v1/orgs/:id/policies`). No settings panel. A future story adds the UI when there's user demand.

**Enforcement location:** API service for all synchronous checks (model, quota, provider). `PolicyService` injected into handlers, reads from `org_policies` table (Redis-cached, 5-minute TTL).

---

### D16: Policy inheritance — org intersects platform

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q-P3)

**Decision:** Platform policy sets the ceiling (what's allowed on this instance). Org policy narrows within that ceiling. `final_allowed = org_policy ∩ platform_policy`. Org can restrict but not expand. No per-user policy within org — all members share the same org policy. If org policy is absent for a key, platform default applies.

---

### D17: OIDC SSO — auto-provision, domain mapping, group claims, server KEK storage

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q-S1 through Q-S4)

**Decision (four sub-decisions):**

**S1 — Auto-provision:** Users are auto-created on first SSO login using the IdP-provided email. This creates separate accounts per email (see D9) — Acme Corp's `alice@acme.com` is a different account from `alice@gmail.com`. Configurable per org (`org_sso_configs.auto_provision`, default true).

**S1 — DEK bootstrapping (critical design constraint):** SSO login provides an OIDC token, not a password. The existing `UnlockDEK` requires a password to derive the user's KEK. Without a password, the DEK cannot be unlocked, and personal credentials cannot be encrypted/decrypted. Solution:

- On first SSO login, the user is prompted: "Set a password for your account." This password initializes the DEK fresh (via `InitializeUserKeys`) — not wrapping an existing DEK.
- Until the password is set: the user can use org workspaces (org DEK is server-cached from an admin's login, so org credential injection works server-side). Personal credential operations return 400 `"set a password first"`.
- If the user never sets a password: they function as an org-only user. Org workspaces work (server-side injection). Personal credentials are unavailable. This is acceptable for SSO-only users who don't need personal credentials.
- On subsequent SSO logins: the password is already set. The auth flow can optionally skip password entry if the session is established via SSO (the DEK is cached from the SSO session, similar to how org DEKs are cached).

**S2 — Domain mapping with verification:** Org admin claims email domains (e.g., `acme.com`). Platform verifies via DNS TXT record. Verified domains enable auto-routing on the login page (user enters email → if domain matches claimed org → show "Sign in with [Org]" button). Unverified domains: SSO works via slug-based URL only. Can ship slug-based routing first, add domain detection as Phase 3b.

**S3 — Group claim mapping:** OIDC `groups` claim maps to org roles via configurable JSON rules (`org_sso_configs.group_role_mapping`). e.g., `{"admins": "admin", "developers": "member", "*": "member"}`. Role applied on every login — if IdP removes admin group, next login demotes the user.

**S4 — Server KEK storage:** OIDC client secret encrypted with `LLMSAFESPACE_MASTER_SECRET`-derived server KEK (same as `owner_type='admin'` credentials). Always decryptable — no org DEK cache dependency. SSO callback always works.

---

### D18: Platform admin — reuse `users.role='admin'`

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q-O1)

**Decision:** No new role. Platform admin = `users.role='admin'`, gated by existing `AdminGuard` middleware. Org admins are per-org (in `org_memberships`) — no collision.

---

### D19: Suspension — both org-level and user-level

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q-O2)

**Decision:** Support both suspension levels:

**Org-level suspension:**
- `organizations.status = 'suspended'` → all members lose access
- Triggered by: non-payment (after grace period), abuse, platform policy violation, enterprise offboarding
- `OrgMemberGuard` / `OrgAdminGuard` check `status = 'active'`

**User-level suspension:**
- `users.status = 'suspended'` → user blocked across ALL contexts (all orgs + personal)
- Triggered by: individual abuse, security incident (compromised account), spam
- Auth middleware checks `users.status = 'active'` on every authenticated request
- New migration: `ALTER TABLE users ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended'))`
- Platform admin UI: list users, suspend/unsuspend

**Last-admin deadlock prevention:** When suspending a user, check if they are the last admin of any org (`SELECT COUNT(*) FROM org_memberships WHERE org_id IN (their orgs) AND role = 'admin' AND pending_key_wrap = false`). If they are the last active admin of any org:
- Block the suspension: return 409 `"Cannot suspend — this user is the last admin of org [name]. Promote another member first."`
- This prevents the scenario where the last admin is suspended and no one can manage the org (promote members, change policies, manage billing).
- Exception: platform admin can force-suspend (`?force=true`) in security emergencies — the org becomes unmanageable until the user is unsuspended or another admin is promoted via direct DB intervention.

**Impact on org workspaces:** If a suspended user created workspaces under an org, those workspaces remain (they're org property). The user just can't log in. Org admins can still access the user's org workspaces (per D6 — admins see all org workspaces).

---

### D20: Org suspension behavior — hard operational, data preserved

**Status:** Confirmed (2026-06-14)
**Source:** User direction (Q-O3)

**Decision:** When an org is suspended, all active workspace pods are terminated (Active → Suspended phase). PVCs are preserved. This is "hard operationally, soft on data."

| Resource | On Suspension | On Unsuspension |
|----------|---------------|-----------------|
| Workspace pods | **All terminated** — every org workspace Active → Suspended | User/admin manually resumes each workspace (~3s each) |
| PVCs | **Preserved** — all data retained (eventually moved to S3 in a future epic) | Intact |
| Member access | 403 from org routes | Restored |
| New workspace creation | Blocked | Allowed |
| Compute metering | **Stops** (no active pods) | Resumes when workspaces resumed |
| Token metering | **Stops** (no active pods = no LLM calls) | Resumes |
| Storage metering | **Continues** — PVCs still allocated | — |
| Pending invitations | Frozen (can't accept) | Can accept |

**Implementation approach:** Controller-query based (not per-workspace labels). The controller's workspace reconciler, on each reconcile cycle, queries the API for the org's status. If the org is suspended, the controller suspends the workspace (Active → Suspended phase). This avoids O(n) K8s PATCH calls from the API service.

```
Controller reconciler (per workspace):
  1. Read workspace CRD → get org_id from spec.owner.orgID
  2. If org_id is set: query API GET /internal/orgs/:orgID/status → { status: "active"|"suspended" }
     (cached in controller's in-memory org-status cache, refreshed every 30s)
  3. If org status = 'suspended' AND workspace phase = 'Active':
     → Transition to Suspended phase (delete pod, retain PVC)
  4. If org status = 'active' AND workspace phase = 'Suspended' AND was suspended by org-suspension:
     → Do NOT auto-resume (admin/user manually resumes)
```

**Why not per-workspace labels:** Labeling every workspace CRD requires O(n) K8s PATCH calls for n workspaces. During the labeling window, some workspaces are labeled (suspended) and others aren't (still running) — non-atomic. The controller-query approach is O(1) per reconcile cycle (cache hit) and atomic (all workspaces see the same org status on their next reconcile).

**New internal API endpoint:** `GET /api/v1/internal/orgs/:orgID/status` — returns `{ status: "active"|"suspended" }`. Unauthenticated (internal cluster network only) or authenticated via service account. Cached in controller memory with 30s TTL.

**PVC-to-S3 migration:** Future epic. For now, suspended org PVCs remain on Longhorn storage. Storage metering continues during suspension (data is still being held).

---

## Migration Numbering

Current highest migration: `000029` (organizations). Next available: `000030`.

| Number | File | Phase | Creates |
|--------|------|-------|---------|
| 000030 | `000030_org_product_columns` | Phase 1 | `organizations.status`, `organizations.plan_id`, `organizations.subscription_status`; `stripe_events` table (webhook idempotency); case-insensitive slug index `idx_orgs_slug_lower` |
| 000031 | `000031_org_invitations` | Phase 1 | `org_invitations` table (with `declined_at`, `bounce_type`, `bounced_at` columns) |
| 000032 | `000032_billing_accounts_check` | Phase 1 | `ALTER TABLE billing_accounts ADD CONSTRAINT billing_accounts_owner_type_chk CHECK (owner_type IN ('user', 'org'))` — adds missing CHECK constraint |
| 000033 | `000033_user_status` | Phase 5 | `users.status` column (for user-level suspension, D19) |
| 000034 | `000034_org_policies` | Phase 2 | `org_policies` table |
| 000035 | `000035_org_sso_configs` | Phase 3 | `org_sso_configs` table |
| 000036 | `000036_audit_log_org_scope` | Phase 3 | `audit_log.org_id` column, `'org'` domain value |
| 000037 | `000037_provider_credentials_base_url` | Phase 1 (US-43.5) | `ALTER TABLE provider_credentials ADD COLUMN base_url TEXT NOT NULL DEFAULT ''` (D10) |

**Notes:**
- Migration 030 includes `stripe_events` for webhook idempotency (FA-4 fix) and the case-insensitive slug index (FA-3 fix) alongside the org product columns.
- Migration 032 fixes the missing `billing_accounts.owner_type` CHECK constraint (SI-8).
- Migration 037 adds the `base_url` column that D10 needs but doesn't exist today (FA-1).

Each migration must be added to **both** `api/migrations/` and `charts/llmsafespace/migrations/` with `.up.sql` and `.down.sql` files. Repolint enforces byte-for-byte equality.
