// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lenaxia/llmsafespace/pkg/relay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayInferenceHandler_NoWebSocket(t *testing.T) {
	// When no relay WebSocket is connected, the handler should return 503.
	rp := newRelayProxy(nil)
	handler := rp.handler()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test"}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "relay not connected")
}

func TestRelayInferenceHandler_HappyPath(t *testing.T) {
	// Simulate a connected relay WebSocket that responds to a proxy request.
	rp := newRelayProxy(nil)

	// Mock client: a goroutine that reads requests from the relay and responds.
	var wg sync.WaitGroup
	reqCh := make(chan relay.ProxyRequest, 1)
	rp.setRequestSender(func(pr relay.ProxyRequest) error {
		reqCh <- pr
		return nil
	})

	wg.Add(1)
	go func() {
		defer wg.Done()
		pr := <-reqCh
		// Simulate client response
		rp.deliverResponse(relay.ProxyResponseStart{
			Type:    relay.TypeProxyResponseStart,
			ID:      pr.ID,
			Status:  200,
			Headers: map[string]string{"content-type": "application/json"},
		})
		rp.deliverChunk(relay.ProxyResponseChunk{
			Type: relay.TypeProxyResponseChunk,
			ID:   pr.ID,
			Data: `{"choices":[{"message":{"content":"hello"}}]}`,
		})
		rp.deliverEnd(relay.ProxyResponseEnd{
			Type: relay.TypeProxyResponseEnd,
			ID:   pr.ID,
		})
	}()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"test","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer public")
	w := httptest.NewRecorder()

	handler := rp.handler()
	handler.ServeHTTP(w, req)

	wg.Wait()
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"choices"`)
}

func TestRelayInferenceHandler_StreamingResponse(t *testing.T) {
	rp := newRelayProxy(nil)

	reqCh := make(chan relay.ProxyRequest, 1)
	rp.setRequestSender(func(pr relay.ProxyRequest) error {
		reqCh <- pr
		return nil
	})

	go func() {
		pr := <-reqCh
		rp.deliverResponse(relay.ProxyResponseStart{
			Type:    relay.TypeProxyResponseStart,
			ID:      pr.ID,
			Status:  200,
			Headers: map[string]string{"content-type": "text/event-stream"},
		})
		rp.deliverChunk(relay.ProxyResponseChunk{
			Type: relay.TypeProxyResponseChunk,
			ID:   pr.ID,
			Data: "data: {\"chunk\":1}\n\n",
		})
		rp.deliverChunk(relay.ProxyResponseChunk{
			Type: relay.TypeProxyResponseChunk,
			ID:   pr.ID,
			Data: "data: {\"chunk\":2}\n\n",
		})
		rp.deliverEnd(relay.ProxyResponseEnd{
			Type: relay.TypeProxyResponseEnd,
			ID:   pr.ID,
		})
	}()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"test","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	rp.handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"chunk":1`)
	assert.Contains(t, w.Body.String(), `"chunk":2`)
}

func TestRelayInferenceHandler_ClientError(t *testing.T) {
	rp := newRelayProxy(nil)

	reqCh := make(chan relay.ProxyRequest, 1)
	rp.setRequestSender(func(pr relay.ProxyRequest) error {
		reqCh <- pr
		return nil
	})

	go func() {
		pr := <-reqCh
		rp.deliverError(relay.ProxyError{
			Type:   relay.TypeProxyError,
			ID:     pr.ID,
			Error:  "CORS blocked",
			Status: 0,
		})
	}()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"test"}`))
	w := httptest.NewRecorder()

	rp.handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "CORS")
}

func TestRelayInferenceHandler_Timeout(t *testing.T) {
	rp := newRelayProxy(nil)
	rp.requestTimeout = 100 * time.Millisecond

	// Sender that never responds
	rp.setRequestSender(func(pr relay.ProxyRequest) error {
		return nil // accept but never respond
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"test"}`))
	w := httptest.NewRecorder()

	rp.handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusGatewayTimeout, w.Code)
}

