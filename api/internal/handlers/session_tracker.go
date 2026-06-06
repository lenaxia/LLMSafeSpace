// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	pkginterfaces "github.com/lenaxia/llmsafespace/pkg/interfaces"
)

const sseIdleTimeout = 5 * time.Minute

type SessionIdleCallback func(workspaceID, sessionID string)

type RawEventCallback func(workspaceID, eventType, rawData string)

// InferenceCallback fires when a session.updated event carries token output.
// modelID/providerID identify the model; input/output are delta tokens.
type InferenceCallback func(workspaceID, modelID, providerID string, inputTokens, outputTokens int64, costDollars float64)

type sseEvent struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

type opencodeEvent struct {
	Payload struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
	} `json:"payload"`
}

type SSETracker struct {
	httpClient      *http.Client
	logger          pkginterfaces.LoggerInterface
	onSessionIdle   SessionIdleCallback
	onSessionActive SessionIdleCallback
	onRawEvent      RawEventCallback
	// onInference fires when a session produces new output tokens (billing/metering).
	onInference InferenceCallback
	// sessionTokenSeen tracks cumulative tokens per sessionID:workspaceID for delta computation.
	tokensMu         sync.Mutex
	sessionTokenSeen map[string]int64
	subscriptions    map[string]context.CancelFunc
	subMu            sync.Mutex
	passwordGetter   func(ctx context.Context, workspaceID string) (string, error)
	podIPResolver    func(workspaceID string) string
	// Drain subscribers (Epic 27b)
	drainMu         sync.Mutex
	drainSubs       map[string]map[uint64]*drainSub
	drainSubCounter uint64
}

type drainSub struct {
	onIdle   func(workspaceID, sessionID string)
	onActive func(workspaceID, sessionID string)
}

func NewSSETracker(
	httpClient *http.Client,
	logger pkginterfaces.LoggerInterface,
	onSessionIdle SessionIdleCallback,
) *SSETracker {
	return &SSETracker{
		httpClient:       httpClient,
		logger:           logger,
		onSessionIdle:    onSessionIdle,
		subscriptions:    make(map[string]context.CancelFunc),
		sessionTokenSeen: make(map[string]int64),
	}
}

func (t *SSETracker) SetPasswordGetter(getter func(ctx context.Context, workspaceID string) (string, error)) {
	t.passwordGetter = getter
}

func (t *SSETracker) SetPodIPResolver(resolver func(workspaceID string) string) {
	t.podIPResolver = resolver
}

func (t *SSETracker) SetOnSessionActive(callback SessionIdleCallback) {
	t.onSessionActive = callback
}

// SetOnInference installs the callback fired when a session produces token output.
func (t *SSETracker) SetOnInference(cb InferenceCallback) {
	t.onInference = cb
}

func (t *SSETracker) SetOnRawEvent(callback RawEventCallback) {
	t.onRawEvent = callback
}

func (t *SSETracker) EnsureWatching(workspaceID string) {
	t.subMu.Lock()
	defer t.subMu.Unlock()

	if _, exists := t.subscriptions[workspaceID]; exists {
		return
	}

	// cancel is stored in t.subscriptions and invoked by StopWatching;
	// gosec's G118 cannot see across the map indirection so it flags
	// this as a leak. Suppressed because the lifecycle is correct.
	//nolint:gosec // G118 false positive; cancel stored in subscriptions map
	ctx, cancel := context.WithCancel(context.Background())
	t.subscriptions[workspaceID] = cancel

	go t.subscribe(ctx, workspaceID)
}

func (t *SSETracker) StopWatching(workspaceID string) {
	t.subMu.Lock()
	defer t.subMu.Unlock()

	if cancel, exists := t.subscriptions[workspaceID]; exists {
		cancel()
		delete(t.subscriptions, workspaceID)
	}
}

func (t *SSETracker) Stop() {
	t.subMu.Lock()
	defer t.subMu.Unlock()

	for id, cancel := range t.subscriptions {
		cancel()
		delete(t.subscriptions, id)
	}
}

func (t *SSETracker) SubscriptionCount() int {
	t.subMu.Lock()
	defer t.subMu.Unlock()
	return len(t.subscriptions)
}

// SubscribeDrain registers callbacks for session-status events in the given
// workspace. Multiple concurrent drain subscriptions per workspace are supported.
// Returns a cancel function the caller MUST invoke when done.
func (t *SSETracker) SubscribeDrain(
	workspaceID string,
	onIdle func(workspaceID, sessionID string),
	onActive func(workspaceID, sessionID string),
) (cancel func()) {
	t.drainMu.Lock()
	defer t.drainMu.Unlock()

	if t.drainSubs == nil {
		t.drainSubs = make(map[string]map[uint64]*drainSub)
	}
	if t.drainSubs[workspaceID] == nil {
		t.drainSubs[workspaceID] = make(map[uint64]*drainSub)
	}
	t.drainSubCounter++
	id := t.drainSubCounter
	t.drainSubs[workspaceID][id] = &drainSub{onIdle: onIdle, onActive: onActive}

	return func() {
		t.drainMu.Lock()
		defer t.drainMu.Unlock()
		delete(t.drainSubs[workspaceID], id)
		if len(t.drainSubs[workspaceID]) == 0 {
			delete(t.drainSubs, workspaceID)
		}
	}
}

