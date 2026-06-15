# 0033: Design Review — Internal Consistency & Correctness Pass

**Date:** 2026-06-15
**Status:** Review of 0031/0032
**Purpose:** Verify internal consistency, consistency with existing code, right problem/abstraction/complexity

---

## Findings

### F1 (CRITICAL): D7 (entitlement model) is unnecessary complexity given D8 (eliminate org DEK)

**The problem:** The entitlement model was invented to solve the DEK bootstrap problem — if the owner creates the org themselves, their password wraps the DEK directly, no cross-user key transfer needed. But D8 eliminates the org DEK entirely. With no DEK, there's no key to bootstrap. The entitlement model solves a problem that no longer exists.

**What the entitlement model adds:**
- New `org_entitlements` table
- Two new endpoints (`POST /admin/org-entitlements`, `GET /users/me/entitlement`)
- Two-step flow (admin approves → owner creates)
- "Create your organisation" UI prompt
- Entitlement consumption logic
- Story 2 (4h) + Story 8 (3h) = 7h of work

**What direct admin creation looks like instead:**
1. Admin enters owner email + org name + plan
2. `POST /api/v1/orgs { name, slug, ownerEmail, planId }` (admin-only)
3. Backend resolves email → user ID (one query, not a search endpoint). If not found → 404.
4. Org created. Owner added as admin.
5. Owner logs in, sees org button. Done.

No new table. No two-step flow. No UI prompt. One API call. The owner doesn't need to do anything — their org is just there.

**The contradiction in my design:** D1 says "platform admins only" can create orgs. D7 says non-admins with entitlements can create orgs. These contradict. D7 undermines D1.

**Recommendation:** Drop the entitlement model entirely. Direct admin creation with email resolution. Remove D7, simplify D1, delete Stories 2 and 8 and replace with one simpler story.

---

### F2 (BUG): Password requirement at org creation is vestigial

**The problem:** `CreateOrgRequest.Password` is `binding:"required"`. It was needed to derive the KEK that wrapped the org DEK. D8 eliminates the org DEK. With no DEK, the password serves no purpose at org creation.

My Flow 1 step 6 says "Enters org name, slug, their password" — but the password does nothing now. The `orgKeySvc.WrapOrgDEKForNewAdmin` call that consumed it is being deleted.

**Impact:** If we keep the password field, users enter a password that's used for nothing. If we remove it, the API contract changes.

**Recommendation:** Remove `Password` from `CreateOrgRequest`. Update the handler to not derive a KEK. The admin creating the org provides name, slug, ownerEmail, planId — nothing else.

---

### F3 (BUG): Existing org credentials are encrypted with the org DEK — they can't be decrypted with server KEK without migration

**The problem:** D8 says org credentials move to server KEK. But existing org credentials in the DB are encrypted with the org DEK (via `OrgCredentialsHandler.Create` → `secrets.EncryptSecret(orgDEK, plaintext)`). If we just switch to server KEK, existing ciphertext can't be decrypted — `DecryptSecret(serverKEK, orgDEKEncryptedCiphertext)` fails.

**Impact:** "No existing orgs exist" (user confirmed), so this is moot for production. But the code change must be complete — we can't have a mix of DEK-encrypted and server-KEK-encrypted credentials.

**Recommendation:** Since no orgs exist, no migration of existing ciphertext is needed. The code change is: `OrgCredentialsHandler.Create` uses `deriveServerKey` → `EncryptSecret(serverKEK, plaintext)`. `decryptBinding` for `owner_type='org'` uses `serverKEK`. All new credentials are server-KEK-encrypted from the start. Document that this is a breaking change for any existing org credentials (there are none).

---

### F4 (DESIGN GAP): Server KEK label for org credentials — domain separation

**The problem:** The current code derives the server KEK with label `"provider-credentials"` (for `owner_type='admin'`). My design says org credentials use "server KEK" but doesn't specify the label. If we reuse `"provider-credentials"`, admin and org credentials share the same encryption key — no domain separation.

The injection code (`injection.go:62-68`) derives the server KEK only when it sees `owner_type='admin'` bindings:

```go
for _, b := range bindings {
    if b.OwnerType == "admin" {
        serverKEK = s.deriveAdminKey("provider-credentials")
        break
    }
}
```

With org credentials also needing server KEK, this loop must also trigger on `owner_type='org'`.

