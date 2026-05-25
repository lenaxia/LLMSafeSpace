package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8smocks "github.com/lenaxia/llmsafespace/mocks/kubernetes"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// newStreamEventsRouter returns a gin engine with only the StreamEvents route
// wired. The ProxyHandler already has a broker attached.
func newStreamEventsRouter(h *ProxyHandler) *gin.Engine {
	r := gin.New()
	r.GET("/api/v1/workspaces/:id/events", h.StreamEvents)
	return r
}

// doStreamingRequest sends the request to the router and returns a cancel
// function plus the response body reader. The cancel function cancels the
// request context which simulates a client disconnect.
func doStreamingRequest(router *gin.Engine, path string) (cancel context.CancelFunc, body io.ReadCloser, respHeader http.Header, statusCode *int) {
	pr, pw := io.Pipe()
	sc := new(int)
	h := http.Header{}

	ctx, cancelFn := context.WithCancel(context.Background())

	go func() {
		req := httptest.NewRequestWithContext(ctx, "GET", path, nil)
		// Use a custom ResponseWriter that writes to the pipe.
		rw := &pipeResponseWriter{pw: pw, header: h, code: sc}
		router.ServeHTTP(rw, req)
		pw.Close()
	}()

	return cancelFn, pr, h, sc
}

// pipeResponseWriter is a minimal http.ResponseWriter that streams to an io.PipeWriter.
type pipeResponseWriter struct {
	pw     *io.PipeWriter
	header http.Header
	code   *int
}

func (p *pipeResponseWriter) Header() http.Header { return p.header }
func (p *pipeResponseWriter) WriteHeader(code int) {
	if *p.code == 0 {
		*p.code = code
	}
}
func (p *pipeResponseWriter) Write(b []byte) (int, error) {
	if *p.code == 0 {
		*p.code = http.StatusOK
	}
	return p.pw.Write(b)
}
func (p *pipeResponseWriter) Flush() {}

// readNextSSEDataLine reads lines from r until it finds a "data: ..." line,
// then returns the parsed JSON map. Fails the test on timeout.
func readNextSSEDataLine(t *testing.T, r *bufio.Reader) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var m map[string]interface{}
			if jsonErr := json.Unmarshal([]byte(data), &m); jsonErr == nil {
				return m
			}
		}
		if err != nil {
			t.Fatalf("SSE stream ended unexpectedly: %v", err)
		}
	}
	t.Fatal("timed out waiting for SSE data line")
	return nil
}

// --- Tests ---

