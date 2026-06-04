// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/lenaxia/llmsafespace/pkg/relay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRelayIntegration_EndToEnd wires together:
//   - A fake "provider API" (simulates opencode.ai)
//   - The API server's relay handler (real)
//   - An "agentd" WebSocket client that sends proxy requests
//   - A "browser" WebSocket client that executes the requests
//
// This proves the full relay path works without mocks.
func TestRelayIntegration_EndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 1. Fake provider API that returns a streaming response
	providerHits := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerHits++
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			_, _ = w.Write([]byte("data: {\"chunk\":" + string(rune('0'+i)) + "}\n\n"))
			flusher.Flush()
		}
	}))
	defer provider.Close()

	// 2. Real relay handler
	h := NewRelayHandler(nil) // nil wsGetter for test (ownership skipped)

	router := gin.New()
	router.GET("/api/v1/workspaces/:id/relay", func(c *gin.Context) {
		c.Set("userID", "user1")
		h.HandleRelay(c)
	})

	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/workspaces/ws1/relay"

	// 3. Connect "agentd" participant
	agentConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?role=agentd", nil)
	require.NoError(t, err)
	defer agentConn.Close()

	// 4. Connect "browser" participant (the relay client)
	browserConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?role=client", nil)
	require.NoError(t, err)
	defer browserConn.Close()

	// 5. Browser reads requests and executes them (simulates useRelayClient)
	go func() {
		for {
			_, msg, err := browserConn.ReadMessage()
			if err != nil {
				return
			}
			var env relay.Envelope
			if json.Unmarshal(msg, &env) != nil {
				continue
			}
			if env.Type != relay.TypeProxyRequest {
				continue
			}
			var req relay.ProxyRequest
			if json.Unmarshal(msg, &req) != nil {
				continue
			}

			// Execute the HTTP request to the provider
			httpReq, _ := http.NewRequest(req.Method, req.URL, strings.NewReader(req.Body))
			for k, v := range req.Headers {
				httpReq.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(httpReq)
			if err != nil {
				errMsg, _ := json.Marshal(relay.ProxyError{
					Type: relay.TypeProxyError, ID: req.ID, Error: err.Error(),
				})
				_ = browserConn.WriteMessage(websocket.TextMessage, errMsg)
				continue
			}

			// Send response start
			start, _ := json.Marshal(relay.ProxyResponseStart{
				Type: relay.TypeProxyResponseStart, ID: req.ID, Status: resp.StatusCode,
				Headers: map[string]string{"content-type": resp.Header.Get("Content-Type")},
			})
			_ = browserConn.WriteMessage(websocket.TextMessage, start)

			// Stream body
			buf := make([]byte, 4096)
			for {
				n, readErr := resp.Body.Read(buf)
				if n > 0 {
					chunk, _ := json.Marshal(relay.ProxyResponseChunk{
						Type: relay.TypeProxyResponseChunk, ID: req.ID, Data: string(buf[:n]),
					})
					_ = browserConn.WriteMessage(websocket.TextMessage, chunk)
				}
				if readErr != nil {
					break
				}
			}
			resp.Body.Close()

			// Send end
			end, _ := json.Marshal(relay.ProxyResponseEnd{
				Type: relay.TypeProxyResponseEnd, ID: req.ID,
			})
			_ = browserConn.WriteMessage(websocket.TextMessage, end)
		}
	}()

	// Give browser time to start reading
	time.Sleep(50 * time.Millisecond)

	// 6. Agentd sends a proxy request (simulates what relay_proxy.go does)
	proxyReq := relay.ProxyRequest{
		Type:    relay.TypeProxyRequest,
		ID:      "req_integration_1",
		Method:  "POST",
		URL:     provider.URL + "/v1/chat/completions",
		Headers: map[string]string{"content-type": "application/json"},
		Body:    `{"model":"test","messages":[]}`,
	}
	data, _ := json.Marshal(proxyReq)
	require.NoError(t, agentConn.WriteMessage(websocket.TextMessage, data))

	// 7. Agentd reads the response (forwarded from browser via relay)
	agentConn.SetReadDeadline(time.Now().Add(5 * time.Second))

	var responses []json.RawMessage
	for {
		_, msg, err := agentConn.ReadMessage()
		if err != nil {
			break
		}
		responses = append(responses, msg)
		var env relay.Envelope
		_ = json.Unmarshal(msg, &env)
		if env.Type == relay.TypeProxyResponseEnd || env.Type == relay.TypeProxyError {
			break
		}
	}

	// 8. Verify the full response chain
	require.True(t, len(responses) >= 3, "expected at least start + chunk + end, got %d", len(responses))

	// First message should be response_start with status 200
	var start relay.ProxyResponseStart
	require.NoError(t, json.Unmarshal(responses[0], &start))
	assert.Equal(t, relay.TypeProxyResponseStart, start.Type)
	assert.Equal(t, 200, start.Status)
	assert.Equal(t, "req_integration_1", start.ID)

	// Last message should be response_end
	var end relay.Envelope
	require.NoError(t, json.Unmarshal(responses[len(responses)-1], &end))
	assert.Equal(t, relay.TypeProxyResponseEnd, end.Type)

	// Middle messages should be chunks containing the SSE data
	var allData string
	for _, msg := range responses[1 : len(responses)-1] {
		var chunk relay.ProxyResponseChunk
		if json.Unmarshal(msg, &chunk) == nil && chunk.Type == relay.TypeProxyResponseChunk {
			allData += chunk.Data
		}
	}
	assert.Contains(t, allData, "data: {\"chunk\":")

	// Provider was actually hit
	assert.Equal(t, 1, providerHits)
}

// TestRelayIntegration_CORSError tests the full path when the browser can't
// reach the provider (simulates CORS failure).
func TestRelayIntegration_CORSError(t *testing.T) {
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

	browserConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?role=client", nil)
	require.NoError(t, err)
	defer browserConn.Close()

	// Browser sends CORS error for any request
	go func() {
		for {
			_, msg, err := browserConn.ReadMessage()
			if err != nil {
				return
			}
			var env relay.Envelope
			if json.Unmarshal(msg, &env) == nil && env.Type == relay.TypeProxyRequest {
				errMsg, _ := json.Marshal(relay.ProxyError{
					Type: relay.TypeProxyError, ID: env.ID,
					Error: "CORS blocked", Status: 0,
				})
				_ = browserConn.WriteMessage(websocket.TextMessage, errMsg)
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	// Agent sends request
	proxyReq := relay.ProxyRequest{
		Type: relay.TypeProxyRequest, ID: "req_cors", Method: "GET",
		URL: "https://opencode.ai/v1/models",
	}
	data, _ := json.Marshal(proxyReq)
	require.NoError(t, agentConn.WriteMessage(websocket.TextMessage, data))

	// Agent should receive error
	agentConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := agentConn.ReadMessage()
	require.NoError(t, err)

	var pe relay.ProxyError
	require.NoError(t, json.Unmarshal(msg, &pe))
	assert.Equal(t, relay.TypeProxyError, pe.Type)
	assert.Equal(t, "req_cors", pe.ID)
	assert.Contains(t, pe.Error, "CORS")
}
