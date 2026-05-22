package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSSETracker(onIdle SessionIdleCallback) *SSETracker {
	return NewSSETracker(&http.Client{Timeout: 2 * time.Second}, &testLogger{}, onIdle)
}

func TestSSETracker_ProcessEvent_SessionStatusIdle(t *testing.T) {
	var mu sync.Mutex
	var calls []struct {
		sandboxID string
		sessionID string
	}
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {
		mu.Lock()
		calls = append(calls, struct {
			sandboxID string
			sessionID string
		}{sandboxID, sessionID})
		mu.Unlock()
	})

	data, _ := json.Marshal(sseEvent{
		Type:      "session.status",
		SessionID: "sess-1",
		Status:    "idle",
	})
	tracker.processEvent("sb-1", string(data))

	mu.Lock()
	require.Len(t, calls, 1)
	assert.Equal(t, "sb-1", calls[0].sandboxID)
	assert.Equal(t, "sess-1", calls[0].sessionID)
	mu.Unlock()
}

func TestSSETracker_ProcessEvent_IgnoresNonSessionStatus(t *testing.T) {
	var called int32
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {
		atomic.AddInt32(&called, 1)
	})

	tests := []struct {
		name  string
		event sseEvent
	}{
		{"other event type", sseEvent{Type: "session.created", SessionID: "sess-1", Status: "idle"}},
		{"output event", sseEvent{Type: "session.output", SessionID: "sess-1", Status: "idle"}},
		{"ping event", sseEvent{Type: "ping", SessionID: "", Status: ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			atomic.StoreInt32(&called, 0)
			data, _ := json.Marshal(tt.event)
			tracker.processEvent("sb-1", string(data))
			assert.Equal(t, int32(0), atomic.LoadInt32(&called))
		})
	}
}

func TestSSETracker_ProcessEvent_IgnoresBusyStatus(t *testing.T) {
	var called int32
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {
		atomic.AddInt32(&called, 1)
	})

	tests := []struct {
		name   string
		status string
	}{
		{"busy", "busy"},
		{"active", "active"},
		{"running", "running"},
		{"pending", "pending"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			atomic.StoreInt32(&called, 0)
			data, _ := json.Marshal(sseEvent{
				Type:      "session.status",
				SessionID: "sess-1",
				Status:    tt.status,
			})
			tracker.processEvent("sb-1", string(data))
			assert.Equal(t, int32(0), atomic.LoadInt32(&called))
		})
	}
}

func TestSSETracker_ProcessEvent_IgnoresMalformedJSON(t *testing.T) {
	var called int32
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {
		atomic.AddInt32(&called, 1)
	})

	tests := []struct {
		name string
		data string
	}{
		{"not json", "this is not json"},
		{"broken json", `{"type":"session.status","session_id":`},
		{"random text", "hello world"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			atomic.StoreInt32(&called, 0)
			tracker.processEvent("sb-1", tt.data)
			assert.Equal(t, int32(0), atomic.LoadInt32(&called))
		})
	}
}

func TestSSETracker_ProcessEvent_IgnoresEmptyData(t *testing.T) {
	var called int32
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {
		atomic.AddInt32(&called, 1)
	})

	tests := []struct {
		name string
		data string
	}{
		{"empty string", ""},
		{"whitespace only", "   "},
		{"newlines", "\n\n"},
		{"tabs and spaces", "\t  \t"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			atomic.StoreInt32(&called, 0)
			tracker.processEvent("sb-1", tt.data)
			assert.Equal(t, int32(0), atomic.LoadInt32(&called))
		})
	}
}

func TestSSETracker_ProcessEvent_IgnoresIdleWithEmptySessionID(t *testing.T) {
	var called int32
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {
		atomic.AddInt32(&called, 1)
	})

	data, _ := json.Marshal(sseEvent{
		Type:      "session.status",
		SessionID: "",
		Status:    "idle",
	})
	tracker.processEvent("sb-1", string(data))
	assert.Equal(t, int32(0), atomic.LoadInt32(&called))
}

func TestSSETracker_EnsureWatching_CreatesSubscription(t *testing.T) {
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})
	tracker.SetPasswordGetter(func(ctx context.Context, sandboxID string) (string, error) {
		return "", fmt.Errorf("test error")
	})

	tracker.EnsureWatching("sb-1")
	assert.Equal(t, 1, tracker.SubscriptionCount())

	tracker.Stop()
}

func TestSSETracker_EnsureWatching_NoDuplicateSubscription(t *testing.T) {
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})
	tracker.SetPasswordGetter(func(ctx context.Context, sandboxID string) (string, error) {
		return "", fmt.Errorf("test error")
	})

	tracker.EnsureWatching("sb-1")
	tracker.EnsureWatching("sb-1")
	tracker.EnsureWatching("sb-1")
	assert.Equal(t, 1, tracker.SubscriptionCount())

	tracker.Stop()
}

