package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewServer creates a configured MCP server with all LLMSafeSpace tools registered.
func NewServer(client APIClient, defaultTimeout time.Duration) *server.MCPServer {
	h := &handlers{client: client, timeout: defaultTimeout}

	srv := server.NewMCPServer(
		"LLMSafeSpace",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	srv.AddTools(
		server.ServerTool{Tool: sandboxCreateTool, Handler: h.sandboxCreate},
		server.ServerTool{Tool: sandboxTerminateTool, Handler: h.sandboxTerminate},
		server.ServerTool{Tool: sessionCreateTool, Handler: h.sessionCreate},
		server.ServerTool{Tool: sessionMessageTool, Handler: h.sessionMessage},
		server.ServerTool{Tool: sessionHistoryTool, Handler: h.sessionHistory},
	)

	return srv
}

type handlers struct {
	client  APIClient
	timeout time.Duration
}

// --- Tool definitions ---

var sandboxCreateTool = mcp.NewTool("sandbox_create",
	mcp.WithDescription("Create a sandbox with an opencode agent server"),
	mcp.WithString("runtime", mcp.Required(), mcp.Description("Runtime (python:3.10, nodejs:18, go:1.21)")),
	mcp.WithString("workspace_id", mcp.Description("Optional workspace to attach")),
	mcp.WithString("security_level", mcp.Description("standard or high")),
)

var sandboxTerminateTool = mcp.NewTool("sandbox_terminate",
	mcp.WithDescription("Terminate a sandbox"),
	mcp.WithString("sandbox_id", mcp.Required(), mcp.Description("Sandbox ID")),
)

var sessionCreateTool = mcp.NewTool("session_create",
	mcp.WithDescription("Create a conversation session in a sandbox"),
	mcp.WithString("sandbox_id", mcp.Required(), mcp.Description("Sandbox ID")),
)

var sessionMessageTool = mcp.NewTool("session_message",
	mcp.WithDescription("Send a message to an agent session and get a response"),
	mcp.WithString("sandbox_id", mcp.Required(), mcp.Description("Sandbox ID")),
	mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID")),
	mcp.WithString("message", mcp.Required(), mcp.Description("The message/prompt to send")),
)

var sessionHistoryTool = mcp.NewTool("session_history",
	mcp.WithDescription("Get the message history of a session"),
	mcp.WithString("sandbox_id", mcp.Required(), mcp.Description("Sandbox ID")),
	mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID")),
)

// --- Tool handlers ---

func (h *handlers) sandboxCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	runtime, _ := args["runtime"].(string)
	if runtime == "" {
		return mcp.NewToolResultError("runtime is required"), nil
	}

	apiReq := CreateSandboxReq{
		Runtime:       runtime,
		WorkspaceID:   strArg(args, "workspace_id"),
		SecurityLevel: strArg(args, "security_level"),
	}

	resp, err := h.client.CreateSandbox(ctx, apiReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create sandbox: %v", err)), nil
	}

	out, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(out)), nil
}

func (h *handlers) sandboxTerminate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	sandboxID, _ := args["sandbox_id"].(string)
	if sandboxID == "" {
		return mcp.NewToolResultError("sandbox_id is required"), nil
	}

	if err := h.client.TerminateSandbox(ctx, sandboxID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to terminate sandbox: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Sandbox %s terminated", sandboxID)), nil
}

func (h *handlers) sessionCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	sandboxID, _ := args["sandbox_id"].(string)
	if sandboxID == "" {
		return mcp.NewToolResultError("sandbox_id is required"), nil
	}

	resp, err := h.client.CreateSession(ctx, sandboxID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create session: %v", err)), nil
	}

	out, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(out)), nil
}

func (h *handlers) sessionMessage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	sandboxID, _ := args["sandbox_id"].(string)
	sessionID, _ := args["session_id"].(string)
	message, _ := args["message"].(string)

	if sandboxID == "" {
		return mcp.NewToolResultError("sandbox_id is required"), nil
	}
	if sessionID == "" {
		return mcp.NewToolResultError("session_id is required"), nil
	}
	if message == "" {
		return mcp.NewToolResultError("message is required"), nil
	}

	response, err := h.client.SendMessage(ctx, sandboxID, sessionID, message, h.timeout)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to send message: %v", err)), nil
	}

	return mcp.NewToolResultText(response), nil
}

func (h *handlers) sessionHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	sandboxID, _ := args["sandbox_id"].(string)
	sessionID, _ := args["session_id"].(string)

	if sandboxID == "" {
		return mcp.NewToolResultError("sandbox_id is required"), nil
	}
	if sessionID == "" {
		return mcp.NewToolResultError("session_id is required"), nil
	}

	msgs, err := h.client.GetHistory(ctx, sandboxID, sessionID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get history: %v", err)), nil
	}

	out, _ := json.Marshal(msgs)
	return mcp.NewToolResultText(string(out)), nil
}

func strArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}
