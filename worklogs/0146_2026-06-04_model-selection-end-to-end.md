# Worklog: Model Selection End-to-End Wiring

**Date:** 2026-06-04
**Session:** Wire model selection from backend to frontend
**Status:** Complete

---

## Objective

Complete the llm-credentials story by making model selection functional end-to-end. The backend handlers existed but were never wired; the frontend had no model selector UI.

---

## Work Completed

### Backend Fixes

| Issue | Fix |
|-------|-----|
| `SetWorkspaceMetadataUpdater` never called in `app.go` → `SetModel` returned 503 | Added `secretsHandler.SetWorkspaceMetadataUpdater(dbSvc)` in app.go |
| `GetDefaultModel` not implemented on any concrete type | Added to `database.Service` with `sql.NullString` for NULL handling |
| `default_model` column never read back from DB | Added to `GetWorkspace`, `ListWorkspaces`, `ListPendingReloadWorkspaces` SQL + scan |
| `DefaultModel` not propagated through workspace service | Added to `WorkspaceMetadata`, `Workspace`, `WorkspaceListItem` types and service mappings |

### Frontend

- Added `listModels`/`setModel` API calls to `workspacesApi`
- Created `ModelSelector` dropdown component in ChatPage header
- Shows available models with tier badges (free/paid)
- Supports model switching via `PUT /workspaces/:id/model`

---

## Key Decisions

1. **Placement:** Model selector in the chat page header (right side, next to kebab menu) — minimally invasive, always visible when workspace is active.
2. **No separate settings page:** Model selection is a quick-switch action, not a configuration workflow.
3. **Backend satisfies two interfaces:** `database.Service` now satisfies both `WorkspaceMetadataUpdater` and `WorkspaceDefaultModelReader` via the type assertion pattern already used in `models.go`.

---

## Tests Run

```
go test ./... → 41 packages ok (0 failures)
npx tsc --noEmit → clean
npx vitest run → 616 tests passed (75 files)
```

---

## Files Modified

- `api/internal/app/app.go` — Wire `SetWorkspaceMetadataUpdater`
- `api/internal/services/database/database.go` — `GetDefaultModel`, SQL updates
- `api/internal/services/database/database_test.go` — `GetDefaultModel` tests, mock column updates
- `api/internal/services/workspace/workspace_service.go` — Map `DefaultModel` in both response paths
- `pkg/types/types.go` — `DefaultModel` field on 3 structs
- `frontend/src/api/types.ts` — `defaultModel` on `WorkspaceListItem`
- `frontend/src/api/workspaces.ts` — `listModels`, `setModel`, `ModelInfo`, `ListModelsResponse`
- `frontend/src/components/chat/ModelSelector.tsx` — NEW
- `frontend/src/components/chat/ModelSelector.test.tsx` — NEW
- `frontend/src/pages/ChatPage.tsx` — Mount `ModelSelector`
