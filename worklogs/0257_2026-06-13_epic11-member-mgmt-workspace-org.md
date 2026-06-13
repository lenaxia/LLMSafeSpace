# Worklog: Epic 11 — US-11.5 Member Management + US-11.8 Workspace Org Attribution

**Date:** 2026-06-13
**Session:** Continue Epic 11 implementation — US-11.5 and US-11.8
**Status:** In Progress

---

## Objective

Implement US-11.5 (member management + key handshake) and US-11.8 (workspace org attribution) following the critical path.

---

## Work Completed

### US-11.5: Member Management + Key Handshake

Extended `api/internal/handlers/orgs.go` with five new handler methods:
- `ListMembers` — GET /api/v1/orgs/:id/members (OrgMemberGuard)
- `AddMember` — POST /api/v1/orgs/:id/members (OrgAdminGuard)
- `RemoveMember` — DELETE /api/v1/orgs/:id/members/:userID (OrgAdminGuard)
- `AcceptKey` — POST /api/v1/orgs/:id/accept-key (authMiddleware only)
- `ChangeMemberRole` — PUT /api/v1/orgs/:id/members/:userID (OrgAdminGuard)

Key behaviors:
- Adding admin creates `pending_key_wrap=true`; member has no pending wrap
- Accept-key verifies membership + pending status before calling `WrapOrgDEKForNewAdmin`
- RemoveMember prevents self-removal and last-admin removal
- ChangeMemberRole handles promotion (sets pending) and demotion (deletes org_key_members row)
- Extended `orgStore` interface with all membership methods

Registered 6 new routes in `router.go` under appropriate guards.

### US-11.8: Workspace Org Attribution

**CRD types** (`pkg/apis/llmsafespace/v1/workspace_types.go`):
- Added `OrgID string` to `WorkspaceOwner` struct (deepcopy auto-handles value types)

**API types** (`pkg/types/types.go`):
- Added `OrgID *string` to `CreateWorkspaceRequest`
- `WorkspaceMetadata.OrgID` was already added in previous session

**Workspace service** (`workspace_service.go`):
- Added `OrgMembershipChecker` interface + `orgStore` field + `SetOrgStore`
- Updated `CreateWorkspace` to validate org membership when `OrgID` is provided
- Updated `verifyOwner` to allow org members access (checks `IsOrgMember`)
- Added `verifyOrgAdmin` for destructive operations (checks `IsOrgAdmin`)
- Replaced `verifyOwner` with `verifyOrgAdmin` in: DeleteWorkspace, SuspendWorkspace, RestartWorkspace, RenameWorkspace
- Updated `buildWorkspaceCRD` to set `org-id` label and `OrgID` in owner
- Updated `WorkspaceMetadata` construction to include `OrgID`

**Database queries** (`database.go`):
- `GetWorkspace` — added `w.org_id` to SELECT and scan
- `CreateWorkspace` — added `org_id` to INSERT
- `ListWorkspaces` — added LEFT JOIN on `org_memberships` to include org workspaces; updated COUNT query to match

**App wiring** (`app.go`):
- Added `wsSvc.SetOrgStore(pgOrgStore)` to wire org membership checks into workspace service

---

## Key Decisions

1. **verifyOrgAdmin for destructive ops** — Delete, Suspend, Restart, Rename all require org admin or creator. Read-only and interactive ops (EnsureSession, ActivateWorkspace, etc.) allow any org member.
2. **ListWorkspaces uses LEFT JOIN** — efficient single query for both personal and org workspaces; COUNT query uses identical predicate.
3. **OrgID on CRD as value string** — not a pointer; keeps deepcopy simple. Empty string = personal workspace.
4. **org-id label on CRD** — enables future K8s label selector queries for org workspace discovery.

---

## Blockers

- Disk space still constrained (~1.1GB free); full `go build`/`go test` cannot run. All files pass `gofmt -e`.

---

## Tests Run

- `gofmt -e` on all 7 modified files — zero syntax errors
- Full compilation and test suite deferred to CI due to disk constraints

---

## Next Steps

1. US-11.6: Org credential CRUD handler
2. US-11.7: decryptBinding org branch + SeedWorkspaceCredentials fix
3. US-11.9: Org DEK rotation
4. US-11.10: Password change re-wrap
5. Verify full compilation in CI

---

## Files Modified

- `api/internal/handlers/orgs.go` — added 5 member management handlers
- `api/internal/server/router.go` — registered 6 new member routes
- `api/internal/services/workspace/workspace_service.go` — added OrgMembershipChecker, verifyOrgAdmin, org-aware verifyOwner, org membership validation in CreateWorkspace
- `api/internal/services/database/database.go` — updated GetWorkspace, CreateWorkspace, ListWorkspaces queries
- `api/internal/app/app.go` — wired wsSvc.SetOrgStore
- `pkg/apis/llmsafespace/v1/workspace_types.go` — added OrgID to WorkspaceOwner
- `pkg/types/types.go` — added OrgID to CreateWorkspaceRequest
