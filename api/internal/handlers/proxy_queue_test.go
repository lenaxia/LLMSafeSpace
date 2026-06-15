package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/lenaxia/llmsafespace/api/internal/services/eventbroker"
	"github.com/lenaxia/llmsafespace/api/internal/services/msgqueue"
	ssetracker "github.com/lenaxia/llmsafespace/api/internal/services/sse"
	k8smocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

func setupQueueTestEnv(t *testing.T) (*ProxyHandler, *msgqueue.Service, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := msgqueue.NewWithClient(client)

	k8sMock := k8smocks.NewMockKubernetesClient()
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", &http.Client{}, nil)
	require.NoError(t, err)
	handler.SetMessageQueueService(svc)
	handler.broker = eventbroker.NewWorkspaceEventBroker()

	return handler, svc, func() {
		_ = client.Close()
		mr.Close()
	}
}

func TestEnqueueMessage_Success(t *testing.T) {
	handler, svc, cleanup := setupQueueTestEnv(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/workspaces/ws-1/sessions/ses-1/queue",
		strings.NewReader(`{"text":"hello"}`))
	c.Params = gin.Params{
		{Key: "id", Value: "ws-1"},
		{Key: "sessionId", Value: "ses-1"},
	}

	handler.EnqueueMessage(c)

	assert.Equal(t, http.StatusAccepted, w.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotEmpty(t, resp["messageID"])

	n, _ := svc.Len(context.Background(), "ws-1", "ses-1")
	assert.Equal(t, int64(1), n)
}

func TestEnqueueMessage_EmptyText(t *testing.T) {
	handler, _, cleanup := setupQueueTestEnv(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/queue", strings.NewReader(`{"text":""}`))
	c.Params = gin.Params{{Key: "id", Value: "ws-1"}, {Key: "sessionId", Value: "ses-1"}}

	handler.EnqueueMessage(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestEnqueueMessage_NoQueueService(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil, nil)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/queue", strings.NewReader(`{"text":"hi"}`))
	c.Params = gin.Params{{Key: "id", Value: "ws-1"}, {Key: "sessionId", Value: "ses-1"}}

	handler.EnqueueMessage(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestListQueue_Success(t *testing.T) {
	handler, svc, cleanup := setupQueueTestEnv(t)
	defer cleanup()

	_, err := svc.Enqueue(context.Background(), "ws-1", "ses-1", "first")
	require.NoError(t, err)
	_, err = svc.Enqueue(context.Background(), "ws-1", "ses-1", "second")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/queue", nil)
	c.Params = gin.Params{{Key: "id", Value: "ws-1"}, {Key: "sessionId", Value: "ses-1"}}

	handler.ListQueue(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Messages []msgqueue.QueuedMessage `json:"messages"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Len(t, resp.Messages, 2)
	assert.Equal(t, "first", resp.Messages[0].Text)
	assert.Equal(t, "second", resp.Messages[1].Text)
}

func TestListQueue_Empty(t *testing.T) {
	handler, _, cleanup := setupQueueTestEnv(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/queue", nil)
	c.Params = gin.Params{{Key: "id", Value: "ws-1"}, {Key: "sessionId", Value: "ses-1"}}

	handler.ListQueue(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Messages []msgqueue.QueuedMessage `json:"messages"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Len(t, resp.Messages, 0)
}

func TestDrainQueuedMessage_EmptyQueue(t *testing.T) {
	handler, _, cleanup := setupQueueTestEnv(t)
	defer cleanup()

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	assert.NotPanics(t, func() {
		handler.drainQueuedMessage("ws-1", "ses-1")
	})

	select {
	case <-sub.Ch:
		t.Fatal("should not publish event when queue is empty")
	default:
	}
}

func TestDrainQueuedMessage_SendsToOpencode(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/session/ses-1/prompt_async", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	transport := &redirectTransport{server: backend}
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	k8sMock := newMockK8sWithWorkspace(t, "ws-1", "10.0.0.1")
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", httpClient, nil)
	require.NoError(t, err)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	svc := msgqueue.NewWithClient(client)
	handler.SetMessageQueueService(svc)
	handler.broker = eventbroker.NewWorkspaceEventBroker()

	setupPasswordSecret(t, handler, "ws-1", "test-pw")

	_, err = svc.Enqueue(context.Background(), "ws-1", "ses-1", "queued msg")
	require.NoError(t, err)

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	go handler.drainQueuedMessage("ws-1", "ses-1")

	require.Eventually(t, func() bool {
		select {
		case evt := <-sub.Ch:
			if evt.Type != "queue.update" {
				return false
			}
			data, ok := evt.Data.(queueUpdateData)
			return ok && data.Event == "sent"
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond, "should publish queue.update with event=sent")

	n, _ := svc.Len(context.Background(), "ws-1", "ses-1")
	assert.Equal(t, int64(0), n, "message should be consumed from queue")
}

func TestDrainQueuedMessage_RequeuesOnFailure(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()

	transport := &redirectTransport{server: backend}
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	k8sMock := newMockK8sWithWorkspace(t, "ws-1", "10.0.0.1")
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", httpClient, nil)
	require.NoError(t, err)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	svc := msgqueue.NewWithClient(client)
	handler.SetMessageQueueService(svc)
	handler.broker = eventbroker.NewWorkspaceEventBroker()

	setupPasswordSecret(t, handler, "ws-1", "test-pw")

	_, err = svc.Enqueue(context.Background(), "ws-1", "ses-1", "will fail")
	require.NoError(t, err)

	go handler.drainQueuedMessage("ws-1", "ses-1")

	require.Eventually(t, func() bool {
		msgs, _ := svc.PeekAll(context.Background(), "ws-1", "ses-1")
		return len(msgs) == 1 && msgs[0].RetryCount == 1
	}, 3*time.Second, 10*time.Millisecond, "message should be requeued with incremented retry count")
}

func TestDrainQueuedMessage_DropsAfterMaxRetries(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()

	transport := &redirectTransport{server: backend}
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	k8sMock := newMockK8sWithWorkspace(t, "ws-1", "10.0.0.1")
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", httpClient, nil)
	require.NoError(t, err)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	svc := msgqueue.NewWithClient(client)
	handler.SetMessageQueueService(svc)
	handler.broker = eventbroker.NewWorkspaceEventBroker()

	setupPasswordSecret(t, handler, "ws-1", "test-pw")

	msg := msgqueue.QueuedMessage{
		ID:          "msg_maxed",
		Text:        "maxed out",
		SessionID:   "ses-1",
		WorkspaceID: "ws-1",
		RetryCount:  maxQueueRetries,
	}
	err = svc.Requeue(context.Background(), "ws-1", "ses-1", msg)
	require.NoError(t, err)

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	go handler.drainQueuedMessage("ws-1", "ses-1")

	require.Eventually(t, func() bool {
		select {
		case evt := <-sub.Ch:
			data, ok := evt.Data.(queueUpdateData)
			return ok && data.Event == "error"
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond, "should publish error event after max retries")

	n, _ := svc.Len(context.Background(), "ws-1", "ses-1")
	assert.Equal(t, int64(0), n, "message should be dropped after max retries")
}

func TestPublishQueueEvent(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil, nil)
	require.NoError(t, err)
	handler.broker = eventbroker.NewWorkspaceEventBroker()

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	handler.publishQueueEvent("ws-1", "ses-1", "sent", "msg_123", "")

	select {
	case evt := <-sub.Ch:
		assert.Equal(t, "queue.update", evt.Type)
		assert.Equal(t, "ses-1", evt.SessionID)
		data, ok := evt.Data.(queueUpdateData)
		require.True(t, ok)
		assert.Equal(t, "sent", data.Event)
		assert.Equal(t, "msg_123", data.MessageID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queue event")
	}
}

func TestDeleteQueueMessage_Success(t *testing.T) {
	handler, svc, cleanup := setupQueueTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	id, err := svc.Enqueue(ctx, "ws-1", "ses-1", "to delete")
	require.NoError(t, err)

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/:id/sessions/:sessionId/queue/:messageId", handler.DeleteQueueMessage)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/ws-1/sessions/ses-1/queue/"+id, nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	n, _ := svc.Len(ctx, "ws-1", "ses-1")
	assert.Equal(t, int64(0), n, "message should be removed from queue")

	select {
	case evt := <-sub.Ch:
		assert.Equal(t, "queue.update", evt.Type)
		data, ok := evt.Data.(queueUpdateData)
		require.True(t, ok)
		assert.Equal(t, "dismissed", data.Event)
		assert.Equal(t, id, data.MessageID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dismissed SSE event")
	}
}

func TestDeleteQueueMessage_NoQueueService(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil, nil)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/:id/sessions/:sessionId/queue/:messageId", handler.DeleteQueueMessage)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/ws-1/sessions/ses-1/queue/msg_123", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestDeleteQueueMessage_NotFound(t *testing.T) {
	handler, svc, cleanup := setupQueueTestEnv(t)
	defer cleanup()

	id, err := svc.Enqueue(context.Background(), "ws-1", "ses-1", "keep me")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/queue", nil)
	c.Params = gin.Params{
		{Key: "id", Value: "ws-1"},
		{Key: "sessionId", Value: "ses-1"},
		{Key: "messageId", Value: "nonexistent"},
	}

	handler.DeleteQueueMessage(c)

	n, _ := svc.Len(context.Background(), "ws-1", "ses-1")
	assert.Equal(t, int64(1), n, "unrelated message should remain")
	_ = id
}

// --- New tests for queue lifecycle behaviors ---

// TestOnPhaseChange_SuspendPublishesDismissedAndClears verifies that when a
// workspace transitions to Suspending, the handler publishes dismissed SSE
// events for all queued messages across all sessions and then clears the queue.
func TestOnPhaseChange_SuspendPublishesDismissedAndClears(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	svc := msgqueue.NewWithClient(client)

	k8sMock := k8smocks.NewMockKubernetesClient()
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil, nil)
	require.NoError(t, err)
	handler.SetMessageQueueService(svc)
	handler.broker = eventbroker.NewWorkspaceEventBroker()

	ctx := context.Background()
	id1, err := svc.Enqueue(ctx, "ws-1", "ses-A", "msg 1")
	require.NoError(t, err)
	id2, err := svc.Enqueue(ctx, "ws-1", "ses-B", "msg 2")
	require.NoError(t, err)

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	// Build a minimal workspace object in Suspending phase
	ws := makeWorkspaceCRDWithStatus("ws-1", "", string(v1.WorkspacePhaseSuspending), "")
	handler.onPhaseChange(ws)

	// Collect dismissed events (up to 2) within a reasonable timeout
	dismissed := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(dismissed) < 2 {
		select {
		case evt := <-sub.Ch:
			if evt.Type != "queue.update" {
				continue
			}
			data, ok := evt.Data.(queueUpdateData)
			if ok && data.Event == "dismissed" {
				dismissed[data.MessageID] = true
			}
		case <-deadline:
			t.Fatalf("timed out; only saw dismissed for: %v", dismissed)
		}
	}
	assert.True(t, dismissed[id1], "id1 should be dismissed")
	assert.True(t, dismissed[id2], "id2 should be dismissed")

	// Queue should be cleared
	n1, _ := svc.Len(ctx, "ws-1", "ses-A")
	n2, _ := svc.Len(ctx, "ws-1", "ses-B")
	assert.Equal(t, int64(0), n1, "ses-A queue should be cleared")
	assert.Equal(t, int64(0), n2, "ses-B queue should be cleared")
}

// TestAbortSession_FlushesQueueThenAborts verifies that AbortSession:
// 1. Publishes dismissed SSE events for all queued messages
// 2. Clears the queue from Redis
// 3. Proxies the abort to opencode
// 4. Launches the background flush-and-abort goroutine
func TestAbortSession_FlushesQueueThenAborts(t *testing.T) {
	abortCalled := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session/ses-1/abort" {
			abortCalled = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	transport := &redirectTransport{server: backend}
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	k8sMock := newMockK8sWithWorkspace(t, "ws-1", "10.0.0.1")
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", httpClient, nil)
	require.NoError(t, err)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	svc := msgqueue.NewWithClient(client)
	handler.SetMessageQueueService(svc)
	handler.broker = eventbroker.NewWorkspaceEventBroker()

	setupPasswordSecret(t, handler, "ws-1", "test-pw")

	ctx := context.Background()
	id1, _ := svc.Enqueue(ctx, "ws-1", "ses-1", "queued msg 1")
	id2, _ := svc.Enqueue(ctx, "ws-1", "ses-1", "queued msg 2")

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/:id/sessions/:sessionId/abort", handler.AbortSession)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/ws-1/sessions/ses-1/abort", nil)
	router.ServeHTTP(w, req)

	// Abort should proxy through
	assert.True(t, abortCalled, "abort should be proxied to opencode")

	// Queue should be cleared from Redis immediately
	require.Eventually(t, func() bool {
		n, _ := svc.Len(ctx, "ws-1", "ses-1")
		return n == 0
	}, 2*time.Second, 10*time.Millisecond, "queue should be cleared")

	// dismissed SSE events should be published for each queued message
	dismissed := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(dismissed) < 2 {
		select {
		case evt := <-sub.Ch:
			if evt.Type != "queue.update" {
				continue
			}
			data, ok := evt.Data.(queueUpdateData)
			if ok && data.Event == "dismissed" {
				dismissed[data.MessageID] = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for dismissed events; got: %v", dismissed)
		}
	}
	assert.True(t, dismissed[id1], "id1 should be dismissed")
	assert.True(t, dismissed[id2], "id2 should be dismissed")
}

// TestAbortSession_EmptyQueue_JustAborts verifies that AbortSession with no
// queued messages simply proxies the abort without touching the queue.
func TestAbortSession_EmptyQueue_JustAborts(t *testing.T) {
	abortCalled := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session/ses-1/abort" {
			abortCalled = true
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	transport := &redirectTransport{server: backend}
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	k8sMock := newMockK8sWithWorkspace(t, "ws-1", "10.0.0.1")
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", httpClient, nil)
	require.NoError(t, err)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	svc := msgqueue.NewWithClient(client)
	handler.SetMessageQueueService(svc)
	handler.broker = eventbroker.NewWorkspaceEventBroker()

	setupPasswordSecret(t, handler, "ws-1", "test-pw")

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/:id/sessions/:sessionId/abort", handler.AbortSession)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/ws-1/sessions/ses-1/abort", nil)
	router.ServeHTTP(w, req)

	assert.True(t, abortCalled, "abort should be proxied even with empty queue")
}

// TestClearQueueOnDispose_PublishesDismissedAndClears verifies that
// AgentReloadHandler.clearQueueOnDispose publishes dismissed SSE events for
// all queued messages and then clears the queue.
func TestClearQueueOnDispose_PublishesDismissedAndClears(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	svc := msgqueue.NewWithClient(client)
	broker := eventbroker.NewWorkspaceEventBroker()

	k8sMock := k8smocks.NewMockKubernetesClient()
	_ = k8sMock
	handler := NewAgentReloadHandler(nil, nil, nil, nil, &testLogger{})
	handler.SetQueueClearer(svc)
	handler.SetBrokerPublisher(broker)

	ctx := context.Background()
	id1, err := svc.Enqueue(ctx, "ws-1", "ses-A", "pending msg 1")
	require.NoError(t, err)
	id2, err := svc.Enqueue(ctx, "ws-1", "ses-B", "pending msg 2")
	require.NoError(t, err)

	sub := broker.Subscribe("ws-1")
	defer broker.Unsubscribe("ws-1", sub)

	handler.clearQueueOnDispose(ctx, "ws-1")

	// Collect dismissed events
	dismissed := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(dismissed) < 2 {
		select {
		case evt := <-sub.Ch:
			if evt.Type != "queue.update" {
				continue
			}
			data, ok := evt.Data.(queueUpdateData)
			if ok && data.Event == "dismissed" {
				dismissed[data.MessageID] = true
			}
		case <-deadline:
			t.Fatalf("timed out; got: %v", dismissed)
		}
	}
	assert.True(t, dismissed[id1])
	assert.True(t, dismissed[id2])

	n1, _ := svc.Len(ctx, "ws-1", "ses-A")
	n2, _ := svc.Len(ctx, "ws-1", "ses-B")
	assert.Equal(t, int64(0), n1)
	assert.Equal(t, int64(0), n2)
}

func newMockK8sWithWorkspace(t *testing.T, workspaceID, podIP string) *k8smocks.MockKubernetesClient {
	t.Helper()
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()
	k8sMock.On("LlmsafespaceV1").Return(llmMock, nil).Maybe()
	llmMock.On("Workspaces", "default").Return(wsMock).Maybe()
	ws := makeWorkspaceCRDWithStatus(workspaceID, podIP, string(v1.WorkspacePhaseActive), workspaceID)
	wsMock.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(ws, nil).Maybe()
	fakeClientset := k8sfake.NewSimpleClientset()
	k8sMock.On("Clientset").Return(fakeClientset).Maybe()
	return k8sMock
}

func setupPasswordSecret(t *testing.T, handler *ProxyHandler, workspaceID, password string) {
	t.Helper()
	secret := makePasswordSecret(workspaceID, password)
	_, err := handler.k8sClient.Clientset().CoreV1().Secrets("default").Create(context.Background(), secret, metav1.CreateOptions{})
	require.NoError(t, err)
}

// TestFlushAndAbortAfterIdle_SingleMessage verifies that flushAndAbortAfterIdle
// sends one message to opencode after the session goes idle and then issues
// a second abort.
func TestFlushAndAbortAfterIdle_SingleMessage(t *testing.T) {
	var receivedPaths []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPaths = append(receivedPaths, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	transport := &redirectTransport{server: backend}
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	k8sMock := newMockK8sWithWorkspace(t, "ws-1", "10.0.0.1")
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", httpClient, nil)
	require.NoError(t, err)
	handler.broker = eventbroker.NewWorkspaceEventBroker()
	setupPasswordSecret(t, handler, "ws-1", "test-pw")

	tracker := ssetracker.NewTracker(httpClient, &testLogger{}, func(workspaceID, sessionID string) {
		handler.onSessionIdle(workspaceID, sessionID)
	})
	handler.sseTracker = tracker

	sseIdle := func(sessionID string) {
		props, _ := json.Marshal(map[string]interface{}{
			"sessionID": sessionID,
			"status":    map[string]string{"type": "idle"},
		})
		tracker.DispatchProperties("ws-1", "session.status", props)
	}

	msg := msgqueue.QueuedMessage{ID: "msg_test_1", Text: "hello", SessionID: "ses-1", WorkspaceID: "ws-1"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.flushAndAbortAfterIdle("ws-1", "ses-1", []msgqueue.QueuedMessage{msg})
	}()

	// Give goroutine time to subscribe, then fire idle.
	time.Sleep(20 * time.Millisecond)
	sseIdle("ses-1")

	// After send, the session becomes busy again; fire another idle to complete the flow.
	time.Sleep(20 * time.Millisecond)
	sseIdle("ses-1")

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("flushAndAbortAfterIdle did not complete")
	}

	// Verify: prompt_async was called once, abort was called once.
	assert.Contains(t, receivedPaths, "/session/ses-1/prompt_async", "should have sent message to opencode")
	assert.Contains(t, receivedPaths, "/session/ses-1/abort", "should have issued second abort")
}

// TestFlushAndAbortAfterIdle_MultipleMessages verifies that flushAndAbortAfterIdle
// sends each message one at a time, waiting for idle between each, so that
// no 409 "session busy" errors occur.
func TestFlushAndAbortAfterIdle_MultipleMessages(t *testing.T) {
	var mu sync.Mutex
	var receivedPaths []string

	// Simulate: prompt_async succeeds, then session becomes busy (opencode processes it).
	// The test manually fires idle between messages.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedPaths = append(receivedPaths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	transport := &redirectTransport{server: backend}
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	k8sMock := newMockK8sWithWorkspace(t, "ws-1", "10.0.0.1")
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", httpClient, nil)
	require.NoError(t, err)
	handler.broker = eventbroker.NewWorkspaceEventBroker()
	setupPasswordSecret(t, handler, "ws-1", "test-pw")

	tracker := ssetracker.NewTracker(httpClient, &testLogger{}, func(workspaceID, sessionID string) {
		handler.onSessionIdle(workspaceID, sessionID)
	})
	handler.sseTracker = tracker

	sseIdle := func(sessionID string) {
		props, _ := json.Marshal(map[string]interface{}{
			"sessionID": sessionID,
			"status":    map[string]string{"type": "idle"},
		})
		tracker.DispatchProperties("ws-1", "session.status", props)
	}

	msgs := []msgqueue.QueuedMessage{
		{ID: "msg_a", Text: "first", SessionID: "ses-1", WorkspaceID: "ws-1"},
		{ID: "msg_b", Text: "second", SessionID: "ses-1", WorkspaceID: "ws-1"},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.flushAndAbortAfterIdle("ws-1", "ses-1", msgs)
	}()

	// Initial idle after abort → first message sent.
	time.Sleep(20 * time.Millisecond)
	sseIdle("ses-1")

	// Idle between msg1 and msg2 → second message sent.
	time.Sleep(50 * time.Millisecond)
	sseIdle("ses-1")

	// Final idle after second abort (not strictly needed for completion).
	time.Sleep(30 * time.Millisecond)
	sseIdle("ses-1")

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("flushAndAbortAfterIdle did not complete")
	}

	mu.Lock()
	paths := make([]string, len(receivedPaths))
	copy(paths, receivedPaths)
	mu.Unlock()

	promptCount := 0
	abortCount := 0
	for _, p := range paths {
		switch p {
		case "/session/ses-1/prompt_async":
			promptCount++
		case "/session/ses-1/abort":
			abortCount++
		}
	}
	assert.Equal(t, 2, promptCount, "both messages should be sent to opencode")
	assert.Equal(t, 1, abortCount, "exactly one second abort should be issued")
}

// TestAbortSession_FailurePreservesQueue verifies that if the abort proxy
// returns an error (>=400), the queue is NOT cleared and no dismissed SSE is published.
func TestAbortSession_FailurePreservesQueue(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()

	transport := &redirectTransport{server: backend}
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	k8sMock := newMockK8sWithWorkspace(t, "ws-1", "10.0.0.1")
	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", httpClient, nil)
	require.NoError(t, err)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	svc := msgqueue.NewWithClient(client)
	handler.SetMessageQueueService(svc)
	handler.broker = eventbroker.NewWorkspaceEventBroker()

	ctx := context.Background()
	_, _ = svc.Enqueue(ctx, "ws-1", "ses-1", "should survive abort failure")

	sub := handler.broker.Subscribe("ws-1")
	defer handler.broker.Unsubscribe("ws-1", sub)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/:id/sessions/:sessionId/abort", handler.AbortSession)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/ws-1/sessions/ses-1/abort", nil)
	router.ServeHTTP(w, req)

	// Queue must be untouched — abort failed.
	n, _ := svc.Len(ctx, "ws-1", "ses-1")
	assert.Equal(t, int64(1), n, "queue should be preserved when abort fails")

	// No dismissed SSE should have been published.
	select {
	case evt := <-sub.Ch:
		if evt.Type == "queue.update" {
			data, ok := evt.Data.(queueUpdateData)
			if ok && data.Event == "dismissed" {
				t.Fatalf("should not publish dismissed when abort fails, got: %+v", data)
			}
		}
	default:
		// Good — no dismissed events.
	}
}

// TestBulkReloadHandler_ClearQueueOnDispose verifies that
// BulkReloadHandler.clearQueueOnDispose publishes dismissed SSE and clears the queue.
func TestBulkReloadHandler_ClearQueueOnDispose(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()
	svc := msgqueue.NewWithClient(client)
	broker := eventbroker.NewWorkspaceEventBroker()

	h := &BulkReloadHandler{
		logger: &testLogger{},
	}
	h.SetQueueClearer(svc)
	h.SetBrokerPublisher(broker)

	ctx := context.Background()
	id1, err := svc.Enqueue(ctx, "ws-bulk", "ses-X", "bulk msg 1")
	require.NoError(t, err)
	id2, err := svc.Enqueue(ctx, "ws-bulk", "ses-Y", "bulk msg 2")
	require.NoError(t, err)

	sub := broker.Subscribe("ws-bulk")
	defer broker.Unsubscribe("ws-bulk", sub)

	h.clearQueueOnDispose(ctx, "ws-bulk")

	dismissed := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(dismissed) < 2 {
		select {
		case evt := <-sub.Ch:
			if evt.Type != "queue.update" {
				continue
			}
			data, ok := evt.Data.(queueUpdateData)
			if ok && data.Event == "dismissed" {
				dismissed[data.MessageID] = true
			}
		case <-deadline:
			t.Fatalf("timed out; got: %v", dismissed)
		}
	}
	assert.True(t, dismissed[id1])
	assert.True(t, dismissed[id2])

	n1, _ := svc.Len(ctx, "ws-bulk", "ses-X")
	n2, _ := svc.Len(ctx, "ws-bulk", "ses-Y")
	assert.Equal(t, int64(0), n1)
	assert.Equal(t, int64(0), n2)
}
