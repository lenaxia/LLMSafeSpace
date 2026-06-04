// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package handlers

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/pkg/relay"
)

// RelayFallbackHandler handles direct server-side HTTP calls when the client
// reports CORS errors (US-26.6). Rate-limited per user.
type RelayFallbackHandler struct {
	client     *http.Client
	mu         sync.Mutex
	userCounts map[string]*fallbackCounter
}

type fallbackCounter struct {
	count     int
	windowEnd time.Time
}

// NewRelayFallbackHandler creates a fallback handler with rate limiting.
func NewRelayFallbackHandler() *RelayFallbackHandler {
	return &RelayFallbackHandler{
		client:     &http.Client{Timeout: 60 * time.Second},
		userCounts: make(map[string]*fallbackCounter),
	}
}

// HandleFallback proxies a request server-side when the client can't due to CORS.
// POST /api/v1/workspaces/:id/relay/fallback
func (h *RelayFallbackHandler) HandleFallback(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid, _ := userID.(string)
	if uid == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	// Rate limit check
	if !h.allowRequest(uid) {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": "fallback rate limit exceeded",
			"limit": relay.FallbackRateLimitPerMinute,
		})
		return
	}

	var req struct {
		Method  string            `json:"method" binding:"required"`
		URL     string            `json:"url" binding:"required"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Validate target host is allowed
	if !isAllowedFallbackURL(req.URL) {
		c.JSON(http.StatusForbidden, gin.H{"error": "target host not allowed for fallback"})
		return
	}

	// Make the HTTP request server-side
	var body io.Reader
	if req.Body != "" {
		body = strings.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(c.Request.Context(), req.Method, req.URL, body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters"})
		return
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := h.client.Do(httpReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream request failed"})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Stream the response back
	for k, vals := range resp.Header {
		for _, v := range vals {
			c.Header(k, v)
		}
	}
	c.Status(resp.StatusCode)
	_, _ = io.Copy(c.Writer, resp.Body)
}

// allowRequest checks the per-user rate limit for fallback requests.
func (h *RelayFallbackHandler) allowRequest(userID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()

	// Periodic cleanup: remove expired entries (bounded memory)
	if len(h.userCounts) > 100 {
		for k, v := range h.userCounts {
			if now.After(v.windowEnd) {
				delete(h.userCounts, k)
			}
		}
	}

	counter, ok := h.userCounts[userID]
	if !ok || now.After(counter.windowEnd) {
		h.userCounts[userID] = &fallbackCounter{
			count:     1,
			windowEnd: now.Add(time.Minute),
		}
		return true
	}

	if counter.count >= relay.FallbackRateLimitPerMinute {
		return false
	}
	counter.count++
	return true
}

// isAllowedFallbackURL validates the target URL is in the allowed host list.
func isAllowedFallbackURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	host := parsed.Hostname() // strips port
	return relay.IsAllowedHost(host)
}
