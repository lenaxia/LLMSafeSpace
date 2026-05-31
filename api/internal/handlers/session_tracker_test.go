// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

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

// --- Nested format (opencode /event format) ---

func makeNestedEvent(eventType string, props map[string]interface{}) string {
	propsJSON, _ := json.Marshal(props)
	data, _ := json.Marshal(map[string]interface{}{
		"directory": "ws-1",
		"payload": map[string]interface{}{
			"type":       eventType,
			"properties": json.RawMessage(propsJSON),
		},
	})
	return string(data)
}

// makeSessionStatusEvent builds a real opencode flat-format session.status event.
// statusType is "idle" or "busy".
func makeSessionStatusEvent(sessionID, statusType string) string {
	data, _ := json.Marshal(map[string]interface{}{
		"type": "session.status",
		"properties": map[string]interface{}{
			"sessionID": sessionID,
			"status":    map[string]string{"type": statusType},
		},
	})
	return string(data)
}

// makeNestedSessionEvent builds a nested-format session.status event (legacy format).
// statusType is "idle" or "busy".
func makeNestedSessionEvent(statusType, sessionID string) string {
	return makeNestedEvent("session.status", map[string]interface{}{
		"sessionID": sessionID,
		"status":    map[string]string{"type": statusType},
	})
}

func makeNestedPartUpdatedEvent(sessionID, partType, text string) string {
	return makeNestedEvent("message.part.updated", map[string]interface{}{
		"sessionID": sessionID,
		"part": map[string]interface{}{
			"type": partType,
			"text": text,
		},
	})
}

func makeNestedMessageUpdatedEvent(sessionID string) string {
	return makeNestedEvent("message.updated", map[string]interface{}{
		"sessionID": sessionID,
		"info": map[string]interface{}{
			"id":   "msg-1",
			"role": "assistant",
		},
	})
}

func TestSSETracker_ProcessEvent_Nested_IdleEvent(t *testing.T) {
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

	tracker.processEvent("sb-1", makeNestedSessionEvent("idle", "sess-1"))

	mu.Lock()
	require.Len(t, calls, 1)
	assert.Equal(t, "sb-1", calls[0].sandboxID)
	assert.Equal(t, "sess-1", calls[0].sessionID)
	mu.Unlock()
}

func TestSSETracker_ProcessEvent_Nested_BusyEvent(t *testing.T) {
	var mu sync.Mutex
	var activeCalls []struct {
		sandboxID string
		sessionID string
	}
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})
	tracker.SetOnSessionActive(func(sandboxID, sessionID string) {
		mu.Lock()
		activeCalls = append(activeCalls, struct {
			sandboxID string
			sessionID string
		}{sandboxID, sessionID})
		mu.Unlock()
	})

	tracker.processEvent("sb-1", makeNestedSessionEvent("busy", "sess-1"))

	mu.Lock()
	require.Len(t, activeCalls, 1)
	assert.Equal(t, "sb-1", activeCalls[0].sandboxID)
	assert.Equal(t, "sess-1", activeCalls[0].sessionID)
	mu.Unlock()
}

func TestSSETracker_ProcessEvent_Nested_IgnoresNonSessionEvents(t *testing.T) {
	var called int32
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {
		atomic.AddInt32(&called, 1)
	})

	tracker.processEvent("sb-1", makeNestedPartUpdatedEvent("sess-1", "text", "hello"))
	tracker.processEvent("sb-1", makeNestedMessageUpdatedEvent("sess-1"))
	tracker.processEvent("sb-1", makeNestedEvent("session.created", map[string]interface{}{"sessionID": "sess-1"}))

	assert.Equal(t, int32(0), atomic.LoadInt32(&called), "idle callback should not be called for non-session.status events")
}

func TestSSETracker_ProcessEvent_Nested_IgnoresBusyStatus(t *testing.T) {
	var called int32
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {
		atomic.AddInt32(&called, 1)
	})

	for _, status := range []string{"active", "running", "pending"} {
		tracker.processEvent("sb-1", makeNestedSessionEvent(status, "sess-1"))
	}
	assert.Equal(t, int32(0), atomic.LoadInt32(&called))
}

