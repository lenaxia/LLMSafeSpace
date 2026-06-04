// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/pkg/relay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayFallback_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRelayFallbackHandler()

	router := gin.New()
	router.POST("/fallback", func(c *gin.Context) {
		// No userID set
		h.HandleFallback(c)
	})

	body := `{"method":"GET","url":"https://opencode.ai/v1/models"}`
	req := httptest.NewRequest(http.MethodPost, "/fallback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRelayFallback_DisallowedHost(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRelayFallbackHandler()

	router := gin.New()
	router.POST("/fallback", func(c *gin.Context) {
		c.Set("userID", "user1")
		h.HandleFallback(c)
	})

	body := `{"method":"GET","url":"https://evil.com/steal-data"}`
	req := httptest.NewRequest(http.MethodPost, "/fallback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestRelayFallback_HappyPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Fake upstream
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models":["test"]}`))
	}))
	defer upstream.Close()

	// Temporarily allow the test server's host
	originalHosts := relay.AllowedProxyHosts
	host := strings.TrimPrefix(upstream.URL, "http://")
	hostWithoutPort := strings.Split(host, ":")[0]
	relay.AllowedProxyHosts = append(relay.AllowedProxyHosts, hostWithoutPort)
	defer func() { relay.AllowedProxyHosts = originalHosts }()

	h := NewRelayFallbackHandler()

	router := gin.New()
	router.POST("/fallback", func(c *gin.Context) {
		c.Set("userID", "user1")
		h.HandleFallback(c)
	})

	reqBody, _ := json.Marshal(map[string]interface{}{
		"method":  "GET",
		"url":     upstream.URL + "/v1/models",
		"headers": map[string]string{"accept": "application/json"},
	})
	req := httptest.NewRequest(http.MethodPost, "/fallback", strings.NewReader(string(reqBody)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"models"`)
}

func TestRelayFallback_RateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	originalHosts := relay.AllowedProxyHosts
	host := strings.TrimPrefix(upstream.URL, "http://")
	hostWithoutPort := strings.Split(host, ":")[0]
	relay.AllowedProxyHosts = append(relay.AllowedProxyHosts, hostWithoutPort)
	defer func() { relay.AllowedProxyHosts = originalHosts }()

	h := NewRelayFallbackHandler()

	router := gin.New()
	router.POST("/fallback", func(c *gin.Context) {
		c.Set("userID", "user1")
		h.HandleFallback(c)
	})

	// Exhaust the rate limit
	for i := 0; i < relay.FallbackRateLimitPerMinute; i++ {
		reqBody := `{"method":"GET","url":"` + upstream.URL + `/test"}`
		req := httptest.NewRequest(http.MethodPost, "/fallback", strings.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "request %d should succeed", i)
	}

	// Next request should be rate limited
	reqBody := `{"method":"GET","url":"` + upstream.URL + `/test"}`
	req := httptest.NewRequest(http.MethodPost, "/fallback", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestRelayFallback_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRelayFallbackHandler()

	router := gin.New()
	router.POST("/fallback", func(c *gin.Context) {
		c.Set("userID", "user1")
		h.HandleFallback(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/fallback", strings.NewReader(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestIsAllowedFallbackURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"opencode.ai allowed", "https://opencode.ai/v1/chat/completions", true},
		{"api.opencode.ai allowed", "https://api.opencode.ai/v1/models", true},
		{"evil.com blocked", "https://evil.com/steal", false},
		{"no scheme blocked", "opencode.ai/v1/models", false},
		{"with port allowed", "https://opencode.ai:443/v1/models", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isAllowedFallbackURL(tt.url))
		})
	}
}
