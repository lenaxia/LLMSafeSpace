# Worklog: Backfill workspaces.org_id for deployment-timing orphans

**Date:** 2026-06-24
**Session:** Resolve `ProviderModelNotFoundError custom/glm-5.2` on workspaces `a847faa5-...` and (initially-suspected) `95139c12-...` after PR #407 (bootstrap fix) deployed cleanly. Surface investigation revealed `95139c12` was actually healthy; only `a847faa5` was broken. Root cause traced to two pre-existing deployment-timing windows, both already closed in code, leaving 5 orphan workspaces in production with `org_id IS NULL` despite the user being a member of an org. Migration `000044` backfills the orphans + their derived `workspace_credential_bindings` idempotently.

**Status:** Migration + integration test green locally against postgres:16. PR pending.

---

## The live failure

User reported `ProviderModelNotFoundError custom/glm-5.2` on `https://safespace.thekao.cloud/chat/a847faa5-19b4-463d-a434-1ce473a16f93/...`.

Direct inspection of pod `a847faa5-...-e9d85f3f`:

```
$ kubectl exec ... -- cat /sandbox-runtime/agent-config.json | jq '.provider | keys'
[
  "opencode",
  "opencode-relay"
]
```

`custom` provider absent. PR #407 fixed the bootstrap-500 path that was masking the org credential at boot — but for `a847faa5` the credential was never bound to the workspace at all, so the bootstrap had nothing to materialize.

DB state confirmed:

```
SELECT pc.owner_type, pc.provider, pc.name
FROM workspace_credential_bindings b
JOIN provider_credentials pc ON pc.id=b.credential_id
WHERE b.workspace_id='a847faa5-...';

 owner_type |   provider   |        name        
------------+--------------+--------------------
 admin      | opencode     | opencode-free-tier
 user       | thekao cloud | thekao
```

The org's `custom` credential (`39e2aaaa-...`, name `thekaocloud`) is missing from the bindings. It IS bound to 12 other workspaces. `a847faa5` is one of 5 orphans.

## Root cause

Two distinct deployment-timing windows:

### Bug class 1 (4 workspaces) — D4 owner-migration didn't exist when the org was created

`CreateOrgWithAdmin` in `api/internal/services/database/pg_org_store.go:170-212` includes a "D4" block that migrates the owner's existing `org_id IS NULL` workspaces to the newly-created org. This block was added in PR #228 merged 2026-06-18 18:11 UTC.

The user's org was created 2026-06-16 07:32:47 UTC — **2 days before PR #228 merged**. So when their org was created, the D4 migration block did not exist. The 4 workspaces they had at that moment kept `org_id IS NULL`. Since the migration block only runs at the moment of org creation, no later code path will fix them.

Affected workspaces: `5f286cc8`, `3c600987`, `a847faa5`, `8154ae86` (created 2026-06-13 → 2026-06-14).

### Bug class 2 (1 workspace) — auto-attribution merged but not yet deployed

`CreateWorkspace` in `api/internal/services/workspace/workspace_service.go:200-212` includes a "D4" auto-attribution block that looks up the user's org and sets `req.OrgID` if the request arrived without one. This block was added in PR #209 merged 2026-06-18 02:58 UTC.

Workspace `3459df6f` was created 2026-06-18 04:58 UTC — **2 hours after PR #209 merged**. CI on the merge commit `46c1f25b` failed (it took until commit `d404260f` at 04:11 UTC for a successful main build with the auto-attribution code). The next workspace created after `3459df6f` was 20+ hours later (2026-06-19 02:32 UTC) and DID have org_id set, suggesting the deploy of the auto-attribution code happened during that window.

`3459df6f` went through the pre-D4 CreateWorkspace path that didn't auto-attribute, and stored `org_id = NULL` despite the user already being in an org.

### Downstream consequence