func (t *SSETracker) subscribe(ctx context.Context, workspaceID string) {
	backoff := 2 * time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := t.connectAndRead(ctx, workspaceID); err != nil {
			t.logger.Debug("SSE subscription ended", "error", err, "workspaceID", workspaceID)
		} else {
			backoff = 2 * time.Second
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func (t *SSETracker) connectAndRead(ctx context.Context, workspaceID string) error {
	if t.passwordGetter == nil {
		return fmt.Errorf("password getter not configured")
	}

	if t.podIPResolver == nil {
		return fmt.Errorf("pod IP resolver not configured")
	}

	podIP := t.podIPResolver(workspaceID)
	if podIP == "" {
		return fmt.Errorf("no pod IP for workspace %s", workspaceID)
	}

	password, err := t.passwordGetter(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("getting password for SSE: %w", err)
	}

	idleCtx, cancelIdle := context.WithCancel(ctx)
	defer cancelIdle()
	idleTimer := time.AfterFunc(sseIdleTimeout, cancelIdle)
	defer idleTimer.Stop()

	targetURL := fmt.Sprintf("http://%s:%d/event", podIP, opencodePort)
	req, err := http.NewRequestWithContext(idleCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return fmt.Errorf("creating SSE request: %w", err)
	}
	req.SetBasicAuth("opencode", password)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("SSE connection: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SSE endpoint returned status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	var eventData strings.Builder
	for scanner.Scan() {
		idleTimer.Reset(sseIdleTimeout)

		line := scanner.Text()

		if strings.HasPrefix(line, "data: ") {
			eventData.WriteString(strings.TrimPrefix(line, "data: "))
			eventData.WriteString("\n")
		} else if line == "" && eventData.Len() > 0 {
			t.processEvent(workspaceID, eventData.String())
			eventData.Reset()
		}
	}

	if idleCtx.Err() != nil {
		return fmt.Errorf("SSE idle timeout for workspace %s", workspaceID)
	}
	return fmt.Errorf("SSE stream ended for workspace %s", workspaceID)
}

func (t *SSETracker) processEvent(workspaceID, data string) {
	data = strings.TrimSpace(data)
	if data == "" {
		return
	}

	// Parse the flat opencode event format:
	// {"type":"session.status","properties":{"sessionID":"ses_...","status":{"type":"idle"}}}
	var evt sseEvent
	if err := json.Unmarshal([]byte(data), &evt); err != nil || evt.Type == "" {
		// Try legacy nested format: {"payload":{"type":"...","properties":{...}}}
		var nested opencodeEvent
		if json.Unmarshal([]byte(data), &nested) == nil && nested.Payload.Type != "" {
			if t.onRawEvent != nil {
				t.onRawEvent(workspaceID, nested.Payload.Type, data)
			}
			t.dispatchProperties(workspaceID, nested.Payload.Type, nested.Payload.Properties)
		}
		return
	}

	if t.onRawEvent != nil {
		t.onRawEvent(workspaceID, evt.Type, data)
	}
	t.dispatchProperties(workspaceID, evt.Type, evt.Properties)
}

func (t *SSETracker) dispatchProperties(workspaceID, eventType string, props json.RawMessage) {
	if eventType == "session.updated" && len(props) > 0 && t.onInference != nil {
		t.handleSessionUpdated(workspaceID, props)
	}
	if eventType != "session.status" || len(props) == 0 {
		return
	}

	var p struct {
		SessionID string `json:"sessionID"`
		Status    struct {
			Type string `json:"type"`
		} `json:"status"`
	}
	if json.Unmarshal(props, &p) != nil || p.SessionID == "" {
		return
	}

	switch p.Status.Type {
	case "idle":
		if t.onSessionIdle != nil {
			t.onSessionIdle(workspaceID, p.SessionID)
		}
		t.drainMu.Lock()
		subs := make([]*drainSub, 0, len(t.drainSubs[workspaceID]))
		for _, s := range t.drainSubs[workspaceID] {
			subs = append(subs, s)
		}
		t.drainMu.Unlock()
		for _, s := range subs {
			s.onIdle(workspaceID, p.SessionID)
		}
	case "busy", "retry":
		if t.onSessionActive != nil {
			t.onSessionActive(workspaceID, p.SessionID)
		}
		t.drainMu.Lock()
		subs := make([]*drainSub, 0, len(t.drainSubs[workspaceID]))
		for _, s := range t.drainSubs[workspaceID] {
			subs = append(subs, s)
		}
		t.drainMu.Unlock()
		for _, s := range subs {
			s.onActive(workspaceID, p.SessionID)
		}
	}
}

// handleSessionUpdated fires the inference callback when a session.updated event
// carries new token output. Uses per-session cumulative tracking to emit deltas.
func (t *SSETracker) handleSessionUpdated(workspaceID string, props []byte) {
	var p struct {
		ID    string `json:"id"`
		Model struct {
			ID         string `json:"id"`
			ProviderID string `json:"providerID"`
		} `json:"model"`
		Tokens struct {
			Input  int64 `json:"input"`
			Output int64 `json:"output"`
		} `json:"tokens"`
		Cost float64 `json:"cost"`
	}
	if json.Unmarshal(props, &p) != nil || p.ID == "" || p.Tokens.Output == 0 || p.Model.ID == "" {
		return
	}
	key := workspaceID + ":" + p.ID
	totalNow := p.Tokens.Input + p.Tokens.Output
	t.tokensMu.Lock()
	prev := t.sessionTokenSeen[key]
	if totalNow <= prev {
		t.tokensMu.Unlock()
		return
	}
	t.sessionTokenSeen[key] = totalNow
	t.tokensMu.Unlock()
	inputDelta, outputDelta := p.Tokens.Input, p.Tokens.Output
	if prev > 0 {
		inputDelta, outputDelta = 0, totalNow-prev
	}
	t.onInference(workspaceID, p.Model.ID, p.Model.ProviderID, inputDelta, outputDelta, p.Cost)
}
