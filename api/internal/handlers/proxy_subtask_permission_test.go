// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lenaxia/llmsafespace/pkg/agent"
	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
)

// TestSubtaskPermission_BubblesToRootSession verifies the end-to-end fix for
// the bug where opencode subagent (subtask) permission requests were silently
// dropped by the chat UI because the SSE event carried the SUBTASK's sessionID
// but the URL session was the PARENT.
//
// Scenario:
//
//	parent session: ses_root (user-visible)
//	  └─ child subtask:  ses_child (e.g. @explore subagent invoked via task tool)
//
// When the subtask raises a `permission.asked`, the API:
//  1. Receives the SSE event with sessionID=ses_child
//  2. Resolves ses_child's parent chain via GET /session/ses_child
//  3. Publishes agent.permission with session_id=ses_child AND
//     root_session_id=ses_root so the frontend's filter can bubble it up.
func TestSubtaskPermission_BubblesToRootSession(t *testing.T) {
	// Backend returns parentID for ses_child → ses_root, and ses_root has none.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "opencode", user)
		assert.Equal(t, "test-password", pass)

		switch r.URL.Path {
		case "/session/ses_child":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":       "ses_child",
				"parentID": "ses_root",
			})
		case "/session/ses_root":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "ses_root",
				// no parentID
			})
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer backend.Close()

	env := newInputTestEnv(t)
	env.handler.httpClient = &http.Client{
		Transport: &redirectTransport{server: backend},
		Timeout:   5 * time.Second,
	}
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.handler.EnableSessionParentResolution()

	env.handler.broker = NewWorkspaceEventBroker()
	ch := env.handler.broker.Subscribe("ws-1")
	defer env.handler.broker.Unsubscribe("ws-1", ch)

	envelope := makeEnvelope("permission.asked", map[string]interface{}{
		"id":         "per_subtask",
		"sessionID":  "ses_child",
		"permission": "shell",
		"patterns":   []string{"ls"},
	})
	env.handler.onRawEvent("ws-1", "permission.asked", envelope)

	// Receive raw passthrough event first, then the normalized one.
	recvWithTimeout(t, ch, "opencode.event")
	evt := recvWithTimeout(t, ch, "agent.permission")

	req, ok := evt.Data.(*agent.PermissionRequest)
	require.True(t, ok, "event data should be *agent.PermissionRequest, got %T", evt.Data)
	assert.Equal(t, "per_subtask", req.ID)
	assert.Equal(t, "ses_child", req.SessionID, "SessionID stays the subtask")
	assert.Equal(t, "ses_root", req.RootSessionID, "RootSessionID points to user-visible parent")
}

// TestSubtaskPermission_ResolutionDisabled_RootEqualsSelf verifies that when
// EnableSessionParentResolution is NOT called, RootSessionID falls back to the
// event's own SessionID. This preserves the safe default: the frontend
// continues to see prompts for top-level sessions, and the only regression is
// that subtask prompts are no longer bubbled (matching V1 behavior).
func TestSubtaskPermission_ResolutionDisabled_RootEqualsSelf(t *testing.T) {
	env := newInputTestEnv(t)
	env.handler.broker = NewWorkspaceEventBroker()
	// NOT calling EnableSessionParentResolution

	ch := env.handler.broker.Subscribe("ws-1")
	defer env.handler.broker.Unsubscribe("ws-1", ch)

	envelope := makeEnvelope("permission.asked", map[string]interface{}{
		"id":         "per_x",
		"sessionID":  "ses_x",
		"permission": "shell",
	})

	// Need to set up the workspace mock for shouldAutoApprovePermissions check
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.handler.onRawEvent("ws-1", "permission.asked", envelope)

	recvWithTimeout(t, ch, "opencode.event")
	evt := recvWithTimeout(t, ch, "agent.permission")
	req := evt.Data.(*agent.PermissionRequest)
	assert.Equal(t, "ses_x", req.SessionID)
	assert.Equal(t, "ses_x", req.RootSessionID, "fallback to self when resolution is disabled")
}

