# Worklog: Auto-Create Session on Workspace Active — SSE Fix + Tests

**Date:** 2026-06-03
**Status:** Complete

---

## Objective

When a workspace transitions from Pending→Active, the UX should automatically create a session and switch focus to it, without requiring the user to click "+".

## Root Cause

`activeWorkspaceId` was `undefined` until workspace became Active, which prevented the SSE event stream from connecting. Without SSE, the `workspace.phase` event was never received, so the auto-create session `useEffect` never fired.

## Fix

**`frontend/src/pages/ChatPage.tsx:432`** — Changed `useEventStream(activeWorkspaceId, ...)` to `useEventStream(workspaceId, ...)` so SSE connects unconditionally and detects phase transitions.

## Tests

| File | Tests |
|---|---|
| `frontend/src/pages/ChatPage.sse.test.tsx` | 4 new unit tests: Pending→Active happy, Suspended unhappy, sessionId-in-URL edge, duplicate event guard |
| `frontend/src/pages/ChatPage.test.tsx` | Existing auto-create test (workspace already Active on mount, already covered) |
| `frontend/tests/e2e/chat.spec.ts` | 2 new Playwright tests with API mocking: happy path (session created) + unhappy path (Pending → no session) |
| `api/internal/services/workspace/workspace_ensure_session_test.go` | 1 new service test: Active workspace → session created via HTTP POST to pod |

## Files Changed

```
M  api/internal/services/workspace/workspace_ensure_session_test.go
M  api/internal/services/workspace/workspace_service.go
M  charts/llmsafespace/crds/workspace.yaml
M  cmd/workspace-agentd/main.go
M  controller/internal/workspace/health.go
M  frontend/src/api/types.ts
M  frontend/src/components/workspace/DiskUsageBar.tsx
M  frontend/src/pages/ChatPage.sse.test.tsx
M  frontend/src/pages/ChatPage.tsx
M  frontend/tests/e2e/chat.spec.ts
M  pkg/agentd/types.go
M  pkg/agentd/types_test.go
M  pkg/apis/llmsafespace/v1/workspace_types.go
M  pkg/types/types.go
A  worklogs/0128_2026-06-03_auto-create-session-sse-fix.md
```

## Verification

- Frontend unit tests: 61 passing (46 in ChatPage.sse.test.tsx + 15 in ChatPage.test.tsx)
- Backend service test: `TestEnsureSession_ActiveWorkspace_ReturnsSession` passes
- Pending workspace test removed: testify mock v1 can't handle callback-style returns for poll-loop mocking
