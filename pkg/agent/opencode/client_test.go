// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go.uber.org/zap/zaptest"

	"github.com/lenaxia/llmsafespaces/pkg/agentd"
	"github.com/lenaxia/llmsafespaces/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPushCredentials_Success(t *testing.T) {
	var gotPayload authPayload
	var gotProvider string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pw, ok := r.BasicAuth()
		if !ok || user != agentd.AuthUsername || pw != "test-pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotProvider = r.URL.Path[len("/auth/"):]
		_ = json.NewDecoder(r.Body).Decode(&gotPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-pw", zaptest.NewLogger(t))
	err := c.PushCredentials(context.Background(), []secrets.LLMProviderData{
		{Kind: "openai", Slug: "openai", APIKey: "sk-test"},
	})
	require.NoError(t, err)
	assert.Equal(t, "openai", gotProvider)
	assert.Equal(t, "sk-test", gotPayload.Key)
	assert.Equal(t, "api", gotPayload.Type)
}

func TestPushCredentials_RetriesOn5xx(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-pw", zaptest.NewLogger(t))
	err := c.PushCredentials(context.Background(), []secrets.LLMProviderData{
		{Kind: "openai", Slug: "openai", APIKey: "sk-test"},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(3), atomic.LoadInt32(&attempts))
}

func TestPushCredentials_NoRetryOn4xx(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-pw", zaptest.NewLogger(t))
	err := c.PushCredentials(context.Background(), []secrets.LLMProviderData{
		{Kind: "openai", Slug: "openai", APIKey: "sk-test"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 400")
	assert.Equal(t, int32(1), atomic.LoadInt32(&attempts))
}

func TestPushCredentials_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := NewClient(srv.URL, "test-pw", zaptest.NewLogger(t))
	err := c.PushCredentials(ctx, []secrets.LLMProviderData{
		{Kind: "openai", Slug: "openai", APIKey: "sk-test"},
	})
	require.Error(t, err)
}

func TestPushCredentials_MaxRetriesExceeded(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-pw", zaptest.NewLogger(t))
	err := c.PushCredentials(context.Background(), []secrets.LLMProviderData{
		{Kind: "openai", Slug: "openai", APIKey: "sk-test"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server error")
	assert.Equal(t, int32(3), atomic.LoadInt32(&attempts))
}

func TestPushCredentials_PartialFailure(t *testing.T) {
	var calledProvider string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledProvider = r.URL.Path[len("/auth/"):]
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-pw", zaptest.NewLogger(t))
	err := c.PushCredentials(context.Background(), []secrets.LLMProviderData{
		{Kind: "anthropic", Slug: "anthropic", APIKey: "key-b"},
		{Kind: "openai", Slug: "openai", APIKey: "key-c"},
	})
	require.Error(t, err)
	assert.Equal(t, "anthropic", calledProvider)
	assert.Contains(t, err.Error(), "anthropic")
}