func TestSSETracker_ProcessEvent_Nested_IgnoresEmptySessionID(t *testing.T) {
	var called int32
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {
		atomic.AddInt32(&called, 1)
	})

	tracker.processEvent("sb-1", makeNestedSessionEvent("idle", ""))
	tracker.processEvent("sb-1", makeNestedSessionEvent("busy", ""))
	assert.Equal(t, int32(0), atomic.LoadInt32(&called))
}

func TestSSETracker_ProcessEvent_Nested_IgnoresMalformedJSON(t *testing.T) {
	var called int32
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {
		atomic.AddInt32(&called, 1)
	})

	tests := []struct {
		name string
		data string
	}{
		{"missing payload", `{"directory":"ws-1"}`},
		{"payload with no type", `{"directory":"ws-1","payload":{"properties":{}}}`},
		{"payload type empty", `{"directory":"ws-1","payload":{"type":"","properties":{}}}`},
		{"properties not an object", `{"directory":"ws-1","payload":{"type":"session.status","properties":"string"}}`},
		{"completely wrong structure", `{"foo":"bar"}`},
		{"empty object", `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			atomic.StoreInt32(&called, 0)
			tracker.processEvent("sb-1", tt.data)
			assert.Equal(t, int32(0), atomic.LoadInt32(&called))
		})
	}
}

// --- RawEventCallback tests ---

func TestSSETracker_RawEventCallback_Nested_AllEventsForwarded(t *testing.T) {
	var mu sync.Mutex
	var rawEvents []struct {
		workspaceID string
		eventType   string
		rawData     string
	}

	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})
	tracker.SetOnRawEvent(func(workspaceID, eventType, rawData string) {
		mu.Lock()
		rawEvents = append(rawEvents, struct {
			workspaceID string
			eventType   string
			rawData     string
		}{workspaceID, eventType, rawData})
		mu.Unlock()
	})

	partUpdatedData := makeNestedPartUpdatedEvent("sess-1", "text", "hello")
	messageUpdatedData := makeNestedMessageUpdatedEvent("sess-1")
	sessionIdleData := makeNestedSessionEvent("idle", "sess-1")
	sessionBusyData := makeNestedSessionEvent("busy", "sess-1")

	tracker.processEvent("sb-1", partUpdatedData)
	tracker.processEvent("sb-1", messageUpdatedData)
	tracker.processEvent("sb-1", sessionIdleData)
	tracker.processEvent("sb-1", sessionBusyData)

	mu.Lock()
	require.Len(t, rawEvents, 4)

	assert.Equal(t, "sb-1", rawEvents[0].workspaceID)
	assert.Equal(t, "message.part.updated", rawEvents[0].eventType)
	assert.Contains(t, rawEvents[0].rawData, "message.part.updated")
	assert.Contains(t, rawEvents[0].rawData, "hello")

	assert.Equal(t, "message.updated", rawEvents[1].eventType)

	assert.Equal(t, "session.status", rawEvents[2].eventType)
	assert.Equal(t, "session.status", rawEvents[3].eventType)
	mu.Unlock()
}

func TestSSETracker_RawEventCallback_FlatFormatStillWorks(t *testing.T) {
	var mu sync.Mutex
	var rawEvents []struct {
		workspaceID string
		eventType   string
	}

	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})
	tracker.SetOnRawEvent(func(workspaceID, eventType, rawData string) {
		mu.Lock()
		rawEvents = append(rawEvents, struct {
			workspaceID string
			eventType   string
		}{workspaceID, eventType})
		mu.Unlock()
	})

	tracker.processEvent("sb-1", makeSessionStatusEvent("sess-1", "idle"))

	mu.Lock()
	require.Len(t, rawEvents, 1)
	assert.Equal(t, "sb-1", rawEvents[0].workspaceID)
	assert.Equal(t, "session.status", rawEvents[0].eventType)
	mu.Unlock()
}

func TestSSETracker_RawEventCallback_NotCalledWhenNil(t *testing.T) {
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})
	// onRawEvent is nil by default — should not panic

	tracker.processEvent("sb-1", makeNestedPartUpdatedEvent("sess-1", "text", "hello"))
	tracker.processEvent("sb-1", makeNestedSessionEvent("idle", "sess-1"))
	tracker.processEvent("sb-1", `{"type":"session.status","session_id":"sess-1","status":"idle"}`)
}

func TestSSETracker_RawEventCallback_PartUpdatedRawDataContainsFullPayload(t *testing.T) {
	var capturedRawData string
	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})
	tracker.SetOnRawEvent(func(workspaceID, eventType, rawData string) {
		capturedRawData = rawData
	})

	data := makeNestedPartUpdatedEvent("sess-1", "text", "hello world")
	tracker.processEvent("sb-1", data)

	// The raw data should include the directory and full payload
	assert.Contains(t, capturedRawData, `"directory"`)
	assert.Contains(t, capturedRawData, `"message.part.updated"`)
	assert.Contains(t, capturedRawData, `"sessionID"`)
	assert.Contains(t, capturedRawData, `"hello world"`)

	// Verify it's valid JSON that can be parsed back
	var parsed map[string]interface{}
	err := json.Unmarshal([]byte(capturedRawData), &parsed)
	assert.NoError(t, err)
	assert.Equal(t, "ws-1", parsed["directory"])
}

func TestSSETracker_RawEventCallback_CalledForAllNestedTypes(t *testing.T) {
	eventTypes := make(map[string]int)
	var mu sync.Mutex

	tracker := newTestSSETracker(func(sandboxID, sessionID string) {})
	tracker.SetOnRawEvent(func(workspaceID, eventType, rawData string) {
		mu.Lock()
		eventTypes[eventType]++
		mu.Unlock()
	})

	events := []string{
		"session.status",
		"session.created",
		"session.updated",
		"session.error",
		"message.part.updated",
		"message.updated",
		"message.error",
	}

	for _, et := range events {
		tracker.processEvent("sb-1", makeNestedEvent(et, map[string]interface{}{
			"sessionID": "sess-1",
		}))
	}

	mu.Lock()
	require.Len(t, eventTypes, len(events))
	for _, et := range events {
		assert.Equal(t, 1, eventTypes[et], "event type %s should be called once", et)
	}
	mu.Unlock()
}

// --- Existing flat-format tests ---

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

	tracker.processEvent("sb-1", makeSessionStatusEvent("sess-1", "idle"))

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
		name      string
		eventData string
	}{
		{"other event type", makeSessionStatusEvent("sess-1", "idle")}, // wrong type below
		{"output event", makeSessionStatusEvent("sess-1", "idle")},
		{"ping event", `{"type":"ping","properties":{}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			atomic.StoreInt32(&called, 0)
			// For non-session.status types, build them directly
			var data string
			switch tt.name {
			case "other event type":
				data = `{"type":"session.created","properties":{"sessionID":"sess-1","status":{"type":"idle"}}}`
			case "output event":
				data = `{"type":"session.output","properties":{"sessionID":"sess-1","status":{"type":"idle"}}}`
			case "ping event":
				data = `{"type":"ping","properties":{}}`
			}
			tracker.processEvent("sb-1", data)
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
			tracker.processEvent("sb-1", makeSessionStatusEvent("sess-1", tt.status))
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

	// Empty sessionID — should not call idle callback
	tracker.processEvent("sb-1", `{"type":"session.status","properties":{"sessionID":"","status":{"type":"idle"}}}`)
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

		// Emit real opencode flat-format events
		eventStrings := []string{
			makeSessionStatusEvent("sess-1", "idle"),
			makeSessionStatusEvent("sess-2", "busy"),
			`{"type":"session.output","properties":{"sessionID":"sess-1"}}`,
			makeSessionStatusEvent("sess-3", "idle"),
		}
		for _, evtStr := range eventStrings {
			fmt.Fprintf(w, "data: %s\n\n", evtStr)
			flusher.Flush()
		}
		// Block until client disconnects so the scanner sees EOF
		<-r.Context().Done()
	}))
	defer sseServer.Close()

	tracker := NewSSETracker(
		&http.Client{
			Transport: &redirectTransport{server: sseServer},
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
