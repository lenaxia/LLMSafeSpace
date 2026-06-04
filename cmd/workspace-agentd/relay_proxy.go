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
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lenaxia/llmsafespace/pkg/relay"
	"go.uber.org/zap"
)

var reqCounter uint64

// relayProxyConfig configures the relay proxy.
type relayProxyConfig struct {
	relayURL       string // WebSocket URL to the API server relay endpoint
	authToken      string // Bearer token for authenticating to the relay endpoint
	targetBaseURL  string // Provider base URL (default: https://opencode.ai)
	requestTimeout time.Duration
	maxPending     int
}

// pendingRequest tracks an in-flight proxy request awaiting client response.
type pendingRequest struct {
	startCh chan relay.ProxyResponseStart
	chunkCh chan relay.ProxyResponseChunk
	endCh   chan struct{}
	errorCh chan relay.ProxyError
	doneCh  chan struct{} // closed when handler finishes
}

// relayProxy is the in-pod relay that receives HTTP requests from opencode
// (via baseURL redirect) and forwards them to the client via the API server's
// relay WebSocket.
type relayProxy struct {
	mu      sync.Mutex
	conn    *websocket.Conn
	connWmu sync.Mutex // serializes WebSocket writes
	pending map[string]*pendingRequest

	requestTimeout time.Duration
	maxPending     int
	targetBaseURL  string
	closed         bool
	closeCh        chan struct{}

	// sendFn overrides the WebSocket send path (for testing).
	sendFn func(relay.ProxyRequest) error
	cfg    *relayProxyConfig

	// log is captured at construction time so relay goroutines never read
	// the mutable package-level `log` variable (which tests swap under the
	// race detector without a lock).
	log *zap.Logger

	// Metrics
	requestsTotal   uint64
	requestsErrored uint64
	requestsTimeout uint64
}

func newRelayProxy(cfg *relayProxyConfig) *relayProxy {
	timeout := relay.RequestTimeout
	maxPending := relay.MaxPendingRequests
	target := "https://opencode.ai"
	if cfg != nil {
		if cfg.requestTimeout > 0 {
			timeout = cfg.requestTimeout
		}
		if cfg.maxPending > 0 {
			maxPending = cfg.maxPending
		}
		if cfg.targetBaseURL != "" {
			target = cfg.targetBaseURL
		}
	}
	return &relayProxy{
		pending:        make(map[string]*pendingRequest),
		requestTimeout: timeout,
		maxPending:     maxPending,
		targetBaseURL:  target,
		closeCh:        make(chan struct{}),
		cfg:            cfg,
		log:            log, // snapshot package-level logger at construction time
	}
}

// setRequestSender sets a custom send function (testing only).
func (rp *relayProxy) setRequestSender(fn func(relay.ProxyRequest) error) {
	rp.mu.Lock()
	rp.sendFn = fn
	rp.mu.Unlock()
}

// isConnected reports whether the relay WebSocket is connected.
func (rp *relayProxy) isConnected() bool {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return rp.conn != nil || rp.sendFn != nil
}

// handler returns the HTTP handler for the relay inference endpoint.
func (rp *relayProxy) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rp.isConnected() {
			http.Error(w, `{"error":"relay not connected"}`, http.StatusServiceUnavailable)
			return
		}

		rp.mu.Lock()
		if len(rp.pending) >= rp.maxPending {
			rp.mu.Unlock()
			http.Error(w, `{"error":"too many pending requests"}`, http.StatusTooManyRequests)
			return
		}
		rp.mu.Unlock()

		body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
		if err != nil {
			http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
			return
		}

		reqID := fmt.Sprintf("req_%d", atomic.AddUint64(&reqCounter, 1))
		headers := buildProxyHeaders(r.Header)
		targetURL := rp.buildTargetURL(r.URL.Path, r.URL.RawQuery)

		pr := relay.ProxyRequest{
			Type:    relay.TypeProxyRequest,
			ID:      reqID,
			Method:  r.Method,
			URL:     targetURL,
			Headers: headers,
			Body:    string(body),
		}

		pending := &pendingRequest{
			startCh: make(chan relay.ProxyResponseStart, 1),
			chunkCh: make(chan relay.ProxyResponseChunk, 128),
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

		atomic.AddUint64(&rp.requestsTotal, 1)

		if err := rp.send(pr); err != nil {
			atomic.AddUint64(&rp.requestsErrored, 1)
			http.Error(w, `{"error":"failed to send relay request"}`, http.StatusBadGateway)
			return
		}

		timer := time.NewTimer(rp.requestTimeout)
		defer timer.Stop()

		select {
		case start := <-pending.startCh:
			rp.writeStreamingResponse(w, r, pending, start)
		case pe := <-pending.errorCh:
			atomic.AddUint64(&rp.requestsErrored, 1)
			errJSON, _ := json.Marshal(map[string]string{"error": pe.Error})
			http.Error(w, string(errJSON), http.StatusBadGateway)
		case <-timer.C:
			atomic.AddUint64(&rp.requestsTimeout, 1)
			http.Error(w, `{"error":"relay timeout"}`, http.StatusGatewayTimeout)
		case <-r.Context().Done():
			// Client disconnected from opencode
		}
	})
}

