# Worklog: Story 4 — Workspace Attribution + Migration on Join

**Date:** 2026-06-18
**Session:** Implement Story 4 from design 0031 (D4, F7) — workspace auto-attribution for org members, personal-workspace migration on invitation accept, and org credential binding after join. TDD → implement → adversarial review → PR.
**Status:** Complete — awaiting orchestrator review/merge

---

## Objective

Implement Story 4 of design `0031`:

1. **D4 — Workspace auto-attribution:** when a user is in an org, all workspaces they create are org-attributed (`org_id` auto-set from `GetUserOrgID`). Users cannot create personal workspaces while in an org. Non-org users get personal workspaces (`org_id` nil).
2. **D4 — Migration on join:** on invitation acceptance, existing personal workspaces migrate to org-attributed (`UPDATE workspaces SET org_id` inside the accept transaction).
3. **F7 — Credential binding:** after migration, org credentials are bound to the newly-attributed workspaces via `BindAllOrgCredentialsToOrgWorkspaces` so members get the org's shared keys immediately (without waiting for a credential reload).

Builds on PR #208 (Story 3, single-org enforcement + `GetUserOrgID`).

---

## Work Completed

### `CreateWorkspace` auto-attribution (D4)

- **`api/internal/services/workspace/workspace_service.go`** — Added `GetUserOrgID` to the `OrgMembershipChecker` interface. In `CreateWorkspace`, when `req.OrgID` is nil/empty and the org store is available, calls `GetUserOrgID(userID)`. If the user is in an org, auto-sets `req.OrgID` — triggering the existing org validation/membership-check/policy-enforcement path. Non-org users get `org_id=nil` (personal workspace).

### `AcceptInvitationTx` workspace migration (D4)

- **`api/internal/services/database/pg_org_store.go`** — Inside the existing accept transaction, after the membership INSERT, added `UPDATE workspaces SET org_id = $2, updated_at = NOW() WHERE user_id = $1 AND org_id IS NULL AND deleted_at IS NULL`. This migrates all non-deleted personal workspaces to the org atomically (if anything else in the tx fails, the migration rolls back too).

### Credential binding after accept (F7)

- **`pkg/secrets/org_credential_store.go`** — New `BindAllOrgCredentialsToOrgWorkspaces(ctx, orgID)` on `*PgSecretStore`: a single SQL `INSERT ... SELECT ... CROSS JOIN` that binds every org credential to every non-deleted org workspace (`ON CONFLICT DO NOTHING`). Used after invitation acceptance to seed org credentials into newly-migrated workspaces.
- **`api/internal/handlers/invitations.go`** — New `orgCredentialBinder` interface + `SetCredentialBinder` setter on `InvitationsHandler`. After `AcceptInvitationTx` succeeds, calls `BindAllOrgCredentialsToOrgWorkspaces(orgID)`. Best-effort: logs errors but does not fail the accept (credentials bind on the next reload anyway).
- **`api/internal/app/app.go`** — Hoisted `orgCredBinder` variable, assigned from `pgStore`, wired into the invitations handler.

---

## Assumptions (stated per Rule 7, validated with evidence)

| # | Assumption | Validation |
|---|---|---|
| A1 | `CreateWorkspace` already honors `req.OrgID` (reads, validates membership, persists) — I only add auto-fill | **Confirmed** — `workspace_service.go:208-295` (original lines). The existing path validates membership + enforces policy quotas. |
| A2 | `OrgMembershipChecker` only has `IsOrgMember`/`IsOrgAdmin` — needs `GetUserOrgID` | **Confirmed** — `workspace_service.go:55-58` (original). Added `GetUserOrgID`. `*PgOrgStore` already implements it (Story 3). |
| A3 | `AcceptInvitationTx` has no workspace migration | **Confirmed** — `pg_org_store.go:879-936` (original). Only inserts membership + marks invitation accepted. Added the migration UPDATE. |
| A4 | `BindCredentialToAllOrgWorkspaces` exists but takes a credentialID (per-credential) | **Confirmed** — `pkg/secrets/org_credential_store.go:25`. Added `BindAllOrgCredentialsToOrgWorkspaces(orgID)` as a convenience that binds ALL org credentials at once. |
| A5 | The `workspaces.org_id` column is nullable UUID with FK to organizations | **Confirmed** — `api/migrations/000029_organizations.up.sql:78-80`. |
| A6 | `AcceptInvitationTx` is inside a transaction — adding the UPDATE keeps it atomic | **Confirmed** — `pg_org_store.go:880` begins tx; `defer rollback` + explicit `Commit()`. The UPDATE runs on `tx`, so it's atomic with the membership insert. |

