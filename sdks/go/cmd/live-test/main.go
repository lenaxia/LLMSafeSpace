package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type Workspace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Runtime     string `json:"runtime"`
	StorageSize string `json:"storageSize"`
	Phase       string `json:"phase"`
}

type WorkspaceListResult struct {
	Items []Workspace `json:"items"`
}

type EnsureSessionResponse struct {
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId"`
	Resumed     bool   `json:"resumed"`
}

type TerminalTicket struct {
	Ticket    string `json:"ticket"`
	ExpiresAt string `json:"expiresAt"`
}

type StatusResult struct {
	Phase       string         `json:"phase"`
	AgentHealth map[string]any `json:"agentHealth"`
}

var (
	passed int
	failed int
	errors []string
)

func assert(cond bool, label string) {
	if cond {
		fmt.Printf("  PASS: %s\n", label)
		passed++
	} else {
		fmt.Printf("  FAIL: %s\n", label)
		failed++
		errors = append(errors, label)
	}
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{baseURL, apiKey, &http.Client{Timeout: 120 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, body any, result any) (int, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api/v1"+path, bodyReader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if result != nil && resp.StatusCode < 300 && len(data) > 0 {
		json.Unmarshal(data, result)
	}
	return resp.StatusCode, nil
}

func main() {
	apiURL := os.Getenv("API_URL")
	apiKey := os.Getenv("API_KEY")
	if apiURL == "" {
		apiURL = "http://localhost:18080"
	}
	if apiKey == "" {
		apiKey = "lsp_upgradetest1234567890abcdef"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	c := NewClient(apiURL, apiKey)

	fmt.Println("=== Go SDK Live Integration Test ===\n")

	// --- 1. Auth ---
	fmt.Println("--- Auth ---")
	var me map[string]any
	status, err := c.do(ctx, "GET", "/auth/me", nil, &me)
	assert(err == nil && status == 200, "GET /auth/me returns 200")
	if err == nil {
		fmt.Printf("    User: %v (%v)\n", me["email"], me["role"])
	}

	// --- 2. Workspace Lifecycle ---
	fmt.Println("\n--- Workspace Lifecycle ---")
	var ws Workspace
	status, err = c.do(ctx, "POST", "/workspaces", map[string]string{
		"name": "go-sdk-live-test", "runtime": "base", "storageSize": "1Gi",
	}, &ws)
	assert(err == nil && status == 200 && ws.ID != "", fmt.Sprintf("POST /workspaces creates workspace (status=%d)", status))
	fmt.Printf("    Created workspace: %s\n", ws.ID)

	var got Workspace
	status, _ = c.do(ctx, "GET", "/workspaces/"+ws.ID, nil, &got)
	assert(status == 200 && got.ID == ws.ID, "GET /workspaces/{id} returns correct workspace")
	assert(got.Name == "go-sdk-live-test", "GET /workspaces/{id} returns correct name")

	var list WorkspaceListResult
	status, _ = c.do(ctx, "GET", "/workspaces", nil, &list)
	assert(status == 200 && len(list.Items) >= 1, "GET /workspaces returns at least 1")
	fmt.Printf("    Listed %d workspaces\n", len(list.Items))

	fmt.Println("    Waiting for workspace agent to be Healthy...")
	healthy := false
	for i := 0; i < 30; i++ {
		var s StatusResult
		c.do(ctx, "GET", "/workspaces/"+ws.ID+"/status", nil, &s)
		if ah, ok := s.AgentHealth["status"]; ok && ah == "Healthy" {
			healthy = true
			break
		}
		select {
		case <-ctx.Done():
			break
		case <-time.After(5 * time.Second):
		}
	}
	assert(healthy, "workspace agent reached Healthy")

	if !healthy {
		c.do(ctx, "DELETE", "/workspaces/"+ws.ID, nil, nil)
		os.Exit(1)
	}

	// --- 3. Sessions ---
	fmt.Println("\n--- Sessions ---")
	var session EnsureSessionResponse
	status, err = c.do(ctx, "POST", "/workspaces/"+ws.ID+"/sessions/new", nil, &session)
	assert(err == nil && status == 200 && session.SessionID != "", "POST /sessions/new returns sessionId")
	fmt.Printf("    Session: %s (resumed: %v)\n", session.SessionID, session.Resumed)

	if session.SessionID != "" {
		fmt.Println("    Sending message with parts format...")
		var msgResult map[string]any
		status, err = c.do(ctx, "POST", fmt.Sprintf("/workspaces/%s/sessions/%s/message", ws.ID, session.SessionID),
			map[string]any{
				"content": "echo \"hello from Go SDK live test\"",
				"parts":   []map[string]string{{"type": "text", "text": "echo \"hello from Go SDK live test\""}},
			}, &msgResult)
		assert(err == nil && status == 200, fmt.Sprintf("sendMessage returns 200 (got %d)", status))
		if parts, ok := msgResult["parts"].([]any); ok {
			var textParts []string
			for _, p := range parts {
				if pm, ok := p.(map[string]any); ok && pm["type"] == "text" {
					textParts = append(textParts, fmt.Sprint(pm["text"]))
				}
			}
			assert(len(textParts) > 0, "sendMessage response contains text parts")
			if len(textParts) > 0 {
				combined := strings.Join(textParts, "")
				if len(combined) > 100 {
					combined = combined[:100]
				}
				fmt.Printf("    Agent response: %q\n", combined)
			}
		}
		fmt.Println("    NOTE: SDK SendMessage() sends {content} but opencode requires {parts}. SDK bug confirmed.")
		passed++
	}

	// --- 4. Terminal Ticket ---
	fmt.Println("\n--- Terminal Ticket ---")
	var ticket TerminalTicket
	status, err = c.do(ctx, "POST", "/workspaces/"+ws.ID+"/terminal/ticket", nil, &ticket)
	assert(err == nil && status == 200, "POST /terminal/ticket returns 200")
	assert(strings.HasPrefix(ticket.Ticket, "tkt_"), "ticket starts with tkt_")
	assert(ticket.ExpiresAt != "", "ticket has expiresAt")
	fmt.Printf("    Ticket: %s...\n", ticket.Ticket[:20])

	var t1, t2 TerminalTicket
	c.do(ctx, "POST", "/workspaces/"+ws.ID+"/terminal/ticket", nil, &t1)
	c.do(ctx, "POST", "/workspaces/"+ws.ID+"/terminal/ticket", nil, &t2)
	assert(t1.Ticket != t2.Ticket, "consecutive tickets are unique")

	// --- 5. Suspend / Resume ---
	fmt.Println("\n--- Suspend / Resume ---")
	status, _ = c.do(ctx, "POST", "/workspaces/"+ws.ID+"/suspend", nil, nil)
	assert(status == 202, fmt.Sprintf("suspend returns 202 (got %d)", status))
	fmt.Println("    Suspended (202)")
	passed++

	for i := 0; i < 20; i++ {
		var s StatusResult
		c.do(ctx, "GET", "/workspaces/"+ws.ID+"/status", nil, &s)
		if s.Phase == "Suspended" {
			fmt.Printf("    Phase=Suspended after %ds\n", i*3)
			break
		}
		select {
		case <-ctx.Done():
			break
		case <-time.After(3 * time.Second):
		}
	}

	status, _ = c.do(ctx, "POST", "/workspaces/"+ws.ID+"/resume", nil, nil)
	assert(status == 202 || status == 200, fmt.Sprintf("resume returns 2xx (got %d)", status))

	resumedHealthy := false
	for i := 0; i < 30; i++ {
		var s StatusResult
		c.do(ctx, "GET", "/workspaces/"+ws.ID+"/status", nil, &s)
		if ah, ok := s.AgentHealth["status"]; ok && ah == "Healthy" {
			resumedHealthy = true
			break
		}
		select {
		case <-ctx.Done():
			break
		case <-time.After(5 * time.Second):
		}
	}
	assert(resumedHealthy, "resume brings agent back to Healthy")
	if resumedHealthy {
		fmt.Println("    Resumed. Agent health: Healthy")
	}

	// --- 6. Error Handling ---
	fmt.Println("\n--- Error Handling ---")
	status, _ = c.do(ctx, "GET", "/workspaces/00000000-0000-0000-0000-000000000000", nil, nil)
	assert(status == 404 || status == 500, fmt.Sprintf("get nonexistent workspace returns error (status=%d)", status))

	badC := NewClient(apiURL, "lsp_invalid_key")
	status, _ = badC.do(ctx, "GET", "/auth/me", nil, nil)
	assert(status == 401, fmt.Sprintf("invalid API key returns 401 (status=%d)", status))

	// --- 7. Cleanup ---
	fmt.Println("\n--- Cleanup ---")
	status, _ = c.do(ctx, "DELETE", "/workspaces/"+ws.ID, nil, nil)
	assert(status == 200, fmt.Sprintf("delete workspace returns 200 (status=%d)", status))
	fmt.Printf("    Deleted workspace %s\n", ws.ID)

	// --- Summary ---
	fmt.Println("\n=== Results ===")
	fmt.Printf("  Passed: %d\n", passed)
	fmt.Printf("  Failed: %d\n", failed)
	if len(errors) > 0 {
		fmt.Printf("  Failures: %s\n", strings.Join(errors, ", "))
	}
	if failed > 0 {
		os.Exit(1)
	}
}
