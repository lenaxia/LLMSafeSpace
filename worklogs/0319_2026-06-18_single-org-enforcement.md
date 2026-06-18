# Worklog: Story 3 — Single-Org Enforcement + Cross-Org Invitation Check

**Date:** 2026-06-18
**Session:** Implement Story 3 from design 0031 (D6, D8, S3, S7) — unique index on `org_memberships(user_id)`, `GetUserOrgID` helper, cross-org invitation/AddMember rejection. TDD → implement → adversarial review → PR.
**Status:** Complete — awaiting orchestrator review/merge

---

## Objective

Implement Story 3 of design `0031_2026-06-15_org-access-control-portal-architecture.md`:

1. **D8 — Single-org enforcement:** schema-level unique index on `org_memberships(user_id)`.
2. **S3 — Cross-org invitation check:** `InvitationsHandler.Accept` rejects a user already in another org with a clear 409 (not a raw DB constraint-violation 500).
3. **S7 — `GetUserOrgID` helper:** returns the user's single org ID (or `""`).
4. **D6 — No self-removal:** `RemoveMember` rejects `targetUserID == callerUserID` (already implemented + tested — verified, no work needed).

Builds on PR #201 (Story 2, admin-only org creation).

---

## Assumptions (stated per Rule 7, validated with evidence)

| # | Assumption | Validation |
|---|---|---|
| A1 | D6 self-removal is already implemented | **Confirmed** — `api/internal/handlers/orgs.go:416` (`RemoveMember`) and `:493` (`ChangeMemberRole` demote-self) both reject `targetUserID == callerUserID`. Tests: `TestOrgsHandler_RemoveMember_SelfRemovalBlocked`, `TestOrgsHandler_ChangeMemberRole_DemoteSelfBlocked`. No work needed. |
| A2 | Next migration number is 000036 | **Confirmed** — `ls api/migrations/ \| sort -V \| tail` shows `000035_drop_org_dek`. |
| A3 | The unique index is plain (no `WHERE` clause) — `org_memberships` has no `deleted_at` column | **Confirmed** — `api/migrations/000029_organizations.up.sql` `org_memberships` table has columns `(org_id, user_id, role, pending_key_wrap, created_at)`; no `deleted_at`. Migration 035 dropped `pending_key_wrap`, leaving `(org_id, user_id, role, created_at)`. A plain `CREATE UNIQUE INDEX` is correct. |
| A4 | `AcceptInvitationTx` uses `ON CONFLICT (org_id, user_id) DO NOTHING` — the new unique index on `user_id` will raise 23505 on cross-org insert which this conflict target does NOT catch | **Confirmed** — `pg_org_store.go:910-913`. This is exactly why the S3 pre-check is essential (prevents the cryptic 500). |
| A5 | `GetUserOrgID` returns `("", nil)` for not-found (consistent with `GetUserIDByEmail`) | **Confirmed by implementation** — `pg_org_store.go:GetUserOrgID` returns `("", nil)` on `sql.ErrNoRows`. Tested. |
| A6 | The chart migration mirror must match `api/migrations/` | **Confirmed** — both directories have 35 migrations; mirrored 036 to both. repolint verifies `chart migrations match api/migrations/`. |

---

## Work Completed

### Migration (D8)

- **`api/migrations/000036_single_org_enforcement.up.sql`** — `CREATE UNIQUE INDEX IF NOT EXISTS idx_org_memberships_single_user ON org_memberships(user_id)`. Idempotent (`IF NOT EXISTS`). The pre-existing non-unique `idx_org_memberships_user` (migration 029) is left in place (harmless, slightly redundant — avoids a DROP that could fail on partial deploys). No data migration (no orgs exist in production).
- **`api/migrations/000036_single_org_enforcement.down.sql`** — `DROP INDEX IF EXISTS idx_org_memberships_single_user`.
- **Mirrored to `charts/llmsafespace/migrations/`** — identical up/down files.

### Store layer (S7)