---

## Key Decisions

1. **Auto-attribution is non-fatal on lookup error.** If `GetUserOrgID` fails (DB error), `CreateWorkspace` proceeds as a personal workspace with a warning log. This prevents a DB hiccup from blocking workspace creation entirely. The user can manually set `OrgID` or retry.
2. **Migration inside `AcceptInvitationTx` (not a separate method).** The design says "single `UPDATE`" — keeping it inside the existing transaction ensures atomicity: if the membership insert or invitation update fails, the workspace migration rolls back too. No partial state.
3. **`BindAllOrgCredentialsToOrgWorkspaces` as a single CROSS JOIN query** rather than list-then-bind. One round-trip, one SQL statement, idempotent (`ON CONFLICT DO NOTHING`). Cleaner than looping in Go.
4. **Credential binding is best-effort.** If binding fails, the accept still succeeds — the credentials will bind on the next credential reload. Failing the accept because of a credential-binding issue would be worse (the user can't retry — the invitation is already marked accepted).

---

## Tests

### New (Rule 0 TDD)
- `TestCreateWorkspace_OrgMember_AutoAttributed` — org member creates workspace without OrgID → OrgID auto-set to their org.
- `TestCreateWorkspace_NonOrgUser_OrgIDStaysNil` — non-org user → OrgID nil.
- `TestInvitations_Accept_BindsOrgCredentials` — after accept, `BindAllOrgCredentialsToOrgWorkspaces` called with the org ID.
- `TestInvitations_Accept_BindError_StillSucceeds` — binder error does not fail the accept (best-effort).

### Mock updates
- `stubOrgChecker`: added `userOrgID` map + `GetUserOrgID` method.
- `mockInvitationStore`: (no changes — binder is a separate dependency).
- `mockCredBinder` (new): records `BindAllOrgCredentialsToOrgWorkspaces` calls.

---

## Tests Run

- `gofmt -l` — clean (after fix)
- `go vet` — PASS
- `go build ./api/... ./pkg/...` — PASS
- `go test -race ./api/internal/handlers/` — PASS (47s)
- `go test -race ./api/internal/services/workspace/` — PASS
- `go test -race ./api/internal/services/database/` — PASS
- `go test ./api/internal/server/ ./api/internal/app/` — PASS
- `golangci-lint run` (changed packages) — 0 issues

---

## Next Steps

1. Orchestrator reviews; iterate on findings.
2. After merge, Story 5 (membership-gated creator access + workspace list filter + SoftDeleteOrg fix + delete guard removal) is unblocked — it depends on D4 (always org-attributed).

---

## Files Modified

### Modified files
- `api/internal/services/workspace/workspace_service.go` — `OrgMembershipChecker` +`GetUserOrgID`; `CreateWorkspace` auto-attribution (D4)
- `api/internal/services/workspace/verify_owner_d6_test.go` — `stubOrgChecker` +`GetUserOrgID`
- `api/internal/services/database/pg_org_store.go` — `AcceptInvitationTx` workspace migration (D4)
- `api/internal/handlers/invitations.go` — `orgCredentialBinder` interface; `SetCredentialBinder`; F7 binding after accept
- `api/internal/handlers/invitations_test.go` — `mockCredBinder`; binding tests; `setupInvitationRouterWithBinder`
- `pkg/secrets/org_credential_store.go` — `BindAllOrgCredentialsToOrgWorkspaces` (F7)
- `api/internal/app/app.go` — credential binder wiring

### New files
- `api/internal/services/workspace/org_attribution_test.go` — D4 auto-attribution tests

### Branch
`feat/epic43-0031-story4-workspace-attribution` (from `main`)