// writeStreamingResponse writes the response headers and streams chunks.
func (rp *relayProxy) writeStreamingResponse(w http.ResponseWriter, r *http.Request, pending *pendingRequest, start relay.ProxyResponseStart) {
	for k, v := range start.Headers {
		w.Header().Set(k, v)
	}
	status := start.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)

	flusher, _ := w.(http.Flusher)
	for {
		select {
		case chunk := <-pending.chunkCh:
			_, _ = w.Write([]byte(chunk.Data))
			if flusher != nil {
				flusher.Flush()
			}
		case <-pending.endCh:
			// Drain remaining buffered chunks
			for {
				select {
				case chunk := <-pending.chunkCh:
					_, _ = w.Write([]byte(chunk.Data))
				default:
					return
				}
			}
		case <-pending.errorCh:
			if rp.log != nil {
				rp.log.Warn("relay error after response started")
			}
			return
		case <-r.Context().Done():
			return
		}
	}
}

// send dispatches a proxy request via sendFn or WebSocket.
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
	rp.connWmu.Lock()
	defer rp.connWmu.Unlock()
	return conn.WriteMessage(websocket.TextMessage, data)
}

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

// deliverChunk blocks until accepted or handler exits (backpressure, not drop).
func (rp *relayProxy) deliverChunk(msg relay.ProxyResponseChunk) {
	rp.mu.Lock()
	p := rp.pending[msg.ID]
	rp.mu.Unlock()
	if p != nil {
		select {
		case p.chunkCh <- msg:
		case <-p.doneCh:
		}
	}
}

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

// failAllPending fails all in-flight requests (called on disconnect).
func (rp *relayProxy) failAllPending(reason string) {
	rp.mu.Lock()
	pending := make(map[string]*pendingRequest, len(rp.pending))
	for k, v := range rp.pending {
		pending[k] = v
	}
	rp.mu.Unlock()

	for id, p := range pending {
		select {
		case p.errorCh <- relay.ProxyError{
			Type:  relay.TypeProxyError,
			ID:    id,
			Error: reason,
		}:
		default:
		}
	}
}

// connect establishes the WebSocket connection. Blocks until disconnected.
func (rp *relayProxy) connect() error {
	if rp.cfg == nil || rp.cfg.relayURL == "" {
		return fmt.Errorf("no relay URL configured")
	}

	header := http.Header{}
	if rp.cfg.authToken != "" {
		header.Set("Authorization", "Bearer "+rp.cfg.authToken)
	}

	conn, resp, err := websocket.DefaultDialer.Dial(rp.cfg.relayURL, header)
	if err != nil {
		return fmt.Errorf("relay WebSocket dial: %w", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}

	rp.mu.Lock()
	rp.conn = conn
	rp.mu.Unlock()

	if rp.log != nil {
		rp.log.Info("relay WebSocket connected", zap.String("url", rp.cfg.relayURL))
	}

	rp.readLoop(conn)

	// Connection lost — fail all pending requests immediately
	rp.failAllPending("relay disconnected")

	if rp.log != nil {
		rp.log.Warn("relay WebSocket disconnected")
	}
	return nil
}

// readLoop reads messages and dispatches. Single JSON unmarshal via RawMessage.
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

		// Single unmarshal: extract type field with RawMessage for the rest
		var raw struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(msg, &raw) != nil {
			continue
		}

		switch raw.Type {
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
			rp.deliverEnd(relay.ProxyResponseEnd{Type: raw.Type, ID: raw.ID})
		case relay.TypeProxyError:
			var pe relay.ProxyError
			if json.Unmarshal(msg, &pe) == nil {
				rp.deliverError(pe)
			}
		case relay.TypePing:
			pong, _ := json.Marshal(relay.Envelope{Type: relay.TypePong})
			rp.connWmu.Lock()
			_ = conn.WriteMessage(websocket.TextMessage, pong)
			rp.connWmu.Unlock()
		}
	}
}

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

// buildTargetURL strips the relay prefix and prepends the configured provider base URL.
func (rp *relayProxy) buildTargetURL(path, query string) string {
	apiPath := strings.TrimPrefix(path, "/relay/inference")
	if apiPath == "" {
		apiPath = "/"
	}
	url := rp.targetBaseURL + apiPath
	if query != "" {
		url += "?" + query
	}
	return url
}

// buildProxyHeaders extracts headers for the proxy request.
// Strips hop-by-hop headers. Keeps authorization (needed by the provider).
func buildProxyHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k := range h {
		lk := strings.ToLower(k)
		// Skip hop-by-hop headers that shouldn't be forwarded
		switch lk {
		case "connection", "keep-alive", "transfer-encoding",
			"te", "trailer", "upgrade", "host":
			continue
		}
		out[lk] = h.Get(k)
	}
	return out
}
