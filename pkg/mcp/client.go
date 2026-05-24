// Package mcp implements the LLMSafeSpace MCP server.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIClient defines the interface for calling the LLMSafeSpace REST API.
// All operations are workspace-centric — the sandbox layer is internal.
type APIClient interface {
	CreateWorkspace(ctx context.Context, req CreateWorkspaceReq) (*WorkspaceResp, error)
	ActivateWorkspace(ctx context.Context, workspaceID string) (*ActivateResp, error)
	SuspendWorkspace(ctx context.Context, workspaceID string) error
	CreateSession(ctx context.Context, workspaceID string) (*SessionResp, error)
	GetHistory(ctx context.Context, workspaceID, sessionID string) ([]Message, error)
	SendMessage(ctx context.Context, workspaceID, sessionID, message string, timeout time.Duration) (string, error)
}

// CreateWorkspaceReq is the request body for workspace creation.
type CreateWorkspaceReq struct {
	Name    string `json:"name,omitempty"`
	Runtime string `json:"runtime"`
}

// WorkspaceResp is the response from workspace creation.
type WorkspaceResp struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Runtime string `json:"runtime"`
	Phase   string `json:"phase"`
}

// ActivateResp is the response from workspace activation.
type ActivateResp struct {
	Resumed   string `json:"resumed"`
	Suspended string `json:"suspended,omitempty"`
}

// SessionResp is the response from session creation.
type SessionResp struct {
	ID string `json:"id"`
}

// Message represents a chat message in session history.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// HTTPClient implements APIClient using HTTP calls to the LLMSafeSpace API.
// It resolves workspace → sandbox internally for session/message operations.
type HTTPClient struct {
	BaseURL    string
	HTTPClient *http.Client
	APIKey     string
}

func (c *HTTPClient) doJSON(ctx context.Context, method, path string, body any, result any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (c *HTTPClient) CreateWorkspace(ctx context.Context, req CreateWorkspaceReq) (*WorkspaceResp, error) {
	var resp WorkspaceResp
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/workspaces", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *HTTPClient) ActivateWorkspace(ctx context.Context, workspaceID string) (*ActivateResp, error) {
	var resp ActivateResp
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/activate", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *HTTPClient) SuspendWorkspace(ctx context.Context, workspaceID string) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/suspend", nil, nil)
}

// CreateSession resolves workspace → sandbox, then creates a session via the proxy.
func (c *HTTPClient) CreateSession(ctx context.Context, workspaceID string) (*SessionResp, error) {
	sandboxID, err := c.resolveSandbox(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	var resp SessionResp
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/sandboxes/"+sandboxID+"/sessions", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *HTTPClient) GetHistory(ctx context.Context, workspaceID, sessionID string) ([]Message, error) {
	sandboxID, err := c.resolveSandbox(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	var msgs []Message
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/sandboxes/"+sandboxID+"/sessions/"+sessionID+"/message", nil, &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

// SendMessage sends a prompt via prompt_async, then subscribes to SSE events
// until session.idle is received or timeout expires.
func (c *HTTPClient) SendMessage(ctx context.Context, workspaceID, sessionID, message string, timeout time.Duration) (string, error) {
	sandboxID, err := c.resolveSandbox(ctx, workspaceID)
	if err != nil {
		return "", err
	}

	// 1. Fire prompt_async
	body := map[string]string{"message": message}
	path := fmt.Sprintf("/api/v1/sandboxes/%s/sessions/%s/prompt", sandboxID, sessionID)
	if err := c.doJSON(ctx, http.MethodPost, path, body, nil); err != nil {
		return "", err
	}

	// 2. Subscribe to SSE events and wait for session.idle
	sseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	eventsURL := fmt.Sprintf("%s/api/v1/sandboxes/%s/events", c.BaseURL, sandboxID)
	req, err := http.NewRequestWithContext(sseCtx, http.MethodGet, eventsURL, nil)
	if err != nil {
		return "", fmt.Errorf("create SSE request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		// SSE failed or timed out — fall back to polling history
		return c.fallbackHistory(ctx, sandboxID, sessionID)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	var response strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var event struct {
				Type    string `json:"type"`
				Content string `json:"content"`
			}
			if json.Unmarshal([]byte(data), &event) == nil {
				if event.Type == "session.idle" {
					break
				}
				if event.Content != "" {
					response.WriteString(event.Content)
				}
			}
		}
	}

	if response.Len() > 0 {
		return response.String(), nil
	}

	// Fallback: poll history (using parent context, not the timed-out SSE context)
	return c.fallbackHistory(ctx, sandboxID, sessionID)
}

func (c *HTTPClient) fallbackHistory(ctx context.Context, sandboxID, sessionID string) (string, error) {
	var msgs []Message
	histPath := fmt.Sprintf("/api/v1/sandboxes/%s/sessions/%s/message", sandboxID, sessionID)
	if err := c.doJSON(ctx, http.MethodGet, histPath, nil, &msgs); err != nil {
		return "", fmt.Errorf("fallback history fetch: %w", err)
	}
	if len(msgs) > 0 {
		return msgs[len(msgs)-1].Content, nil
	}
	return "", nil
}

// resolveSandbox finds the active sandbox for a workspace via GET /workspaces/:id/sandboxes.
func (c *HTTPClient) resolveSandbox(ctx context.Context, workspaceID string) (string, error) {
	var sandboxes []struct {
		ID string `json:"id"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/sandboxes", nil, &sandboxes); err != nil {
		return "", fmt.Errorf("resolve sandbox for workspace %s: %w", workspaceID, err)
	}
	if len(sandboxes) == 0 {
		return "", fmt.Errorf("workspace %s has no active sandbox — call workspace_activate first", workspaceID)
	}
	return sandboxes[0].ID, nil
}
