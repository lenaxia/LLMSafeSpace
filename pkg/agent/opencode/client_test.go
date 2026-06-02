// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lenaxia/llmsafespace/pkg/secrets"
	"github.com/stretchr/testify/require"
)

// --- PushCredentials tests ---

func TestPushCredentials_SingleProvider(t *testing.T) {
	var received []authSetRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path[:6] == "/auth/" {
			body, _ := io.ReadAll(r.Body)
			var req authSetRequest
			require.NoError(t, json.Unmarshal(body, &req))
			req.providerID = r.URL.Path[6:]
			received = append(received, req)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && len(r.URL.Path) > 6 && r.URL.Path[:6] == "/auth/" {
			callCount.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &received)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)

	err := c.PushCredentials(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, int32(0), callCount.Load())

	err = c.PushCredentials(context.Background(), []secrets.LLMProviderData{})
	require.NoError(t, err)
	require.Equal(t, int32(0), callCount.Load())
}

func TestPushCredentials_ServerError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"disk full"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
	}

	err := c.PushCredentials(context.Background(), providers)
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
}

func TestPushCredentials_ConnectionRefused_ReturnsError(t *testing.T) {
	c := NewClient("http://127.0.0.1:1") // guaranteed to refuse
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
	}

	err := c.PushCredentials(context.Background(), providers)
	require.Error(t, err)
}

func TestPushCredentials_PartialFailure_ReturnsFirstError(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid provider"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
		{Provider: "invalid", APIKey: "sk-bad"},
		{Provider: "openai", APIKey: "sk-oai"},
	}

	err := c.PushCredentials(context.Background(), providers)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid")
}

// --- DisposeInstance tests ---

func TestDisposeInstance_Success(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/instance/dispose" {
			called = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	err := c.DisposeInstance(context.Background())
	require.NoError(t, err)
	require.True(t, called)
}

func TestDisposeInstance_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	err := c.DisposeInstance(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
}

func TestDisposeInstance_ConnectionRefused(t *testing.T) {
	c := NewClient("http://127.0.0.1:1")
	err := c.DisposeInstance(context.Background())
	require.Error(t, err)
}

// --- RefreshCredentials (combined operation) tests ---

func TestRefreshCredentials_PushThenDispose(t *testing.T) {
	var order []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && len(r.URL.Path) > 6 && r.URL.Path[:6] == "/auth/" {
			order = append(order, "auth:"+r.URL.Path[6:])
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/instance/dispose" {
			order = append(order, "dispose")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("true"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
	}

	err := c.RefreshCredentials(context.Background(), providers)
	require.NoError(t, err)
	require.Equal(t, []string{"auth:anthropic", "dispose"}, order)
}

func TestRefreshCredentials_EmptyProviders_NoDispose(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	err := c.RefreshCredentials(context.Background(), nil)
	require.NoError(t, err)
	require.False(t, called, "no calls should be made for empty providers")
}

func TestRefreshCredentials_PushFails_NoDispose(t *testing.T) {
	var disposeCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/instance/dispose" {
			disposeCalled = true
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	providers := []secrets.LLMProviderData{
		{Provider: "anthropic", APIKey: "sk-ant"},
	}

	err := c.RefreshCredentials(context.Background(), providers)
	require.Error(t, err)
	require.False(t, disposeCalled, "dispose must NOT be called if push failed")
}

// --- authSetRequest helper for test assertions ---

type authSetRequest struct {
	Type     string            `json:"type"`
	Key      string            `json:"key"`
	Metadata map[string]string `json:"metadata,omitempty"`
	// not serialized — set by test handler from URL path
	providerID string
}
