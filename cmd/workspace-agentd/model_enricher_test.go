// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sec "github.com/lenaxia/llmsafespace/pkg/secrets"
)

// fakeModelsServer returns an httptest.Server that serves a static
// OpenAI-compatible /models response.
func fakeModelsServer(t *testing.T, models []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/models", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		type entry struct {
			ID string `json:"id"`
		}
		type resp struct {
			Data []entry `json:"data"`
		}
		entries := make([]entry, len(models))
		for i, m := range models {
			entries[i] = entry{ID: m}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp{Data: entries})
	}))
}

// --- fetchModels ---

func TestFetchModels_ParsesIDsFromData(t *testing.T) {
	srv := fakeModelsServer(t, []string{"gpt-5.4", "deepseek-v3-chat", "glm-5.1"})
	defer srv.Close()

	models, err := fetchModels(context.Background(), srv.URL, "test-key", srv.Client())
	require.NoError(t, err)
	require.Len(t, models, 3)
	assert.Equal(t, "gpt-5.4", models[0].ID)
	assert.Equal(t, "deepseek-v3-chat", models[1].ID)
	assert.Equal(t, "glm-5.1", models[2].ID)
}

func TestFetchModels_SkipsEmptyIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.4"},{"id":""},{"id":"deepseek-v3-chat"}]}`))
	}))
	defer srv.Close()

	models, err := fetchModels(context.Background(), srv.URL, "any-key", srv.Client())
	require.NoError(t, err)
	assert.Len(t, models, 2, "empty IDs must be skipped")
	assert.Equal(t, "gpt-5.4", models[0].ID)
	assert.Equal(t, "deepseek-v3-chat", models[1].ID)
}

func TestFetchModels_ReturnsErrorOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key"}`))
	}))
	defer srv.Close()

	_, err := fetchModels(context.Background(), srv.URL, "bad-key", srv.Client())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestFetchModels_TrailingSlashInBaseURL(t *testing.T) {
	srv := fakeModelsServer(t, []string{"model-a"})
	defer srv.Close()

	// BaseURL with trailing slash should still produce correct /models path.
	models, err := fetchModels(context.Background(), srv.URL+"/", "test-key", srv.Client())
	require.NoError(t, err)
	assert.Len(t, models, 1)
}

// --- fetchOrCacheModels ---

func TestFetchOrCacheModels_WritesCache(t *testing.T) {
	srv := fakeModelsServer(t, []string{"gpt-5.4", "deepseek-v3-chat"})
	defer srv.Close()

	dir := t.TempDir()
	models, err := fetchOrCacheModels(context.Background(), "openai", srv.URL, "test-key", dir, srv.Client())
	require.NoError(t, err)
	assert.Len(t, models, 2)

	// Cache file must exist.
	cacheFile := filepath.Join(dir, "provider-models-cache-openai.json")
	assert.FileExists(t, cacheFile)
}

func TestFetchOrCacheModels_ReadsCacheThatIsFresh(t *testing.T) {
	dir := t.TempDir()
	cacheFile := filepath.Join(dir, "provider-models-cache-openai.json")
	cached := []sec.LLMModelConfig{{ID: "cached-model"}}
	data, _ := json.Marshal(cached)
	require.NoError(t, os.WriteFile(cacheFile, data, 0o600))

	// Server must NOT be called — cache is fresh.
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	models, err := fetchOrCacheModels(context.Background(), "openai", srv.URL, "test-key", dir, srv.Client())
	require.NoError(t, err)
	assert.Equal(t, 0, callCount, "server must not be called for a fresh cache")
	require.Len(t, models, 1)
	assert.Equal(t, "cached-model", models[0].ID)
}

func TestFetchOrCacheModels_RefetchesExpiredCache(t *testing.T) {
	dir := t.TempDir()
	cacheFile := filepath.Join(dir, "provider-models-cache-openai.json")
	cached := []sec.LLMModelConfig{{ID: "stale-model"}}
	data, _ := json.Marshal(cached)
	require.NoError(t, os.WriteFile(cacheFile, data, 0o600))

	// Back-date the cache file beyond the TTL.
	staleTime := time.Now().Add(-(providerModelCacheTTL + time.Minute))
	require.NoError(t, os.Chtimes(cacheFile, staleTime, staleTime))

	srv := fakeModelsServer(t, []string{"fresh-model"})
	defer srv.Close()

	models, err := fetchOrCacheModels(context.Background(), "openai", srv.URL, "test-key", dir, srv.Client())
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.Equal(t, "fresh-model", models[0].ID, "stale cache must be refreshed")
}

func TestFetchOrCacheModels_CorruptCacheForcesRefetch(t *testing.T) {
	dir := t.TempDir()
	cacheFile := filepath.Join(dir, "provider-models-cache-openai.json")
	require.NoError(t, os.WriteFile(cacheFile, []byte("{not valid json"), 0o600))

	srv := fakeModelsServer(t, []string{"fresh-model"})
	defer srv.Close()

	models, err := fetchOrCacheModels(context.Background(), "openai", srv.URL, "test-key", dir, srv.Client())
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.Equal(t, "fresh-model", models[0].ID)
}

