// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	opencode "github.com/lenaxia/llmsafespace/pkg/agent/opencode"
	"github.com/lenaxia/llmsafespace/pkg/agentd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockOpencode(t *testing.T, statuses map[string]string) (*opencode.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pw, ok := r.BasicAuth()
		if !ok || user != agentd.AuthUsername || pw != "test-pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/session/status" {
			resp := make(map[string]map[string]string)
			for id, typ := range statuses {
				resp[id] = map[string]string{"type": typ}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	client := opencode.NewClient(srv.URL, "test-pw", zaptest.NewLogger(t))
	return client, srv
}

func TestWaitUntilIdle_AlreadyIdle_ReturnsImmediately(t *testing.T) {
	tracker := NewSSETracker(nil, nil, nil)
	client, srv := newMockOpencode(t, map[string]string{
		"sess-1": "idle",
		"sess-2": "idle",
	})
	defer srv.Close()

	err := WaitUntilIdle(context.Background(), "ws-1", tracker, client, 5*time.Second)
	assert.NoError(t, err)
}

func TestWaitUntilIdle_EmptySessions_ReturnsImmediately(t *testing.T) {
	tracker := NewSSETracker(nil, nil, nil)
	client, srv := newMockOpencode(t, map[string]string{})
	defer srv.Close()

	err := WaitUntilIdle(context.Background(), "ws-1", tracker, client, 5*time.Second)
	assert.NoError(t, err)
}

func TestWaitUntilIdle_BusyThenIdle_ReturnsAfterEvent(t *testing.T) {
	tracker := NewSSETracker(nil, nil, nil)
	client, srv := newMockOpencode(t, map[string]string{
		"sess-1": "busy",
	})
	defer srv.Close()

	// Fire idle event after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		// Simulate SSE dispatch
		tracker.dispatchProperties("ws-1", "session.status", json.RawMessage(
			`{"sessionID":"sess-1","status":{"type":"idle"}}`,
		))
	}()

	err := WaitUntilIdle(context.Background(), "ws-1", tracker, client, 5*time.Second)
	assert.NoError(t, err)
}

func TestWaitUntilIdle_NeverIdle_TimeoutReturnsDrainError(t *testing.T) {
	tracker := NewSSETracker(nil, nil, nil)
	client, srv := newMockOpencode(t, map[string]string{
		"sess-1": "busy",
		"sess-2": "busy",
	})
	defer srv.Close()

	err := WaitUntilIdle(context.Background(), "ws-1", tracker, client, 100*time.Millisecond)
	require.Error(t, err)

	var drainErr *ErrDrainTimeout
	require.True(t, errors.As(err, &drainErr))
	assert.Len(t, drainErr.BusySessions, 2)
	assert.Contains(t, drainErr.BusySessions, "sess-1")
	assert.Contains(t, drainErr.BusySessions, "sess-2")
}

func TestWaitUntilIdle_ContextCancelled_ReturnsErr(t *testing.T) {
	tracker := NewSSETracker(nil, nil, nil)
	client, srv := newMockOpencode(t, map[string]string{
		"sess-1": "busy",
	})
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := WaitUntilIdle(ctx, "ws-1", tracker, client, 5*time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestWaitUntilIdle_NewBusyDuringWait_HoldsTillIdle(t *testing.T) {
	tracker := NewSSETracker(nil, nil, nil)
	// Start with one busy
	client, srv := newMockOpencode(t, map[string]string{
		"sess-1": "busy",
	})
	defer srv.Close()

	go func() {
		time.Sleep(30 * time.Millisecond)
		// sess-1 goes idle but sess-2 becomes busy
		tracker.dispatchProperties("ws-1", "session.status", json.RawMessage(
			`{"sessionID":"sess-1","status":{"type":"idle"}}`,
		))
		tracker.dispatchProperties("ws-1", "session.status", json.RawMessage(
			`{"sessionID":"sess-2","status":{"type":"busy"}}`,
		))
		time.Sleep(30 * time.Millisecond)
		// Now sess-2 goes idle too
		tracker.dispatchProperties("ws-1", "session.status", json.RawMessage(
			`{"sessionID":"sess-2","status":{"type":"idle"}}`,
		))
	}()

	err := WaitUntilIdle(context.Background(), "ws-1", tracker, client, 5*time.Second)
	assert.NoError(t, err)
}

func TestWaitUntilIdle_SnapshotFails_ReturnsErr(t *testing.T) {
	tracker := NewSSETracker(nil, nil, nil)
	// Client pointing at closed server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	client := opencode.NewClient(srv.URL, "test-pw", zaptest.NewLogger(t))

	err := WaitUntilIdle(context.Background(), "ws-1", tracker, client, 5*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "snapshot")
}

func TestWaitUntilIdle_RetryStatusTreatedAsBusy(t *testing.T) {
	tracker := NewSSETracker(nil, nil, nil)
	client, srv := newMockOpencode(t, map[string]string{
		"sess-1": "retry",
	})
	defer srv.Close()

	// Will timeout because "retry" is treated as busy
	err := WaitUntilIdle(context.Background(), "ws-1", tracker, client, 100*time.Millisecond)
	require.Error(t, err)

	var drainErr *ErrDrainTimeout
	require.True(t, errors.As(err, &drainErr))
	assert.Equal(t, []string{"sess-1"}, drainErr.BusySessions)
}
