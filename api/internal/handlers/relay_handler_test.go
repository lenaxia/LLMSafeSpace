// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/pkg/relay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// waitBothConnected polls until both agentd and client are registered in the
// relay handler for the given workspaceID. This is necessary because
// websocket.Dial returns as soon as the HTTP upgrade succeeds, but the
// server-side HandleRelay goroutine may not have stored the connection in
// room.agentd / room.client yet. Without this barrier, the first WriteMessage
// can fire before the target slot is set, and the relay handler silently
// drops messages (target==nil → no-op).
func waitBothConnected(t *testing.T, h *RelayHandler, workspaceID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return h.IsBothConnected(workspaceID)
	}, 2*time.Second, 5*time.Millisecond, "both participants did not connect within 2s")
}

func TestRelayHandler_NoUpgrade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRelayHandler(nil)

	router := gin.New()
	router.GET("/api/v1/workspaces/:id/relay", func(c *gin.Context) {
		c.Set("userID", "user1")
		h.HandleRelay(c)
	})

	// Regular HTTP request (no WebSocket upgrade)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws1/relay", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRelayHandler_TwoParticipants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRelayHandler(nil)

	router := gin.New()
	router.GET("/api/v1/workspaces/:id/relay", func(c *gin.Context) {
		c.Set("userID", "user1")
		h.HandleRelay(c)
	})

	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/workspaces/ws1/relay"

	// Connect agentd (participant 1)
	agentConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?role=agentd", nil)
	require.NoError(t, err)
	defer agentConn.Close()

	// Connect client (participant 2)
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?role=client", nil)
	require.NoError(t, err)
	defer clientConn.Close()

	// Wait for both connections to be registered server-side before sending.
	// Without this the agent may write before room.client is set, silently
	// dropping messages (the handler drops if target==nil).
	waitBothConnected(t, h, "ws1")

	// Agent sends a proxy request → should be delivered to client
	proxyReq := relay.ProxyRequest{
		Type:   relay.TypeProxyRequest,
		ID:     "req_1",
		Method: "POST",
		URL:    "https://opencode.ai/v1/chat/completions",
		Body:   `{"model":"test"}`,
	}
	data, _ := json.Marshal(proxyReq)
	require.NoError(t, agentConn.WriteMessage(websocket.TextMessage, data))

	// Client should receive the proxy request
	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := clientConn.ReadMessage()
	require.NoError(t, err)

	var received relay.ProxyRequest
	require.NoError(t, json.Unmarshal(msg, &received))
	assert.Equal(t, "req_1", received.ID)
	assert.Equal(t, relay.TypeProxyRequest, received.Type)

	// Client sends response back → should be delivered to agent
	respStart := relay.ProxyResponseStart{
		Type:   relay.TypeProxyResponseStart,
		ID:     "req_1",
		Status: 200,
	}
	respData, _ := json.Marshal(respStart)
	require.NoError(t, clientConn.WriteMessage(websocket.TextMessage, respData))

	agentConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, agentMsg, err := agentConn.ReadMessage()
	require.NoError(t, err)

	var receivedResp relay.ProxyResponseStart
	require.NoError(t, json.Unmarshal(agentMsg, &receivedResp))
	assert.Equal(t, "req_1", receivedResp.ID)
	assert.Equal(t, 200, receivedResp.Status)
}

func TestRelayHandler_StreamingChunks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRelayHandler(nil)

	router := gin.New()
	router.GET("/api/v1/workspaces/:id/relay", func(c *gin.Context) {
		c.Set("userID", "user1")
		h.HandleRelay(c)
	})

	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/workspaces/ws1/relay"

	agentConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?role=agentd", nil)
	require.NoError(t, err)
	defer agentConn.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?role=client", nil)
	require.NoError(t, err)
	defer clientConn.Close()

	waitBothConnected(t, h, "ws1")

	// Client sends multiple chunks → all should reach agent
	chunks := []string{"chunk1", "chunk2", "chunk3"}
	for _, c := range chunks {
		msg := relay.ProxyResponseChunk{
			Type: relay.TypeProxyResponseChunk,
			ID:   "req_1",
			Data: c,
		}
		data, _ := json.Marshal(msg)
		require.NoError(t, clientConn.WriteMessage(websocket.TextMessage, data))
	}

	// Agent should receive all chunks
	agentConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for _, expected := range chunks {
		_, msg, err := agentConn.ReadMessage()
		require.NoError(t, err)
		var chunk relay.ProxyResponseChunk
		require.NoError(t, json.Unmarshal(msg, &chunk))
		assert.Equal(t, expected, chunk.Data)
	}
}

func TestRelayHandler_ClientDisconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRelayHandler(nil)

	router := gin.New()
	router.GET("/api/v1/workspaces/:id/relay", func(c *gin.Context) {
		c.Set("userID", "user1")
		h.HandleRelay(c)
	})

	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/workspaces/ws1/relay"

	agentConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?role=agentd", nil)
	require.NoError(t, err)
	defer agentConn.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?role=client", nil)
	require.NoError(t, err)

	// Disconnect client
	clientConn.Close()
	time.Sleep(100 * time.Millisecond)

	// Agent sends a request — should not crash, relay delivers nothing
	proxyReq := relay.ProxyRequest{Type: relay.TypeProxyRequest, ID: "req_1", Method: "GET", URL: "https://opencode.ai/test"}
	data, _ := json.Marshal(proxyReq)
	// This should not panic or crash the server
	err = agentConn.WriteMessage(websocket.TextMessage, data)
	// Write may or may not error depending on timing
	_ = err
}

