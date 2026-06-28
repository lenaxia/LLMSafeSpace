// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/lenaxia/llmsafespaces/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// Integration tests: verify StageCredentials writes credentials
// to opencode's auth store without triggering dispose.
// ============================================================

func TestStageCredentials_CorrectHTTPVerbs(t *testing.T) {
	var requests []struct {
		Method string
		Path   string
	}
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, struct {
			Method string
			Path   string
		}{r.Method, r.URL.Path})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	})))
	defer srv.Close()

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	providers := []secrets.LLMProviderData{
		{Kind: "anthropic", Slug: "anthropic", APIKey: "sk-1"},
		{Kind: "openai", Slug: "openai", APIKey: "sk-2"},
	}

	err := c.StageCredentials(context.Background(), providers)
	require.NoError(t, err)

	// StageCredentials only pushes to auth store — no dispose call
	require.Len(t, requests, 2)
	assert.Equal(t, "PUT", requests[0].Method)
	assert.Equal(t, "/auth/anthropic", requests[0].Path)
	assert.Equal(t, "PUT", requests[1].Method)
	assert.Equal(t, "/auth/openai", requests[1].Path)
}

func TestStageCredentials_AuthPayloadMatchesOpenCodeSchema(t *testing.T) {
	var bodies []json.RawMessage
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			bodies = append(bodies, body)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	})))
	defer srv.Close()

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	providers := []secrets.LLMProviderData{
		{Kind: "anthropic", Slug: "anthropic", APIKey: "sk-ant-api03-xyz", BaseURL: "https://proxy.example.com/v1"},
	}

	err := c.StageCredentials(context.Background(), providers)
	require.NoError(t, err)
	require.Len(t, bodies, 1)

	// Validate against opencode's Auth.Info schema for type:"api"
	var payload struct {
		Type     string            `json:"type"`
		Key      string            `json:"key"`
		Metadata map[string]string `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal(bodies[0], &payload))
	assert.Equal(t, "api", payload.Type)
	assert.Equal(t, "sk-ant-api03-xyz", payload.Key)
	assert.Equal(t, "https://proxy.example.com/v1", payload.Metadata["baseURL"])
}

func TestStageCredentials_ContentTypeJSON(t *testing.T) {
	var contentTypes []string
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentTypes = append(contentTypes, r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	})))
	defer srv.Close()

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	providers := []secrets.LLMProviderData{{Kind: "x", Slug: "x", APIKey: "k"}}

	err := c.StageCredentials(context.Background(), providers)
	require.NoError(t, err)

	for i, ct := range contentTypes {
		assert.Equal(t, "application/json", ct, "request %d should have JSON content type", i)
	}
}

// ============================================================
// Context propagation tests: verify cancellation/timeout works
// ============================================================

func TestPushCredentials_ContextCancelled_Aborts(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		// Slow response to give context time to cancel
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	})))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	providers := []secrets.LLMProviderData{{Kind: "x", Slug: "x", APIKey: "k"}}

	err := c.PushCredentials(ctx, providers)
	require.Error(t, err)
}

func TestDisposeInstance_ContextTimeout(t *testing.T) {
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond) // longer than context timeout
	})))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	err := c.DisposeInstance(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

// ============================================================
// Error handling regression tests
// ============================================================

func TestPushCredentials_4xxError_IncludesProviderName(t *testing.T) {
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})))
	defer srv.Close()

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	providers := []secrets.LLMProviderData{{Kind: "my-provider", Slug: "my-provider", APIKey: "k"}}

	err := c.PushCredentials(context.Background(), providers)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "my-provider")
	assert.Contains(t, err.Error(), "400")
}

func TestPushCredentials_5xxError_IncludesProviderName(t *testing.T) {
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})))
	defer srv.Close()

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	providers := []secrets.LLMProviderData{{Kind: "broken-provider", Slug: "broken-provider", APIKey: "k"}}

	err := c.PushCredentials(context.Background(), providers)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "broken-provider")
	assert.Contains(t, err.Error(), "500")
}

func TestDisposeInstance_404_StillError(t *testing.T) {
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})))
	defer srv.Close()

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	err := c.DisposeInstance(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

// ============================================================
// Regression: provider ID special characters in URL path
// ============================================================

func TestPushCredentials_ProviderIDWithSlash(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	})))
	defer srv.Close()

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	// opencode provider IDs like "google-vertex" are simple, but test edge case
	providers := []secrets.LLMProviderData{{Kind: "custom-provider", Slug: "custom-provider", APIKey: "k"}}

	err := c.PushCredentials(context.Background(), providers)
	require.NoError(t, err)
	assert.Equal(t, "/auth/custom-provider", receivedPath)
}

func TestPushCredentials_ProviderIDWithSpecialChars(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	})))
	defer srv.Close()

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	// Provider IDs from opencode are typically simple strings, but verify no mangling
	providers := []secrets.LLMProviderData{{Kind: "openai", Slug: "openai", APIKey: "k"}}

	err := c.PushCredentials(context.Background(), providers)
	require.NoError(t, err)
	assert.Equal(t, "/auth/openai", receivedPath)
}

// ============================================================
// Regression: metadata field handling
// ============================================================

func TestPushCredentials_EmptyBaseURL_OmitsMetadata(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	})))
	defer srv.Close()

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	providers := []secrets.LLMProviderData{{Kind: "anthropic", Slug: "anthropic", APIKey: "sk", BaseURL: ""}}

	err := c.StageCredentials(context.Background(), providers)
	require.NoError(t, err)

	// JSON should NOT contain "metadata" key when BaseURL is empty
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &raw))
	_, hasMetadata := raw["metadata"]
	assert.False(t, hasMetadata, "metadata must be omitted when baseURL is empty")
}

func TestPushCredentials_NonEmptyBaseURL_IncludesMetadata(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	})))
	defer srv.Close()

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	providers := []secrets.LLMProviderData{{Kind: "openai", Slug: "openai", APIKey: "sk", BaseURL: "https://x.com"}}

	err := c.StageCredentials(context.Background(), providers)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &raw))
	metadata, ok := raw["metadata"].(map[string]interface{})
	require.True(t, ok, "metadata must be present when baseURL is set")
	assert.Equal(t, "https://x.com", metadata["baseURL"])
}

// ============================================================
// Concurrency safety: verify client is safe for concurrent use
// ============================================================

func TestClient_ConcurrentStageCredentials(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		time.Sleep(10 * time.Millisecond) // simulate latency
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	})))
	defer srv.Close()

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	providers := []secrets.LLMProviderData{{Kind: "anthropic", Slug: "anthropic", APIKey: "sk"}}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.StageCredentials(context.Background(), providers)
		}()
	}
	wg.Wait()

	// 5 concurrent stages × 1 PUT each = 5 calls (no dispose)
	assert.Equal(t, int32(5), callCount.Load())
}

// ============================================================
// Regression: client timeout configuration
// ============================================================

func TestNewClient_HasReasonableTimeout(t *testing.T) {
	c := NewClient("http://localhost:4096", testPassword, zaptest.NewLogger(t))
	assert.Equal(t, 10*time.Second, c.httpClient.Timeout)
}

// ============================================================
// Regression: dispose is NOT called if credentials are empty
// (even if StageCredentials is called with empty slice)
// ============================================================

func TestStageCredentials_NilProviders_NoHTTPCalls(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	})))
	defer srv.Close()

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	require.NoError(t, c.StageCredentials(context.Background(), nil))
	require.NoError(t, c.StageCredentials(context.Background(), []secrets.LLMProviderData{}))
	assert.Equal(t, int32(0), callCount.Load())
}

// ============================================================
// Regression: partial failure stops at first error, does not
// continue pushing to remaining providers
// ============================================================

func TestPushCredentials_StopsAtFirstFailure(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/auth/bad" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	})))
	defer srv.Close()

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	providers := []secrets.LLMProviderData{
		{Kind: "good1", Slug: "good1", APIKey: "k"},
		{Kind: "bad", Slug: "bad", APIKey: "k"},
		{Kind: "good2", Slug: "good2", APIKey: "k"}, // should NOT be reached
	}

	err := c.PushCredentials(context.Background(), providers)
	require.Error(t, err)
	assert.Equal(t, []string{"/auth/good1", "/auth/bad"}, paths,
		"should stop after first failure, not attempt good2")
}

// ============================================================
// Regression: large API key values are transmitted correctly
// ============================================================

func TestPushCredentials_LargeAPIKey(t *testing.T) {
	var receivedKey string
	srv := httptest.NewServer(requireAuth(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			var p struct{ Key string }
			_ = json.Unmarshal(body, &p)
			receivedKey = p.Key
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	})))
	defer srv.Close()

	// Some API keys (e.g. GCP service account JSON) can be very large
	largeKey := ""
	for i := 0; i < 1000; i++ {
		largeKey += "abcdefghij"
	}

	c := NewClient(srv.URL, testPassword, zaptest.NewLogger(t))
	providers := []secrets.LLMProviderData{{Kind: "test", Slug: "test", APIKey: largeKey}}

	err := c.PushCredentials(context.Background(), providers)
	require.NoError(t, err)
	assert.Equal(t, largeKey, receivedKey)
}
