// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lenaxia/llmsafespace/pkg/relay"
	"go.uber.org/zap"
)

// relayProxyConfig configures the relay proxy.
type relayProxyConfig struct {
	relayURL       string // WebSocket URL to the API server relay endpoint
	requestTimeout time.Duration
	maxPending     int
}

// pendingRequest tracks an in-flight proxy request awaiting client response.
type pendingRequest struct {
	startCh chan relay.ProxyResponseStart
	chunkCh chan relay.ProxyResponseChunk
	endCh   chan struct{}
	errorCh chan relay.ProxyError
	doneCh  chan struct{} // closed when handler finishes (for cleanup)
}

// relayProxy is the in-pod relay that receives HTTP requests from opencode
// (via baseURL redirect) and forwards them to the client via the API server's
// relay WebSocket.
type relayProxy struct {
	mu             sync.Mutex
	conn           *websocket.Conn
	pending        map[string]*pendingRequest
	requestTimeout time.Duration
	maxPending     int
	closed         bool
	closeCh        chan struct{}

	// sendFn is the function used to send proxy requests to the client.
	// In production, this writes to the WebSocket conn.
	// In tests, this can be overridden for injection.
	sendFn func(relay.ProxyRequest) error

	cfg *relayProxyConfig
}

func newRelayProxy(cfg *relayProxyConfig) *relayProxy {
	timeout := relay.RequestTimeout
	maxPending := relay.MaxPendingRequests
	if cfg != nil {
		if cfg.requestTimeout > 0 {
			timeout = cfg.requestTimeout
		}
		if cfg.maxPending > 0 {
			maxPending = cfg.maxPending
		}
	}
	return &relayProxy{
		pending:        make(map[string]*pendingRequest),
		requestTimeout: timeout,
		maxPending:     maxPending,
		closeCh:        make(chan struct{}),
		cfg:            cfg,
	}
}

// setRequestSender sets a custom send function (used for testing).
func (rp *relayProxy) setRequestSender(fn func(relay.ProxyRequest) error) {
	rp.mu.Lock()
	rp.sendFn = fn
	rp.mu.Unlock()
}

// handler returns the HTTP handler for the relay inference endpoint.
// opencode sends requests here (via baseURL override).
func (rp *relayProxy) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rp.mu.Lock()
		if rp.sendFn == nil && rp.conn == nil {
			rp.mu.Unlock()
			http.Error(w, `{"error":"relay not connected"}`, http.StatusServiceUnavailable)
			return
		}
		if len(rp.pending) >= rp.maxPending {
			rp.mu.Unlock()
			http.Error(w, `{"error":"too many pending requests"}`, http.StatusTooManyRequests)
			return
		}
		rp.mu.Unlock()

		// Read the request body
		body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10MB max
		if err != nil {
			http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
			return
		}

		// Build proxy request
		reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
		headers := make(map[string]string)
		for k := range r.Header {
			headers[normalizeHeaderKey(k)] = r.Header.Get(k)
		}

		// Reconstruct the target URL. opencode sends to our local endpoint
		// but the actual target is the opencode.ai provider gateway.
		// The path from the request tells us what path opencode wanted.
		targetURL := rp.buildTargetURL(r.URL.Path, r.URL.RawQuery)

		pr := relay.ProxyRequest{
			Type:    relay.TypeProxyRequest,
			ID:      reqID,
			Method:  r.Method,
			URL:     targetURL,
			Headers: headers,
			Body:    string(body),
		}

		// Register pending request
		pending := &pendingRequest{
			startCh: make(chan relay.ProxyResponseStart, 1),
			chunkCh: make(chan relay.ProxyResponseChunk, 64),
			endCh:   make(chan struct{}, 1),
			errorCh: make(chan relay.ProxyError, 1),
			doneCh:  make(chan struct{}),
		}
		rp.mu.Lock()
		rp.pending[reqID] = pending
		rp.mu.Unlock()

		defer func() {
			close(pending.doneCh)
			rp.mu.Lock()
			delete(rp.pending, reqID)
			rp.mu.Unlock()
		}()

		// Send proxy request
		if err := rp.send(pr); err != nil {
			http.Error(w, `{"error":"failed to send relay request"}`, http.StatusBadGateway)
			return
		}

		// Wait for response start or timeout
		timer := time.NewTimer(rp.requestTimeout)
		defer timer.Stop()

		select {
		case start := <-pending.startCh:
			// Write response headers
			for k, v := range start.Headers {
				w.Header().Set(k, v)
			}
			if start.Status == 0 {
				start.Status = http.StatusOK
			}
			w.WriteHeader(start.Status)

			// Stream chunks until end or error
			flusher, _ := w.(http.Flusher)
			for {
				select {
				case chunk := <-pending.chunkCh:
					_, _ = w.Write([]byte(chunk.Data))
					if flusher != nil {
						flusher.Flush()
					}
				case <-pending.endCh:
					// Drain any remaining chunks before returning
					for {
						select {
						case chunk := <-pending.chunkCh:
							_, _ = w.Write([]byte(chunk.Data))
							if flusher != nil {
								flusher.Flush()
							}
						default:
							return
						}
					}
				case pe := <-pending.errorCh:
					// Error after response start — can't change status code
					if log != nil {
						log.Warn("relay error after response started", zap.String("error", pe.Error))
					}
					return
				case <-r.Context().Done():
					return
				}
			}

		case pe := <-pending.errorCh:
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, pe.Error), http.StatusBadGateway)
			return

		case <-timer.C:
			http.Error(w, `{"error":"relay timeout"}`, http.StatusGatewayTimeout)
			return

		case <-r.Context().Done():
			return
		}
	})
}

