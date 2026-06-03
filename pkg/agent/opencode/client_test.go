// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lenaxia/llmsafespace/pkg/agentd"
	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/stretchr/testify/require"
)

// testPassword is the Basic-auth password every mock server in this file
// expects. It mirrors how real opencode behaves: every request to /auth/*
// and /instance/* (and indeed every endpoint) is gated by HTTP Basic auth
// with username `agentd.AuthUsername` ("opencode") and the password from
// `OPENCODE_SERVER_PASSWORD` (= /sandbox-cfg/password in production).
//
// Tests that build mock servers MUST wrap their handler in `requireAuth`.
// Without that wrapping, a regression where the client forgets to call
// SetBasicAuth (Bug 1, worklog 0125) silently passes — exactly what
// happened in worklog 0121.
const testPassword = "test-opencode-password"

// requireAuth wraps an http.Handler so it returns 401 + WWW-Authenticate
// if the request lacks Basic auth matching (agentd.AuthUsername, testPassword).
// This is what opencode itself does — see the cluster verification in
// worklog 0125: probing PUT /auth/openai without auth returns:
//
//	HTTP/1.1 401 Unauthorized
//	www-authenticate: Basic realm="Secure Area"
//
// Returning the same response shape from the test fixture forces the
// client to set Basic auth on every request, which is the production
// invariant.
func requireAuth(t *testing.T, h http.Handler) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pw, ok := r.BasicAuth()
		if !ok || user != agentd.AuthUsername || pw != testPassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="Secure Area"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// newClientForTest constructs a Client wired with the test Basic-auth
// password. Centralized so a future change to the constructor surface
// (e.g. adding a TLS option) only requires updating one place.
func newClientForTest(baseURL string) *Client {
	return NewClient(baseURL, testPassword)
}

// --- PushCredentials tests ---

func TestPushCredentials_SingleProvider(t *testing.T) {
	var received []authSetRequest
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/auth/") {
			body, _ := io.ReadAll(r.Body)
			var req authSetRequest
			require.NoError(t, json.Unmarshal(body, &req))
			req.providerID = strings.TrimPrefix(r.URL.Path, "/auth/")
			received = append(received, req)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
			return
		}
		http.NotFound(w, r)
	})))
	defer srv.Close()

	c := newClientForTest(srv.URL)
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant-123"},
	}

	err := c.PushCredentials(context.Background(), providers)
	require.NoError(t, err)
	require.Len(t, received, 1)
	require.Equal(t, "anthropic", received[0].providerID)
	require.Equal(t, "api", received[0].Type)
	require.Equal(t, "sk-ant-123", received[0].Key)
}

func TestPushCredentials_MultipleProviders(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/auth/") {
			callCount.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
			return
		}
		http.NotFound(w, r)
	})))
	defer srv.Close()

	c := newClientForTest(srv.URL)
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
		{Provider: "openai", APIKey: "sk-oai"},
		{Provider: "google", APIKey: "sk-goog"},
	}

	err := c.PushCredentials(context.Background(), providers)
	require.NoError(t, err)
	require.Equal(t, int32(3), callCount.Load())
}

func TestPushCredentials_WithMetadata_BaseURL(t *testing.T) {
	var received authSetRequest
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &received)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
			return
		}
		http.NotFound(w, r)
	})))
	defer srv.Close()

	c := newClientForTest(srv.URL)
	providers := []secrets.LLMProviderData{
		{Provider: "openai", APIKey: "sk-oai", BaseURL: "https://custom.endpoint/v1"},
	}

	err := c.PushCredentials(context.Background(), providers)
	require.NoError(t, err)
	require.Equal(t, "sk-oai", received.Key)
	require.NotNil(t, received.Metadata)
	require.Equal(t, "https://custom.endpoint/v1", received.Metadata["baseURL"])
}

func TestPushCredentials_WithoutBaseURL_NoMetadata(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
			return
		}
		http.NotFound(w, r)
	})))
	defer srv.Close()

	c := newClientForTest(srv.URL)
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
	}

	err := c.PushCredentials(context.Background(), providers)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &parsed))
	// metadata should be absent or nil when no baseURL
	_, hasMetadata := parsed["metadata"]
	require.False(t, hasMetadata, "metadata should be omitted when baseURL is empty")
}

func TestPushCredentials_EmptyProviders_NoOp(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	})))
	defer srv.Close()

	c := newClientForTest(srv.URL)

	err := c.PushCredentials(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, int32(0), callCount.Load())

	err = c.PushCredentials(context.Background(), []secrets.LLMProviderData{})
	require.NoError(t, err)
	require.Equal(t, int32(0), callCount.Load())
}

