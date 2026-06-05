// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package relaycftest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_WorkerProxy_FullChain simulates the complete relay path:
// opencode (client) → Worker (proxy) → upstream provider (fake)
//
// This validates:
// - Request path is forwarded correctly
// - Authorization header passes through
// - Request body passes through
// - Response body streams back unmodified
// - Response status code passes through
func TestE2E_WorkerProxy_FullChain(t *testing.T) {
	// 1. Fake upstream (simulates opencode.ai/zen/v1)
	var capturedReq *http.Request
	var capturedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "test-123",
			"object":  "chat.completion",
			"choices": []map[string]string{{"message": "hello"}},
		})
	}))
	defer upstream.Close()

	// 2. Simulated Worker (same logic as workers/inference-relay/src/index.ts)
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Worker logic: proxy to upstream with path appended
		target := upstream.URL + r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		proxyReq, _ := http.NewRequest(r.Method, target, r.Body)
		proxyReq.Header = r.Header.Clone()

		resp, err := http.DefaultClient.Do(proxyReq)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()

		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	defer worker.Close()

	// 3. Simulate opencode calling the Worker (as it would with baseURL override)
	reqBody := `{"model":"deepseek-v4-flash-free","input":[{"role":"user","content":"hi"}],"max_tokens":5}`
	req, err := http.NewRequest("POST", worker.URL+"/responses", strings.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer public")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// 4. Validate the chain
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Upstream received correct path
	assert.Equal(t, "/responses", capturedReq.URL.Path)

	// Upstream received auth header (passthrough)
	assert.Equal(t, "Bearer public", capturedReq.Header.Get("Authorization"))

	// Upstream received body
	assert.Equal(t, reqBody, capturedBody)

	// Response body came back
	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, "test-123", result["id"])
}

// TestE2E_WorkerProxy_StreamingResponse verifies chunked/streaming responses
// pass through correctly (LLM inference responses are typically streamed).
func TestE2E_WorkerProxy_StreamingResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			w.Write([]byte("data: {\"chunk\":" + string(rune('0'+i)) + "}\n\n"))
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := upstream.URL + r.URL.Path
		proxyReq, _ := http.NewRequest(r.Method, target, r.Body)
		proxyReq.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(proxyReq)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	defer worker.Close()

	resp, err := http.DefaultClient.Post(worker.URL+"/responses", "application/json",
		strings.NewReader(`{"model":"test","stream":true}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "data: {\"chunk\":0}")
	assert.Contains(t, string(body), "data: {\"chunk\":1}")
	assert.Contains(t, string(body), "data: {\"chunk\":2}")
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
}

// TestE2E_WorkerProxy_ErrorPassthrough verifies upstream errors are not swallowed.
func TestE2E_WorkerProxy_ErrorPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer upstream.Close()

	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := upstream.URL + r.URL.Path
		proxyReq, _ := http.NewRequest(r.Method, target, r.Body)
		proxyReq.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(proxyReq)
		if err != nil || resp == nil {
			http.Error(w, "proxy error", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	defer worker.Close()

	resp, err := http.DefaultClient.Post(worker.URL+"/responses", "application/json",
		strings.NewReader(`{"model":"test"}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	// Error status and body must pass through unchanged
	assert.Equal(t, 429, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "rate limited")
}

// TestE2E_WorkerProxy_QueryParams verifies query parameters forward correctly.
func TestE2E_WorkerProxy_QueryParams(t *testing.T) {
	var capturedQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := upstream.URL + r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		proxyReq, _ := http.NewRequest(r.Method, target, r.Body)
		resp, err := http.DefaultClient.Do(proxyReq)
		if err != nil || resp == nil {
			http.Error(w, "proxy error", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
	}))
	defer worker.Close()

	http.Get(worker.URL + "/models?limit=10&offset=0")
	assert.Equal(t, "limit=10&offset=0", capturedQuery)
}