**Options:**
1. **Same key** (`"provider-credentials"` for both admin and org). Simplest. No domain separation. Compromise of one = compromise of both. But they're both derived from the same master secret anyway.
2. **Separate keys** (`"provider-credentials"` for admin, `"org-credentials"` for org). Domain separation. `decryptBinding` needs to receive or derive both keys. `injection.go` derives both upfront.

**Recommendation:** Option 2 (separate keys). The cost is one additional HKDF derivation per injection. The benefit is blast-radius reduction — if the admin credential key is somehow leaked (e.g., a debug log), org credentials remain protected. This matches the existing pattern where different `purpose` labels produce independent keys.

---

### F5 (BUG): `IsOrgAdmin` checks `pending_key_wrap = false` — dropping the column changes behavior

**Verified in code:** `IsOrgAdmin` (`pg_org_store.go`) queries:
```sql
WHERE m.role = 'admin' AND m.pending_key_wrap = false
```

D8 drops `pending_key_wrap`. This means `IsOrgAdmin` must change to:
```sql
WHERE m.role = 'admin'
```

**Behavioral change:** Today, a newly-added admin with `pending_key_wrap = true` fails `IsOrgAdmin` — they can't access admin endpoints until they complete key setup. After the change, they have immediate admin access.

**Is this correct?** Yes. With no org DEK, there's nothing to "set up." The admin can manage org credentials immediately because they're encrypted with the always-available server KEK. The key-setup gate was protecting the DEK bootstrap, which no longer exists.

**Recommendation:** Explicitly document this as a behavioral change. The migration drops the column; `IsOrgAdmin` removes the check. No admin should be in a `pending_key_wrap = true` state after migration (there are no existing orgs).

---

### F6 (BUG): SoftDeleteOrg nulls workspace org_id — contradicts D4/D5 intent

**Verified in code:** `SoftDeleteOrg` (`pg_org_store.go`) runs:
```sql
UPDATE workspaces SET org_id = NULL WHERE org_id = $1
```

**The interaction with D4/D5:**
- D4: workspaces are always org-attributed for org members
- D5: membership-gated creator access (creator must be current member)
- SoftDeleteOrg nulls org_id → workspaces become personal → D5's membership check no longer applies → creator has unconditional access

So when an org admin deletes the org, all workspaces become personal, and all creators regain unconditional access. Former members walk away with what were org workspaces as personal workspaces.

**Is this correct?** For org deletion by the org admin — probably yes. The admin is intentionally dissolving the org. Members keeping their workspaces as personal is a reasonable outcome (softer than data loss).

But it contradicts the enterprise intent ("they wouldn't want someone taking their work account with them"). If an enterprise admin deletes the org, former members shouldn't walk away with work.

**Options:**
1. **Keep current behavior** (null org_id → workspaces become personal). Accept that org deletion releases workspaces.
2. **Don't null org_id on soft delete.** Workspaces keep org_id. But the org is soft-deleted (`deleted_at` set). `IsOrgMember` checks `o.deleted_at IS NULL` → returns false → membership-gated access denies. Workspaces are frozen (no access by anyone). Org is gone, workspaces are orphaned.
3. **Suspend rather than delete.** Phase 5's suspension is the correct tool for enterprise offboarding. Soft delete is for "I made this org by mistake."

**Recommendation:** Option 2. Don't null org_id on soft delete. Workspaces become frozen artifacts of a dissolved org. This is consistent with D5 (membership-gated access) and the enterprise intent. The `UPDATE workspaces SET org_id = NULL` line in `SoftDeleteOrg` is removed.

---

### F7 (DESIGN GAP): Workspace credential re-seeding after migration on join

**The problem:** D4 migrates personal workspaces to org-attributed on join:
```sql
UPDATE workspace_metadata SET org_id = $2 WHERE user_id = $1 AND org_id IS NULL
```

But this doesn't bind org credentials to the newly-attributed workspaces. `SeedWorkspaceCredentials` (which creates the credential bindings) only runs at workspace creation time. The migrated workspaces won't receive org credentials until:
- A new org credential is created (triggers `BindCredentialToAllOrgWorkspaces`)
- A credential reload happens

**Impact:** A user joins an org, their existing workspaces get org_id, but they don't get the org's shared LLM key in those workspaces until a reload or new credential creation.

**Recommendation:** After the migration UPDATE, call `BindCredentialToAllOrgWorkspaces` for each existing org credential, or call `SeedWorkspaceCredentials` for each migrated workspace. This ensures org credentials are immediately available.