func TestRelayInferenceHandler_ConcurrentRequests(t *testing.T) {
	rp := newRelayProxy(nil)

	reqCh := make(chan relay.ProxyRequest, 10)
	rp.setRequestSender(func(pr relay.ProxyRequest) error {
		reqCh <- pr
		return nil
	})

	// Responder goroutine
	go func() {
		for pr := range reqCh {
			id := pr.ID
			rp.deliverResponse(relay.ProxyResponseStart{
				Type: relay.TypeProxyResponseStart, ID: id, Status: 200,
				Headers: map[string]string{"content-type": "application/json"},
			})
			rp.deliverChunk(relay.ProxyResponseChunk{
				Type: relay.TypeProxyResponseChunk, ID: id,
				Data: `{"id":"` + id + `"}`,
			})
			rp.deliverEnd(relay.ProxyResponseEnd{Type: relay.TypeProxyResponseEnd, ID: id})
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
				strings.NewReader(`{"model":"test"}`))
			w := httptest.NewRecorder()
			rp.handler().ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
		}()
	}
	wg.Wait()
	close(reqCh)
}

func TestRelayWebSocketConnection(t *testing.T) {
	// Test the agentd-side WebSocket client that connects to the API relay endpoint.
	// Simulate an API relay server.
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	var serverConn *websocket.Conn
	var connMu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		connMu.Lock()
		serverConn = conn
		connMu.Unlock()

		// Echo proxy requests back as immediate 200 responses
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var env relay.Envelope
			_ = json.Unmarshal(msg, &env)
			if env.Type == relay.TypeProxyRequest {
				start, _ := json.Marshal(relay.ProxyResponseStart{
					Type: relay.TypeProxyResponseStart, ID: env.ID, Status: 200,
					Headers: map[string]string{"content-type": "application/json"},
				})
				_ = conn.WriteMessage(websocket.TextMessage, start)
				chunk, _ := json.Marshal(relay.ProxyResponseChunk{
					Type: relay.TypeProxyResponseChunk, ID: env.ID,
					Data: `{"ok":true}`,
				})
				_ = conn.WriteMessage(websocket.TextMessage, chunk)
				end, _ := json.Marshal(relay.ProxyResponseEnd{
					Type: relay.TypeProxyResponseEnd, ID: env.ID,
				})
				_ = conn.WriteMessage(websocket.TextMessage, end)
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	rp := newRelayProxy(&relayProxyConfig{
		relayURL: wsURL,
	})
	go rp.connectLoop(t)

	// Wait for connection
	time.Sleep(200 * time.Millisecond)

	// Make a request through the relay
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"test"}`))
	w := httptest.NewRecorder()
	rp.handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok":true`)

	rp.close()
	connMu.Lock()
	if serverConn != nil {
		_ = serverConn.Close()
	}
	connMu.Unlock()
}

// connectLoop is a test helper that connects once (no reconnection logic).
func (rp *relayProxy) connectLoop(t *testing.T) {
	t.Helper()
	err := rp.connect()
	if err != nil {
		t.Logf("relay connect: %v", err)
	}
}

func TestRelayProxy_RateLimitExceeded(t *testing.T) {
	rp := newRelayProxy(nil)
	rp.maxPending = 1

	// Fill the pending slot
	reqCh := make(chan relay.ProxyRequest, 2)
	rp.setRequestSender(func(pr relay.ProxyRequest) error {
		reqCh <- pr
		return nil
	})

	// First request — will block
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"test"}`))
		w := httptest.NewRecorder()
		rp.handler().ServeHTTP(w, req)
	}()

	// Wait for first request to be pending
	time.Sleep(50 * time.Millisecond)

	// Second request — should be rejected (too many pending)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"test"}`))
	w := httptest.NewRecorder()
	rp.handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	// Resolve first request
	pr := <-reqCh
	rp.deliverResponse(relay.ProxyResponseStart{Type: relay.TypeProxyResponseStart, ID: pr.ID, Status: 200})
	rp.deliverEnd(relay.ProxyResponseEnd{Type: relay.TypeProxyResponseEnd, ID: pr.ID})
}

