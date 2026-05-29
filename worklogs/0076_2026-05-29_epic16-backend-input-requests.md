# Worklog: Epic 16 Backend — Agent Input Requests

**Date:** 2026-05-29
**Session:** Implement all backend stories for Epic 16 (Agent Input Requests) that do not conflict with Epic 15
**Status:** Complete

---

## Objective

Implement the backend portions of Epic 16 (Agent Input Requests — Questions & Permissions) without conflicting with Epic 15 (Streaming State Resilience). Epic 15 is entirely frontend; all Epic 16 backend stories are safe to implement in parallel.

---

## Work Completed

### US-16.1: Agent Dialect Interface + OpenCode Implementation
- Created `pkg/agent/dialect.go` — `Dialect` interface with session routes, input request routes, SSE event classification, and event parsing methods
- Created `pkg/agent/types.go` — normalized `QuestionRequest`, `PermissionRequest`, `QuestionInfo`, `QuestionOption`, `ToolRef`, `InputResolution` types
- Created `pkg/agent/opencode/dialect.go` — full OpenCode implementation with proper event parsing (handles `custom` field defaulting to true, nested `status.type` for session status)
- Created `pkg/agent/opencode/dialect_test.go` — 22 test cases covering all paths, routes, event classification, parsing (including real payloads from worklog 0069)

### US-16.0: Fix MCP Client — SSE Parsing + ID Validation
- Fixed `validID` regex: `^[a-zA-Z0-9][a-zA-Z0-9._\-]{0,252}$` (added underscore for opencode IDs like `ses_abc`, `que_xyz`)
- Fixed `SendMessage` SSE parsing: now detects `{"type":"session.status","session_id":"...","status":"idle"}` (real wire format) instead of the non-existent `{"type":"session.idle"}`
- Added session ID filtering: only breaks on idle for the correct session
- Updated all existing tests to use real wire format
- Added 5 new tests: session filtering, busy event ignoring, underscore ID acceptance

### US-16.2a: Question/Permission Proxy Routes
- Created `api/internal/handlers/proxy_input.go` — 5 handlers: `ListQuestions`, `QuestionReply`, `QuestionReject`, `ListPermissions`, `PermissionReply`
- Added `dialect agent.Dialect` field to `ProxyHandler` struct
- Updated `NewProxyHandler` signature to accept dialect parameter
- Updated `api/internal/app/app.go` to pass `&agentoc.Dialect{}` to `NewProxyHandler`
- Registered 5 new routes in `api/internal/server/router.go`
- ID validation: `que_` prefix for questions, `per_` prefix for permissions
- Created `proxy_input_test.go` — 12 test cases (happy paths, invalid IDs, workspace not active, body forwarding, dialect nil guard)

### US-16.3: Normalized Event Emission
- Modified `onRawEvent` in `proxy.go` to call `emitNormalizedInputEvent` when dialect is set
- Implemented `emitNormalizedInputEvent`: detects question/permission events, parses via dialect, publishes normalized `agent.question`, `agent.question.resolved`, `agent.permission`, `agent.permission.resolved` events
- Raw `opencode.event` always published (no regression)
- Parse errors logged at Warn, do not crash or suppress raw event
- 9 test cases covering all event types, parse errors, broker nil safety

### US-16.5: Headless Permission Auto-Approve
- Added `AutoApprovePermissions bool` field to `WorkspaceSpec` in `pkg/apis/llmsafespace/v1/workspace_types.go`
- Added `autoApprovePermissions` to `workspaceConfig` cache struct
- Implemented `shouldAutoApprovePermissions` — reads from cache, falls back to K8s CRD read, fail-closed on error
- Implemented `autoApprovePermission` — async goroutine that POSTs `{"reply":"always"}` to the pod
- Questions are NEVER auto-answered regardless of setting
- 4 test cases: auto-approve enabled, questions not auto-answered, default false, workspace not found (fail closed)

### US-16.6/16.7: MCP Tools for Question/Permission Reply + SendMessage Question Detection
- Extended `APIClient` interface with `QuestionReply`, `QuestionReject`, `PermissionReply` methods
- Implemented all 3 methods on `HTTPClient` with ID validation
- Added `validQuestionID` and `validPermissionID` regex validators
- Added 3 new MCP tools: `session_question_reply`, `session_question_reject`, `session_permission_reply`
- Implemented `parseAnswers` helper for converting MCP args to `[][]string`
- Updated `MockAPIClient` in tests
- Updated integration test tool count from 6 to 9
- 7 new client tests covering happy paths, invalid IDs, invalid reply values
- **US-16.7**: Modified `SendMessage` SSE loop to detect `agent.question` events and return early with structured question data `{"type":"question","request":{...}}`
- **US-16.7**: Added permission auto-approve in headless mode — when `agent.permission` event arrives during `SendMessage`, auto-replies with "always"
- 3 new SendMessage tests: question detected, question for different session ignored, permission auto-approved

