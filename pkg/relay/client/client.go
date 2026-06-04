// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package relayclient implements the client side of the relay protocol for
// Go SDK consumers. It connects to the API server relay WebSocket, receives
// proxy requests, makes the actual HTTP calls, and streams responses back.
package relayclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lenaxia/llmsafespace/pkg/relay"
)

// Client is a Go SDK relay client that handles proxy requests from the
// API server relay channel.
type Client struct {
	relayURL   string
	authToken  string
	httpClient *http.Client

	mu     sync.Mutex
	conn   *websocket.Conn
	closed int32
	doneCh chan struct{}
}

// Config configures the relay client.
type Config struct {
	// RelayURL is the WebSocket URL for the relay endpoint.
	// Example: "wss://api.example.com/api/v1/workspaces/ws123/relay?role=client"
	RelayURL string

	// AuthToken is the JWT or API key for authentication.
	AuthToken string

	// HTTPClient is the HTTP client used for proxied requests. If nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// New creates a new relay client.
func New(cfg Config) *Client {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 120 * time.Second}
	}
	return &Client{
		relayURL:   cfg.RelayURL,
		authToken:  cfg.AuthToken,
		httpClient: httpClient,
		doneCh:     make(chan struct{}),
	}
}

// Connect establishes the WebSocket connection and starts processing proxy requests.
// Blocks until the connection is closed or the context is canceled.
func (c *Client) Connect(ctx context.Context) error {
	header := http.Header{}
	if c.authToken != "" {
		header.Set("Authorization", "Bearer "+c.authToken)
	}

	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, c.relayURL, header)
	if err != nil {
		return fmt.Errorf("relay dial: %w", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	defer func() {
		_ = conn.Close()
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
		close(c.doneCh)
	}()

	// Start ping loop
	go c.pingLoop(ctx, conn)

	// Read loop
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			if atomic.LoadInt32(&c.closed) == 1 {
				return nil
			}
			return fmt.Errorf("relay read: %w", err)
		}

		var env relay.Envelope
		if json.Unmarshal(msg, &env) != nil {
			continue
		}

		switch env.Type {
		case relay.TypeProxyRequest:
			var req relay.ProxyRequest
			if json.Unmarshal(msg, &req) == nil {
				go c.handleRequest(ctx, conn, req)
			}
		case relay.TypePing:
			pong, _ := json.Marshal(relay.Envelope{Type: relay.TypePong})
			_ = conn.WriteMessage(websocket.TextMessage, pong)
		}
	}
}

// Close shuts down the relay client.
func (c *Client) Close() error {
	atomic.StoreInt32(&c.closed, 1)
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

// Done returns a channel that is closed when the client disconnects.
func (c *Client) Done() <-chan struct{} {
	return c.doneCh
}

func (c *Client) handleRequest(ctx context.Context, conn *websocket.Conn, req relay.ProxyRequest) {
	var body io.Reader
	if req.Body != "" {
		body = strings.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, body)
	if err != nil {
		c.sendError(conn, req.ID, fmt.Sprintf("build request: %v", err))
		return
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		c.sendError(conn, req.ID, fmt.Sprintf("fetch failed: %v", err))
		return
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // best-effort drain

	// Send response start
	respHeaders := make(map[string]string)
	for k := range resp.Header {
		respHeaders[strings.ToLower(k)] = resp.Header.Get(k)
	}
	c.sendJSON(conn, relay.ProxyResponseStart{
		Type:    relay.TypeProxyResponseStart,
		ID:      req.ID,
		Status:  resp.StatusCode,
		Headers: respHeaders,
	})

	// Stream body in chunks
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			c.sendJSON(conn, relay.ProxyResponseChunk{
				Type: relay.TypeProxyResponseChunk,
				ID:   req.ID,
				Data: string(buf[:n]),
			})
		}
		if err != nil {
			break
		}
	}

	// Send end
	c.sendJSON(conn, relay.ProxyResponseEnd{
		Type: relay.TypeProxyResponseEnd,
		ID:   req.ID,
	})
}

func (c *Client) sendJSON(conn *websocket.Conn, msg interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = conn.WriteMessage(websocket.TextMessage, data)
}

func (c *Client) sendError(conn *websocket.Conn, id, errMsg string) {
	c.sendJSON(conn, relay.ProxyError{
		Type:   relay.TypeProxyError,
		ID:     id,
		Error:  errMsg,
		Status: 0,
	})
}

func (c *Client) pingLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(relay.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			err := conn.WriteMessage(websocket.PingMessage, nil)
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}
