# 0032: Open Questions — Analysis After User Feedback

**Date:** 2026-06-15
**Status:** Design analysis
**Companion to:** `0031_2026-06-15_org-access-control-portal-architecture.md`

---

## Settled by user feedback

| Issue | Resolution |
|-------|-----------|
| Org deletion | Org admins can self-delete (soft delete, existing `SoftDeleteOrg`) |
| Workspace attribution | **Always org-attributed when in an org.** Users cannot create personal workspaces while part of an org. |
| Pre-existing personal workspaces on join | **Migrate to org-attributed** on invitation acceptance (automatic) |
| Read-only workspace view in portal | Defer to later (UI-level read-only is sufficient for now) |
| Departed user workspace access | Membership-gated creator access (creator must be current org member) |
| "Can't leave orgs" | No self-removal. Only admins can remove members (offboarding). |
| Existing orgs | None exist — not a production system yet. Issue is moot. |
| User search endpoint | **No.** Active account enumeration is a security leak vector. |
| Single vs multi-org | Single-org (schema-level enforcement) |

---

## Workspace attribution — always org-attributed

The user clarified: "when a user is part of an org, their workspaces should ALWAYS be attributed to the org, they shouldn't be able to create personal workspaces while part of an org."

Pre-existing personal workspaces migrate to org-attributed on join.

### What this means

**For users in an org:**
- All workspaces they create get `org_id` set automatically (no personal workspaces)
- The sidebar's create-workspace button passes the user's org ID implicitly
- Backend rejects `CreateWorkspace` with null `org_id` for org members

**For users not in an org:**
- All workspaces are personal (`org_id` is null)
- Backend rejects `CreateWorkspace` with a non-null `org_id`

**On joining an org (invitation acceptance):**
- All of the user's existing personal workspaces (`org_id IS NULL`) are migrated: `org_id` set to the joining org's ID
- This is automatic — no user action required
- The user retains ownership (`user_id` unchanged) — they just gain org attribution
- Org admins gain read visibility to these workspaces