func TestStreamEvents_WorkspaceNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()
	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	wsMock.On("Get", "ws-missing", metav1.GetOptions{}).
		Return(nil, fmt.Errorf("not found")).Once()

	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil)
	require.NoError(t, err)
	handler.broker = NewWorkspaceEventBroker()

	router := newStreamEventsRouter(handler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/workspaces/ws-missing/events", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStreamEvents_SetsSSEHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	env := newTestEnv(t)
	env.handler.broker = NewWorkspaceEventBroker()
	env.wsMock.On("Get", "ws-1", metav1.GetOptions{}).
		Return(makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1"), nil).Maybe()

	cancel, body, header, _ := doStreamingRequest(newStreamEventsRouter(env.handler), "/api/v1/workspaces/ws-1/events")
	defer body.Close()

	// Give the handler time to write headers.
	time.Sleep(30 * time.Millisecond)
	cancel()

	assert.Equal(t, "text/event-stream", header.Get("Content-Type"))
	assert.Equal(t, "no-cache", header.Get("Cache-Control"))
	assert.Equal(t, "keep-alive", header.Get("Connection"))
}

func TestStreamEvents_PhaseEventDeliveredToClient(t *testing.T) {
	gin.SetMode(gin.TestMode)

	env := newTestEnv(t)
	broker := NewWorkspaceEventBroker()
	env.handler.broker = broker
	env.wsMock.On("Get", "ws-1", metav1.GetOptions{}).
		Return(makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1"), nil).Maybe()

	cancel, body, _, _ := doStreamingRequest(newStreamEventsRouter(env.handler), "/api/v1/workspaces/ws-1/events")
	defer cancel()
	defer body.Close()

	// Wait for the subscriber to register.
	require.Eventually(t, func() bool {
		broker.mu.Lock()
		n := len(broker.subs["ws-1"])
		broker.mu.Unlock()
		return n > 0
	}, time.Second, 5*time.Millisecond)

	broker.Publish("ws-1", WorkspaceSSEEvent{Type: "workspace.phase", Phase: "Suspended"})

	evt := readNextSSEDataLine(t, bufio.NewReader(body))
	assert.Equal(t, "workspace.phase", evt["type"])
	assert.Equal(t, "Suspended", evt["phase"])
}

func TestStreamEvents_SessionStatusEventDeliveredToClient(t *testing.T) {
	gin.SetMode(gin.TestMode)

	env := newTestEnv(t)
	broker := NewWorkspaceEventBroker()
	env.handler.broker = broker
	env.wsMock.On("Get", "ws-1", metav1.GetOptions{}).
		Return(makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1"), nil).Maybe()

	cancel, body, _, _ := doStreamingRequest(newStreamEventsRouter(env.handler), "/api/v1/workspaces/ws-1/events")
	defer cancel()
	defer body.Close()

	require.Eventually(t, func() bool {
		broker.mu.Lock()
		n := len(broker.subs["ws-1"])
		broker.mu.Unlock()
		return n > 0
	}, time.Second, 5*time.Millisecond)

	broker.Publish("ws-1", WorkspaceSSEEvent{
		Type:      "session.status",
		SessionID: "s1",
		Status:    "idle",
	})

	evt := readNextSSEDataLine(t, bufio.NewReader(body))
	assert.Equal(t, "session.status", evt["type"])
	assert.Equal(t, "s1", evt["session_id"])
	assert.Equal(t, "idle", evt["status"])
}

func TestStreamEvents_ClientDisconnectUnsubscribes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	env := newTestEnv(t)
	broker := NewWorkspaceEventBroker()
	env.handler.broker = broker
	env.wsMock.On("Get", "ws-1", metav1.GetOptions{}).
		Return(makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1"), nil).Maybe()

	cancel, body, _, _ := doStreamingRequest(newStreamEventsRouter(env.handler), "/api/v1/workspaces/ws-1/events")
	defer body.Close()

	// Wait for subscriber to register.
	require.Eventually(t, func() bool {
		broker.mu.Lock()
		n := len(broker.subs["ws-1"])
		broker.mu.Unlock()
		return n > 0
	}, time.Second, 5*time.Millisecond)

	// Cancel the client request (simulate disconnect).
	cancel()

	// Broker should clean up the subscription.
	assert.Eventually(t, func() bool {
		broker.mu.Lock()
		n := len(broker.subs["ws-1"])
		broker.mu.Unlock()
		return n == 0
	}, time.Second, 5*time.Millisecond, "broker should unsubscribe disconnected client")
}

func TestStreamEvents_OnPhaseChange_PublishesToBroker(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()
	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil)
	require.NoError(t, err)

	broker := NewWorkspaceEventBroker()
	handler.broker = broker

	ch := broker.Subscribe("ws-1")
	defer broker.Unsubscribe("ws-1", ch)

	phases := []string{
		string(v1.WorkspacePhaseActive),
		"Suspending",
		"Suspended",
		"Terminating",
		"Terminated",
	}

	for _, phase := range phases {
		ws := makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", phase, "ws-1")
		handler.onPhaseChange(ws)

		select {
		case evt := <-ch:
			assert.Equal(t, "workspace.phase", evt.Type, "phase=%s", phase)
			assert.Equal(t, phase, evt.Phase, "phase=%s", phase)
		case <-time.After(time.Second):
			t.Fatalf("expected phase event for phase %s", phase)
		}
	}
}

func TestStreamEvents_OnSessionIdle_PublishesToBroker(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()
	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil)
	require.NoError(t, err)

	broker := NewWorkspaceEventBroker()
	handler.broker = broker

	ch := broker.Subscribe("ws-1")
	defer broker.Unsubscribe("ws-1", ch)

	handler.onSessionIdle("ws-1", "s1")

	select {
	case evt := <-ch:
		assert.Equal(t, "session.status", evt.Type)
		assert.Equal(t, "s1", evt.SessionID)
		assert.Equal(t, "idle", evt.Status)
	case <-time.After(time.Second):
		t.Fatal("expected session.status idle event from onSessionIdle")
	}
}

func TestStreamEvents_OnSessionActive_PublishesToBroker(t *testing.T) {
	k8sMock := k8smocks.NewMockKubernetesClient()
	llmMock := k8smocks.NewMockLLMSafespaceV1Interface()
	wsMock := k8smocks.NewMockWorkspaceInterface()
	k8sMock.On("LlmsafespaceV1").Return(llmMock)
	llmMock.On("Workspaces", "default").Return(wsMock)

	handler, err := NewProxyHandler(k8sMock, &testLogger{}, "default", nil)
	require.NoError(t, err)

	broker := NewWorkspaceEventBroker()
	handler.broker = broker

	// onSessionActive also needs wsConfig to track max sessions.
	handler.wsConfigMu.Lock()
	handler.wsConfig["ws-1"] = workspaceConfig{workspaceID: "ws-1", maxActiveSessions: 5}
	handler.wsConfigMu.Unlock()

	ch := broker.Subscribe("ws-1")
	defer broker.Unsubscribe("ws-1", ch)

	handler.onSessionActive("ws-1", "s2")

	select {
	case evt := <-ch:
		assert.Equal(t, "session.status", evt.Type)
		assert.Equal(t, "s2", evt.SessionID)
		assert.Equal(t, "busy", evt.Status)
	case <-time.After(time.Second):
		t.Fatal("expected session.status busy event from onSessionActive")
	}
}
