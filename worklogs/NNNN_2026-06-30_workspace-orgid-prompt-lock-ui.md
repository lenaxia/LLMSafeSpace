# Worklog: Workspace OrgID propagation + fail-closed prompt-lock UI

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

## Work Completed

### Investigation (live cluster reproduction)

- Pulled the API logs from `chat.safespaces.dev` and confirmed `PUT /api/v1/workspaces/890fad31-.../prompt` returned 403 with the expected error.
- Pulled the matching `GET /api/v1/workspaces` response and confirmed `orgId` was absent from every item (`omitempty` drops nil).
- Traced the conversion at `api/internal/services/workspace/workspace_service.go:458-472` and confirmed `OrgID` was not assigned even though `m.OrgID` (`WorkspaceMetadata.OrgID`) was non-nil for org-scoped rows.

### Validated assumptions

1. **`WorkspaceListItem` is the only payload the frontend gets for the workspace list.** Validated by reading `api/internal/server/router.go:869` — the frontend never separately fetches per-workspace metadata for the drawer; it relies on the list-response shape.
2. **`WorkspaceMetadata` carries `OrgID *string`.** Validated at `pkg/types/workspace.go:113` (`db:"org_id"` tag) and the Scan calls at `database.go:752` / `pg_org_store.go:809`.
3. **Frontend `WorkspaceListItem` already declares `orgId?: string`.** Validated at `frontend/src/api/types.ts:42` — backend-side omission bug, not a contract issue.
4. **There is exactly one conversion site.** `grep -rn "WorkspaceListItem{"` returns only `workspace_service.go:458` and a test fixture.
5. **Backend enforcement resolves `org_id` server-side.** `api/internal/handlers/prompts.go:227-230` via `h.store.GetWorkspaceOrgID`. Validated by live 403 from `request_id=2fda2be9-2409-475a-a888-47cb901d9a38`.
6. **`omitempty` semantics: a `&""` OrgID would render as `"orgId":""` and be falsy in `if (orgId)`.** Correct behavior, and a pre-existing data-integrity concern not introduced by this PR.

### Backend fix

- `pkg/types/workspace.go`: added `OrgID *string `json:"orgId,omitempty"`` to `WorkspaceListItem`, mirroring `WorkspaceMetadata.OrgID`.
- `api/internal/services/workspace/workspace_service.go`: wired `OrgID: m.OrgID` through the conversion.

### Frontend fix

- `frontend/src/components/workspace/WorkspaceSettingsDrawer.tsx`: changed the policy-fetch `.catch` from `setPromptLocked(false)` (fail open) to `setPromptLocked(true)` (fail closed). Defense in depth — if the org policy fetch fails for any reason, the textarea is locked rather than allowing a write that would 403.

### Review-feedback iteration

After the first AI review approved with three minor findings:
- Dropped the `at line 107` reference from the `OrgID` field doc (line numbers in comments are fragile; the referenced line was already wrong — `WorkspaceMetadata.OrgID` had moved to line 113).
- Removed `#477` issue-number leakage from three production-source comments (one in `pkg/types/workspace.go`, one in `workspace_service.go`, one in `WorkspaceSettingsDrawer.tsx`). Issue numbers belong in commit messages and git blame.
- Added this worklog entry.

After the second AI review's worklog-template-conformance finding, rewrote this entry to match the exact template structure required by README-LLM §Worklog Requirements.

---

## Key Decisions

- **Backend type fix over frontend defensive lookup.** The frontend type already declared `orgId?: string`. Adding a defensive separate-fetch path on the frontend would have masked the backend type-omission bug indefinitely. Fixing the source-of-truth (the Go type) is the correct layer.
- **Fail closed over fail open** for the org policy fetch. The previous fail-open behavior optimized for "let the user edit even if we can't reach the policy endpoint" — but the cost of being wrong is a silent server-side 403 with a generic "Save failed" toast. Fail-closed surfaces the uncertainty as `Managed by your organization. Contact your admin to request changes.` which is at worst slightly inaccurate (when allow_user_prompt is actually true but the fetch transiently failed) and at best correctly informs the user.
- **Keep `#477` references in *test* comment bodies (regression-test documentation).** The second AI review flagged that test files still reference `#477` after the production-source cleanup. Defensible — issue numbers in regression-test bodies document *which bug this guards against*, which is durable value (the issue page itself stays as the historical record of why the test exists). Production-source comments don't have that excuse. Non-blocking per the reviewer.

---

## Blockers

None.

---

## Tests Run

- `go test -timeout 60s -run "TestListWorkspaces_PropagatesOrgID" ./api/internal/services/workspace/` — initial RED (`Expected value not to be nil`), then GREEN after applying both type and conversion fixes.
- `go test -timeout 60s -run "TestListWorkspaces" ./api/internal/services/workspace/ -v` — all 6 tests pass after fix.
- `go test -timeout 300s -short ./api/... ./pkg/... ./controller/...` — full Go test suite passes.
- `go test -timeout 30s ./pkg/types/` — regenerates `frontend/src/api/contract-fixtures.json`; no diff (nil `*string` + `omitempty` correctly drops the field).
- `cd frontend && npx vitest run src/components/workspace/WorkspaceSettingsDrawer.test.tsx` — RED on the fail-closed test (1 of 4 new tests), then GREEN after the `setPromptLocked(true)` change.
- `cd frontend && npx vitest run src/api/contract.test.ts` — 9 tests pass; contract types still align.
- `cd frontend && npx vitest run` — full frontend test suite: 1252 tests pass across 115 files.

---

## Next Steps

- Survey the other top-level list endpoints (sessions, secrets, etc.) for the same bug class: API undersending fields the frontend types declare. This is the second instance (the first was #467, where `currentModelProviderID` was stale during a cache window — also a backend-undersend symptom). A single one-pass audit using `grep -n 'WorkspaceMetadata\|...Metadata' api/internal/services/*/...go` and cross-referencing frontend `api/types.ts` should surface any remaining drops. File as a separate issue if anything turns up.

---

## Files Modified

- `pkg/types/workspace.go` — added `OrgID *string` field to `WorkspaceListItem`.
- `api/internal/services/workspace/workspace_service.go` — wired `OrgID: m.OrgID` through the metadata→list-item conversion.
- `api/internal/services/workspace/workspace_service_test.go` — added `TestListWorkspaces_PropagatesOrgID` regression test.
- `frontend/src/components/workspace/WorkspaceSettingsDrawer.tsx` — changed the policy-fetch `.catch` to fail closed (`setPromptLocked(true)`).
- `frontend/src/components/workspace/WorkspaceSettingsDrawer.test.tsx` — added the `promptsApi.getOrg` mock and four new regression tests under "org prompt-customization lock".
- `worklogs/NNNN_2026-06-30_workspace-orgid-prompt-lock-ui.md` (this file).
