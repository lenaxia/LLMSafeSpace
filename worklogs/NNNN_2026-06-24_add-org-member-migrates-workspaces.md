# Worklog: AddOrgMember migrates new member's personal workspaces

**Date:** 2026-06-24
**Session:** User asked "do all workspaces get updated when a user joins an org?" while reviewing the PR #409 migration. Audit of the three join paths revealed only two (CreateOrgWithAdmin, AcceptInvitationTx) atomically migrate the user's personal workspaces; the third path (AddOrgMember — admin direct-add via `POST /orgs/:id/members` and SSO JIT provisioning) only inserted the membership row. The latent gap would produce the same orphan-org_id state migration 000044 just backfilled.

**Status:** Implementation + failing-first test landed locally; PR pending.

---

## The gap

Three code paths add a user to an org. Pre-fix matrix:

| Path | Source | Migrates workspaces? |
|---|---|---|
| User creates org themselves | `CreateOrgWithAdmin` at `pg_org_store.go:170` | ✅ Yes (D4 block) |
| User accepts invitation | `AcceptInvitationTx` at `pg_org_store.go:1142` | ✅ Yes (D4 block) |
| **Admin direct-add via POST /orgs/:id/members** | `AddOrgMember` at `pg_org_store.go:325` | ❌ **No** |
| **SSO JIT provisioning on first login** | `AddOrgMember` (same function, called from `sso.go:654`) | ❌ **No** |

The third and fourth paths both route through the same `AddOrgMember` function. It only INSERTed the membership row. Downstream consequence: when an admin adds a user (or SSO provisions one) whose user_id has pre-existing workspaces, those workspaces stay `org_id IS NULL`. `BindCredentialToAllOrgWorkspaces` later filters by `w.org_id = orgID` and silently skips them, producing the exact `ProviderModelNotFoundError custom/glm-5.2` symptom we just spent two PRs fixing.

Production state today:

- No SSO is configured (`org_sso_configs` table is empty).
- 4 org_memberships total cluster-wide; none look like they hit AddOrgMember.
- So the gap has no current production victims — but it's a one-feature-launch away from re-introducing the same incident class.

## The fix

`AddOrgMember` now opens its own transaction and runs the same UPDATE that the other two join paths run. The implementation is byte-identical to the D4 block in `CreateOrgWithAdmin:200` and `AcceptInvitationTx:1186`:

```go
if _, err := tx.ExecContext(ctx,
    `UPDATE workspaces SET org_id = $2, updated_at = NOW()
     WHERE user_id = $1 AND org_id IS NULL AND deleted_at IS NULL`,
    userID, orgID,
); err != nil {
    return fmt.Errorf("migrate new member's personal workspaces to org: %w", err)
}
```

Wrapped in `BeginTx/Commit`. If UPDATE fails, the INSERT rolls back. If the user is in another org, the INSERT itself fails on the existing `idx_org_memberships_single_user UNIQUE (user_id)` and the whole transaction aborts.

Signature unchanged. Callers (`sso.go:654`, `orgs.go:443`) don't need updates. Existing mocks (`sso_test.go:117`, `orgs_test.go:168`, `org_sso_test.go:126`) keep returning nil and pass through.

## Test plan (TDD)

`TestAddOrgMember_MigratesPersonalWorkspaces` in `pg_org_store_test.go` was written first. Uses sqlmock to assert:

1. A transaction is opened.
2. The INSERT membership runs.
3. The UPDATE workspaces runs with the exact WHERE clause: `WHERE user_id = .* AND org_id IS NULL AND deleted_at IS NULL` (anchored regex so a regression that drops scoping is caught).
4. The transaction commits.

The test failed against the pre-fix code with:

```
add org member: call to ExecQuery 'INSERT INTO org_memberships ...' was not expected,
  next expectation is: ExpectedBegin
```

— exactly the right red bar: the pre-fix code skipped the `Begin` because it didn't use a transaction. After the implementation change, the test passes alongside the existing `TestCreateOrgWithAdmin_MigratesOwnerPersonalWorkspaces` and `TestAcceptInvitationTx_MigratesPersonalWorkspaces`. All three join paths now have explicit migration-block tests.

Full `api/internal/services/database`, `api/internal/handlers`, and `api/internal/services/sso` suites still pass.

## Adversarial review (10 attacks)

| # | Attack | Outcome |
|---|---|---|
| 1 | Atomicity (UPDATE failure rolls back INSERT) | Both in BEGIN/COMMIT ✓ |
| 2 | Re-add same user → unique-violation | Pre-existing behavior; AcceptInvitationTx uses ON CONFLICT DO NOTHING but AddOrgMember is one-shot — unchanged |
| 3 | User in another org → single-user UNIQUE rejects | Pre-existing index handles it |
| 4 | UPDATE migrates the wrong workspaces | `WHERE user_id = $1 AND org_id IS NULL AND deleted_at IS NULL` — same filter as the other two paths |
| 5 | SSO call site `sso.go:654` now atomic | No outer transaction; my function manages its own — no conflict |
| 6 | Admin handler `orgs.go:443` same as #5 | No outer transaction; safe |
| 7 | Existing mocks expect single INSERT | Mocks return nil regardless; not affected |
| 8 | Reverse-test asserting "no transaction" | None exists; I checked |
| 9 | Naming nit: M1 vs M2 vs D4 | Cosmetic; matched existing nomenclature where possible |
| 10 | Migration 000044 still needed? | Yes — it cleans historical orphans; this PR prevents future orphans. They don't overlap |

## Files changed

- `api/internal/services/database/pg_org_store.go` — `AddOrgMember` wrapped in a transaction with the workspace-migration UPDATE
- `api/internal/services/database/pg_org_store_test.go` — `TestAddOrgMember_MigratesPersonalWorkspaces` added

## Definition of done

- [x] Audit identified the gap (all three join paths).
- [x] Failing test written first (failed for the right reason).
- [x] Implementation passes the new test + existing tests.
- [x] Adversarial review (10 attacks) documented.
- [ ] PR opened, CI green.
- [ ] PR merged.
- [ ] Long-term coverage: when SSO ships or an admin bulk-imports users, no new orphan workspaces appear.

## Why this is a code defect, not a deployment-timing orphan

Unlike the bugs that produced the 5 orphan workspaces fixed by migration 000044, this is a current code defect — the WHERE-clause UPDATE block is missing from one of the three documented join paths. The other two have it. The audit comment in `AcceptInvitationTx:198` even says "Keeps the two 'join the org' paths consistent" — anticipating exactly the symmetry that AddOrgMember broke when it was written or modified later.

Refs: PR #409 (the historical-orphan backfill that exposed this gap), PR #228 (D4 in CreateOrgWithAdmin), PR #209 (auto-attribution).