func TestRelayHandler_PingPong(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRelayHandler(nil)

	router := gin.New()
	router.GET("/api/v1/workspaces/:id/relay", func(c *gin.Context) {
		c.Set("userID", "user1")
		h.HandleRelay(c)
	})

	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/workspaces/ws1/relay"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL+"?role=client", nil)
	require.NoError(t, err)
	defer conn.Close()

	// Send ping
	ping := relay.Envelope{Type: relay.TypePing}
	data, _ := json.Marshal(ping)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	// Should receive pong
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)

	var env relay.Envelope
	require.NoError(t, json.Unmarshal(msg, &env))
	assert.Equal(t, relay.TypePong, env.Type)
}

func TestRelayHandler_MultipleWorkspaces(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRelayHandler(nil)

	router := gin.New()
	router.GET("/api/v1/workspaces/:id/relay", func(c *gin.Context) {
		c.Set("userID", "user1")
		h.HandleRelay(c)
	})

	server := httptest.NewServer(router)
	defer server.Close()

	baseURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/workspaces/"

	// Connect to workspace 1
	ws1Agent, _, err := websocket.DefaultDialer.Dial(baseURL+"ws1/relay?role=agentd", nil)
	require.NoError(t, err)
	defer ws1Agent.Close()

	ws1Client, _, err := websocket.DefaultDialer.Dial(baseURL+"ws1/relay?role=client", nil)
	require.NoError(t, err)
	defer ws1Client.Close()

	// Connect to workspace 2
	ws2Agent, _, err := websocket.DefaultDialer.Dial(baseURL+"ws2/relay?role=agentd", nil)
	require.NoError(t, err)
	defer ws2Agent.Close()

	ws2Client, _, err := websocket.DefaultDialer.Dial(baseURL+"ws2/relay?role=client", nil)
	require.NoError(t, err)
	defer ws2Client.Close()

	// Wait for all four connections to register before sending.
	waitBothConnected(t, h, "ws1")
	waitBothConnected(t, h, "ws2")

	// Message on ws1 should NOT go to ws2
	req := relay.ProxyRequest{Type: relay.TypeProxyRequest, ID: "ws1_req", Method: "POST", URL: "https://opencode.ai/test"}
	data, _ := json.Marshal(req)
	require.NoError(t, ws1Agent.WriteMessage(websocket.TextMessage, data))

	// ws1 client should receive it
	ws1Client.SetReadDeadline(time.Now().Add(time.Second))
	_, msg, err := ws1Client.ReadMessage()
	require.NoError(t, err)
	var received relay.Envelope
	require.NoError(t, json.Unmarshal(msg, &received))
	assert.Equal(t, "ws1_req", received.ID)

	// ws2 client should NOT receive it (timeout expected)
	ws2Client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err = ws2Client.ReadMessage()
	assert.Error(t, err) // timeout
}

func TestRelayHandler_ConcurrentMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRelayHandler(nil)

	router := gin.New()
	router.GET("/api/v1/workspaces/:id/relay", func(c *gin.Context) {
		c.Set("userID", "user1")
		h.HandleRelay(c)
	})

	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/workspaces/ws1/relay"

	agentConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?role=agentd", nil)
	require.NoError(t, err)
	defer agentConn.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?role=client", nil)
	require.NoError(t, err)
	defer clientConn.Close()

	// Wait for client to be registered before agent starts sending.
	waitBothConnected(t, h, "ws1")

	// Send 10 requests from agent sequentially (gorilla/websocket doesn't support concurrent writes)
	for i := 0; i < 10; i++ {
		req := relay.ProxyRequest{
			Type: relay.TypeProxyRequest,
			ID:   strings.Replace("req_X", "X", string(rune('0'+i)), 1),
		}
		data, _ := json.Marshal(req)
		require.NoError(t, agentConn.WriteMessage(websocket.TextMessage, data))
	}

	// Client should receive all 10
	received := 0
	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < 10; i++ {
		_, _, err := clientConn.ReadMessage()
		if err != nil {
			break
		}
		received++
	}
	assert.Equal(t, 10, received)
}

func TestRelayHandler_IsClientConnected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRelayHandler(nil)

	// No room exists
	assert.False(t, h.IsClientConnected("ws-nonexistent"))

	router := gin.New()
	router.GET("/api/v1/workspaces/:id/relay", func(c *gin.Context) {
		c.Set("userID", "user1")
		h.HandleRelay(c)
	})

	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/workspaces/ws1/relay"

	// Connect client
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?role=client", nil)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	assert.True(t, h.IsClientConnected("ws1"))

	// Disconnect
	clientConn.Close()
	time.Sleep(100 * time.Millisecond)
	assert.False(t, h.IsClientConnected("ws1"))
}

func TestRelayHandler_OwnershipEnforced(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Mock workspace getter that returns a workspace owned by "owner1"
	mockGetter := &mockRelayWSGetter{
		workspaces: map[string]string{
			"ws1": "owner1",
		},
	}
	h := NewRelayHandler(mockGetter)

	router := gin.New()
	router.GET("/api/v1/workspaces/:id/relay", func(c *gin.Context) {
		c.Set("userID", "attacker") // not the owner
		h.HandleRelay(c)
	})

	// Regular request (not WebSocket) — should get 404 because ownership check fails
	// before the upgrade attempt
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws1/relay?role=client", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// mockRelayWSGetter implements WorkspaceGetter for testing.
type mockRelayWSGetter struct {
	workspaces map[string]string // id → owner user-id
}

func (m *mockRelayWSGetter) GetWorkspace(id string) (*v1.Workspace, error) {
	owner, ok := m.workspaces[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &v1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   id,
			Labels: map[string]string{"user-id": owner},
		},
	}, nil
}
