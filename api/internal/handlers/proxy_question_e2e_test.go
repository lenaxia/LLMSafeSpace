// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lenaxia/llmsafespaces/api/internal/services/eventbroker"
	agentoc "github.com/lenaxia/llmsafespaces/pkg/agent/opencode"
	v1 "github.com/lenaxia/llmsafespaces/pkg/apis/llmsafespaces/v1"
)

// readNextSSEDataLineOfType reads SSE lines until it finds a data line whose
// JSON "type" field matches want. Necessary because emitNormalizedInputEvent
// publishes both an opencode.event (raw) and the typed normalized event.
func readNextSSEDataLineOfType(t *testing.T, r *bufio.Reader, want string) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m := readNextSSEDataLine(t, r)
		if m["type"] == want {
			return m
		}
	}
	t.Fatalf("timed out waiting for SSE event of type %q", want)
	return nil
}

// TestE2E_QuestionFlow_FullRoundTrip closes the US-16.13 Definition-of-Done gap.
//
// It exercises the full question lifecycle through real handlers wired to a
// real broker, against a fake workspace pod (httptest.Server):
//
//	pod emits "question.asked"
//	  → tracker.onRawEvent → emitNormalizedInputEvent
//	  → broker publishes "agent.question"
//	  → SSE client (user browser) receives "agent.question"
//	POST /question/<id>/reply
//	  → proxy forwards to pod with Basic Auth + correct path
//	  → pod records the reply
//	pod emits "question.replied"
//	  → broker publishes "agent.question.resolved"
//	  → SSE client receives "agent.question.resolved"
//
// Unit-test variants in proxy_input_test.go cover each piece in isolation;
// this test wires the pieces together to prove the full request path.
func TestE2E_QuestionFlow_FullRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var (
		mu               sync.Mutex
		replyPathHit     string
		replyBody        string
		replyContentType string
	)

	podBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		require.True(t, ok, "Basic Auth must reach the pod")
		assert.Equal(t, "opencode", user)
		assert.Equal(t, "test-pw", pass)

		if r.Method == http.MethodPost && len(r.URL.Path) >= 8 && r.URL.Path[len(r.URL.Path)-6:] == "/reply" {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			replyPathHit = r.URL.Path
			replyBody = string(body)
			replyContentType = r.Header.Get("Content-Type")
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		// Default: echo method+path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"method": r.Method,
			"path":   r.URL.Path,
		})
	}))
	defer podBackend.Close()

	env := newTestEnvWithBackend(t, podBackend.Config.Handler.(http.HandlerFunc))
	env.handler.dialect = &agentoc.Dialect{}
	env.handler.userBroker = eventbroker.NewUserEventBroker()
	env.wsMock.On("Get", mock.Anything, "ws-1", metav1.GetOptions{}).
		Return(makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1"), nil).Maybe()
	env.setupPasswordWithT(t, "ws-1", "test-pw")

	// Question routes are not in the default proxy group; add them on the same router.
	env.router.GET("/api/v1/workspaces/:id/question", env.handler.ListQuestions)
	env.router.POST("/api/v1/workspaces/:id/question/:requestID/reply", env.handler.QuestionReply)
	env.router.POST("/api/v1/workspaces/:id/question/:requestID/reject", env.handler.QuestionReject)

	// Open the user-side SSE stream.
	cancel, body, _, status := doStreamingRequest(env.router, "/api/v1/workspaces/ws-1/events")
	defer cancel()
	defer body.Close()

	// Wait until the broker has an active subscriber (user SSE is wired).
	require.Eventually(t, func() bool {
		return env.handler.userBroker.WorkspaceSubscriberCount("ws-1") > 0
	}, 2*time.Second, 5*time.Millisecond)
	require.NotNil(t, status)

	// 1. Simulate the pod emitting a question.asked event via the tracker.
	env.handler.onRawEvent("ws-1", "question.asked", makeEnvelope("question.asked", map[string]interface{}{
		"id":        "que_e2e",
		"sessionID": "ses_e2e",
		"questions": []map[string]interface{}{
			{"question": "Pick?", "header": "H", "options": []map[string]string{{"label": "A", "description": "a"}}},
		},
	}))

	// 2. SSE client should receive the agent.question event.
	reader := bufio.NewReader(body)
	askEvt := readNextSSEDataLineOfType(t, reader, "agent.question")
	dataBytes, _ := json.Marshal(askEvt["data"])
	assert.Contains(t, string(dataBytes), `"id":"que_e2e"`)
	assert.Contains(t, string(dataBytes), `"session_id":"ses_e2e"`)

	// 3. User POSTs /question/que_e2e/reply with their answer.
	replyPayload := `{"answers":[["A"]]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/ws-1/question/que_e2e/reply",
		strings.NewReader(replyPayload))
	req.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "reply must succeed against the pod")

	// 4. Verify the pod received the reply at the correct dialect-specific path.
	mu.Lock()
	gotPath := replyPathHit
	gotBody := replyBody
	gotCT := replyContentType
	mu.Unlock()
	assert.NotEmpty(t, gotPath, "pod must receive the reply POST")
	assert.Contains(t, gotPath, "que_e2e", "path must include the question ID")
	assert.Contains(t, gotPath, "/reply", "path must be the reply endpoint")
	assert.Equal(t, replyPayload, gotBody, "reply body must reach the pod verbatim")
	assert.Equal(t, "application/json", gotCT)

	// 5. Simulate the pod emitting question.replied after consuming the answer.
	env.handler.onRawEvent("ws-1", "question.replied", makeEnvelope("question.replied", map[string]interface{}{
		"id":        "que_e2e",
		"sessionID": "ses_e2e",
		"answers":   [][]string{{"A"}},
	}))

	// 6. SSE client receives agent.question.resolved.
	resolvedEvt := readNextSSEDataLineOfType(t, reader, "agent.question.resolved")
	resData, ok := resolvedEvt["data"].(map[string]interface{})
	require.True(t, ok, "resolved event data must be a map")
	assert.Equal(t, "que_e2e", resData["request_id"])
	assert.Equal(t, "ses_e2e", resData["session_id"])
}

// TestE2E_QuestionFlow_RejectClearsQuestion mirrors the round-trip for the
// reject branch. Validates that POST /question/<id>/reject reaches the pod at
// the dialect-specific reject endpoint and emits question.rejected.
func TestE2E_QuestionFlow_RejectClearsQuestion(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var (
		mu           sync.Mutex
		rejectPath   string
		rejectMethod string
	)
	podBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); !ok {
			t.Errorf("pod backend requires Basic Auth")
		}
		if r.Method == http.MethodPost && len(r.URL.Path) >= 7 && r.URL.Path[len(r.URL.Path)-7:] == "/reject" {
			mu.Lock()
			rejectPath = r.URL.Path
			rejectMethod = r.Method
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"method": r.Method, "path": r.URL.Path})
	}))
	defer podBackend.Close()

	env := newTestEnvWithBackend(t, podBackend.Config.Handler.(http.HandlerFunc))
	env.handler.dialect = &agentoc.Dialect{}
	env.handler.userBroker = eventbroker.NewUserEventBroker()
	env.wsMock.On("Get", mock.Anything, "ws-1", metav1.GetOptions{}).
		Return(makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1"), nil).Maybe()
	env.setupPasswordWithT(t, "ws-1", "test-pw")

	env.router.POST("/api/v1/workspaces/:id/question/:requestID/reject", env.handler.QuestionReject)

	cancel, body, _, _ := doStreamingRequest(env.router, "/api/v1/workspaces/ws-1/events")
	defer cancel()
	defer body.Close()

	require.Eventually(t, func() bool {
		return env.handler.userBroker.WorkspaceSubscriberCount("ws-1") > 0
	}, 2*time.Second, 5*time.Millisecond)

	env.handler.onRawEvent("ws-1", "question.asked", makeEnvelope("question.asked", map[string]interface{}{
		"id":        "que_rej",
		"sessionID": "ses_rej",
		"questions": []map[string]interface{}{
			{"question": "Q?", "header": "H", "options": []map[string]string{{"label": "X", "description": "x"}}},
		},
	}))

	reader := bufio.NewReader(body)
	askEvt := readNextSSEDataLineOfType(t, reader, "agent.question")
	assert.Equal(t, "agent.question", askEvt["type"])

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/ws-1/question/que_rej/reject", nil)
	env.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	mu.Lock()
	gotPath := rejectPath
	gotMethod := rejectMethod
	mu.Unlock()
	assert.NotEmpty(t, gotPath, "pod must receive the reject POST")
	assert.Contains(t, gotPath, "que_rej")
	assert.Contains(t, gotPath, "/reject")
	assert.Equal(t, http.MethodPost, gotMethod)

	env.handler.onRawEvent("ws-1", "question.rejected", makeEnvelope("question.rejected", map[string]interface{}{
		"id":        "que_rej",
		"sessionID": "ses_rej",
	}))

	resolvedEvt := readNextSSEDataLineOfType(t, reader, "agent.question.resolved")
	resData, ok := resolvedEvt["data"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "que_rej", resData["request_id"])
}

// TestE2E_QuestionFlow_BadRequestIDReturns400 verifies the input validation
// gate: the proxy must reject malformed IDs before they reach the pod.
func TestE2E_QuestionFlow_BadRequestIDReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newTestEnv(t)
	env.handler.dialect = &agentoc.Dialect{}
	env.wsMock.On("Get", mock.Anything, "ws-1", metav1.GetOptions{}).
		Return(makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1"), nil).Maybe()
	env.setupPasswordWithT(t, "ws-1", "test-pw")

	env.router.POST("/api/v1/workspaces/:id/question/:requestID/reply", env.handler.QuestionReply)

	cases := []string{"not-an-id", "per_abc", "que_", "QUE_UPPER"}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost,
				"/api/v1/workspaces/ws-1/question/"+id+"/reply", nil)
			env.router.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code,
				"malformed ID %q must be rejected at the proxy before reaching the pod", id)
		})
	}
}

// TestE2E_QuestionFlow_SuspendedWorkspaceReturns503 verifies that a
// suspended workspace never reaches the pod — the proxy must 503 before
// forwarding.
func TestE2E_QuestionFlow_SuspendedWorkspaceReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	env := newTestEnv(t)
	env.handler.dialect = &agentoc.Dialect{}
	env.wsMock.On("Get", mock.Anything, "ws-1", metav1.GetOptions{}).
		Return(makeWorkspaceCRDWithStatus("ws-1", "10.0.0.1", string(v1.WorkspacePhaseSuspended), "ws-1"), nil).Maybe()

	env.router.POST("/api/v1/workspaces/:id/question/:requestID/reply", env.handler.QuestionReply)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/ws-1/question/que_abc/reply", nil)
	env.router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
