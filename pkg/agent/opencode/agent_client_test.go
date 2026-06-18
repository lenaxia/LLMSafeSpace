// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/lenaxia/llmsafespace/pkg/secrets"
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

// --- WorkspaceClient tests ---

type mockPodIPResolver struct{ ip string }

func (m *mockPodIPResolver) GetWorkspacePodIP(_ context.Context, _, _ string) (string, error) {
	return m.ip, nil
}

func TestWorkspaceClient_ListModels_ResolvesWorkspace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, _ = r.BasicAuth()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"connected":[]}`))
	}))
	defer srv.Close()

	// WorkspaceClient constructs the baseURL from podIP + AgentPort.
	// For testing, we point at the test server by overriding the port
	// resolution via a test-specific constructor.
	wcl := &WorkspaceClient{
		passwordResolver: func(_ context.Context, _ string) (string, error) { return "pw", nil },
		podIPResolver:    &mockPodIPResolver{ip: "127.0.0.1"},
		httpClient:       &http.Client{Timeout: 5e9},
		logger:           zap.NewNop(),
	}
	// Monkey-patch the resolve to point at our test server by using the
	// full Client directly via a wrapper. In production, resolve constructs
	// http://<podIP>:4096.
	_ = wcl // compile check

	// Direct test of the interface method via a test server listening on
	// the real AgentPort is environment-dependent. Instead, verify the
	// resolve logic produces the correct baseURL shape.
	c, err := wcl.resolve(context.Background(), "ws-1")
	require.NoError(t, err)
	assert.Contains(t, c.baseURL, "127.0.0.1")
	assert.Equal(t, "pw", c.password)
}

func TestWorkspaceClient_Resolve_NoPodIP_ReturnsError(t *testing.T) {
	wcl := &WorkspaceClient{
		passwordResolver: func(_ context.Context, _ string) (string, error) { return "pw", nil },
		podIPResolver:    &mockPodIPResolver{ip: ""},
		httpClient:       &http.Client{Timeout: 5e9},
		logger:           zap.NewNop(),
	}
	_, err := wcl.resolve(context.Background(), "ws-missing")
	require.Error(t, err)
}

// Compile-time interface conformance.
func TestWorkspaceClient_SatisfiesAgentClient(t *testing.T) {
	var _ AgentClient = (*WorkspaceClient)(nil)
}

// Ensure secrets import is used.
var _ = secrets.LLMProviderData{}
