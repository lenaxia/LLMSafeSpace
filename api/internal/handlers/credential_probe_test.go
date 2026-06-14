// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupProbeAnonRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v1/probe-models", ProbeModelsAnon)
	return r
}

// TestProbeModelsAnon_MissingFields verifies 400 when apiKey or baseURL absent.
func TestProbeModelsAnon_MissingFields(t *testing.T) {
	router := setupProbeAnonRouter()

	for _, body := range []string{
		`{"baseURL":"https://api.example.com/v1"}`, // missing apiKey
		`{"apiKey":"sk-test"}`,                     // missing baseURL
		`{}`,                                       // both missing
	} {
		req, _ := http.NewRequest("POST", "/api/v1/probe-models", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", body)
	}
}

// TestProbeModelsAnon_SSRFRejected verifies that private/internal baseURLs
// are rejected with 400 before any outbound request is made.
func TestProbeModelsAnon_SSRFRejected(t *testing.T) {
	router := setupProbeAnonRouter()

	for _, tc := range []struct {
		name    string
		baseURL string
	}{
		{"loopback-ip", "http://127.0.0.1/v1"},
		{"loopback-localhost", "http://localhost/v1"},
		{"private-10x", "https://10.0.0.1/v1"},
		{"private-192168", "https://192.168.1.1/v1"},
		{"link-local", "http://169.254.169.254/v1"},
		{"file-scheme", "file:///etc/passwd"},
		{"internal-hostname", "https://metadata.internal/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{
				"apiKey":  "sk-test",
				"baseURL": tc.baseURL,
			})
			req, _ := http.NewRequest("POST", "/api/v1/probe-models", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code, "SSRF URL %q must be rejected with 400", tc.baseURL)
		})
	}
}

// TestProbeModelsAnon_Success verifies the probe function directly when
// calling a reachable provider. The handler is not used here because
// httptest.NewServer binds to 127.0.0.1 which is blocked by the SSRF guard
// in ProbeModelsAnon. The SSRF guard is tested separately via
// TestProbeModelsAnon_SSRFRejected and TestValidateProbeBaseURL_PrivateRanges.
func TestProbeModelsAnon_Success(t *testing.T) {
	fakeProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		assert.Equal(t, "Bearer sk-probe-test", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-5.1"},{"id":"glm-5.2"}]}`))
	}))
	defer fakeProvider.Close()

	// Call probeCredentialModels directly (bypasses SSRF — intentional for unit test).
	pd := struct {
		APIKey  string `json:"apiKey"`
		BaseURL string `json:"baseURL"`
	}{APIKey: "sk-probe-test", BaseURL: fakeProvider.URL + "/v1"}
	plaintext, _ := json.Marshal(pd)
	resp := probeCredentialModels(context.Background(), plaintext, nil)

	assert.Empty(t, resp.Warning)
	require.Len(t, resp.Models, 2)
	assert.Equal(t, "glm-5.1", resp.Models[0].ID)
	assert.Equal(t, "glm-5.2", resp.Models[1].ID)
}

// TestProbeModelsAnon_ProviderError verifies graceful handling when the provider
// returns an error — must return a warning, not panic.
func TestProbeModelsAnon_ProviderError(t *testing.T) {
	fakeProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":"invalid key"}`)
	}))
	defer fakeProvider.Close()

	// Call probeCredentialModels directly (bypasses SSRF — intentional for unit test).
	pd := struct {
		APIKey  string `json:"apiKey"`
		BaseURL string `json:"baseURL"`
	}{APIKey: "sk-bad", BaseURL: fakeProvider.URL + "/v1"}
	plaintext, _ := json.Marshal(pd)
	resp := probeCredentialModels(context.Background(), plaintext, nil)

	assert.NotEmpty(t, resp.Warning, "provider error must produce a warning")
	assert.Empty(t, resp.Models, "no models expected on provider error")
}

// TestValidateProbeBaseURL_PrivateRanges exercises the SSRF validation function
// directly against the known private ranges.
func TestValidateProbeBaseURL_PrivateRanges(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1/v1",
		"http://127.0.0.2/v1",
		"http://localhost/v1",
		"https://10.0.0.1/v1",
		"https://10.255.255.255/v1",
		"https://172.16.0.1/v1",
		"https://172.31.255.255/v1",
		"https://192.168.0.1/v1",
		"https://169.254.1.1/v1",
		"https://100.64.0.1/v1",
		"file:///etc/passwd",
		"ftp://files.example.com",
		"http://api.local/v1",
		"https://metadata.internal/v1",
	}
	for _, u := range blocked {
		assert.Error(t, validateProbeBaseURL(u), "expected %q to be blocked", u)
	}

	allowed := []string{
		"https://api.openai.com/v1",
		"https://ai.thekao.cloud/v1",
		"https://litellm.example.com/v1",
	}
	for _, u := range allowed {
		assert.NoError(t, validateProbeBaseURL(u), "expected %q to be allowed", u)
	}
}
