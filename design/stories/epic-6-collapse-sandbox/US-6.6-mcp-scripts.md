# US-6.7: MCP Client + Scripts Update

**Epic:** 6 — Collapse Sandbox into Workspace
**Status:** Planning
**Dependencies:** US-6.5

## Objective

Rewrite MCP client and test scripts to use workspace routes.

## MCP Client (`pkg/mcp/client.go`)

### Remove `resolveSandbox` entirely (lines 292-306)

No more workspace→sandbox resolution. Workspace IS the proxy target.

### `CreateSession`
```go
// Before: resolveSandbox → POST /sandboxes/{sandboxID}/sessions
// After:  POST /workspaces/{workspaceID}/sessions
```

### `GetHistory`
```go
// Before: resolveSandbox → GET /sandboxes/{sandboxID}/sessions/{sid}/message
// After:  GET /workspaces/{workspaceID}/sessions/{sid}/message
```

### `SendMessage`
```go
// Before: resolveSandbox → POST /sandboxes/{sandboxID}/sessions/{sid}/prompt + SSE /sandboxes/{sandboxID}/events
// After:  POST /workspaces/{workspaceID}/sessions/{sid}/prompt + SSE /workspaces/{workspaceID}/events
```

### `fallbackHistory`
```go
// Before: GET /sandboxes/{sandboxID}/sessions/{sid}/message
// After:  GET /workspaces/{workspaceID}/sessions/{sid}/message
```

## Test Script (`local/test.sh`)

Remove all sandbox CRUD tests. Session creation uses `/workspaces/{id}/sessions`.

## Acceptance Criteria

1. Zero `/sandboxes/` references in `pkg/mcp/`
2. MCP CreateSession calls `/workspaces/{id}/sessions` directly
3. `local/test.sh` covers workspace lifecycle without sandbox routes
