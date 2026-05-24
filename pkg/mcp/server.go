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
// Tools are workspace-centric — the sandbox layer is hidden from callers.
func NewServer(client APIClient, defaultTimeout time.Duration) *server.MCPServer {
	h := &handlers{client: client, timeout: defaultTimeout}

	srv := server.NewMCPServer(
		"LLMSafeSpace",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	srv.AddTools(
		server.ServerTool{Tool: workspaceCreateTool, Handler: h.workspaceCreate},
		server.ServerTool{Tool: workspaceActivateTool, Handler: h.workspaceActivate},
		server.ServerTool{Tool: workspaceStopTool, Handler: h.workspaceStop},
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

var workspaceCreateTool = mcp.NewTool("workspace_create",
	mcp.WithDescription("Create a new workspace with a persistent development environment"),
	mcp.WithString("runtime", mcp.Required(), mcp.Description("Runtime (python:3.10, nodejs:18, go:1.21)")),
	mcp.WithString("name", mcp.Description("Optional workspace name")),
)

var workspaceActivateTool = mcp.NewTool("workspace_activate",
	mcp.WithDescription("Activate a workspace (starts the agent). Required before creating sessions."),
	mcp.WithString("workspace_id", mcp.Required(), mcp.Description("Workspace ID")),
)

var workspaceStopTool = mcp.NewTool("workspace_stop",
	mcp.WithDescription("Stop a workspace (suspends the agent, preserves all files)"),
	mcp.WithString("workspace_id", mcp.Required(), mcp.Description("Workspace ID")),
)

var sessionCreateTool = mcp.NewTool("session_create",
	mcp.WithDescription("Create a conversation session in an active workspace"),
	mcp.WithString("workspace_id", mcp.Required(), mcp.Description("Workspace ID")),
)

var sessionMessageTool = mcp.NewTool("session_message",
	mcp.WithDescription("Send a message to an agent session and get a response"),
	mcp.WithString("workspace_id", mcp.Required(), mcp.Description("Workspace ID")),
	mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID")),
	mcp.WithString("message", mcp.Required(), mcp.Description("The message/prompt to send")),
)

var sessionHistoryTool = mcp.NewTool("session_history",
	mcp.WithDescription("Get the message history of a session"),
	mcp.WithString("workspace_id", mcp.Required(), mcp.Description("Workspace ID")),
	mcp.WithString("session_id", mcp.Required(), mcp.Description("Session ID")),
)

// --- Tool handlers ---

func (h *handlers) workspaceCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	runtime, _ := args["runtime"].(string)
	if runtime == "" {
		return mcp.NewToolResultError("runtime is required"), nil
	}

	apiReq := CreateWorkspaceReq{
		Runtime: runtime,
		Name:    strArg(args, "name"),
	}

	resp, err := h.client.CreateWorkspace(ctx, apiReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create workspace: %v", err)), nil
	}

	out, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(out)), nil
}

func (h *handlers) workspaceActivate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	workspaceID, _ := args["workspace_id"].(string)
	if workspaceID == "" {
		return mcp.NewToolResultError("workspace_id is required"), nil
	}

	resp, err := h.client.ActivateWorkspace(ctx, workspaceID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to activate workspace: %v", err)), nil
	}

	out, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(out)), nil
}

func (h *handlers) workspaceStop(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	workspaceID, _ := args["workspace_id"].(string)
	if workspaceID == "" {
		return mcp.NewToolResultError("workspace_id is required"), nil
	}

	if err := h.client.SuspendWorkspace(ctx, workspaceID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to stop workspace: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Workspace %s stopped (files preserved)", workspaceID)), nil
}

func (h *handlers) sessionCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	workspaceID, _ := args["workspace_id"].(string)
	if workspaceID == "" {
		return mcp.NewToolResultError("workspace_id is required"), nil
	}

	resp, err := h.client.CreateSession(ctx, workspaceID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create session: %v", err)), nil
	}

	out, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(out)), nil
}

func (h *handlers) sessionMessage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	workspaceID, _ := args["workspace_id"].(string)
	sessionID, _ := args["session_id"].(string)
	message, _ := args["message"].(string)

	if workspaceID == "" {
		return mcp.NewToolResultError("workspace_id is required"), nil
	}
	if sessionID == "" {
		return mcp.NewToolResultError("session_id is required"), nil
	}
	if message == "" {
		return mcp.NewToolResultError("message is required"), nil
	}

	response, err := h.client.SendMessage(ctx, workspaceID, sessionID, message, h.timeout)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to send message: %v", err)), nil
	}

	return mcp.NewToolResultText(response), nil
}

func (h *handlers) sessionHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	workspaceID, _ := args["workspace_id"].(string)
	sessionID, _ := args["session_id"].(string)

	if workspaceID == "" {
		return mcp.NewToolResultError("workspace_id is required"), nil
	}
	if sessionID == "" {
		return mcp.NewToolResultError("session_id is required"), nil
	}

	msgs, err := h.client.GetHistory(ctx, workspaceID, sessionID)
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
