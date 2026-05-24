package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestHTTPClient(handler http.Handler) (*HTTPClient, *httptest.Server) {
	ts := httptest.NewServer(handler)
	client := &HTTPClient{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
		APIKey:     "test-key",
	}
	return client, ts
}

// ===== CreateWorkspace =====

func TestHTTPClient_CreateWorkspace_HappyPath(t *testing.T) {
	client, ts := newTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/api/v1/workspaces", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		var req CreateWorkspaceReq
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "python:3.10", req.Runtime)

		json.NewEncoder(w).Encode(WorkspaceResp{ID: "ws-1", Runtime: "python:3.10", Phase: "Active"})
	}))
	defer ts.Close()

	resp, err := client.CreateWorkspace(context.Background(), CreateWorkspaceReq{Runtime: "python:3.10"})
	require.NoError(t, err)
	assert.Equal(t, "ws-1", resp.ID)
}

func TestHTTPClient_CreateWorkspace_APIError(t *testing.T) {
	client, ts := newTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"workspace limit reached"}`))
	}))
	defer ts.Close()

	_, err := client.CreateWorkspace(context.Background(), CreateWorkspaceReq{Runtime: "python:3.10"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "409")
	assert.Contains(t, err.Error(), "workspace limit reached")
}

// ===== ActivateWorkspace =====

func TestHTTPClient_ActivateWorkspace_HappyPath(t *testing.T) {
	client, ts := newTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/api/v1/workspaces/ws-1/activate", r.URL.Path)
		json.NewEncoder(w).Encode(ActivateResp{Resumed: "ws-1"})
	}))
	defer ts.Close()

	resp, err := client.ActivateWorkspace(context.Background(), "ws-1")
	require.NoError(t, err)
	assert.Equal(t, "ws-1", resp.Resumed)
}

// ===== SuspendWorkspace =====

func TestHTTPClient_SuspendWorkspace_HappyPath(t *testing.T) {
	client, ts := newTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/api/v1/workspaces/ws-1/suspend", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	err := client.SuspendWorkspace(context.Background(), "ws-1")
	assert.NoError(t, err)
}

// ===== resolveSandbox =====

func TestHTTPClient_ResolveSandbox_HappyPath(t *testing.T) {
	client, ts := newTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/workspaces/ws-1/sandboxes", r.URL.Path)
		json.NewEncoder(w).Encode([]struct{ ID string }{{ID: "sb-99"}})
	}))
	defer ts.Close()

	id, err := client.resolveSandbox(context.Background(), "ws-1")
	require.NoError(t, err)
	assert.Equal(t, "sb-99", id)
}

func TestHTTPClient_ResolveSandbox_NoActiveSandbox(t *testing.T) {
	client, ts := newTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]struct{ ID string }{})
	}))
	defer ts.Close()

	_, err := client.resolveSandbox(context.Background(), "ws-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no active sandbox")
	assert.Contains(t, err.Error(), "workspace_activate")
}

// ===== CreateSession =====

func TestHTTPClient_CreateSession_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/ws-1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]struct{ ID string }{{ID: "sb-99"}})
	})
	mux.HandleFunc("/api/v1/sandboxes/sb-99/sessions", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		json.NewEncoder(w).Encode(SessionResp{ID: "sess-1"})
	})

	client, ts := newTestHTTPClient(mux)
	defer ts.Close()

	resp, err := client.CreateSession(context.Background(), "ws-1")
	require.NoError(t, err)
	assert.Equal(t, "sess-1", resp.ID)
}

func TestHTTPClient_CreateSession_WorkspaceNotActive(t *testing.T) {
	client, ts := newTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]struct{ ID string }{})
	}))
	defer ts.Close()

	_, err := client.CreateSession(context.Background(), "ws-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no active sandbox")
}

// ===== GetHistory =====

func TestHTTPClient_GetHistory_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/ws-1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]struct{ ID string }{{ID: "sb-99"}})
	})
	mux.HandleFunc("/api/v1/sandboxes/sb-99/sessions/sess-1/message", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		json.NewEncoder(w).Encode([]Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		})
	})

	client, ts := newTestHTTPClient(mux)
	defer ts.Close()

	msgs, err := client.GetHistory(context.Background(), "ws-1", "sess-1")
	require.NoError(t, err)
	assert.Len(t, msgs, 2)
	assert.Equal(t, "hello", msgs[1].Content)
}

// ===== SendMessage =====

func TestHTTPClient_SendMessage_SSEResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/ws-1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]struct{ ID string }{{ID: "sb-99"}})
	})
	mux.HandleFunc("/api/v1/sandboxes/sb-99/sessions/sess-1/prompt", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/sandboxes/sb-99/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"type\":\"content\",\"content\":\"Hello \"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"type\":\"content\",\"content\":\"world!\"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"type\":\"session.idle\"}\n\n")
		flusher.Flush()
	})

	client, ts := newTestHTTPClient(mux)
	defer ts.Close()

	resp, err := client.SendMessage(context.Background(), "ws-1", "sess-1", "hi", 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "Hello world!", resp)
}

func TestHTTPClient_SendMessage_FallbackToHistory(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/ws-1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]struct{ ID string }{{ID: "sb-99"}})
	})
	mux.HandleFunc("/api/v1/sandboxes/sb-99/sessions/sess-1/prompt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/sandboxes/sb-99/events", func(w http.ResponseWriter, r *http.Request) {
		// SSE stream closes immediately without session.idle
		w.Header().Set("Content-Type", "text/event-stream")
	})
	mux.HandleFunc("/api/v1/sandboxes/sb-99/sessions/sess-1/message", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "fallback response"},
		})
	})

	client, ts := newTestHTTPClient(mux)
	defer ts.Close()

	resp, err := client.SendMessage(context.Background(), "ws-1", "sess-1", "hi", 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "fallback response", resp)
}

func TestHTTPClient_SendMessage_PromptReturns429(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/ws-1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]struct{ ID string }{{ID: "sb-99"}})
	})
	mux.HandleFunc("/api/v1/sandboxes/sb-99/sessions/sess-1/prompt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"active session limit reached"}`))
	})

	client, ts := newTestHTTPClient(mux)
	defer ts.Close()

	_, err := client.SendMessage(context.Background(), "ws-1", "sess-1", "hi", 5*time.Second)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "429")
}

