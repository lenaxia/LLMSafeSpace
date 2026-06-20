// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package relaycftest validates the assumptions Epic 26 relies on:
// - opencode provider catalog format (models.dev/api.json)
// - opencode.ai/zen/v1 endpoint behavior
//
// These tests hit the real opencode.ai API (no mocks) and will fail
// if opencode changes their provider format, endpoint paths, or auth.
// Run with: go test -tags=integration ./tests/epic26/
package relaycftest

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// TestModelsCatalog_OpencodeProviderExists verifies the opencode provider
// is present in models.dev/api.json with the expected structure.
func TestModelsCatalog_OpencodeProviderExists(t *testing.T) {
	resp, err := httpClient.Get("https://models.dev/api.json")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)

	var catalog map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&catalog))

	_, ok := catalog["opencode"]
	require.True(t, ok, "opencode provider must exist in models.dev/api.json")
}

// TestModelsCatalog_OpencodeAPIField verifies the opencode provider's
// api field is opencode.ai/zen/v1 (our Worker's UPSTREAM_URL target).
func TestModelsCatalog_OpencodeAPIField(t *testing.T) {
	resp, err := httpClient.Get("https://models.dev/api.json")
	require.NoError(t, err)
	defer resp.Body.Close()

	var catalog map[string]struct {
		API string `json:"api"`
		NPM string `json:"npm"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&catalog))

	oc, ok := catalog["opencode"]
	require.True(t, ok)
	assert.Equal(t, "https://opencode.ai/zen/v1", oc.API,
		"opencode provider API URL changed — Worker UPSTREAM_URL must be updated")
	assert.Equal(t, "@ai-sdk/openai-compatible", oc.NPM,
		"opencode provider npm package changed — endpoint path format may differ")
}

// TestModelsCatalog_FreeModelsExist verifies at least one free model exists.
func TestModelsCatalog_FreeModelsExist(t *testing.T) {
	resp, err := httpClient.Get("https://models.dev/api.json")
	require.NoError(t, err)
	defer resp.Body.Close()

	var catalog map[string]struct {
		Models map[string]struct {
			Cost json.RawMessage `json:"cost"`
		} `json:"models"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&catalog))

	oc := catalog["opencode"]
	freeCount := 0
	for _, m := range oc.Models {
		var cost struct {
			Input float64 `json:"input"`
		}
		if json.Unmarshal(m.Cost, &cost) == nil && cost.Input == 0 {
			freeCount++
		}
	}
	assert.Greater(t, freeCount, 0, "no free models found — Epic 26 relay has no target models")
	t.Logf("%d free models available", freeCount)
}

// TestOpencodeZenV1_Reachable verifies the inference endpoint is up.
func TestOpencodeZenV1_Reachable(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://opencode.ai/zen/v1/chat/completions", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer public")
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	// 400 (bad request body) or 200 both confirm the endpoint exists
	// 404 would mean the path changed
	assert.NotEqual(t, 404, resp.StatusCode,
		"opencode.ai/zen/v1/chat/completions returned 404 — endpoint path changed")
}

// TestOpencodeZenV1_ResponsesEndpoint verifies the /responses path works
// (opencode uses OpenAI Responses API format, not Chat Completions).
func TestOpencodeZenV1_ResponsesEndpoint(t *testing.T) {
	body := `{"model":"deepseek-v4-flash-free","input":[{"role":"user","content":"say hi"}],"max_tokens":5}`
	req, _ := http.NewRequest("POST", "https://opencode.ai/zen/v1/responses",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer public")

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Accept 200 (success) or model-specific errors (model disabled etc)
	// Reject 404 (path changed) or 401 (auth format changed)
	assert.NotEqual(t, 404, resp.StatusCode,
		"opencode.ai/zen/v1/responses returned 404 — Responses API path changed")
	assert.NotEqual(t, 401, resp.StatusCode,
		"Bearer public no longer accepted — auth mechanism changed")
}

// TestOpencodeZenV1_BearerPublicAccepted verifies "Bearer public" auth works.
func TestOpencodeZenV1_BearerPublicAccepted(t *testing.T) {
	body := `{"model":"deepseek-v4-flash-free","input":[{"role":"user","content":"1+1"}],"max_tokens":5}`
	req, _ := http.NewRequest("POST", "https://opencode.ai/zen/v1/responses",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer public")

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	// Must not be 401/403 — Bearer public is the free-tier mechanism.
	// NOTE (2026-06-20): Zen gates free-model inference on a per-model
	// `allowAnonymous` flag (opencode handler.ts:599-603 + model.ts:26), not on
	// the key or source IP. This test pins a model known to be allowAnonymous
	// (deepseek-v4-flash-free). A 401 here means EITHER Zen retired this
	// model's allowAnonymous flag OR the key path changed — re-probe before
	// treating as a key death (A23 was the false-positive version of this).
	assert.NotEqual(t, 401, resp.StatusCode,
		"Bearer public returned 401 — either this model lost allowAnonymous or opencode changed the public-key path")
	assert.NotEqual(t, 403, resp.StatusCode,
		"Bearer public returned 403 — opencode blocked public key")

	// If 200, verify response has expected shape
	if resp.StatusCode == 200 {
		var result map[string]interface{}
		require.NoError(t, json.Unmarshal(respBody, &result))
		assert.Contains(t, result, "id", "response missing 'id' field")
	}
}

// TestOpencodeZenV1_NoCORSHeaders confirms CORS is still absent
// (documents why we need the CF Worker rather than direct browser calls).
func TestOpencodeZenV1_NoCORSHeaders(t *testing.T) {
	req, _ := http.NewRequest("OPTIONS", "https://opencode.ai/zen/v1/responses", nil)
	req.Header.Set("Origin", "https://safespaces.dev")
	req.Header.Set("Access-Control-Request-Method", "POST")

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	acao := resp.Header.Get("Access-Control-Allow-Origin")
	if acao != "" {
		t.Logf("WARNING: opencode.ai now returns CORS headers (%s) — CF Worker may no longer be necessary", acao)
	} else {
		t.Log("Confirmed: no CORS headers — CF Worker relay is still required")
	}
}
