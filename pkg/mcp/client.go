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
type APIClient interface {
	CreateSandbox(ctx context.Context, req CreateSandboxReq) (*SandboxResp, error)
	TerminateSandbox(ctx context.Context, sandboxID string) error
	CreateSession(ctx context.Context, sandboxID string) (*SessionResp, error)
	GetHistory(ctx context.Context, sandboxID, sessionID string) ([]Message, error)
	SendMessage(ctx context.Context, sandboxID, sessionID, message string, timeout time.Duration) (string, error)
}

// CreateSandboxReq is the request body for sandbox creation.
type CreateSandboxReq struct {
	Runtime       string `json:"runtime"`
	WorkspaceID   string `json:"workspaceId,omitempty"`
	SecurityLevel string `json:"securityLevel,omitempty"`
}

// SandboxResp is the response from sandbox creation.
type SandboxResp struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Runtime string `json:"runtime"`
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

func (c *HTTPClient) CreateSandbox(ctx context.Context, req CreateSandboxReq) (*SandboxResp, error) {
	var resp SandboxResp
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/sandboxes", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *HTTPClient) TerminateSandbox(ctx context.Context, sandboxID string) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/sandboxes/"+sandboxID, nil, nil)
}

func (c *HTTPClient) CreateSession(ctx context.Context, sandboxID string) (*SessionResp, error) {
	var resp SessionResp
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/sandboxes/"+sandboxID+"/sessions", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *HTTPClient) GetHistory(ctx context.Context, sandboxID, sessionID string) ([]Message, error) {
	var msgs []Message
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/sandboxes/"+sandboxID+"/sessions/"+sessionID+"/message", nil, &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

// SendMessage sends a prompt via prompt_async, then subscribes to SSE events
// until session.idle is received or timeout expires.
func (c *HTTPClient) SendMessage(ctx context.Context, sandboxID, sessionID, message string, timeout time.Duration) (string, error) {
	// 1. Fire prompt_async
	body := map[string]string{"message": message}
	path := fmt.Sprintf("/api/v1/sandboxes/%s/sessions/%s/prompt", sandboxID, sessionID)
	if err := c.doJSON(ctx, http.MethodPost, path, body, nil); err != nil {
		return "", err
	}

	// 2. Subscribe to SSE events and wait for session.idle
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	eventsURL := fmt.Sprintf("%s/api/v1/sandboxes/%s/events", c.BaseURL, sandboxID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsURL, nil)
	if err != nil {
		return "", fmt.Errorf("create SSE request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("SSE connect failed: %w", err)
	}
	defer resp.Body.Close()

	// Read SSE events until session.idle
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

	// If we got content from SSE, return it
	if response.Len() > 0 {
		return response.String(), nil
	}

	// Fallback: poll history
	msgs, err := c.GetHistory(ctx, sandboxID, sessionID)
	if err != nil {
		return "", fmt.Errorf("fallback history fetch: %w", err)
	}
	if len(msgs) > 0 {
		return msgs[len(msgs)-1].Content, nil
	}
	return "", nil
}
