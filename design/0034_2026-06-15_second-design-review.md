# 0034: Second Design Review — Structural Issues

**Date:** 2026-06-15
**Status:** Review of 0031 after 0033 corrections
**Focus:** Structural issues that would cause implementation problems or impact functionality

---

## S1 (FUNCTIONAL): Frozen workspaces are visible in the sidebar but inaccessible

**The problem:** When a member is offboarded (D5+D6) or an org is deleted (D9+F6), their org-attributed workspaces become frozen — `verifyOwner` denies access. But `ListWorkspaces` (`database.go:555`) queries:

```sql
WHERE w.deleted_at IS NULL AND w.user_id = $1
```

This returns ALL workspaces owned by the user, regardless of org membership status. The user sees their frozen workspaces in the sidebar, clicks on one, and gets a 403. Bad UX, confusing error.

**Impact:** Every offboarded user sees dead workspaces they can't open. Every user of a deleted org sees dead workspaces. No way to dismiss, hide, or interact with them.

**Root cause:** The workspace list query is purely user-scoped. D5 adds a membership check at the access layer (`verifyOwner`) but not at the list layer. The list and the access check are inconsistent.

**Options:**

1. **Filter the list query:** Add a membership check to `ListWorkspaces`:
   ```sql
   WHERE w.deleted_at IS NULL AND w.user_id = $1
     AND (w.org_id IS NULL 
          OR EXISTS (SELECT 1 FROM org_memberships m 
                     JOIN organizations o ON o.id = m.org_id
                     WHERE m.user_id = $1 AND m.org_id = w.org_id
                       AND o.deleted_at IS NULL))
   ```
   Frozen workspaces disappear from the sidebar entirely. Clean UX. One query change.

2. **Show with "frozen" badge:** Keep them visible, add a visual indicator (greyed out, lock icon). Clicking shows "This workspace belongs to an organization you are no longer a member of." More informative but more UI work.

3. **Transfer to org admin:** Not viable without workspace transfer (deferred).

**Recommendation:** Option 1 (filter the list). Simpler, cleaner UX. The user doesn't need to see workspaces they can't access. If they rejoin the org (re-invited), the workspaces reappear automatically. Add this to Story 5.

---

## S2 (INCONSISTENCY): D9 and Flow 5 still describe the old SoftDeleteOrg behavior

**The problem:** D9 says:

> Soft delete only (existing `SoftDeleteOrg` — sets `deleted_at`, nulls `workspaces.org_id`)

Flow 5 says:

> Nulls workspaces.org_id

But F6 (applied to D4) explicitly changed this:

> SoftDeleteOrg no longer nulls workspace org_id

The endpoint changes table (line 341) correctly says "Remove `UPDATE workspaces SET org_id = NULL`." But D9 and Flow 5 were not updated. An implementer reading D9 would keep the null behavior, creating a contradiction with D4.

