// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package freemodels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeModelsDevServer returns an httptest.Server that serves a
// models.dev-shaped response with one paid model, two free models, and
// a model whose ID is implicit (encoded as the map key).
func fakeModelsDevServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"opencode": {
				"models": {
					"paid-model": {"id": "paid-model", "name": "Paid", "cost": {"input": 0.5, "output": 1.0}, "limit": {"context": 1000, "output": 500}},
					"free-a": {"id": "free-a", "name": "Free A", "cost": {"input": 0, "output": 0}, "limit": {"context": 200000, "output": 32000}},
					"free-b": {"id": "free-b", "name": "Free B", "cost": {"input": 0, "output": 1.5}, "limit": {"context": 1048576, "output": 64000}},
					"implicit-id": {"name": "Implicit", "cost": {"input": 0, "output": 0}, "limit": {"context": 100000, "output": 10000}}
				}
			},
			"openai": {"models": {"gpt-4": {"id": "gpt-4", "cost": {"input": 0.03}}}}
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetcher_FiltersToFreeOpenCodeModels(t *testing.T) {
	srv := fakeModelsDevServer(t)
	f := &Fetcher{URL: srv.URL}

	models, err := f.Fetch(context.Background())
	require.NoError(t, err)

	// Expect: free-a, free-b, implicit-id (sorted). paid-model excluded
	// because cost.input != 0. The openai entry is excluded because we
	// filter to opencode-provider models only.
	require.Len(t, models, 3, "must include exactly the 3 free-tier opencode models")
	assert.Equal(t, "free-a", models[0].ID)
	assert.Equal(t, "free-b", models[1].ID)
	assert.Equal(t, "implicit-id", models[2].ID,
		"model with empty 'id' field must fall back to map key as ID")
}

func TestFetcher_StableSort(t *testing.T) {
	srv := fakeModelsDevServer(t)
	f := &Fetcher{URL: srv.URL}

	first, err := f.Fetch(context.Background())
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		again, err := f.Fetch(context.Background())
		require.NoError(t, err)
		assert.Equal(t, first, again,
			"successive fetches must produce identical slices — stable sort is required so SyncConfigMap can fast-path no-change refreshes")
	}
}

func TestFetcher_PreservesContextAndOutputLimits(t *testing.T) {
	srv := fakeModelsDevServer(t)
	f := &Fetcher{URL: srv.URL}

	models, err := f.Fetch(context.Background())
	require.NoError(t, err)

	// free-a: context=200000, output=32000.
	for _, m := range models {
		if m.ID == "free-a" {
			assert.Equal(t, 200000, m.ContextLimit, "ContextLimit must round-trip from limit.context")
			assert.Equal(t, 32000, m.OutputLimit, "OutputLimit must round-trip from limit.output")
			return
		}
	}
	t.Fatal("free-a not found in result")
}

func TestFetcher_OpenCodeAbsent_ReturnsEmptyNoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"openai": {"models": {}}}`))
	}))
	t.Cleanup(srv.Close)

	f := &Fetcher{URL: srv.URL}
	models, err := f.Fetch(context.Background())
	require.NoError(t, err,
		"missing opencode entry is treated as transient — empty list, no error, so the refresher keeps the existing CM")
	assert.Empty(t, models)
}

func TestFetcher_NoFreeModels_ReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"opencode": {
				"models": {
					"paid-only": {"id": "paid-only", "cost": {"input": 0.5}}
				}
			}
		}`))
	}))
	t.Cleanup(srv.Close)

	f := &Fetcher{URL: srv.URL}
	models, err := f.Fetch(context.Background())
	require.NoError(t, err)
	assert.Empty(t, models, "all paid models means no free tier available; empty result is correct")
}

func TestFetcher_HTTP500_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	t.Cleanup(srv.Close)

	f := &Fetcher{URL: srv.URL}
	_, err := f.Fetch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500", "error must surface the HTTP status code for diagnostics")
}

func TestFetcher_MalformedJSON_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{ this is not json`))
	}))
	t.Cleanup(srv.Close)

	f := &Fetcher{URL: srv.URL}
	_, err := f.Fetch(context.Background())
	require.Error(t, err)
}

func TestFetcher_RespectsContextCancellation(t *testing.T) {
	// Server hangs forever to force ctx-deadline-exceeded.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	f := &Fetcher{URL: srv.URL, HTTPClient: &http.Client{}}
	_, err := f.Fetch(ctx)
	require.Error(t, err, "canceled context must surface as an error rather than blocking")
}

// TestFetcher_DefaultURL verifies that an empty Fetcher.URL falls back
// to ModelsDevAPIURL. We don't actually make a network call here —
// just exercise the fallback path with an empty client (which will
// fail quickly on DNS but only after the URL is constructed).
func TestFetcher_DefaultURL(t *testing.T) {
	f := &Fetcher{HTTPClient: &http.Client{}}
	// Canceled context guarantees a fast failure without hitting the network.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.Fetch(ctx)
	require.Error(t, err)
	// URL fallback is observable via the wrapped error string.
	assert.Contains(t, err.Error(), "models.dev",
		"empty Fetcher.URL must fall back to ModelsDevAPIURL")
}

// TestCatalog_JSONShape pins the wire format that agentd's materialize
// subcommand consumes. Field name changes here REQUIRE coordinated
// changes in cmd/workspace-agentd; this test guards against accidental
// rename.
func TestCatalog_JSONShape(t *testing.T) {
	c := Catalog{
		Models: []Model{
			{ID: "m1", Name: "Model One", ContextLimit: 1000, OutputLimit: 500},
		},
		Source: "https://models.dev/api.json",
	}
	out, err := json.Marshal(c)
	require.NoError(t, err)

	var roundtrip map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &roundtrip))
	require.Contains(t, roundtrip, "models")
	require.Contains(t, roundtrip, "fetched_at")
	require.Contains(t, roundtrip, "source")

	var modelsOut []map[string]any
	require.NoError(t, json.Unmarshal(roundtrip["models"], &modelsOut))
	require.Len(t, modelsOut, 1)
	assert.Contains(t, modelsOut[0], "id")
	assert.Contains(t, modelsOut[0], "name")
	assert.Contains(t, modelsOut[0], "context_limit")
	assert.Contains(t, modelsOut[0], "output_limit")
}
