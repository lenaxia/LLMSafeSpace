// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- AbortSession ---

func TestAbortSession_Success(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/session/ses_abc/abort", r.URL.Path)
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "opencode", user)
		assert.Equal(t, "pw", pass)
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	}))
	defer server.Close()

	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 5 * time.Second}}
	orig := getAgentAddr()
	defer func() { setAgentAddr(orig) }()
	setAgentAddr(server.URL)

	err := client.AbortSession(context.Background(), "ses_abc")
	require.NoError(t, err)
	assert.True(t, called)
}

func TestAbortSession_Non2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 5 * time.Second}}
	orig := getAgentAddr()
	defer func() { setAgentAddr(orig) }()
	setAgentAddr(server.URL)

	err := client.AbortSession(context.Background(), "ses_notfound")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestAbortSession_NetworkError(t *testing.T) {
	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 100 * time.Millisecond}}
	orig := getAgentAddr()
	defer func() { setAgentAddr(orig) }()
	setAgentAddr("http://127.0.0.1:1") // nothing listening

	err := client.AbortSession(context.Background(), "ses_x")
	require.Error(t, err)
}

// --- abortStaleSessions ---
//
// abortStaleSessions aborts ALL sessions unconditionally after a restart,
// since session status is not persisted to SQLite and is not available via
// the REST API. Aborting an idle session is a no-op in opencode.

func TestAbortStaleSessions_AbortsAllSessions(t *testing.T) {
	var abortedSessions []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/session":
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"id": "ses_1", "title": "Session 1"},
				{"id": "ses_2", "title": "Session 2"},
				{"id": "ses_3", "title": "Session 3"},
			})
		case r.Method == http.MethodPost:
			path := r.URL.Path
			sid := path[len("/session/") : len(path)-len("/abort")]
			abortedSessions = append(abortedSessions, sid)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
		default:
			// fetchSessionTitle fallback
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{})
		}
	}))
	defer server.Close()

	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 5 * time.Second}}
	orig := getAgentAddr()
	defer func() { setAgentAddr(orig) }()
	setAgentAddr(server.URL)

	withTestLogger(t)
	abortStaleSessions(context.Background(), client, log)

	assert.ElementsMatch(t, []string{"ses_1", "ses_2", "ses_3"}, abortedSessions)
}

func TestAbortStaleSessions_NoSessions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/session" {
			_ = json.NewEncoder(w).Encode([]map[string]string{})
		}
	}))
	defer server.Close()

	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 5 * time.Second}}
	orig := getAgentAddr()
	defer func() { setAgentAddr(orig) }()
	setAgentAddr(server.URL)

	withTestLogger(t)
	// Should not panic or error on empty session list.
	abortStaleSessions(context.Background(), client, log)
}

func TestAbortStaleSessions_AbortFailureContinues(t *testing.T) {
	// ses_1 abort fails, ses_2 abort succeeds — both must be attempted.
	var abortAttempts []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/session":
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"id": "ses_1", "title": "One"},
				{"id": "ses_2", "title": "Two"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_1/abort":
			abortAttempts = append(abortAttempts, "ses_1")
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_2/abort":
			abortAttempts = append(abortAttempts, "ses_2")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
		default:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{})
		}
	}))
	defer server.Close()

	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 5 * time.Second}}
	orig := getAgentAddr()
	defer func() { setAgentAddr(orig) }()
	setAgentAddr(server.URL)

	withTestLogger(t)
	// Should not panic even when one abort fails.
	abortStaleSessions(context.Background(), client, log)
	assert.ElementsMatch(t, []string{"ses_1", "ses_2"}, abortAttempts)
}

func TestAbortStaleSessions_ListSessionsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 5 * time.Second}}
	orig := getAgentAddr()
	defer func() { setAgentAddr(orig) }()
	setAgentAddr(server.URL)

	withTestLogger(t)
	// Should not panic when list fails.
	abortStaleSessions(context.Background(), client, log)
}

// --- managedProcess.onStart integration ---

func TestManagedProcess_OnStartCalledOnBoot(t *testing.T) {
	started := make(chan struct{}, 4)
	p := &managedProcess{
		cmdFactory: func() *exec.Cmd {
			return exec.Command("sleep", "30")
		},
		onStart: func() {
			select {
			case started <- struct{}{}:
			default:
			}
		},
	}
	p.start()
	t.Cleanup(p.stop)

	// Deterministic: onStart MUST fire on the initial boot. This is the
	// production wiring the fix guarantees — onStart is set in the struct
	// literal before start(). The original bug assigned onStart after
	// start(), racing with supervise()'s mutex-protected read and
	// sometimes skipping the boot invocation.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("onStart did not fire on initial boot within 5s; production wiring is racy")
	}
}

