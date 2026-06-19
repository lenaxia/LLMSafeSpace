// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- Client.ListModels / PatchConfig tests ---

func TestClient_ListModels_HappyPath(t *testing.T) {
	var gotAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		gotAuth = ok && user == "opencode" && pass == "test-pw"
		assert.Equal(t, "/provider", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"connected":["anthropic"]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-pw", zap.NewNop())
	body, err := c.ListModels(context.Background())
	require.NoError(t, err)
	assert.Contains(t, string(body), "anthropic")
	assert.True(t, gotAuth, "must send Basic auth")
}

func TestClient_ListModels_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-pw", zap.NewNop())
	_, err := c.ListModels(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestClient_PatchConfig_HappyPath(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/global/config", r.URL.Path)
		assert.Equal(t, http.MethodPatch, r.Method)
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-pw", zap.NewNop())
	err := c.PatchConfig(context.Background(), map[string]any{"model": "claude-sonnet-4-5"})
	require.NoError(t, err)
	assert.Equal(t, "claude-sonnet-4-5", gotBody["model"])
}

func TestClient_PatchConfig_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-pw", zap.NewNop())
	err := c.PatchConfig(context.Background(), map[string]any{"model": "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

// --- WorkspaceClient resolve tests ---

type mockPodIPResolver struct {
	ip      string
	errFunc func(userID, workspaceID string) error
}

func (m *mockPodIPResolver) GetWorkspacePodIP(_ context.Context, userID, workspaceID string) (string, error) {
	if m.errFunc != nil {
		if err := m.errFunc(userID, workspaceID); err != nil {
			return "", err
		}
	}
	return m.ip, nil
}

func TestWorkspaceClient_Resolve_EmptyPodIP_ReturnsCleanError(t *testing.T) {
	wcl := NewWorkspaceClient(
		func(_ context.Context, _ string) (string, error) { return "pw", nil },
		&mockPodIPResolver{ip: ""},
		zap.NewNop(),
	)
	_, err := wcl.resolve(context.Background(), "user-1", "ws-missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoRunningPod)
	assert.NotContains(t, err.Error(), "%!w", "error must not contain fmt verb artifacts")
}

func TestWorkspaceClient_Resolve_PodIPErr_ReturnsWrappedError(t *testing.T) {
	wcl := NewWorkspaceClient(
		func(_ context.Context, _ string) (string, error) { return "pw", nil },
		&mockPodIPResolver{ip: "", errFunc: func(_, _ string) error {
			return context.DeadlineExceeded
		}},
		zap.NewNop(),
	)
	_, err := wcl.resolve(context.Background(), "user-1", "ws-slow")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve pod IP")
}

func TestWorkspaceClient_Resolve_PasswordErr_ReturnsWrappedError(t *testing.T) {
	wcl := NewWorkspaceClient(
		func(_ context.Context, _ string) (string, error) { return "", context.Canceled },
		&mockPodIPResolver{ip: "10.0.0.1"},
		zap.NewNop(),
	)
	_, err := wcl.resolve(context.Background(), "user-1", "ws-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve password")
}

func TestWorkspaceClient_PassUserID_ToResolver(t *testing.T) {
	var capturedUserID string
	wcl := NewWorkspaceClient(
		func(_ context.Context, _ string) (string, error) { return "pw", nil },
		&mockPodIPResolver{
			ip: "10.0.0.1",
			errFunc: func(userID, _ string) error {
				capturedUserID = userID
				return nil
			},
		},
		zap.NewNop(),
	)
	_, _ = wcl.resolve(context.Background(), "user-99", "ws-1")
	assert.Equal(t, "user-99", capturedUserID, "userID must be passed through to PodIPResolver")
}

// --- WorkspaceClient end-to-end test ---

// TestWorkspaceClient_ListModels_EndToEnd verifies the full delegation
// path: WorkspaceClient.ListModels → resolve → Client.ListModels → HTTP.
// Uses a real listener on a dynamic port to avoid hardcoding 4096.
func TestWorkspaceClient_ListModels_EndToEnd(t *testing.T) {
	var gotAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		gotAuth = ok && user == "opencode" && pass == "e2e-pw"
		assert.Equal(t, "/provider", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"connected":["anthropic"]}`))
	}))
	defer srv.Close()

	// Extract the port from the test server URL so WorkspaceClient
	// resolves to it.
	_, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	testPort, _ := strconv.Atoi(portStr)

	wcl := NewWorkspaceClient(
		func(_ context.Context, _ string) (string, error) { return "e2e-pw", nil },
		&mockPodIPResolver{ip: "127.0.0.1"},
		zap.NewNop(),
	)
	wcl.agentPort = testPort

	body, err := wcl.ListModels(context.Background(), "user-1", "ws-e2e")
	require.NoError(t, err)
	assert.Contains(t, string(body), "anthropic")
	assert.True(t, gotAuth, "end-to-end call must send Basic auth")
}

// Compile-time interface conformance.
func TestWorkspaceClient_SatisfiesAgentClient(t *testing.T) {
	var _ AgentClient = (*WorkspaceClient)(nil)
}
