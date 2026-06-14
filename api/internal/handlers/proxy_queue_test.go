package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