// TestSubtaskPermission_TopLevelSession_RootEqualsSelf verifies that a
// permission from the root session itself (no parent) sets root_session_id
// equal to session_id.
func TestSubtaskPermission_TopLevelSession_RootEqualsSelf(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session/ses_top" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "ses_top",
				// no parentID
			})
			return
		}
		http.Error(w, "unexpected path", http.StatusNotFound)
	}))
	defer backend.Close()

	env := newInputTestEnv(t)
	env.handler.httpClient = &http.Client{
		Transport: &redirectTransport{server: backend},
		Timeout:   5 * time.Second,
	}
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.handler.EnableSessionParentResolution()

	env.handler.broker = NewWorkspaceEventBroker()
	ch := env.handler.broker.Subscribe("ws-1")
	defer env.handler.broker.Unsubscribe("ws-1", ch)

	envelope := makeEnvelope("permission.asked", map[string]interface{}{
		"id":         "per_top",
		"sessionID":  "ses_top",
		"permission": "shell",
	})
	env.handler.onRawEvent("ws-1", "permission.asked", envelope)

	recvWithTimeout(t, ch, "opencode.event")
	evt := recvWithTimeout(t, ch, "agent.permission")
	req := evt.Data.(*agent.PermissionRequest)
	assert.Equal(t, "ses_top", req.SessionID)
	assert.Equal(t, "ses_top", req.RootSessionID, "top-level session is its own root")
}

// TestSubtaskQuestion_BubblesToRootSession is the question-event analog of
// TestSubtaskPermission_BubblesToRootSession. Subagent question prompts must
// also bubble up to the parent session.
func TestSubtaskQuestion_BubblesToRootSession(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session/ses_child":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "ses_child", "parentID": "ses_root"})
		case "/session/ses_root":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "ses_root"})
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer backend.Close()

	env := newInputTestEnv(t)
	env.handler.httpClient = &http.Client{
		Transport: &redirectTransport{server: backend},
		Timeout:   5 * time.Second,
	}
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.handler.EnableSessionParentResolution()

	env.handler.broker = NewWorkspaceEventBroker()
	ch := env.handler.broker.Subscribe("ws-1")
	defer env.handler.broker.Unsubscribe("ws-1", ch)

	envelope := makeEnvelope("question.asked", map[string]interface{}{
		"id":        "que_subtask",
		"sessionID": "ses_child",
		"questions": []map[string]interface{}{
			{
				"question": "Pick one",
				"header":   "Choose",
				"options":  []map[string]string{{"label": "A", "description": "x"}},
			},
		},
	})
	env.handler.onRawEvent("ws-1", "question.asked", envelope)

	recvWithTimeout(t, ch, "opencode.event")
	evt := recvWithTimeout(t, ch, "agent.question")
	req := evt.Data.(*agent.QuestionRequest)
	assert.Equal(t, "que_subtask", req.ID)
	assert.Equal(t, "ses_child", req.SessionID)
	assert.Equal(t, "ses_root", req.RootSessionID)
}

// TestSubtaskPermission_FetcherFails_FallsBackToSelf verifies graceful
// degradation: when the workspace pod is unreachable, the resolver still
// publishes the event using sessionID as the root. Better to bubble to the
// (possibly wrong) subtask view than to silently drop the user's prompt.
func TestSubtaskPermission_FetcherFails_FallsBackToSelf(t *testing.T) {
	// Backend returns 500 for every session lookup
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer backend.Close()

	env := newInputTestEnv(t)
	env.handler.httpClient = &http.Client{
		Transport: &redirectTransport{server: backend},
		Timeout:   5 * time.Second,
	}
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.handler.EnableSessionParentResolution()

	env.handler.broker = NewWorkspaceEventBroker()
	ch := env.handler.broker.Subscribe("ws-1")
	defer env.handler.broker.Unsubscribe("ws-1", ch)

	envelope := makeEnvelope("permission.asked", map[string]interface{}{
		"id":         "per_x",
		"sessionID":  "ses_unreachable",
		"permission": "shell",
	})
	env.handler.onRawEvent("ws-1", "permission.asked", envelope)

	recvWithTimeout(t, ch, "opencode.event")
	evt := recvWithTimeout(t, ch, "agent.permission")
	req := evt.Data.(*agent.PermissionRequest)
	assert.Equal(t, "ses_unreachable", req.SessionID)
	assert.Equal(t, "ses_unreachable", req.RootSessionID, "fallback to self when fetch fails")
}