func TestManagedProcess_OnStartCalledOnRestart(t *testing.T) {
	started := make(chan struct{}, 4)
	p := &managedProcess{
		cmdFactory: func() *exec.Cmd {
			return exec.Command("sleep", "30")
		},
		onStart: func() {
			select {
			case started <- struct{}{}:
			default:
			}
		},
	}
	p.start()
	t.Cleanup(p.stop)

	// First invocation: boot.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("onStart did not fire on initial boot")
	}

	p.restart()

	// Second invocation: after restart.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("onStart did not fire after restart")
	}
}

func TestManagedProcess_NilOnStartIsNoop(t *testing.T) {
	p := &managedProcess{
		cmdFactory: func() *exec.Cmd { return exec.Command("sleep", "30") },
		onStart:    nil,
	}
	p.start()
	// Wait for the supervisor to start the child so stop()'s SIGTERM
	// reaches a live process. Without this, stop() can observe p.cmd ==
	// nil (supervisor hasn't reached the assignment yet) and block until
	// the backoff cycle elapses.
	waitForChildStart(t, p)
	// Should not panic. stop() joins the supervisor cleanly.
	p.stop()
}

// waitForChildStart polls until the supervisor has assigned a running
// child process. Used by tests that don't have an onStart signal to
// synchronize on.
func waitForChildStart(t *testing.T, p *managedProcess) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		cmd := p.cmd
		p.mu.Unlock()
		if cmd != nil && cmd.Process != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("supervisor did not start a child process within 5s")
}

// --- abortStaleSessionsAfterStart ---

func TestAbortStaleSessionsAfterStart_WaitsForHealth(t *testing.T) {
	var healthCallCount atomic.Int32
	var abortCalled atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health":
			n := healthCallCount.Add(1)
			if n < 3 {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"healthy": false})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"healthy": true, "version": "test"})
		case "/session":
			_ = json.NewEncoder(w).Encode([]map[string]string{})
		default:
			if r.Method == http.MethodPost {
				abortCalled.Store(true)
			}
		}
	}))
	defer server.Close()

	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 5 * time.Second}}
	orig := getAgentAddr()
	defer func() { setAgentAddr(orig) }()
	setAgentAddr(server.URL)

	withTestLogger(t)
	abortStaleSessionsAfterStart(context.Background(), client, log)

	assert.GreaterOrEqual(t, int(healthCallCount.Load()), 3, "should poll health until healthy")
}

// TestAbortStaleSessionsAfterStart_AbortsSessionsAfterHealth stitches together
// the two halves that the unit-level tests cover independently: health-polling
// (TestAbortStaleSessionsAfterStart_WaitsForHealth) and session abort
// (TestAbortStaleSessions_AbortsAllSessions). With non-empty /session
// responses, this proves the full production wiring: opencode becomes healthy →
// abortStaleSessionsAfterStart lists sessions → each is POST-aborted. Closes
// the gap where a regression in the wiring between abortStaleSessionsAfterStart
// and abortStaleSessions (e.g. a future refactor that breaks the call) would
// not be caught by the existing tests.
func TestAbortStaleSessionsAfterStart_AbortsSessionsAfterHealth(t *testing.T) {
	var mu sync.Mutex
	var aborted []string
	var healthChecks int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/global/health" && r.Method == http.MethodGet:
			atomic.AddInt32(&healthChecks, 1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"healthy": true, "version": "test"})
		case r.URL.Path == "/session" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"id": "ses_stale1", "title": "Stale One"},
				{"id": "ses_stale2", "title": "Stale Two"},
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/abort"):
			sid := strings.TrimPrefix(r.URL.Path, "/session/")
			sid = strings.TrimSuffix(sid, "/abort")
			mu.Lock()
			aborted = append(aborted, sid)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
			return
		default:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{})
		}
	}))
	defer server.Close()

	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 5 * time.Second}}
	orig := getAgentAddr()
	defer func() { setAgentAddr(orig) }()
	setAgentAddr(server.URL)

	withTestLogger(t)
	abortStaleSessionsAfterStart(context.Background(), client, log)

	assert.GreaterOrEqual(t, atomic.LoadInt32(&healthChecks), int32(1),
		"should have polled health at least once before aborting")

	mu.Lock()
	got := append([]string(nil), aborted...)
	mu.Unlock()
	assert.ElementsMatch(t, []string{"ses_stale1", "ses_stale2"}, got,
		"both sessions must be aborted after health check passes — the full "+
			"abortStaleSessionsAfterStart → abortStaleSessions wiring must be intact")
}

func TestAbortStaleSessionsAfterStart_TimesOutIfNeverHealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"healthy": false})
	}))
	defer server.Close()

	client := &OpenCodeClient{password: "pw", client: &http.Client{Timeout: 500 * time.Millisecond}}
	orig := getAgentAddr()
	defer func() { setAgentAddr(orig) }()
	setAgentAddr(server.URL)

	withTestLogger(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	abortStaleSessionsAfterStart(ctx, client, log)
	// Must return within the context deadline, not hang.
	assert.Less(t, time.Since(start), 4*time.Second)
}