func TestFetchOrCacheModels_SanitizesProviderNameInCacheFilepath(t *testing.T) {
	// A provider name with path-traversal characters must produce a safe cache
	// filename — the sanitized name must stay within cacheDir.
	dir := t.TempDir()

	srv := fakeModelsServer(t, []string{"model-x"})
	defer srv.Close()

	// Provider name with ../ path traversal attempt
	_, err := fetchOrCacheModels(context.Background(), "../../evil", srv.URL, "test-key", dir, srv.Client())
	require.NoError(t, err)

	// The cache file must be inside dir, not escaped via traversal.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.True(t, strings.HasPrefix(entries[0].Name(), "provider-models-cache-"),
		"cache filename must start with provider-models-cache-")
	assert.NotContains(t, entries[0].Name(), "..", "sanitized filename must not contain ..")
}

// --- enrichProviderModels ---

func TestEnrichProviderModels_PopulatesModelsFromEndpoint(t *testing.T) {
	srv := fakeModelsServer(t, []string{"gpt-5.4", "deepseek-v3-chat", "glm-5.1"})
	defer srv.Close()

	dir := t.TempDir()
	providers := []sec.LLMProviderData{
		{Provider: "openai", APIKey: "test-key", BaseURL: srv.URL, Models: nil},
	}

	fn := enrichProviderModels(context.Background(), dir, srv.Client())
	out := fn(providers)

	require.Len(t, out, 1)
	assert.Len(t, out[0].Models, 3)
	assert.Equal(t, "gpt-5.4", out[0].Models[0].ID)
}

func TestEnrichProviderModels_SkipsProviderWithNoBaseURL(t *testing.T) {
	dir := t.TempDir()
	providers := []sec.LLMProviderData{
		{Provider: "anthropic", APIKey: "test-key", BaseURL: "", Models: nil},
	}

	callCount := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		callCount++
		return nil, nil
	})}

	fn := enrichProviderModels(context.Background(), dir, client)
	out := fn(providers)

	assert.Equal(t, 0, callCount, "no HTTP call for provider without BaseURL")
	assert.Empty(t, out[0].Models)
}

func TestEnrichProviderModels_SkipsProviderWithExistingModels(t *testing.T) {
	dir := t.TempDir()
	existingModels := []sec.LLMModelConfig{{ID: "allow-listed-model"}}
	providers := []sec.LLMProviderData{
		{Provider: "openai", APIKey: "test-key", BaseURL: "https://example.com/v1", Models: existingModels},
	}

	callCount := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		callCount++
		return nil, nil
	})}

	fn := enrichProviderModels(context.Background(), dir, client)
	out := fn(providers)

	assert.Equal(t, 0, callCount, "no HTTP call when models are already populated")
	assert.Equal(t, existingModels, out[0].Models, "existing models must be preserved")
}

func TestEnrichProviderModels_FetchFailIsBestEffort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	providers := []sec.LLMProviderData{
		{Provider: "openai", APIKey: "test-key", BaseURL: srv.URL, Models: nil},
	}

	fn := enrichProviderModels(context.Background(), dir, srv.Client())
	out := fn(providers)

	// Fetch failure must NOT be fatal — provider is returned with empty Models.
	require.Len(t, out, 1)
	assert.Empty(t, out[0].Models, "failed fetch must leave Models empty (best-effort)")
}

func TestEnrichProviderModels_MultipleProviders(t *testing.T) {
	// Each provider has its own server to avoid conflating auth headers.
	type entry struct {
		ID string `json:"id"`
	}
	type resp struct {
		Data []entry `json:"data"`
	}
	makeSrv := func(models []string, expectedKey string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer "+expectedKey, r.Header.Get("Authorization"))
			entries := make([]entry, len(models))
			for i, m := range models {
				entries[i] = entry{ID: m}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp{Data: entries})
		}))
	}
	srv1 := makeSrv([]string{"model-a", "model-b"}, "key1")
	defer srv1.Close()
	srv2 := makeSrv([]string{"model-c"}, "key2")
	defer srv2.Close()

	dir := t.TempDir()
	providers := []sec.LLMProviderData{
		{Provider: "provider1", APIKey: "key1", BaseURL: srv1.URL},
		{Provider: "provider2", APIKey: "key2", BaseURL: srv2.URL},
		{Provider: "provider3", APIKey: "key3", BaseURL: ""}, // no BaseURL — skipped
	}

	// Use srv1's client (both are plain httptest servers — same transport).
	fn := enrichProviderModels(context.Background(), dir, srv1.Client())
	out := fn(providers)

	assert.Len(t, out[0].Models, 2)
	assert.Len(t, out[1].Models, 1)
	assert.Empty(t, out[2].Models)
}

// roundTripFunc is a helper to build a custom http.RoundTripper from a function.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