`BindCredentialToAllOrgWorkspaces` (`pkg/secrets/org_credential_store.go:25-37`) filters `WHERE w.org_id = $orgID`. Workspaces with `org_id IS NULL` are silently skipped. So when an org credential is created (or auto-applied via `BindAllOrgCredentialsToOrgWorkspaces`), orphan-org_id workspaces never receive the binding. This is what produced the `custom/glm-5.2` symptom — the credential exists, the workspace exists, the user owns both, but the binding row is missing.

## Why this is not a current code defect

Both fix paths are correct *going forward*. There is no existing code path that creates a workspace with `org_id = NULL` for a user-in-an-org. There is no existing code path that creates an org without migrating the owner's personal workspaces. The bugs are purely historical: rows created during the pre-fix windows remain in their pre-fix state, and there is no automatic backfill.

## Migration design

`api/migrations/000044_backfill_workspace_org_id_orphans.up.sql` runs once at deploy, idempotently:

```sql
BEGIN;

-- Step 1: Set org_id on workspaces whose owner is an active org member.
-- Soft-deleted orgs are excluded — org_memberships rows survive a soft-
-- delete, and a naive UPDATE join would happily attribute a live
-- workspace to a dead org (Attack 14 in the stress-test).
UPDATE workspaces w
   SET org_id = m.org_id, updated_at = NOW()
  FROM org_memberships m
  JOIN organizations o ON o.id = m.org_id AND o.deleted_at IS NULL
 WHERE w.user_id = m.user_id
   AND w.org_id IS NULL
   AND w.deleted_at IS NULL;

-- Step 2: Backfill workspace_credential_bindings for org credentials.
-- Mirrors BindCredentialToAllOrgWorkspaces' INSERT semantics so the app
-- path and migration path produce identical rows. Cast pc.owner_id to
-- uuid because the column is TEXT (it stores _platform for admin creds).
INSERT INTO workspace_credential_bindings (credential_id, workspace_id, source_type, within_priority)
SELECT pc.id, w.id, 'auto', 5
  FROM provider_credentials pc
  JOIN workspaces w ON w.org_id = pc.owner_id::uuid
 WHERE pc.owner_type = 'org'
   AND w.deleted_at IS NULL
ON CONFLICT (credential_id, workspace_id) DO NOTHING;

COMMIT;
```

Both statements are atomic within a single transaction. A failure in step 2 rolls back step 1.

The `down.sql` is intentionally a no-op: by the time the migration commits, the rows are correct, and silently re-introducing the bug would risk breaking active workspaces.

## Production impact preview

Run via a transactional `BEGIN ... ROLLBACK` against the live DB:

```
UPDATE 5      -- 5 workspaces get org_id backfilled
INSERT 0 7    -- 7 (org_credential, workspace) bindings get created
```

5 workspaces, 7 bindings. Both numbers match the user's state precisely.

## Test plan (TDD)

Failing test was written first as `hack/migration-orphan-org-id-backfill.sh` and wired into `.github/workflows/migration-safety.yml` as a new `org-id-backfill` job (mirrors the existing `data-cleanup` job). Test:

1. Resets schema, applies migrations 000001–000043.
2. Seeds 6 rows reproducing every shape from the production state:
   - Active org + member with NULL-org_id orphan workspace (the bug)
   - Same member's healthy workspace with org_id set (control)
   - Solo non-org user's personal workspace (must stay personal)
   - Soft-deleted workspace owned by org member (must not be modified)
   - Soft-deleted-org member's workspace (Attack 14 — must NOT be attributed to the dead org)
   - Org credential bound only to the healthy workspace (mirrors prod)
3. Applies migration 000044.
4. Asserts 9 invariants:
   - orphan workspace org_id backfilled ✓
   - healthy workspace untouched ✓
   - solo user's workspace remains personal ✓
   - soft-deleted workspace untouched ✓
   - **stale soft-deleted-org membership did not leak ✓**
   - org credential bound to formerly-orphan workspace ✓
   - healthy binding survives without duplication ✓
   - solo workspace received no spurious bindings ✓
   - soft-deleted workspace received no bindings ✓