---

### F8 (SCOPE CHECK): Is this the right level of complexity for the stated problem?

**The user's original complaint:** "Individual users should not be able to create or manage orgs."

**What that minimally requires:**
1. 403 on `POST /api/v1/orgs` for non-admins (one check)
2. Remove the Settings "Organisations" tab (UI deletion)
3. Add org button to sidebar for org admins (UI addition)

**What my design adds beyond that:**
- Org DEK elimination (found a real bug — justified)
- Entitlement model (F1 says unnecessary — remove)
- Single-org enforcement (simplifies — justified)
- Workspace attribution changes (D4 — user explicitly requested)
- Membership-gated access (D5 — needed for enterprise offboarding)
- PortalLayout extraction (user requested)
- Route rename (clarity improvement)

**Assessment:** After removing the entitlement model (F1), the remaining scope is justified. Each change solves a real problem found by tracing code or explicitly requested by the user. The DEK elimination is the highest-risk change (touches credential injection) but fixes a production bug.

---

### F9 (ABSTRACTION CHECK): PortalLayout — right abstraction?

**Question:** Am I extracting PortalLayout at the right seam?

The current `OrgAdminLayout` does three things:
1. Data fetching (load org by slug)
2. Auth/access checking (is the user an admin?)
3. Layout rendering (header, nav, content area)

PortalLayout should handle only #3. The consumer handles #1 and #2. This is the right separation — data and auth are portal-specific, layout is shared.

**Concern:** The `context` prop passes arbitrary data to `<Outlet>`. This is how React Router works (`useOutletContext`), but it means PortalLayout is a thin wrapper. Is it worth extracting?

**Assessment:** Yes — the header (back link, title, badges), nav rendering, and responsive collapse logic are ~60 lines that would be duplicated in every portal. The extraction is at the right seam. The `context` passthrough is standard React Router pattern.

---

### F10 (MISSING): `RewrapAllOrgDEKsForAdmin` on password change — dead code after D8

**Verified in code:** `secrets.go:836-837` calls `RewrapAllOrgDEKsForAdmin` on password change. With D8, this call is dead code and must be removed. The entire `RewrapAllOrgDEKsForAdmin` method in `OrgKeyService` is dead code.

**Recommendation:** Add to the deletion list in Story 1. The password change handler no longer touches org keys.

---

### F11 (MISSING): `OrgAwareKeyService` wrapper — what replaces it?

**Verified in code:** `app.go:302` creates `OrgAwareKeyService` which wraps `KeyService` and adds `UnlockAllOrgDEKs` on login. With D8, this call is gone. But `OrgAwareKeyService` implements `KeyServiceInterface` — if we delete it, what satisfies the interface?

**Answer:** The base `KeyService` already implements `KeyServiceInterface`. `OrgAwareKeyService` was a decorator that added org DEK unlocking. Without org DEKs, the decorator is unnecessary. Wire the base `KeyService` directly.

**Recommendation:** In `app.go`, replace `orgAwareKS` with `keyService` in the auth service wiring. Delete `OrgAwareKeyService`.

---

## Summary of Changes to Design

| Finding | Impact on 0031 |
|---------|----------------|
| F1: Entitlement model unnecessary | **Drop D7.** Admin creates org directly with ownerEmail. Delete Stories 2, 8. New simpler Story 2. |
| F2: Password vestigial | **Remove Password from CreateOrgRequest.** Update Flow 1. |
| F3: No existing ciphertext to migrate | **Document as non-issue.** No migration needed (no orgs exist). |
| F4: Server KEK label | **Use separate label `"org-credentials"`.** Update injection.go to derive both keys. |
| F5: IsOrgAdmin behavior change | **Document explicitly.** Remove `pending_key_wrap` check from query. |
| F6: SoftDeleteOrg nulls org_id | **Change SoftDeleteOrg to NOT null org_id.** Workspaces stay frozen. |
| F7: Credential re-seeding on join | **Add BindCredentialToAllOrgWorkspaces call after migration.** |
| F8: Scope is justified (after F1) | No change. |
| F9: PortalLayout is right | No change. |
| F10: Password change dead code | **Add to deletion list.** |
| F11: OrgAwareKeyService replacement | **Wire base KeyService directly. Add to deletion list.** |

**Revised effort:** Removing the entitlement model saves ~7h. Net estimate drops from 31h to ~24h for Stories 1-9.
