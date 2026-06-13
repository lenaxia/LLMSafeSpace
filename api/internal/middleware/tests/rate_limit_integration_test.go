// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package tests

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lenaxia/llmsafespace/api/internal/middleware"
	apilogger "github.com/lenaxia/llmsafespace/api/internal/logger"
	"github.com/lenaxia/llmsafespace/api/internal/services/ratelimit"
	logmock "github.com/lenaxia/llmsafespace/mocks/logger"
)

// newRealRateLimitService builds a real ratelimit.Service backed by a miniredis
// instance so tests can exercise the full middleware → Service → Redis path
// without mocks.
func newRealRateLimitService(t *testing.T) (*ratelimit.Service, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	log, _ := apilogger.New(true, "error", "console")
	svc := ratelimit.NewWithRedisClient(log, client)
	return svc, func() {
		_ = svc.Stop()
		_ = client.Close()
		mr.Close()
	}
}

func rateLimitTestLogger() *logmock.MockLogger {
	l := logmock.NewMockLogger()
	l.On("Warn", mock.Anything, mock.Anything, mock.Anything).Maybe()
	l.On("Error", mock.Anything, mock.Anything, mock.Anything).Maybe()
	return l
}

// TestRateLimitIntegration_FixedWindow drives the real rate limiter (with a
// miniredis backend) through the HTTP middleware end-to-end: requests under
// the limit pass, and the request that crosses the limit receives 429.
func TestRateLimitIntegration_FixedWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc, cleanup := newRealRateLimitService(t)
	defer cleanup()

	config := middleware.RateLimitConfig{
		Enabled:       true,
		DefaultLimit:  3,
		DefaultWindow: time.Minute,
		Strategy:      "fixed_window",
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("apiKey", "integration-fixed")
		c.Next()
	})
	router.Use(middleware.RateLimitMiddleware(svc, rateLimitTestLogger(), config, nil))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d should pass", i+1)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "request over the limit should be rejected")
	assert.Equal(t, "3", w.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "0", w.Header().Get("X-RateLimit-Remaining"))
	assert.NotEmpty(t, w.Header().Get("X-RateLimit-Reset"))
}

// TestRateLimitIntegration_SlidingWindow exercises the sliding-window strategy
// (sorted-set add/prune/count) end-to-end through the middleware and a real
// Redis backend.
func TestRateLimitIntegration_SlidingWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc, cleanup := newRealRateLimitService(t)
	defer cleanup()

	config := middleware.RateLimitConfig{
		Enabled:       true,
		DefaultLimit:  3,
		DefaultWindow: time.Minute,
		Strategy:      "sliding_window",
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("apiKey", "integration-sliding")
		c.Next()
	})
	router.Use(middleware.RateLimitMiddleware(svc, rateLimitTestLogger(), config, nil))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/test", nil)
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d should pass", i+1)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "request over the limit should be rejected")
	assert.Equal(t, "3", w.Header().Get("X-RateLimit-Limit"))
}