5. Re-runs the migration and asserts `UPDATE 0` + `INSERT 0 0` (idempotency).

All 11 assertions pass against postgres:16.

## Adversarial review (15 attacks)

Stress-tested before opening the PR:

| # | Attack | Outcome |
|---|---|---|
| 1 | Multi-org user | Impossible — `idx_org_memberships_single_user UNIQUE (user_id)` |
| 2 | User leaves org A, joins org B | Migration only touches NULL rows; old workspaces with org_id=A keep that value |
| 3 | Org member's workspace was intentionally personal | Product policy says this is impossible; migration aligns with policy |
| 4 | Concurrent INSERT during migration | MVCC + row-level locks |
| 5 | Live traffic during migration | Reads of `workspaces.org_id` happen at create-time only; INSERTs into bindings are additive |
| 6 | Solo user gets attributed to no-such-org | INNER JOIN on org_memberships filters them |
| 7 | Down migration loses data | No-op down (consistent with `000040_user_email_verified.down.sql`) |
| 8 | Partial failure mid-migration | Wrapped in BEGIN/COMMIT — atomic |
| 9 | Soft-deleted credentials | `provider_credentials` has no soft-delete column; matches app behavior |
| 10 | Idempotency | Test confirms `UPDATE 0` + `INSERT 0 0` on re-run |
| 11 | credential_auto_apply rules with custom priority | One rule exists in prod (`target_type='all'`); fully covered already; orphans are only the org-credential auto-binding path which depends on `w.org_id` |
| 12 | Compound bugs (multi-workspace, multi-cred) | Single statement covers the cross-product |
| 13 | Interaction with user-creds-to-user-workspaces | That path filters by `user_id`; my migration touches only org creds; no overlap |
| 14 | **Stale membership in soft-deleted org** | **Caught by stress-test, fixed before shipping**: added `JOIN organizations ... WHERE deleted_at IS NULL` to the UPDATE |
| 15 | Production preview | 5 workspaces, 7 bindings. Matches expected user state. |

The Attack 14 finding is what justifies the stress-test: the original migration would have attributed any orphan workspace owned by a member of a soft-deleted org to that dead org, propagating bad state. Caught and fixed before the PR opened.

## Files changed

- `api/migrations/000044_backfill_workspace_org_id_orphans.up.sql` (new)
- `api/migrations/000044_backfill_workspace_org_id_orphans.down.sql` (new, no-op)
- `hack/migration-orphan-org-id-backfill.sh` (new, integration test)
- `.github/workflows/migration-safety.yml` (added `org-id-backfill` job)

## Risks accepted

- **Down migration is a no-op.** A rollback to release N-1 leaves the backfilled rows in their post-migration state. This is the right trade-off — the migration corrects bad data, rolling back the data would silently re-introduce the bug. Acceptable because:
  - Rolling back N-1 doesn't reintroduce the *code* bugs (those were fixed in PR #209 / PR #228).
  - The corrected rows are equivalent to what the post-fix code paths would have produced.
- **Migration touches up to ~all workspaces in the cluster.** On a deployment with N orphan workspaces, the UPDATE is O(N) bounded by the number of orphans (it's filtered to NULL rows). The INSERT is O(orgs × org_workspaces) but ON CONFLICT DO NOTHING makes most rows no-ops. Bounded by data already in the DB.

## Definition of done

- [x] Failing test (with the bug seeded) authored before migration.
- [x] Migration up + down written.
- [x] Test passes locally against postgres:16.
- [x] Wired into CI as a new migration-safety job.
- [x] Stress-tested for 15 attack vectors.
- [x] Production impact previewed (transactional rollback).
- [ ] PR opened, CI green.
- [ ] PR merged.
- [ ] Verify on live cluster: bindings appear on `a847faa5` and other 4 affected workspaces.
- [ ] Verify chat sessions on `a847faa5` no longer error.
