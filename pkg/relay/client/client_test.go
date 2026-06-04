// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relayclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lenaxia/llmsafespace/pkg/relay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_ConnectAndHandleRequest(t *testing.T) {
	// Fake upstream provider API
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"response":"hello"}`))
	}))
	defer upstream.Close()

	// Fake relay server — accepts connection, sends proxy request, reads responses
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	resultCh := make(chan []relay.Envelope, 1)

	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send a proxy request
		proxyReq := relay.ProxyRequest{
			Type:    relay.TypeProxyRequest,
			ID:      "req_1",
			Method:  "GET",
			URL:     upstream.URL + "/v1/models",
			Headers: map[string]string{"accept": "application/json"},
		}
		data, _ := json.Marshal(proxyReq)
		_ = conn.WriteMessage(websocket.TextMessage, data)

		// Read responses
		var messages []relay.Envelope
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var env relay.Envelope
			_ = json.Unmarshal(msg, &env)
			messages = append(messages, env)
			if env.Type == relay.TypeProxyResponseEnd || env.Type == relay.TypeProxyError {
				break
			}
		}
		resultCh <- messages
	}))
	defer relayServer.Close()

	wsURL := "ws" + strings.TrimPrefix(relayServer.URL, "http")
	client := New(Config{
		RelayURL:   wsURL,
		AuthToken:  "test-token",
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = client.Connect(ctx) }()

	messages := <-resultCh
	types := make([]string, len(messages))
	for i, m := range messages {
		types[i] = m.Type
	}
	assert.Contains(t, types, relay.TypeProxyResponseStart)
	assert.Contains(t, types, relay.TypeProxyResponseEnd)

	cancel()
	_ = client.Close()
}

func TestClient_HandleFetchError(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	resultCh := make(chan relay.Envelope, 1)

	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send request to non-existent host
		proxyReq := relay.ProxyRequest{
			Type:   relay.TypeProxyRequest,
			ID:     "req_fail",
			Method: "GET",
			URL:    "http://localhost:1/never-exists",
		}
		data, _ := json.Marshal(proxyReq)
		_ = conn.WriteMessage(websocket.TextMessage, data)

		// Read error response
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var env relay.Envelope
		_ = json.Unmarshal(msg, &env)
		resultCh <- env
	}))
	defer relayServer.Close()

	wsURL := "ws" + strings.TrimPrefix(relayServer.URL, "http")
	client := New(Config{
		RelayURL:   wsURL,
		HTTPClient: &http.Client{Timeout: 1 * time.Second},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = client.Connect(ctx) }()

	env := <-resultCh
	assert.Equal(t, relay.TypeProxyError, env.Type)
	assert.Equal(t, "req_fail", env.ID)

	cancel()
	_ = client.Close()
}

func TestClient_PingPong(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	resultCh := make(chan relay.Envelope, 1)

	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send ping
		ping, _ := json.Marshal(relay.Envelope{Type: relay.TypePing})
		_ = conn.WriteMessage(websocket.TextMessage, ping)

		// Read pong
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var env relay.Envelope
		_ = json.Unmarshal(msg, &env)
		resultCh <- env
	}))
	defer relayServer.Close()

	wsURL := "ws" + strings.TrimPrefix(relayServer.URL, "http")
	client := New(Config{RelayURL: wsURL})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = client.Connect(ctx) }()

	env := <-resultCh
	assert.Equal(t, relay.TypePong, env.Type)

	cancel()
	_ = client.Close()
}

func TestClient_Close(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer conn.Close()
		// Just hold connection
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}))
	defer relayServer.Close()

	wsURL := "ws" + strings.TrimPrefix(relayServer.URL, "http")
	client := New(Config{RelayURL: wsURL})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- client.Connect(ctx) }()

	time.Sleep(200 * time.Millisecond)
	require.NoError(t, client.Close())

	select {
	case err := <-errCh:
		// Should exit without error (or nil)
		_ = err
	case <-time.After(2 * time.Second):
		t.Fatal("client did not exit after Close()")
	}
}