### US-16.4: Pending State Recovery on SSE Connect
- Modified `StreamEvents` to call `emitPendingInputRequests` asynchronously after SSE subscribe
- Implemented `emitPendingInputRequests`: fetches pending questions/permissions from pod via GET endpoints, publishes as synthetic `agent.question`/`agent.permission` events
- Implemented `fetchFromPod`: generic GET helper with basic auth
- Implemented `parseQuestionList` and `parsePermissionList`: parse pod responses into normalized types via dialect
- Skips permission fetch if auto-approve is enabled (no point showing prompts that will be auto-approved)
- Graceful degradation: workspace not active, pod unreachable, or fetch failure → no events, no error

---

## Key Decisions

1. **Dialect as nil-safe**: All handlers check `h.dialect == nil` and return 500 if not configured. This prevents panics if the handler is used without proper initialization.
2. **Auto-approve is workspace-level setting, not subscriber-count heuristic**: Explicit, deterministic, inspectable, testable. See US-16.5 design doc for rationale.
3. **Permission ID regex allows underscores in suffix**: `^per_[a-zA-Z0-9_]+$` because opencode permission IDs can contain timestamps with underscores (e.g., `per_1748012345000_abc`).
4. **No US-16.2b (proxy.go split) or US-16.4 (pending state recovery) in this session**: US-16.2b is a pure refactor with no new functionality — lower priority. US-16.4 requires more careful integration with the SSE connection lifecycle and should be done after the core stories are validated.

---

## Blockers

None.

---

## Tests Run

```
go test -timeout 120s -short -race -count=1 ./api/... ./pkg/... — ALL PASS
go build ./... — SUCCESS
go vet ./... — CLEAN
```

Specific test counts:
- `pkg/agent/...`: 2 packages, all pass
- `pkg/mcp/...`: 1 package, all pass (including 5 new US-16.0 tests, 7 new US-16.6 tests)
- `api/internal/handlers/...`: 1 package, all pass (including 12 new proxy_input tests, 9 normalized event tests, 4 auto-approve tests)

---

## Next Steps

1. **US-16.2b**: Split proxy.go into focused files + migrate session handlers to use dialect paths (pure refactor, no new tests needed)
2. **US-16.4**: Pending state recovery on SSE connect — fetch pending questions/permissions from pod when browser reconnects
3. **US-16.7**: Modify `SendMessage` in MCP client to detect `question.asked` events and return early with question data (currently deferred — requires more careful SSE parsing changes)
4. **Frontend stories (US-16.8–16.12)**: Depend on Epic 15 completion

---

## Files Modified

- `pkg/agent/dialect.go` — NEW: Dialect interface
- `pkg/agent/types.go` — NEW: normalized input request types
- `pkg/agent/opencode/dialect.go` — NEW: OpenCode dialect implementation
- `pkg/agent/opencode/dialect_test.go` — NEW: 22 test cases
- `pkg/mcp/client.go` — fixed validID regex, fixed SendMessage SSE parsing, added QuestionReply/QuestionReject/PermissionReply methods
- `pkg/mcp/client_test.go` — updated existing tests to real wire format, added 12 new tests
- `pkg/mcp/server.go` — added 3 new tools + handlers + parseAnswers helper
- `pkg/mcp/server_test.go` — added mock methods for new interface
- `pkg/mcp/integration_test.go` — updated tool count assertion
- `pkg/apis/llmsafespace/v1/workspace_types.go` — added AutoApprovePermissions field
- `api/internal/handlers/proxy.go` — added dialect field, updated NewProxyHandler, added emitNormalizedInputEvent + shouldAutoApprovePermissions + autoApprovePermission
- `api/internal/handlers/proxy_input.go` — NEW: 5 question/permission handlers
- `api/internal/handlers/proxy_input_test.go` — NEW: 25 test cases
- `api/internal/server/router.go` — added 5 new routes
- `api/internal/app/app.go` — pass dialect to NewProxyHandler
- `api/internal/handlers/proxy_test.go` — updated all NewProxyHandler calls for new signature
- `api/internal/handlers/stream_events_test.go` — updated NewProxyHandler calls
