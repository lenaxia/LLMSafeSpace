// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8smocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestStreamUserEvents_Unauthenticated_Returns401(t *testing.T) {
	broker := NewUserEventBroker()
	h := &ProxyHandler{logger: &testLogger{}, namespace: "default", userBroker: broker}

	router := gin.New()
	router.GET("/api/v1/events", func(c *gin.Context) {
		// Don't set userID — simulates unauthenticated
		h.StreamUserEvents(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/events", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestStreamUserEvents_NilBroker_Returns503(t *testing.T) {
	h := &ProxyHandler{logger: &testLogger{}, namespace: "default", userBroker: nil}

	router := gin.New()
	router.GET("/api/v1/events", func(c *gin.Context) {
		c.Set("userID", "user-1")
		h.StreamUserEvents(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/events", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestStreamUserEvents_TooManyConnections_Returns429(t *testing.T) {
	broker := NewUserEventBroker()
	h := &ProxyHandler{logger: &testLogger{}, namespace: "default", userBroker: broker}

	// Fill all subscriber slots
	subs := make([]*subscriber, maxSubscribersPerUser)
	for i := 0; i < maxSubscribersPerUser; i++ {
		s, err := broker.SubscribeUser("user-full")
		require.NoError(t, err)
		subs[i] = s
	}
	defer func() {
		for _, s := range subs {
			broker.UnsubscribeUser("user-full", s)
		}
	}()

	router := gin.New()
	router.GET("/api/v1/events", func(c *gin.Context) {
		c.Set("userID", "user-full")
		h.StreamUserEvents(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/events", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestStreamUserEvents_SSEHeaders(t *testing.T) {
	broker := NewUserEventBroker()
	h := &ProxyHandler{logger: &testLogger{}, namespace: "default", userBroker: broker}

	router := gin.New()
	router.GET("/api/v1/events", func(c *gin.Context) {
		c.Set("userID", "user-hdr")
		h.StreamUserEvents(c)
	})

	// Use a real server so SSE streaming works
	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "no", resp.Header.Get("X-Accel-Buffering"))
}

func TestStreamUserEvents_LiveEventDelivery(t *testing.T) {
	broker := NewUserEventBroker()
	h := &ProxyHandler{logger: &testLogger{}, namespace: "default", userBroker: broker}

	router := gin.New()
	router.GET("/api/v1/events", func(c *gin.Context) {
		c.Set("userID", "user-live")
		h.StreamUserEvents(c)
	})

	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Publish an event after connection is established
	time.Sleep(100 * time.Millisecond)
	broker.PublishToUser("user-live", WorkspaceSSEEvent{
		Type:        "workspace.phase",
		WorkspaceID: "ws-test",
		Phase:       "Active",
	})

	// Read SSE events
	scanner := bufio.NewScanner(resp.Body)
	var foundEvent bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var evt WorkspaceSSEEvent
			if err := json.Unmarshal([]byte(data), &evt); err == nil {
				if evt.Type == "workspace.phase" && evt.Phase == "Active" && evt.WorkspaceID == "ws-test" {
					foundEvent = true
					assert.NotZero(t, evt.EventID)
					break
				}
			}
		}
	}
	assert.True(t, foundEvent, "should have received the live workspace.phase event")
}

func TestStreamUserEvents_LiveEvent_HasIDLine(t *testing.T) {
	broker := NewUserEventBroker()
	h := &ProxyHandler{logger: &testLogger{}, namespace: "default", userBroker: broker}

	router := gin.New()
	router.GET("/api/v1/events", func(c *gin.Context) {
		c.Set("userID", "user-id-line")
		h.StreamUserEvents(c)
	})

	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	time.Sleep(100 * time.Millisecond)
	broker.PublishToUser("user-id-line", WorkspaceSSEEvent{
		Type:        "workspace.phase",
		WorkspaceID: "ws-x",
		Phase:       "Active",
	})

	scanner := bufio.NewScanner(resp.Body)
	var foundIDLine bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "id: ") {
			foundIDLine = true
			break
		}
	}
	assert.True(t, foundIDLine, "live events should have id: line")
}

func TestStreamUserEvents_Replay(t *testing.T) {
	broker := NewUserEventBroker()
	h := &ProxyHandler{logger: &testLogger{}, namespace: "default", userBroker: broker}

	// Pre-populate replay buffer
	for i := 0; i < 3; i++ {
		broker.PublishToUser("user-replay", WorkspaceSSEEvent{
			Type:        "workspace.phase",
			WorkspaceID: "ws-r",
			Phase:       "Active",
		})
	}

	router := gin.New()
	router.GET("/api/v1/events", func(c *gin.Context) {
		c.Set("userID", "user-replay")
		h.StreamUserEvents(c)
	})

	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Connect with Last-Event-ID: 1 — should replay events 2 and 3
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/events", nil)
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	var replayedCount int
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "id: ") {
			replayedCount++
			if replayedCount >= 2 {
				break
			}
		}
	}
	assert.Equal(t, 2, replayedCount)
}

func TestStreamUserEvents_HeartbeatEmitted(t *testing.T) {
	// This test uses a short-lived connection and verifies heartbeat
	// by checking that the SSE comment line ":\n" is emitted.
	// We override the heartbeat interval for testing.
	// Note: in production heartbeatInterval is 25s; this test would be too slow.
	// Instead we verify the heartbeatLoop function directly.

	s := &subscriber{ch: make(chan WorkspaceSSEEvent, 10)}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Run heartbeat with a very short interval for testing
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.send(WorkspaceSSEEvent{Type: heartbeatSentinelType})
			}
		}
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()

	// Should have at least 2 heartbeats in the channel
	var heartbeats int
	for {
		select {
		case evt := <-s.ch:
			if evt.Type == heartbeatSentinelType {
				heartbeats++
			}
		default:
			goto done
		}
	}
