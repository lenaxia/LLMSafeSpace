# US-4.1: Implement MCP Server Core

**Epic:** 4 - MCP Server
**Priority:** Critical
**Depends on:** US-3.1

## User Story

As an external AI tool (Claude, Cursor), I want to connect to LLMSafeSpace via MCP, so that I can create sandboxes and interact with agents programmatically.

## Acceptance Criteria

- [ ] MCP server binary starts and accepts connections
- [ ] 5 core tools registered: sandbox_create, session_create, session_message, session_history, sandbox_terminate
- [ ] stdio transport works (for direct CLI use)
- [ ] SSE transport works (for remote tools)
- [ ] All tools call LLMSafeSpace API internally

## Technical Details

**New files:**

| File | Purpose |
|------|---------|
| `api/internal/mcp/server.go` | MCP server setup, tool registration |
| `api/internal/mcp/tools.go` | Tool definitions and handlers |
| `api/internal/mcp/resources.go` | MCP resources (sandbox sessions, workspace files) |
| `api/internal/mcp/prompts.go` | MCP prompts (debug-in-sandbox, run-tests, code-review) |
| `api/internal/mcp/transport.go` | stdio + SSE transport setup |
| `cmd/mcp/main.go` | MCP server entrypoint (top-level cmd/ directory) |

**Tool → API mapping:**

| MCP Tool | API Call |
|----------|----------|
| `sandbox_create` | `POST /api/v1/sandboxes` |
| `session_create` | `POST /api/v1/sandboxes/{id}/sessions` |
| `session_message` | `POST /api/v1/sandboxes/{id}/sessions/{sid}/prompt` (uses prompt_async) |
| `session_history` | `GET /api/v1/sandboxes/{id}/sessions/{sid}/message` |
| `sandbox_terminate` | `DELETE /api/v1/sandboxes/{id}` |

**MCP session_message flow (uses prompt_async):**
1. Call `POST /session/{sid}/prompt_async` → 204
2. Subscribe to `GET /event` SSE channel
3. Collect events until `session.idle` event
4. Return complete response as tool result

**Active session limit handling:**
If `session_message` receives a 429 (active session limit reached on the workspace), the MCP tool returns a descriptive error to the caller:
```
Error: active session limit reached (5 active of 5 max). Wait for an active session to complete or close a session, then retry.
```
The MCP server does NOT retry automatically — the caller (an LLM agent) decides whether to wait and retry or inform the user.

If the `session.idle` event is not received within the configured timeout (default 300s from MCP config `default_timeout`), the tool polls `GET /session/{sid}/message` as a fallback to retrieve whatever response was generated, then returns it. This handles SSE event delivery failures.

**File transfer tools (sandbox_upload_file, sandbox_download_file) are deferred to V2.1.**

OpenCode v1.1.48 has `GET /file/content` for downloads but no upload endpoint. Adding file transfer requires either:
- A dedicated upload API endpoint using pod exec (requires retaining K8s exec capability)
- Waiting for opencode to add a file upload endpoint

For V1, MCP callers can instruct the agent to read/write files through the agent's own tools (the agent has filesystem access). This is the simpler path and aligns with the design principle that every interaction goes through the agent.

**Dependency:** `github.com/mark3labs/mcp-go`

## Design Reference

Section 4, 7.5, 7.5a (MCP File Transfer Implementation)

## Effort

Large (8-10 hours)
