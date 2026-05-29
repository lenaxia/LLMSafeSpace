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
	"regexp"
	"strings"
	"time"
)

const (
	// maxResponseBody limits API response reads to 10MB to prevent OOM.
	maxResponseBody = 10 * 1024 * 1024
	// maxSSELineSize limits individual SSE event lines to 1MB.
	maxSSELineSize = 1 * 1024 * 1024
	// maxSSETotal limits total accumulated SSE content to 50MB.
	maxSSETotal = 50 * 1024 * 1024
	// requestTimeout is the default per-request timeout for non-SSE API calls.
	requestTimeout = 30 * time.Second
	// maxMessageSize limits the message body sent to session_message.
	maxMessageSize = 1 * 1024 * 1024 // 1MB
)

// validID matches safe identifiers (alphanumeric, hyphens, dots, underscores, max 253 chars).
// Underscores are required for opencode IDs (ses_abc, que_xyz, per_123).
var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._\-]{0,252}$`)

// validateID checks that an ID is safe to embed in a URL path.
func validateID(id, fieldName string) error {
	if id == "" {
		return fmt.Errorf("%s is required", fieldName)
	}
	if !validID.MatchString(id) {
		return fmt.Errorf("%s contains invalid characters", fieldName)
	}
	return nil
}

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
	// Apply per-request timeout if context has no deadline
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, requestTimeout)
		defer cancel()
	}

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

	// Limit response body reads to prevent OOM
	limited := io.LimitReader(resp.Body, maxResponseBody)

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(limited)
		// Sanitize: truncate long error bodies to avoid leaking internal details
		errMsg := string(respBody)
		if len(errMsg) > 512 {
			errMsg = errMsg[:512] + "...(truncated)"
		}
		return fmt.Errorf("API error %d: %s", resp.StatusCode, errMsg)
	}

	if result != nil {
		if err := json.NewDecoder(limited).Decode(result); err != nil {
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
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return nil, err
	}
	var resp ActivateResp
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/activate", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *HTTPClient) SuspendWorkspace(ctx context.Context, workspaceID string) error {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/suspend", nil, nil)
}

// CreateSession resolves workspace → sandbox, then creates a session via the proxy.
func (c *HTTPClient) CreateSession(ctx context.Context, workspaceID string) (*SessionResp, error) {
	var resp SessionResp
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/sessions", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *HTTPClient) GetHistory(ctx context.Context, workspaceID, sessionID string) ([]Message, error) {
	if err := validateID(sessionID, "session_id"); err != nil {
		return nil, err
	}
	var msgs []Message
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/sessions/"+sessionID+"/message", nil, &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

// SendMessage sends a prompt via prompt_async, then subscribes to SSE events
// until session.idle is received or timeout expires.
func (c *HTTPClient) SendMessage(ctx context.Context, workspaceID, sessionID, message string, timeout time.Duration) (string, error) {
	if err := validateID(sessionID, "session_id"); err != nil {
		return "", err
	}
	if len(message) > maxMessageSize {
		return "", fmt.Errorf("message too large (%d bytes, max %d)", len(message), maxMessageSize)
	}

	// 1. Fire prompt_async
	body := map[string]string{"message": message}
	path := fmt.Sprintf("/api/v1/workspaces/%s/sessions/%s/prompt", workspaceID, sessionID)
	if err := c.doJSON(ctx, http.MethodPost, path, body, nil); err != nil {
		return "", err
	}

	// 2. Subscribe to SSE events and wait for session.idle
	sseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	eventsURL := fmt.Sprintf("%s/api/v1/workspaces/%s/events", c.BaseURL, workspaceID)
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
		return c.fallbackHistory(ctx, workspaceID, sessionID)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELineSize)

	var response strings.Builder
	for scanner.Scan() {
		// Guard against unbounded accumulation
		if response.Len() > maxSSETotal {
			break
		}
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var event struct {
				Type      string          `json:"type"`
				SessionID string          `json:"session_id"`
				Status    string          `json:"status"`
				EventType string          `json:"event_type"`
				Content   string          `json:"content"`
				Data      json.RawMessage `json:"data"`
			}
			if json.Unmarshal([]byte(data), &event) == nil {
				// Detect session idle: direct session.status event from broker
				if event.Type == "session.status" && event.Status == "idle" && event.SessionID == sessionID {
					break
				}

				// Detect question asked: agent is waiting for user input
				if event.Type == "agent.question" {
					// Verify it's for our session by checking the data
					var qData struct {
						SessionID string `json:"session_id"`
					}
					if json.Unmarshal(event.Data, &qData) == nil && qData.SessionID == sessionID {
						// Return structured question result
						result := map[string]interface{}{
							"type":    "question",
							"request": json.RawMessage(event.Data),
						}
						out, _ := json.Marshal(result)
						return string(out), nil
					}
				}

				// Detect permission asked: auto-approve in headless mode
				if event.Type == "agent.permission" {
					var pData struct {
						ID        string `json:"id"`
						SessionID string `json:"session_id"`
					}
					if json.Unmarshal(event.Data, &pData) == nil && pData.SessionID == sessionID {
						go c.PermissionReply(ctx, workspaceID, pData.ID, "always", "")
					}
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
	return c.fallbackHistory(ctx, workspaceID, sessionID)
}

func (c *HTTPClient) fallbackHistory(ctx context.Context, workspaceID, sessionID string) (string, error) {
	var msgs []Message
	histPath := fmt.Sprintf("/api/v1/workspaces/%s/sessions/%s/message", workspaceID, sessionID)
	if err := c.doJSON(ctx, http.MethodGet, histPath, nil, &msgs); err != nil {
		return "", fmt.Errorf("fallback history fetch: %w", err)
	}
	if len(msgs) > 0 {
		return msgs[len(msgs)-1].Content, nil
	}
	return "", nil
}

// validQuestionID matches opencode question request IDs.
var validQuestionID = regexp.MustCompile(`^que_[a-zA-Z0-9]+$`)

// validPermissionID matches opencode permission request IDs.
var validPermissionID = regexp.MustCompile(`^per_[a-zA-Z0-9_]+$`)

func (c *HTTPClient) QuestionReply(ctx context.Context, workspaceID, requestID string, answers [][]string) error {
	if !validQuestionID.MatchString(requestID) {
		return fmt.Errorf("invalid question request ID: %s", requestID)
	}
	body := map[string]interface{}{"answers": answers}
	path := fmt.Sprintf("/api/v1/workspaces/%s/question/%s/reply", workspaceID, requestID)
	return c.doJSON(ctx, http.MethodPost, path, body, nil)
}

func (c *HTTPClient) QuestionReject(ctx context.Context, workspaceID, requestID string) error {
	if !validQuestionID.MatchString(requestID) {
		return fmt.Errorf("invalid question request ID: %s", requestID)
	}
	path := fmt.Sprintf("/api/v1/workspaces/%s/question/%s/reject", workspaceID, requestID)
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

func (c *HTTPClient) PermissionReply(ctx context.Context, workspaceID, requestID, reply, message string) error {
	if !validPermissionID.MatchString(requestID) {
		return fmt.Errorf("invalid permission request ID: %s", requestID)
	}
	validReplies := map[string]bool{"once": true, "always": true, "reject": true}
	if !validReplies[reply] {
		return fmt.Errorf("reply must be 'once', 'always', or 'reject'")
	}
	body := map[string]interface{}{"reply": reply}
	if message != "" {
		body["message"] = message
	}
	path := fmt.Sprintf("/api/v1/workspaces/%s/permission/%s/reply", workspaceID, requestID)
	return c.doJSON(ctx, http.MethodPost, path, body, nil)
}