func TestRelayProxy_BodyForwarded(t *testing.T) {
	rp := newRelayProxy(nil)

	var capturedReq relay.ProxyRequest
	reqCh := make(chan struct{}, 1)
	rp.setRequestSender(func(pr relay.ProxyRequest) error {
		capturedReq = pr
		reqCh <- struct{}{}
		// Respond immediately
		rp.deliverResponse(relay.ProxyResponseStart{Type: relay.TypeProxyResponseStart, ID: pr.ID, Status: 200})
		rp.deliverEnd(relay.ProxyResponseEnd{Type: relay.TypeProxyResponseEnd, ID: pr.ID})
		return nil
	})

	body := `{"model":"claude-sonnet","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer public")
	w := httptest.NewRecorder()
	rp.handler().ServeHTTP(w, req)

	<-reqCh
	assert.Equal(t, "POST", capturedReq.Method)
	assert.Equal(t, body, capturedReq.Body)
	assert.Equal(t, "application/json", capturedReq.Headers["content-type"])
	assert.Equal(t, "Bearer public", capturedReq.Headers["authorization"])
}

func TestBuildTargetURL(t *testing.T) {
	rp := newRelayProxy(nil)
	tests := []struct {
		name  string
		path  string
		query string
		want  string
	}{
		{"strips relay prefix", "/relay/inference/v1/chat/completions", "", "https://opencode.ai/v1/chat/completions"},
		{"strips prefix with query", "/relay/inference/v1/models", "limit=10", "https://opencode.ai/v1/models?limit=10"},
		{"handles root path", "/relay/inference", "", "https://opencode.ai/"},
		{"handles path without prefix", "/v1/chat/completions", "", "https://opencode.ai/v1/chat/completions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rp.buildTargetURL(tt.path, tt.query)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRelayProxy_FailAllPendingOnDisconnect(t *testing.T) {
	// Simulate a relay that disconnects while a request is in flight.
	// The handler should receive an error rather than hanging.
	rp := newRelayProxy(nil)
	rp.requestTimeout = 5 * time.Second

	reqCh := make(chan relay.ProxyRequest, 1)
	rp.setRequestSender(func(pr relay.ProxyRequest) error {
		reqCh <- pr
		return nil
	})

	doneCh := make(chan int, 1) // receives HTTP status code

	go func() {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"test"}`))
		w := httptest.NewRecorder()
		rp.handler().ServeHTTP(w, req)
		doneCh <- w.Code
	}()

	// Wait for request to be sent
	<-reqCh

	// Simulate disconnect — fail all pending
	rp.failAllPending("connection lost")

	// Handler should complete with 502
	select {
	case code := <-doneCh:
		assert.Equal(t, http.StatusBadGateway, code)
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not complete after failAllPending")
	}
}

func TestRelayProxy_IsConnected(t *testing.T) {
	rp := newRelayProxy(nil)
	assert.False(t, rp.isConnected())

	rp.setRequestSender(func(pr relay.ProxyRequest) error { return nil })
	assert.True(t, rp.isConnected())
}

func TestBuildProxyHeaders_StripsHopByHop(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "Bearer public")
	h.Set("Connection", "keep-alive")
	h.Set("Transfer-Encoding", "chunked")
	h.Set("Host", "localhost:4097")

	result := buildProxyHeaders(h)

	assert.Equal(t, "application/json", result["content-type"])
	assert.Equal(t, "Bearer public", result["authorization"])
	assert.Empty(t, result["connection"])
	assert.Empty(t, result["transfer-encoding"])
	assert.Empty(t, result["host"])
}
