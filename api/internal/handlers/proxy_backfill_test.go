// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1 "github.com/lenaxia/llmsafespace/pkg/apis/llmsafespace/v1"
	"github.com/lenaxia/llmsafespace/pkg/types"
)

// recordingSessionIndex captures UpsertParent calls so backfill behavior
// can be asserted without spinning up a real PostgreSQL.
type recordingSessionIndex struct {
	mu      sync.Mutex
	parents map[string]string // sessionID → parentID
	titles  map[string]string
}

func newRecordingSessionIndex() *recordingSessionIndex {
	return &recordingSessionIndex{
		parents: make(map[string]string),
		titles:  make(map[string]string),
	}
}

func (r *recordingSessionIndex) RecordMessage(_, _, _ string, _ time.Time) {}
func (r *recordingSessionIndex) ListByWorkspace(_ context.Context, _ string) ([]types.SessionListItem, error) {
	return nil, nil
}
func (r *recordingSessionIndex) DeleteByWorkspace(_ context.Context, _ string) error { return nil }
func (r *recordingSessionIndex) UpsertTitle(_ context.Context, _, sessionID, title string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.titles[sessionID] = title
	return nil
}
func (r *recordingSessionIndex) UpsertParent(_ context.Context, _, sessionID, parentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.parents[sessionID] = parentID
	return nil
}
func (r *recordingSessionIndex) UpdateLastSeen(_ context.Context, _, _ string) error { return nil }
func (r *recordingSessionIndex) Start() error { return nil }
func (r *recordingSessionIndex) Stop() error  { return nil }

func (r *recordingSessionIndex) parentOf(sessionID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.parents[sessionID]
}

func (r *recordingSessionIndex) parentCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.parents)
}

// TestBackfillSessionParents_HappyPath verifies that BackfillSessionParents
// fetches /session from the workspace pod and writes parent_session_id for
// every session that has one. This is the data path for the sidebar
// hierarchy when the user opens an existing workspace whose sessions
// pre-date the parent_session_id migration.
func TestBackfillSessionParents_HappyPath(t *testing.T) {
	var requestCount atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		assert.Equal(t, "/session", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{"id": "ses_root"},
			{"id": "ses_child", "parentID": "ses_root"},
			{"id": "ses_grandchild", "parentID": "ses_child"},
		})
	}))
	defer backend.Close()

	env := newInputTestEnv(t)
	env.handler.httpClient = &http.Client{
		Transport: &redirectTransport{server: backend},
		Timeout:   5 * time.Second,
	}
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")

	si := newRecordingSessionIndex()
	env.handler.sessionIndex = si

	env.handler.BackfillSessionParents("ws-1")

	require.Eventually(t, func() bool {
		return si.parentCount() == 2
	}, 2*time.Second, 10*time.Millisecond, "expected parent records for child + grandchild")

	assert.Equal(t, "ses_root", si.parentOf("ses_child"))
	assert.Equal(t, "ses_child", si.parentOf("ses_grandchild"))
	assert.Equal(t, "", si.parentOf("ses_root"), "top-level session must NOT be written")
}

// TestBackfillSessionParents_IsIdempotent verifies the once-per-workspace
// gate: subsequent calls within the same process lifetime are no-ops, so
// the steady-state cost of opening the sidebar is a single map lookup —
// not an HTTP round-trip per request.
func TestBackfillSessionParents_IsIdempotent(t *testing.T) {
	var requestCount atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{"id": "ses_root"},
		})
	}))
	defer backend.Close()

	env := newInputTestEnv(t)
	env.handler.httpClient = &http.Client{
		Transport: &redirectTransport{server: backend},
		Timeout:   5 * time.Second,
	}
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.handler.sessionIndex = newRecordingSessionIndex()

	for i := 0; i < 5; i++ {
		env.handler.BackfillSessionParents("ws-1")
	}

	// Allow goroutines to complete; only ONE backend request should fire
	// despite 5 calls.
	require.Eventually(t, func() bool { return requestCount.Load() >= 1 }, time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond) // window for any spurious extra calls

	assert.Equal(t, int32(1), requestCount.Load(), "backfill must only hit the pod once per workspace")
}