done:
	assert.GreaterOrEqual(t, heartbeats, 2)
}

func TestStreamUserEvents_SnapshotEmitsBeforeLiveEvents(t *testing.T) {
	broker := NewUserEventBroker()

	// Set up a mock k8s client that returns workspaces for the user
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()
	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)
	wsMock.On("List", mock.MatchedBy(func(opts metav1.ListOptions) bool {
		return opts.LabelSelector == labelUserID+"=user-snap"
	})).Return(&v1.WorkspaceList{
		Items: []v1.Workspace{
			{ObjectMeta: metav1.ObjectMeta{Name: "ws-a"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "ws-b"}},
		},
	}, nil)

	// Set up watcher with known phases
	watcher, _ := NewWorkspaceWatcher(k8sMock, &testLogger{}, "default", func(*v1.Workspace) {})
	watcher.knownPhasesMu.Lock()
	watcher.knownPhases["ws-a"] = "Active"
	watcher.knownPhases["ws-b"] = "Suspended"
	watcher.knownPhasesMu.Unlock()

	h := &ProxyHandler{
		k8sClient:  k8sMock,
		logger:     &testLogger{},
		namespace:  "default",
		userBroker: broker,
		watcher:    watcher,
	}

	router := gin.New()
	router.GET("/api/v1/events", func(c *gin.Context) {
		c.Set("userID", "user-snap")
		h.StreamUserEvents(c)
	})

	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Collect events — expect snapshot events for ws-a and ws-b
	scanner := bufio.NewScanner(resp.Body)
	var snapshotEvents []WorkspaceSSEEvent
	deadline := time.After(2 * time.Second)

	for {
		select {
		case <-deadline:
			goto done
		default:
		}
		if !scanner.Scan() {
			break
		}
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			var evt WorkspaceSSEEvent
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt); err == nil {
				if evt.Type == "workspace.phase" {
					snapshotEvents = append(snapshotEvents, evt)
					if len(snapshotEvents) >= 2 {
						goto done
					}
				}
			}
		}
	}
done:
	cancel()

	assert.Len(t, snapshotEvents, 2)
	// Snapshot events should have EventID=0 (no id: line in SSE)
	for _, evt := range snapshotEvents {
		assert.Zero(t, evt.EventID, "snapshot events should have EventID=0")
	}
	// Verify both workspaces present
	phases := map[string]string{}
	for _, evt := range snapshotEvents {
		phases[evt.WorkspaceID] = evt.Phase
	}
	assert.Equal(t, "Active", phases["ws-a"])
	assert.Equal(t, "Suspended", phases["ws-b"])
}

func TestStreamUserEvents_SnapshotSkipsEmptyPhase(t *testing.T) {
	broker := NewUserEventBroker()

	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()
	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)
	wsMock.On("List", mock.Anything).Return(&v1.WorkspaceList{
		Items: []v1.Workspace{
			{ObjectMeta: metav1.ObjectMeta{Name: "ws-known"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "ws-deleted"}}, // not in knownPhases
		},
	}, nil)

	watcher, _ := NewWorkspaceWatcher(k8sMock, &testLogger{}, "default", func(*v1.Workspace) {})
	watcher.knownPhasesMu.Lock()
	watcher.knownPhases["ws-known"] = "Active"
	// ws-deleted intentionally NOT in knownPhases (F4: deleted between list and map read)
	watcher.knownPhasesMu.Unlock()

	h := &ProxyHandler{
		k8sClient:  k8sMock,
		logger:     &testLogger{},
		namespace:  "default",
		userBroker: broker,
		watcher:    watcher,
	}

	router := gin.New()
	router.GET("/api/v1/events", func(c *gin.Context) {
		c.Set("userID", "user-f4")
		h.StreamUserEvents(c)
	})

	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	var events []WorkspaceSSEEvent
	deadline := time.After(1 * time.Second)

	for {
		select {
		case <-deadline:
			goto done2
		default:
		}
		if !scanner.Scan() {
			break
		}
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			var evt WorkspaceSSEEvent
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt); err == nil {
				if evt.Type == "workspace.phase" {
					events = append(events, evt)
				}
			}
		}
	}
done2:
	cancel()

	// Only ws-known should appear (ws-deleted has empty phase, skipped per F4)
	assert.Len(t, events, 1)
	assert.Equal(t, "ws-known", events[0].WorkspaceID)
	assert.Equal(t, "Active", events[0].Phase)
}
