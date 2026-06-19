// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package sse

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAgentDiedTracker(t *testing.T, server *httptest.Server, onDied AgentDiedCallback) *Tracker {
	t.Helper()
	tracker := NewTracker(
		&http.Client{Transport: &redirectTransport{server: server}},
		&testLogger{},
		func(workspaceID, sessionID string) {},
	)
	tracker.SetPasswordGetter(fakePWProvider{pw: "test-pw"})
	tracker.SetPodIPResolver(func(workspaceID string) string { return "10.0.0.1" })
	tracker.SetOnAgentDied(onDied)
	return tracker
}

// newErrorAfterDataServer returns an httptest SSE server that writes one valid
// SSE data event, flushes it, then hijacks the connection and forcibly closes
// the raw socket with a prior half-write to induce a TCP RST — producing a
// non-EOF read error on the client side (distinct from a clean EOF / handler
// return). This simulates a mid-stream network failure after data was received.
func newErrorAfterDataServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", makeSessionStatusEvent("sess-1", "busy"))
		flusher.Flush()

		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Log("server does not support hijack; falling back to plain close")
			return
		}
		conn, bufrw, hijErr := hj.Hijack()
		if hijErr != nil {
			t.Logf("hijack failed: %v", hijErr)
			return
		}
		// Inject garbage framing to provoke a read error on the client.
		// Then SetLinger(0) + Close forces a RST rather than a FIN, so the
		// client sees a connection reset, not a clean EOF.
		_, _ = bufrw.WriteString("GARBAGE-NOT-SSE")
		_ = bufrw.Flush()
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.SetLinger(0)
		}
		_ = conn.Close()
	}))
	t.Cleanup(server.Close)
	return server
}

func TestSSETracker_AgentDied_FiresOnEOFAfterData(t *testing.T) {
	var got atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", makeSessionStatusEvent("sess-1", "busy"))
		flusher.Flush()
	}))
	t.Cleanup(server.Close)

	tracker := newAgentDiedTracker(t, server, func(workspaceID string) {
		if workspaceID == "ws-died" {
			got.Add(1)
		}
	})
	tracker.EnsureWatching("ws-died")
	t.Cleanup(tracker.Stop)

	require.Eventually(t, func() bool { return got.Load() == 1 },
		3*time.Second, 25*time.Millisecond, "onAgentDied must fire once after data+EOF")
}

func TestSSETracker_AgentDied_DoesNotFireOnEOFWithZeroData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	tracker := newAgentDiedTracker(t, server, func(workspaceID string) {
		t.Fatalf("onAgentDied must not fire when stream ends with zero bytes; got %s", workspaceID)
	})
	tracker.EnsureWatching("ws-empty")
	t.Cleanup(tracker.Stop)

	time.Sleep(500 * time.Millisecond)
}

func TestSSETracker_AgentDied_DoesNotFireOnNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	tracker := newAgentDiedTracker(t, server, func(workspaceID string) {
		t.Fatalf("onAgentDied must not fire when upstream returns non-200; got %s", workspaceID)
	})
	tracker.EnsureWatching("ws-503")
	t.Cleanup(tracker.Stop)

	time.Sleep(500 * time.Millisecond)
}

func TestSSETracker_AgentDied_FiresExactlyOncePerStreamEnd(t *testing.T) {
	var got atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", makeSessionStatusEvent("sess-1", "busy"))
		flusher.Flush()
	}))
	t.Cleanup(server.Close)

	tracker := newAgentDiedTracker(t, server, func(workspaceID string) { got.Add(1) })
	tracker.EnsureWatching("ws-once")
	t.Cleanup(tracker.Stop)

	require.Eventually(t, func() bool { return got.Load() >= 1 },
		3*time.Second, 25*time.Millisecond, "onAgentDied must fire at least once after first stream end")

	first := got.Load()
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, first, got.Load(),
		"onAgentDied must fire exactly once per stream end (no duplicate within one connectAndRead); reconnect-driven fires are delayed by the 2s backoff (>300ms sleep window); Stop runs in t.Cleanup")
}

func TestSSETracker_AgentDied_NilCallbackDoesNotPanic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", makeSessionStatusEvent("sess-1", "busy"))
		flusher.Flush()
	}))
	t.Cleanup(server.Close)

	tracker := NewTracker(
		&http.Client{Transport: &redirectTransport{server: server}},
		&testLogger{},
		func(workspaceID, sessionID string) {},
	)
	tracker.SetPasswordGetter(fakePWProvider{pw: "test-pw"})
	tracker.SetPodIPResolver(func(workspaceID string) string { return "10.0.0.1" })

	assert.NotPanics(t, func() {
		tracker.EnsureWatching("ws-nocb")
		time.Sleep(400 * time.Millisecond)
		tracker.Stop()
	})
}

// TestSSETracker_AgentDied_DoesNotFireOnIdleTimeout exercises PATH A of the
// return-decision block (the highest-frequency false-positive vector). With a
// short injectable idle timeout, the upstream sends data (so bytesReceived > 0)
// then stalls; the idle timer must fire and the callback must NOT — this is the
// spam-prevention guarantee for a 5min-silent live workspace.
func TestSSETracker_AgentDied_DoesNotFireOnIdleTimeout(t *testing.T) {
	firedCh := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", makeSessionStatusEvent("sess-1", "busy"))
		flusher.Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	tracker := newAgentDiedTracker(t, server, func(workspaceID string) {
		select {
		case firedCh <- workspaceID:
		default:
		}
	})
	tracker.SetIdleTimeout(200 * time.Millisecond)
	tracker.EnsureWatching("ws-idle")
	t.Cleanup(tracker.Stop)

	select {
	case ws := <-firedCh:
		t.Fatalf("onAgentDied must not fire on idle timeout; fired for %s", ws)
	case <-time.After(800 * time.Millisecond):
	}
}

// TestSSETracker_AgentDied_DoesNotFireOnNonEOFScannerError exercises the
// scanner.Err() guard: a non-EOF read error (simulated via an error-returning
// reader transport) after data was received must NOT fire onAgentDied, aligning
// with US-44.1a's network-failure vs process-death distinction.
func TestSSETracker_AgentDied_DoesNotFireOnNonEOFScannerError(t *testing.T) {
	firedCh := make(chan string, 1)
	server := newErrorAfterDataServer(t)
	tracker := newAgentDiedTracker(t, server, func(workspaceID string) {
		select {
		case firedCh <- workspaceID:
		default:
		}
	})
	tracker.SetIdleTimeout(time.Minute)
	tracker.EnsureWatching("ws-rst")
	t.Cleanup(tracker.Stop)

	select {
	case ws := <-firedCh:
		t.Fatalf("onAgentDied must not fire on non-EOF scanner error; fired for %s", ws)
	case <-time.After(500 * time.Millisecond):
	}
}