func TestHTTPClient_SendMessage_Timeout(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/ws-1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]struct{ ID string }{{ID: "sb-99"}})
	})
	mux.HandleFunc("/api/v1/sandboxes/sb-99/sessions/sess-1/prompt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/sandboxes/sb-99/events", func(w http.ResponseWriter, r *http.Request) {
		// Block until context cancelled (simulates timeout)
		w.Header().Set("Content-Type", "text/event-stream")
		<-r.Context().Done()
	})
	mux.HandleFunc("/api/v1/sandboxes/sb-99/sessions/sess-1/message", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]Message{{Role: "assistant", Content: "timeout fallback"}})
	})

	client, ts := newTestHTTPClient(mux)
	defer ts.Close()

	resp, err := client.SendMessage(context.Background(), "ws-1", "sess-1", "hi", 100*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, "timeout fallback", resp)
}

// ===== Context cancellation =====

func TestHTTPClient_ContextCancelled(t *testing.T) {
	client, ts := newTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // will be cancelled
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := client.CreateWorkspace(ctx, CreateWorkspaceReq{Runtime: "python:3.10"})
	assert.Error(t, err)
}

// ===== Malformed responses =====

func TestHTTPClient_MalformedJSONResponse(t *testing.T) {
	client, ts := newTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`<html>502 Bad Gateway</html>`))
	}))
	defer ts.Close()

	_, err := client.CreateWorkspace(context.Background(), CreateWorkspaceReq{Runtime: "python:3.10"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decode response")
}

// ===== SSE with keepalive comments and retry directives =====

func TestHTTPClient_SendMessage_SSEWithKeepalives(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/ws-1/sandboxes", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]struct{ ID string }{{ID: "sb-99"}})
	})
	mux.HandleFunc("/api/v1/sandboxes/sb-99/sessions/sess-1/prompt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/sandboxes/sb-99/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// Real SSE streams have comments, retry directives, and event types
		fmt.Fprintf(w, ":keepalive\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "retry: 3000\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"type\":\"content\",\"content\":\"answer\"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: not-json-at-all\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"type\":\"session.idle\"}\n\n")
		flusher.Flush()
	})

	client, ts := newTestHTTPClient(mux)
	defer ts.Close()

	resp, err := client.SendMessage(context.Background(), "ws-1", "sess-1", "hi", 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "answer", resp)
}