- **`api/internal/services/database/pg_org_store.go`** — Added `GetUserOrgID(ctx, userID) (string, error)` to the `OrgStore` interface + `*PgOrgStore` implementation: single-column `SELECT org_id FROM org_memberships WHERE user_id = $1`; returns `("", nil)` on `sql.ErrNoRows`.

### Handler layer (S3 + robustness)

- **`api/internal/handlers/invitations.go`** — `Accept` handler: after the same-org membership check, added a `GetUserOrgID(userID)` pre-check. If the user is already in any org → **409** `"user is already a member of another organization"`. Added `GetUserOrgID` to the `invitationStore` interface.
- **`api/internal/handlers/orgs.go`** — `AddMember` handler: added the same cross-org pre-check (the `AddMember` path takes a raw `UserID`, so without the pre-check it would also hit the unique index as a raw 500). Added `GetUserOrgID` to the handler-local `orgStore` interface.

### What was NOT touched

- D6 (self-removal) — already implemented and tested (A1).
- Workspace attribution (D4 — Story 4), membership-gated access (D5 — Story 5). Out of scope.
- Frontend — Story 3 is backend-only.

---

## Key Decisions

1. **Pre-existing `idx_org_memberships_user` left in place.** Dropping it would require a `DROP INDEX` that could fail on partial deploys (e.g., if migration 029 hadn't run). The unique index is a strict superset; the old index is harmless redundancy. Idempotent `CREATE UNIQUE INDEX IF NOT EXISTS` is the safe choice.
2. **Cross-org check added to both `Accept` AND `AddMember`.** The design S3 specifically names invitation acceptance, but `AddMember` (POST /orgs/:id/members) takes a raw `UserID` and would hit the same unique-index violation as a cryptic 500. Per Rule 4 (robust) and Rule 5 (no pre-existing errors), I added the same pre-check to `AddMember` with a clear 409.
3. **`GetUserOrgID` returns `("", nil)` for not-found** — consistent with `GetUserIDByEmail` and the codebase convention (not-found is not an error for lookups). The handler treats `""` as "not in any org" → proceeds.
4. **The pre-check + unique index is a two-layer defense.** The pre-check returns a clean 409 in the common case (user accepted an invitation while already in another org). The unique index is the schema-level backstop that prevents data corruption even if the pre-check has a TOCTOU race (see adversarial review F1).

---

## Adversarial Self-Review (Rule 11)

### Phase 1 + Phase 2 (validated each)

| # | Finding | Verdict | Resolution |
|---|---|---|---|
| F1 | TOCTOU race: two concurrent invitation accepts for the same user (to two different orgs) could both pass the `GetUserOrgID` pre-check (both see `""`), then the second `AcceptInvitationTx` insert hits the unique index → raw 23505 → 500 instead of 409 | **Real edge case, accepted.** The pre-check handles the common case (user in org A accepts invite to org B). The unique index prevents data corruption (the core invariant). The narrow race window (same user, two invitations, milliseconds apart) would produce a 500, not corruption. The design's fix (S3) is the pre-check, which I've done. Making `AcceptInvitationTx` detect 23505-on-`user_id` and return a typed error for a clean 409 is a future improvement (would require parsing the constraint name from the PgError). Documented. |
| F2 | `AcceptInvitationTx` uses `ON CONFLICT (org_id, user_id) DO NOTHING` — with the new unique index, should I change the conflict target? | **False alarm.** `ON CONFLICT` can specify only one arbiter. The PK `(org_id, user_id)` is the correct conflict to handle (same-org re-accept via a different invitation). The `user_id` unique conflict is handled by the pre-check + DB index backstop. Changing it would be over-engineering. |
| F3 | Should the migration drop the redundant `idx_org_memberships_user`? | **No — decided to keep it.** Dropping requires a `DROP INDEX` that could fail on partial deploys. The redundancy is harmless (PostgreSQL will use the unique index for `user_id` lookups). Idempotency > minimalism here. |
| F4 | Pre-existing 0318 worklog collision on main (`admin-only-org-creation` + `us-45.2-redis-activesess` both numbered 0318) | **Pre-existing main issue, flagged.** Not introduced by this PR. My worklog is 0319 (next free). The collision should be resolved by renaming one of the two on main, but that touches already-merged history — out of scope for Story 3. |

### Phase 3
F1 is a documented accepted edge case (no corruption, rare, design's fix is the pre-check). F2–F4 are false alarms / pre-existing. **Zero actionable findings remaining.**

---

## Tests

### Backend — new (Rule 0 TDD: tests written first, confirmed red, then implemented)

**`api/internal/handlers/invitations_test.go`** (new tests):
- `TestInvitations_Accept_AlreadyInAnotherOrg_Conflict` — user already in org-2 accepts invite to org-1 → **409** with "another organization" message; invitation NOT marked accepted.
- `TestInvitations_Accept_GetUserOrgIDError_500` — `GetUserOrgID` DB error → **500**.

**`api/internal/handlers/orgs_test.go`** (new test):
- `TestOrgsHandler_AddMember_AlreadyInAnotherOrg_Conflict` — adding a user already in org-2 → **409** with "another organization" message.

**`api/internal/services/database/pg_org_store_test.go`** (new tests):
- `TestPgOrgStore_GetUserOrgID_Found`.
- `TestPgOrgStore_GetUserOrgID_NotFound_ReturnsEmptyNoError` — confirms `("", nil)` contract.
- `TestPgOrgStore_GetUserOrgID_DBError`.

### Mock updates
- `mockInvitationStore`: added `userOrgID` map + `userOrgIDErr` field + `GetUserOrgID` method.
- `mockOrgStore`: added `userOrgID` map + `userOrgIDErr` field + `GetUserOrgID` method.

---

## Tests Run

- `gofmt -l` on all changed Go files — **clean**
- `go vet ./api/internal/handlers/ ./api/internal/services/database/` — **PASS**
- `go build ./api/... ./pkg/...` — **PASS**
- `go test -race ./api/internal/handlers/` — **PASS** (41.2s)
- `go test -race ./api/internal/services/database/` — **PASS** (1.2s)
- `go test ./api/internal/server/ ./api/internal/app/` — **PASS** (wiring intact)
- `golangci-lint run` (changed packages) — **0 issues**
- `bin/repolint` — migrations/chart-mirror/CRD checks **PASS** (the worklog-sequence FAIL is the pre-existing 0318 main collision, not introduced here)

TDD: the cross-org invitation tests were run against the unchanged handler first and confirmed red (200 instead of 409), then the pre-check was implemented and re-run green.

---

## Blockers

None. (Pre-existing 0318 worklog collision on main is flagged but not introduced by this PR.)

---

## Next Steps

1. Orchestrator reviews; iterate on findings.
2. After merge, Story 4 (workspace attribution + migration on join, D4/F7) is unblocked — it uses `GetUserOrgID` (added here) to auto-attribute workspaces for org members.
3. Story 5 (membership-gated creator access, D5) depends on Story 4.

---

## Files Modified

### New files
- `api/migrations/000036_single_org_enforcement.up.sql`
- `api/migrations/000036_single_org_enforcement.down.sql`
- `charts/llmsafespace/migrations/000036_single_org_enforcement.up.sql`
- `charts/llmsafespace/migrations/000036_single_org_enforcement.down.sql`

### Modified files
- `api/internal/handlers/invitations.go` — `invitationStore` interface +`GetUserOrgID`; `Accept` cross-org pre-check (S3)
- `api/internal/handlers/invitations_test.go` — mock +`GetUserOrgID`; cross-org + lookup-error tests
- `api/internal/handlers/orgs.go` — `orgStore` interface +`GetUserOrgID`; `AddMember` cross-org pre-check
- `api/internal/handlers/orgs_test.go` — mock +`GetUserOrgID`; AddMember cross-org test
- `api/internal/services/database/pg_org_store.go` — `OrgStore` interface +`GetUserOrgID`; `*PgOrgStore.GetUserOrgID` impl (S7)
- `api/internal/services/database/pg_org_store_test.go` — `GetUserOrgID` store tests

### Branch
`feat/epic43-0031-story3-single-org-enforcement` (from `main`)
