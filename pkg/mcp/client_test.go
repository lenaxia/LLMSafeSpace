package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// ===== CreateSession =====

func TestHTTPClient_CreateSession_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/ws-1/sessions", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		json.NewEncoder(w).Encode(SessionResp{ID: "sess-1"})
	})

	client, ts := newTestHTTPClient(mux)
	defer ts.Close()

	resp, err := client.CreateSession(context.Background(), "ws-1")
	require.NoError(t, err)
	assert.Equal(t, "sess-1", resp.ID)
}

// ===== GetHistory =====

func TestHTTPClient_GetHistory_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/ws-1/sessions/sess-1/message", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/api/v1/workspaces/ws-1/sessions/sess-1/prompt", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/workspaces/ws-1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"type\":\"content\",\"content\":\"Hello \"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"type\":\"content\",\"content\":\"world!\"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"type\":\"session.status\",\"session_id\":\"sess-1\",\"status\":\"idle\"}\n\n")
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
	mux.HandleFunc("/api/v1/workspaces/ws-1/sessions/sess-1/prompt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/workspaces/ws-1/events", func(w http.ResponseWriter, r *http.Request) {
		// SSE stream closes immediately without session.idle
		w.Header().Set("Content-Type", "text/event-stream")
	})
	mux.HandleFunc("/api/v1/workspaces/ws-1/sessions/sess-1/message", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/api/v1/workspaces/ws-1/sessions/sess-1/prompt", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/api/v1/workspaces/ws-1/sessions/sess-1/prompt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/workspaces/ws-1/events", func(w http.ResponseWriter, r *http.Request) {
		// Block until context cancelled (simulates timeout)
		w.Header().Set("Content-Type", "text/event-stream")
		<-r.Context().Done()
	})
	mux.HandleFunc("/api/v1/workspaces/ws-1/sessions/sess-1/message", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/api/v1/workspaces/ws-1/sessions/sess-1/prompt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/workspaces/ws-1/events", func(w http.ResponseWriter, r *http.Request) {
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
		fmt.Fprintf(w, "data: {\"type\":\"session.status\",\"session_id\":\"sess-1\",\"status\":\"idle\"}\n\n")
		flusher.Flush()
	})

	client, ts := newTestHTTPClient(mux)
	defer ts.Close()

	resp, err := client.SendMessage(context.Background(), "ws-1", "sess-1", "hi", 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "answer", resp)
}

// ===== Input validation (path traversal) =====

func TestHTTPClient_InvalidSessionID(t *testing.T) {
	client := &HTTPClient{BaseURL: "http://localhost", HTTPClient: http.DefaultClient}

	_, err := client.GetHistory(context.Background(), "ws-1", "../../../etc/passwd")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid characters")
}

// ===== Message size limit =====

func TestHTTPClient_MessageTooLarge(t *testing.T) {
	client := &HTTPClient{BaseURL: "http://localhost", HTTPClient: http.DefaultClient}

	bigMessage := strings.Repeat("x", maxMessageSize+1)
	_, err := client.SendMessage(context.Background(), "ws-1", "sess-1", bigMessage, 5*time.Second)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "message too large")
}

// ===== Response body size limit =====

func TestHTTPClient_HugeResponseTruncated(t *testing.T) {
	// Server returns a response larger than maxResponseBody
	client, ts := newTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Write valid JSON start, then pad with spaces (won't parse as valid WorkspaceResp)
		w.Write([]byte(`{"id":"ws-1","runtime":"python:3.10","phase":"Active","name":"`))
		// Write enough to exceed limit
		for i := 0; i < maxResponseBody/1024; i++ {
			w.Write(bytes.Repeat([]byte("x"), 1024))
		}
		w.Write([]byte(`"}`))
	}))
	defer ts.Close()

	_, err := client.CreateWorkspace(context.Background(), CreateWorkspaceReq{Runtime: "python:3.10"})
	// Should fail with decode error (truncated JSON) rather than OOM
	assert.Error(t, err)
}

// ===== Error message sanitization =====

func TestHTTPClient_LongErrorTruncated(t *testing.T) {
	client, ts := newTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		// Simulate a stack trace leak
		w.Write([]byte(strings.Repeat("internal error details with paths /var/lib/secrets ", 100)))
	}))
	defer ts.Close()

	_, err := client.CreateWorkspace(context.Background(), CreateWorkspaceReq{Runtime: "python:3.10"})
	assert.Error(t, err)
	// Error should be truncated, not contain the full 5000+ char body
	assert.Less(t, len(err.Error()), 600)
	assert.Contains(t, err.Error(), "truncated")
}

// ===== US-16.0: SendMessage ignores idle for other sessions =====

func TestHTTPClient_SendMessage_IgnoresIdleForOtherSession(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/ws-1/sessions/sess-1/prompt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/workspaces/ws-1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// Idle for a DIFFERENT session — should be ignored
		fmt.Fprintf(w, "data: {\"type\":\"session.status\",\"session_id\":\"sess-OTHER\",\"status\":\"idle\"}\n\n")
		flusher.Flush()
		// Content for our session
		fmt.Fprintf(w, "data: {\"type\":\"content\",\"content\":\"result\"}\n\n")
		flusher.Flush()
		// Idle for OUR session — should break
		fmt.Fprintf(w, "data: {\"type\":\"session.status\",\"session_id\":\"sess-1\",\"status\":\"idle\"}\n\n")
		flusher.Flush()
	})

	client, ts := newTestHTTPClient(mux)
	defer ts.Close()

	resp, err := client.SendMessage(context.Background(), "ws-1", "sess-1", "hi", 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "result", resp)
}

func TestHTTPClient_SendMessage_IgnoresBusyEvents(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/ws-1/sessions/sess-1/prompt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/workspaces/ws-1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// Busy event — should be ignored
		fmt.Fprintf(w, "data: {\"type\":\"session.status\",\"session_id\":\"sess-1\",\"status\":\"busy\"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"type\":\"content\",\"content\":\"done\"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"type\":\"session.status\",\"session_id\":\"sess-1\",\"status\":\"idle\"}\n\n")
		flusher.Flush()
	})

	client, ts := newTestHTTPClient(mux)
	defer ts.Close()

	resp, err := client.SendMessage(context.Background(), "ws-1", "sess-1", "hi", 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "done", resp)
}

// ===== US-16.0: validID accepts underscores (opencode IDs) =====

func TestValidateID_AcceptsUnderscoreIDs(t *testing.T) {
	tests := []struct {
		id      string
		wantErr bool
	}{
		{"ses_18b28260affeoxXrX1iwPH8wFg", false},
		{"que_e74d7e6db001ZI3VDSHthsee0g", false},
		{"per_1748012345000_xyz", false},
		{"msg_e74d7da37001Nw4A59Ndzegm3A", false},
		{"sess-1", false},                // existing hyphen format still works
		{"ws.test.123", false},            // dots still work
		{"../etc/passwd", true},           // path traversal rejected
		{"", true},                        // empty rejected
		{".leading-dot", true},            // must start with alphanumeric
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			err := validateID(tt.id, "test_field")
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHTTPClient_GetHistory_AcceptsOpenCodeSessionID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspaces/ws-1/sessions/ses_18b28260affeoxXrX1iwPH8wFg/message", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]Message{{Role: "assistant", Content: "ok"}})
	})

	client, ts := newTestHTTPClient(mux)
	defer ts.Close()

	msgs, err := client.GetHistory(context.Background(), "ws-1", "ses_18b28260affeoxXrX1iwPH8wFg")
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
}
