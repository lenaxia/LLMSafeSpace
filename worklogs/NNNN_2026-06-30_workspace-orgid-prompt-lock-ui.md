# Worklog: Workspace OrgID propagation + fail-closed prompt-lock UI (#477)

**Date:** 2026-06-30
**Session:** Fix issue #477 — Workspace Settings drawer's prompt-customization Lock UI never rendered for org-scoped workspaces because the API list response dropped the `OrgID` field.
**Status:** Complete

---

## Objective

A member of an org whose admin had disabled `allow_user_prompt` could:

1. Open Workspace Settings → see the editable "Custom Instructions" textarea (the Lock UI was supposed to render but didn't).
2. Type a custom prompt, click Save.
3. See a generic "Save failed" toast (backend correctly returned 403 with `org admin has disabled member prompt customization`).
4. Reload the page → edits silently dropped.

Backend enforcement was correct. The UX was broken: the user had no signal that customization was disabled until the save round-trip failed, and even then the error surface was thin.

---

## Assumptions (stated + validated)

1. **`WorkspaceListItem` is the only payload the frontend gets for the workspace list.** → Validated by reading `api/internal/server/router.go:869` (`wsSvc.ListWorkspaces` → response body items). The frontend never separately fetches per-workspace metadata for the drawer; it relies on the list-response shape.
2. **`WorkspaceMetadata` (the DB row) carries `OrgID *string`.** → Validated at `pkg/types/workspace.go:113` (`db:"org_id"` tag) and verified by tracing `database.go:752` / `pg_org_store.go:809` Scan calls.
3. **The frontend already declares `orgId?: string` on its `WorkspaceListItem` type.** → Validated at `frontend/src/api/types.ts:42`. So this is a backend-side data-omission bug, not a contract negotiation issue.
4. **There is exactly one conversion site `WorkspaceMetadata` → `WorkspaceListItem`.** → Validated by `grep -rn "WorkspaceListItem{"` returning only `api/internal/services/workspace/workspace_service.go:458` (one production site, plus a test fixture in `router_workspace_access_test.go`).
5. **Backend enforcement (`PromptHandler.SetWorkspacePrompt` at `api/internal/handlers/prompts.go:227-230`) is correct and resolves `org_id` server-side via `h.store.GetWorkspaceOrgID`.** → Validated by live reproduction: `request_id=2fda2be9-2409-475a-a888-47cb901d9a38` returned 403 with the expected error string.
6. **Frontend `if (workspace.orgId)` short-circuited when `orgId` was `undefined`.** → Validated by inspecting `WorkspaceSettingsDrawer.tsx:43-48` and pulling the list-response body from cluster logs — `orgId: null` for all 8 workspaces despite the org clearly existing server-side (the 403 above proves it).

---

## Work Completed

### Investigation (live cluster reproduction)

- Pulled the API logs from `chat.safespaces.dev` and confirmed `PUT /api/v1/workspaces/890fad31-.../prompt` returned 403.
- Pulled the matching `GET /api/v1/workspaces` response and confirmed `orgId` was absent from every item (`omitempty` drops nil).
- Traced the conversion at `api/internal/services/workspace/workspace_service.go:458-472` and confirmed `OrgID` was not assigned even though `m.OrgID` (`WorkspaceMetadata.OrgID`) was non-nil for org-scoped rows.

### Fix

- **`pkg/types/workspace.go`**: added `OrgID *string `json:"orgId,omitempty"`` to `WorkspaceListItem`, mirroring `WorkspaceMetadata.OrgID`.
- **`api/internal/services/workspace/workspace_service.go`**: wired `OrgID: m.OrgID` through the conversion.
- **`frontend/src/components/workspace/WorkspaceSettingsDrawer.tsx`**: changed the policy-fetch `.catch` from `setPromptLocked(false)` (fail open) to `setPromptLocked(true)` (fail closed). Defense in depth — if the org policy fetch fails for any reason, the textarea is locked rather than allowing a write that would 403.

### Tests (TDD)

- **Backend (`TestListWorkspaces_PropagatesOrgID`)**: seeds one org-scoped + one personal workspace, asserts the returned `WorkspaceListItem.OrgID` matches the source. Initial run failed red against the original code (`Expected value not to be nil`); fix turns it green.
- **Frontend (four new tests under "org prompt-customization lock")**:
  - Locked org → lock message renders, textarea must NOT be present.
  - Allowed org → editable textarea renders.
  - Personal workspace (no `orgId`) → editable textarea, `promptsApi.getOrg` never called.
  - Fail-closed → `getOrg` rejection → lock message renders.

All 14 drawer tests + 1252 frontend tests + full Go test suite pass.

### Review-feedback changes

After AI review approved with three minor findings:
- Dropped the `at line 107` reference from the `OrgID` field doc (line numbers in comments are fragile and that one was already wrong — `WorkspaceMetadata.OrgID` had moved to line 113).
- Removed `#477` issue-number leakage from source comments (one in `pkg/types/workspace.go`, one in `workspace_service.go`, one in `WorkspaceSettingsDrawer.tsx`). Issue numbers belong in commit messages and git blame, not source.
- Added this worklog entry.

---

## Decisions

- **Backend type fix over frontend defensive lookup.** The frontend type already declared `orgId?: string`. Adding a defensive separate-fetch path on the frontend would have masked the backend type-omission bug indefinitely. Fixing the source-of-truth (the Go type) is the correct layer.
- **Fail closed over fail open** for the org policy fetch. The previous fail-open behavior optimized for "let the user edit even if we can't reach the policy endpoint" — but the cost of being wrong is a silent server-side 403 with a generic "Save failed" toast. Fail-closed surfaces the uncertainty (`Managed by your organization. Contact your admin to request changes.`) which is at worst slightly inaccurate (when allow_user_prompt is actually true but the fetch transiently failed) and at best correctly informs the user.

---

## Follow-ups

None identified for this fix. The original review approved with style/process nits only.

---

## Linked

- Issue: #477
- PR: #478
- Related: this is the same code-quality category as #467 (`currentModelProviderID` stale window) — both bugs are downstream effects of the API undersending fields the frontend depends on. Worth a survey of the other top-level list endpoints to check for similar drops.