**Impact:** Implementation would either null org_id (contradicting D4's frozen-workspace model) or the implementer would notice the contradiction and have to guess which is correct.

**Fix:** Update D9 and Flow 5 to describe the new behavior (workspaces keep org_id, become frozen).

---

## S3 (IMPLEMENTATION BLOCKER): Invitation acceptance needs cross-org membership check

**The problem:** D8 adds `CREATE UNIQUE INDEX idx_org_memberships_single_user ON org_memberships(user_id)`. The invitation acceptance handler (`invitations.go:266`) checks:

```go
existing, err := h.store.GetOrgMember(ctx, inv.OrgID, userID)
if existing != nil {
    return 409 "user is already a member of this org"
}
```

This checks membership in the **same org**. With single-org enforcement, the user could be in a **different** org. The `INSERT INTO org_memberships` would fail with a unique constraint violation on `user_id` — a cryptic database error instead of a clear "you are already in another organization" message.

**Impact:** User in org A accepts invitation to org B → 500 error (constraint violation) instead of a clear 409.

**Fix:** The acceptance handler must check `ListOrgsForUser(userID)` (or a new `GetUserOrgID(userID)`) before attempting the insert. If the user is already in any org, return 409: "you are already a member of another organization." Add this to Story 3 or Story 4.

---

## S4 (OPERATIONAL GAP): Frozen workspace pods keep running

**The problem:** When workspaces become frozen (offboarding or org deletion), the workspace pods keep running in Kubernetes. Nobody can access them via the API to suspend or delete them. Compute costs continue indefinitely.

**Impact:** Orphaned pods consuming cluster resources with no API-level way to stop them. Requires manual `kubectl` intervention.

**Assessment:** This is a real gap but it's properly deferred — Phase 5 (US-43.19) adds suspension infrastructure. The controller doesn't currently know about org membership changes. The design should note this as a known consequence and document the manual mitigation (`kubectl delete pod`).

**Not a blocker for this design.** The number of frozen workspaces will be near-zero initially (no existing orgs). The risk grows with adoption, which is when Phase 5 ships.

---

## S5 (PERFORMANCE): D5 adds a DB query to every workspace access

**The problem:** D5 adds an `IsOrgMember` DB call to `verifyOwner` for org-attributed workspaces accessed by their creator. `verifyOwner` runs on every workspace API call (get, update, suspend, activate, proxy, etc.). This is a hot path that was previously a pure in-memory comparison (`meta.UserID == userID`).

**Impact:** One additional indexed `EXISTS` query per workspace API call for org workspace creators. For a system with 1000 daily active workspace sessions, that's ~1000 extra queries/day. Negligible at current scale but worth noting.

**Assessment:** Acceptable. `IsOrgMember` is a simple indexed lookup. No action needed now. If it becomes a bottleneck, a short-TTL Redis cache of membership status (like the rate limiter's IP cache) would solve it. Do not prematurely optimize.

---

## S6 (DESIGN QUESTION): Credential precedence — org vs personal

**Observed in code:** Credential bindings are ordered `within_priority DESC`:
- User credentials: `within_priority = 10` (higher = comes first)
- Org credentials: `within_priority = 5`
- Admin credentials: varies by auto-apply rule

`injection.go` deduplicates by provider ("first wins"). So **personal credentials override org credentials** for the same provider. If an org sets an OpenAI key and a user has their own OpenAI key, the user's key is used.

**Is this the right behavior for enterprise?** An enterprise org admin who sets a shared key might expect it to take precedence (cost tracking, compliance). But the current system lets personal keys override it.

**Impact on this design:** Not changed by this design — existing behavior. But the design should acknowledge it. When a user joins an org and their personal workspaces are migrated (D4), both personal and org credentials are bound. The personal credential wins for any provider overlap. The org admin's intent (shared key for all members) is silently overridden.

**Recommendation:** Do not change this in the current design (scope creep). But flag it for future review — enterprise customers may want org credentials to take precedence.

---

## S7 (MISSING): `getUserOrgID` helper doesn't exist

**The problem:** D4's backend enforcement code references `getUserOrg(ctx, userID)` to determine the user's org. This function doesn't exist. `ListOrgsForUser` exists but returns full org details (heavier than needed). With single-org enforcement, a simpler query suffices:

```go
func (s *PgOrgStore) GetUserOrgID(ctx context.Context, userID string) (string, error) {
    var orgID string
    err := s.db.QueryRowContext(ctx,
        `SELECT org_id FROM org_memberships WHERE user_id = $1`, userID,
    ).Scan(&orgID)
    if err == sql.ErrNoRows { return "", nil }
    return orgID, err
}
```

**Impact:** Minor — straightforward to implement. But the design should list it as new code so it's not missed.

**Where it's needed:**
- `CreateWorkspace`: auto-set org_id for org members
- Invitation acceptance: check if user is already in an org (S3)
- Frontend: determine if org button should show

---

## S8 (TRADE-OFF CONFIRMATION): Org DEK elimination — user instinct vs analysis

**Observation:** The user initially said "org DEK may be useful." After I traced the injection bug and showed Options A/D were the same, the user chose "option D" (eliminate). The decision is clear, but the design should explicitly state what's lost:

**What's lost:** Zero-knowledge encryption for org credentials. The server (via `LLMSAFESPACE_MASTER_SECRET`) can decrypt all org LLM provider keys. An attacker with server access can extract org credentials.

**What's gained:** Reliability (credentials always inject, no 24h cache dependency), simplicity (~1000 lines deleted), and the `pendingKeyWrap`/`accept-key` UX burden eliminated.

**Context:** Admin credentials already have this property (server KEK). OIDC client secrets are planned to have it (D17 S4). The org DEK was the last holdout.

**Assessment:** The trade-off is sound. The user's instinct ("may be useful") was reasonable before the bug was known. The bug makes the DEK not just complex but non-functional for its primary use case. Confirming the decision is final — no action needed unless the user wants to revisit.

---

## Summary of Required Changes to 0031

| Finding | Severity | Change |
|---------|----------|--------|
| S1: Frozen workspaces visible in sidebar | **Functional bug** | Add membership filter to `ListWorkspaces` query. Add to Story 5. |
| S2: D9/Flow 5 contradict F6 | **Inconsistency** | Update D9 and Flow 5 to describe new SoftDeleteOrg behavior. |
| S3: Invitation needs cross-org check | **Implementation blocker** | Add cross-org membership check to invitation acceptance. Add to Story 3 or 4. |
| S4: Frozen pods keep running | Operational gap (deferred) | Document as known consequence. Phase 5 mitigates. |
| S5: IsOrgMember on hot path | Performance characteristic | Document. No action needed now. |
| S6: Personal creds override org | Existing behavior | Flag for future review. No change in this design. |
| S7: getUserOrgID doesn't exist | Missing code | List as new helper in backend changes. |
| S8: DEK trade-off confirmation | Confirmation only | No action unless user revisits. |