func TestSSETracker_EnsureWatching_MultipleSandboxes(t *testing.T) {
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})
	tracker.SetPasswordGetter(func(ctx context.Context, sandboxID string) (string, error) {
		return "", fmt.Errorf("test error")
	})

	tracker.EnsureWatching("sb-1")
	tracker.EnsureWatching("sb-2")
	tracker.EnsureWatching("sb-3")
	assert.Equal(t, 3, tracker.SubscriptionCount())

	tracker.Stop()
}

func TestSSETracker_StopWatching_CancelsSubscription(t *testing.T) {
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})
	tracker.SetPasswordGetter(func(ctx context.Context, sandboxID string) (string, error) {
		return "", fmt.Errorf("test error")
	})

	tracker.EnsureWatching("sb-1")
	tracker.EnsureWatching("sb-2")
	assert.Equal(t, 2, tracker.SubscriptionCount())

	tracker.StopWatching("sb-1")
	assert.Equal(t, 1, tracker.SubscriptionCount())

	tracker.Stop()
}

func TestSSETracker_StopWatching_NonexistentSandbox(t *testing.T) {
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})

	tracker.StopWatching("sb-nonexistent")
	assert.Equal(t, 0, tracker.SubscriptionCount())
}

func TestSSETracker_Stop_CancelsAllSubscriptions(t *testing.T) {
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})
	tracker.SetPasswordGetter(func(ctx context.Context, sandboxID string) (string, error) {
		return "", fmt.Errorf("test error")
	})

	tracker.EnsureWatching("sb-1")
	tracker.EnsureWatching("sb-2")
	tracker.EnsureWatching("sb-3")
	assert.Equal(t, 3, tracker.SubscriptionCount())

	tracker.Stop()
	assert.Equal(t, 0, tracker.SubscriptionCount())
}

func TestSSETracker_SubscriptionCount_ZeroInitially(t *testing.T) {
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})
	assert.Equal(t, 0, tracker.SubscriptionCount())
}

func TestSSETracker_SubscriptionCount_AccurateAfterOperations(t *testing.T) {
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})
	tracker.SetPasswordGetter(func(ctx context.Context, sandboxID string) (string, error) {
		return "", fmt.Errorf("test error")
	})

	assert.Equal(t, 0, tracker.SubscriptionCount())

	tracker.EnsureWatching("sb-1")
	assert.Equal(t, 1, tracker.SubscriptionCount())

	tracker.EnsureWatching("sb-2")
	assert.Equal(t, 2, tracker.SubscriptionCount())

	tracker.StopWatching("sb-1")
	assert.Equal(t, 1, tracker.SubscriptionCount())

	tracker.EnsureWatching("sb-3")
	assert.Equal(t, 2, tracker.SubscriptionCount())

	tracker.Stop()
	assert.Equal(t, 0, tracker.SubscriptionCount())
}

func TestSSETracker_SetPasswordGetter(t *testing.T) {
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})
	require.Nil(t, tracker.passwordGetter)

	getter := func(ctx context.Context, sandboxID string) (string, error) {
		return "test-password", nil
	}
	tracker.SetPasswordGetter(getter)
	require.NotNil(t, tracker.passwordGetter)

	pw, err := tracker.passwordGetter(context.Background(), "sb-1")
	assert.NoError(t, err)
	assert.Equal(t, "test-password", pw)
}

func TestSSETracker_Subscribe_ReceivesIdleEvent(t *testing.T) {
	var mu sync.Mutex
	var idleCalls []struct {
		sandboxID string
		sessionID string
	}

	sseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		assert.Equal(t, "opencode", user)
		assert.Equal(t, "test-pw", pass)
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		events := []sseEvent{
			{Type: "session.status", SessionID: "sess-1", Status: "idle"},
			{Type: "session.status", SessionID: "sess-2", Status: "busy"},
			{Type: "session.output", SessionID: "sess-1", Status: ""},
			{Type: "session.status", SessionID: "sess-3", Status: "idle"},
		}
		for _, evt := range events {
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}))
	defer sseServer.Close()

	tracker := NewSSETracker(
		&http.Client{
			Transport: &redirectTransport{server: sseServer},
			Timeout:   5 * time.Second,
		},
		&testLogger{},
		func(sandboxID, sessionID string) {
			mu.Lock()
			idleCalls = append(idleCalls, struct {
				sandboxID string
				sessionID string
			}{sandboxID, sessionID})
			mu.Unlock()
		},
	)
	tracker.SetPasswordGetter(func(ctx context.Context, sandboxID string) (string, error) {
		return "test-pw", nil
	})
	tracker.SetPodIPResolver(func(sandboxID string) string {
		return "10.0.0.1"
	})

	tracker.EnsureWatching("sb-1")

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(idleCalls) == 2
	}, 5*time.Second, 50*time.Millisecond, "expected 2 idle callbacks")

	mu.Lock()
	assert.Equal(t, "sb-1", idleCalls[0].sandboxID)
	assert.Equal(t, "sess-1", idleCalls[0].sessionID)
	assert.Equal(t, "sb-1", idleCalls[1].sandboxID)
	assert.Equal(t, "sess-3", idleCalls[1].sessionID)
	mu.Unlock()

	tracker.Stop()
}