// TestSessionParentCache_InvalidateOnWorkspaceCacheFlush verifies that
// invalidateCaches() drops cached session-parent entries for a workspace —
// otherwise stale parents from before a suspend/restart could be returned.
func TestSessionParentCache_InvalidateOnWorkspaceCacheFlush(t *testing.T) {
	calls := 0
	env := newInputTestEnv(t)
	env.handler.sessionParents = newSessionParentCache(
		func(_ context.Context, _, _ string) (string, error) {
			calls++
			return "", nil
		},
	)

	_ = env.handler.sessionParents.resolveRoot(context.Background(), "ws-1", "ses_x")
	require.Equal(t, 1, calls)

	env.handler.invalidateCaches("ws-1")

	_ = env.handler.sessionParents.resolveRoot(context.Background(), "ws-1", "ses_x")
	require.Equal(t, 2, calls, "cache must be invalidated on workspace cache flush")
}

// TestE2E_SubtaskPermission_BubblesThroughSSE is the full-stack end-to-end
// test for the subtask permission bubbling fix. It exercises the path that
// the production browser actually uses:
//
//	opencode pod  → onRawEvent           (synthetic raw SSE event)
//	              → emitNormalizedInputEvent
//	              → resolveRootSessionID  (real fetcher → fake opencode HTTP)
//	              → broker.Publish
//	              → StreamEvents handler  (real /api/v1/workspaces/:id/events route)
//	              → SSE wire format       (real "data: {...}\n\n" framing)
//	              → client reads bytes off the socket and parses JSON
//
// The assertion is on the actual JSON line a browser would see, not on
// in-process broker channels. This is the test that proves the bug
// reported in worklog 0121 is fixed for real users.
func TestE2E_SubtaskPermission_BubblesThroughSSE(t *testing.T) {
	// 1. Stand up a fake opencode pod that returns a parent chain:
	//      ses_child → parentID=ses_root → no parent
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session/ses_child":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":       "ses_child",
				"parentID": "ses_root",
			})
		case "/session/ses_root":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "ses_root",
			})
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer backend.Close()

	// 2. Wire up a real ProxyHandler with the fake backend, broker, dialect,
	//    and parent resolution enabled (matching production wiring in app.go).
	env := newInputTestEnv(t)
	env.handler.httpClient = &http.Client{
		Transport: &redirectTransport{server: backend},
		Timeout:   5 * time.Second,
	}
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.handler.broker = NewWorkspaceEventBroker()
	env.handler.EnableSessionParentResolution()

	// 3. Mount the real /events route the browser hits.
	router := newStreamEventsRouter(env.handler)

	// 4. Open the SSE stream as a client would.
	cancel, body, _, _ := doStreamingRequest(router, "/api/v1/workspaces/ws-1/events")
	defer cancel()
	defer body.Close()

	// Wait for the StreamEvents handler to actually subscribe before publishing,
	// otherwise the event would be lost (broker fan-out has no replay).
	require.Eventually(t, func() bool {
		env.handler.broker.mu.Lock()
		n := len(env.handler.broker.subs["ws-1"])
		env.handler.broker.mu.Unlock()
		return n > 0
	}, 2*time.Second, 5*time.Millisecond, "subscriber should register on /events open")

	// 5. Inject the subtask's permission.asked event as if it came from the pod.
	envelope := makeEnvelope("permission.asked", map[string]interface{}{
		"id":         "per_e2e",
		"sessionID":  "ses_child",
		"permission": "shell",
		"patterns":   []string{"rm -rf /"},
	})
	env.handler.onRawEvent("ws-1", "permission.asked", envelope)

	// 6. Read SSE wire bytes; first event is the raw passthrough, second is
	//    the normalized agent.permission. Assert on the JSON the browser sees.
	reader := bufio.NewReader(body)

	rawEvt := readNextSSEDataLine(t, reader)
	assert.Equal(t, "opencode.event", rawEvt["type"], "first SSE line should be the raw opencode passthrough")

	normEvt := readNextSSEDataLine(t, reader)
	assert.Equal(t, "agent.permission", normEvt["type"])

	data, ok := normEvt["data"].(map[string]interface{})
	require.True(t, ok, "agent.permission.data must be an object, got %T", normEvt["data"])
	assert.Equal(t, "per_e2e", data["id"])
	assert.Equal(t, "ses_child", data["session_id"], "session_id stays the subtask")
	assert.Equal(t, "ses_root", data["root_session_id"], "root_session_id resolved to user-visible parent")
	assert.Equal(t, "shell", data["permission"])
}

