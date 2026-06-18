# Worklog: Story 5 — Membership-Gated Access + List Filter + SoftDeleteOrg Fix + Delete Guard Removal

**Date:** 2026-06-18
**Session:** Implement Story 5 from design 0031 (D5, F6, S1, S12) — the final backend story. TDD → implement → adversarial review → PR.
**Status:** Complete — awaiting orchestrator review/merge

---

## Objective

Implement Story 5 of design `0031`:

1. **D5 — Membership-gated creator access:** `verifyOwner` gains a check: for org-attributed workspaces, the creator must be a **current** org member. Offboarded users lose access automatically.
2. **S1 — Workspace list filter:** `ListWorkspaces` filters out frozen workspaces (org-attributed workspaces where the user is no longer a member). Frozen workspaces disappear from the sidebar.
3. **F6 — SoftDeleteOrg fix:** stops nulling `workspaces.org_id` on org deletion. Workspaces remain org-attributed and become frozen (`IsOrgMember` returns false for deleted orgs).
4. **S12 — Delete guard removal:** `OrgsHandler.Delete` removes the `OrgHasActiveWorkspaces` guard. With always-org-attributed workspaces (D4), every org has workspaces — the guard made deletion impossible.

Builds on PR #209 (Story 4, workspace attribution).

---

## Work Completed

### verifyOwner membership gate (D5)

- **`api/internal/services/workspace/workspace_service.go`** — When the caller is the workspace creator AND the workspace is org-attributed, now checks `IsOrgMember(orgID, userID)`. If the creator left the org (offboarded) → 403 forbidden. Personal workspaces (no `org_id`) are unaffected — creator always has access.

### ListWorkspaces frozen filter (S1)

- **`api/internal/services/database/database.go`** — Both the COUNT and SELECT queries now include a membership condition: a workspace is shown only if `org_id IS NULL` (personal) OR the user is a current member of that org (`EXISTS` subquery joining `org_memberships` + `organizations` with `deleted_at IS NULL AND status != 'suspended'`, mirroring `IsOrgMember`). Frozen workspaces (offboarded user, deleted org) are excluded.

### SoftDeleteOrg fix (F6)

- **`api/internal/services/database/pg_org_store.go`** — Removed the `UPDATE workspaces SET org_id = NULL` statement. The method now only sets `organizations.deleted_at`. Workspaces keep their `org_id` and become frozen via the `IsOrgMember` deleted-at check. Removed the transaction wrapper (single statement no longer needs it).

### Delete guard removal (S12)

- **`api/internal/handlers/orgs.go`** — Removed the `OrgHasActiveWorkspaces` check from the `Delete` handler. Deletion now succeeds even with active workspaces (they become frozen).
- Removed `OrgHasActiveWorkspaces` from both the handler-local `orgStore` interface and the `OrgStore` interface, plus the `*PgOrgStore` implementation and the mock — all dead code (zero remaining callers, per Rule 5).

---

## Assumptions (stated per Rule 7, validated with evidence)

| # | Assumption | Validation |
|---|---|---|
| A1 | `verifyOwner` returns nil immediately for `meta.UserID == userID` without checking org membership | **Confirmed** — `workspace_service.go:763-764` (original). Added membership check. |
| A2 | `IsOrgMember` checks `o.deleted_at IS NULL AND o.status != 'suspended'` — so soft-deleted orgs → `IsOrgMember` returns false | **Confirmed** — `pg_org_store.go:IsOrgMember` query. This is the mechanism that makes workspaces "frozen." |
| A3 | `ListWorkspaces` DB query filters by `user_id` only, no org membership filter | **Confirmed** — `database.go:555-612` (original). Added the membership condition. |
| A4 | `SoftDeleteOrg` nulls `workspaces.org_id` at lines 242-246 | **Confirmed** — removed. |
| A5 | `OrgHasActiveWorkspaces` is only called from the `Delete` handler | **Confirmed** — `grep -rn "OrgHasActiveWorkspaces" --include="*.go" \| grep -v _test.go` showed only the Delete handler call site. Removed all traces. |
| A6 | The D6 test `TestVerifyOwner_D6_CreatorAlwaysAllowed` assumed unconditional creator access — must be updated for D5 | **Confirmed** — updated to `TestVerifyOwner_D6_CreatorMemberAllowed` (creator must be a current member). |

---

## Key Decisions

1. **Membership check uses `IsOrgMember` (not `IsOrgAdmin`) for the creator path.** The design D5 says "current org member" — this is broader than admin. The existing `IsOrgAdmin` check at lines 766-774 (for non-creator access) is preserved (D6).
2. **S1 filter uses an `EXISTS` subquery** rather than a JOIN — avoids row duplication and keeps the pagination COUNT accurate. The subquery mirrors `IsOrgMember` exactly (same deleted_at + status guards).
3. **SoftDeleteOrg no longer needs a transaction** — with the workspace UPDATE removed, only one statement remains (`UPDATE organizations SET deleted_at`). Dropped the `BeginTx`/`Commit` wrapper.
4. **Removed `OrgHasActiveWorkspaces` entirely** (interface + impl + mock) — zero remaining callers after S12. Rule 5.

---

## Tests

### New (Rule 0 TDD)
- `TestVerifyOwner_D5_CreatorLeftOrg_Denied` — offboarded creator → 403.
- `TestVerifyOwner_D5_CreatorStillMember_Allowed` — current member → access granted.
- `TestVerifyOwner_D5_PersonalWorkspace_CreatorAlwaysAllowed` — personal workspace → creator always allowed.

### Updated
- `TestVerifyOwner_D6_CreatorAlwaysAllowed` → `TestVerifyOwner_D6_CreatorMemberAllowed` — creator must now be a current member (D5 behavior change).
- `TestOrgsHandler_Delete_OrgWithWorkspaces` → `TestOrgsHandler_Delete_SucceedsWithWorkspaces` — deletion now succeeds (204) even with workspaces (S12).

---

## Tests Run

- `gofmt -l` — clean
- `go vet` — PASS
- `go build ./api/... ./pkg/...` — PASS
- `go test -race ./api/internal/handlers/` — PASS (40s)
- `go test -race ./api/internal/services/workspace/` — PASS
- `go test -race ./api/internal/services/database/` — PASS
- `go test ./api/internal/server/ ./api/internal/app/` — PASS
- `golangci-lint run` — 0 issues

---

## Next Steps

1. Orchestrator reviews; iterate on findings.
2. Stories 1–5 (all backend) are now complete. Remaining stories are frontend: Story 6 (PortalLayout), Story 7 (sidebar org button), Story 8 (admin org UI), Story 9 (Danger Zone).

---

## Files Modified

### Modified files
- `api/internal/services/workspace/workspace_service.go` — `verifyOwner` D5 membership gate
- `api/internal/services/workspace/verify_owner_d6_test.go` — updated D6 test for D5 behavior; added `GetUserOrgID` to stub (Story 4)
- `api/internal/services/database/database.go` — `ListWorkspaces` S1 frozen filter
- `api/internal/services/database/pg_org_store.go` — `SoftDeleteOrg` F6 fix; removed `OrgHasActiveWorkspaces`
- `api/internal/handlers/orgs.go` — `Delete` S12 guard removal; removed `OrgHasActiveWorkspaces` from interface
- `api/internal/handlers/orgs_test.go` — updated Delete test; removed mock method + field

### New files
- `api/internal/services/workspace/verify_owner_d5_membership_test.go` — D5 membership gate tests

### Branch
`feat/epic43-0031-story5-membership-access` (from `main`)
