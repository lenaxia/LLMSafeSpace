// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

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
		server.ServerTool{Tool: credentialCreateTool, Handler: h.credentialCreate},
		server.ServerTool{Tool: credentialListTool, Handler: h.credentialList},
		server.ServerTool{Tool: credentialDeleteTool, Handler: h.credentialDelete},
		server.ServerTool{Tool: modelListTool, Handler: h.modelList},
		server.ServerTool{Tool: modelSetTool, Handler: h.modelSet},
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
	if len(message) > maxMessageSize {
		return mcp.NewToolResultError(fmt.Sprintf("message too large (%d bytes, max %d)", len(message), maxMessageSize)), nil
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

// --- Credential & Model Tool definitions ---

var credentialCreateTool = mcp.NewTool("credential_create",
	mcp.WithDescription("Create an LLM provider credential. Optionally bind to a workspace."),
	mcp.WithString("provider", mcp.Required(), mcp.Description("Provider name (anthropic, openai, google, etc.)")),
	mcp.WithString("api_key", mcp.Required(), mcp.Description("Provider API key")),
	mcp.WithString("name", mcp.Description("Optional credential name (defaults to provider name)")),
	mcp.WithString("base_url", mcp.Description("Optional custom base URL for the provider")),
	mcp.WithString("default_model", mcp.Description("Optional default model ID (e.g. anthropic/claude-sonnet-4-5)")),
	mcp.WithString("workspace_id", mcp.Description("If set, auto-binds the credential to this workspace")),
)

var credentialListTool = mcp.NewTool("credential_list",
	mcp.WithDescription("List configured LLM provider credentials (names and IDs, never values)"),
)

var credentialDeleteTool = mcp.NewTool("credential_delete",
	mcp.WithDescription("Delete an LLM provider credential"),
	mcp.WithString("credential_id", mcp.Required(), mcp.Description("Credential ID to delete")),
)

var modelListTool = mcp.NewTool("model_list",
	mcp.WithDescription("List available models for a workspace (requires workspace to be active)"),
	mcp.WithString("workspace_id", mcp.Required(), mcp.Description("Workspace ID")),
)

var modelSetTool = mcp.NewTool("model_set",
	mcp.WithDescription("Set the default model for a workspace"),
	mcp.WithString("workspace_id", mcp.Required(), mcp.Description("Workspace ID")),
	mcp.WithString("model", mcp.Required(), mcp.Description("Model ID (e.g. anthropic/claude-sonnet-4-5)")),
)

// --- Credential & Model handlers ---

func (h *handlers) credentialCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	provider := strArg(args, "provider")
	apiKey := strArg(args, "api_key")
	if provider == "" || apiKey == "" {
		return mcp.NewToolResultError("provider and api_key are required"), nil
	}

	resp, err := h.client.CreateCredential(ctx, CreateCredentialReq{
		Name:        strArg(args, "name"),
		Provider:    provider,
		APIKey:      apiKey,
		BaseURL:     strArg(args, "base_url"),
		Default:     strArg(args, "default_model"),
		WorkspaceID: strArg(args, "workspace_id"),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create credential: %v", err)), nil
	}

	out, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(out)), nil
}

func (h *handlers) credentialList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	creds, err := h.client.ListCredentials(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list credentials: %v", err)), nil
	}
	out, _ := json.Marshal(creds)
	return mcp.NewToolResultText(string(out)), nil
}

func (h *handlers) credentialDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	credID := strArg(args, "credential_id")
	if credID == "" {
		return mcp.NewToolResultError("credential_id is required"), nil
	}

	if err := h.client.DeleteCredential(ctx, credID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to delete credential: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Credential %s deleted", credID)), nil
}

func (h *handlers) modelList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	workspaceID := strArg(args, "workspace_id")
	if workspaceID == "" {
		return mcp.NewToolResultError("workspace_id is required"), nil
	}

	models, err := h.client.ListModels(ctx, workspaceID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list models: %v", err)), nil
	}
	return mcp.NewToolResultText(string(models)), nil
}

func (h *handlers) modelSet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	workspaceID := strArg(args, "workspace_id")
	model := strArg(args, "model")
	if workspaceID == "" || model == "" {
		return mcp.NewToolResultError("workspace_id and model are required"), nil
	}

	if err := h.client.SetModel(ctx, workspaceID, model); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to set model: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Model set to %s", model)), nil
}
