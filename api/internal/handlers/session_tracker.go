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

type sseEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

type SSETracker struct {
	httpClient      *http.Client
	logger          pkginterfaces.LoggerInterface
	onSessionIdle   SessionIdleCallback
	onSessionActive SessionIdleCallback
	subscriptions   map[string]context.CancelFunc
	subMu           sync.Mutex
	passwordGetter  func(ctx context.Context, workspaceID string) (string, error)
	podIPResolver   func(workspaceID string) string
}

func NewSSETracker(
	httpClient *http.Client,
	logger pkginterfaces.LoggerInterface,
	onSessionIdle SessionIdleCallback,
) *SSETracker {
	return &SSETracker{
		httpClient:    httpClient,
		logger:        logger,
		onSessionIdle: onSessionIdle,
		subscriptions: make(map[string]context.CancelFunc),
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

func (t *SSETracker) EnsureWatching(workspaceID string) {
	t.subMu.Lock()
	defer t.subMu.Unlock()

	if _, exists := t.subscriptions[workspaceID]; exists {
		return
	}

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
			t.logger.Error("SSE subscription error", err, "workspaceID", workspaceID)
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
	defer resp.Body.Close()

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

	var evt sseEvent
	if err := json.Unmarshal([]byte(data), &evt); err != nil {
		return
	}

	if evt.Type != "session.status" || evt.SessionID == "" {
		return
	}

	switch evt.Status {
	case "idle":
		t.onSessionIdle(workspaceID, evt.SessionID)
	case "busy":
		if t.onSessionActive != nil {
			t.onSessionActive(workspaceID, evt.SessionID)
		}
	}
}