**On leaving an org (admin-initiated removal):**
- The user's org-attributed workspaces stay with the org (`org_id` unchanged)
- The user loses access via membership-gated check (they're no longer a member)
- Workspaces become "frozen" until workspace transfer is built

### Backend enforcement

`CreateWorkspace` validation:
```go
// Determine the user's org membership (single-org model)
userOrgID := getUserOrg(ctx, userID) // returns org ID or ""

if userOrgID != "" {
    // User is in an org — workspace MUST be org-attributed
    if req.OrgID == nil || *req.OrgID == "" {
        // Auto-set rather than reject — the user has no choice
        req.OrgID = &userOrgID
    }
    if *req.OrgID != userOrgID {
        return error("cannot specify a different org")
    }
} else {
    // User is not in an org — workspace must be personal
    if req.OrgID != nil && *req.OrgID != "" {
        return error("user is not a member of any org")
    }
}
```

### Migration on join

In the invitation acceptance handler (`invitations.go:Accept`), after creating the membership:

```go
// Migrate personal workspaces to org-attributed
if err := store.MigrateWorkspacesToOrg(ctx, userID, orgID); err != nil {
    // Non-fatal — log and continue. Workspaces remain personal (access works,
    // just not org-attributed). Admin can trigger re-attribution manually.
    logger.Warn("failed to migrate personal workspaces to org", "userID", userID, "orgID", orgID, "error", err)
}
```

```sql
-- MigrateWorkspacesToOrg
UPDATE workspaces
SET org_id = $2
WHERE user_id = $1 AND org_id IS NULL
```

This is a single UPDATE — atomic, idempotent. Existing personal workspaces become org-attributed. The user retains `user_id` ownership.

---

## Invitation / member onboarding — the core question

### The user's requirements

- No user search (account enumeration risk)
- Admins submit bulk emails
- Emails sent to all (rate-limited, batched to avoid SES throttling/blacklisting)
- LDAP/POSIX import as future input sources (same output: invitations)
- Need to handle: what happens when an invited email already has an account?

### Mental model clarification: accounts vs memberships

The user asked "Does it become an org account, or does admin get an error?" This implies a model where accounts can be "owned" by orgs. Two fundamentally different models exist:

**Model A — Independent accounts (GitHub/Slack pattern):**
- Users own their accounts. Accounts exist independently of orgs.
- Orgs have memberships (a row linking user → org with a role).
- Adding a user to an org creates a membership, not an account transformation.
- The account's personal workspaces, credentials, settings are unaffected.
- User can leave the org; their account persists.

**Model B — Org-managed accounts (Google Workspace / Microsoft 365 pattern):**
- Orgs own accounts. The admin creates accounts, resets passwords, suspends.
- Users have no account outside the org.
- Adding a user means creating their account under the org's domain.
- User cannot leave; their account is deleted when removed from the org.

**The current system uses Model A.** Accounts are independent. `org_memberships` links users to orgs. The invitation system sends emails; recipients accept; a membership row is created. The account is never "transformed."

This is worth confirming with the user, because the question "does it become an org account" only makes sense in Model B. In Model A, the answer is: **nothing happens to the account. A membership is created. The account was always the user's.**

### Assuming Model A (current): what happens when admin invites an existing email?

The existing invitation system already handles this correctly. Let me trace the flow:

1. Admin submits emails via `POST /orgs/:id/invitations { emails: [...], role: "member" }`
2. For each email: create invitation row (email, org, role, token, expiry) → send email
3. The invitation does NOT resolve the email to a user ID. It stores the email.
4. Recipient clicks the link → sees org name, inviter, role → must authenticate → accepts
5. On accept (`POST /invitations/:token/accept`):
   - The authenticated user's ID is used
   - System checks if this user is already a member → 409 if so
   - Creates membership row
6. If the email had no account: recipient registers first, then accepts

**So the existing system already does the right thing:** existing users go through the same invitation flow as new users. They must authenticate and accept. The admin doesn't need to know who has accounts. No account enumeration. No error for existing accounts.

### But: should we even send invitations to existing users?

The existing flow sends an email to everyone. For existing users, this is slightly redundant — they could be auto-added since they've already authenticated to the platform. The options:

#### Option 1: Always invite (current behavior, no change)

All emails get invitations. Existing users receive an email, click, log in, accept. New users receive an email, click, register, accept.

| Pros | Cons |
|------|------|
| Uniform flow — one code path | Existing users get an email they don't strictly need |
| Consent required for everyone | Slight friction for existing users |
| No account enumeration (admin never learns who has an account) | |
| Reuses existing tested infrastructure | |
| Works for LDAP/POSIX import (same output) | |

#### Option 2: Auto-add existing users, invite new ones

System resolves each email to a user ID at invitation-create time. Existing users are added as members immediately (with an in-app notification). New emails get invitations.

| Pros | Cons |
|------|------|
| Faster for existing users (no email round-trip) | **Account enumeration**: admin can infer which emails have accounts by observing which were auto-added vs invited |
| Less email noise | No consent before membership is created |
| | Two code paths (auto-add vs invite) |
| | Resolves emails server-side — the resolution itself is the leak (even if the API doesn't return it, timing differences reveal it) |

**This directly violates the user's "no account enumeration" requirement.** Even if the API response is identical, the behavioral difference (immediate membership vs email sent) is observable. Reject.

#### Option 3: Send invitations to all, but existing users get in-app notification too

All emails get invitations (uniform). Additionally, existing users see an in-app banner on next login ("You have a pending invitation from Org X"). This speeds up acceptance without leaking account status (the email is sent either way).

| Pros | Cons |
|------|------|
| No enumeration (email sent to all) | Slightly more complex (in-app notification system) |
| Faster for existing users (see it on login) | Notification infrastructure doesn't exist yet |
| Consent still required | |
| Reuses invitation flow | |

#### Option 4: Invite all, but link invitation to existing account at acceptance time only

Same as Option 1, but with a UX improvement: when an existing user logs in, the system checks for pending invitations matching their email and shows them prominently. No change to the invitation flow itself — just better in-app discovery.

| Pros | Cons |
|------|------|
| No enumeration | Requires a "pending invitations for my email" query on login |
| Better UX for existing users | Minor additional query per login |
| No flow change | |
| Consent required | |

### Analysis

**Option 1 is already correct and deployed.** The existing invitation system handles existing and new users uniformly, with consent, without enumeration. The only improvement worth making is Option 4's in-app discovery (show pending invitations on login/dashboard).

**The answer to "does it become an org account":** No. Accounts are independent (Model A). The user always owned their account. Accepting an invitation creates a membership row. The account is unaffected. This is the correct behavior and already implemented.

**The answer to "does admin get an error":** No. The admin submits emails. All get invitations. The system never reveals which emails have existing accounts.

### Bulk email infrastructure

The existing `Create` handler sends emails synchronously in a loop (one SES call per email). For bulk sends this needs:

1. **Rate limiting**: SES has per-second and per-24h limits. Need a configurable rate (e.g., 10 emails/second).
2. **Batching**: Process the email list in batches with delays between batches.
3. **Queue**: Move email sending to a background queue (worker goroutine or Redis queue) so the API returns immediately after creating invitation rows.
4. **Bounce handling**: Already exists (US-43.2 bounce webhook). Bounced emails mark the invitation.
5. **Deduplication**: Prevent the same email from being invited twice (unique constraint on `(org_id, email) WHERE accepted_at IS NULL AND declined_at IS NULL` — already exists in the schema).

This is infrastructure work that applies to the existing invitation flow regardless of the account-existence question.

### LDAP/POSIX import

Both produce a list of users to invite. The output is the same: invitations. The input parsing differs:

- **LDAP**: Connect to directory, query for users, extract `mail` attribute. Requires LDAP client config (server URL, bind credentials, search base, filter). Complex — separate epic.
- **POSIX**: Parse `/etc/passwd` or similar. POSIX users have usernames but typically no email. Would need email mapping. Less useful for a cloud-native product.

**Recommendation:** Defer LDAP/POSIX import to a future epic. The bulk email invitation flow covers the immediate need. LDAP import is a Phase 5+ enterprise feature that builds on the same invitation output.

---

## Updated recommendations for all original issues

| Issue | Recommendation | Rationale |
|-------|---------------|-----------|
| 1. DEK bootstrap | **Defer decision.** Use entitlement model for now (owner self-creates, no cross-user key). When OIDC or Stripe auto-provisioning lands, implement server KEK bootstrap. The "eliminate org DEK entirely" option (Option 9 in prior analysis) deserves a separate discussion — it's a bigger architectural question. | Entitlement model eliminates the bootstrap problem without crypto changes. No urgency to decide on server KEK until auto-provisioning is needed. |
| 2. User search | **None needed.** Invitations use email only. No user lookup, no enumeration. | The invitation system already works this way. |
| 3. Org deletion | **Self-delete (soft), UI in portal.** Keep `DELETE /api/v1/orgs/:id` in orgAdminGroup. Add a Danger Zone section in the portal Overview tab with type-to-confirm. | User confirmed: org admins can self-delete (soft). |
| 4. Single-org | **Enforce at schema level.** Partial unique index on `org_memberships(user_id)`. | User confirmed single-org. No existing orgs, so no migration conflict. |
| 5. Existing orgs | **Moot.** No orgs exist. | User confirmed. |
| 6. Workspace attribution | **Org-attributed by default for org members.** Keep Workspaces tab in portal (read-only list). Reverse the 0031 design that removed it. | User confirmed: org-attributed, read-only visibility for admins, owned by users. |
| 7. Personal vs org credentials | **Both injected.** Personal credentials continue to work alongside org credentials. Policy engine intersects org ∩ platform (not org ∩ personal). | No change needed — existing behavior is correct. |
| 8. Platform admin context | **Separate routes.** `/orgs/:slug` (org admin context) vs `/platform/*` (platform admin context, Phase 5). The sidebar org button only appears for org admins. Platform admins who are also org admins see both the org button and the admin settings. | Route separation makes the context explicit. |

---

## Org DEK — tracing the actual credential injection flow

The user asked: "what happens if the org admin wants to set up an llm provider for all org members? how is that done?"

Tracing the code reveals a **latent production bug** in the current org DEK model.

### The flow

**Admin creates org credential** (`org_credentials.go:59`):
1. Admin logs in → password derives KEK → unwraps org DEK → caches it (24h TTL)
2. Admin enters provider + API key in portal
3. `GetOrgDEK(orgID)` reads from cache → encrypts credential → stores ciphertext
4. If cache miss → 409: "org DEK not available — log out and back in"

**Member's workspace injects the credential** (`injection.go:157`):
1. Member opens workspace → `PrepareSecretsForInjection` → `decryptBinding`
2. For `owner_type='org'` → `GetOrgDEK(orgID)` reads from cache
3. If cache hit → decrypts → injects
4. If cache miss → **silently fails** — credential not injected

### The bug

`UnlockAllOrgDEKs` (`org_key_service.go:109`) only runs for org **admins** (fetches `GetOrgKeyMembersForUser` = key members = admins). Regular members don't have key member records. So:

- Org DEK is cached only when an admin logs in
- Cache TTL = 24h (same as token duration)
- If no admin logs in for 24h → org DEK expires from cache → **all org credentials silently stop injecting into member workspaces**
- No error, no alert, no fallback — members just lose their org LLM keys

This means "set up a credential once, all members get it" only works if an admin logs in daily. The current tests don't catch this because they mock the cache.

### Options for fixing

#### Option A: Server KEK for org credentials (same as admin credentials)

Org credentials encrypted with `deriveServerKey("org-credentials")` instead of the org DEK. Always available — no cache dependency, no admin login requirement.

What this eliminates:
- `OrgKeyService.GetOrgDEK` cache dependency for credential injection
- The 24h cache expiry bug
- The `pendingKeyWrap` flow (no per-admin key wrapping needed for credential encryption)
- The `accept-key` flow
- The entire `org_key_members` table usage for credential access
- The key-setup banner in the portal

What this keeps:
- The org DEK for **user secrets** (zero-knowledge encrypted user secrets at the org level) if that's ever needed
- The `OrgKeyService` for org-key-member management (admin-to-admin key sharing)

What this changes:
- `OrgCredentialsHandler.Create` uses `deriveServerKey` instead of `GetOrgDEK`
- `decryptBinding` for `owner_type='org'` uses server KEK instead of org DEK
- Org credentials are always injectable — the "set up once, all members get it" use case works reliably

**Blast radius:** Server KEK compromise exposes all org credentials. But server KEK compromise already exposes all admin credentials (same key derivation from `LLMSAFESPACE_MASTER_SECRET`). No new blast radius.

#### Option B: Server KEK bootstrap copy of org DEK

Keep the org DEK for credential encryption. Add a server-wrapped copy as a fallback when the cache misses. `GetOrgDEK` tries cache first, falls back to server-wrapped copy.

What this keeps:
- Zero-knowledge property (org DEK is the encryption key, server KEK is only a recovery path)
- All existing code paths (just adds a fallback)

What this adds:
- `org_key_bootstrap` table (one row per org: server-wrapped DEK)
- `GetOrgDEK` gains a fallback path
- Audit logging for bootstrap-path unwraps

**Trade-off:** More complex than Option A for no additional security benefit. The server KEK can decrypt all org credentials either way (via the bootstrap copy → DEK → credential, or directly if we go with Option A). The intermediate DEK layer adds complexity without adding a security boundary the server can't cross.

#### Option C: Refresh org DEK on member login too

Cache the org DEK when ANY org member logs in, not just admins. Members would need their own wrapped copy of the org DEK.

What this requires:
- All members get a wrapped org DEK (not just admins) — `org_key_members` for everyone
- The `pendingKeyWrap` flow extends to all members (not just admins)
- Every member must complete key setup before org credentials work for them

**Trade-off:** Extends the key-setup burden to all members. Operationally heavy. Doesn't solve the "no one has logged in for 24h" problem (just reduces its probability).

#### Option D: Eliminate org DEK entirely

Same as Option A but also eliminates the `OrgKeyService` for user-secret encryption. All org-level encryption uses server KEK. No per-admin key wrapping, no `pendingKeyWrap`, no `accept-key`.

This is the most radical simplification. It removes:
- `pkg/secrets/org_key_service.go` (entire file)
- `pkg/secrets/org_aware_key_service.go` (entire file)
- `org_key_members` table
- `pendingKeyWrap` column
- `accept-key` / `rotate-key` endpoints
- All key-setup UI

**Trade-off:** Org user secrets (if any exist) lose zero-knowledge encryption. But checking the code: user secrets use the **user** DEK, not the org DEK. The org DEK is only used for **org credentials** (LLM provider keys). There are no "org user secrets" — secrets are per-user. So eliminating the org DEK has no impact on user secret encryption.

### Recommendation

**Option D (eliminate org DEK entirely).**

Rationale:
1. **The org DEK doesn't work.** The primary use case (org credentials for all members) is broken due to cache dependency. Option A fixes this but keeps unnecessary infrastructure.
2. **The org DEK only protects org credentials** (LLM provider keys). User secrets use the user DEK. There are no org-level user secrets.
3. **Admin credentials already use server KEK.** The security precedent is set. Org credentials following the same model is consistent.
4. **Eliminates massive complexity.** The `OrgKeyService`, `OrgAwareKeyService`, `org_key_members` table, `pendingKeyWrap` column, `accept-key` flow, key-setup banner — all deleted. This is ~1000+ lines of code and an entire class of bugs.
5. **The "zero-knowledge" property was already broken.** OIDC client secrets are planned for server KEK (D17 S4). Admin credentials use server KEK. The org DEK was the last holdout, and it doesn't even work.

If the user wants to preserve zero-knowledge for org credentials specifically, **Option A** (server KEK for org credentials, keep OrgKeyService for future use) is the fallback. But Option D is the cleaner choice.

---

## "Can't leave orgs" — enterprise account model

The user said: "once an account is part of an org, we should not allow accounts to leave orgs. Think of an enterprise customer, they wouldn't want someone taking their work account with them."

### What this means in the current system

Today `RemoveOrgMember` (`pg_org_store.go`) deletes the membership row but does NOT touch `workspaces`. The offboarded user's `user_id` stays on their org-attributed workspaces.

`verifyOwner` (`workspace_service.go:738`) grants access if `meta.UserID == userID` — **the creator always has access, regardless of org membership.** So today, an offboarded user can still access their org workspaces. This contradicts the enterprise requirement.

### Two coupled questions

1. **Can the departed user still access their org workspaces?** (access control)
2. **Who owns/manages them after departure?** (ownership)

These are coupled — the answer to #2 depends on #1.

### Options

#### Option 1: Account suspension on offboarding

When an admin removes a member, the member's **account is suspended** (not just the membership). They can't log in. Their org workspaces become inaccessible to them. Org admins retain read access via `IsOrgAdmin`.

- Who owns them? The `user_id` field still points to the suspended user. Nobody actively "owns" them — they're frozen artifacts.
- Org admins can view them in the portal (read-only list).
- A future workspace transfer feature lets admins reassign them.

| Pros | Cons |
|------|------|
| Matches enterprise offboarding (account locked, data retained) | Requires `users.status` column (Phase 5, US-43.19 — not yet built) |
| Departed user fully locked out | Workspaces are frozen until transfer is built |
| Simple — one state change on removal | "Who owns it?" is unanswered — it's in limbo |
| No `user_id` change needed | |

**Blocked by:** Phase 5 user suspension infrastructure. Can't ship without `users.status`.

#### Option 2: Membership-gated creator access

Change `verifyOwner`: if the workspace has an `org_id`, the creator must ALSO be a current member of that org. Offboarded users lose access automatically (no account suspension needed).

- Who owns them? `user_id` unchanged. The workspace is accessible by org admins only.
- No new schema columns needed.
- Offboarding = remove membership → creator check fails for org workspaces → locked out.

| Pros | Cons |
|------|------|
| No new infrastructure needed — works today | Departed user can still log in to the platform (just can't access org workspaces) |
| Automatic — no explicit suspension step | Personal workspaces still accessible to the departed user |
| `user_id` stays accurate (historical record of who created it) | "Who owns it?" still unanswered — admin can view but can't manage (suspend/delete/modify) without a transfer path |
| Simple one-line check change in `verifyOwner` | |

**Ship-blocking issue:** Org admins can view but can't manage (suspend, delete, create sessions in) the departed user's workspaces. `verifyOwner` grants access, but workspace management actions (delete, suspend) may require owner-level permissions that admins don't have.

#### Option 3: Ownership transfers to removing admin

When admin A removes member B, B's org workspaces get `user_id = A`. Admin A becomes the owner.

| Pros | Cons |
|------|------|
| Clear ownership — someone can manage the workspaces | Admin A may not want or need B's workspaces |
| No frozen artifacts | Which admin? If multiple admins, whoever clicks "remove" inherits everything |
| Simple UPDATE on removal | Ownership history lost (no record that B created them) |
| | Could result in one admin accumulating hundreds of workspaces |

#### Option 4: Ownership becomes "unassigned" (nullable user_id)

`workspaces.user_id` becomes nullable. When a member is offboarded, `user_id` is set to NULL. Org admins can manage NULL-owned workspaces via `IsOrgAdmin` check.

| Pros | Cons |
|------|------|
| Clean — no fake owner | `user_id` nullable is a significant schema change — touched by many code paths |
| Any org admin can manage | Loses historical ownership record |
| Matches "the org owns it, not a person" mental model | Personal workspaces still need non-null `user_id` — inconsistent |
| | Every query, every handler, every test that assumes `user_id` is set must handle NULL |

**Complexity:** High. The `user_id` field is referenced throughout the codebase. Making it nullable has wide blast radius.

#### Option 5: Explicit reassignment during offboarding (GitHub model)

When admin removes a member, the UI prompts: "Reassign X's workspaces to:" with a member picker. Each workspace is transferred to the selected member.

| Pros | Cons |
|------|------|
| Most correct — intentional handoff | Requires workspace transfer feature (D7 deferred this as "no transfer") |
| No ambiguity about ownership | Offboarding becomes a multi-step workflow |
| Matches enterprise norms (GitHub, GitLab) | Complex UI + API |
| | If no other member is available (small org), what happens? |

**Conflict:** D7 explicitly deferred workspace transfer. This option requires it.

#### Option 6: Ghost user / departed-users bucket

Create a synthetic "departed users" account. Offboarded members' workspaces get `user_id = <ghost>`. Org admins access via `IsOrgAdmin`. Portal groups them under "Departed Members' Workspaces."

| Pros | Cons |
|------|------|
| Preserves `user_id` non-null invariant | Synthetic user is a hack |
| Workspaces grouped and visible in portal | Loss of original creator attribution (unless we add a `created_by` column) |
| Admins can manage them | Ghost user accumulates all departed workspaces |
| Matches GitLab's ghost user pattern | New concept to explain to users |

#### Option 7: Add `created_by` column, transfer `user_id` to org admin

Keep `user_id` as "current owner" (mutable). Add `created_by` as "original creator" (immutable). On offboarding, `user_id` transfers to the removing admin. `created_by` preserves history.

| Pros | Cons |
|------|------|
| Preserves history (`created_by`) | New column, new concept |
| Clear current ownership (`user_id`) | Still has the "which admin inherits?" problem |
| No nullable fields | Migration + all reads/writes that touch `user_id` semantics |
| | Dual concepts (owner vs creator) add cognitive load |

### Analysis

The right answer depends on what "ownership" means in this context:

- **View access:** Org admins already have this via `IsOrgAdmin` in `verifyOwner`. No change needed.
- **Management access (suspend, delete, modify):** This is the gap. Today only the `user_id` owner can do these.
- **Historical record (who created it):** Currently `user_id` serves double duty (current owner + historical creator).

The cleanest separation is **Option 7** (add `created_by`, make `user_id` the current manager), but it's the most complex.

The simplest that works is **Option 2** (membership-gated access) for the immediate term, with **Option 5** (explicit reassignment) when workspace transfer is built.

### Recommended: Option 2 now, Option 5 later

**Immediate (this design):**
- Change `verifyOwner`: for org-attributed workspaces, creator access requires active org membership
- Offboarded users lose access to org workspaces automatically
- Org admins retain read access (via `IsOrgAdmin`)
- The workspace is "frozen" — no one can manage it (suspend/delete) until transfer is built
- Add a `departed_owner` boolean or check (user_id is set but user is not a member) so the portal can display these workspaces under a "Departed Members" section

**When workspace transfer lands (future):**
- Admin can reassign frozen workspaces to an active member
- The `user_id` updates to the new owner
- Full management access restored

**Why not Option 1 (account suspension):** Blocked by Phase 5 (`users.status` doesn't exist). Option 2 ships today with a one-line change.

**Why not Option 3 (auto-transfer to removing admin):** Wrong abstraction. The removing admin may be an HR person, not the right technical inheritor. Auto-assignment without consent is worse than frozen.

**Why not Option 4 (nullable user_id):** Too wide a blast radius for the immediate need. The `user_id` field is referenced everywhere.

### The membership-gated access check

```go
// verifyOwner — proposed change for org workspaces
if meta.UserID == userID {
    // Creator access: for org workspaces, require active membership
    if meta.OrgID != nil && *meta.OrgID != "" && s.orgStore != nil {
        isMember, err := s.orgStore.IsOrgMember(ctx, *meta.OrgID, userID)
        if err != nil {
            return fmt.Errorf("check org membership: %w", err)
        }
        if !isMember {
            // Creator is no longer in the org — deny access to org workspace
            return apierrors.NewForbiddenError("workspace access denied", ...)
        }
    }
    return nil
}
```

This is a **one-check addition** to the existing creator branch. For personal workspaces (no org_id), creator access is unchanged. For org workspaces, the creator must still be a member.

**Edge case:** What if the user is removed and re-invited later? Their `user_id` is still on the workspaces. When they rejoin (new membership row), access is restored automatically. The workspaces weren't transferred — they were frozen and thawed.

---

### Summary of all settled items

| Issue | Status | Resolution |
|-------|--------|-----------|
| Org deletion | Settled | Self-delete (soft), Danger Zone in portal |
| Workspace attribution | Settled | Always org-attributed for org members. No personal workspaces while in an org. |
| Personal workspace migration on join | Settled | Migrate to org-attributed on invitation acceptance (automatic) |
| Read-only workspace view in portal | Settled | Defer API enforcement to later (UI-level sufficient for now) |
| User search | Settled | None — invitations use email only |
| Existing orgs | Settled | None exist |
| Single-org | Settled | Enforce at schema level |
| "Can't leave orgs" | Settled | No self-removal. Admin-only offboarding. |
| Departed user workspace access | Settled | Membership-gated creator access (creator must be current org member) |
| Workspace ownership after departure | Deferred | Frozen until workspace transfer is built (future) |
| Org DEK | Settled | Eliminate entirely — server KEK for org credentials (D7 in 0031) |
| Bulk email infrastructure | Settled | Current synchronous loop sufficient. Fire-and-forget goroutine when batch >50. Redis queue at enterprise scale. (D10 in 0031) |

**All items resolved.** No open questions remain. See `0031_2026-06-15_org-access-control-portal-architecture.md` for the complete design.