// TestBackfillSessionParents_RetriesAfterFailure verifies that a failed
// backfill (e.g. pod unreachable) does NOT permanently mark the workspace
// as backfilled — the next call must retry. Otherwise a transient network
// error at startup would silently disable hierarchy for the workspace.
func TestBackfillSessionParents_RetriesAfterFailure(t *testing.T) {
	var requestCount atomic.Int32
	failFirst := atomic.Bool{}
	failFirst.Store(true)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if failFirst.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{"id": "ses_recovered", "parentID": "ses_root"},
		})
	}))
	defer backend.Close()

	env := newInputTestEnv(t)
	env.handler.httpClient = &http.Client{
		Transport: &redirectTransport{server: backend},
		Timeout:   5 * time.Second,
	}
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")

	si := newRecordingSessionIndex()
	env.handler.sessionIndex = si

	// First call hits the failing backend.
	env.handler.BackfillSessionParents("ws-1")
	require.Eventually(t, func() bool { return requestCount.Load() >= 1 }, time.Second, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, si.parentCount(), "no parents written on first (failing) attempt")

	// Backend recovers; second call retries since we cleared the gate.
	failFirst.Store(false)
	env.handler.BackfillSessionParents("ws-1")
	require.Eventually(t, func() bool {
		return si.parentCount() == 1
	}, 2*time.Second, 10*time.Millisecond, "retry should populate parents after backend recovers")
	assert.Equal(t, "ses_root", si.parentOf("ses_recovered"))
}

// TestBackfillSessionParents_SkipsWhenWorkspaceNotActive verifies the
// short-circuit: a Suspended/Pending workspace has no pod to talk to, so
// we drop the gate and don't fire HTTP. The next call when the workspace
// becomes Active will succeed.
func TestBackfillSessionParents_SkipsWhenWorkspaceNotActive(t *testing.T) {
	var requestCount atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
	}))
	defer backend.Close()

	env := newInputTestEnv(t)
	env.handler.httpClient = &http.Client{
		Transport: &redirectTransport{server: backend},
		Timeout:   5 * time.Second,
	}
	// Workspace exists but is Suspended.
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", "Suspended", "ws-1")
	env.handler.sessionIndex = newRecordingSessionIndex()

	env.handler.BackfillSessionParents("ws-1")
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, int32(0), requestCount.Load(), "must not hit backend for non-Active workspace")
}

// TestBackfillSessionParents_InvalidateCachesAllowsRetry verifies that
// invalidateCaches (called on workspace suspend/restart) clears the
// backfill marker so the next call after the workspace becomes Active
// runs a fresh backfill against the new pod.
func TestBackfillSessionParents_InvalidateCachesAllowsRetry(t *testing.T) {
	var requestCount atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{"id": "ses_root"},
		})
	}))
	defer backend.Close()

	env := newInputTestEnv(t)
	env.handler.httpClient = &http.Client{
		Transport: &redirectTransport{server: backend},
		Timeout:   5 * time.Second,
	}
	env.setupWorkspacePodWithT(t, "ws-1", "10.0.0.1", string(v1.WorkspacePhaseActive), "ws-1")
	env.setupPasswordWithT(t, "ws-1", "test-password")
	env.handler.sessionIndex = newRecordingSessionIndex()

	env.handler.BackfillSessionParents("ws-1")
	require.Eventually(t, func() bool { return requestCount.Load() == 1 }, time.Second, 10*time.Millisecond)

	// Simulate suspend/restart cycle.
	env.handler.invalidateCaches("ws-1")

	env.handler.BackfillSessionParents("ws-1")
	require.Eventually(t, func() bool { return requestCount.Load() == 2 }, time.Second, 10*time.Millisecond,
		"backfill must rerun after invalidateCaches")
}