// send dispatches a proxy request via the configured send function or WebSocket.
func (rp *relayProxy) send(pr relay.ProxyRequest) error {
	rp.mu.Lock()
	fn := rp.sendFn
	conn := rp.conn
	rp.mu.Unlock()

	if fn != nil {
		return fn(pr)
	}
	if conn == nil {
		return fmt.Errorf("not connected")
	}
	data, err := json.Marshal(pr)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

// deliverResponse delivers a response start to the pending request.
func (rp *relayProxy) deliverResponse(msg relay.ProxyResponseStart) {
	rp.mu.Lock()
	p := rp.pending[msg.ID]
	rp.mu.Unlock()
	if p != nil {
		select {
		case p.startCh <- msg:
		default:
		}
	}
}

// deliverChunk delivers a response chunk to the pending request.
func (rp *relayProxy) deliverChunk(msg relay.ProxyResponseChunk) {
	rp.mu.Lock()
	p := rp.pending[msg.ID]
	rp.mu.Unlock()
	if p != nil {
		select {
		case p.chunkCh <- msg:
		default:
		}
	}
}

// deliverEnd signals the end of a response.
func (rp *relayProxy) deliverEnd(msg relay.ProxyResponseEnd) {
	rp.mu.Lock()
	p := rp.pending[msg.ID]
	rp.mu.Unlock()
	if p != nil {
		select {
		case p.endCh <- struct{}{}:
		default:
		}
	}
}

// deliverError delivers an error to the pending request.
func (rp *relayProxy) deliverError(msg relay.ProxyError) {
	rp.mu.Lock()
	p := rp.pending[msg.ID]
	rp.mu.Unlock()
	if p != nil {
		select {
		case p.errorCh <- msg:
		default:
		}
	}
}

// connect establishes the WebSocket connection to the API server relay endpoint.
// Blocks until the connection is closed or lost.
func (rp *relayProxy) connect() error {
	if rp.cfg == nil || rp.cfg.relayURL == "" {
		return fmt.Errorf("no relay URL configured")
	}

	conn, resp, err := websocket.DefaultDialer.Dial(rp.cfg.relayURL, nil)
	if err != nil {
		return fmt.Errorf("relay WebSocket dial: %w", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}

	rp.mu.Lock()
	rp.conn = conn
	rp.mu.Unlock()

	// readLoop blocks until the connection is closed.
	rp.readLoop(conn)
	return nil
}

// readLoop reads messages from the WebSocket and dispatches to pending requests.
func (rp *relayProxy) readLoop(conn *websocket.Conn) {
	defer func() {
		rp.mu.Lock()
		if rp.conn == conn {
			rp.conn = nil
		}
		rp.mu.Unlock()
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var env relay.Envelope
		if json.Unmarshal(msg, &env) != nil {
			continue
		}
		switch env.Type {
		case relay.TypeProxyResponseStart:
			var start relay.ProxyResponseStart
			if json.Unmarshal(msg, &start) == nil {
				rp.deliverResponse(start)
			}
		case relay.TypeProxyResponseChunk:
			var chunk relay.ProxyResponseChunk
			if json.Unmarshal(msg, &chunk) == nil {
				rp.deliverChunk(chunk)
			}
		case relay.TypeProxyResponseEnd:
			var end relay.ProxyResponseEnd
			if json.Unmarshal(msg, &end) == nil {
				rp.deliverEnd(end)
			}
		case relay.TypeProxyError:
			var pe relay.ProxyError
			if json.Unmarshal(msg, &pe) == nil {
				rp.deliverError(pe)
			}
		case relay.TypePing:
			// Respond with pong
			pong, _ := json.Marshal(relay.Envelope{Type: relay.TypePong})
			_ = conn.WriteMessage(websocket.TextMessage, pong)
		}
	}
}

// close shuts down the relay proxy.
func (rp *relayProxy) close() {
	rp.mu.Lock()
	if rp.closed {
		rp.mu.Unlock()
		return
	}
	rp.closed = true
	close(rp.closeCh)
	conn := rp.conn
	rp.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// buildTargetURL reconstructs the target provider URL from the request path.
// opencode sends requests to our local relay with the provider API path appended.
// We need to reconstruct the full target URL for the client to make the actual call.
func (rp *relayProxy) buildTargetURL(path, query string) string {
	// Default target: opencode.ai (the free-tier gateway)
	base := "https://opencode.ai"
	url := base + path
	if query != "" {
		url += "?" + query
	}
	return url
}

// normalizeHeaderKey converts header keys to lowercase for transport.
func normalizeHeaderKey(k string) string {
	return strings.ToLower(k)
}
