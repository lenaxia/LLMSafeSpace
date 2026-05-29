package llmsafespace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_ListWorkspaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workspaces" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer lsp_test" {
			t.Errorf("unexpected auth: %s", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(WorkspaceListResult{
			Items: []WorkspaceListItem{{ID: "ws-1", Name: "test"}},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, WithAPIKey("lsp_test"))
	result, err := c.Workspaces.List(context.Background(), 20, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != "ws-1" {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestClient_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "workspace not found"})
	}))
	defer srv.Close()

	c := New(srv.URL, WithAPIKey("lsp_test"))
	_, err := c.Workspaces.Get(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsNotFound(err) {
		t.Errorf("expected NotFound, got: %v", err)
	}
}

func TestClient_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "authentication required"})
	}))
	defer srv.Close()

	c := New(srv.URL, WithAPIKey("lsp_bad"))
	_, err := c.Auth.Me(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsAuth(err) {
		t.Errorf("expected Auth error, got: %v", err)
	}
}

func TestClient_SendMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"id":   "msg-1",
			"role": "assistant",
			"parts": []map[string]string{
				{"type": "text", "text": "Hello "},
				{"type": "text", "text": "world!"},
			},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, WithAPIKey("lsp_test"))
	resp, err := c.Sessions.SendMessage(context.Background(), "ws-1", "sess-1", "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Hello world!" {
		t.Errorf("expected 'Hello world!', got: %q", resp.Content)
	}
}

func TestClient_Suspend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
	}))
	defer srv.Close()

	c := New(srv.URL, WithAPIKey("lsp_test"))
	err := c.Workspaces.Suspend(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_TerminalTicket(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(TerminalTicket{Ticket: "tkt_abc", ExpiresAt: "2026-05-29T18:00:00Z"})
	}))
	defer srv.Close()

	c := New(srv.URL, WithAPIKey("lsp_test"))
	ticket, err := c.Terminal.GetTicket(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ticket.Ticket != "tkt_abc" {
		t.Errorf("expected tkt_abc, got: %s", ticket.Ticket)
	}
}

func TestClient_AutoLogin(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.URL.Path == "/api/v1/auth/login" {
			json.NewEncoder(w).Encode(map[string]any{"token": "jwt-abc"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer jwt-abc" {
			t.Errorf("expected jwt-abc token, got: %s", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(map[string]string{"id": "u1"})
	}))
	defer srv.Close()

	c := New(srv.URL, WithCredentials("test@example.com", "pass"))
	_, err := c.Auth.Me(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls (login + me), got %d", callCount)
	}
}