func TestPushCredentials_ServerError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"disk full"}`))
	})))
	defer srv.Close()

	c := newClientForTest(srv.URL)
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
	}

	err := c.PushCredentials(context.Background(), providers)
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
}

func TestPushCredentials_ConnectionRefused_ReturnsError(t *testing.T) {
	c := newClientForTest("http://127.0.0.1:1") // guaranteed to refuse
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
	}

	err := c.PushCredentials(context.Background(), providers)
	require.Error(t, err)
}

func TestPushCredentials_PartialFailure_ReturnsFirstError(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid provider"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	})))
	defer srv.Close()

	c := newClientForTest(srv.URL)
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
		{Provider: "invalid", APIKey: "sk-bad"},
		{Provider: "openai", APIKey: "sk-oai"},
	}

	err := c.PushCredentials(context.Background(), providers)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid")
}

// TestPushCredentials_UnauthenticatedClient_Returns401 is the regression
// guard for Bug 1 (worklog 0125): if a future change drops the
// SetBasicAuth call, real opencode will reject the request with 401 and
// the live credential flow breaks. We construct a Client with a wrong
// password and verify that requireAuth (which mirrors opencode's gate)
// produces the same 401 the operator saw on the cluster.
func TestPushCredentials_UnauthenticatedClient_Returns401(t *testing.T) {
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	})))
	defer srv.Close()

	// Wrong password — server should reject with 401.
	c := NewClient(srv.URL, "wrong-password")
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
	}

	err := c.PushCredentials(context.Background(), providers)
	require.Error(t, err)
	require.Contains(t, err.Error(), "401",
		"client with wrong password must surface opencode's 401 (Bug 1 regression guard)")
}

// --- DisposeInstance tests ---

func TestDisposeInstance_Success(t *testing.T) {
	var called bool
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/instance/dispose" {
			called = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
			return
		}
		http.NotFound(w, r)
	})))
	defer srv.Close()

	c := newClientForTest(srv.URL)
	err := c.DisposeInstance(context.Background())
	require.NoError(t, err)
	require.True(t, called)
}

func TestDisposeInstance_ServerError(t *testing.T) {
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})))
	defer srv.Close()

	c := newClientForTest(srv.URL)
	err := c.DisposeInstance(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
}

func TestDisposeInstance_ConnectionRefused(t *testing.T) {
	c := newClientForTest("http://127.0.0.1:1")
	err := c.DisposeInstance(context.Background())
	require.Error(t, err)
}

// TestDisposeInstance_UnauthenticatedClient_Returns401 — same regression
// guard as TestPushCredentials_UnauthenticatedClient_Returns401, for the
// dispose path.
func TestDisposeInstance_UnauthenticatedClient_Returns401(t *testing.T) {
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	defer srv.Close()

	c := NewClient(srv.URL, "wrong-password")
	err := c.DisposeInstance(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}

// --- StageCredentials tests ---

func TestStageCredentials_DoesNotCallDispose(t *testing.T) {
	var disposeCalled bool
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/auth/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
			return
		}
		if r.URL.Path == "/instance/dispose" {
			disposeCalled = true
			t.Fatal("DisposeInstance must NOT be called by StageCredentials")
			return
		}
		http.NotFound(w, r)
	})))
	defer srv.Close()

	c := newClientForTest(srv.URL)
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
	}

	err := c.StageCredentials(context.Background(), providers)
	require.NoError(t, err)
	require.False(t, disposeCalled, "StageCredentials must not call dispose")
}

func TestStageCredentials_EmptyProviders_NoOp(t *testing.T) {
	var called bool
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})))
	defer srv.Close()

	c := newClientForTest(srv.URL)
	err := c.StageCredentials(context.Background(), nil)
	require.NoError(t, err)
	require.False(t, called, "no calls should be made for empty providers")
}

func TestStageCredentials_PushFailure_NoSideEffects(t *testing.T) {
	var disposeCalled bool
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/instance/dispose" {
			disposeCalled = true
			w.WriteHeader(http.StatusOK)
			return
		}
	})))
	defer srv.Close()

	c := newClientForTest(srv.URL)
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
	}

	err := c.StageCredentials(context.Background(), providers)
	require.Error(t, err)
	require.False(t, disposeCalled, "dispose must NOT be called if push failed")
}

func TestStageCredentials_BasicAuth_Required(t *testing.T) {
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	})))
	defer srv.Close()

	c := NewClient(srv.URL, "wrong-password")
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
	}

	err := c.StageCredentials(context.Background(), providers)
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}

// --- authSetRequest helper for test assertions ---

type authSetRequest struct {
	Type     string            `json:"type"`
	Key      string            `json:"key"`
	Metadata map[string]string `json:"metadata,omitempty"`
	// not serialized — set by test handler from URL path
	providerID string
}