// TestE2E_SubtaskQuestion_BubblesThroughSSE is the question-event analog of
// TestE2E_SubtaskPermission_BubblesThroughSSE. Same full-stack path, different
// event type — important to keep in lockstep because the fix touches both.
func TestE2E_SubtaskQuestion_BubblesThroughSSE(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session/ses_child":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "ses_child", "parentID": "ses_root"})
		case "/session/ses_root":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "ses_root"})
		default:
			http.Error(w, "unexpected: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer backend.Close()

	env := newInputTestEnv(t)
	env.handler.httpClient = &http.Client{
		Transport: &redirectTransport{server: backend},
		Timeout:   5 * time.Second,
	}
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.handler.broker = NewWorkspaceEventBroker()
	env.handler.EnableSessionParentResolution()

	router := newStreamEventsRouter(env.handler)
	cancel, body, _, _ := doStreamingRequest(router, "/api/v1/workspaces/ws-1/events")
	defer cancel()
	defer body.Close()

	require.Eventually(t, func() bool {
		env.handler.broker.mu.Lock()
		n := len(env.handler.broker.subs["ws-1"])
		env.handler.broker.mu.Unlock()
		return n > 0
	}, 2*time.Second, 5*time.Millisecond)

	envelope := makeEnvelope("question.asked", map[string]interface{}{
		"id":        "que_e2e",
		"sessionID": "ses_child",
		"questions": []map[string]interface{}{
			{
				"question": "Confirm?",
				"header":   "Confirm action",
				"options":  []map[string]string{{"label": "Yes", "description": "go"}},
			},
		},
	})
	env.handler.onRawEvent("ws-1", "question.asked", envelope)

	reader := bufio.NewReader(body)
	_ = readNextSSEDataLine(t, reader) // raw opencode.event

	normEvt := readNextSSEDataLine(t, reader)
	assert.Equal(t, "agent.question", normEvt["type"])
	data := normEvt["data"].(map[string]interface{})
	assert.Equal(t, "que_e2e", data["id"])
	assert.Equal(t, "ses_child", data["session_id"])
	assert.Equal(t, "ses_root", data["root_session_id"])
}

// TestE2E_NestedSubtask_TwoLevelsBubbleToRoot proves the cache walks the
// chain correctly when the user has a subtask that itself spawned a
// subtask (e.g. `task` → `task`). The deepest grandchild's permission must
// still bubble to the user-visible root.
func TestE2E_NestedSubtask_TwoLevelsBubbleToRoot(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session/ses_grandchild":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "ses_grandchild", "parentID": "ses_child"})
		case "/session/ses_child":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "ses_child", "parentID": "ses_root"})
		case "/session/ses_root":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "ses_root"})
		default:
			http.Error(w, "unexpected: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer backend.Close()

	env := newInputTestEnv(t)
	env.handler.httpClient = &http.Client{
		Transport: &redirectTransport{server: backend},
		Timeout:   5 * time.Second,
	}
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.handler.broker = NewWorkspaceEventBroker()
	env.handler.EnableSessionParentResolution()

	router := newStreamEventsRouter(env.handler)
	cancel, body, _, _ := doStreamingRequest(router, "/api/v1/workspaces/ws-1/events")
	defer cancel()
	defer body.Close()

	require.Eventually(t, func() bool {
		env.handler.broker.mu.Lock()
		n := len(env.handler.broker.subs["ws-1"])
		env.handler.broker.mu.Unlock()
		return n > 0
	}, 2*time.Second, 5*time.Millisecond)

	envelope := makeEnvelope("permission.asked", map[string]interface{}{
		"id":         "per_nested",
		"sessionID":  "ses_grandchild",
		"permission": "shell",
		"patterns":   []string{"ls"},
	})
	env.handler.onRawEvent("ws-1", "permission.asked", envelope)

	reader := bufio.NewReader(body)
	_ = readNextSSEDataLine(t, reader) // raw

	normEvt := readNextSSEDataLine(t, reader)
	require.Equal(t, "agent.permission", normEvt["type"])
	data := normEvt["data"].(map[string]interface{})
	assert.Equal(t, "ses_grandchild", data["session_id"])
	assert.Equal(t, "ses_root", data["root_session_id"], "two-level walk should reach the root")
}

// avoid unused-import lint when only some tests reference these
var _ = metav1.GetOptions{}
var _ = (*agent.PermissionRequest)(nil)
