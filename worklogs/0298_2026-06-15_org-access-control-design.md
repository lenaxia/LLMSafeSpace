# Worklog: Org Access Control & Portal Architecture Design

**Date:** 2026-06-15
**Session:** Design org access control restructuring (0031-0036)
**Status:** Complete

---

## Objective

Design the org access control restructuring that follows Epic 43 Phases 1-4. The original self-service org creation model was replaced with platform-admin-only creation. The org DEK was eliminated (fixing a latent credential injection bug). Portal architecture, workspace attribution, and membership-gated access were designed.

---

## Work Completed

### Design documents (6 files, ~1700 lines)

**0031** — Main design: 13 decisions (D1-D13), 7 user flows, 10 implementation stories (~31h core effort). Key decisions:
- D1: Org creation restricted to platform admins (direct creation with ownerEmail)
- D7: Org DEK eliminated — server KEK for org credentials (fixes silent injection bug)
- D8: Single-org enforced at schema level
- D4: Always org-attributed workspaces for org members
- D5: Membership-gated creator access (offboarded users lose access)
- D2/D3: PortalLayout pattern, route rename to /orgs/:slug

**0032** — Open questions analysis: explored options for DEK bootstrap, user search, org deletion, single vs multi-org, invitation/existing-account question. All resolved.

**0033-0036** — Four iterative design review passes tracing every claim against actual code:
- 0033: Removed unnecessary entitlement model, found SoftDeleteOrg contradiction, credential re-seeding gap
- 0034: Frozen workspaces visible in sidebar, cross-org invitation constraint violation
- 0035: Wrong table name, dropped-table inserts, impossible org deletion, missing /auth/me org role
- 0036: PendingOrgCleaner dead code, already-in-org edge case, migration irreversibility

### Key bug found

Tracing the org credential injection flow revealed that org credentials silently stop injecting into member workspaces after 24h without admin login. `UnlockAllOrgDEKs` only runs for org admins; the DEK cache expires; `decryptBinding` for `owner_type='org'` fails silently. The fix (D7: server KEK) eliminates the entire org DEK machinery (~1000 lines deleted).

---

## Key Decisions

1. **Eliminate org DEK entirely** — server KEK for org credentials. The DEK only protected org credentials (user secrets use user DEK), was broken for its primary use case, and the zero-knowledge property was already broken for admin credentials.
2. **Direct admin creation, not entitlements** — platform admin creates org with ownerEmail. The entitlement model was invented to solve the DEK bootstrap problem, but D7 eliminates the DEK, making entitlements unnecessary.
3. **Single-org enforcement** — schema-level UNIQUE on org_memberships(user_id).
4. **Always org-attributed workspaces** — no personal workspaces while in an org. Personal workspaces migrate on join.
5. **Membership-gated creator access** — offboarded users lose workspace access via one-line check in verifyOwner.
6. **Email-only invitations** — no user search (prevents account enumeration).

---

## Blockers

None. All design questions resolved through 4 review passes.

---

## Tests Run

Design-only session. No code changes, no tests run. CI on PR #186 (docs) — all passing.

---

## Next Steps

1. Merge PR #186 (design docs) after review iteration
2. Implement Story 1 (eliminate org DEK) — the 10h keystone change
3. Implement Stories 2-9 in dependency order
4. Story 10 (bulk email) deferred

---

## Files Modified

### New files
- `design/0031_2026-06-15_org-access-control-portal-architecture.md`
- `design/0032_2026-06-15_org-access-open-questions-analysis.md`
- `design/0033_2026-06-15_design-review-consistency-pass.md`
- `design/0034_2026-06-15_second-design-review.md`
- `design/0035_2026-06-15_third-design-review.md`
- `design/0036_2026-06-15_fourth-design-review.md`
- `worklogs/0292_2026-06-15_org-access-control-design.md`

### Modified files
- `design/stories/epic-43-organization-management/README.md` — cross-references to 0031/0032
