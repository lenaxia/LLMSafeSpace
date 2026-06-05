# US-6.7: MCP Client + Scripts Update

**Epic:** 6 — Collapse Sandbox into Workspace
**Status:** Planning
**Dependencies:** US-6.5

## Objective

Rewrite MCP client HTTP paths and test scripts to use workspace routes directly. Remove `resolveSandbox` indirection entirely.

## Programmatic User Scenarios (MCP)

The MCP server exposes these tools to external MCP clients (Claude, Cursor, etc.):

| Tool | User Intent | API Path (After) |
|------|-------------|-----------------|
| `workspace_create` | Create a dev environment | `POST /api/v1/workspaces` |
| `workspace_activate` | Start/resume a workspace | `POST /api/v1/workspaces/:id/activate` |
| `workspace_stop` | Suspend a workspace | `POST /api/v1/workspaces/:id/suspend` |
| `session_create` | Start a conversation | `POST /api/v1/workspaces/:id/sessions` (proxy) |
| `session_message` | Send a prompt, get response | `POST /api/v1/workspaces/:id/sessions/:sid/prompt` + `GET /api/v1/workspaces/:id/events` (SSE) |
| `session_history` | Read conversation history | `GET /api/v1/workspaces/:id/sessions/:sid/message` |

### Typical programmatic flow:
```
1. workspace_create(runtime="python:3.10") → { id: "abc-123" }
2. workspace_activate(workspace_id="abc-123") → { resumed: "abc-123" }
3. (poll status until Active, or rely on activate blocking)
4. session_create(workspace_id="abc-123") → { id: "sess-456" }
5. session_message(workspace_id="abc-123", session_id="sess-456", message="write hello world") → "Here's..."
6. session_history(workspace_id="abc-123", session_id="sess-456") → [...]
7. workspace_stop(workspace_id="abc-123") → done
```

## MCP Client (`pkg/mcp/client.go`)

### Remove `resolveSandbox` entirely (lines 292-306)

No more workspace→sandbox resolution. Workspace IS the proxy target.

### Method rewrites

| Method | Before | After |
|--------|--------|-------|
| `CreateSession` | `resolveSandbox` → `POST /sandboxes/{sandboxID}/sessions` | `POST /workspaces/{workspaceID}/sessions` |
| `GetHistory` | `resolveSandbox` → `GET /sandboxes/{sandboxID}/sessions/{sid}/message` | `GET /workspaces/{workspaceID}/sessions/{sid}/message` |
| `SendMessage` | `resolveSandbox` → `POST /sandboxes/{sandboxID}/sessions/{sid}/prompt` + SSE `/sandboxes/{sandboxID}/events` | `POST /workspaces/{workspaceID}/sessions/{sid}/prompt` + SSE `/workspaces/{workspaceID}/events` |
| `fallbackHistory` | `GET /sandboxes/{sandboxID}/sessions/{sid}/message` | `GET /workspaces/{workspaceID}/sessions/{sid}/message` |

### `APIClient` interface — no change needed

The interface already takes `workspaceID` parameters. Only the `HTTPClient` implementation routes through sandbox internally.

## Test Script (`local/test.sh`)

### Current sandbox references (A6):
Lines 279, 301, 329, 336, 350, 602, 624, 717, 785 — all call `/sandboxes/` routes.

### Changes:
- Remove all sandbox CRUD tests (`POST /sandboxes`, `GET /sandboxes/:id`, `DELETE /sandboxes/:id`)
- Session creation: `POST /workspaces/{id}/sessions` (was `POST /sandboxes/{id}/sessions`)
- Message send: `POST /workspaces/{id}/sessions/{sid}/message` (was sandbox path)
- History: `GET /workspaces/{id}/sessions/{sid}/message` (was sandbox path)
- Events: `GET /workspaces/{id}/events` (was sandbox path)
- Status check: `GET /workspaces/{id}/status` now returns `podIP` and `endpoint`

### Test flow (after):
```bash
# 1. Create workspace
WS_ID=$(curl -s POST /api/v1/workspaces -d '{"name":"test","runtime":"base","storageSize":"1Gi"}' | jq -r .id)

# 2. Wait for Active
poll_until "GET /api/v1/workspaces/$WS_ID/status" '.phase == "Active"'

# 3. Create session (proxy to pod)
SESSION_ID=$(curl -s POST /api/v1/workspaces/$WS_ID/sessions | jq -r .id)

# 4. Send message
curl -s POST /api/v1/workspaces/$WS_ID/sessions/$SESSION_ID/message -d '{"parts":[{"type":"text","text":"hello"}]}'

# 5. Get history
curl -s GET /api/v1/workspaces/$WS_ID/sessions/$SESSION_ID/message

# 6. Suspend
curl -s POST /api/v1/workspaces/$WS_ID/suspend

# 7. Resume
curl -s POST /api/v1/workspaces/$WS_ID/activate

# 8. Delete
curl -s DELETE /api/v1/workspaces/$WS_ID
```

## Files Modified

| File | Change |
|------|--------|
| `pkg/mcp/client.go` | Remove `resolveSandbox`; rewrite all paths from `/sandboxes/` to `/workspaces/` |
| `pkg/mcp/client_test.go` | Update all test expectations to use `/workspaces/` paths |
| `pkg/mcp/integration_test.go` | Update mock server routes |
| `local/test.sh` | Remove sandbox CRUD tests; rewrite session/message/event paths |

## Acceptance Criteria

1. Zero `/sandboxes/` references in `pkg/mcp/`
2. `resolveSandbox` method deleted
3. MCP `CreateSession` calls `POST /workspaces/{id}/sessions` directly
4. MCP `SendMessage` SSE connects to `/workspaces/{id}/events`
5. MCP `GetHistory` calls `GET /workspaces/{id}/sessions/{sid}/message`
6. `go test ./pkg/mcp/...` passes
7. `local/test.sh` covers full workspace lifecycle without sandbox routes
8. Zero `/sandboxes/` references in `local/test.sh`
